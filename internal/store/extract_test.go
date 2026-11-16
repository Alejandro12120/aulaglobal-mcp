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
	store, err := Open(dir, 1<<20, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Root.Close()
	b, _ := os.ReadFile("testdata/tiny.pdf")
	os.WriteFile(filepath.Join(dir, "tiny.pdf"), b, 0600)
	r, err := store.Read(context.Background(), "tiny.pdf", 0, 1000)
	if err != nil || !strings.Contains(r.Text, "Tiny PDF citation") || !strings.Contains(r.Text, "[Page 1]") {
		t.Fatalf("%+v %v", r, err)
	}
	m, err := store.Search(context.Background(), "citation", false, 10)
	if err != nil || len(m) != 1 || m[0].Page != 1 {
		t.Fatalf("%+v %v", m, err)
	}
	for _, tc := range []struct{ ext, entry string }{{"docx", "word/document.xml"}, {"pptx", "ppt/slides/slide2.xml"}, {"odt", "content.xml"}, {"odp", "content.xml"}} {
		var b bytes.Buffer
		z := zip.NewWriter(&b)
		w, _ := z.Create(tc.entry)
		fmt.Fprint(w, "<root><p><t>Office needle</t></p></root>")
		z.Close()
		os.WriteFile(filepath.Join(dir, "x."+tc.ext), b.Bytes(), 0600)
		r, err := store.Read(context.Background(), "x."+tc.ext, 0, 1000)
		if err != nil || !strings.Contains(r.Text, "Office needle") {
			t.Fatalf("%s %+v %v", tc.ext, r, err)
		}
	}
	// Highly compressible content is refused even when it is not selected for XML.
	var bomb bytes.Buffer
	z := zip.NewWriter(&bomb)
	w, _ := z.Create("padding")
	fmt.Fprint(w, strings.Repeat("x", 1<<20))
	z.Close()
	if _, err := decodeDocument(context.Background(), "bomb.docx", bomb.Bytes()); err == nil {
		t.Fatal("zip bomb accepted")
	}
	for _, b := range []string{"%PDF-fake", "%PDF-1.4\ntrailer << /Root 1 0 R >>", "random"} {
		os.WriteFile(filepath.Join(dir, "bad.pdf"), []byte(b), 0600)
		store.Read(context.Background(), "bad.pdf", 0, 1000)
	}
	if _, err := store.Read(context.Background(), ".aulaglobal-cache/manifest.json", 0, 100); err == nil {
		t.Fatal("cache readable")
	}
}
func TestCacheAndFullSearch(t *testing.T) {
	dir := t.TempDir()
	store, _ := Open(dir, 1<<20, 1)
	defer store.Root.Close()
	p := filepath.Join(dir, "a.txt")
	os.WriteFile(p, []byte(strings.Repeat(" ", 70000)+"needle"), 0600)
	m, err := store.Search(context.Background(), "needle", false, 10)
	if err != nil || len(m) != 1 || m[0].Offset != 70000 {
		t.Fatalf("%+v %v", m, err)
	}
	os.WriteFile(p, []byte(strings.Repeat(" ", 70000)+"change"), 0600)
	os.Chtimes(p, time.Now(), time.Now().Add(time.Second))
	m, err = store.Search(context.Background(), "change", false, 10, "a.txt")
	if err != nil || len(m) != 1 {
		t.Fatalf("%+v %v", m, err)
	}
	m, err = store.Search(context.Background(), "change", false, 10, "other")
	if err != nil || len(m) != 0 {
		t.Fatalf("%+v %v", m, err)
	}
	entries, _ := os.ReadDir(filepath.Join(dir, ".aulaglobal-cache"))
	if len(entries) != 2 {
		t.Fatalf("cache invalidation entries: %d", len(entries))
	}
}
func TestManifestReplacement(t *testing.T) {
	moodle := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, r.URL.Query().Get("body")) }))
	defer moodle.Close()
	dir := t.TempDir()
	store, _ := Open(dir, 1024, 2)
	defer store.Root.Close()
	client := aulaglobal.New(moodle.URL, aulaglobal.StaticToken{Value: "secret"})
	n := int64(3)
	a := aulaglobal.File{CourseID: 1, CourseName: "Álgebra", Section: "Tema 1", ModuleID: 2, Filepath: "/Carpeta/", Filename: "a.txt", Handle: "aaa", URL: moodle.URL + "?body=old", Size: &n, Modified: 1}
	b := a
	b.Handle = "bbb"
	b.ModuleID = 3
	results := dl(store, context.Background(), client, []aulaglobal.File{b, a})
	if results[0].Path == results[1].Path || results[1].Path != path.Join("Álgebra", "Tema 1", "Carpeta", "a.txt") {
		t.Fatalf("%+v", results)
	}
	store.Root.Close()
	store, _ = Open(dir, 1024, 1)
	defer store.Root.Close()
	if r := dl(store, context.Background(), client, []aulaglobal.File{a})[0]; r.Status != "skipped" {
		t.Fatalf("%+v", r)
	}
	a.URL = moodle.URL + "?body=new"
	a.Modified = 2
	r := dl(store, context.Background(), client, []aulaglobal.File{a})[0]
	data, _ := store.Root.ReadFile(r.Path)
	if r.Status != "downloaded" || r.Path != results[1].Path || string(data) != "new" {
		t.Fatalf("%+v %s", r, data)
	}
}
func TestDownloadStall(t *testing.T) {
	moodle := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("x"))
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer moodle.Close()
	store, _ := Open(t.TempDir(), 1024, 1)
	defer store.Root.Close()
	client := aulaglobal.New(moodle.URL, aulaglobal.StaticToken{Value: "secret"})
	client.StallTimeout = 40 * time.Millisecond
	start := time.Now()
	r := dl(store, context.Background(), client, []aulaglobal.File{{Filename: "x.txt", Handle: "x", URL: moodle.URL}})[0]
	if r.Status != "error" || time.Since(start) > time.Second {
		t.Fatalf("%+v", r)
	}
}

func TestExtractionCaps(t *testing.T) {
	ctx := context.Background()
	if _, err := decodeDocument(ctx, "large.txt", []byte(strings.Repeat("x", textCap+1))); err == nil {
		t.Fatal("output cap ignored")
	}
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	for i := 0; i < 2049; i++ {
		z.Create(fmt.Sprint(i))
	}
	z.Close()
	if _, err := decodeDocument(ctx, "many.docx", b.Bytes()); err == nil {
		t.Fatal("entry count ignored")
	}
	pdf, _ := os.ReadFile("testdata/tiny.pdf")
	pdf = bytes.Replace(pdf, []byte("/Count 1"), []byte("/Count 501"), 1)
	if _, err := decodeDocument(ctx, "many.pdf", pdf); err == nil {
		t.Fatal("page count ignored")
	}
}

func TestLateExtractionCaches(t *testing.T) {
	for _, callerTimeout := range []bool{false, true} {
		t.Run(fmt.Sprint(callerTimeout), func(t *testing.T) {
			dir := t.TempDir()
			store, _ := Open(dir, 1024, 1)
			defer store.Root.Close()
			os.WriteFile(filepath.Join(dir, "slow.pdf"), []byte("fake"), 0600)
			finished := make(chan struct{})
			release := make(chan struct{})
			store.pdfExtractor = func(context.Context, []byte) (document, error) {
				<-release
				close(finished)
				return document{Pages: []Page{{1, "late result"}}}, nil
			}
			store.ExtractTimeout = 20 * time.Millisecond
			ctx := context.Background()
			if callerTimeout {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, 10*time.Millisecond)
				defer cancel()
			}
			r, err := store.Read(ctx, "slow.pdf", 0, 1000)
			close(release)
			<-finished
			if (!callerTimeout && (err != nil || !strings.Contains(r.Note, "extraction in progress"))) || (callerTimeout && err != context.DeadlineExceeded) {
				t.Errorf("timeout note: %+v %v", r, err)
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
			r, err = store.Read(context.Background(), "slow.pdf", 0, 1000)
			if err != nil || !strings.Contains(r.Text, "late result") {
				t.Fatalf("%+v %v", r, err)
			}

		})
	}
}
func TestCanonicalFoldersAndReadableCollisions(t *testing.T) {
	dir := t.TempDir()
	store, _ := Open(dir, 1024, 1)
	defer store.Root.Close()
	a := aulaglobal.File{Handle: "aaa", CourseID: 1, CourseName: "Álgebra", Section: "Tema", Filename: "a.pdf"}
	b := a
	b.Handle = "bbb"
	paths, err := store.paths([]aulaglobal.File{b, a})
	if err != nil || paths["bbb"] != path.Join("Álgebra", "Tema", "a (2).pdf") {
		t.Errorf("%v %v", paths, err)
	}
	store.Root.Close()
	store, _ = Open(dir, 1024, 1)
	defer store.Root.Close()
	client := a
	client.Handle = "ccc"
	client.CourseName = "Renamed"
	client.Filename = "other.pdf"
	paths, err = store.paths([]aulaglobal.File{client, b})
	if err != nil || paths["ccc"] != path.Join("Álgebra", "Tema", "other.pdf") || paths["bbb"] != path.Join("Álgebra", "Tema", "a (2).pdf") {
		t.Fatalf("%v %v", paths, err)
	}
}

func TestExtractTimeoutConfiguration(t *testing.T) {
	for _, v := range []string{"", "1", "600", "0", "601", "-1", "abc"} {
		t.Run(v, func(t *testing.T) {
			t.Setenv("AULAGLOBAL_EXTRACT_TIMEOUT_SECONDS", v)
			store, err := Open(t.TempDir(), 1024, 1)
			if err == nil {
				defer store.Root.Close()
			}
			good := v == "" || v == "1" || v == "600"
			if (err == nil) != good {
				t.Fatalf("value %q: %v", v, err)
			}
			if err == nil {
				expected := 60 * time.Second
				if v == "1" {
					expected = time.Second
				}
				if v == "600" {
					expected = 600 * time.Second
				}
				if store.ExtractTimeout != expected {
					t.Fatal(store.ExtractTimeout)
				}
			}
		})
	}
}

func TestExtractionSlotBound(t *testing.T) {
	dir := t.TempDir()
	store, _ := Open(dir, 1024, 1)
	defer store.Root.Close()
	store.ExtractTimeout = 20 * time.Millisecond
	started := make(chan struct{}, 3)
	release := make(chan struct{})
	store.pdfExtractor = func(context.Context, []byte) (document, error) {
		started <- struct{}{}
		<-release
		return document{Pages: []Page{{1, "done"}}}, nil
	}
	for _, name := range []string{"a.pdf", "b.pdf", "c.pdf"} {
		os.WriteFile(filepath.Join(dir, name), []byte("fake"), 0600)
		r, err := store.Read(context.Background(), name, 0, 1000)
		if err != nil || !strings.Contains(r.Note, "extraction in progress") {
			t.Fatalf("%+v %v", r, err)
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
