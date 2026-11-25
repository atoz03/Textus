package ai

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// What a request carries of the document: the whole of it, or an outline
// and the section in focus when it is long.

const (
	// FullDocumentLimit is how long a document may be and be embedded in
	// full; past it, an outline and excerpts, and the model reads on
	// through the document tools. Counted in char units (CJK as 3) when
	// tools can read on, else in characters. A provider whose every model
	// reads 128k tokens or more takes RoomyDocumentLimit; any other (a
	// gateway, a local server) keeps this.
	FullDocumentLimit  = 40_000
	RoomyDocumentLimit = 120_000
	// excerptChars are how much of a long document's start and end are
	// shown.
	excerptChars = 2_000
	// maxOutlineHeadings are the most headings a long document's outline
	// lists.
	maxOutlineHeadings = 120
	// focusSectionChars are the most of a focus section embedded beside a
	// truncated document.
	focusSectionChars = 12_000
)

// CharUnits is a character's weight in the document and read limits: a
// CJK character is about a token where English takes about four letters,
// so it counts 3.
func CharUnits(c rune) int {
	switch {
	case c >= 0x1100 && c <= 0x11ff, c >= 0x2e80 && c <= 0x9fff, c >= 0xac00 && c <= 0xd7af,
		c >= 0xf900 && c <= 0xfaff, c >= 0xff00 && c <= 0xffef, c >= 0x20000 && c <= 0x3ffff:
		return 3
	}
	return 1
}

// Reading is how a request reads the document: with the document tools to
// read on (else whole, whatever its length), and how much it takes whole.
type Reading struct {
	Tools bool
	Whole int
}

// LeastReading is the room every model has, a local server's too.
func LeastReading(tools bool) Reading { return Reading{Tools: tools, Whole: FullDocumentLimit} }

// ShownWhole reports whether a request shows the whole document - else an
// outline and excerpts, which the document tools read on from.
func ShownWhole(document string, r Reading) bool {
	size := 0
	for _, c := range document {
		if r.Tools {
			size += CharUnits(c)
		} else {
			size++
		}
	}
	return size <= r.Whole
}

// headingLevel is the ATX heading level of a markdown line (`## Title` is
// 2), or 0.
func headingLevel(line string) int {
	trimmed := strings.TrimLeftFunc(line, unicode.IsSpace)
	hashes := len(trimmed) - len(strings.TrimLeft(trimmed, "#"))
	if hashes >= 1 && hashes <= 6 && len(trimmed) > hashes && trimmed[hashes] == ' ' {
		return hashes
	}
	return 0
}

// heading is a heading's line index (0-based) and level.
type heading struct{ line, level int }

// headingLines are the headings outside fenced code blocks - a `# `
// comment in a shell snippet isn't a section.
func headingLines(lines []string) []heading {
	inFence := false
	var out []heading
	for i, line := range lines {
		trimmed := strings.TrimLeftFunc(line, unicode.IsSpace)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			inFence = !inFence
			continue
		}
		if level := headingLevel(line); !inFence && level > 0 {
			out = append(out, heading{i, level})
		}
	}
	return out
}

// headingTitle is a heading line's words.
func headingTitle(line string) string {
	return strings.TrimSpace(strings.TrimLeft(strings.TrimLeftFunc(line, unicode.IsSpace), "#"))
}

// sectionRange is the line range [start, end) of the section under
// heading, to the next heading at its level or above. An exact title wins,
// else the first heading containing it, ignoring case.
func sectionRange(lines []string, title string) (int, int, bool) {
	wanted := strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(title), "#"))
	if wanted == "" {
		return 0, 0, false
	}
	headings := headingLines(lines)
	pos := -1
	for k, h := range headings {
		if headingTitle(lines[h.line]) == wanted {
			pos = k
			break
		}
	}
	if pos < 0 {
		lower := strings.ToLower(wanted)
		for k, h := range headings {
			if strings.Contains(strings.ToLower(headingTitle(lines[h.line])), lower) {
				pos = k
				break
			}
		}
	}
	if pos < 0 {
		return 0, 0, false
	}
	first, level := headings[pos].line, headings[pos].level
	last := len(lines)
	for _, h := range headings[pos+1:] {
		if h.level <= level {
			last = h.line
			break
		}
	}
	return first, last, true
}

// lineStarts are the byte offsets each line of text starts at.
func lineStarts(text string) []int {
	var starts []int
	for at := 0; at < len(text); {
		starts = append(starts, at)
		i := strings.IndexByte(text[at:], '\n')
		if i < 0 {
			break
		}
		at += i + 1
	}
	return starts
}

// headingOutline is the document's headings with their line and block id
// (`L12 [#k3f9] ## Methods`), the map read ranges and edits refer to.
func headingOutline(d *Document) []string {
	all := lines(d.Text)
	starts := lineStarts(d.Text)
	var out []string
	for k, h := range headingLines(all) {
		if k == maxOutlineHeadings {
			break
		}
		id := ""
		if h.line < len(starts) {
			if found, ok := IDAt(d.Blocks, starts[h.line]); ok {
				id = "[#" + found + "] "
			}
		}
		out = append(out, fmt.Sprintf("L%d %s%s", h.line+1, id, strings.TrimSpace(all[h.line])))
	}
	return out
}

// charEnd is the byte where the first n characters of s end.
func charEnd(s string, n int) int {
	for i := range s {
		if n == 0 {
			return i
		}
		n--
	}
	return len(s)
}

// charPrefix is s's first n characters (not bytes: a byte slice could
// land mid-character).
func charPrefix(s string, n int) string { return s[:charEnd(s, n)] }

// charSuffix is s's last n characters.
func charSuffix(s string, n int) string {
	total := utf8.RuneCountInString(s)
	if total <= n {
		return s
	}
	return s[charEnd(s, total-n):]
}

// focusSection is the section headed title (to the next heading at its
// level or above), marked with its blocks' ids, and the line it starts
// on.
func focusSection(d *Document, title string) (int, string, bool) {
	all := lines(d.Text)
	first, last, ok := sectionRange(all, title)
	if !ok {
		return 0, "", false
	}
	from := 0
	if starts := lineStarts(d.Text); first < len(starts) {
		from = starts[first]
	}
	text := strings.Join(all[first:last], "\n")
	to := min(from+charEnd(text, focusSectionChars), len(d.Text))
	return first + 1, Annotate(d, from, to), true
}

// DocumentSection is the `---document---` section, each block marked with
// its id: the whole document, or when it is long an outline, the section
// in focus and its start and end.
func DocumentSection(d *Document, r Reading, focusHeading string) string {
	if ShownWhole(d.Text, r) {
		return "---document---\n" + Annotate(d, 0, len(d.Text))
	}
	focus := ""
	if focusHeading != "" {
		if line, text, ok := focusSection(d, focusHeading); ok {
			focus = fmt.Sprintf("\n\n[the section the user is working in, from line %d]\n%s", line, text)
		}
	}
	outline := "(no headings)"
	if headings := headingOutline(d); len(headings) > 0 {
		outline = strings.Join(headings, "\n")
	}
	note := "This document is long, so only an outline and the start/end are shown below - some content in the middle isn't visible here."
	if r.Tools {
		note = "This document is long, so only an outline (with line numbers) and the start/end are shown below. Read any section in full with read_document (by heading or line range) before revising or quoting it - never guess at text you haven't seen, and never propose an edit in the same step as the read it depends on - and use search_document to find where something is mentioned. Read the sections a change needs side by side in one step (several read_document calls), then propose their edits together in the next; a change through the whole document goes a few sections per step, each step's edits sent with the next step's reads."
	}
	head := len(charPrefix(d.Text, excerptChars))
	tail := len(d.Text) - len(charSuffix(d.Text, excerptChars))
	return fmt.Sprintf("---document (truncated, %d chars, %d lines total)---\n%s\n\n[outline]\n%s%s\n\n[start]\n%s\n\n[end]\n%s",
		utf8.RuneCountInString(d.Text), len(lines(d.Text)), note, outline, focus,
		Annotate(d, 0, head), Annotate(d, tail, len(d.Text)))
}
