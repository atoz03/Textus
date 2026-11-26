package extract

import (
	"archive/zip"
	"bytes"
	"strconv"
	"strings"
)

// OpenDocument spreadsheets (.ods): content.xml's tables, each a sheet. A
// cell reads as calamine read it - a number, date, time or truth value by
// its value, anything else by its paragraphs - and a row or cell written
// once with a repeat count counts that many times.

func odsSheets(z *zip.Reader) ([]*sheet, error) {
	// An .odt renamed is no spreadsheet.
	if mime, ok := zipEntry(z, "mimetype"); ok && !bytes.HasPrefix(mime, []byte("application/vnd.oasis.opendocument.spreadsheet")) {
		return nil, errSpreadsheet
	}
	content, ok := zipEntry(z, "content.xml")
	if !ok {
		return nil, errSpreadsheet
	}
	var sheets []*sheet
	x := newPull(content)
	for n := x.next(); n.kind != nodeEnd; n = x.next() {
		if n.kind == nodeOpen && n.tag.name == "table" {
			s := &sheet{name: n.tag.val("name")}
			odsTable(x, s)
			sheets = append(sheets, s)
		}
	}
	return sheets, nil
}

// odsTable reads the rows of the table just opened into s: in the table
// itself, its header rows or its row groups.
func odsTable(x *pull, s *sheet) {
	x.walk(func(t tag) bool {
		switch t.name {
		case "table-row":
			s.add(odsRow(x), odsRepeat(t, "number-rows-repeated"))
		case "table-header-rows", "table-row-group", "table-rows":
			return false
		default:
			x.skip()
		}
		return true
	})
}

// odsRow is the cells with text of the row just opened.
func odsRow(x *pull) []sheetCell {
	var cells []sheetCell
	col := 0
	x.walk(func(t tag) bool {
		if t.name != "table-cell" && t.name != "covered-table-cell" {
			x.skip()
			return true
		}
		n := odsRepeat(t, "number-columns-repeated")
		if text := odsCell(x, t); strings.TrimSpace(text) != "" {
			for c := col; c < min(col+n, maxSheetColumns); c++ {
				cells = append(cells, sheetCell{c, text})
			}
		}
		col = min(col+n, maxSheetColumns)
		return true
	})
	return cells
}

// odsRepeat is a row's or cell's repeat count: 1 unless it says more.
func odsRepeat(t tag, name string) int {
	n, err := strconv.ParseUint(t.val(name), 10, 32)
	if err != nil || n == 0 {
		return 1
	}
	return int(n)
}

// odsCell is the text of the cell c, just opened.
func odsCell(x *pull, c tag) string {
	text := odsParagraphs(x)
	switch c.val("value-type") {
	case "float", "percentage", "currency":
		if f, err := strconv.ParseFloat(c.val("value"), 64); err == nil {
			return sheetNumber(f)
		}
	case "date":
		return c.val("date-value")
	case "time":
		return c.val("time-value")
	case "boolean":
		return strconv.FormatBool(c.val("boolean-value") == "true")
	}
	if text == "" {
		return c.val("string-value")
	}
	return text
}

// odsParagraphs is the text of the cell just opened, a line a paragraph;
// a comment on the cell (office:annotation) is none of it.
func odsParagraphs(x *pull) string {
	var lines []string
	x.walk(func(t tag) bool {
		if t.name == "p" || t.name == "h" {
			lines = append(lines, odsText(x))
		} else {
			x.skip()
		}
		return true
	})
	return strings.Join(lines, "\n")
}

// odsText is the text of the paragraph or span just opened: <text:s
// text:c="3"/> as its spaces, tabs and line breaks as such, notes and
// frames left out.
func odsText(x *pull) string {
	var out strings.Builder
	for n := x.next(); n.kind == nodeOpen || n.kind == nodeText; n = x.next() {
		if n.kind == nodeText {
			out.WriteString(n.text)
			continue
		}
		switch n.tag.name {
		case "s":
			// Past a line's width, more spaces read as no more.
			out.WriteString(strings.Repeat(" ", min(odsRepeat(n.tag, "c"), 80)))
			x.skip()
		case "tab":
			out.WriteByte('\t')
			x.skip()
		case "line-break":
			out.WriteByte('\n')
			x.skip()
		case "note", "annotation", "frame":
			x.skip()
		default:
			out.WriteString(odsText(x))
		}
	}
	return out.String()
}
