package store

import (
	"bytes"
	"strconv"
	"sync"
)

const (
	maxListDepth    = 8
	maxTableColumns = 1024
	maxTableCells   = 1 << 20
	maxEmptyRows    = 8
)

type blockKind uint8

const (
	blockText blockKind = iota
	blockList
	blockTable
)

// markdown is the single serializer behind every document format. Parsers push
// inline text and close blocks; escaping and layout live here. Only the table
// being read is buffered, because GFM needs the column count before the header.
type markdown struct {
	out     []byte
	inline  []byte
	last    blockKind
	lead    string
	numbers [maxListDepth + 1]int
	err     error

	tableDepth int
	table      markdownTable
}

type markdownTable struct {
	trim     bool
	data     []byte
	cellEnds []int
	rowEnds  []int
}

func (md *markdown) size() int { return len(md.out) + len(md.inline) + len(md.table.data) }

func (md *markdown) fail(err error) {
	if md.err == nil {
		md.err = err
	}
}

func (md *markdown) text(data []byte) { md.inline = append(md.inline, data...) }

func (md *markdown) textString(text string) { md.inline = append(md.inline, text...) }

func (md *markdown) space() {
	if n := len(md.inline); n > 0 && md.inline[n-1] != ' ' && md.inline[n-1] != '\n' {
		md.inline = append(md.inline, ' ')
	}
}

func (md *markdown) spaces(count int) {
	for range min(count, 64) {
		md.inline = append(md.inline, ' ')
	}
}

func (md *markdown) lineBreak() { md.inline = append(md.inline, '\n') }

// collapse appends text the way ODF reads it: whitespace runs become one space.
func (md *markdown) collapse(data []byte) {
	for _, char := range data {
		if char == ' ' || char == '\t' || char == '\n' || char == '\r' {
			if n := len(md.inline); n > 0 && md.inline[n-1] != ' ' && md.inline[n-1] != '\n' {
				md.inline = append(md.inline, ' ')
			}
			continue
		}
		md.inline = append(md.inline, char)
	}
}

func (md *markdown) discard() { md.inline = md.inline[:0] }

// setLead schedules a label block that is written only if content follows it.
func (md *markdown) setLead(label string) { md.lead = label }

func (md *markdown) startBlock(kind blockKind) {
	if md.lead != "" {
		lead := md.lead
		md.lead = ""
		md.startBlock(blockText)
		md.out = append(md.out, lead...)
	}
	if len(md.out) > 0 {
		if kind == blockList && md.last == blockList {
			md.out = append(md.out, '\n')
		} else {
			md.out = append(md.out, '\n', '\n')
		}
	}
	if kind != blockList {
		md.numbers = [maxListDepth + 1]int{}
	}
	md.last = kind
}

// closeInCell ends a paragraph inside a table cell, which keeps one text stream.
func (md *markdown) closeInCell() {
	md.inline = bytes.TrimRight(md.inline, " \t\r\n")
	if len(md.inline) > 0 {
		md.inline = append(md.inline, '\n')
	}
}

func (md *markdown) oneLine() []byte {
	text := bytes.TrimSpace(md.inline)
	for index, char := range text {
		if char == '\n' || char == '\r' {
			text[index] = ' '
		}
	}
	return text
}

func (md *markdown) paragraph() {
	if md.tableDepth > 0 {
		md.closeInCell()
		return
	}
	text := bytes.TrimSpace(md.inline)
	if len(text) > 0 {
		md.startBlock(blockText)
		if needsEscape(text) {
			md.out = append(md.out, '\\')
		}
		md.out = append(md.out, text...)
	}
	md.inline = md.inline[:0]
}

func needsEscape(text []byte) bool {
	switch text[0] {
	case '#', '>', '|':
		return true
	case '-', '+', '*':
		return len(text) > 1 && text[1] == ' '
	}
	index := 0
	for index < len(text) && text[index] >= '0' && text[index] <= '9' {
		index++
	}
	return index > 0 && index+1 < len(text) && (text[index] == '.' || text[index] == ')') && text[index+1] == ' '
}

func (md *markdown) heading(level int) {
	if md.tableDepth > 0 {
		md.closeInCell()
		return
	}
	text := md.oneLine()
	if len(text) > 0 {
		md.startBlock(blockText)
		for range min(max(level, 1), 6) {
			md.out = append(md.out, '#')
		}
		md.out = append(md.out, ' ')
		md.out = append(md.out, text...)
	}
	md.inline = md.inline[:0]
}

func (md *markdown) listItem(depth int, ordered bool) {
	if md.tableDepth > 0 {
		md.closeInCell()
		return
	}
	text := md.oneLine()
	if len(text) > 0 {
		depth = min(max(depth, 0), maxListDepth)
		md.startBlock(blockList)
		for range depth {
			md.out = append(md.out, ' ', ' ')
		}
		if ordered {
			md.numbers[depth]++
			md.out = strconv.AppendInt(md.out, int64(md.numbers[depth]), 10)
			md.out = append(md.out, '.', ' ')
		} else {
			md.numbers[depth] = 0
			md.out = append(md.out, '-', ' ')
		}
		for deeper := depth + 1; deeper <= maxListDepth; deeper++ {
			md.numbers[deeper] = 0
		}
		md.out = append(md.out, text...)
	}
	md.inline = md.inline[:0]
}

// tableBegin opens a table. Tables inside a cell are flattened into the cell text.
func (md *markdown) tableBegin(trim bool) {
	if md.tableDepth == 0 {
		md.paragraph()
		md.table.trim = trim
		md.table.data = md.table.data[:0]
		md.table.cellEnds = md.table.cellEnds[:0]
		md.table.rowEnds = md.table.rowEnds[:0]
	}
	md.tableDepth++
}

func (md *markdown) rowCells() int {
	table := &md.table
	if len(table.rowEnds) == 0 {
		return len(table.cellEnds)
	}
	return len(table.cellEnds) - table.rowEnds[len(table.rowEnds)-1]
}

func (md *markdown) addCell(text []byte) {
	table := &md.table
	if len(table.cellEnds) >= maxTableCells {
		md.fail(fail(ErrLimit, "table cell limit"))
		return
	}
	if md.rowCells() >= maxTableColumns {
		return
	}
	for _, char := range text {
		switch char {
		case '|', '\\':
			table.data = append(table.data, '\\', char)
		case '\n':
			table.data = append(table.data, "<br>"...)
		case '\r':
		default:
			table.data = append(table.data, char)
		}
	}
	table.cellEnds = append(table.cellEnds, len(table.data))
}

// cellEnd stores the collected text as a cell; spanned columns become empty cells.
func (md *markdown) cellEnd(span int) {
	if md.tableDepth != 1 {
		md.space()
		return
	}
	md.addCell(bytes.TrimSpace(md.inline))
	md.inline = md.inline[:0]
	md.emptyCells(span - 1)
}

func (md *markdown) emptyCells(count int) {
	if md.tableDepth != 1 {
		return
	}
	count = min(count, maxTableColumns-md.rowCells())
	for range max(count, 0) {
		md.addCell(nil)
	}
}

func (md *markdown) repeatCell(extra int) {
	table := &md.table
	if extra <= 0 || md.tableDepth != 1 || len(table.cellEnds) == 0 {
		return
	}
	end := table.cellEnds[len(table.cellEnds)-1]
	start := 0
	if len(table.cellEnds) > 1 {
		start = table.cellEnds[len(table.cellEnds)-2]
	}
	cell := bytes.Clone(table.data[start:end])
	for range min(max(extra, 0), maxTableColumns) {
		if md.rowCells() >= maxTableColumns || len(table.cellEnds) >= maxTableCells {
			return
		}
		table.data = append(table.data, cell...)
		table.cellEnds = append(table.cellEnds, len(table.data))
	}
}

// rowEnd closes a row, preceded by a bounded number of skipped empty rows.
func (md *markdown) rowEnd(skippedBefore int) {
	if md.tableDepth != 1 {
		md.space()
		return
	}
	table := &md.table
	start := len(table.cellEnds) - md.rowCells()
	for range min(max(skippedBefore, 0), maxEmptyRows) {
		table.rowEnds = append(table.rowEnds, start)
	}
	table.rowEnds = append(table.rowEnds, len(table.cellEnds))
}

// dropRow discards the cells of the row being read.
func (md *markdown) dropRow() {
	table := &md.table
	if md.tableDepth != 1 {
		return
	}
	keep := len(table.cellEnds) - md.rowCells()
	table.cellEnds = table.cellEnds[:keep]
	if keep == 0 {
		table.data = table.data[:0]
	} else {
		table.data = table.data[:table.cellEnds[keep-1]]
	}
	md.inline = md.inline[:0]
}

// repeatRow duplicates the last closed row.
func (md *markdown) repeatRow(extra int) {
	table := &md.table
	if extra <= 0 || md.tableDepth != 1 || len(table.rowEnds) == 0 {
		return
	}
	end := table.rowEnds[len(table.rowEnds)-1]
	start := 0
	if len(table.rowEnds) > 1 {
		start = table.rowEnds[len(table.rowEnds)-2]
	}
	if end == start {
		return
	}
	dataStart := 0
	if start > 0 {
		dataStart = table.cellEnds[start-1]
	}
	rowData := bytes.Clone(table.data[dataStart:table.cellEnds[end-1]])
	rowCells := make([]int, end-start)
	for index := range rowCells {
		rowCells[index] = table.cellEnds[start+index] - dataStart
	}
	for range extra {
		if len(table.cellEnds)+len(rowCells) > maxTableCells || len(table.data)+len(rowData) > textCap {
			md.fail(fail(ErrLimit, "table cell limit"))
			return
		}
		base := len(table.data)
		table.data = append(table.data, rowData...)
		for _, cellEnd := range rowCells {
			table.cellEnds = append(table.cellEnds, base+cellEnd)
		}
		table.rowEnds = append(table.rowEnds, len(table.cellEnds))
	}
}

func (md *markdown) cellLength(index int) int {
	if index == 0 {
		return md.table.cellEnds[0]
	}
	return md.table.cellEnds[index] - md.table.cellEnds[index-1]
}

// tableEnd renders the table as GFM when the outermost table closes.
func (md *markdown) tableEnd() {
	if md.tableDepth == 0 {
		return
	}
	md.tableDepth--
	if md.tableDepth > 0 {
		return
	}
	table := &md.table
	rows, width := len(table.rowEnds), 0
	effective := func(row int) int {
		start := 0
		if row > 0 {
			start = table.rowEnds[row-1]
		}
		count := table.rowEnds[row] - start
		for table.trim && count > 0 && md.cellLength(start+count-1) == 0 {
			count--
		}
		return count
	}
	for table.trim && rows > 0 && effective(rows-1) == 0 {
		rows--
	}
	for row := range rows {
		width = max(width, effective(row))
	}
	if rows == 0 || width == 0 {
		return
	}
	md.startBlock(blockTable)
	for row := range rows {
		start, end := 0, table.rowEnds[row]
		if row > 0 {
			start = table.rowEnds[row-1]
		}
		if row > 0 {
			md.out = append(md.out, '\n')
		}
		md.out = append(md.out, '|')
		for column := range width {
			if start+column < end {
				cellStart := 0
				if start+column > 0 {
					cellStart = table.cellEnds[start+column-1]
				}
				if cell := table.data[cellStart:table.cellEnds[start+column]]; len(cell) > 0 {
					md.out = append(md.out, ' ')
					md.out = append(md.out, cell...)
				}
			}
			md.out = append(md.out, ' ', '|')
		}
		if row == 0 {
			md.out = append(md.out, '\n', '|')
			for range width {
				md.out = append(md.out, " --- |"...)
			}
		}
	}
}

// page returns the Markdown of the current page and resets the writer.
func (md *markdown) page() string {
	md.paragraph()
	md.tableDepth = 0
	text := ""
	if len(md.out) > 0 {
		md.out = append(md.out, '\n')
		text = string(md.out)
	}
	md.out = md.out[:0]
	md.lead = ""
	md.numbers = [maxListDepth + 1]int{}
	return text
}

var markdownPool = sync.Pool{New: func() any { return new(markdown) }}

func acquireMarkdown() *markdown { return markdownPool.Get().(*markdown) }

// release keeps modest buffers for the next document and drops the ones a huge document grew.
func (md *markdown) release() {
	table := md.table
	if cap(md.out) > 1<<20 || cap(md.inline) > 1<<20 || cap(table.data) > 1<<20 || cap(table.cellEnds) > 1<<17 || cap(table.rowEnds) > 1<<17 {
		return
	}
	*md = markdown{out: md.out[:0], inline: md.inline[:0], table: markdownTable{data: table.data[:0], cellEnds: table.cellEnds[:0], rowEnds: table.rowEnds[:0]}}
	markdownPool.Put(md)
}
