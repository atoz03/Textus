package extract

import (
	"archive/zip"
	"bytes"
	"encoding/json/v2"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const wordNS = `xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"`

// part is one more file of a test .docx: its zip path and XML, where {W}
// stands for the Word namespaces.
type part struct{ name, xml string }

// testDocx is a .docx holding body (the inside of <w:body>) and parts.
func testDocx(body string, parts ...part) []byte {
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	add := func(name, content string) {
		w, _ := z.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store})
		w.Write([]byte(content))
	}
	add("word/document.xml", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><w:document `+wordNS+`><w:body>`+body+`</w:body></w:document>`)
	for _, p := range parts {
		add(p.name, strings.ReplaceAll(p.xml, "{W}", wordNS))
	}
	z.Close()
	return b.Bytes()
}

func writeDocx(t *testing.T, body string, parts ...part) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "t.docx")
	if err := os.WriteFile(path, testDocx(body, parts...), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// read is body read for the AI.
func read(t *testing.T, body string, parts ...part) string {
	t.Helper()
	md, err := DocxMarkdown(writeDocx(t, body, parts...))
	if err != nil {
		t.Fatal(err)
	}
	return md
}

// open is body opened as a document, with the pictures it saved.
func open(t *testing.T, body string, parts ...part) (string, []string) {
	t.Helper()
	var saved []string
	md, err := DocxDocument(writeDocx(t, body, parts...), func(name string, data []byte) (string, bool) {
		saved = append(saved, name+"="+string(data))
		return fmt.Sprintf("/drafts/image-%d.png", len(saved)), true
	})
	if err != nil {
		t.Fatal(err)
	}
	return md, saved
}

func p(style, text string) string {
	return `<w:p><w:pPr><w:pStyle w:val="` + style + `"/></w:pPr><w:r><w:t>` + text + `</w:t></w:r></w:p>`
}

func item(level, num int, text string) string {
	return fmt.Sprintf(`<w:p><w:pPr><w:numPr><w:ilvl w:val="%d"/><w:numId w:val="%d"/></w:numPr></w:pPr><w:r><w:t>%s</w:t></w:r></w:p>`, level, num, text)
}

var stylesPart = part{"word/styles.xml", `<w:styles {W}>
	<w:style w:styleId="Titel"><w:name w:val="Title"/></w:style>
	<w:style w:styleId="berschrift1"><w:name w:val="heading 1"/></w:style>
	<w:style w:styleId="Chapter"><w:name w:val="Chapter"/><w:basedOn w:val="berschrift2"/></w:style>
	<w:style w:styleId="berschrift2"><w:name w:val="heading 2"/></w:style>
	<w:style w:styleId="ListBullet"><w:name w:val="List Bullet"/><w:pPr><w:numPr><w:numId w:val="1"/></w:numPr></w:pPr></w:style>
	<w:style w:styleId="Strong"><w:name w:val="Strong"/></w:style>
	<w:style w:styleId="Author"><w:name w:val="Author"/><w:basedOn w:val="Titel"/></w:style>
	<w:style w:styleId="Zitat"><w:name w:val="Quote"/></w:style>
</w:styles>`}

func expect(t *testing.T, got, want string) {
	t.Helper()
	if got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}

func TestHeadingStylesBecomeHeadingsByNameNotID(t *testing.T) {
	// German Word's ids ("berschrift1") with the English built-in names
	// Word stores whatever its language; "Chapter" is based on heading 2.
	body := p("Titel", "Book") + p("Author", "Ann") + p("berschrift1", "Part one") +
		`<w:p><w:pPr><w:pStyle w:val="Chapter"/></w:pPr><w:r><w:rPr><w:b/></w:rPr><w:t>A chapter</w:t></w:r></w:p>` +
		p("Zitat", "Quoted.") + p("Normal", "Text.")
	// pandoc's "Author" is based on "Title" and is no heading; a bold
	// heading is just a heading.
	expect(t, read(t, body, stylesPart), "# Book\n\nAnn\n\n# Part one\n\n## A chapter\n\n> Quoted.\n\nText.")
}

func TestListsAreNumberedOrBulletedAsTheNumberingPartSays(t *testing.T) {
	numbering := part{"word/numbering.xml", `<w:numbering {W}>
		<w:abstractNum w:abstractNumId="5"><w:lvl w:ilvl="0"><w:numFmt w:val="decimal"/></w:lvl><w:lvl w:ilvl="1"><w:numFmt w:val="bullet"/></w:lvl></w:abstractNum>
		<w:abstractNum w:abstractNumId="6"><w:lvl w:ilvl="0"><w:numFmt w:val="bullet"/></w:lvl></w:abstractNum>
		<w:num w:numId="1"><w:abstractNumId w:val="6"/></w:num>
		<w:num w:numId="2"><w:abstractNumId w:val="5"/></w:num>
	</w:numbering>`}
	body := item(0, 2, "First") + item(1, 2, "Detail") + item(0, 2, "Second") + p("Normal", "Between.") + p("ListBullet", "Styled bullet")
	expect(t, read(t, body, stylesPart, numbering), "1. First\n    - Detail\n2. Second\n\nBetween.\n\n- Styled bullet")
}

func TestEmphasisLinksAndBreaksSurvive(t *testing.T) {
	rels := part{"word/_rels/document.xml.rels", `<Relationships><Relationship Id="rId9" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/hyperlink" Target="https://example.com/" TargetMode="External"/></Relationships>`}
	body := `<w:p>
		<w:r><w:t xml:space="preserve">Plain </w:t></w:r>
		<w:r><w:rPr><w:b/></w:rPr><w:t>bo</w:t></w:r><w:r><w:rPr><w:b/></w:rPr><w:t>ld</w:t></w:r>
		<w:r><w:rPr><w:b w:val="0"/><w:i/></w:rPr><w:t xml:space="preserve"> italic</w:t></w:r>
		<w:r><w:rPr><w:rStyle w:val="Strong"/></w:rPr><w:t xml:space="preserve"> strong</w:t></w:r>
		<w:r><w:t xml:space="preserve"> and </w:t></w:r>
		<w:hyperlink r:id="rId9"><w:r><w:t>a link</w:t></w:r></w:hyperlink>
		<w:r><w:br/><w:t>Tom &amp; Jerry</w:t></w:r>
		<w:del><w:r><w:delText>gone</w:delText></w:r></w:del>
	</w:p>`
	expect(t, read(t, body, stylesPart, rels), "Plain **bold** *italic* **strong** and [a link](https://example.com/)\nTom & Jerry")
}

func TestTablesBecomePipeTables(t *testing.T) {
	cell := func(text string) string { return "<w:tc><w:p><w:r><w:t>" + text + "</w:t></w:r></w:p></w:tc>" }
	body := `<w:tbl><w:tblPr/><w:tblGrid/>
		<w:tr>` + cell("Name") + cell("Age") + `</w:tr>
		<w:tr><w:tc><w:tcPr><w:gridSpan w:val="2"/></w:tcPr><w:p><w:r><w:t>wide</w:t></w:r></w:p><w:p><w:r><w:t>cell</w:t></w:r></w:p></w:tc></w:tr>
		<w:tr>` + cell("Ann") + cell("41") + `</w:tr>
		<w:customXml><w:tr><w:sdt><w:sdtContent>` + cell("Bo") + `</w:sdtContent></w:sdt>` + cell("7") + `</w:tr></w:customXml>
	</w:tbl>`
	// Rows and cells inside custom XML and content controls are looked through.
	expect(t, read(t, body), "| Name | Age |\n| --- | --- |\n| wide<br>cell |  |\n| Ann | 41 |\n| Bo | 7 |")
}

func TestFootnotesAreNumberedByFirstReferenceAndListedAtTheEnd(t *testing.T) {
	notes := part{"word/footnotes.xml", `<w:footnotes {W}>
		<w:footnote w:type="separator" w:id="-1"><w:p><w:r><w:separator/></w:r></w:p></w:footnote>
		<w:footnote w:id="1"><w:p><w:r><w:footnoteRef/></w:r><w:r><w:t xml:space="preserve"> Smith, 1999.</w:t></w:r></w:p></w:footnote>
		<w:footnote w:id="2"><w:p><w:r><w:t>Ibid.</w:t></w:r></w:p></w:footnote>
	</w:footnotes>`}
	body := `<w:p><w:r><w:t>Claim</w:t></w:r><w:r><w:footnoteReference w:id="2"/></w:r><w:r><w:t xml:space="preserve"> and another</w:t></w:r><w:r><w:footnoteReference w:id="1"/></w:r><w:r><w:t>.</w:t></w:r></w:p>`
	expect(t, read(t, body, notes), "Claim[^1] and another[^2].\n\n[^1]: Ibid.\n\n[^2]: Smith, 1999.")
}

func TestImagesKeepTheirAltTextAndDrawingsAreReadOnce(t *testing.T) {
	rels := part{"word/_rels/document.xml.rels", `<Relationships><Relationship Id="rId4" Type="image" Target="media/image1.png"/></Relationships>`}
	body := `<w:p><w:r><mc:AlternateContent xmlns:mc="mc">
		<mc:Choice><w:drawing><wp:inline xmlns:wp="wp"><wp:docPr id="1" name="Picture 1" descr="" title="A [red] barn"/>
			<a:graphic xmlns:a="a"><a:graphicData><pic:pic xmlns:pic="pic"><pic:blipFill><a:blip r:embed="rId4"/></pic:blipFill></pic:pic></a:graphicData></a:graphic>
		</wp:inline></w:drawing></mc:Choice>
		<mc:Fallback><w:pict><v:imagedata xmlns:v="v" r:id="rId4"/></w:pict></mc:Fallback>
	</mc:AlternateContent></w:r></w:p>`
	expect(t, read(t, body, rels), "![A red barn](media/image1.png)")
}

func TestNumberedItemsShowTheNumberWordShows(t *testing.T) {
	// Level 0 starts at 5; numId 3 is "Restart at 1" on the same list.
	numbering := part{"word/numbering.xml", `<w:numbering {W}>
		<w:abstractNum w:abstractNumId="0">
			<w:lvl w:ilvl="0"><w:start w:val="5"/><w:numFmt w:val="decimal"/></w:lvl>
			<w:lvl w:ilvl="1"><w:start w:val="1"/><w:numFmt w:val="lowerLetter"/></w:lvl>
		</w:abstractNum>
		<w:num w:numId="2"><w:abstractNumId w:val="0"/></w:num>
		<w:num w:numId="3"><w:abstractNumId w:val="0"/><w:lvlOverride w:ilvl="0"><w:startOverride w:val="1"/></w:lvlOverride></w:num>
	</w:numbering>`}
	body := item(0, 2, "Five") + item(1, 2, "a") + item(1, 2, "b") + item(0, 2, "Six") + item(1, 2, "a again") +
		p("Normal", "Between.") + item(0, 2, "Seven, after the paragraph") + item(0, 3, "Restarted") + item(0, 3, "Two")
	expect(t, read(t, body, numbering),
		"5. Five\n    1. a\n    2. b\n6. Six\n    1. a again\n\nBetween.\n\n7. Seven, after the paragraph\n1. Restarted\n2. Two")
}

func TestAListLevelPastWordsNineCountsAsTheLast(t *testing.T) {
	n := parseNumbering(nil)
	if got := n.nextNumber("1", 4_000_000_000); got != 1 || len(n.counters["1"]) != 9 {
		t.Error(got, n.counters)
	}
	// A list may start at 0, and counts on from it.
	n.starts[levelKey{"a", 0}] = 0
	n.abstractOf["z"] = "a"
	if a, b := n.nextNumber("z", 0), n.nextNumber("z", 0); a != 0 || b != 1 {
		t.Error(a, b)
	}
}

// runs is text as the runs of one paragraph: a \n is a line break.
func runs(text string) string {
	text = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(text)
	var out []string
	for _, line := range strings.Split(text, "\n") {
		out = append(out, `<w:r><w:t xml:space="preserve">`+line+`</w:t></w:r>`)
	}
	return strings.Join(out, "<w:r><w:br/></w:r>")
}

// The text of testdata/word-text.json as Word would hold it, opened: the
// Markdown is testdata/word-text.md, which a frontend test reads back
// (src/document-view/word-text.test.ts).
func TestOpenedAsADocumentTextReadsBackAsTheSameWords(t *testing.T) {
	raw, err := os.ReadFile("testdata/word-text.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Heading    string     `json:"heading"`
		Paragraphs []string   `json:"paragraphs"`
		Table      [][]string `json:"table"`
	}
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	var body strings.Builder
	body.WriteString(`<w:p><w:pPr><w:outlineLvl w:val="0"/></w:pPr>` + runs(fixture.Heading) + `</w:p>`)
	for _, text := range fixture.Paragraphs {
		body.WriteString("<w:p>" + runs(text) + "</w:p>")
	}
	body.WriteString("<w:tbl>")
	for _, row := range fixture.Table {
		body.WriteString("<w:tr>")
		for _, cell := range row {
			body.WriteString("<w:tc><w:p>" + runs(cell) + "</w:p></w:tc>")
		}
		body.WriteString("</w:tr>")
	}
	body.WriteString("</w:tbl>")
	want, err := os.ReadFile("testdata/word-text.md")
	if err != nil {
		t.Fatal(err)
	}
	md, _ := open(t, body.String())
	expect(t, md, strings.TrimRight(string(want), " \t\r\n"))
	// The AI reads the same file as written, unescaped.
	if ai := read(t, body.String()); !strings.Contains(ai, "Price: $5 and $10, *each* or _two_") {
		t.Error(ai)
	}
}

func TestOpenedAsADocumentEmphasisListsAndFootnotesStillMeanMarkdown(t *testing.T) {
	notes := part{"word/footnotes.xml", `<w:footnotes {W}><w:footnote w:id="1"><w:p><w:r><w:t xml:space="preserve">See [1], p. 5*</w:t></w:r></w:p></w:footnote></w:footnotes>`}
	numbering := part{"word/numbering.xml", `<w:numbering {W}><w:abstractNum w:abstractNumId="0"><w:lvl w:ilvl="0"><w:numFmt w:val="decimal"/></w:lvl></w:abstractNum><w:num w:numId="1"><w:abstractNumId w:val="0"/></w:num></w:numbering>`}
	body := `
		<w:p><w:r><w:rPr><w:b/></w:rPr><w:t>2 * 3</w:t></w:r><w:r><w:t xml:space="preserve"> is six</w:t></w:r><w:r><w:footnoteReference w:id="1"/></w:r></w:p>
		<w:p><w:pPr><w:numPr><w:ilvl w:val="0"/><w:numId w:val="1"/></w:numPr></w:pPr><w:r><w:t>1. item</w:t></w:r><w:r><w:br/><w:t># line</w:t></w:r></w:p>
		<w:p><w:pPr><w:numPr><w:ilvl w:val="0"/><w:numId w:val="1"/></w:numPr></w:pPr><w:r><w:t>_two_</w:t></w:r></w:p>`
	md, _ := open(t, body, notes, numbering)
	expect(t, md, "**2 \\* 3** is six[^1]\n\n1. 1\\. item\\\n   \\# line\n2. \\_two\\_\n\n[^1]: See \\[1], p. 5\\*")
}

func TestOpenedAsADocumentPicturesAreSavedOnceAndLinked(t *testing.T) {
	rels := part{"word/_rels/document.xml.rels", `<Relationships>
		<Relationship Id="rId4" Type="image" Target="media/image1.png"/>
		<Relationship Id="rId5" Type="image" Target="https://example.com/logo.png" TargetMode="External"/>
	</Relationships>`}
	picture := func(id, alt string) string {
		return `<w:p><w:r><w:drawing><wp:inline xmlns:wp="wp"><wp:docPr id="1" descr="` + alt + `"/><a:graphic xmlns:a="a"><a:graphicData><pic:pic xmlns:pic="pic"><pic:blipFill><a:blip r:embed="` + id + `"/></pic:blipFill></pic:pic></a:graphicData></a:graphic></wp:inline></w:drawing></w:r></w:p>`
	}
	body := picture("rId4", "A barn") + picture("rId4", "The barn again") + picture("rId5", "Logo")
	md, saved := open(t, body, rels, part{"word/media/image1.png", "PNG bytes"})
	expect(t, md, "![A barn](/drafts/image-1.png)\n\n![The barn again](/drafts/image-1.png)\n\n![Logo](https://example.com/logo.png)")
	if !slices.Equal(saved, []string{"image1.png=PNG bytes"}) {
		t.Error(saved)
	}
}

func TestAFileThatIsNotADocxSaysSo(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.docx")
	os.WriteFile(path, []byte("not a zip"), 0o644)
	if _, err := DocxMarkdown(path); err == nil || err.Error() != "This .docx is not a readable Word file." {
		t.Error(err)
	}
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	z.Create("word/styles.xml")
	z.Close()
	os.WriteFile(path, b.Bytes(), 0o644)
	if _, err := DocxMarkdown(path); err == nil || err.Error() != "This .docx has no document body." {
		t.Error(err)
	}
}
