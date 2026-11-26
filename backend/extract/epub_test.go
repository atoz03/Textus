package extract

import (
	"fmt"
	"testing"
)

// testEpub is an .epub of chapters (href, the inside of <body>), its spine
// in the reverse of its manifest's order.
func testEpub(chapters [][2]string) []byte {
	parts := []part{
		{"mimetype", "application/epub+zip"},
		{"META-INF/container.xml", `<container><rootfiles><rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`},
	}
	manifest, spine := "", ""
	for i, c := range chapters {
		manifest += fmt.Sprintf(`<item id="c%d" href="%s" media-type="application/xhtml+xml"/>`, i, c[0])
		// Reading order is the spine's, not the manifest's.
		spine = fmt.Sprintf(`<itemref idref="c%d"/>`, i) + spine
	}
	parts = append(parts, part{"OEBPS/content.opf", `<package xmlns:dc="http://purl.org/dc/elements/1.1/"><metadata><dc:title>A Book</dc:title><dc:creator>Ann</dc:creator><dc:creator>Bo</dc:creator></metadata><manifest>` + manifest + `</manifest><spine>` + spine + `</spine></package>`})
	for _, c := range chapters {
		parts = append(parts, part{"OEBPS/" + percentDecode(c[0]), `<?xml version="1.0"?><html xmlns="http://www.w3.org/1999/xhtml"><head><title>x</title></head><body>` + c[1] + `</body></html>`})
	}
	return zipOf(parts...)
}

func TestChaptersAreReadInSpineOrderAfterTheMetadata(t *testing.T) {
	book := testEpub([][2]string{
		{"text/Chapter%202.xhtml", "<h1>Two</h1><p>Later&nbsp;on.</p>"},
		{"text/one.xhtml", "<h1>One</h1><p>It <i>began</i>.</p>"},
	})
	md, err := EpubMarkdown(writeFile(t, "book.epub", book))
	if err != nil {
		t.Fatal(err)
	}
	expect(t, md, "**Title:** A Book\n**Authors:** Ann, Bo\n\n# One\n\nIt *began*.\n\n# Two\n\nLater\u00a0on.")
}

func TestABookWithoutChapterTextHasNoText(t *testing.T) {
	md, err := EpubMarkdown(writeFile(t, "book.epub", testEpub([][2]string{{"one.xhtml", " "}})))
	if err != nil || md != "" {
		t.Errorf("%q %v", md, err)
	}
}

func TestAZipThatIsNoBookSaysSo(t *testing.T) {
	for _, data := range [][]byte{[]byte("not a zip"), zipOf(part{"mimetype", "application/epub+zip"})} {
		_, err := EpubMarkdown(writeFile(t, "x.epub", data))
		if err == nil || err.Error() != "This .epub is not a readable e-book." {
			t.Errorf("%v", err)
		}
	}
}

func TestPercentEscapesDecodeAndMalformedOnesStay(t *testing.T) {
	expect(t, percentDecode("Chapter%201.xhtml"), "Chapter 1.xhtml")
	expect(t, percentDecode("100%.xhtml"), "100%.xhtml")
	expect(t, percentDecode("%zz%4"), "%zz%4")
}
