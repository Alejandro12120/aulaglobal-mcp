package store

import (
	"archive/zip"
	"path"
	"strings"
)

const maxStyles = 4096

type docxStyle struct {
	heading int8
	list    bool
	basedOn string
}

// docxContext resolves style and numbering references; both parts are read only if a paragraph needs them.
type docxContext struct {
	document      *zipDocument
	styles        map[string]docxStyle
	stylesLoaded  bool
	orderedLevels map[string]uint16
	numToAbstract map[string]string
	numberingRead bool
}

func headingFromName(name string) int8 {
	if strings.EqualFold(name, "title") {
		return 1
	}
	if len(name) == 9 && strings.EqualFold(name[:8], "heading ") && name[8] >= '1' && name[8] <= '9' {
		return int8(min(name[8]-'0', 6))
	}
	return 0
}

func (docx *docxContext) loadStyles() error {
	docx.stylesLoaded = true
	entry := docx.document.find("word/styles.xml")
	if entry == nil {
		return nil
	}
	scan, err := docx.document.scanner(entry)
	if err != nil {
		return err
	}
	docx.styles = map[string]docxStyle{}
	var current docxStyle
	id := ""
	for {
		kind, err := scan.next()
		if err != nil || kind == tokenEOF {
			return err
		}
		if err = docx.document.check(nil); err != nil {
			return err
		}
		switch kind {
		case tokenStart:
			switch string(scan.name) {
			case "style":
				id, current = scan.attrString("styleId"), docxStyle{}
			case "name":
				if id != "" {
					current.heading = headingFromName(scan.attrString("val"))
				}
			case "basedOn":
				current.basedOn = scan.attrString("val")
			case "outlineLvl":
				if level := scan.attrInt("val", 9); level < 9 && current.heading == 0 {
					current.heading = int8(min(level+1, 6))
				}
			case "numId":
				current.list = string(scan.attr("val")) != "0"
			}
		case tokenEnd:
			if string(scan.name) == "style" && id != "" {
				if len(docx.styles) >= maxStyles {
					return fail(ErrLimit, "style limit")
				}
				docx.styles[id] = current
				id = ""
			}
		}
	}
}

func (docx *docxContext) loadNumbering() error {
	docx.numberingRead = true
	entry := docx.document.find("word/numbering.xml")
	if entry == nil {
		return nil
	}
	scan, err := docx.document.scanner(entry)
	if err != nil {
		return err
	}
	docx.orderedLevels, docx.numToAbstract = map[string]uint16{}, map[string]string{}
	abstract, number, level := "", "", -1
	for {
		kind, err := scan.next()
		if err != nil || kind == tokenEOF {
			return err
		}
		if err = docx.document.check(nil); err != nil {
			return err
		}
		switch kind {
		case tokenStart:
			switch string(scan.name) {
			case "abstractNum":
				abstract, level = scan.attrString("abstractNumId"), -1
			case "lvl":
				level = scan.attrInt("ilvl", -1)
			case "numFmt":
				if format := string(scan.attr("val")); abstract != "" && level >= 0 && level < 16 && format != "bullet" && format != "none" {
					docx.orderedLevels[abstract] |= 1 << level
				}
			case "num":
				number = scan.attrString("numId")
			case "abstractNumId":
				if number != "" {
					docx.numToAbstract[number] = scan.attrString("val")
				}
			}
		case tokenEnd:
			switch string(scan.name) {
			case "abstractNum":
				abstract = ""
			case "num":
				number = ""
			}
		}
		if len(docx.numToAbstract) > maxStyles || len(docx.orderedLevels) > maxStyles {
			return fail(ErrLimit, "numbering limit")
		}
	}
}

func (docx *docxContext) styleOf(id string) (heading int, list bool) {
	if !docx.stylesLoaded {
		if err := docx.loadStyles(); err != nil {
			docx.styles = nil
		}
	}
	for hops := 0; id != "" && hops < 8; hops++ {
		style, found := docx.styles[id]
		if !found {
			break
		}
		if style.heading > 0 {
			return int(style.heading), style.list
		}
		list = list || style.list
		id = style.basedOn
	}
	return 0, list
}

func (docx *docxContext) ordered(number string, level int) bool {
	if !docx.numberingRead {
		if err := docx.loadNumbering(); err != nil {
			docx.numToAbstract = nil
		}
	}
	return level >= 0 && level < 16 && docx.orderedLevels[docx.numToAbstract[number]]>>level&1 == 1
}

func (document *zipDocument) decodeDOCX(sink *pageSink, md *markdown) error {
	entry := document.find("word/document.xml")
	if entry == nil {
		return errXML
	}
	scan, err := document.scanner(entry)
	if err != nil {
		return err
	}
	docx := &docxContext{document: document}
	var (
		inText                  bool
		skip, span              = 0, 1
		styleID, numberID       string
		listLevel, outlineLevel = -1, -1
	)
	flush := func() {
		heading := 0
		if outlineLevel >= 0 && outlineLevel < 9 {
			heading = min(outlineLevel+1, 6)
		}
		styleList := false
		if styleID != "" {
			var fromStyle int
			fromStyle, styleList = docx.styleOf(styleID)
			if heading == 0 {
				heading = fromStyle
			}
		}
		switch {
		case heading > 0:
			md.heading(heading)
		case numberID != "" && numberID != "0":
			md.listItem(max(listLevel, 0), docx.ordered(numberID, listLevel))
		case numberID == "" && styleList:
			md.listItem(max(listLevel, 0), false)
		default:
			md.paragraph()
		}
		styleID, numberID, listLevel, outlineLevel = "", "", -1, -1
	}
	for {
		kind, err := scan.next()
		if err != nil {
			return err
		}
		if kind == tokenEOF {
			break
		}
		if err = document.check(md); err != nil {
			return err
		}
		if skip > 0 {
			switch kind {
			case tokenStart:
				skip++
			case tokenEnd:
				skip--
			}
			continue
		}
		switch kind {
		case tokenText:
			if inText {
				md.text(scan.text)
			}
		case tokenStart:
			switch string(scan.name) {
			case "del", "moveFrom", "pPrChange", "rPrChange", "sectPrChange", "tblPrChange", "trPrChange", "tcPrChange", "Fallback":
				skip = 1
			case "p":
				if len(md.inline) > 0 {
					flush()
				}
				styleID, numberID, listLevel, outlineLevel = "", "", -1, -1
			case "pStyle":
				styleID = scan.attrString("val")
			case "ilvl":
				listLevel = scan.attrInt("val", -1)
			case "numId":
				numberID = scan.attrString("val")
			case "outlineLvl":
				outlineLevel = scan.attrInt("val", -1)
			case "t":
				inText = true
			case "tab":
				md.space()
			case "br", "cr":
				if string(scan.attr("type")) != "page" {
					md.lineBreak()
				}
			case "noBreakHyphen":
				md.textString("-")
			case "tbl":
				md.tableBegin(false)
			case "tc":
				span = 1
			case "gridSpan":
				span = min(max(scan.attrInt("val", 1), 1), maxTableColumns)
			}
		case tokenEnd:
			switch string(scan.name) {
			case "t":
				inText = false
			case "p":
				flush()
			case "tc":
				md.cellEnd(span)
				span = 1
			case "tr":
				md.rowEnd(0)
			case "tbl":
				md.tableEnd()
			}
		}
	}
	return sink.add(0, md.page())
}

// shapeState describes the shape being read; slides and notes share one parser.
type shapeState struct {
	skip, title, body bool
}

const (
	bulletUnset = iota
	bulletOn
	bulletOff
)

func (document *zipDocument) parsePresentationPart(entry *zip.File, md *markdown, notes bool) error {
	scan, err := document.scanner(entry)
	if err != nil {
		return err
	}
	var (
		shape        shapeState
		inText, para bool
		skip, level  int
		bullet       = bulletUnset
		ordered      bool
	)
	for {
		kind, err := scan.next()
		if err != nil {
			return err
		}
		if kind == tokenEOF {
			return nil
		}
		if err = document.check(md); err != nil {
			return err
		}
		if skip > 0 {
			switch kind {
			case tokenStart:
				skip++
			case tokenEnd:
				skip--
			}
			continue
		}
		switch kind {
		case tokenText:
			if inText {
				md.text(scan.text)
			}
		case tokenStart:
			switch string(scan.name) {
			case "Fallback":
				skip = 1
			case "sp", "pic", "graphicFrame", "cxnSp":
				shape = shapeState{}
			case "ph":
				switch string(scan.attr("type")) {
				case "title", "ctrTitle":
					shape.title = !notes
				case "sldNum", "sldImg", "hdr", "ftr", "dt":
					shape.skip = true
				case "", "body", "obj":
					shape.body = !notes
				}
			case "p":
				para, level, bullet, ordered = true, 0, bulletUnset, false
			case "pPr":
				if para {
					level = min(scan.attrInt("lvl", 0), maxListDepth)
				}
			case "buNone":
				if para {
					bullet = bulletOff
				}
			case "buChar", "buBlip":
				if para {
					bullet = bulletOn
				}
			case "buAutoNum":
				if para {
					bullet, ordered = bulletOn, true
				}
			case "t":
				inText = true
			case "br":
				md.lineBreak()
			case "tbl":
				md.tableBegin(false)
			}
		case tokenEnd:
			switch string(scan.name) {
			case "t":
				inText = false
			case "p":
				para = false
				switch {
				case md.tableDepth > 0:
					md.paragraph()
				case shape.skip:
					md.discard()
				case shape.title:
					md.space()
				case notes || bullet == bulletOff:
					md.paragraph()
				case bullet == bulletOn || level > 0 || shape.body:
					md.listItem(level, ordered)
				default:
					md.paragraph()
				}
			case "tc":
				md.cellEnd(1)
			case "tr":
				md.rowEnd(0)
			case "tbl":
				md.tableEnd()
			case "sp":
				if shape.title {
					md.heading(2)
				}
				shape = shapeState{}
			}
		}
	}
}

func (document *zipDocument) slideOrder() ([]partRef, error) {
	parts, err := document.orderedParts("ppt/presentation.xml", "ppt/_rels/presentation.xml.rels", "ppt", "sldId")
	if err != nil || len(parts) > 0 {
		return parts, err
	}
	return document.numberedParts("ppt/slides/slide"), nil
}

func (document *zipDocument) notesPart(slide partRef) *zip.File {
	rels := document.find("ppt/slides/_rels/" + path.Base(slide.entry.Name) + ".rels")
	if rels == nil {
		return nil
	}
	var target string
	if document.relationships(rels, func(id, kind, found string) bool {
		if strings.HasSuffix(kind, "/notesSlide") {
			target = found
			return false
		}
		return true
	}) != nil || target == "" {
		return nil
	}
	return document.find(resolveTarget("ppt/slides", target))
}

func (document *zipDocument) decodePPTX(sink *pageSink, md *markdown) error {
	document.buildIndex()
	slides, err := document.slideOrder()
	if err != nil {
		return err
	}
	for _, slide := range slides {
		if err = document.parsePresentationPart(slide.entry, md, false); err != nil {
			return err
		}
		if notes := document.notesPart(slide); notes != nil {
			md.setLead("**Notes:**")
			if err = document.parsePresentationPart(notes, md, true); err != nil {
				return err
			}
		}
		if err = sink.add(slide.number, md.page()); err != nil {
			return err
		}
	}
	return nil
}
