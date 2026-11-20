package store

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

type zipPart struct{ name, body string }

func buildZip(tb testing.TB, parts ...zipPart) []byte {
	tb.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for _, part := range parts {
		entry, err := writer.Create(part.name)
		if err == nil {
			_, err = entry.Write([]byte(part.body))
		}
		if err != nil {
			tb.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		tb.Fatal(err)
	}
	return buffer.Bytes()
}

func pagesOf(tb testing.TB, path string, content []byte) []Page {
	tb.Helper()
	doc, err := decodeDocument(context.Background(), path, content)
	if err != nil {
		tb.Fatalf("%s: %v", path, err)
	}
	return doc.Pages
}

func expectPages(t *testing.T, got []Page, want ...Page) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("pages: got %d %+v, want %d", len(got), got, len(want))
	}
	for index := range want {
		if got[index] != want[index] {
			t.Errorf("page %d:\n got %+v\nwant %+v", index, got[index], want[index])
		}
	}
}

const docxStyles = `<w:styles>
<w:style w:type="paragraph" w:styleId="Ttulo"><w:name w:val="Title"/></w:style>
<w:style w:type="paragraph" w:styleId="Ttulo1"><w:name w:val="heading 1"/></w:style>
<w:style w:type="paragraph" w:styleId="Ttulo2"><w:name w:val="heading 2"/></w:style>
<w:style w:type="paragraph" w:styleId="Mio"><w:name w:val="Mi estilo"/><w:basedOn w:val="Ttulo2"/></w:style>
<w:style w:type="paragraph" w:styleId="Esquema"><w:name w:val="Esquema"/><w:pPr><w:outlineLvl w:val="2"/></w:pPr></w:style>
</w:styles>`

const docxNumbering = `<w:numbering>
<w:abstractNum w:abstractNumId="0"><w:lvl w:ilvl="0"><w:numFmt w:val="decimal"/></w:lvl></w:abstractNum>
<w:abstractNum w:abstractNumId="1"><w:lvl w:ilvl="0"><w:numFmt w:val="bullet"/></w:lvl><w:lvl w:ilvl="1"><w:numFmt w:val="bullet"/></w:lvl></w:abstractNum>
<w:num w:numId="1"><w:abstractNumId w:val="0"/></w:num>
<w:num w:numId="2"><w:abstractNumId w:val="1"/></w:num>
</w:numbering>`

func docxParagraph(properties, runs string) string {
	return "<w:p><w:pPr>" + properties + "</w:pPr>" + runs + "</w:p>"
}

func docxRun(text string) string { return "<w:r><w:t>" + text + "</w:t></w:r>" }

func docxCell(properties, text string) string {
	return "<w:tc><w:tcPr>" + properties + "</w:tcPr>" + docxParagraph("", docxRun(text)) + "</w:tc>"
}

func TestDOCXMarkdown(t *testing.T) {
	body := docxParagraph(`<w:pStyle w:val="Ttulo"/>`, docxRun("Doc Title")) +
		docxParagraph(`<w:pStyle w:val="Ttulo1"/>`, docxRun("Intro")) +
		docxParagraph(`<w:pStyle w:val="Mio"/>`, docxRun("Inherited")) +
		docxParagraph(`<w:pStyle w:val="Esquema"/>`, docxRun("Outline")) +
		docxParagraph(`<w:outlineLvl w:val="3"/>`, docxRun("Direct outline")) +
		docxParagraph("", docxRun("Hello")+"<w:r><w:tab/><w:t>world</w:t></w:r><w:r><w:br/><w:t>line2</w:t></w:r>"+
			`<w:del><w:r><w:delText>gone</w:delText></w:r></w:del><w:r><w:instrText> PAGE </w:instrText></w:r>`) +
		docxParagraph(`<w:numPr><w:ilvl w:val="0"/><w:numId w:val="2"/></w:numPr>`, docxRun("bullet one")) +
		docxParagraph(`<w:numPr><w:ilvl w:val="1"/><w:numId w:val="2"/></w:numPr>`, docxRun("nested")) +
		docxParagraph(`<w:numPr><w:ilvl w:val="0"/><w:numId w:val="1"/></w:numPr>`, docxRun("first")) +
		docxParagraph(`<w:numPr><w:ilvl w:val="0"/><w:numId w:val="1"/></w:numPr>`, docxRun("second")) +
		"<w:tbl><w:tr>" + docxCell("", "A") + docxCell("", `B|C \ D`) + "</w:tr><w:tr>" +
		docxCell(`<w:gridSpan w:val="2"/>`, "wide") + "</w:tr><w:tr>" +
		"<w:tc>" + docxParagraph("", docxRun("one")) + docxParagraph("", docxRun("two")) +
		"<w:tbl><w:tr>" + docxCell("", "inner") + docxCell("", "cell") + "</w:tr></w:tbl></w:tc>" + docxCell("", "z") + "</w:tr></w:tbl>" +
		docxParagraph("", docxRun("- not a list"))
	content := buildZip(t,
		zipPart{"word/document.xml", "<w:document><w:body>" + body + "</w:body></w:document>"},
		zipPart{"word/styles.xml", docxStyles}, zipPart{"word/numbering.xml", docxNumbering})
	want := "# Doc Title\n\n# Intro\n\n## Inherited\n\n### Outline\n\n#### Direct outline\n\nHello world\nline2\n\n" +
		"- bullet one\n  - nested\n1. first\n2. second\n\n" +
		"| A | B\\|C \\\\ D |\n| --- | --- |\n| wide | |\n| one<br>two<br>inner<br>cell | z |\n\n\\- not a list\n"
	expectPages(t, pagesOf(t, "a.docx", content), Page{0, want})
}

func slideXML(parts ...string) string {
	return "<p:sld><p:cSld><p:spTree>" + strings.Join(parts, "") + "</p:spTree></p:cSld></p:sld>"
}

func shape(placeholder, paragraphs string) string {
	return "<p:sp><p:nvSpPr><p:nvPr>" + placeholder + "</p:nvPr></p:nvSpPr><p:txBody>" + paragraphs + "</p:txBody></p:sp>"
}

func slideParagraph(properties, text string) string {
	return "<a:p><a:pPr" + properties + "/><a:r><a:t>" + text + "</a:t></a:r></a:p>"
}

func presentation(order ...string) zipPart {
	var list strings.Builder
	list.WriteString("<p:presentation><p:sldIdLst>")
	for index, id := range order {
		fmt.Fprintf(&list, `<p:sldId id="%d" r:id="%s"/>`, 256+index, id)
	}
	list.WriteString("</p:sldIdLst></p:presentation>")
	return zipPart{"ppt/presentation.xml", list.String()}
}

func relationships(entries ...[3]string) string {
	var list strings.Builder
	list.WriteString("<Relationships>")
	for _, entry := range entries {
		fmt.Fprintf(&list, `<Relationship Id="%s" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/%s" Target="%s"/>`, entry[0], entry[1], entry[2])
	}
	return list.String() + "</Relationships>"
}

func TestPPTXSlideOrder(t *testing.T) {
	var parts []zipPart
	var rels [][3]string
	var ids []string
	for number := 1; number <= 12; number++ {
		parts = append(parts, zipPart{fmt.Sprintf("ppt/slides/slide%d.xml", number), slideXML(shape(`<p:ph type="title"/>`, slideParagraph("", fmt.Sprint("Slide ", number))))})
		rels = append(rels, [3]string{fmt.Sprint("rId", number), "slide", fmt.Sprintf("slides/slide%d.xml", number)})
		ids = append(ids, fmt.Sprint("rId", number))
	}
	parts = append(parts, presentation(ids...), zipPart{"ppt/_rels/presentation.xml.rels", relationships(rels...)})
	t.Run("natural order", func(t *testing.T) {
		pages := pagesOf(t, "a.pptx", buildZip(t, parts...))
		if len(pages) != 12 {
			t.Fatalf("%d pages", len(pages))
		}
		for index, page := range pages {
			if want := fmt.Sprintf("## Slide %d\n", index+1); page.Number != index+1 || page.Text != want {
				t.Errorf("page %d: %+v", index, page)
			}
		}
	})
	t.Run("without presentation.xml the filename number decides", func(t *testing.T) {
		pages := pagesOf(t, "a.pptx", buildZip(t, parts[:12]...))
		for index, page := range pages {
			if want := fmt.Sprintf("## Slide %d\n", index+1); page.Number != index+1 || page.Text != want {
				t.Errorf("page %d: %+v", index, page)
			}
		}
	})
	t.Run("broken presentation.xml falls back", func(t *testing.T) {
		broken := append(append([]zipPart{}, parts[:12]...), zipPart{"ppt/presentation.xml", "<p:presentation><p:sldIdLst><p:sldId"}, zipPart{"ppt/_rels/presentation.xml.rels", relationships(rels...)})
		if pages := pagesOf(t, "a.pptx", buildZip(t, broken...)); len(pages) != 12 || pages[9].Number != 10 {
			t.Fatalf("%+v", pages)
		}
	})
	t.Run("relationship order differs from filename order", func(t *testing.T) {
		pages := pagesOf(t, "a.pptx", buildZip(t,
			zipPart{"ppt/slides/slide1.xml", slideXML(shape(`<p:ph type="title"/>`, slideParagraph("", "File one")))},
			zipPart{"ppt/slides/slide2.xml", slideXML(shape(`<p:ph type="title"/>`, slideParagraph("", "File two")))},
			zipPart{"ppt/slides/slide3.xml", slideXML(shape(`<p:ph type="title"/>`, slideParagraph("", "File three")))},
			presentation("rId9", "rId5", "rId7"),
			zipPart{"ppt/_rels/presentation.xml.rels", relationships([3]string{"rId5", "slide", "slides/slide1.xml"}, [3]string{"rId7", "slide", "/ppt/slides/slide2.xml"}, [3]string{"rId9", "slide", "slides/slide3.xml"})}))
		expectPages(t, pages, Page{1, "## File three\n"}, Page{2, "## File one\n"}, Page{3, "## File two\n"})
	})
}

func TestPPTXContent(t *testing.T) {
	table := `<p:graphicFrame><a:tbl><a:tr><a:tc><a:txBody>` + slideParagraph("", "H1") + `</a:txBody></a:tc><a:tc><a:txBody>` + slideParagraph("", "H2") +
		`</a:txBody></a:tc></a:tr><a:tr><a:tc gridSpan="2"><a:txBody>` + slideParagraph("", "merged") + `</a:txBody></a:tc><a:tc hMerge="1"><a:txBody><a:p/></a:txBody></a:tc></a:tr></a:tbl></p:graphicFrame>`
	slide := slideXML(
		shape(`<p:ph type="ctrTitle"/>`, slideParagraph("", "Welcome")+slideParagraph("", "everyone")),
		shape(`<p:ph idx="1"/>`, slideParagraph("", "First point")+slideParagraph(` lvl="1"`, "Sub point")),
		shape("", slideParagraph("", "Loose box")+`<a:p><a:pPr><a:buAutoNum type="arabicPeriod"/></a:pPr><a:r><a:t>Numbered</a:t></a:r></a:p>`),
		shape(`<p:ph type="sldNum"/>`, `<a:p><a:fld type="slidenum"><a:t>7</a:t></a:fld></a:p>`),
		table)
	notes := slideXML(
		shape(`<p:ph type="sldImg"/>`, ""),
		shape(`<p:ph type="body" idx="1"/>`, slideParagraph("", "Remember this")),
		shape(`<p:ph type="sldNum"/>`, `<a:p><a:fld type="slidenum"><a:t>7</a:t></a:fld></a:p>`))
	content := buildZip(t,
		zipPart{"ppt/slides/slide1.xml", slide}, zipPart{"ppt/slides/slide2.xml", slideXML(shape(`<p:ph type="title"/>`, slideParagraph("", "Next")))},
		zipPart{"ppt/slides/_rels/slide1.xml.rels", relationships([3]string{"rId1", "slideLayout", "../slideLayouts/slideLayout1.xml"}, [3]string{"rId2", "notesSlide", "../notesSlides/notesSlide1.xml"})},
		zipPart{"ppt/notesSlides/notesSlide1.xml", notes},
		presentation("rId1", "rId2"),
		zipPart{"ppt/_rels/presentation.xml.rels", relationships([3]string{"rId1", "slide", "slides/slide1.xml"}, [3]string{"rId2", "slide", "slides/slide2.xml"})})
	expectPages(t, pagesOf(t, "a.pptx", content),
		Page{1, "## Welcome everyone\n\n- First point\n  - Sub point\n\nLoose box\n\n1. Numbered\n\n| H1 | H2 |\n| --- | --- |\n| merged | |\n\n**Notes:**\n\nRemember this\n"},
		Page{2, "## Next\n"})
}

func odfContent(kind, body string) string {
	return `<office:document-content><office:body><office:` + kind + `>` + body + `</office:` + kind + `></office:body></office:document-content>`
}

func TestODTMarkdown(t *testing.T) {
	body := `<text:h text:outline-level="2">Chapter</text:h>
<text:p>Some <text:span>styled</text:span>  text<text:s text:c="2"/>end<text:tab/>x<text:line-break/>next<office:annotation><text:p>comment</text:p></office:annotation></text:p>
<text:list><text:list-item><text:p>one</text:p><text:list><text:list-item><text:p>nested</text:p></text:list-item></text:list></text:list-item><text:list-item><text:p>two</text:p></text:list-item></text:list>
<table:table><table:table-row><table:table-cell><text:p>A</text:p></table:table-cell><table:table-cell table:number-columns-spanned="2"><text:p>B</text:p></table:table-cell><table:covered-table-cell/></table:table-row>
<table:table-row><table:table-cell><text:p>1|2</text:p></table:table-cell><table:table-cell/><table:table-cell table:number-columns-repeated="1"/></table:table-row></table:table>
<text:p>After</text:p>`
	want := "## Chapter\n\nSome styled text  end x\nnext\n\n- one\n  - nested\n- two\n\n| A | B | |\n| --- | --- | --- |\n| 1\\|2 | | |\n\nAfter\n"
	for _, mimetype := range []string{"application/vnd.oasis.opendocument.text", ""} {
		parts := []zipPart{{"content.xml", odfContent("text", body)}}
		if mimetype != "" {
			parts = append(parts, zipPart{"mimetype", mimetype})
		}
		expectPages(t, pagesOf(t, "a.odt", buildZip(t, parts...)), Page{0, want})
	}
}

func TestODPPages(t *testing.T) {
	frame := func(class, text string) string {
		return `<draw:frame presentation:class="` + class + `"><draw:text-box><text:p>` + text + `</text:p></draw:text-box></draw:frame>`
	}
	body := `<draw:page draw:name="one">` + frame("title", "First") + frame("outline", "body text") + frame("footer", "foot") +
		`<presentation:notes><draw:frame presentation:class="page"><draw:page-thumbnail/></draw:frame>` + frame("notes", "say hi") + `</presentation:notes></draw:page>` +
		`<draw:page draw:name="two">` + frame("title", "Second") + `</draw:page>` + `<draw:page draw:name="three"></draw:page><draw:page>` + frame("title", "Fourth") + `</draw:page>`
	expectPages(t, pagesOf(t, "a.odp", buildZip(t, zipPart{"content.xml", odfContent("presentation", body)})),
		Page{1, "## First\n\nbody text\n\n**Notes:**\n\nsay hi\n"}, Page{2, "## Second\n"}, Page{4, "## Fourth\n"})
}

func TestXLSXSheets(t *testing.T) {
	workbook := `<workbook><sheets><sheet name="Data" sheetId="1" r:id="rId2"/><sheet name="Other" sheetId="2" r:id="rId1"/></sheets></workbook>`
	data := `<worksheet><sheetData>
<row r="1"><c r="A1" t="s"><v>0</v></c><c r="B1" t="s"><v>1</v></c><c r="D1" s="3"/></row>
<row r="2"><c r="A2" t="s"><v>2</v></c><c r="C2"><v>5</v></c></row>
<row r="3"><c r="A3" t="inlineStr"><is><r><t>in</t></r><r><t>line</t></r></is></c><c r="B3" t="b"><v>1</v></c><c r="C3" t="e"><v>#DIV/0!</v></c></row>
<row r="5"><c r="B5" t="str"><v>late</v></c></row>
<row r="6"><c r="A6"/></row><row r="9"><c r="XFD9"><v>1</v></c></row>
</sheetData></worksheet>`
	shared := `<sst><si><t>Name</t></si><si><r><t>Q</t></r><r><t>ty</t></r><rPh><t>skip</t></rPh></si><si><t>x|y</t></si></sst>`
	content := buildZip(t,
		zipPart{"xl/workbook.xml", workbook},
		zipPart{"xl/_rels/workbook.xml.rels", relationships([3]string{"rId1", "worksheet", "worksheets/sheet2.xml"}, [3]string{"rId2", "worksheet", "worksheets/sheet1.xml"})},
		zipPart{"xl/sharedStrings.xml", shared}, zipPart{"xl/worksheets/sheet1.xml", data},
		zipPart{"xl/worksheets/sheet2.xml", `<worksheet><sheetData><row r="1"><c r="B1"><v>7</v></c></row></sheetData></worksheet>`})
	want1 := "## Data\n\n| Name | Qty | |\n| --- | --- | --- |\n| x\\|y | | 5 |\n| inline | TRUE | #DIV/0! |\n| | | |\n| | late | |\n"
	expectPages(t, pagesOf(t, "a.xlsx", content), Page{1, want1}, Page{2, "## Other\n\n| | 7 |\n| --- | --- |\n"})
	expectPages(t, pagesOf(t, "a.xlsm", content), Page{1, want1}, Page{2, "## Other\n\n| | 7 |\n| --- | --- |\n"})
}

func TestODSRepeatedFillerIsNotExpanded(t *testing.T) {
	filler := `<table:table-row table:number-rows-repeated="1048576"><table:table-cell table:number-columns-repeated="1024"/></table:table-row>`
	body := `<table:table table:name="Sheet &amp; one"><table:table-column table:number-columns-repeated="1024"/>
<table:table-row><table:table-cell office:value-type="string"><text:p>Name</text:p></table:table-cell><table:table-cell office:value-type="float" office:value="3.5"><text:p>3,5</text:p></table:table-cell><table:table-cell table:number-columns-repeated="1048576"/></table:table-row>` +
		`<table:table-row table:number-rows-repeated="2"><table:table-cell office:value-type="float" office:value="9"/></table:table-row>` + filler +
		`<table:table-row><table:table-cell table:number-columns-repeated="3"/><table:table-cell table:number-columns-repeated="2"/><table:table-cell><text:p>five</text:p></table:table-cell></table:table-row>` + filler +
		`</table:table><table:table table:name="Far"><table:table-row><table:table-cell table:number-columns-repeated="1048576"/><table:table-cell><text:p>x</text:p></table:table-cell></table:table-row></table:table><table:table table:name="Empty">` + filler + `</table:table>`
	start := time.Now()
	pages := pagesOf(t, "a.ods", buildZip(t, zipPart{"content.xml", odfContent("spreadsheet", body)}))
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("repeated filler took %v", elapsed)
	}
	emptyRow := "| | | | | | |\n"
	want := "## Sheet & one\n\n| Name | 3,5 | | | | |\n| --- | --- | --- | --- | --- | --- |\n| 9 | | | | | |\n| 9 | | | | | |\n" + strings.Repeat(emptyRow, maxEmptyRows) + "| | | | | | five |\n"
	expectPages(t, pages, Page{1, want}, Page{2, "## Far\n"}, Page{3, "## Empty\n"})
}

func TestSniffing(t *testing.T) {
	pdf, _ := os.ReadFile("testdata/tiny.pdf")
	t.Run("mislabeled PDF", func(t *testing.T) {
		for _, name := range []string{"a.docx", "a.txt", "a.xlsx"} {
			pages := pagesOf(t, name, pdf)
			if len(pages) != 1 || !strings.Contains(pages[0].Text, "Tiny PDF citation") {
				t.Errorf("%s: %+v", name, pages)
			}
		}
	})
	t.Run("mislabeled office archive", func(t *testing.T) {
		docx := buildZip(t, zipPart{"word/document.xml", "<w:document><w:p><w:r><w:t>Real docx</w:t></w:r></w:p></w:document>"})
		if pages := pagesOf(t, "a.pdf", docx); len(pages) != 1 || pages[0].Text != "Real docx\n" {
			t.Errorf("%+v", pages)
		}
		odt := buildZip(t, zipPart{"mimetype", "application/vnd.oasis.opendocument.text"}, zipPart{"content.xml", odfContent("text", "<text:p>Real odt</text:p>")})
		if pages := pagesOf(t, "a.xlsx", odt); len(pages) != 1 || pages[0].Text != "Real odt\n" {
			t.Errorf("%+v", pages)
		}
	})
	ole := append(append([]byte{}, oleMagic...), make([]byte, 512)...)
	encrypted := append(append([]byte{}, ole...), []byte("E\x00n\x00c\x00r\x00y\x00p\x00t\x00e\x00d\x00P\x00a\x00c\x00k\x00a\x00g\x00e\x00")...)
	for _, test := range []struct {
		name    string
		content []byte
		want    error
	}{
		{"a.doc", ole, ErrUnsupported}, {"a.docx", ole, ErrUnsupported}, {"a.xls", ole, ErrUnsupported}, {"a.docx", encrypted, ErrEncrypted},
		{"a.ppt", []byte("not ole"), ErrUnsupported}, {"a.docx", []byte("<html>error page</html>"), ErrMalformed},
		{"a.docx", buildZip(t, zipPart{"readme.txt", "x"}), ErrUnsupported}, {"a.pdf", []byte("%PDF-1.4\ntrailer << /Encrypt 3 0 R >>"), ErrEncrypted},
		{"a.pdf", []byte("garbage"), ErrMalformed},
	} {
		if _, err := decodeDocument(context.Background(), test.name, test.content); !errors.Is(err, test.want) {
			t.Errorf("%s: got %v, want %v", test.name, err, test.want)
		}
	}
}

func TestScannedPDFNeedsOCR(t *testing.T) {
	pdf, _ := os.ReadFile("testdata/tiny.pdf")
	text := []byte("BT /F1 12 Tf 72 720 Td (Tiny PDF citation) Tj ET")
	blank := bytes.Replace(pdf, text, bytes.Repeat([]byte(" "), len(text)), 1)
	_, err := decodeDocument(context.Background(), "scan.pdf", blank)
	if !errors.Is(err, ErrNeedsOCR) || !strings.Contains(err.Error(), "OCR not supported") {
		t.Fatalf("%v", err)
	}
}

func TestNegativeCache(t *testing.T) {
	dir := t.TempDir()
	store, _ := Open(dir, 1<<20, 1)
	defer store.Root.Close()
	calls := 0
	failure := fail(ErrNeedsOCR, "PDF has no text layer (scanned); OCR not supported")
	store.pdfExtractor = func(context.Context, []byte) (document, error) {
		calls++
		return document{}, failure
	}
	os.WriteFile(dir+"/scan.pdf", []byte("fake"), 0600)
	for range 3 {
		result, err := store.Read(context.Background(), "scan.pdf", 0, 100)
		if err != nil || !strings.Contains(result.Note, "OCR not supported") {
			t.Fatalf("%+v %v", result, err)
		}
	}
	if _, err := store.extract(context.Background(), "scan.pdf"); !errors.Is(err, ErrNeedsOCR) || calls != 1 {
		t.Fatalf("err %v after %d parses", err, calls)
	}
	// Transient failures are never cached.
	store.pdfExtractor = func(context.Context, []byte) (document, error) {
		calls++
		return document{}, errors.New("boom")
	}
	os.WriteFile(dir+"/flaky.pdf", []byte("fake"), 0600)
	calls = 0
	store.Read(context.Background(), "flaky.pdf", 0, 100)
	store.Read(context.Background(), "flaky.pdf", 0, 100)
	if calls != 2 {
		t.Fatalf("transient error cached: %d parses", calls)
	}
}

func TestCacheKeyVersion(t *testing.T) {
	dir := t.TempDir()
	store, _ := Open(dir, 1<<20, 1)
	defer store.Root.Close()
	os.WriteFile(dir+"/a.txt", []byte("fresh"), 0600)
	info, _ := os.Stat(dir + "/a.txt")
	name := func(version string) string {
		key := sha256.Sum256([]byte(fmt.Sprintf("%s:a.txt:%d:%d", version, info.Size(), info.ModTime().UnixNano())))
		return ".aulaglobal-cache/" + hex.EncodeToString(key[:]) + ".json"
	}
	store.Root.WriteFile(name("v1"), []byte(`{"pages":[{"page":0,"text":"stale"}]}`), 0600)
	result, err := store.Read(context.Background(), "a.txt", 0, 100)
	if err != nil || result.Text != "fresh" {
		t.Fatalf("%+v %v", result, err)
	}
	if _, err := store.Root.Stat(name("v2")); err != nil {
		t.Fatal("v2 cache entry missing")
	}
}

func TestTruncatedXMLNeverPanics(t *testing.T) {
	docx := "<w:document><w:body>" + docxParagraph(`<w:pStyle w:val="Ttulo1"/><w:numPr><w:ilvl w:val="1"/><w:numId w:val="2"/></w:numPr>`, docxRun("a &amp; b &#65; &bogus; &#x41;")) +
		"<w:tbl><w:tr>" + docxCell(`<w:gridSpan w:val="2"/>`, "x") + "</w:tr></w:tbl><![CDATA[raw]]><!-- c --><?pi x?></w:body></w:document>"
	slide := slideXML(shape(`<p:ph type="title"/>`, slideParagraph(` lvl="1"`, "t")), `<p:graphicFrame><a:tbl><a:tr><a:tc><a:txBody><a:p/></a:txBody></a:tc></a:tr></a:tbl></p:graphicFrame>`)
	sheet := `<worksheet><sheetData><row r="1"><c r="A1" t="s"><v>0</v></c><c r="C1" t="b"><v>1</v></c></row></sheetData></worksheet>`
	ods := odfContent("spreadsheet", `<table:table table:name="a"><table:table-row table:number-rows-repeated="3"><table:table-cell table:number-columns-repeated="2"><text:p>x</text:p></table:table-cell></table:table-row></table:table>`)
	for _, test := range []struct {
		path  string
		parts func(xml string) []zipPart
		xml   string
	}{
		{"a.docx", func(xml string) []zipPart {
			return []zipPart{{"word/document.xml", xml}, {"word/styles.xml", docxStyles}, {"word/numbering.xml", docxNumbering}}
		}, docx},
		{"a.pptx", func(xml string) []zipPart { return []zipPart{{"ppt/slides/slide1.xml", xml}} }, slide},
		{"a.xlsx", func(xml string) []zipPart {
			return []zipPart{{"xl/worksheets/sheet1.xml", xml}, {"xl/sharedStrings.xml", "<sst><si><t>s</t></si></sst>"}}
		}, sheet},
		{"a.ods", func(xml string) []zipPart { return []zipPart{{"content.xml", xml}} }, ods},
	} {
		for cut := 0; cut <= len(test.xml); cut += 3 {
			decodeDocument(context.Background(), test.path, buildZip(t, test.parts(test.xml[:cut])...))
		}
	}
}
