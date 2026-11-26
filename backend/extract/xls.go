package extract

import (
	"encoding/binary"
	"math"
	"strconv"
	"strings"
	"unicode/utf16"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/charmap"
)

// Excel 97-2003 workbooks (.xls, BIFF8) and Excel 5/95 ones (BIFF5): the
// records of the Workbook stream (MS-XLS). The globals give the sheet list,
// the shared strings and the cell formats; each worksheet's records its
// cells, a formula by its last value.

const (
	xlsFormula  = 0x0006
	xlsEOF      = 0x000A
	xlsFilePass = 0x002F
	xlsContinue = 0x003C
	xlsMulRK    = 0x00BD
	xlsSST      = 0x00FC
	xlsLabelSST = 0x00FD
	xlsNumber   = 0x0203
	xlsLabel    = 0x0204
	xlsBoolErr  = 0x0205
	xlsString   = 0x0207
	xlsRK       = 0x027E
	xlsBOF      = 0x0809
)

var xlsLE = binary.LittleEndian

type xlsBook struct {
	excelStyles
	data  []byte
	biff5 bool
	// codePage decodes BIFF5's text, bytes in the workbook's code page;
	// BIFF8 writes UTF-16.
	codePage encoding.Encoding
	strings  []string
}

func xlsSheets(data []byte) ([]*sheet, error) {
	c, ok := openCFB(data)
	if !ok {
		return nil, errSpreadsheet
	}
	stream, ok := c.stream("Workbook")
	if !ok {
		stream, ok = c.stream("Book")
	}
	if !ok {
		// An .xlsx saved with a password is a compound file too.
		if _, ok := c.stream("EncryptedPackage"); ok {
			return nil, errSheetEncrypted
		}
		return nil, errSpreadsheet
	}
	b := &xlsBook{data: stream, codePage: charmap.Windows1252}
	kind, body, pos, ok := xlsRecord(stream, 0)
	if !ok || kind != xlsBOF || len(body) < 4 {
		return nil, errSpreadsheet
	}
	switch xlsLE.Uint16(body) {
	case 0x0600:
	case 0x0500:
		b.biff5 = true
	default:
		return nil, errSpreadsheet
	}
	var names []string
	var starts []int
	formats := map[int]string{}
	var xfs []int
	for kind, body, next, ok := xlsRecord(stream, pos); ok && kind != xlsEOF; kind, body, next, ok = xlsRecord(stream, pos) {
		pos = next
		switch {
		case kind == xlsFilePass:
			return nil, errSheetEncrypted
		case kind == 0x0042 && len(body) >= 2: // CODEPAGE; 0x8000 is Mac Roman
			if e := codePage(int(xlsLE.Uint16(body))); e != nil {
				b.codePage = e
			} else if xlsLE.Uint16(body) == 0x8000 {
				b.codePage = charmap.Macintosh
			}
		case kind == 0x0022 && len(body) >= 2: // DATEMODE
			b.date1904 = xlsLE.Uint16(body) == 1
		case kind == 0x041E && len(body) >= 2: // FORMAT
			text, _ := b.text(&xlsSegments{segs: [][]byte{body[2:]}}, 2-btoi(b.biff5))
			formats[int(xlsLE.Uint16(body))] = text
		case kind == 0x00E0 && len(body) >= 4: // XF
			xfs = append(xfs, int(xlsLE.Uint16(body[2:])))
		case kind == 0x0085 && len(body) >= 6: // BOUNDSHEET: where, state, kind, name
			if body[5] == 0 {
				name, _ := b.text(&xlsSegments{segs: [][]byte{body[6:]}}, 1)
				names, starts = append(names, name), append(starts, int(xlsLE.Uint32(body)))
			}
		case kind == xlsSST:
			var segs [][]byte
			segs, pos = xlsContinued(stream, body, pos)
			b.readSST(segs)
		}
	}
	b.dates = excelCellDates(xfs, formats)
	var sheets []*sheet
	for i, start := range starts {
		s := &sheet{name: names[i]}
		b.readSheet(start, s)
		sheets = append(sheets, s)
	}
	return sheets, nil
}

// xlsRecord is the record at pos: its type and data, and where the next
// one starts.
func xlsRecord(data []byte, pos int) (int, []byte, int, bool) {
	if pos < 0 || pos+4 > len(data) {
		return 0, nil, pos, false
	}
	size := int(xlsLE.Uint16(data[pos+2:]))
	if pos+4+size > len(data) {
		return 0, nil, pos, false
	}
	return int(xlsLE.Uint16(data[pos:])), data[pos+4 : pos+4+size], pos + 4 + size, true
}

// xlsContinued is a record's data with that of the CONTINUE records after
// it, and where the next record starts.
func xlsContinued(data, body []byte, pos int) ([][]byte, int) {
	segs := [][]byte{body}
	for {
		kind, more, next, ok := xlsRecord(data, pos)
		if !ok || kind != xlsContinue {
			return segs, pos
		}
		segs, pos = append(segs, more), next
	}
}

// readSST reads the shared strings: rich text and phonetic guides are
// skipped past, not read.
func (b *xlsBook) readSST(segs [][]byte) {
	r := &xlsSegments{segs: segs}
	r.skip(4)
	unique, _ := r.u32()
	for range unique {
		n, ok1 := r.u16()
		flags, ok2 := r.u8()
		runs, ext, ok3, ok4 := 0, 0, true, true
		if flags&8 != 0 {
			runs, ok3 = r.u16()
		}
		if flags&4 != 0 {
			ext, ok4 = r.u32()
		}
		text, ok5 := r.chars(n, flags&1 != 0)
		if !ok1 || !ok2 || !ok3 || !ok4 || !ok5 {
			return
		}
		b.strings = append(b.strings, text)
		if !r.skip(4*runs + ext) {
			return
		}
	}
}

// text is a string as a record holds one: its length in lenBytes, then
// in BIFF8 a flags byte and the characters, in BIFF5 bytes in the
// workbook's code page.
func (b *xlsBook) text(r *xlsSegments, lenBytes int) (string, bool) {
	read := r.u8
	if lenBytes == 2 {
		read = r.u16
	}
	n, ok := read()
	if !ok {
		return "", false
	}
	if b.biff5 {
		raw, ok := r.bytes(n)
		decoded, err := b.codePage.NewDecoder().Bytes(raw)
		if err != nil {
			decoded = raw
		}
		return strings.ToValidUTF8(string(decoded), "\uFFFD"), ok
	}
	flags, ok := r.u8()
	if !ok {
		return "", false
	}
	return r.chars(n, flags&1 != 0)
}

// readSheet reads the cells of the worksheet whose BOF is at pos into s,
// row by row as the file holds them. A chart drawn on the sheet nests its
// own BOF to EOF, whose cached values are no cells.
func (b *xlsBook) readSheet(pos int, s *sheet) {
	kind, _, pos, ok := xlsRecord(b.data, pos)
	if !ok || kind != xlsBOF {
		return
	}
	row, formula, depth := -1, -1, 1
	var cells []sheetCell
	add := func(r, col int, text string) {
		if r != row {
			s.add(cells, 1)
			row, cells = r, nil
		}
		if col < maxSheetColumns && strings.TrimSpace(text) != "" {
			cells = append(cells, sheetCell{col, text})
		}
	}
	for kind, body, next, ok := xlsRecord(b.data, pos); ok; kind, body, next, ok = xlsRecord(b.data, pos) {
		pos = next
		switch kind {
		case xlsBOF:
			depth++
		case xlsEOF:
			depth--
		}
		if depth == 0 {
			break
		}
		if depth > 1 {
			continue
		}
		if kind == xlsString && formula >= 0 {
			// A string formula's value, in the record after it.
			var segs [][]byte
			segs, pos = xlsContinued(b.data, body, pos)
			text, _ := b.text(&xlsSegments{segs: segs}, 2)
			add(row, formula, text)
			formula = -1
			continue
		}
		if len(body) < 6 {
			continue
		}
		r, col, xf := int(xlsLE.Uint16(body)), int(xlsLE.Uint16(body[2:])), int(xlsLE.Uint16(body[4:]))
		switch {
		case kind == xlsLabelSST && len(body) >= 10:
			if i := xlsLE.Uint32(body[6:]); int64(i) < int64(len(b.strings)) {
				add(r, col, b.strings[i])
			}
		case kind == xlsLabel || kind == 0x00D6: // LABEL, RSTRING
			text, _ := b.text(&xlsSegments{segs: [][]byte{body[6:]}}, 2)
			add(r, col, text)
		case kind == xlsNumber && len(body) >= 14:
			add(r, col, b.number(math.Float64frombits(xlsLE.Uint64(body[6:])), xf))
		case kind == xlsRK && len(body) >= 10:
			add(r, col, b.number(excelRK(xlsLE.Uint32(body[6:])), xf))
		case kind == xlsMulRK:
			// Cells side by side: a cell format and RK for each, then the
			// last column.
			for i := 0; 4+6*i+6 <= len(body)-2; i++ {
				at := body[4+6*i:]
				add(r, col+i, b.number(excelRK(xlsLE.Uint32(at[2:])), int(xlsLE.Uint16(at))))
			}
		case kind == xlsBoolErr && len(body) >= 8:
			add(r, col, xlsTruthOrError(body[6], body[7] != 0))
		case kind == xlsFormula && len(body) >= 14:
			value := body[6:14]
			if value[6] != 0xFF || value[7] != 0xFF {
				add(r, col, b.number(math.Float64frombits(xlsLE.Uint64(value)), xf))
				continue
			}
			switch value[0] {
			case 0:
				add(r, col, "")
				formula = col
			case 1, 2:
				add(r, col, xlsTruthOrError(value[2], value[0] == 2))
			}
		}
	}
	s.add(cells, 1)
}

func xlsTruthOrError(value byte, isError bool) string {
	if isError {
		return excelError(value)
	}
	return strconv.FormatBool(value != 0)
}

func btoi(b bool) int {
	if b {
		return 1
	}
	return 0
}

// xlsSegments reads across a record and its CONTINUE records as one run
// of bytes - except that a string whose characters go on in the next
// record starts it with a new flags byte for the rest.
type xlsSegments struct {
	segs   [][]byte
	i, pos int
}

func (r *xlsSegments) bytes(n int) ([]byte, bool) {
	out := make([]byte, 0, min(n, 1<<16))
	for len(out) < n {
		if r.i >= len(r.segs) {
			return out, false
		}
		seg := r.segs[r.i]
		if r.pos >= len(seg) {
			r.i, r.pos = r.i+1, 0
			continue
		}
		take := min(n-len(out), len(seg)-r.pos)
		out = append(out, seg[r.pos:r.pos+take]...)
		r.pos += take
	}
	return out, true
}

func (r *xlsSegments) skip(n int) bool {
	for n > 0 {
		if r.i >= len(r.segs) {
			return false
		}
		take := min(n, len(r.segs[r.i])-r.pos)
		if take <= 0 {
			r.i, r.pos = r.i+1, 0
			continue
		}
		r.pos += take
		n -= take
	}
	return true
}

func (r *xlsSegments) u8() (int, bool) {
	b, ok := r.bytes(1)
	if !ok {
		return 0, false
	}
	return int(b[0]), true
}

func (r *xlsSegments) u16() (int, bool) {
	b, ok := r.bytes(2)
	if !ok {
		return 0, false
	}
	return int(xlsLE.Uint16(b)), true
}

func (r *xlsSegments) u32() (int, bool) {
	b, ok := r.bytes(4)
	if !ok {
		return 0, false
	}
	return int(xlsLE.Uint32(b)), true
}

// chars reads n characters, two bytes each when high and one (the low
// byte of a UTF-16 unit) when not.
func (r *xlsSegments) chars(n int, high bool) (string, bool) {
	units := make([]uint16, 0, min(n, 1<<16))
	for len(units) < n {
		if r.i >= len(r.segs) {
			return "", false
		}
		seg := r.segs[r.i]
		if r.pos >= len(seg) {
			r.i, r.pos = r.i+1, 1
			if r.i >= len(r.segs) || len(r.segs[r.i]) == 0 {
				return "", false
			}
			high = r.segs[r.i][0]&1 != 0
			continue
		}
		if !high {
			units = append(units, uint16(seg[r.pos]))
			r.pos++
			continue
		}
		if r.pos+2 > len(seg) {
			return "", false
		}
		units = append(units, xlsLE.Uint16(seg[r.pos:]))
		r.pos += 2
	}
	return string(utf16.Decode(units)), true
}
