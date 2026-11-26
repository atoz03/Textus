package extract

import (
	"bytes"
	"encoding/binary"
	"errors"
	"math"
	"testing"
	"unicode/utf16"

	"golang.org/x/text/encoding/charmap"
)

// testCFB is a compound file of 512-byte sectors holding streams: each
// under the cutoff in the mini stream, the others in sectors of their own.
func testCFB(streams ...part) []byte {
	const end, free = 0xFFFFFFFE, 0xFFFFFFFF
	le := binary.LittleEndian
	sectors := [][]byte{nil} // sector 0 holds the FAT
	fat := []uint32{0xFFFFFFFD}
	alloc := func(data []byte) uint32 {
		if len(data) == 0 {
			return end
		}
		start := uint32(len(sectors))
		for i := 0; i < len(data); i += 512 {
			sector := make([]byte, 512)
			copy(sector, data[i:])
			sectors = append(sectors, sector)
			fat = append(fat, uint32(len(sectors)))
		}
		fat[len(fat)-1] = end
		return start
	}
	type entry struct {
		name        string
		kind        byte
		start, next uint32
		size        int
	}
	entries := []entry{{name: "Root Entry", kind: 5}}
	var mini, miniFAT []byte
	for i, s := range streams {
		e := entry{name: s.name, kind: 2, size: len(s.xml), next: free}
		if i+1 < len(streams) {
			e.next = uint32(i + 2)
		}
		if len(s.xml) < 4096 {
			e.start = uint32(len(mini) / 64)
			for j := 0; j < len(s.xml); j += 64 {
				chunk := make([]byte, 64)
				copy(chunk, s.xml[j:])
				mini = append(mini, chunk...)
				next := uint32(len(mini) / 64)
				if j+64 >= len(s.xml) {
					next = end
				}
				miniFAT = le.AppendUint32(miniFAT, next)
			}
		} else {
			e.start = alloc([]byte(s.xml))
		}
		entries = append(entries, e)
	}
	entries[0].start, entries[0].size = alloc(mini), len(mini)
	miniFATStart := alloc(miniFAT)
	var dir []byte
	for i, e := range entries {
		b := make([]byte, 128)
		units := utf16.Encode([]rune(e.name))
		for j, u := range units {
			le.PutUint16(b[2*j:], u)
		}
		le.PutUint16(b[0x40:], uint16(2*len(units)+2))
		b[0x42], b[0x43] = e.kind, 1
		child := uint32(free)
		if i == 0 && len(entries) > 1 {
			child = 1
		}
		le.PutUint32(b[0x44:], free)
		le.PutUint32(b[0x48:], e.next)
		le.PutUint32(b[0x4C:], child)
		le.PutUint32(b[0x74:], e.start)
		le.PutUint64(b[0x78:], uint64(e.size))
		dir = append(dir, b...)
	}
	dirStart := alloc(dir)
	sectors[0] = make([]byte, 512)
	for i := range 128 {
		v := uint32(free)
		if i < len(fat) {
			v = fat[i]
		}
		le.PutUint32(sectors[0][4*i:], v)
	}
	header := make([]byte, 512)
	copy(header, cfbSignature)
	for at, v := range map[int]uint16{0x18: 0x3E, 0x1A: 3, 0x1C: 0xFFFE, 0x1E: 9, 0x20: 6} {
		le.PutUint16(header[at:], v)
	}
	for at, v := range map[int]uint32{0x2C: 1, 0x30: dirStart, 0x38: 4096, 0x3C: miniFATStart, 0x40: uint32((len(miniFAT) + 511) / 512), 0x44: end} {
		le.PutUint32(header[at:], v)
	}
	for i := 1; i < 109; i++ {
		le.PutUint32(header[0x4C+4*i:], free)
	}
	return append(header, bytes.Join(sectors, nil)...)
}

func biffRecord(kind int, body ...[]byte) []byte {
	data := bytes.Join(body, nil)
	out := binary.LittleEndian.AppendUint16(nil, uint16(kind))
	return append(binary.LittleEndian.AppendUint16(out, uint16(len(data))), data...)
}

func biffU16(v int) []byte    { return binary.LittleEndian.AppendUint16(nil, uint16(v)) }
func biffU32(v uint32) []byte { return binary.LittleEndian.AppendUint32(nil, v) }
func biffF64(v float64) []byte {
	return binary.LittleEndian.AppendUint64(nil, math.Float64bits(v))
}

// biffString is a BIFF8 string: its length in lenBytes, a flags byte, and
// its characters - one byte each when they all fit, else UTF-16.
func biffString(s string, lenBytes int) []byte {
	units := utf16.Encode([]rune(s))
	out := []byte{byte(len(units))}
	if lenBytes == 2 {
		out = biffU16(len(units))
	}
	high := false
	for _, u := range units {
		high = high || u > 0xFF
	}
	if !high {
		return append(append(out, 0), []byte(s)...)
	}
	out = append(out, 1)
	for _, u := range units {
		out = binary.LittleEndian.AppendUint16(out, u)
	}
	return out
}

func biffCell(row, col, xf int) []byte {
	return bytes.Join([][]byte{biffU16(row), biffU16(col), biffU16(xf)}, nil)
}

// biffRK is v as a 30-bit RK double: v's low 34 bits must be zero.
func biffRK(v float64) []byte { return biffU32(uint32(math.Float64bits(v) >> 32)) }

// testXLSStream is the Workbook stream of a BIFF8 workbook with a
// worksheet and a chart sheet, padded to pad bytes.
func testXLSStream(pad int) []byte {
	bof := func(kind int) []byte { return biffRecord(0x0809, biffU16(0x0600), biffU16(kind), make([]byte, 12)) }
	xf := func(format int) []byte { return biffRecord(0x00E0, biffU16(0), biffU16(format), make([]byte, 16)) }
	sheet := bytes.Join([][]byte{
		bof(0x10),
		biffRecord(0x00FD, biffCell(0, 0, 0), biffU32(0)),
		biffRecord(0x00FD, biffCell(0, 1, 0), biffU32(1)),
		biffRecord(0x00FD, biffCell(0, 2, 0), biffU32(2)),
		biffRecord(0x00FD, biffCell(0, 3, 0), biffU32(3)),
		biffRecord(0x0203, biffCell(1, 0, 1), biffF64(45943)),
		// MULRK: cells 1 and 2 of row 1, then the last column.
		biffRecord(0x00BD, biffU16(1), biffU16(1), biffU16(2), biffRK(45943.5), biffU16(0), biffU32(7<<2|2), biffU16(2)),
		biffRecord(0x027E, biffCell(1, 3, 0), biffU32(12345<<2|3)),
		biffRecord(0x0205, biffCell(2, 0, 0), []byte{1, 0}),
		biffRecord(0x0205, biffCell(2, 1, 0), []byte{0x07, 1}),
		// A string formula, its value in the STRING record past a shared
		// formula's.
		biffRecord(0x0006, biffCell(2, 2, 0), []byte{0, 0, 0, 0, 0, 0, 0xFF, 0xFF}, make([]byte, 6)),
		biffRecord(0x04BC, make([]byte, 10)),
		biffRecord(0x0207, biffString("Done", 2)),
		biffRecord(0x0006, biffCell(2, 3, 0), biffF64(2.5), make([]byte, 6)),
		biffRecord(0x0204, biffCell(3, 0, 0), biffString("Label", 2)),
		// A chart drawn on the sheet: its cached values are no cells.
		bof(0x20), biffRecord(0x0203, biffCell(9, 0, 0), biffF64(99)), biffRecord(0x000A),
		biffRecord(0x0203, biffCell(3, 1, 0), biffF64(-4)),
		biffRecord(0x000A),
	}, nil)
	chart := bytes.Join([][]byte{bof(0x20), biffRecord(0x0203, biffCell(0, 0, 0), biffF64(1)), biffRecord(0x000A)}, nil)
	globals := func(at int) []byte {
		boundSheet := func(name string, kind byte, pos int) []byte {
			return biffRecord(0x0085, biffU32(uint32(pos)), []byte{0, kind}, biffString(name, 1))
		}
		return bytes.Join([][]byte{
			bof(0x05), biffRecord(0x0042, biffU16(1200)), biffRecord(0x0022, biffU16(0)),
			biffRecord(0x041E, biffU16(164), biffString("yyyy-mm-dd", 2)),
			xf(0), xf(164), xf(22),
			boundSheet("Plan", 0, at), boundSheet("Chart", 2, at+len(sheet)),
			// The last string's characters go on in the CONTINUE record,
			// which starts with a flags byte of their own: UTF-16 there.
			biffRecord(0x00FC, biffU32(4), biffU32(4), biffString("Task", 2), biffString("你好", 2),
				biffU16(4), []byte{8}, biffU16(1), []byte("Bold"), make([]byte, 4), biffU16(8), []byte{0}, []byte("Split ")),
			biffRecord(0x003C, []byte{1}, biffU16(0x4E2D), biffU16(0x6587)),
			biffRecord(0x000A),
		}, nil)
	}
	stream := globals(len(globals(0)))
	stream = append(append(stream, sheet...), chart...)
	return append(stream, make([]byte, max(pad-len(stream), 0))...)
}

func TestAnXLSWorkbookReadsItsWorksheets(t *testing.T) {
	// Small, a workbook's stream is in the mini stream; past 4096 bytes in
	// sectors of its own.
	for _, pad := range []int{0, 5000} {
		sheets, err := xlsSheets(testCFB(part{"Workbook", string(testXLSStream(pad))}))
		if err != nil || len(sheets) != 1 {
			t.Fatalf("%d sheets, %v", len(sheets), err)
		}
		expect(t, sheets[0].markdown(), "## Plan\n\n| Task | 你好 | Bold | Split 中文 |\n| --- | --- | --- | --- |\n"+
			"| 2025-10-13 | 2025-10-13 12:00:00 | 7 | 123.45 |\n| true | #DIV/0! | Done | 2.5 |\n| Label | -4 |  |  |")
	}
}

func TestAnExcel95WorkbookReadsInItsCodePage(t *testing.T) {
	cyrillic := func(s string) []byte {
		b, _ := charmap.Windows1251.NewEncoder().Bytes([]byte(s))
		return b
	}
	bof := func(kind int) []byte { return biffRecord(0x0809, biffU16(0x0500), biffU16(kind), make([]byte, 4)) }
	name := cyrillic("Лист")
	globals := func(at int) []byte {
		return bytes.Join([][]byte{bof(0x05), biffRecord(0x0042, biffU16(1251)),
			biffRecord(0x0085, biffU32(uint32(at)), []byte{0, 0, byte(len(name))}, name), biffRecord(0x000A)}, nil)
	}
	label := cyrillic("Привет")
	sheet := bytes.Join([][]byte{bof(0x10), biffRecord(0x0204, biffCell(0, 0, 0), biffU16(len(label)), label),
		biffRecord(0x0203, biffCell(1, 0, 0), biffF64(3)), biffRecord(0x000A)}, nil)
	stream := append(globals(len(globals(0))), sheet...)
	sheets, err := xlsSheets(testCFB(part{"Book", string(stream)}))
	if err != nil || len(sheets) != 1 {
		t.Fatalf("%d sheets, %v", len(sheets), err)
	}
	expect(t, sheets[0].markdown(), "## Лист\n\n| Привет |\n| --- |\n| 3 |")
}

func TestAnEncryptedWorkbookSaysSo(t *testing.T) {
	filePass := testXLSStream(0)
	filePass = append(append(append([]byte{}, filePass[:20]...), biffRecord(0x002F, make([]byte, 6))...), filePass[20:]...)
	for name, file := range map[string][]byte{
		"xlsx with a password": testCFB(part{"EncryptionInfo", "info"}, part{"EncryptedPackage", "package"}),
		"xls with a password":  testCFB(part{"Workbook", string(filePass)}),
	} {
		if _, err := xlsSheets(file); !errors.Is(err, errSheetEncrypted) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := xlsSheets(testCFB(part{"WordDocument", "a .doc"})); !errors.Is(err, errSpreadsheet) {
		t.Errorf("a .doc: %v", err)
	}
}

func TestABrokenXLSEndsWithoutPanicking(t *testing.T) {
	data := testCFB(part{"Workbook", string(testXLSStream(5000))})
	for n := 0; n < len(data); n += 97 {
		xlsSheets(data[:n])
	}
	for i := 0; i < len(data); i += 13 {
		flipped := bytes.Clone(data)
		flipped[i] ^= 0xFF
		xlsSheets(flipped)
	}
	// A FAT whose every chain loops back on itself.
	loop := bytes.Clone(data)
	for i := range 128 {
		binary.LittleEndian.PutUint32(loop[512+4*i:], 1)
	}
	xlsSheets(loop)
}

func TestRKNumbersUnpack(t *testing.T) {
	for rk, want := range map[uint32]float64{
		0x3FF00000: 1, 0x3FF00001: 0.01, 7<<2 | 2: 7, 12345<<2 | 3: 123.45, 0xFFFFFFFE: -1,
	} {
		if got := excelRK(rk); got != want {
			t.Errorf("%#x: %v, want %v", rk, got, want)
		}
	}
}

// xlsbRecord is a record of an .xlsb part: its type and size seven bits a
// byte, then its data.
func xlsbRecord(kind int, body ...[]byte) []byte {
	data := bytes.Join(body, nil)
	var out []byte
	for _, n := range []int{kind, len(data)} {
		for ; n > 0x7F; n >>= 7 {
			out = append(out, byte(n&0x7F)|0x80)
		}
		out = append(out, byte(n))
	}
	return append(out, data...)
}

func xlsbText(s string) []byte {
	units := utf16.Encode([]rune(s))
	out := biffU32(uint32(len(units)))
	for _, u := range units {
		out = binary.LittleEndian.AppendUint16(out, u)
	}
	return out
}

func TestAnXLSBWorkbookReadsItsWorksheets(t *testing.T) {
	cell := func(col, xf int) []byte { return append(biffU32(uint32(col)), biffU32(uint32(xf))...) }
	xf := func(format int) []byte { return xlsbRecord(47, biffU16(0), biffU16(format), make([]byte, 12)) }
	for _, c := range []struct {
		flags  byte
		serial float64
	}{{0, 45943}, {1, 44481}} {
		path := writeFile(t, "plan.xlsb", zipOf(
			part{"xl/workbook.bin", string(bytes.Join([][]byte{
				xlsbRecord(131), xlsbRecord(153, []byte{c.flags, 0, 0, 0}, make([]byte, 8)), xlsbRecord(143),
				xlsbRecord(156, biffU32(0), biffU32(1), xlsbText("rId1"), xlsbText("Plan")),
				xlsbRecord(156, biffU32(0), biffU32(2), xlsbText("rId2"), xlsbText("Chart")),
				xlsbRecord(144), xlsbRecord(132),
			}, nil))},
			part{"xl/_rels/workbook.bin.rels", `<Relationships>` + xlsxRel("rId1", "worksheet", "worksheets/sheet1.bin") +
				xlsxRel("rId2", "chartsheet", "chartsheets/sheet1.bin") + xlsxRel("rId3", "sharedStrings", "sharedStrings.bin") +
				xlsxRel("rId4", "styles", "styles.bin") + `</Relationships>`},
			part{"xl/sharedStrings.bin", string(bytes.Join([][]byte{
				xlsbRecord(159, biffU32(2), biffU32(2)), xlsbRecord(19, []byte{0}, xlsbText("Task")),
				xlsbRecord(19, []byte{0}, xlsbText("你好")), xlsbRecord(160),
			}, nil))},
			// The cell style formats (626 to 627) are not the cells'.
			part{"xl/styles.bin", string(bytes.Join([][]byte{
				xlsbRecord(615, biffU32(1)), xlsbRecord(44, biffU16(164), xlsbText("yyyy-mm-dd")), xlsbRecord(616),
				xlsbRecord(626, biffU32(1)), xf(14), xlsbRecord(627),
				xlsbRecord(617, biffU32(3)), xf(0), xf(164), xf(22), xlsbRecord(618),
			}, nil))},
			part{"xl/worksheets/sheet1.bin", string(bytes.Join([][]byte{
				xlsbRecord(129), xlsbRecord(145),
				xlsbRecord(0, biffU32(0), make([]byte, 13)),
				xlsbRecord(7, cell(0, 0), biffU32(0)), xlsbRecord(7, cell(1, 0), biffU32(1)),
				xlsbRecord(0, biffU32(1), make([]byte, 13)),
				xlsbRecord(5, cell(0, 1), biffF64(c.serial)), xlsbRecord(2, cell(1, 2), biffRK(c.serial+0.5)),
				xlsbRecord(4, cell(2, 0), []byte{1}), xlsbRecord(3, cell(3, 0), []byte{0x2A}),
				xlsbRecord(0, biffU32(2), make([]byte, 13)),
				xlsbRecord(8, cell(0, 0), xlsbText("Sum"), make([]byte, 6)), xlsbRecord(9, cell(1, 0), biffF64(2.5), make([]byte, 6)),
				xlsbRecord(62, cell(2, 0), []byte{0}, xlsbText("rich")), xlsbRecord(6, cell(3, 0), xlsbText("inline")),
				xlsbRecord(146),
				// Past the sheet's data: no cell of it.
				xlsbRecord(7, cell(0, 0), biffU32(0)), xlsbRecord(130),
			}, nil))},
		))
		text, err := SpreadsheetMarkdown(path)
		if err != nil {
			t.Fatal(err)
		}
		expect(t, text, "## Plan\n\n| Task | 你好 |  |  |\n| --- | --- | --- | --- |\n"+
			"| 2025-10-13 | 2025-10-13 12:00:00 | true | #N/A |\n| Sum | 2.5 | rich | inline |")
	}
}
