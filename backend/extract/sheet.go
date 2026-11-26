package extract

import (
	"archive/zip"
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// Spreadsheets as Markdown: every sheet a table under its name, its blank
// rows dropped and the ones past maxSheetRows counted rather than shown.
// Ported from the Rust reader at 8da2d2f0 (src-tauri/src/extract/mod.rs),
// which read workbooks through calamine.

const (
	// maxSheetRows is how many rows of one sheet are rendered; past this
	// the sheet is described, not shown.
	maxSheetRows = 400
	// maxSheetColumns is as wide as an Excel or LibreOffice sheet goes
	// (column XFD): no real file has a cell past it.
	maxSheetColumns = 16384
	// maxSheetCells bounds the cells one sheet's table keeps: a small file
	// can repeat a cell across every column of every row.
	maxSheetCells = 1 << 20
)

// errSpreadsheet is a workbook that doesn't open as one.
var errSpreadsheet = errors.New("This spreadsheet can't be read.")

// SpreadsheetMarkdown is every sheet of an .xlsx, .xlsm or .ods workbook
// as a Markdown table under its name; "" when no sheet holds any text.
// The format is the file's, not its extension's: an .xls is often an
// .xlsx renamed.
func SpreadsheetMarkdown(path string) (string, error) {
	z, err := zip.OpenReader(path)
	if err != nil {
		return "", errSpreadsheet
	}
	defer z.Close()
	var sheets []*sheet
	switch {
	case hasPart(&z.Reader, "xl/workbook.xml"):
		sheets, err = xlsxSheets(&z.Reader)
	case hasPart(&z.Reader, "content.xml"):
		sheets, err = odsSheets(&z.Reader)
	default:
		err = errSpreadsheet
	}
	if err != nil {
		return "", err
	}
	var out []string
	for _, s := range sheets {
		if text := s.markdown(); text != "" {
			out = append(out, text)
		}
	}
	return strings.Join(out, "\n\n"), nil
}

func hasPart(z *zip.Reader, name string) bool {
	return slices.ContainsFunc(z.File, func(f *zip.File) bool { return f.Name == name })
}

// sheet is one sheet as read: its name, its first rows with text, and how
// many more there were.
type sheet struct {
	name  string
	rows  [][]sheetCell
	cells int
	more  int
	// first and last are the columns the kept rows' text spans: a table
	// starts at its first column with text, not at A.
	first, last int
}

// sheetCell is a cell with text in it, by its column (A is 0).
type sheetCell struct {
	col  int
	text string
}

// add adds a row of cells with text, n times over: ODS repeats a row
// rather than writing it again. A row without text is dropped, not
// counted.
func (s *sheet) add(row []sheetCell, n int) {
	if len(row) == 0 {
		return
	}
	for ; n > 0; n-- {
		if len(s.rows) == maxSheetRows || s.cells+len(row) > maxSheetCells {
			s.more += n
			return
		}
		if len(s.rows) == 0 {
			s.first, s.last = row[0].col, row[0].col
		}
		for _, c := range row {
			s.first, s.last = min(s.first, c.col), max(s.last, c.col)
		}
		s.rows = append(s.rows, row)
		s.cells += len(row)
	}
}

// markdown is the sheet under its name, its first row the table's header,
// with the rows not shown said out loud: a silently cut sheet reads as
// "the model saw my data". "" for a sheet without text.
func (s *sheet) markdown() string {
	if len(s.rows) == 0 {
		return ""
	}
	var out strings.Builder
	out.WriteString("## " + s.name + "\n\n")
	row := make([]string, s.last-s.first+1)
	for i, cells := range s.rows {
		clear(row)
		for _, c := range cells {
			row[c.col-s.first] = c.text
		}
		tableRow(&out, row, i == 0)
	}
	text := strings.TrimRightFunc(out.String(), unicode.IsSpace)
	if s.more > 0 {
		text += fmt.Sprintf("\n\n[... %d more rows not shown]", s.more)
	}
	return text
}

// sheetColumn is the column of a cell reference (B3 is 1), or next for a
// cell written without one; maxSheetColumns for one past the last.
func sheetColumn(ref string, next int) int {
	col := 0
	for _, c := range strings.ToUpper(ref) {
		if c < 'A' || c > 'Z' {
			break
		}
		if col = col*26 + int(c-'A') + 1; col > maxSheetColumns {
			return maxSheetColumns
		}
	}
	if col == 0 {
		return next
	}
	return col - 1
}

// sheetNumber is a number as calamine wrote it: its shortest exact
// decimal, never in E notation.
func sheetNumber(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }

// excelDate is a serial date - days since Excel's epoch, the time of day
// as the fraction - as the date it is: 45943 is 2025-10-13 and 45943.5
// noon that day. In the 1900 system day 0 has no date, so a value under
// one day is a time of day. False for a value no date of Excel's has.
func excelDate(serial float64, date1904 bool) (string, bool) {
	if date1904 {
		serial += 1462
	}
	if !(serial >= 0 && serial < 2958466) { // to 9999-12-31
		return "", false
	}
	days := math.Floor(serial)
	ms := int64(math.Round((serial - days) * 86_400_000))
	if ms == 86_400_000 {
		days, ms = days+1, 0
	}
	clock := fmt.Sprintf("%02d:%02d:%02d", ms/3_600_000, ms/60_000%60, ms/1000%60)
	var date string
	switch d := int(days); {
	case d == 0:
		return clock, true
	case d == 60:
		// Excel counts a 1900-02-29, as Lotus 1-2-3 did.
		date = "1900-02-29"
	case d < 60:
		date = time.Date(1899, 12, 31, 0, 0, 0, 0, time.UTC).AddDate(0, 0, d).Format(time.DateOnly)
	default:
		date = time.Date(1899, 12, 30, 0, 0, 0, 0, time.UTC).AddDate(0, 0, d).Format(time.DateOnly)
	}
	if ms < 1000 {
		return date, true
	}
	return date + " " + clock, true
}

// excelDateFormat is whether a custom number format code shows a date or
// time, as calamine tells: a d, m, y, h or s outside quotes, brackets and
// escapes in its first section, or the p, m or / of an AM/PM. An elapsed
// time ([h]:mm) is a duration, shown as its number.
func excelDateFormat(code string) bool {
	escaped, quoted, ampm, elapsed := false, false, false, false
	brackets, prev := 0, ' '
	for _, c := range code {
		switch {
		case escaped:
			escaped = false
		case c == '_' || c == '\\':
			escaped = true
		case quoted:
			quoted = c != '"'
		case c == '"':
			quoted = true
		case c == ';':
			return false
		case c == '[':
			brackets++
		case c == ']' && brackets == 1 && elapsed:
			return false
		case c == ']':
			brackets = max(brackets-1, 0)
		case brackets == 0 && !ampm && (c == 'a' || c == 'A'):
			ampm = true
		case brackets == 0 && ampm && strings.ContainsRune("pm/PM", c):
			return true
		case brackets == 0 && !ampm && strings.ContainsRune("dmhysDMHYS", c):
			return true
		case !elapsed || unicode.ToLower(c) != unicode.ToLower(prev):
			elapsed = prev == '[' && strings.ContainsRune("hmsHMS", c)
		}
		prev = c
	}
	return false
}

// excelBuiltinDate is whether a number format Excel numbers rather than
// writes out shows a date or time: 14-22, 45 and 47 everywhere, 27-36 and
// 50-58 in East Asian Excel. 46 ([h]:mm:ss) is a duration.
func excelBuiltinDate(id int) bool {
	return id >= 14 && id <= 22 || id == 45 || id == 47 || id >= 27 && id <= 36 || id >= 50 && id <= 58
}
