package store

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"
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

func benchDOCX(tb testing.TB) []byte {
	var body strings.Builder
	for index := range 50 {
		switch {
		case index%10 == 0:
			body.WriteString(docxParagraph(`<w:pStyle w:val="Ttulo1"/>`, docxRun(fmt.Sprint("Section ", index/10))))
		case index%10 == 1:
			body.WriteString(docxParagraph(`<w:numPr><w:ilvl w:val="0"/><w:numId w:val="2"/></w:numPr>`, docxRun("A list item with some words")))
		default:
			body.WriteString(docxParagraph("", docxRun("A body paragraph with <w:t> typical words, ")+docxRun("and a second run of text for the benchmark.")))
		}
	}
	body.WriteString("<w:tbl>")
	for row := range 20 {
		body.WriteString("<w:tr>")
		for column := range 5 {
			body.WriteString(docxCell("", fmt.Sprintf("r%dc%d", row, column)))
		}
		body.WriteString("</w:tr>")
	}
	body.WriteString("</w:tbl>")
	return buildZip(tb, zipPart{"word/document.xml", "<w:document><w:body>" + body.String() + "</w:body></w:document>"},
		zipPart{"word/styles.xml", docxStyles}, zipPart{"word/numbering.xml", docxNumbering})
}

func benchPPTX(tb testing.TB) []byte {
	var parts []zipPart
	var rels [][3]string
	var ids []string
	for number := 1; number <= 30; number++ {
		table := ""
		if number == 5 {
			table = `<p:graphicFrame><a:tbl><a:tr><a:tc><a:txBody>` + slideParagraph("", "H1") + `</a:txBody></a:tc><a:tc><a:txBody>` + slideParagraph("", "H2") + `</a:txBody></a:tc></a:tr></a:tbl></p:graphicFrame>`
		}
		slide := slideXML(shape(`<p:ph type="title"/>`, slideParagraph("", fmt.Sprint("Slide title ", number))),
			shape(`<p:ph idx="1"/>`, slideParagraph("", "First bullet point")+slideParagraph(` lvl="1"`, "Second level bullet")+slideParagraph("", "Third")), table)
		name := fmt.Sprintf("ppt/slides/slide%d.xml", number)
		id := fmt.Sprint("rId", number)
		parts = append(parts, zipPart{name, slide},
			zipPart{fmt.Sprintf("ppt/slides/_rels/slide%d.xml.rels", number), relationships([3]string{"rId1", "notesSlide", fmt.Sprintf("../notesSlides/notesSlide%d.xml", number)})},
			zipPart{fmt.Sprintf("ppt/notesSlides/notesSlide%d.xml", number), slideXML(shape(`<p:ph type="body" idx="1"/>`, slideParagraph("", "Speaker notes for the slide")), shape(`<p:ph type="sldNum"/>`, slideParagraph("", "1")))})
		rels = append(rels, [3]string{id, "slide", fmt.Sprintf("slides/slide%d.xml", number)})
		ids = append(ids, id)
	}
	return buildZip(tb, append(parts, presentation(ids...), zipPart{"ppt/_rels/presentation.xml.rels", relationships(rels...)})...)
}

func benchXLSX(tb testing.TB) []byte {
	var shared strings.Builder
	shared.WriteString("<sst>")
	for index := range 500 {
		fmt.Fprintf(&shared, "<si><t>shared text %d</t></si>", index)
	}
	shared.WriteString("</sst>")
	parts := []zipPart{{"xl/sharedStrings.xml", shared.String()}}
	var rels [][3]string
	var sheets strings.Builder
	for sheet := 1; sheet <= 3; sheet++ {
		var data strings.Builder
		data.WriteString("<worksheet><sheetData>")
		for row := 1; row <= 2000; row++ {
			fmt.Fprintf(&data, `<row r="%d">`, row)
			for column := range 10 {
				reference := fmt.Sprintf("%c%d", 'A'+column, row)
				if column%2 == 0 {
					fmt.Fprintf(&data, `<c r="%s" t="s"><v>%d</v></c>`, reference, (row*7+column)%500)
				} else {
					fmt.Fprintf(&data, `<c r="%s"><v>%d.5</v></c>`, reference, row*column)
				}
			}
			data.WriteString("</row>")
		}
		data.WriteString("</sheetData></worksheet>")
		parts = append(parts, zipPart{fmt.Sprintf("xl/worksheets/sheet%d.xml", sheet), data.String()})
		rels = append(rels, [3]string{fmt.Sprint("rId", sheet), "worksheet", fmt.Sprintf("worksheets/sheet%d.xml", sheet)})
		fmt.Fprintf(&sheets, `<sheet name="Sheet%d" sheetId="%d" r:id="rId%d"/>`, sheet, sheet, sheet)
	}
	return buildZip(tb, append(parts, zipPart{"xl/workbook.xml", "<workbook><sheets>" + sheets.String() + "</sheets></workbook>"},
		zipPart{"xl/_rels/workbook.xml.rels", relationships(rels...)})...)
}

func benchODS(tb testing.TB) []byte {
	var body strings.Builder
	body.WriteString(`<table:table table:name="Data"><table:table-column table:number-columns-repeated="1024"/>`)
	for row := range 1000 {
		body.WriteString("<table:table-row>")
		for column := range 10 {
			fmt.Fprintf(&body, `<table:table-cell office:value-type="string"><text:p>cell %d,%d</text:p></table:table-cell>`, row, column)
		}
		body.WriteString(`<table:table-cell table:number-columns-repeated="1014"/></table:table-row>`)
	}
	body.WriteString(`<table:table-row table:number-rows-repeated="1048000"><table:table-cell table:number-columns-repeated="1024"/></table:table-row></table:table>`)
	return buildZip(tb, zipPart{"mimetype", "application/vnd.oasis.opendocument.spreadsheet"}, zipPart{"content.xml", odfContent("spreadsheet", body.String())})
}

func BenchmarkOfficeExtraction(b *testing.B) {
	for _, fixture := range []struct {
		path string
		data []byte
	}{
		{"structured.docx", benchDOCX(b)},
		{"structured.pptx", benchPPTX(b)},
		{"structured.xlsx", benchXLSX(b)},
		{"filler.ods", benchODS(b)},
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
