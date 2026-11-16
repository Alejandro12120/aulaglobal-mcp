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
	for _, store := range []string{"../../etc/passwd", "\\..\\evil", "..hidden", "NUL", "CON.txt", "a\x00\nb", strings.Repeat("x", 300)} {
		got := Sanitize(store)
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
	moodle := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	defer moodle.Close()
	store, err := Open(t.TempDir(), 1024, 4)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Root.Close()
	client := aulaglobal.New(moodle.URL, aulaglobal.StaticToken{Value: "secret"})
	if err := client.SetMaxRPS(aulaglobal.MaxMaxRPS); err != nil { // 200 files must not wait on the default politeness limit
		t.Fatal(err)
	}
	files := []aulaglobal.File{}
	size := int64(5)
	for i := 0; i < 200; i++ {
		files = append(files, aulaglobal.File{CourseID: 1, ModuleID: 1, Filename: "../../a.txt", Handle: fmt.Sprint(i), URL: moodle.URL + "/file", Size: &size, Modified: 1700000000})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, r := range dl(store, ctx, client, files) {
		if r.Status != "downloaded" {
			t.Fatalf("%+v", r)
		}
	}
	if peak.Load() > 4 {
		t.Fatal("unbounded concurrency")
	}
	for _, r := range dl(store, ctx, client, files) {
		if r.Status != "skipped" {
			t.Fatalf("%+v", r)
		}
	}
	cancel()
	if r := dl(store, ctx, client, files[:1])[0]; r.Status != "error" {
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
		moodle := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", tc.typ)
			fmt.Fprint(w, tc.body)
		}))
		store, err := Open(t.TempDir(), 1024, 2)
		if err != nil {
			t.Fatal(err)
		}
		f := aulaglobal.File{Filename: "x.pdf", Handle: "x", URL: moodle.URL, Size: &tc.size}
		defer store.Root.Close()
		defer moodle.Close()
		r := dl(store, context.Background(), aulaglobal.New(moodle.URL, aulaglobal.StaticToken{Value: "secret"}), []aulaglobal.File{f})[0]
		if r.Status != "error" {
			t.Fatalf("saved invalid file %+v", r)
		}
		if _, err = store.Root.Stat(r.Path); !os.IsNotExist(err) {
			t.Fatal("destination exists")
		}
		store.Root.Close()
		moodle.Close()
	}
}
func TestRootConfinement(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("secret"), 0600)
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	store, err := Open(root, 1024, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Root.Close()
	for _, p := range []string{"../secret.txt", filepath.Join(outside, "secret.txt"), "escape/secret.txt"} {
		if _, err := store.Read(context.Background(), p, 0, 100); err == nil {
			t.Fatalf("escaped via %s", p)
		}
	}
	if err = store.Root.MkdirAll("escape/new", 0700); err == nil {
		t.Fatal("write escape")
	}
	os.WriteFile(filepath.Join(root, "ok.txt"), []byte("alpha hello world"), 0600)
	matches, err := store.Search(context.Background(), "hello", false, 10)
	if err != nil || len(matches) != 1 {
		t.Fatalf("%v %v", matches, err)
	}
}

func TestReadingPagingAndSearchLimits(t *testing.T) {
	dir := t.TempDir()
	store, err := Open(dir, 1024, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Root.Close()
	for name, content := range map[string]string{"unicode.txt": "ábc", "page.html": "<script>bad</script><p>hola &amp; mundo</p>", "file.pdf": "%PDF-fake", "large.txt": strings.Repeat("a", 10000)} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	ctx := context.Background()
	r, err := store.Read(ctx, "unicode.txt", 0, 2)
	if err != nil || r.Text != "á" || r.NextOffset != 2 {
		t.Fatalf("%+v %v", r, err)
	}
	if _, err = store.Read(ctx, "unicode.txt", 1, 2); err == nil {
		t.Fatal("accepted partial UTF-8 offset")
	}
	if _, err = store.Read(ctx, "unicode.txt", 0, 1); err == nil {
		t.Fatal("accepted non-progressing page")
	}
	r, err = store.Read(ctx, "page.html", 0, 100)
	if err != nil || strings.Contains(r.Text, "bad") || !strings.Contains(r.Text, "hola & mundo") {
		t.Fatalf("%+v %v", r, err)
	}
	r, err = store.Read(ctx, "file.pdf", 0, 100)
	if err != nil || r.Text != "" || r.Note == "" {
		t.Fatalf("%+v %v", r, err)
	}
	matches, err := store.Search(ctx, "a+", true, 100)
	if err != nil {
		t.Fatal(err)
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
	store, err := Open(dir, 1024, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Root.Close()
	if err = store.atomic(".aulaglobal-cache/manifest.json", []byte(`{"h":{"path":"Course\\Section\\notes.txt","size":null,"timemodified":0}}`)); err != nil {
		t.Fatal(err)
	}
	paths, err := store.paths([]aulaglobal.File{{Handle: "h", CourseID: 1, Filename: "notes.txt"}})
	if err != nil || paths["h"] != "Course/Section/notes.txt" {
		t.Fatal(paths, err)
	}
	b, err := store.cacheRead(".aulaglobal-cache/manifest.json")
	if err != nil || strings.Contains(string(b), `\\`) {
		t.Fatal(string(b), err)
	}
}

func TestPortablePathRoundTrip(t *testing.T) {
	moodle := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "portable needle")
	}))
	defer moodle.Close()
	store, err := Open(t.TempDir(), 1024, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Root.Close()
	f := aulaglobal.File{Handle: "portable", CourseID: 1, CourseName: "Course", Section: "Section", Filename: "notes.txt", URL: moodle.URL}
	const want = "Course/Section/notes.txt"
	if got := Path(f); got != want {
		t.Fatalf("Path = %q, want %q", got, want)
	}
	result := dl(store, context.Background(), aulaglobal.New(moodle.URL, aulaglobal.StaticToken{Value: "secret"}), []aulaglobal.File{f})[0]
	if result.Status != "downloaded" || result.Path != want {
		t.Fatalf("download: %+v", result)
	}
	for _, prefix := range []string{"", "Course/Section", want} {
		matches, err := store.Search(context.Background(), "needle", false, 10, prefix)
		if err != nil || len(matches) != 1 || matches[0].Path != want {
			t.Fatalf("search prefix %q: %+v, %v", prefix, matches, err)
		}
		r, err := store.Read(context.Background(), matches[0].Path, 0, 100)
		if err != nil || r.Path != want || r.Text != "portable needle" {
			t.Fatalf("read: %+v, %v", r, err)
		}
	}
	// Native Windows input is also accepted, while returned paths stay portable.
	r, err := store.Read(context.Background(), filepath.FromSlash(want), 0, 100)
	if err != nil || r.Path != want || r.Text != "portable needle" {
		t.Fatalf("native read: %+v, %v", r, err)
	}
}

// dl drops the batch-level error for tests that only inspect per-file results.
func dl(store *Store, ctx context.Context, client *aulaglobal.Client, files []aulaglobal.File) []Result {
	r, _ := store.Download(ctx, client, files)
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
			moodle := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/server.php") {
					fmt.Fprint(w, tc.siteInfo)
					return
				}
				fileCalls.Add(1)
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprintf(w, `{"errorcode":%q,"exception":"x"}`, tc.fileCode)
			}))
			defer moodle.Close()
			store, err := Open(t.TempDir(), 1024, 2)
			if err != nil {
				t.Fatal(err)
			}
			defer store.Root.Close()
			files := []aulaglobal.File{}
			for i := 0; i < 40; i++ {
				files = append(files, aulaglobal.File{CourseID: 1, ModuleID: 1, Filename: "a.txt", Handle: fmt.Sprint(i), URL: moodle.URL + "/pluginfile.php/f"})
			}
			client := aulaglobal.New(moodle.URL, aulaglobal.StaticToken{Value: "secret"})
			_ = client.SetMaxRPS(aulaglobal.MaxMaxRPS) // 40 files: do not wait on the default politeness limit
			res, err := store.Download(context.Background(), client, files)
			if !tc.abort {
				if err != nil || len(res) != len(files) || fileCalls.Load() != int32(len(files)) {
					t.Fatalf("per-file errors expected: %v, %d results, %d calls", err, len(res), fileCalls.Load())
				}
				for _, r := range res {
					if r.Status != "error" || r.Error == "" {
						t.Fatalf("%+v", r)
					}
				}
				return
			}
			if !errors.Is(err, aulaglobal.ErrTokenExpired) || strings.Contains(err.Error(), "secret") {
				t.Fatalf("want token expired error, got %v", err)
			}
			if n := fileCalls.Load(); n > 4 {
				t.Fatalf("batch not short-circuited: %d file requests", n)
			}
		})
	}
}

// Persistent 429s end the batch with the single user-facing error instead of
// grinding through every remaining file.
func TestDownloadAbortsWhenRateLimited(t *testing.T) {
	var calls atomic.Int32
	moodle := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Retry-After", "1") // real time: two 1 s pauses before giving up
		w.WriteHeader(429)
	}))
	defer moodle.Close()
	store, err := Open(t.TempDir(), 1024, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Root.Close()
	files := []aulaglobal.File{}
	for i := 0; i < 20; i++ {
		files = append(files, aulaglobal.File{CourseID: 1, ModuleID: 1, Filename: "a.txt", Handle: fmt.Sprint(i), URL: moodle.URL + "/pluginfile.php/f"})
	}
	client := aulaglobal.New(moodle.URL, aulaglobal.StaticToken{Value: "secret"})
	_, err = store.Download(context.Background(), client, files)
	if !errors.Is(err, aulaglobal.ErrRateLimited) || !strings.Contains(err.Error(), "AulaGlobal está limitando las peticiones") || strings.Contains(err.Error(), "secret") {
		t.Fatalf("got %v", err)
	}
	if n := calls.Load(); n > 6 {
		t.Fatalf("%d requests: the batch kept going after being throttled", n)
	}
}
