package store

import (
	"aulaglobal-mcp/internal/aulaglobal"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestSanitizeAndCollisions(t *testing.T) {
	for _, s := range []string{"../../etc/passwd", "\\..\\evil", "..hidden", "NUL", "CON.txt", "a\x00\nb", strings.Repeat("x", 300)} {
		got := Sanitize(s)
		if strings.ContainsAny(got, "/\\\x00\n") || strings.Contains(got, "..") || strings.HasPrefix(got, ".") || len(got) > 101 {
			t.Fatalf("unsafe %q", got)
		}
	}
	a := aulaglobal.File{CourseID: 1, ModuleID: 1, Filename: "a.txt", Handle: "one"}
	b := a
	b.ModuleID = 2
	if Path(a) != Path(b) {
		t.Fatal("base paths should be readable before manifest disambiguation")
	}
	b = a
	b.Handle = "two"
	if Path(a) != Path(b) {
		t.Fatal("manifest must disambiguate")
	}
}
func TestManyDownloads(t *testing.T) {
	var active, peak atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := active.Add(1)
		defer active.Add(-1)
		for p := peak.Load(); n > p; p = peak.Load() {
			if peak.CompareAndSwap(p, n) {
				break
			}
		}
		time.Sleep(time.Millisecond)
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprint(w, "hello")
	}))
	defer ts.Close()
	s, e := Open(t.TempDir(), 1024, 4)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Root.Close()
	c := aulaglobal.New(ts.URL, aulaglobal.StaticToken{Value: "secret"})
	files := []aulaglobal.File{}
	size := int64(5)
	for i := 0; i < 200; i++ {
		files = append(files, aulaglobal.File{CourseID: 1, ModuleID: 1, Filename: "../../a.txt", Handle: fmt.Sprint(i), URL: ts.URL + "/file", Size: &size, Modified: 1700000000})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, r := range dl(s, ctx, c, files) {
		if r.Status != "downloaded" {
			t.Fatalf("%+v", r)
		}
	}
	if peak.Load() > 4 {
		t.Fatal("unbounded concurrency")
	}
	for _, r := range dl(s, ctx, c, files) {
		if r.Status != "skipped" {
			t.Fatalf("%+v", r)
		}
	}
	cancel()
	if r := dl(s, ctx, c, files[:1])[0]; r.Status != "error" {
		t.Fatal("cancellation ignored")
	}
}
func TestRejectBadDownloads(t *testing.T) {
	for _, tc := range []struct {
		typ, body string
		size      int64
	}{
		{"text/html", "<html>login", 11}, {"application/json", `{"errorcode":"invalidtoken","exception":"x"}`, 42}, {"text/plain", "short", 100},
	} {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", tc.typ)
			fmt.Fprint(w, tc.body)
		}))
		s, e := Open(t.TempDir(), 1024, 2)
		if e != nil {
			t.Fatal(e)
		}
		f := aulaglobal.File{Filename: "x.pdf", Handle: "x", URL: ts.URL, Size: &tc.size}
		defer s.Root.Close()
		defer ts.Close()
		r := dl(s, context.Background(), aulaglobal.New(ts.URL, aulaglobal.StaticToken{Value: "secret"}), []aulaglobal.File{f})[0]
		if r.Status != "error" {
			t.Fatalf("saved invalid file %+v", r)
		}
		if _, e = s.Root.Stat(r.Path); !os.IsNotExist(e) {
			t.Fatal("destination exists")
		}
		s.Root.Close()
		ts.Close()
	}
}
func TestRootConfinement(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("secret"), 0600)
	if e := os.Symlink(outside, filepath.Join(root, "escape")); e != nil {
		t.Skipf("symlink unavailable: %v", e)
	}
	s, e := Open(root, 1024, 1)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Root.Close()
	for _, p := range []string{"../secret.txt", filepath.Join(outside, "secret.txt"), "escape/secret.txt"} {
		if _, e := s.Read(context.Background(), p, 0, 100); e == nil {
			t.Fatalf("escaped via %s", p)
		}
	}
	if e = s.Root.MkdirAll("escape/new", 0700); e == nil {
		t.Fatal("write escape")
	}
	os.WriteFile(filepath.Join(root, "ok.txt"), []byte("alpha hello world"), 0600)
	matches, e := s.Search(context.Background(), "hello", false, 10)
	if e != nil || len(matches) != 1 {
		t.Fatalf("%v %v", matches, e)
	}
}

func TestReadingPagingAndSearchLimits(t *testing.T) {
	dir := t.TempDir()
	s, e := Open(dir, 1024, 1)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Root.Close()
	for name, content := range map[string]string{"unicode.txt": "ábc", "page.html": "<script>bad</script><p>hola &amp; mundo</p>", "file.pdf": "%PDF-fake", "large.txt": strings.Repeat("a", 10000)} {
		if e := os.WriteFile(filepath.Join(dir, name), []byte(content), 0600); e != nil {
			t.Fatal(e)
		}
	}
	ctx := context.Background()
	r, e := s.Read(ctx, "unicode.txt", 0, 2)
	if e != nil || r.Text != "á" || r.NextOffset != 2 {
		t.Fatalf("%+v %v", r, e)
	}
	if _, e = s.Read(ctx, "unicode.txt", 1, 2); e == nil {
		t.Fatal("accepted partial UTF-8 offset")
	}
	if _, e = s.Read(ctx, "unicode.txt", 0, 1); e == nil {
		t.Fatal("accepted non-progressing page")
	}
	r, e = s.Read(ctx, "page.html", 0, 100)
	if e != nil || strings.Contains(r.Text, "bad") || !strings.Contains(r.Text, "hola & mundo") {
		t.Fatalf("%+v %v", r, e)
	}
	r, e = s.Read(ctx, "file.pdf", 0, 100)
	if e != nil || r.Text != "" || r.Note == "" {
		t.Fatalf("%+v %v", r, e)
	}
	matches, e := s.Search(ctx, "a+", true, 100)
	if e != nil {
		t.Fatal(e)
	}
	for _, m := range matches {
		if len(m.Snippet) > 240 {
			t.Fatal("unbounded snippet")
		}
	}
}

// A manifest written by an older Windows build must also work on Unix.
func TestManifestPortablePaths(t *testing.T) {
	dir := t.TempDir()
	s, e := Open(dir, 1024, 1)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Root.Close()
	if e = s.atomic(".aulaglobal-cache/manifest.json", []byte(`{"h":{"path":"Course\\Section\\notes.txt","size":null,"timemodified":0}}`)); e != nil {
		t.Fatal(e)
	}
	paths, e := s.paths([]aulaglobal.File{{Handle: "h", CourseID: 1, Filename: "notes.txt"}})
	if e != nil || paths["h"] != "Course/Section/notes.txt" {
		t.Fatal(paths, e)
	}
	b, e := s.cacheRead(".aulaglobal-cache/manifest.json")
	if e != nil || strings.Contains(string(b), `\\`) {
		t.Fatal(string(b), e)
	}
}

func TestPortablePathRoundTrip(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "portable needle")
	}))
	defer ts.Close()
	s, err := Open(t.TempDir(), 1024, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Root.Close()
	f := aulaglobal.File{Handle: "portable", CourseID: 1, CourseName: "Course", Section: "Section", Filename: "notes.txt", URL: ts.URL}
	const want = "Course/Section/notes.txt"
	if got := Path(f); got != want {
		t.Fatalf("Path = %q, want %q", got, want)
	}
	result := dl(s, context.Background(), aulaglobal.New(ts.URL, aulaglobal.StaticToken{Value: "secret"}), []aulaglobal.File{f})[0]
	if result.Status != "downloaded" || result.Path != want {
		t.Fatalf("download: %+v", result)
	}
	for _, prefix := range []string{"", "Course/Section", want} {
		matches, err := s.Search(context.Background(), "needle", false, 10, prefix)
		if err != nil || len(matches) != 1 || matches[0].Path != want {
			t.Fatalf("search prefix %q: %+v, %v", prefix, matches, err)
		}
		r, err := s.Read(context.Background(), matches[0].Path, 0, 100)
		if err != nil || r.Path != want || r.Text != "portable needle" {
			t.Fatalf("read: %+v, %v", r, err)
		}
	}
	// Native Windows input is also accepted, while returned paths stay portable.
	r, err := s.Read(context.Background(), filepath.FromSlash(want), 0, 100)
	if err != nil || r.Path != want || r.Text != "portable needle" {
		t.Fatalf("native read: %+v, %v", r, err)
	}
}

// dl drops the batch-level error for tests that only inspect per-file results.
func dl(s *Store, ctx context.Context, c *aulaglobal.Client, files []aulaglobal.File) []Result {
	r, _ := s.Download(ctx, c, files)
	return r
}

func TestDownloadAbortsOnDeadToken(t *testing.T) {
	for _, tc := range []struct {
		name, fileCode, siteInfo string
		abort                    bool
	}{
		{"invalidtoken", "invalidtoken", "", true},
		{"accessexception with dead token", "accessexception", `{"errorcode":"invalidtoken","exception":"x"}`, true},
		{"accessexception with valid token", "accessexception", `{"fullname":"Ana","userid":1}`, false},
		{"unrelated error", "filenotfound", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var fileCalls atomic.Int32
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/server.php") {
					fmt.Fprint(w, tc.siteInfo)
					return
				}
				fileCalls.Add(1)
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprintf(w, `{"errorcode":%q,"exception":"x"}`, tc.fileCode)
			}))
			defer ts.Close()
			s, e := Open(t.TempDir(), 1024, 2)
			if e != nil {
				t.Fatal(e)
			}
			defer s.Root.Close()
			files := []aulaglobal.File{}
			for i := 0; i < 40; i++ {
				files = append(files, aulaglobal.File{CourseID: 1, ModuleID: 1, Filename: "a.txt", Handle: fmt.Sprint(i), URL: ts.URL + "/pluginfile.php/f"})
			}
			c := aulaglobal.New(ts.URL, aulaglobal.StaticToken{Value: "secret"})
			res, e := s.Download(context.Background(), c, files)
			if !tc.abort {
				if e != nil || len(res) != len(files) || fileCalls.Load() != int32(len(files)) {
					t.Fatalf("per-file errors expected: %v, %d results, %d calls", e, len(res), fileCalls.Load())
				}
				for _, r := range res {
					if r.Status != "error" || r.Error == "" {
						t.Fatalf("%+v", r)
					}
				}
				return
			}
			if !errors.Is(e, aulaglobal.ErrTokenExpired) || strings.Contains(e.Error(), "secret") {
				t.Fatalf("want token expired error, got %v", e)
			}
			if n := fileCalls.Load(); n > 4 {
				t.Fatalf("batch not short-circuited: %d file requests", n)
			}
		})
	}
}
