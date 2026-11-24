package backend

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/egoist/mygo"
)

// pdfFixture is a plain PDF in the shape a renderer writes: resources and
// media box shared on the Pages node, one text line a page ("Body N"),
// origin as the media box's corner. Each page leaves a transform
// unbalanced, as some renderers do: the number must not inherit it.
func pdfFixture(pages int, origin [2]int) []byte {
	var b bytes.Buffer
	b.WriteString("%PDF-1.4\n")
	offsets := map[int]int{}
	obj := func(n int, body string) {
		offsets[n] = b.Len()
		fmt.Fprintf(&b, "%d 0 obj\n%s\nendobj\n", n, body)
	}
	// 1 catalog, 2 pages, 3 font, 4 resources, then content and page pairs.
	obj(1, "<< /Type /Catalog /Pages 2 0 R >>")
	var kids []string
	for i := range pages {
		kids = append(kids, fmt.Sprintf("%d 0 R", 6+2*i))
	}
	obj(2, fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d /Resources 4 0 R /MediaBox [%d %d %d %d] >>",
		strings.Join(kids, " "), pages, origin[0], origin[1], origin[0]+595, origin[1]+842))
	obj(3, "<< /Type /Font /Subtype /Type1 /BaseFont /Times-Roman >>")
	obj(4, "<< /Font << /F1 3 0 R >> >>")
	for i := range pages {
		text := fmt.Sprintf("2 0 0 2 0 0 cm\nBT /F1 12 Tf %d %d Td (Body %d) Tj ET\n", origin[0]+30, origin[1]+300, i+1)
		obj(5+2*i, fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(text), text))
		obj(6+2*i, fmt.Sprintf("<< /Type /Page /Parent 2 0 R /Contents %d 0 R >>", 5+2*i))
	}
	size := 5 + 2*pages
	xref := b.Len()
	fmt.Fprintf(&b, "xref\n0 %d\n0000000000 65535 f \n", size)
	for n := 1; n < size; n++ {
		fmt.Fprintf(&b, "%010d 00000 n \n", offsets[n])
	}
	fmt.Fprintf(&b, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", size, xref)
	return b.Bytes()
}

// compressedFixture is two pages whose page objects sit in a compressed
// object stream, listed by a cross-reference stream with a PNG predictor,
// as Chromium and Cairo can write them.
func compressedFixture() []byte {
	var b bytes.Buffer
	b.WriteString("%PDF-1.5\n")
	offsets := map[int]int{}
	obj := func(n int, body string) {
		offsets[n] = b.Len()
		fmt.Fprintf(&b, "%d 0 obj\n%s\nendobj\n", n, body)
	}
	deflate := func(data []byte) []byte {
		var z bytes.Buffer
		w := zlib.NewWriter(&z)
		w.Write(data)
		w.Close()
		return z.Bytes()
	}
	obj(1, "<< /Type /Catalog /Pages 2 0 R >>")
	obj(2, "<< /Type /Pages /Kids [4 0 R 5 0 R] /Count 2 /MediaBox [0 0 612 792] >>")
	for i, text := range []string{"BT (One) Tj ET", "BT (Two) Tj ET"} {
		obj(6+i, fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(text), text))
	}
	// Object stream 3 holds pages 4 and 5.
	page4 := "<< /Type /Page /Parent 2 0 R /Contents 6 0 R /Resources << >> >>"
	page5 := "<< /Type /Page /Parent 2 0 R /Contents [7 0 R] >>"
	header := fmt.Sprintf("4 0 5 %d ", len(page4)+1)
	objects := page4 + " " + page5
	packed := deflate([]byte(header + objects))
	offsets[3] = b.Len()
	fmt.Fprintf(&b, "3 0 obj\n<< /Type /ObjStm /N 2 /First %d /Filter /FlateDecode /Length %d >>\nstream\n", len(header), len(packed))
	b.Write(packed)
	b.WriteString("\nendstream\nendobj\n")
	// The cross-reference stream, object 8, with PNG Up rows.
	xref := b.Len()
	var rows bytes.Buffer
	prev := make([]byte, 7)
	entry := func(kind byte, a uint32, c uint16) {
		row := make([]byte, 7)
		row[0] = kind
		binary.BigEndian.PutUint32(row[1:5], a)
		binary.BigEndian.PutUint16(row[5:7], c)
		rows.WriteByte(2) // Up
		for i := range row {
			rows.WriteByte(row[i] - prev[i])
		}
		prev = row
	}
	entry(0, 0, 65535)
	entry(1, uint32(offsets[1]), 0)
	entry(1, uint32(offsets[2]), 0)
	entry(1, uint32(offsets[3]), 0)
	entry(2, 3, 0)
	entry(2, 3, 1)
	entry(1, uint32(offsets[6]), 0)
	entry(1, uint32(offsets[7]), 0)
	entry(1, uint32(xref), 0)
	packed = deflate(rows.Bytes())
	fmt.Fprintf(&b, "8 0 obj\n<< /Type /XRef /Size 9 /W [1 4 2] /Root 1 0 R /Filter /FlateDecode /DecodeParms << /Predictor 12 /Columns 7 >> /Length %d >>\nstream\n", len(packed))
	b.Write(packed)
	fmt.Fprintf(&b, "\nendstream\nendobj\nstartxref\n%d\n%%%%EOF\n", xref)
	return b.Bytes()
}

// pageTexts reads each page's content streams back, joined.
func pageTexts(t *testing.T, pdf []byte) []string {
	t.Helper()
	f, err := readPDF(pdf)
	if err != nil {
		t.Fatal(err)
	}
	pages, err := f.pages()
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, p := range pages {
		contents, _ := p.dict.get("Contents")
		contents, _ = f.resolve(contents)
		parts := contents.list
		if contents.kind != pdfArray {
			parts = []pdfValue{contents}
		}
		var text strings.Builder
		for _, part := range parts {
			ref := part
			if ref.kind != pdfRef {
				t.Fatalf("content %v is no reference", part)
			}
			e := f.xref[ref.num]
			_, _, data, err := f.objectAt(f.data, e.offset, true)
			if err != nil {
				t.Fatal(err)
			}
			text.Write(data)
		}
		out = append(out, text.String())
	}
	return out
}

func TestEveryPageGetsItsNumberAfterItsContent(t *testing.T) {
	numbered, err := numberPages(pdfFixture(3, [2]int{}))
	if err != nil {
		t.Fatal(err)
	}
	texts := pageTexts(t, numbered)
	if len(texts) != 3 {
		t.Fatalf("%d pages", len(texts))
	}
	for i, text := range texts {
		n := i + 1
		body := strings.Index(text, fmt.Sprintf("(Body %d) Tj", n))
		number := strings.Index(text, fmt.Sprintf("(%d) Tj", n))
		if body < 0 || number < body || !strings.HasPrefix(text, "q\n") ||
			!strings.Contains(text[body:], "Q\nq\nBT\n/TextusPageNo 9 Tf") {
			t.Errorf("page %d: %q", n, text)
		}
	}
}

func TestTheNumberIsCentredInTheBottomBand(t *testing.T) {
	numbered, err := numberPages(pdfFixture(12, [2]int{}))
	if err != nil {
		t.Fatal(err)
	}
	texts := pageTexts(t, numbered)
	// 595pt wide; "1" is 5.004pt wide at 9pt, "12" twice that.
	if !strings.Contains(texts[0], "1 0 0 1 295.00 28.00 Tm") || !strings.Contains(texts[11], "1 0 0 1 292.50 28.00 Tm") {
		t.Errorf("%q\n%q", texts[0], texts[11])
	}
}

func TestASheetCutFromATallerRenderIsNumberedInItsOwnBox(t *testing.T) {
	numbered, err := numberPages(pdfFixture(2, [2]int{0, -1684}))
	if err != nil {
		t.Fatal(err)
	}
	if texts := pageTexts(t, numbered); !strings.Contains(texts[0], "1 0 0 1 295.00 -1656.00 Tm") {
		t.Errorf("%q", texts[0])
	}
}

func TestSharedResourcesAreCopiedNotChanged(t *testing.T) {
	original := pdfFixture(2, [2]int{})
	numbered, err := numberPages(original)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(numbered, original) {
		t.Fatal("the renderer's bytes changed")
	}
	f, err := readPDF(numbered)
	if err != nil {
		t.Fatal(err)
	}
	pages, _ := f.pages()
	for _, p := range pages {
		resources, _ := p.dict.get("Resources")
		fonts, _ := resources.get("Font")
		if _, ok := fonts.get("F1"); !ok {
			t.Errorf("page %d lost its font: %v", p.num, resources)
		}
		font, _ := fonts.get(pageNumberFont)
		font, _ = f.resolve(font)
		if base, _ := font.get("BaseFont"); !base.named("Helvetica") {
			t.Errorf("page %d: %v", p.num, font)
		}
	}
	shared, _ := f.object(4)
	if fonts, _ := shared.get("Font"); len(fonts.keys) != 1 {
		t.Errorf("the shared resources changed: %v", shared)
	}
}

func TestCompressedObjectsAndCrossReferenceStreamsAreNumbered(t *testing.T) {
	numbered, err := numberPages(compressedFixture())
	if err != nil {
		t.Fatal(err)
	}
	texts := pageTexts(t, numbered)
	if len(texts) != 2 || !strings.Contains(texts[0], "(One) Tj") || !strings.Contains(texts[1], "(Two) Tj") ||
		!strings.Contains(texts[1], "(2) Tj") {
		t.Fatalf("%q", texts)
	}
	// 612pt wide: the Letter sheet.
	if !strings.Contains(texts[0], "1 0 0 1 303.50 28.00 Tm") {
		t.Errorf("%q", texts[0])
	}
	// The update is a cross-reference stream too, linked to the first.
	tail := numbered[bytes.LastIndex(numbered, []byte("obj\n<<")):]
	if !bytes.Contains(tail, []byte("/Type /XRef")) || !bytes.Contains(tail, []byte("/Prev")) {
		t.Errorf("update section: %q", tail[:min(len(tail), 200)])
	}
}

func TestWhatIsNotAPDFIsRefused(t *testing.T) {
	for _, data := range [][]byte{nil, []byte("<html>"), []byte("%PDF-1.4\nstartxref\n999999\n%%EOF")} {
		if _, err := numberPages(data); err == nil {
			t.Errorf("%q was numbered", data)
		}
	}
}

func TestPrintOptionsFollowThePlatform(t *testing.T) {
	sheet := PdfSheet{Width: 595, Height: 842, Margin: 56.7}
	linux := pdfOptions(sheet, "linux")
	if math.Abs(linux.Margins.Top-0.7875) > 1e-9 || linux.Margins.Left != 0 || !linux.Background {
		t.Errorf("linux: %+v", linux.Margins)
	}
	for _, goos := range []string{"darwin", "windows"} {
		if o := pdfOptions(sheet, goos); (*o.Margins != mygo.Margins{}) || o.PageSize.Width != 595.0/72 {
			t.Errorf("%s: %+v %+v", goos, o.Margins, o.PageSize)
		}
	}
	if !strings.Contains(settleScript(sheet, "darwin"), "793.333px") || strings.Contains(settleScript(sheet, "linux"), "px") {
		t.Error("only macOS pins the page's width")
	}
}
