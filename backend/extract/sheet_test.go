package extract

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
)

func TestSheetsBecomeTablesWithDatesAsDates(t *testing.T) {
	for _, c := range []struct {
		serial   float64
		date1904 bool
		want     string
	}{
		{45943.0, false, "2025-10-13"},
		{45943.5, false, "2025-10-13 12:00:00"},
		{45943.999999999, false, "2025-10-14"}, // rounds to the next midnight
		{0.75, false, "18:00:00"},              // a time of day
		{59, false, "1900-02-28"},
		{60, false, "1900-02-29"}, // the day Excel counts that never was
		{61, false, "1900-03-01"},
		{0, true, "1904-01-01"},
		{44481.25, true, "2025-10-13 06:00:00"},
	} {
		if got, ok := excelDate(c.serial, c.date1904); !ok || got != c.want {
			t.Errorf("%v (1904: %v): %q, want %q", c.serial, c.date1904, got, c.want)
		}
	}
	for _, serial := range []float64{-1, 2958466} {
		if got, ok := excelDate(serial, false); ok {
			t.Errorf("%v: %q, want no date", serial, got)
		}
	}
	expect(t, sheetNumber(3.5), "3.5")
	expect(t, sheetNumber(3), "3")
	expect(t, sheetNumber(1e21), "1000000000000000000000")
}

func TestNumberFormatsTellDatesAsCalamineDid(t *testing.T) {
	for code, want := range map[string]bool{
		"yyyy-mm-dd": true, "d-mmm-yy": true, "h:mm AM/PM": true, "mm:ss": true,
		`yyyy"年"m"月"d"日"`: true, "[$-F800]dddd, mmmm dd, yyyy": true,
		"General": false, "0.00%": false, "#,##0;[Red]-#,##0": false, `"Day "0`: false,
		`\d0`: false, "[h]:mm:ss": false, "[Red]0.00": false, "@": false, "0.00E+00": false,
	} {
		if got := excelDateFormat(code); got != want {
			t.Errorf("%s: %v, want %v", code, got, want)
		}
	}
}

func TestAnODSSheetReadsAsATableWithItsName(t *testing.T) {
	// ODS is the one spreadsheet format simple enough to write by hand: a
	// zip with a mimetype and content.xml.
	row := func(a, b string) string {
		return `<table:table-row><table:table-cell office:value-type="string"><text:p>` + a +
			`</text:p></table:table-cell><table:table-cell office:value-type="string"><text:p>` + b +
			`</text:p></table:table-cell></table:table-row>`
	}
	rows := row("Name", "Words") + row("", "")
	for i := 1; i <= maxSheetRows+2; i++ {
		rows += row("Ch", strconv.Itoa(i))
	}
	content := `<?xml version="1.0" encoding="UTF-8"?><office:document-content xmlns:office="urn:oasis:names:tc:opendocument:xmlns:office:1.0" xmlns:table="urn:oasis:names:tc:opendocument:xmlns:table:1.0" xmlns:text="urn:oasis:names:tc:opendocument:xmlns:text:1.0" office:version="1.2"><office:body><office:spreadsheet><table:table table:name="Draft | counts">` +
		rows + `</table:table></office:spreadsheet></office:body></office:document-content>`
	path := writeFile(t, "counts.ods", zipOf(
		part{"mimetype", "application/vnd.oasis.opendocument.spreadsheet"},
		part{"META-INF/manifest.xml", `<manifest:manifest xmlns:manifest="urn:oasis:names:tc:opendocument:xmlns:manifest:1.0"/>`},
		part{"content.xml", content},
	))
	text, err := SpreadsheetMarkdown(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(text, "## Draft | counts\n\n| Name | Words |\n| --- | --- |\n| Ch | 1 |\n") {
		t.Errorf("%s", text)
	}
	// The blank row is dropped, not counted; the header counts as a row.
	if want := fmt.Sprintf("| Ch | %d |\n\n[... 3 more rows not shown]", maxSheetRows-1); !strings.HasSuffix(text, want) {
		t.Errorf("%s", text)
	}
}

// testODS is an .ods holding the tables of a content.xml.
func testODS(tables string) []byte {
	return zipOf(
		part{"mimetype", "application/vnd.oasis.opendocument.spreadsheet"},
		part{"content.xml", `<office:document-content xmlns:office="urn:oasis:names:tc:opendocument:xmlns:office:1.0" xmlns:table="urn:oasis:names:tc:opendocument:xmlns:table:1.0" xmlns:text="urn:oasis:names:tc:opendocument:xmlns:text:1.0"><office:body><office:spreadsheet>` +
			tables + `</office:spreadsheet></office:body></office:document-content>`},
	)
}

func TestODSCellsReadByTheirValuesAndRepeatsCount(t *testing.T) {
	cell := func(attrs, text string) string {
		return `<table:table-cell ` + attrs + `><text:p>` + text + `</text:p></table:table-cell>`
	}
	str := func(text string) string { return cell(`office:value-type="string"`, text) }
	path := writeFile(t, "values.ods", testODS(`<table:table table:name="Values">
<table:table-column table:number-columns-repeated="3"/>
<table:table-header-rows><table:table-row><table:table-cell/>`+str("What")+str("Value")+`</table:table-row></table:table-header-rows>
<table:table-row><table:table-cell/>`+str("Share")+cell(`office:value-type="percentage" office:value="0.125"`, "12.5%")+`</table:table-row>
<table:table-row><table:table-cell/>`+str("Due")+cell(`office:value-type="date" office:date-value="2025-10-13"`, "13/10/25")+`</table:table-row>
<table:table-row><table:table-cell/>`+str("Done")+cell(`office:value-type="boolean" office:boolean-value="false"`, "FALSE")+`</table:table-row>
<table:table-row-group><table:table-row table:number-rows-repeated="2"><table:table-cell/>
<table:table-cell table:number-columns-repeated="2" office:value-type="string"><text:p>a<text:s text:c="2"/>b<text:tab/>c</text:p><text:p>second <text:span>line</text:span></text:p><office:annotation><text:p>A comment</text:p></office:annotation></table:table-cell>
</table:table-row></table:table-row-group>
<table:table-row table:number-rows-repeated="1048570"><table:table-cell table:number-columns-repeated="1024"/></table:table-row>
</table:table>
<table:table table:name="Empty"><table:table-row><table:table-cell/></table:table-row></table:table>`))
	text, err := SpreadsheetMarkdown(path)
	if err != nil {
		t.Fatal(err)
	}
	// The table starts at the first column with text; a cell's paragraphs
	// are lines, its comment none of them.
	expect(t, text, "## Values\n\n| What | Value |\n| --- | --- |\n| Share | 0.125 |\n| Due | 2025-10-13 |\n| Done | false |\n"+
		"| a  b\tc<br>second line | a  b\tc<br>second line |\n| a  b\tc<br>second line | a  b\tc<br>second line |")
}

func TestRepeatsStopAtTheSheetsEdges(t *testing.T) {
	path := writeFile(t, "big.ods", testODS(`<table:table table:name="Big"><table:table-row table:number-rows-repeated="1000000">`+
		`<table:table-cell table:number-columns-repeated="20000" office:value-type="float" office:value="1"/></table:table-row></table:table>`))
	text, err := SpreadsheetMarkdown(path)
	if err != nil {
		t.Fatal(err)
	}
	// A row is as wide as a sheet goes, and the table keeps as many rows
	// as its cells allow.
	kept := maxSheetCells / maxSheetColumns
	if !strings.HasSuffix(text, fmt.Sprintf("|\n\n[... %d more rows not shown]", 1000000-kept)) {
		t.Errorf("%s", text[len(text)-100:])
	}
	header, _, _ := strings.Cut(strings.TrimPrefix(text, "## Big\n\n"), "\n")
	if got := strings.Count(header, "1"); got != maxSheetColumns {
		t.Errorf("%d columns", got)
	}
	if got := strings.Count(text, " |\n"); got != kept+1 {
		t.Errorf("%d table lines", got)
	}
}

// testXLSX is an .xlsx of a workbook.xml, its relationships and parts
// under xl/.
func testXLSX(workbook, rels string, parts ...part) []byte {
	all := []part{
		{"xl/workbook.xml", `<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">` + workbook + `</workbook>`},
		{"xl/_rels/workbook.xml.rels", `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` + rels + `</Relationships>`},
	}
	return zipOf(append(all, parts...)...)
}

func xlsxRel(id, kind, target string) string {
	return `<Relationship Id="` + id + `" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/` + kind + `" Target="` + target + `"/>`
}

func TestAnXLSXWorkbookReadsItsWorksheetsInOrder(t *testing.T) {
	path := writeFile(t, "plan.xlsx", testXLSX(
		`<sheets><sheet name="Plan" sheetId="1" r:id="rId1"/><sheet name="Chart" sheetId="2" r:id="rId2"/><sheet name="Blank" sheetId="3" r:id="rId3"/><sheet name="Later" sheetId="4" r:id="rId4"/></sheets>`,
		xlsxRel("rId1", "worksheet", "worksheets/sheet1.xml")+xlsxRel("rId2", "chartsheet", "chartsheets/sheet1.xml")+
			xlsxRel("rId3", "worksheet", "worksheets/sheet2.xml")+xlsxRel("rId4", "worksheet", "/xl/worksheets/sheet3.xml")+
			xlsxRel("rId5", "sharedStrings", "sharedStrings.xml")+xlsxRel("rId6", "styles", "styles.xml"),
		part{"xl/sharedStrings.xml", `<sst><si><t>Task</t></si><si><r><rPr><b/></rPr><t>Du</t></r><r><t>e</t></r><rPh sb="0" eb="1"><t>guide</t></rPh></si>` +
			"<si><t>Line_x000D_\ntwo</t></si><si><t xml:space=\"preserve\"> </t></si></sst>"},
		// The dxf's numFmt 164 and the cellStyleXfs are not the cells'.
		part{"xl/styles.xml", `<styleSheet><numFmts><numFmt numFmtId="164" formatCode="yyyy-mm-dd"/><numFmt numFmtId="165" formatCode="[h]:mm:ss"/></numFmts>` +
			`<cellStyleXfs><xf numFmtId="14"/></cellStyleXfs><cellXfs><xf numFmtId="0"/><xf numFmtId="164" applyNumberFormat="1"><alignment/></xf><xf numFmtId="22"/><xf numFmtId="165"/></cellXfs>` +
			`<dxfs><dxf><numFmt numFmtId="164" formatCode="0.00"/></dxf></dxfs></styleSheet>`},
		part{"xl/worksheets/sheet1.xml", `<worksheet><sheetData>
<row r="1"><c r="A1" t="s"><v>0</v></c><c r="B1" t="s"><v>1</v></c><c r="C1" t="inlineStr"><is><t>Hours</t></is></c><c r="D1" t="s"><v>3</v></c></row>
<row r="2"><c r="A2" t="str"><f>A3</f><v>Write</v></c><c r="B2" s="1"><v>45943</v></c><c r="C2" s="3"><v>1.5</v></c></row>
<row r="4"><c r="A4" t="s"><v>2</v></c><c s="2"><v>45943.5</v></c><c t="b"><v>1</v></c><c r="E4" t="e"><v>#DIV/0!</v></c></row>
<row r="5"><c r="A5" t="s"><v>3</v></c></row>
</sheetData></worksheet>`},
		part{"xl/worksheets/sheet2.xml", `<worksheet><sheetData/></worksheet>`},
		part{"xl/worksheets/sheet3.xml", `<worksheet><sheetData><row><c r="C7"><v>7</v></c></row></sheetData></worksheet>`},
	))
	text, err := SpreadsheetMarkdown(path)
	if err != nil {
		t.Fatal(err)
	}
	expect(t, text, "## Plan\n\n| Task | Due | Hours |  |  |\n| --- | --- | --- | --- | --- |\n| Write | 2025-10-13 | 1.5 |  |  |\n"+
		"| Line<br>two | 2025-10-13 12:00:00 | true |  | #DIV/0! |\n\n## Later\n\n| 7 |\n| --- |")
}

func TestDatesCountFrom1904WhenTheWorkbookSays(t *testing.T) {
	// What Excel for Mac wrote before 2011.
	path := writeFile(t, "mac.xlsm", testXLSX(
		`<workbookPr date1904="1"/><sheets><sheet name="Dates" sheetId="1" r:id="rId1"/></sheets>`,
		xlsxRel("rId1", "worksheet", "worksheets/sheet1.xml")+xlsxRel("rId2", "styles", "styles.xml"),
		part{"xl/styles.xml", `<styleSheet><cellXfs><xf numFmtId="0"/><xf numFmtId="14"/></cellXfs></styleSheet>`},
		part{"xl/worksheets/sheet1.xml", `<worksheet><sheetData><row><c s="1"><v>44481</v></c><c><v>44481</v></c></row></sheetData></worksheet>`},
	))
	text, err := SpreadsheetMarkdown(path)
	if err != nil {
		t.Fatal(err)
	}
	expect(t, text, "## Dates\n\n| 2025-10-13 | 44481 |\n| --- | --- |")
}

func TestAFileThatIsNoSpreadsheetSaysSo(t *testing.T) {
	for name, data := range map[string][]byte{
		"plain.xlsx": []byte("plain text"),
		"word.xlsx":  zipOf(part{"word/document.xml", "<document/>"}),
		"text.ods": zipOf(part{"mimetype", "application/vnd.oasis.opendocument.text"},
			part{"content.xml", "<office:document-content/>"}),
	} {
		if _, err := SpreadsheetMarkdown(writeFile(t, name, data)); err == nil || err.Error() != "This spreadsheet can't be read." {
			t.Errorf("%s: %v", name, err)
		}
	}
}
