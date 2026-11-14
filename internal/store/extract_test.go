package store

import (
	"archive/zip"
	"aulaglobal-mcp/internal/aulaglobal"
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDocuments(t *testing.T) {
	dir := t.TempDir()
	s, e := Open(dir, 1<<20, 2)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Root.Close()
	b, _ := os.ReadFile("testdata/tiny.pdf")
	os.WriteFile(filepath.Join(dir, "tiny.pdf"), b, 0600)
	r, e := s.Read(context.Background(), "tiny.pdf", 0, 1000)
	if e != nil || !strings.Contains(r.Text, "Tiny PDF citation") || !strings.Contains(r.Text, "[Page 1]") {
		t.Fatalf("%+v %v", r, e)
	}
	m, e := s.Search(context.Background(), "citation", false, 10)
	if e != nil || len(m) != 1 || m[0].Page != 1 {
		t.Fatalf("%+v %v", m, e)
	}
	for _, tc := range []struct{ ext, entry string }{{"docx", "word/document.xml"}, {"pptx", "ppt/slides/slide2.xml"}, {"odt", "content.xml"}, {"odp", "content.xml"}} {
		var b bytes.Buffer
		z := zip.NewWriter(&b)
		w, _ := z.Create(tc.entry)
		fmt.Fprint(w, "<root><p><t>Office needle</t></p></root>")
		z.Close()
		os.WriteFile(filepath.Join(dir, "x."+tc.ext), b.Bytes(), 0600)
		r, e := s.Read(context.Background(), "x."+tc.ext, 0, 1000)
		if e != nil || !strings.Contains(r.Text, "Office needle") {
			t.Fatalf("%s %+v %v", tc.ext, r, e)
		}
	}
	// Highly compressible content is refused even when it is not selected for XML.
	var bomb bytes.Buffer
	z := zip.NewWriter(&bomb)
	w, _ := z.Create("padding")
	fmt.Fprint(w, strings.Repeat("x", 1<<20))
	z.Close()
	if _, e := decodeDocument(context.Background(), "bomb.docx", bomb.Bytes()); e == nil {
		t.Fatal("zip bomb accepted")
	}
	for _, b := range []string{"%PDF-fake", "%PDF-1.4\ntrailer << /Root 1 0 R >>", "random"} {
		os.WriteFile(filepath.Join(dir, "bad.pdf"), []byte(b), 0600)
		s.Read(context.Background(), "bad.pdf", 0, 1000)
	}
	if _, e := s.Read(context.Background(), ".aulaglobal-cache/manifest.json", 0, 100); e == nil {
		t.Fatal("cache readable")
	}
}
func TestCacheAndFullSearch(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir, 1<<20, 1)
	defer s.Root.Close()
	p := filepath.Join(dir, "a.txt")
	os.WriteFile(p, []byte(strings.Repeat(" ", 70000)+"needle"), 0600)
	m, e := s.Search(context.Background(), "needle", false, 10)
	if e != nil || len(m) != 1 || m[0].Offset != 70000 {
		t.Fatalf("%+v %v", m, e)
	}
	os.WriteFile(p, []byte(strings.Repeat(" ", 70000)+"change"), 0600)
	os.Chtimes(p, time.Now(), time.Now().Add(time.Second))
	m, e = s.Search(context.Background(), "change", false, 10, "a.txt")
	if e != nil || len(m) != 1 {
		t.Fatalf("%+v %v", m, e)
	}
	m, e = s.Search(context.Background(), "change", false, 10, "other")
	if e != nil || len(m) != 0 {
		t.Fatalf("%+v %v", m, e)
	}
	entries, _ := os.ReadDir(filepath.Join(dir, ".aulaglobal-cache"))
	if len(entries) != 2 {
		t.Fatalf("cache invalidation entries: %d", len(entries))
	}
}
func TestManifestReplacement(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, r.URL.Query().Get("body")) }))
	defer ts.Close()
	dir := t.TempDir()
	s, _ := Open(dir, 1024, 2)
	defer s.Root.Close()
	c := aulaglobal.New(ts.URL, aulaglobal.StaticToken{Value: "secret"})
	n := int64(3)
	a := aulaglobal.File{CourseID: 1, CourseName: "Álgebra", Section: "Tema 1", ModuleID: 2, Filepath: "/Carpeta/", Filename: "a.txt", Handle: "aaa", URL: ts.URL + "?body=old", Size: &n, Modified: 1}
	b := a
	b.Handle = "bbb"
	b.ModuleID = 3
	results := dl(s, context.Background(), c, []aulaglobal.File{b, a})
	if results[0].Path == results[1].Path || results[1].Path != path.Join("Álgebra", "Tema 1", "Carpeta", "a.txt") {
		t.Fatalf("%+v", results)
	}
	s.Root.Close()
	s, _ = Open(dir, 1024, 1)
	defer s.Root.Close()
	if r := dl(s, context.Background(), c, []aulaglobal.File{a})[0]; r.Status != "skipped" {
		t.Fatalf("%+v", r)
	}
	a.URL = ts.URL + "?body=new"
	a.Modified = 2
	r := dl(s, context.Background(), c, []aulaglobal.File{a})[0]
	data, _ := s.Root.ReadFile(r.Path)
	if r.Status != "downloaded" || r.Path != results[1].Path || string(data) != "new" {
		t.Fatalf("%+v %s", r, data)
	}
}
func TestDownloadStall(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("x"))
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer ts.Close()
	s, _ := Open(t.TempDir(), 1024, 1)
	defer s.Root.Close()
	c := aulaglobal.New(ts.URL, aulaglobal.StaticToken{Value: "secret"})
	c.StallTimeout = 40 * time.Millisecond
	start := time.Now()
	r := dl(s, context.Background(), c, []aulaglobal.File{{Filename: "x.txt", Handle: "x", URL: ts.URL}})[0]
	if r.Status != "error" || time.Since(start) > time.Second {
		t.Fatalf("%+v", r)
	}
}

func TestExtractionCaps(t *testing.T) {
	ctx := context.Background()
	if _, e := decodeDocument(ctx, "large.txt", []byte(strings.Repeat("x", textCap+1))); e == nil {
		t.Fatal("output cap ignored")
	}
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	for i := 0; i < 2049; i++ {
		z.Create(fmt.Sprint(i))
	}
	z.Close()
	if _, e := decodeDocument(ctx, "many.docx", b.Bytes()); e == nil {
		t.Fatal("entry count ignored")
	}
	pdf, _ := os.ReadFile("testdata/tiny.pdf")
	pdf = bytes.Replace(pdf, []byte("/Count 1"), []byte("/Count 501"), 1)
	if _, e := decodeDocument(ctx, "many.pdf", pdf); e == nil {
		t.Fatal("page count ignored")
	}
}

func TestLateExtractionCaches(t *testing.T) {
	for _, callerTimeout := range []bool{false, true} {
		t.Run(fmt.Sprint(callerTimeout), func(t *testing.T) {
			dir := t.TempDir()
			s, _ := Open(dir, 1024, 1)
			defer s.Root.Close()
			os.WriteFile(filepath.Join(dir, "slow.pdf"), []byte("fake"), 0600)
			finished := make(chan struct{})
			release := make(chan struct{})
			s.pdfExtractor = func(context.Context, []byte) (document, error) {
				<-release
				close(finished)
				return document{Pages: []Page{{1, "late result"}}}, nil
			}
			s.ExtractTimeout = 20 * time.Millisecond
			ctx := context.Background()
			if callerTimeout {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, 10*time.Millisecond)
				defer cancel()
			}
			r, e := s.Read(ctx, "slow.pdf", 0, 1000)
			close(release)
			<-finished
			if (!callerTimeout && (e != nil || !strings.Contains(r.Note, "extraction in progress"))) || (callerTimeout && e != context.DeadlineExceeded) {
				t.Errorf("timeout note: %+v %v", r, e)
			}
			// Wait for the asynchronous cache write, without starting another parse.
			deadline := time.Now().Add(time.Second)
			for {
				entries, _ := os.ReadDir(filepath.Join(dir, ".aulaglobal-cache"))
				cached := false
				for _, entry := range entries {
					if strings.HasSuffix(entry.Name(), ".json") {
						cached = true
					}
				}
				if cached {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("late successful parse was not cached")
				}
				time.Sleep(time.Millisecond)
			}
			r, e = s.Read(context.Background(), "slow.pdf", 0, 1000)
			if e != nil || !strings.Contains(r.Text, "late result") {
				t.Fatalf("%+v %v", r, e)
			}

		})
	}
}
func TestCanonicalFoldersAndReadableCollisions(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir, 1024, 1)
	defer s.Root.Close()
	a := aulaglobal.File{Handle: "aaa", CourseID: 1, CourseName: "Álgebra", Section: "Tema", Filename: "a.pdf"}
	b := a
	b.Handle = "bbb"
	paths, e := s.paths([]aulaglobal.File{b, a})
	if e != nil || paths["bbb"] != path.Join("Álgebra", "Tema", "a (2).pdf") {
		t.Errorf("%v %v", paths, e)
	}
	s.Root.Close()
	s, _ = Open(dir, 1024, 1)
	defer s.Root.Close()
	c := a
	c.Handle = "ccc"
	c.CourseName = "Renamed"
	c.Filename = "other.pdf"
	paths, e = s.paths([]aulaglobal.File{c, b})
	if e != nil || paths["ccc"] != path.Join("Álgebra", "Tema", "other.pdf") || paths["bbb"] != path.Join("Álgebra", "Tema", "a (2).pdf") {
		t.Fatalf("%v %v", paths, e)
	}
}

func TestExtractTimeoutConfiguration(t *testing.T) {
	for _, v := range []string{"", "1", "600", "0", "601", "-1", "abc"} {
		t.Run(v, func(t *testing.T) {
			t.Setenv("AULAGLOBAL_EXTRACT_TIMEOUT_SECONDS", v)
			s, e := Open(t.TempDir(), 1024, 1)
			if e == nil {
				defer s.Root.Close()
			}
			good := v == "" || v == "1" || v == "600"
			if (e == nil) != good {
				t.Fatalf("value %q: %v", v, e)
			}
			if e == nil {
				expected := 60 * time.Second
				if v == "1" {
					expected = time.Second
				}
				if v == "600" {
					expected = 600 * time.Second
				}
				if s.ExtractTimeout != expected {
					t.Fatal(s.ExtractTimeout)
				}
			}
		})
	}
}

func TestExtractionSlotBound(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir, 1024, 1)
	defer s.Root.Close()
	s.ExtractTimeout = 20 * time.Millisecond
	started := make(chan struct{}, 3)
	release := make(chan struct{})
	s.pdfExtractor = func(context.Context, []byte) (document, error) {
		started <- struct{}{}
		<-release
		return document{Pages: []Page{{1, "done"}}}, nil
	}
	for _, name := range []string{"a.pdf", "b.pdf", "c.pdf"} {
		os.WriteFile(filepath.Join(dir, name), []byte("fake"), 0600)
		r, e := s.Read(context.Background(), name, 0, 1000)
		if e != nil || !strings.Contains(r.Note, "extraction in progress") {
			t.Fatalf("%+v %v", r, e)
		}
	}
	if len(started) != 2 {
		t.Errorf("started %d parsers, want 2", len(started))
	}
	close(release)
	// A successful retry waits until both earlier jobs have released their slots.
	deadline := time.Now().Add(time.Second)
	for len(extractionSlots) > 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if len(extractionSlots) != 0 {
		t.Fatal("slots not released")
	}
}
