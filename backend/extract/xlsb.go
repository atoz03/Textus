package extract

import (
	"archive/zip"
	"math"
	"strconv"
	"strings"
	"unicode/utf16"
)

// .xlsb workbooks: an .xlsx's parts as binary records (MS-XLSB) - the
// workbook's sheet list, the shared strings, the cell formats and each
// worksheet's cells, a formula by its last value.

const (
	xlsbRowHeader     = 0
	xlsbSharedString  = 19
	xlsbFormat        = 44
	xlsbCellFormat    = 47
	xlsbRichString    = 62
	xlsbEndSheetData  = 146
	xlsbWorkbookProps = 153
	xlsbSheet         = 156
	xlsbCellFormats   = 617
	xlsbEndCellFormat = 618
)

type xlsbBook struct {
	excelStyles
	strings []string
}

// xlsbSheets reads the workbook at part (xl/workbook.bin) and its sheets.
func xlsbSheets(z *zip.Reader, part string) ([]*sheet, error) {
	workbook, ok := zipEntry(z, part)
	if !ok {
		return nil, errSpreadsheet
	}
	dir, r := partRels(z, part)
	var b xlsbBook
	records := xlsbRecords{data: excelPart(z, dir, r, "sharedStrings", "sharedStrings.bin")}
	for kind, body, ok := records.next(); ok; kind, body, ok = records.next() {
		if kind == xlsbSharedString && len(body) > 0 {
			// Flags first: whether rich text and phonetic guides follow.
			text, _, _ := xlsbString(body[1:])
			b.strings = append(b.strings, text)
		}
	}
	b.readStyles(excelPart(z, dir, r, "styles", "styles.bin"))
	var names, parts []string
	records = xlsbRecords{data: workbook}
	for kind, body, ok := records.next(); ok; kind, body, ok = records.next() {
		switch {
		case kind == xlsbWorkbookProps && len(body) > 0:
			b.date1904 = body[0]&1 != 0
		case kind == xlsbSheet && len(body) >= 8:
			// Its state and tab number, its relationship, its name.
			id, n, ok1 := xlsbString(body[8:])
			name, _, ok2 := xlsbString(body[8+n:])
			if p, ok := xlsxWorksheet(r, dir, id); ok1 && ok2 && ok {
				names, parts = append(names, name), append(parts, p)
			}
		}
	}
	var sheets []*sheet
	for i, p := range parts {
		if data, ok := zipEntry(z, p); ok {
			s := &sheet{name: names[i]}
			b.read(data, s)
			sheets = append(sheets, s)
		}
	}
	return sheets, nil
}

// readStyles reads which cell formats show dates: the number formats the
// workbook writes out, and the one each cell format uses.
func (b *xlsbBook) readStyles(data []byte) {
	formats := map[int]string{}
	var xfs []int
	cellFormats := false
	records := xlsbRecords{data: data}
	for kind, body, ok := records.next(); ok; kind, body, ok = records.next() {
		switch {
		case kind == xlsbFormat && len(body) >= 2:
			formats[int(xlsLE.Uint16(body))], _, _ = xlsbString(body[2:])
		case kind == xlsbCellFormats:
			cellFormats = true
		case kind == xlsbEndCellFormat:
			cellFormats = false
		case kind == xlsbCellFormat && cellFormats && len(body) >= 4:
			xfs = append(xfs, int(xlsLE.Uint16(body[2:])))
		}
	}
	b.dates = excelCellDates(xfs, formats)
}

// read reads a worksheet's cells into s, a row at each row header.
func (b *xlsbBook) read(data []byte, s *sheet) {
	var cells []sheetCell
	records := xlsbRecords{data: data}
	for kind, body, ok := records.next(); ok && kind != xlsbEndSheetData; kind, body, ok = records.next() {
		switch {
		case kind == xlsbRowHeader:
			s.add(cells, 1)
			cells = nil
		case (kind >= 2 && kind <= 11 || kind == xlsbRichString) && len(body) >= 8:
			// The column, the cell format in three bytes, then the value.
			col := xlsLE.Uint32(body)
			if text := b.cell(kind, body[8:], int(xlsLE.Uint32(body[4:])&0xFFFFFF)); col < maxSheetColumns && strings.TrimSpace(text) != "" {
				cells = append(cells, sheetCell{int(col), text})
			}
		}
	}
	s.add(cells, 1)
}

// cell is a cell record's text by its kind, from its value.
func (b *xlsbBook) cell(kind int, v []byte, xf int) string {
	switch {
	case kind == 2 && len(v) >= 4: // RK number
		return b.number(excelRK(xlsLE.Uint32(v)), xf)
	case (kind == 3 || kind == 11) && len(v) >= 1: // error, a formula's too
		return excelError(v[0])
	case (kind == 4 || kind == 10) && len(v) >= 1: // truth value
		return strconv.FormatBool(v[0] != 0)
	case (kind == 5 || kind == 9) && len(v) >= 8: // number
		return b.number(math.Float64frombits(xlsLE.Uint64(v)), xf)
	case kind == 6 || kind == 8: // text
		text, _, _ := xlsbString(v)
		return text
	case kind == 7 && len(v) >= 4: // shared string
		if i := xlsLE.Uint32(v); int64(i) < int64(len(b.strings)) {
			return b.strings[i]
		}
	case kind == xlsbRichString && len(v) >= 1:
		text, _, _ := xlsbString(v[1:])
		return text
	}
	return ""
}

// xlsbRecords reads a part's records: a type in one or two bytes and a
// size in up to four, seven bits each, then the data.
type xlsbRecords struct {
	data []byte
	pos  int
}

func (r *xlsbRecords) next() (int, []byte, bool) {
	kind, ok1 := r.varint(2)
	size, ok2 := r.varint(4)
	if !ok1 || !ok2 || size > len(r.data)-r.pos {
		return 0, nil, false
	}
	body := r.data[r.pos : r.pos+size]
	r.pos += size
	return kind, body, true
}

func (r *xlsbRecords) varint(most int) (int, bool) {
	v := 0
	for i := range most {
		if r.pos >= len(r.data) {
			return 0, false
		}
		c := r.data[r.pos]
		r.pos++
		v |= int(c&0x7F) << (7 * i)
		if c&0x80 == 0 {
			return v, true
		}
	}
	return 0, false
}

// xlsbString is the string at the start of p - a count of UTF-16 units,
// then the units - and the bytes it took. A count of 0xFFFFFFFF is no
// string at all.
func xlsbString(p []byte) (string, int, bool) {
	if len(p) < 4 {
		return "", 0, false
	}
	n := xlsLE.Uint32(p)
	if n == 0xFFFFFFFF {
		return "", 4, true
	}
	if uint64(n)*2 > uint64(len(p)-4) {
		return "", 0, false
	}
	units := make([]uint16, n)
	for i := range units {
		units[i] = xlsLE.Uint16(p[4+2*i:])
	}
	return string(utf16.Decode(units)), 4 + 2*int(n), true
}
