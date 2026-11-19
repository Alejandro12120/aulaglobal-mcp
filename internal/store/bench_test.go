package store

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"os"
	"testing"
)

func BenchmarkStoreExtraction(b *testing.B) {
	pdf, err := os.ReadFile("testdata/tiny.pdf")
	if err != nil {
		b.Fatal(err)
	}
	var docx bytes.Buffer
	archive := zip.NewWriter(&docx)
	entry, err := archive.Create("word/document.xml")
	if err != nil {
		b.Fatal(err)
	}
	if _, err = fmt.Fprint(entry, `<document><p><t>Benchmark text</t></p></document>`); err != nil {
		b.Fatal(err)
	}
	if err = archive.Close(); err != nil {
		b.Fatal(err)
	}
	for _, fixture := range []struct {
		path string
		data []byte
	}{
		{"sample.txt", bytes.Repeat([]byte("Benchmark text.\n"), 6400)},
		{"sample.html", bytes.Repeat([]byte("<p>Benchmark <b>text</b>.</p>\n"), 3600)},
		{"sample.pdf", pdf},
		{"sample.docx", docx.Bytes()},
	} {
		b.Run(fixture.path, func(b *testing.B) {
			b.SetBytes(int64(len(fixture.data)))
			for b.Loop() {
				doc, err := decodeDocument(context.Background(), fixture.path, fixture.data)
				if err != nil || doc.text() == "" {
					b.Fatalf("extract %s: %v", fixture.path, err)
				}
			}
		})
	}
}

func BenchmarkStoreSearch(b *testing.B) {
	dataStore, err := Open(b.TempDir(), 256<<20, 4)
	if err != nil {
		b.Fatal(err)
	}
	defer dataStore.Root.Close()
	chunk := bytes.Repeat([]byte("benchmark text for a full corpus scan.\n"), 2688)
	for index := range 300 {
		if err = dataStore.Root.WriteFile(fmt.Sprintf("file-%03d.txt", index), chunk, 0600); err != nil {
			b.Fatal(err)
		}
	}
	ctx := context.Background()
	for _, scenario := range []struct {
		name, query string
		regex       bool
	}{
		{"literal_warm", "absent needle", false},
		{"regex_warm", "absent [nN]eedle", true},
	} {
		b.Run(scenario.name, func(b *testing.B) {
			if _, err := dataStore.Search(ctx, scenario.query, scenario.regex, 20); err != nil {
				b.Fatal(err)
			}
			b.SetBytes(int64(300 * len(chunk)))
			for b.Loop() {
				matches, err := dataStore.Search(ctx, scenario.query, scenario.regex, 20)
				if err != nil || len(matches) != 0 {
					b.Fatalf("search: %v, %v", matches, err)
				}
			}
		})
	}
}
