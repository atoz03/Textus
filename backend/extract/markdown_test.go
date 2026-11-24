package extract

import (
	"strings"
	"testing"
)

var bold = marks{bold: true}

func TestRunsWithTheSameMarksMergeBeforeEmphasis(t *testing.T) {
	var line inline
	line.push("He", bold, "")
	line.push("llo", bold, "")
	line.push(" ", marks{}, "")
	line.push("world", bold, "")
	line.push(" and more", marks{}, "")
	expect(t, line.render(), "**Hello world** and more")
}

func TestEmphasisHugsTheWords(t *testing.T) {
	expect(t, emphasize(" bold ", bold), " **bold** ")
	expect(t, emphasize("  ", bold), "  ")
	expect(t, emphasize("all", marks{true, true, true}), "~~***all***~~")
}

func TestALinkWrapsItsEmphasis(t *testing.T) {
	var line inline
	line.push("See ", marks{}, "")
	line.push("the site", bold, "https://example.com/a b")
	expect(t, line.render(), "See [**the site**](https://example.com/a%20b)")
	// A link nobody should follow keeps its text only.
	var unsafe inline
	unsafe.push("click", marks{}, "javascript:alert(1)")
	expect(t, unsafe.render(), "click")
}

func TestListItemsAreTightAndNeverSkipALevel(t *testing.T) {
	var b blocks
	b.push("Intro")
	b.pushItem(2, "-", "first")
	b.pushItem(1, "1.", "nested\nsecond line")
	b.pushItem(0, "-", "back out")
	b.pushItem(0, "1.", "another list")
	b.push("After")
	expect(t, b.String(), "Intro\n\n- first\n    1. nested\n       second line\n- back out\n\n1. another list\n\nAfter")
}

func TestTablesGetAHeaderPaddedRowsAndEscapedCells(t *testing.T) {
	expect(t, table([][]string{{"Name", "Note"}, {"a|b"}, {"c", "two\n\nlines"}}),
		"| Name | Note |\n| --- | --- |\n| a\\|b |  |\n| c | two<br>lines |")
	expect(t, table(nil), "")
}

func TestOnlyWhatWouldBeMarkdownIsEscaped(t *testing.T) {
	for text, want := range map[string]string{
		"a = b":         "a = b",
		"==mark==":      `\=\=mark\=\=`,
		"Tom &amp; co":  `Tom \&amp; co`,
		"Tom & co":      "Tom & co",
		"&#8217; &x":    `\&#8217; &x`,
		"[x] and y]":    `\[x] and y]`,
		"$5 *each* _a_": `\$5 \*each\* \_a\_`,
	} {
		expect(t, escapeText(text, false), want)
	}
	expect(t, escapeText("a]", true), `a\]`)
	for text, want := range map[string]string{
		"# x":         `\# x`,
		"  > x":       `  \> x`,
		"1. x":        `1\. x`,
		"12) x":       `12\) x`,
		"3.5 m":       "3.5 m",
		"1.":          `1\.`,
		"1234567890.": "1234567890.",
		"a\n- b":      "a\n\\- b",
	} {
		expect(t, escapeLineStarts(text), want)
	}
	for text, want := range map[string]string{
		"Notes on C #": `Notes on C \#`,
		"C#":           "C#",
		"##":           `\##`,
		"Plain":        "Plain",
	} {
		expect(t, escapeClosingHashes(text), want)
	}
}

func TestFollowableLinks(t *testing.T) {
	for href, ok := range map[string]bool{
		"https://a.test": true, "mailto:x@y.z": true, "file:///a": true, "notes/a.md": true,
		"#top": true, "javascript:alert(1)": false, "data:text/html,x": false, " ": false,
	} {
		if followable(href) != ok {
			t.Errorf("%q", href)
		}
	}
}

func TestAPartNestedPastAnyRealDocumentReadsAsEnded(t *testing.T) {
	x := newPull([]byte(strings.Repeat("<a>", 100_000)))
	opened := 0
	for n := x.next(); n.kind != nodeEnd; n = x.next() {
		opened++
	}
	if opened != maxDepth {
		t.Error(opened)
	}
}

func TestEscapedCharactersAreKept(t *testing.T) {
	x := newPull([]byte("<t>Tom &amp; Jerry &#8217;s</t>"))
	if n := x.next(); n.kind != nodeOpen {
		t.Fatal(n)
	}
	expect(t, x.text(), "Tom & Jerry ’s")
}

func TestAnEmptyElementIsOpenedAndClosed(t *testing.T) {
	x := newPull([]byte(`<p><x:br x:type="page"/>a</p>`))
	if n := x.next(); n.kind != nodeOpen || n.tag.name != "p" {
		t.Fatal(n)
	}
	br := x.next()
	if br.kind != nodeOpen || br.tag.name != "br" || br.tag.val("type") != "page" {
		t.Fatal(br)
	}
	if n := x.next(); n.kind != nodeClose {
		t.Fatal(n)
	}
	if n := x.next(); n.kind != nodeText || n.text != "a" {
		t.Fatal(n)
	}
}

func TestRelationshipTargetsResolveToZipEntries(t *testing.T) {
	expect(t, resolvePart("ppt/slides", "../notesSlides/notesSlide2.xml"), "ppt/notesSlides/notesSlide2.xml")
	expect(t, resolvePart("OEBPS", "text/ch1.xhtml"), "OEBPS/text/ch1.xhtml")
	expect(t, resolvePart("", "ch1.xhtml"), "ch1.xhtml")
	expect(t, resolvePart("word", "/word/media/a.png"), "word/media/a.png")
}
