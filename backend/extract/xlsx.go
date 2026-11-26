package extract

import (
	"archive/zip"
	"strconv"
	"strings"
)

// .xlsx and .xlsm workbooks: the worksheets the workbook lists, in its
// order, each cell as Excel holds it - shared or inline text, a number (a
// date when its number format shows one), a truth value or an error.

// xlsxBook is what reading a worksheet needs from the rest of the
// workbook.
type xlsxBook struct {
	excelStyles
	strings []string
}

func xlsxSheets(z *zip.Reader) ([]*sheet, error) {
	workbook, ok := zipEntry(z, "xl/workbook.xml")
	if !ok {
		return nil, errSpreadsheet
	}
	data, _ := zipEntry(z, "xl/_rels/workbook.xml.rels")
	r := parseRels(data)
	var b xlsxBook
	if target, ok := r.ofKind("sharedStrings"); ok {
		data, _ := zipEntry(z, resolvePart("xl", target))
		b.strings = xlsxStrings(data)
	}
	if target, ok := r.ofKind("styles"); ok {
		data, _ := zipEntry(z, resolvePart("xl", target))
		b.dates = xlsxDates(data)
	}
	var names, parts []string
	x := newPull(workbook)
	for n := x.next(); n.kind != nodeEnd; n = x.next() {
		switch {
		case n.kind != nodeOpen:
		case n.tag.name == "workbookPr":
			v := n.tag.val("date1904")
			b.date1904 = v == "1" || v == "true"
		case n.tag.name == "sheet":
			if part, ok := xlsxWorksheet(r, n.tag.val("id")); ok {
				names, parts = append(names, n.tag.val("name")), append(parts, part)
			}
		}
	}
	var sheets []*sheet
	for i, part := range parts {
		if data, ok := zipEntry(z, part); ok {
			s := &sheet{name: names[i]}
			b.read(data, s)
			sheets = append(sheets, s)
		}
	}
	return sheets, nil
}

// xlsxWorksheet is the part of the sheet whose relationship is id, when
// it is a worksheet: a chart sheet has no cells.
func xlsxWorksheet(r rels, id string) (string, bool) {
	for _, x := range r {
		if x.id == id && strings.HasSuffix(x.kind, "/worksheet") {
			return resolvePart("xl", x.target), true
		}
	}
	return "", false
}

// xlsxStrings are the shared strings, in order.
func xlsxStrings(data []byte) []string {
	var out []string
	x := newPull(data)
	for n := x.next(); n.kind != nodeEnd; n = x.next() {
		if n.kind == nodeOpen && n.tag.name == "si" {
			out = append(out, xlsxText(x))
		}
	}
	return out
}

// xlsxText is the text of the string item just opened (a shared string, a
// cell's inline one): its runs' text without the phonetic guides (rPh)
// over it, Excel's _xHHHH_ escapes decoded.
func xlsxText(x *pull) string {
	var out strings.Builder
	x.walk(func(t tag) bool {
		switch t.name {
		case "t":
			out.WriteString(x.text())
		case "rPh", "phoneticPr":
			x.skip()
		default:
			return false
		}
		return true
	})
	return xlsxUnescape(out.String())
}

// xlsxUnescape decodes the _xHHHH_ escapes Excel writes for characters XML
// can't hold (_x000D_, a carriage return); _x005F_ escapes the underscore
// of text that reads like one.
func xlsxUnescape(s string) string {
	if !strings.Contains(s, "_x") {
		return s
	}
	var out strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '_' && i+7 <= len(s) && s[i+1] == 'x' && s[i+6] == '_' {
			if c, err := strconv.ParseUint(s[i+2:i+6], 16, 16); err == nil {
				out.WriteRune(rune(c))
				i += 6
				continue
			}
		}
		out.WriteByte(s[i])
	}
	return out.String()
}

// xlsxDates is, for each cell format (cellXfs) in order, whether its
// number format - one the workbook writes out (numFmts) or one of
// Excel's own - shows a date.
func xlsxDates(data []byte) []bool {
	formats := map[int]string{}
	var xfs []int
	x := newPull(data)
	for n := x.next(); n.kind != nodeEnd; n = x.next() {
		if n.kind != nodeOpen {
			continue
		}
		switch n.tag.name {
		case "numFmts":
			x.walk(func(t tag) bool {
				if id, err := strconv.Atoi(t.val("numFmtId")); t.name == "numFmt" && err == nil {
					formats[id] = t.val("formatCode")
				}
				x.skip()
				return true
			})
		case "cellXfs":
			x.walk(func(t tag) bool {
				if t.name == "xf" {
					id, _ := strconv.Atoi(t.val("numFmtId"))
					xfs = append(xfs, id)
				}
				x.skip()
				return true
			})
		}
	}
	return excelCellDates(xfs, formats)
}

// read reads a worksheet's rows into s.
func (b *xlsxBook) read(data []byte, s *sheet) {
	x := newPull(data)
	for n := x.next(); n.kind != nodeEnd; n = x.next() {
		if n.kind == nodeOpen && n.tag.name == "row" {
			s.add(b.row(x), 1)
		}
	}
}

// row is the cells with text of the row just opened. A cell written
// without its reference is the one after the cell before it.
func (b *xlsxBook) row(x *pull) []sheetCell {
	var cells []sheetCell
	col := -1
	x.walk(func(t tag) bool {
		if t.name != "c" {
			x.skip()
			return true
		}
		col = sheetColumn(t.val("r"), col+1)
		if text := b.cell(x, t); col < maxSheetColumns && strings.TrimSpace(text) != "" {
			cells = append(cells, sheetCell{col, text})
		}
		return true
	})
	return cells
}

// cell is the text of the cell c, just opened, as calamine read its value.
func (b *xlsxBook) cell(x *pull, c tag) string {
	var value, inline string
	x.walk(func(t tag) bool {
		switch t.name {
		case "v":
			value = x.text()
		case "is":
			inline = xlsxText(x)
		default:
			x.skip()
		}
		return true
	})
	v := strings.TrimSpace(value)
	switch c.val("t") {
	case "s":
		if i, err := strconv.Atoi(v); err == nil && i >= 0 && i < len(b.strings) {
			return b.strings[i]
		}
		return ""
	case "inlineStr":
		return inline
	case "str":
		return xlsxUnescape(value)
	case "b":
		if v == "" {
			return ""
		}
		return strconv.FormatBool(v != "0")
	case "e", "d":
		// An error (#DIV/0!) as Excel writes it; a date in ISO 8601.
		return v
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return v
	}
	style, _ := strconv.Atoi(c.val("s"))
	return b.number(f, style)
}
