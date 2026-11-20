package store

import (
	"archive/zip"
	"strconv"
)

type sharedStrings struct {
	data []byte
	ends []int
}

func (pool *sharedStrings) get(index int) []byte {
	if pool == nil || index < 0 || index >= len(pool.ends) {
		return nil
	}
	start := 0
	if index > 0 {
		start = pool.ends[index-1]
	}
	return pool.data[start:pool.ends[index]]
}

// loadSharedStrings reads xl/sharedStrings.xml once into one buffer; count and size are capped.
func (document *zipDocument) loadSharedStrings() (*sharedStrings, error) {
	pool := &sharedStrings{}
	entry := document.find("xl/sharedStrings.xml")
	if entry == nil {
		return pool, nil
	}
	scan, err := document.scanner(entry)
	if err != nil {
		return nil, err
	}
	inText, skip := false, 0
	for {
		kind, err := scan.next()
		if err != nil {
			return nil, err
		}
		if kind == tokenEOF {
			return pool, nil
		}
		if err = document.check(nil); err != nil {
			return nil, err
		}
		if len(pool.data) > textCap || len(pool.ends) > maxTableCells {
			return nil, fail(ErrLimit, "shared strings limit")
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
				pool.data = append(pool.data, scan.text...)
			}
		case tokenStart:
			switch string(scan.name) {
			case "rPh":
				skip = 1
			case "t":
				inText = true
			}
		case tokenEnd:
			switch string(scan.name) {
			case "t":
				inText = false
			case "si":
				pool.ends = append(pool.ends, len(pool.data))
			}
		}
	}
}

// columnIndex converts the letters of a cell reference such as "AB12" to a zero-based column.
func columnIndex(reference []byte) int {
	column := 0
	for index, char := range reference {
		char &^= 0x20
		if char < 'A' || char > 'Z' {
			if index == 0 {
				return -1
			}
			break
		}
		if column = column*26 + int(char-'A') + 1; column > 1<<20 {
			return -1
		}
	}
	return column - 1
}

func (document *zipDocument) decodeXLSX(sink *pageSink, md *markdown) error {
	document.buildIndex()
	sheets, err := document.orderedParts("xl/workbook.xml", "xl/_rels/workbook.xml.rels", "xl", "sheet")
	if err != nil {
		return err
	}
	if len(sheets) == 0 {
		sheets = document.numberedParts("xl/worksheets/sheet")
	}
	var shared *sharedStrings
	for _, sheet := range sheets {
		name := sheet.name
		if name == "" {
			name = "Sheet " + strconv.Itoa(sheet.number)
		}
		md.textString(name)
		md.heading(2)
		md.tableBegin(true)
		if err = document.parseSheet(sheet.entry, md, &shared); err != nil {
			return err
		}
		md.tableEnd()
		if err = sink.add(sheet.number, md.page()); err != nil {
			return err
		}
	}
	return nil
}

func (document *zipDocument) parseSheet(entry *zip.File, md *markdown, shared **sharedStrings) error {
	scan, err := document.scanner(entry)
	if err != nil {
		return err
	}
	var (
		collect, inCell, rowHasContent bool
		skip                           int
		cellType                       byte
		cellColumn, nextColumn         int
		rowNumber, lastContentRow      int
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
			if collect {
				md.text(scan.text)
			}
		case tokenStart:
			switch string(scan.name) {
			case "rPh":
				skip = 1
			case "row":
				rowNumber = scan.attrInt("r", rowNumber+1)
				nextColumn, rowHasContent = 0, false
			case "c":
				inCell, cellType = true, 0
				if value := scan.attr("t"); len(value) == 1 {
					cellType = value[0]
				}
				if cellColumn = columnIndex(scan.attr("r")); cellColumn < 0 {
					cellColumn = nextColumn
				}
				md.inline = md.inline[:0]
			case "v", "t":
				collect = inCell
			}
		case tokenEnd:
			switch string(scan.name) {
			case "v", "t":
				collect = false
			case "c":
				inCell = false
				if len(md.inline) == 0 || cellColumn < nextColumn || cellColumn >= maxTableColumns {
					md.inline = md.inline[:0]
					break
				}
				switch cellType {
				case 's':
					if *shared == nil {
						if *shared, err = document.loadSharedStrings(); err != nil {
							return err
						}
					}
					index, _ := strconv.Atoi(string(md.inline))
					md.inline = append(md.inline[:0], (*shared).get(index)...)
				case 'b':
					if len(md.inline) == 1 && md.inline[0] == '1' {
						md.inline = append(md.inline[:0], "TRUE"...)
					} else if len(md.inline) == 1 && md.inline[0] == '0' {
						md.inline = append(md.inline[:0], "FALSE"...)
					}
				}
				md.emptyCells(cellColumn - nextColumn)
				md.cellEnd(1)
				nextColumn, rowHasContent = cellColumn+1, true
			case "row":
				if !rowHasContent {
					md.dropRow()
					break
				}
				skipped := 0
				if lastContentRow > 0 {
					skipped = rowNumber - lastContentRow - 1
				}
				md.rowEnd(skipped)
				lastContentRow = rowNumber
			case "worksheet":
				return nil
			}
		}
	}
}
