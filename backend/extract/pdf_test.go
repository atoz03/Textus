package extract

import (
	"bytes"
	"fmt"
	"testing"
)

// pdfOf is a PDF of objects, numbered from 1 (the first the catalog), its
// cross-reference table worked out as it is written; trailer is more of
// the trailer dictionary.
func pdfOf(trailer string, objects ...string) []byte {
	var b bytes.Buffer
	b.WriteString("%PDF-1.4\n")
	at := make([]int, len(objects))
	for i, o := range objects {
		at[i] = b.Len()
		fmt.Fprintf(&b, "%d 0 obj\n%s\nendobj\n", i+1, o)
	}
	xref := b.Len()
	fmt.Fprintf(&b, "xref\n0 %d\n0000000000 65535 f \n", len(objects)+1)
	for _, n := range at {
		fmt.Fprintf(&b, "%010d 00000 n \n", n)
	}
	fmt.Fprintf(&b, "trailer\n<< /Size %d /Root 1 0 R %s >>\nstartxref\n%d\n%%%%EOF\n", len(objects)+1, trailer, xref)
	return b.Bytes()
}

// pdfStreamOf is a stream object of data, with more of its dictionary.
func pdfStreamOf(dict, data string) string {
	return fmt.Sprintf("<< %s /Length %d >>\nstream\n%s\nendstream", dict, len(data), data)
}

// pdfPagesOf is a PDF of a page for each content stream, font (object 3)
// their /F1, the objects more numbered from 4 and the first their /X1.
func pdfPagesOf(font string, more []string, contents ...string) []byte {
	objects := append([]string{"<< /Type /Catalog /Pages 2 0 R >>", "", font}, more...)
	kids := ""
	for _, c := range contents {
		page := len(objects) + 1
		kids += fmt.Sprintf(" %d 0 R", page)
		objects = append(objects,
			fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 3 0 R >> /XObject << /X1 4 0 R >> >> /Contents %d 0 R >>", page+1),
			c)
	}
	objects[1] = fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>", kids, len(contents))
	return pdfOf("", objects...)
}

// pdfDrawn is a content stream that draws text with /F1 at 10 points,
// from (72, 700).
func pdfDrawn(text string) string {
	return pdfStreamOf("", "BT /F1 10 Tf 72 700 Td "+text+" ET")
}

const pdfHelvetica = "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica /Encoding /WinAnsiEncoding >>"

func pdfTextIs(t *testing.T, data []byte, want string) {
	t.Helper()
	got, err := PDFText(writeFile(t, "t.pdf", data))
	if err != nil || got != want {
		t.Fatalf("got %q, %v\nwant %q", got, err, want)
	}
}

func TestPDFWordsLinesAndParagraphsAreWhereTheTextIsDrawn(t *testing.T) {
	pdfTextIs(t, pdfPagesOf(pdfHelvetica, nil,
		pdfDrawn(`[(Hel) -20 (lo) -300 (world)] TJ 0 -12 Td (Next line) Tj 0 -30 Td (A paragraph, caf\351.) Tj`)),
		"Hello world\nNext line\n\nA paragraph, café.")
}

func TestPDFCJKTextIsReadByItsToUnicodeMapAndRadicalsAsIdeographs(t *testing.T) {
	toUnicode := pdfStreamOf("", `/CIDInit /ProcSet findresource begin 12 dict begin begincmap
1 begincodespacerange <0000> <FFFF> endcodespacerange
2 beginbfchar <0001> <4F60> <0002> <597D> endbfchar
2 beginbfrange <0003> <0004> [<2ED4> <2F2F>] <0005> <0006> <0041> endbfrange
endcmap CMapName currentdict /CMap defineresource pop end end`)
	font := "<< /Type /Font /Subtype /Type0 /BaseFont /Song /Encoding /Identity-H /DescendantFonts [4 0 R] /ToUnicode 5 0 R >>"
	cidFont := "<< /Type /Font /Subtype /CIDFontType2 /BaseFont /Song /DW 1000 >>"
	// Characters 0.2 em apart are one word; 0.4 em, two.
	pdfTextIs(t, pdfPagesOf(font, []string{cidFont, toUnicode},
		pdfDrawn(`[<00010002> -200 <0003> -400 <0004> -400 <00050006>] TJ`)),
		"你好门 工 AB")
}

func TestPDFGlyphNamesSayWhatADifferencesArrayDraws(t *testing.T) {
	font := "<< /Type /Font /Subtype /Type1 /BaseFont /Times-Roman /Encoding << /BaseEncoding /WinAnsiEncoding /Differences [1 /fi /Gamma /uni00E9 /u1F600 /endash] >> >>"
	pdfTextIs(t, pdfPagesOf(font, nil, pdfDrawn(`(\001ne \002 caf\003 \004 a\005b) Tj`)),
		"fine Γ café 😀 a–b")
}

func TestPDFTeXFontsAreReadByTheirOwnEncodingAndAccentsPlaced(t *testing.T) {
	program := `%!PS-AdobeFont-1.0: CMR10 003.002
/FontName /CMR10 def
/Encoding 256 array
0 1 255 {1 index exch /.notdef put} for
dup 12 /fi put
dup 16 /dotlessi put
dup 34 /quotedblright put
dup 92 /quotedblleft put
dup 123 /endash put
dup 127 /dieresis put
readonly def
currentfile eexec
` + "\x8f\x9a dup 65 /Gamma put (("
	font := "<< /Type /Font /Subtype /Type1 /BaseFont /CMR10 /FontDescriptor 4 0 R >>"
	descriptor := "<< /Type /FontDescriptor /FontName /CMR10 /Flags 4 /FontFile 5 0 R >>"
	// TeX draws ï as ¨ and then ı, moved back under it.
	pdfTextIs(t, pdfPagesOf(font, []string{descriptor, pdfStreamOf("", program)},
		pdfDrawn(`[(\014ne \\quoted" na\177) 500 (\020ve \173 An) -10 (gstr\177) 500 (om)] TJ`)),
		"fine “quoted” naïve – Angström")
}

func TestPDFFormsAndPagesAreReadInOrder(t *testing.T) {
	form := pdfStreamOf("/Type /XObject /Subtype /Form /BBox [0 0 612 792] /Matrix [1 0 0 1 0 -50] /Resources << /Font << /F1 3 0 R >> >>",
		"BT /F1 10 Tf 72 700 Td (In the form) Tj ET")
	pdfTextIs(t, pdfPagesOf(pdfHelvetica, []string{form},
		pdfStreamOf("", "BT /F1 10 Tf 72 700 Td (Before) Tj ET /X1 Do BT /F1 10 Tf 72 600 Td (After) Tj ET"),
		pdfDrawn("(Page two) Tj")),
		"Before\n\nIn the form\n\nAfter\n\nPage two")
}

func TestAPDFPageThatCantBeUnpackedSaysSo(t *testing.T) {
	pdfTextIs(t, pdfPagesOf(pdfHelvetica, nil,
		pdfDrawn("(Page one) Tj"),
		pdfStreamOf("/Filter /LZWDecode", "\x80\x0b\x60\x50")),
		"Page one\n\n[page 2 can't be read]")
}

func TestAPDFAfterOtherBytesAndOfALaterVersionReads(t *testing.T) {
	file := bytes.Replace(pdfPagesOf(pdfHelvetica, nil, pdfDrawn("(Hello) Tj")), []byte("%PDF-1.4"), []byte("%PDF-2.0"), 1)
	data := append([]byte("Content-Type: application/pdf\r\n\r\n"), file...)
	pdfTextIs(t, append(data, "\r\n--boundary--\r\n"...), "Hello")
}

func TestPDFsThatCantBeReadSaySo(t *testing.T) {
	for _, c := range []struct {
		data []byte
		want error
	}{
		{[]byte("PK\x03\x04 a zip"), errPDFUnreadable},
		{[]byte("%PDF-1.4 cut short"), errPDFUnreadable},
		{pdfPagesOf(pdfHelvetica, nil, pdfStreamOf("", "0 0 m 100 100 l S")), errPDFNoText},
		{pdfOf("/Encrypt << /Filter /Standard /V 5 /R 6 /Length 256 /O <00> /U <00> /P -4 >> /ID [<01> <01>]",
			"<< /Type /Catalog /Pages 2 0 R >>", "<< /Type /Pages /Kids [] /Count 0 >>"), errPDFEncrypted},
	} {
		if _, err := PDFTextOf(c.data); err != c.want {
			t.Errorf("%.20q: %v, want %v", c.data, err, c.want)
		}
	}
	if _, err := PDFText(t.TempDir() + "/none.pdf"); err == nil {
		t.Error("a missing file read")
	}
}

func TestPDFBlankLinesFoldAndLineEndsAreTrimmed(t *testing.T) {
	if got := tidyLines("\n\na  \n\n\n\nb\t\n \n\n"); got != "a\n\nb" {
		t.Fatalf("%q", got)
	}
}

func TestRadicalsAndStrokesReadAsTheirIdeographs(t *testing.T) {
	for from, want := range map[rune]rune{'⻔': '门', '⼯': '工', '㇐': '一', '⺇': '𠘨', 'A': 'A', '门': '门'} {
		if got := ideograph(from); got != want {
			t.Errorf("%c: %c, want %c", from, got, want)
		}
	}
}
