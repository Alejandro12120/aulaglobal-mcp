package store

import (
	"context"
	"testing"
	"time"
)

func FuzzOfficeXML(f *testing.F) {
	for _, seed := range []string{
		`<w:document><w:body><w:p><w:pPr><w:pStyle w:val="Ttulo1"/><w:numPr><w:ilvl w:val="1"/></w:numPr></w:pPr><w:r><w:t>a &amp; b</w:t></w:r></w:p><w:tbl><w:tr><w:tc><w:tcPr><w:gridSpan w:val="3"/></w:tcPr><w:p><w:r><w:t>x|y</w:t></w:r></w:p></w:tc></w:tr></w:tbl></w:body></w:document>`,
		`<p:sld><p:cSld><p:spTree><p:sp><p:nvSpPr><p:nvPr><p:ph type="title"/></p:nvPr></p:nvSpPr><p:txBody><a:p><a:pPr lvl="2"/><a:r><a:t>T</a:t></a:r></a:p></p:txBody></p:sp><a:tbl><a:tr><a:tc><a:txBody><a:p><a:r><a:t>c</a:t></a:r></a:p></a:txBody></a:tc></a:tr></a:tbl></p:spTree></p:cSld></p:sld>`,
		`<worksheet><sheetData><row r="1"><c r="ZZ9" t="s"><v>0</v></c><c r="A1" t="inlineStr"><is><t>i</t></is></c></row></sheetData></worksheet>`,
		`<office:document-content><office:body><office:spreadsheet><table:table table:name="S"><table:table-row table:number-rows-repeated="1048576"><table:table-cell table:number-columns-repeated="1024" office:value="1"><text:p>v</text:p></table:table-cell></table:table-row></table:table></office:spreadsheet></office:body></office:document-content>`,
		`<office:document-content><office:body><office:presentation><draw:page><draw:frame presentation:class="title"><draw:text-box><text:p>t</text:p></draw:text-box></draw:frame><presentation:notes><text:p>n</text:p></presentation:notes></draw:page></office:presentation></office:body></office:document-content>`,
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, xml string) {
		rels := `<Relationships><Relationship Id="rId1" Type="x/slide" Target="slides/slide1.xml"/><Relationship Id="rId2" Type="x/worksheet" Target="worksheets/sheet1.xml"/></Relationships>`
		inputs := map[string][]zipPart{
			"a.docx": {{"word/document.xml", xml}, {"word/styles.xml", xml}, {"word/numbering.xml", xml}},
			"a.pptx": {{"ppt/presentation.xml", `<p:presentation><p:sldIdLst><p:sldId r:id="rId1"/></p:sldIdLst></p:presentation>`}, {"ppt/_rels/presentation.xml.rels", rels}, {"ppt/slides/slide1.xml", xml}, {"ppt/slides/_rels/slide1.xml.rels", `<Relationships><Relationship Id="n" Type="x/notesSlide" Target="../notesSlides/notesSlide1.xml"/></Relationships>`}, {"ppt/notesSlides/notesSlide1.xml", xml}},
			"a.xlsx": {{"xl/workbook.xml", `<workbook><sheets><sheet name="s" r:id="rId2"/></sheets></workbook>`}, {"xl/_rels/workbook.xml.rels", rels}, {"xl/worksheets/sheet1.xml", xml}, {"xl/sharedStrings.xml", xml}},
			"a.odt":  {{"mimetype", "application/vnd.oasis.opendocument.text"}, {"content.xml", xml}},
			"a.odp":  {{"mimetype", "application/vnd.oasis.opendocument.presentation"}, {"content.xml", xml}},
			"a.ods":  {{"mimetype", "application/vnd.oasis.opendocument.spreadsheet"}, {"content.xml", xml}},
		}
		for name, parts := range inputs {
			start := time.Now()
			decodeDocument(context.Background(), name, buildZip(t, parts...))
			if elapsed := time.Since(start); elapsed > 2*time.Second {
				t.Fatalf("%s took %v", name, elapsed)
			}
		}
	})
}
