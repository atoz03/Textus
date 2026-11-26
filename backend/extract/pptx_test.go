package extract

import (
	"archive/zip"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// zipOf is a zip of parts, stored, in order.
func zipOf(parts ...part) []byte {
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	for _, p := range parts {
		w, _ := z.CreateHeader(&zip.FileHeader{Name: p.name, Method: zip.Store})
		w.Write([]byte(p.xml))
	}
	z.Close()
	return b.Bytes()
}

// writeFile writes data as name in a temporary folder and answers its path.
func writeFile(t *testing.T, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// convertPptx is the Markdown of a .pptx of slides (each the inside of
// <p:spTree>) and parts.
func convertPptx(t *testing.T, slides []string, parts ...part) string {
	t.Helper()
	var all []part
	for i, tree := range slides {
		all = append(all, part{
			fmt.Sprintf("ppt/slides/slide%d.xml", i+1),
			`<p:sld xmlns:p="p" xmlns:a="a" xmlns:r="r"><p:cSld><p:spTree>` + tree + `</p:spTree></p:cSld></p:sld>`,
		})
	}
	md, err := PptxMarkdown(writeFile(t, "t.pptx", zipOf(append(all, parts...)...)))
	if err != nil {
		t.Fatal(err)
	}
	return md
}

func sp(ph string, y int, paragraphs string) string {
	return fmt.Sprintf(`<p:sp><p:nvSpPr><p:nvPr>%s</p:nvPr></p:nvSpPr><p:spPr><a:xfrm><a:off x="0" y="%d"/></a:xfrm></p:spPr><p:txBody><a:bodyPr/>%s</p:txBody></p:sp>`, ph, y, paragraphs)
}

func para(text string) string { return "<a:p><a:r><a:t>" + text + "</a:t></a:r></a:p>" }

func TestSlidesGetTheirTitleBodyListAndNotes(t *testing.T) {
	body := para("Why") +
		`<a:p><a:pPr lvl="1"/><a:r><a:rPr b="1"/><a:t>Because</a:t></a:r></a:p>` +
		`<a:p><a:pPr><a:buNone/></a:pPr><a:r><a:t>Aside</a:t></a:r></a:p>` +
		strings.Repeat(`<a:p><a:pPr><a:buAutoNum type="arabicPeriod" startAt="4"/></a:pPr><a:r><a:t>Fourth</a:t></a:r></a:p>`, 2)
	// In the file the body comes first, the footer is template furniture,
	// and the text box sits below the body on the slide.
	slide := sp(`<p:ph idx="1"/>`, 100, body) +
		sp("", 900, para("A text box")) +
		sp(`<p:ph type="sldNum"/>`, 999, para("7")) +
		sp(`<p:ph type="title"/>`, 0, para("The plan"))
	notes := `<p:notes xmlns:p="p" xmlns:a="a"><p:cSld><p:spTree>` + sp(`<p:ph type="body" idx="1"/>`, 0, para("Say it slowly.")) + `</p:spTree></p:cSld></p:notes>`
	got := convertPptx(t, []string{slide},
		part{"ppt/slides/_rels/slide1.xml.rels", `<Relationships><Relationship Id="rId2" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/notesSlide" Target="../notesSlides/notesSlide1.xml"/></Relationships>`},
		part{"ppt/notesSlides/notesSlide1.xml", notes})
	expect(t, got, "## Slide 1: The plan\n\n- Why\n    - **Because**\n\nAside\n\n4. Fourth\n5. Fourth\n\nA text box\n\n### Notes\n\nSay it slowly.")
}

func TestTablesPicturesAndGroupsAreReadInPlace(t *testing.T) {
	cell := func(text string) string { return "<a:tc><a:txBody>" + para(text) + "</a:txBody><a:tcPr/></a:tc>" }
	table := `<p:graphicFrame><p:xfrm><a:off x="0" y="500"/></p:xfrm><a:graphic><a:graphicData><a:tbl><a:tblGrid/><a:tr>` +
		cell("Q") + cell("Sales") + `</a:tr><a:tr>` + cell("Q1") + cell("12") + `</a:tr></a:tbl></a:graphicData></a:graphic></p:graphicFrame>`
	picture := `<p:pic><p:nvPicPr><p:cNvPr id="4" name="Picture 3" descr="Sales chart"/></p:nvPicPr><p:blipFill><a:blip r:embed="rId3"/></p:blipFill><p:spPr><a:xfrm><a:off x="0" y="200"/></a:xfrm></p:spPr></p:pic>`
	group := `<p:grpSp><p:nvGrpSpPr/><p:grpSpPr><a:xfrm><a:off x="0" y="100"/></a:xfrm></p:grpSpPr>` +
		sp("", 20, para("second in group")) + sp("", 10, para("first in group")) + `</p:grpSp>`
	got := convertPptx(t, []string{table + picture + group},
		part{"ppt/slides/_rels/slide1.xml.rels", `<Relationships><Relationship Id="rId3" Type="image" Target="../media/image3.png"/></Relationships>`})
	expect(t, got, "## Slide 1\n\nfirst in group\n\nsecond in group\n\n![Sales chart](image3.png)\n\n| Q | Sales |\n| --- | --- |\n| Q1 | 12 |")
}

func TestSlidesAreNumberedByFileAndEmptyOnesDropped(t *testing.T) {
	slides := make([]string, 9, 10)
	slides = append(slides, sp(`<p:ph type="ctrTitle"/>`, 0, para("Hello &amp; welcome")))
	expect(t, convertPptx(t, slides), "## Slide 10: Hello & welcome")
	// Nothing to read is no text, which the caller says.
	expect(t, convertPptx(t, []string{""}), "")
}

func TestALinkInARunIsKeptAndALevelPastPowerPointsIsTheLast(t *testing.T) {
	body := `<a:p><a:r><a:rPr><a:hlinkClick r:id="rId1"/></a:rPr><a:t>the site</a:t></a:r></a:p>` +
		`<a:p><a:pPr lvl="99"/><a:r><a:t>deep</a:t></a:r></a:p>`
	got := convertPptx(t, []string{sp(`<p:ph idx="1"/>`, 0, body)},
		part{"ppt/slides/_rels/slide1.xml.rels", `<Relationships><Relationship Id="rId1" Type="hyperlink" Target="https://example.com/" TargetMode="External"/></Relationships>`})
	expect(t, got, "## Slide 1\n\n- [the site](https://example.com/)\n    - deep")
}

func TestAFileThatIsNotAPptxSaysSo(t *testing.T) {
	_, err := PptxMarkdown(writeFile(t, "x.pptx", []byte("not a zip")))
	if err == nil || err.Error() != "This .pptx is not a readable PowerPoint file." {
		t.Errorf("%v", err)
	}
}
