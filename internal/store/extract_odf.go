package store

import (
	"bytes"
)

const maxRepeat = 1 << 20

func repeatCount(scan *xmlScanner, local string) int {
	return min(max(scan.attrInt(local, 1), 1), maxRepeat)
}

// valueAttribute returns the stored value of an ODS cell, used when it has no display text.
func valueAttribute(scan *xmlScanner) string {
	for index := 0; ; {
		name, value, ok := scan.nextAttr(&index)
		if !ok {
			return ""
		}
		switch string(localName(name)) {
		case "value", "string-value", "date-value", "time-value", "boolean-value":
			if len(value) > 0 {
				return string(scan.decode(value))
			}
		}
	}
}

// decodeODF reads content.xml of an odt, odp or ods in a single pass.
// Spanned columns need no handling: ODF lists covered-table-cell elements explicitly.
func (document *zipDocument) decodeODF(docFormat format, sink *pageSink, md *markdown) error {
	entry := document.find("content.xml")
	if entry == nil {
		return errXML
	}
	scan, err := document.scanner(entry)
	if err != nil {
		return err
	}
	sheets := docFormat == formatODS
	var (
		skip, paraDepth, headingLevel, listDepth int
		slide, sheet, inSheet                    int
		frameDepth, classDepth                   int
		frameClass                               string
		inNotes                                  bool
		cellRepeat, rowRepeat                    = 1, 1
		pendingRows, pendingCells                int
		rowHasContent                            bool
		cellValue                                string
	)
	endParagraph := func() {
		heading := headingLevel
		headingLevel = 0
		switch {
		// Some producers omit presentation:class on the notes frame; only a frame that names another class is dropped.
		case inNotes && frameClass != "" && frameClass != "notes":
			md.discard()
		case !inNotes && (frameClass == "header" || frameClass == "footer" || frameClass == "date-time" || frameClass == "page-number"):
			md.discard()
		case frameClass == "title" && !inNotes && md.tableDepth == 0:
			md.space()
		case heading > 0:
			md.heading(heading)
		case listDepth > 0:
			md.listItem(listDepth-1, false)
		default:
			md.paragraph()
		}
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
			if paraDepth > 0 {
				md.collapse(scan.text)
			}
		case tokenStart:
			switch string(scan.name) {
			case "annotation", "tracked-changes", "note":
				skip = 1
			case "shapes", "content-validations":
				if sheets {
					skip = 1
				}
			case "p", "h":
				paraDepth++
				if string(scan.name) == "h" {
					headingLevel = min(max(scan.attrInt("outline-level", 1), 1), 6)
				}
			case "s":
				md.spaces(scan.attrInt("c", 1))
			case "tab":
				md.spaces(1)
			case "line-break":
				md.lineBreak()
			case "list":
				listDepth++
			case "page":
				if docFormat == formatODP {
					slide++
				}
			case "notes":
				if docFormat == formatODP {
					inNotes = true
					md.setLead("**Notes:**")
				}
			case "frame":
				frameDepth++
				if class := scan.attr("class"); len(class) > 0 && classDepth == 0 {
					frameClass, classDepth = string(class), frameDepth
				}
			case "table":
				if !sheets {
					md.tableBegin(false)
				} else if inSheet == 0 {
					inSheet = 1
					sheet++
					md.textString(scan.attrString("name"))
					md.heading(2)
					md.tableBegin(true)
					pendingRows = 0
				} else {
					skip = 1
				}
			case "table-row":
				rowRepeat, rowHasContent, pendingCells = repeatCount(scan, "number-rows-repeated"), false, 0
			case "table-cell", "covered-table-cell":
				cellRepeat, cellValue = repeatCount(scan, "number-columns-repeated"), valueAttribute(scan)
				if md.tableDepth == 1 {
					md.inline = md.inline[:0]
				}
			}
		case tokenEnd:
			switch string(scan.name) {
			case "p", "h":
				if paraDepth--; paraDepth <= 0 {
					paraDepth = 0
					endParagraph()
				}
			case "list":
				listDepth = max(listDepth-1, 0)
			case "page":
				if docFormat == formatODP && slide > 0 {
					if err = sink.add(slide, md.page()); err != nil {
						return err
					}
					inNotes, frameClass, classDepth, frameDepth = false, "", 0, 0
				}
			case "notes":
				inNotes = false
				md.setLead("")
			case "frame":
				if frameClass == "title" && classDepth == frameDepth && !inNotes {
					md.heading(2)
				}
				if frameDepth == classDepth {
					frameClass, classDepth = "", 0
				}
				frameDepth = max(frameDepth-1, 0)
			case "table-cell", "covered-table-cell":
				if md.tableDepth != 1 {
					md.cellEnd(1)
					break
				}
				if sheets {
					empty := len(bytes.TrimSpace(md.inline)) == 0
					if empty && cellValue != "" {
						md.inline = append(md.inline[:0], cellValue...)
						empty = false
					}
					if empty {
						pendingCells += cellRepeat
						break
					}
					md.emptyCells(pendingCells)
					pendingCells, rowHasContent = 0, true
				}
				md.cellEnd(1)
				md.repeatCell(cellRepeat - 1)
			case "table-row":
				switch {
				case md.tableDepth != 1 || !sheets:
					md.rowEnd(0)
				case !rowHasContent:
					md.dropRow()
					pendingRows += rowRepeat
				default:
					md.rowEnd(pendingRows)
					md.repeatRow(rowRepeat - 1)
					pendingRows = 0
				}
			case "table":
				if !sheets {
					md.tableEnd()
				} else if inSheet == 1 {
					inSheet = 0
					md.tableEnd()
					if err = sink.add(sheet, md.page()); err != nil {
						return err
					}
				}
			}
		}
	}
	if docFormat == formatODS {
		return nil
	}
	return sink.add(slide, md.page())
}
