package extract

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// The Markdown the extractors write, shared so tables and runs from every
// format read the same.

// marks are how a run of text is emphasized.
type marks struct{ bold, italic, strike bool }

// inline is one paragraph's text, assembled run by run. Word splits
// sentences into many runs, so runs with the same marks and link merge
// first, or bold would come out as **He****llo**.
type inline struct{ spans []span }

type span struct {
	marks marks
	link  string // "": none
	text  string
}

func (l *inline) push(text string, m marks, link string) {
	if text == "" {
		return
	}
	if n := len(l.spans); n > 0 {
		last := &l.spans[n-1]
		// Whitespace takes on the marks before it: a bold word, a plain
		// space and a bold word are one bold phrase, not two.
		if (last.marks == m || strings.TrimSpace(text) == "") && last.link == link {
			last.text += text
			return
		}
	}
	l.spans = append(l.spans, span{m, link, text})
}

// pushMarkdown adds text that is Markdown already: a picture, a note.
func (l *inline) pushMarkdown(markdown string) { l.push(markdown, marks{}, "") }

func (l *inline) render() string { return l.renderWith(true) }

// renderPlain is for a heading, whose emphasis is the heading's: Word
// styles a heading bold, and # **Chapter One** says nothing # Chapter One
// doesn't.
func (l *inline) renderPlain() string {
	return strings.ReplaceAll(l.renderWith(false), "\n", " ")
}

func (l *inline) renderWith(emphasis bool) string {
	var out strings.Builder
	for i := 0; i < len(l.spans); {
		link := l.spans[i].link
		var inner strings.Builder
		for ; i < len(l.spans) && l.spans[i].link == link; i++ {
			m := l.spans[i].marks
			if !emphasis {
				m = marks{}
			}
			inner.WriteString(emphasize(l.spans[i].text, m))
		}
		if link != "" {
			out.WriteString(linked(inner.String(), link))
		} else {
			out.WriteString(inner.String())
		}
	}
	return strings.TrimSpace(out.String())
}

// chomp splits text into leading whitespace, the rest, and trailing
// whitespace: emphasis must hug the words (**bold **next doesn't parse).
func chomp(text string) (lead, core, trail string) {
	core = strings.TrimSpace(text)
	if core == "" {
		return text, "", ""
	}
	start := len(text) - len(strings.TrimLeftFunc(text, unicode.IsSpace))
	end := start + len(core)
	return text[:start], core, text[end:]
}

func emphasize(text string, m marks) string {
	lead, core, trail := chomp(text)
	if core == "" {
		return text
	}
	open := ""
	if m.strike {
		open += "~~"
	}
	if m.bold {
		open += "**"
	}
	if m.italic {
		open += "*"
	}
	return lead + open + core + reverse(open) + trail
}

func reverse(s string) string {
	b := []byte(s)
	for i, j := 0, len(b)-1; i < j; i, j = i+1, j-1 {
		b[i], b[j] = b[j], b[i]
	}
	return string(b)
}

// linked is [text](url), or just the text when there is nothing to click,
// or nothing safe to (javascript:, data:).
func linked(text, url string) string {
	lead, core, trail := chomp(text)
	if core == "" || !followable(url) {
		return text
	}
	return lead + "[" + core + "](" + linkTarget(url) + ")" + trail
}

// followable is a link a reader can follow: relative, or http(s), file or
// mailto.
func followable(href string) bool {
	href = strings.TrimSpace(href)
	if href == "" {
		return false
	}
	if i := strings.IndexAny(href, ":/?#"); i >= 0 && href[i] == ':' {
		switch strings.ToLower(href[:i]) {
		case "http", "https", "file", "mailto":
			return true
		}
		return false
	}
	return true
}

// linkTarget is a URL as a Markdown link target: a space or a parenthesis
// would end it early, so they are percent-encoded.
func linkTarget(url string) string {
	return strings.NewReplacer(" ", "%20", "(", "%28", ")", "%29").Replace(strings.TrimSpace(url))
}

// blocks are joined the way Markdown reads them: a blank line between
// paragraphs, none between the items of one list.
type blocks struct {
	out strings.Builder
	// lastItem is the level and marker of the list item last written;
	// hasItem is false after any other block.
	lastLevel  int
	lastMarker string
	hasItem    bool
}

func (b *blocks) push(text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	if b.out.Len() > 0 {
		b.out.WriteString("\n\n")
	}
	b.out.WriteString(text)
	b.hasItem = false
}

// pushItem adds a list item at nesting level (0: the top), marked - or a
// number (1.). Nested items indent four spaces a level, never more than
// one deeper than the item before (deeper is a code block); a switch of
// marker type starts a new list.
func (b *blocks) pushItem(level int, marker, text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	sameList := false
	if b.hasItem {
		level = min(level, b.lastLevel+1)
		ordered := func(m string) bool { return strings.HasSuffix(m, ".") }
		sameList = level != b.lastLevel || ordered(b.lastMarker) == ordered(marker)
	} else {
		level = 0
	}
	if b.out.Len() > 0 {
		if sameList {
			b.out.WriteString("\n")
		} else {
			b.out.WriteString("\n\n")
		}
	}
	indent := strings.Repeat("    ", level)
	hang := strings.Repeat(" ", len(marker)+1)
	for i, line := range lines(text) {
		if i == 0 {
			b.out.WriteString(indent + marker + " " + line)
		} else {
			b.out.WriteString("\n" + indent + hang + strings.TrimLeftFunc(line, unicode.IsSpace))
		}
	}
	b.lastLevel, b.lastMarker, b.hasItem = level, marker, true
}

func (b *blocks) empty() bool { return b.out.Len() == 0 }

func (b *blocks) String() string { return b.out.String() }

// lines splits text at line ends as a reader does: a \r before one goes
// with it, and a last line end opens no empty line.
func lines(text string) []string {
	if text == "" {
		return nil
	}
	out := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	for i, line := range out {
		out[i] = strings.TrimSuffix(line, "\r")
	}
	return out
}

// escapeText backslash-escapes every character that would mean Markdown
// in running text, so a Word document's *, $5 or [draft] stay text. =, &
// and ] only where they'd be markup, as the editor's writer does.
func escapeText(text string, inLink bool) string {
	chars := []rune(text)
	var out strings.Builder
	for i, c := range chars {
		escape := false
		switch c {
		case '\\', '*', '_', '`', '[', '<', '~', '$':
			escape = true
		case ']':
			escape = inLink
		case '=':
			escape = i > 0 && chars[i-1] == '=' || i+1 < len(chars) && chars[i+1] == '='
		case '&':
			escape = startsEntity(chars[i+1:])
		}
		if escape {
			out.WriteByte('\\')
		}
		out.WriteRune(c)
	}
	return out.String()
}

// startsEntity is whether what follows a & reads as a character reference
// (amp;, #8217;, #x2019;).
func startsEntity(rest []rune) bool {
	n := 0
	for n < len(rest) && (rest[n] < utf8.RuneSelf && (rest[n] >= 'a' && rest[n] <= 'z' || rest[n] >= 'A' && rest[n] <= 'Z' || rest[n] >= '0' && rest[n] <= '9' || rest[n] == '#')) {
		n++
	}
	return n > 0 && n < len(rest) && rest[n] == ';'
}

// escapeLineStarts escapes what would start a block at the beginning of
// each line of escaped text (#, >, list markers, = and - lines, |), since
// these can interrupt a paragraph.
func escapeLineStarts(text string) string {
	var out strings.Builder
	for i, line := range strings.Split(text, "\n") {
		if i > 0 {
			out.WriteByte('\n')
		}
		body := strings.TrimLeftFunc(line, unicode.IsSpace)
		out.WriteString(line[:len(line)-len(body)])
		if body != "" && strings.ContainsRune("#>-+=|", rune(body[0])) {
			out.WriteByte('\\')
		} else {
			// "1." and "1)" followed by a space or nothing: an ordered item.
			digits := len(body) - len(strings.TrimLeft(body, "0123456789"))
			after := body[digits:]
			if digits >= 1 && digits <= 9 && after != "" && (after[0] == '.' || after[0] == ')') {
				next, _ := utf8.DecodeRuneInString(after[1:])
				if after[1:] == "" || unicode.IsSpace(next) {
					out.WriteString(body[:digits] + `\` + after)
					continue
				}
			}
		}
		out.WriteString(body)
	}
	return out.String()
}

// escapeClosingHashes keeps a heading's trailing # run as text, or it
// would be read as closing hashes and dropped ("Notes #" to "Notes").
func escapeClosingHashes(text string) string {
	body := strings.TrimRight(text, "#")
	if len(body) == len(text) || !(body == "" || strings.HasSuffix(body, " ") || strings.HasSuffix(body, "\t")) {
		return text
	}
	return body + `\` + text[len(body):]
}

// table is a pipe table whose first row is the header. A line break in a
// cell becomes <br>, | is escaped, and rows are padded to the widest.
func table(rows [][]string) string {
	width := 0
	for _, row := range rows {
		width = max(width, len(row))
	}
	if width == 0 {
		return ""
	}
	var out strings.Builder
	padded := make([]string, width)
	for i, row := range rows {
		clear(padded)
		copy(padded, row)
		tableRow(&out, padded, i == 0)
	}
	return strings.TrimRightFunc(out.String(), unicode.IsSpace)
}

// tableRow writes one row of a pipe table, and after the header the line
// that makes it one.
func tableRow(out *strings.Builder, row []string, header bool) {
	if len(row) == 0 {
		return
	}
	cells := make([]string, len(row))
	for i, cell := range row {
		cells[i] = tableCell(cell)
	}
	out.WriteString("| " + strings.Join(cells, " | ") + " |\n")
	if header {
		out.WriteString("|" + strings.Repeat(" --- |", len(row)) + "\n")
	}
}

func tableCell(cell string) string {
	var kept []string
	for _, line := range lines(cell) {
		if line = strings.TrimSpace(line); line != "" {
			kept = append(kept, line)
		}
	}
	return strings.ReplaceAll(strings.Join(kept, "<br>"), "|", `\|`)
}
