package extract

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
	"unicode"

	"golang.org/x/net/html"
)

// HTML to Markdown, for .html attachments and documents and web pages.
// Follows MarkItDown's (MIT) rules: scripts dropped, only followable
// links, `data:` images cut to their type. x/net/html parses tag soup as a
// browser would.

// HTMLMarkdown is the Markdown of an HTML document or fragment. It never
// fails: HTML has no malformed input, only input a browser repairs.
func HTMLMarkdown(source string) string {
	doc, err := html.Parse(strings.NewReader(source))
	if err != nil {
		return ""
	}
	w := &htmlWriter{}
	w.children(doc)
	return w.finish()
}

type htmlWriter struct {
	out strings.Builder
	// line is the paragraph being written, whitespace already collapsed.
	line []byte
	// flushes is bumped whenever line is flushed, so an inline element
	// split by a block inside it (`<a><div>…</div></a>`) is left unwrapped.
	flushes int
	// inItem: a nested list follows the item's text on the next line,
	// keeping the list tight.
	inItem bool
}

func (w *htmlWriter) children(n *html.Node) {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		w.node(c)
	}
}

func attr(n *html.Node, name string) (string, bool) {
	for _, a := range n.Attr {
		if strings.EqualFold(a.Key, name) {
			return a.Val, true
		}
	}
	return "", false
}

func (w *htmlWriter) node(n *html.Node) {
	switch n.Type {
	case html.TextNode:
		w.text(n.Data)
		return
	case html.ElementNode, html.DocumentNode:
	default:
		return
	}
	name := strings.ToLower(n.Data)
	switch name {
	case "head", "script", "style", "noscript", "template", "svg", "math", "canvas",
		"iframe", "object", "embed", "select", "textarea", "button":
	case "br":
		w.line = append(bytes.TrimRight(w.line, " "), '\n')
	case "hr":
		w.block("---", false)
	case "h1", "h2", "h3", "h4", "h5", "h6":
		level := int(name[1] - '0')
		w.block(strings.Repeat("#", level)+" "+inlineOf(n), false)
	case "ul", "ol":
		w.block(htmlList(n, name == "ol"), w.inItem)
	case "blockquote":
		var quoted []string
		for _, line := range lines(render(n, false)) {
			if line == "" {
				quoted = append(quoted, ">")
			} else {
				quoted = append(quoted, "> "+line)
			}
		}
		w.block(strings.Join(quoted, "\n"), false)
	case "pre":
		w.block(codeBlock(n), false)
	case "table":
		w.block(htmlTable(n), false)
	case "img":
		w.line = append(w.line, htmlImage(n)...)
	case "input":
		if t, _ := attr(n, "type"); strings.EqualFold(t, "checkbox") {
			if _, checked := attr(n, "checked"); checked {
				w.text("[x] ")
			} else {
				w.text("[ ] ")
			}
		}
	case "a":
		href, ok := attr(n, "href")
		follow := ok && followable(href)
		w.wrapped(n, func(inner string) string {
			if follow {
				return linked(inner, href)
			}
			return inner
		})
	case "strong", "b":
		w.wrapped(n, func(inner string) string { return emphasize(inner, marks{bold: true}) })
	case "em", "i", "cite", "dfn":
		w.wrapped(n, func(inner string) string { return emphasize(inner, marks{italic: true}) })
	case "del", "s", "strike":
		w.wrapped(n, func(inner string) string { return emphasize(inner, marks{strike: true}) })
	case "code", "kbd", "samp", "tt":
		if code := strings.TrimSpace(collapse(textOf(n))); code != "" {
			w.line = append(w.line, inlineCode(code)...)
		}
	case "p", "div", "section", "article", "main", "header", "footer", "aside", "nav", "figure",
		"figcaption", "address", "details", "summary", "center", "form", "fieldset", "dl", "dt",
		"dd", "li", "body", "html", "caption":
		w.flush()
		w.children(n)
		w.flush()
	default:
		w.children(n)
	}
}

// text is text as a browser lays it out: whitespace runs are one space,
// none at a line start. ASCII only: a no-break space is the author's.
func (w *htmlWriter) text(text string) {
	for i := 0; i < len(text); i++ {
		c := text[i]
		if !isASCIISpace(c) {
			w.line = append(w.line, c)
		} else if n := len(w.line); n > 0 && w.line[n-1] != ' ' && w.line[n-1] != '\n' {
			w.line = append(w.line, ' ')
		}
	}
}

func isASCIISpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f'
}

// collapse is text with each run of ASCII whitespace made one space.
func collapse(text string) string {
	return strings.Join(strings.FieldsFunc(text, func(r rune) bool { return r < 0x80 && isASCIISpace(byte(r)) }), " ")
}

// wrapped writes the children of n inline, then wraps them as a whole.
func (w *htmlWriter) wrapped(n *html.Node, wrap func(inner string) string) {
	flushes, start := w.flushes, len(w.line)
	w.children(n)
	if w.flushes != flushes {
		return
	}
	inner := string(w.line[start:])
	w.line = append(w.line[:start], wrap(inner)...)
}

func (w *htmlWriter) flush() {
	var trimmed []string
	for _, l := range lines(string(w.line)) {
		trimmed = append(trimmed, strings.TrimSpace(l))
	}
	w.line = w.line[:0]
	w.flushes++
	w.block(strings.TrimSpace(strings.Join(trimmed, "\n")), false)
}

// block writes a finished block; tight joins it to the one before with a
// single newline (a nested list under its item's text).
func (w *htmlWriter) block(markdown string, tight bool) {
	if len(w.line) > 0 {
		w.flush()
	}
	if strings.TrimSpace(markdown) == "" {
		return
	}
	if w.out.Len() > 0 {
		if tight {
			w.out.WriteString("\n")
		} else {
			w.out.WriteString("\n\n")
		}
	}
	w.out.WriteString(strings.TrimRightFunc(markdown, unicode.IsSpace))
}

func (w *htmlWriter) finish() string {
	w.flush()
	return w.out.String()
}

// render is n's children as Markdown of their own: a list item's, a
// quote's or a table cell's content.
func render(n *html.Node, inItem bool) string {
	w := &htmlWriter{inItem: inItem}
	w.children(n)
	return w.finish()
}

// inlineOf is the inline Markdown of n's children, as one line.
func inlineOf(n *html.Node) string {
	var out []string
	for _, l := range lines(render(n, false)) {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return strings.Join(out, " ")
}

// textOf is the text of n and everything in it.
func textOf(n *html.Node) string {
	if n.Type == html.TextNode {
		return n.Data
	}
	var b strings.Builder
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		b.WriteString(textOf(c))
	}
	return b.String()
}

func elementChildren(n *html.Node) []*html.Node {
	var out []*html.Node
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode {
			out = append(out, c)
		}
	}
	return out
}

func htmlList(n *html.Node, ordered bool) string {
	number := int64(1)
	if s, ok := attr(n, "start"); ok {
		if v, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64); err == nil {
			number = v
		}
	}
	var items []string
	for _, child := range elementChildren(n) {
		switch name := strings.ToLower(child.Data); name {
		case "li":
			content := render(child, true)
			if content == "" {
				continue
			}
			marker := "-"
			if ordered {
				marker = fmt.Sprintf("%d.", number)
				number++
			}
			items = append(items, hang(marker, content))
		case "ul", "ol":
			// `<ul><li>a</li><ul>…</ul></ul>`: invalid, and common - the
			// list belongs under the item before it.
			nested := htmlList(child, name == "ol")
			var indented []string
			for _, l := range lines(nested) {
				indented = append(indented, "    "+l)
			}
			if nested != "" {
				items = append(items, strings.Join(indented, "\n"))
			}
		}
	}
	return strings.Join(items, "\n")
}

// hang is a list item: the marker before its first line, the rest
// indented under the text so CommonMark keeps them in the item.
func hang(marker, content string) string {
	indent := strings.Repeat(" ", len(marker)+1)
	var b strings.Builder
	for i, line := range lines(content) {
		switch {
		case i == 0:
			b.WriteString(marker + " " + line)
		case line == "":
			b.WriteByte('\n')
		default:
			b.WriteString("\n" + indent + line)
		}
	}
	return b.String()
}

func codeBlock(n *html.Node) string {
	code := strings.TrimRight(textOf(n), "\n\r")
	classes, _ := attr(n, "class")
	if kids := elementChildren(n); len(kids) > 0 {
		inner, _ := attr(kids[0], "class")
		classes += " " + inner
	}
	language := ""
	for _, c := range strings.Fields(classes) {
		if l, ok := strings.CutPrefix(c, "language-"); ok {
			language = l
			break
		}
		if l, ok := strings.CutPrefix(c, "lang-"); ok {
			language = l
			break
		}
	}
	// A fence longer than any backtick run inside, so the code can't end it.
	fence := strings.Repeat("`", max(longestBackticks(code), 2)+1)
	return fence + language + "\n" + code + "\n" + fence
}

// longestBackticks is the longest run of backticks in code: a fence must
// be longer.
func longestBackticks(code string) int {
	longest := 0
	for _, run := range strings.FieldsFunc(code, func(r rune) bool { return r != '`' }) {
		longest = max(longest, len(run))
	}
	return longest
}

func inlineCode(code string) string {
	longest := longestBackticks(code)
	if longest == 0 {
		return "`" + code + "`"
	}
	fence := strings.Repeat("`", longest+1)
	return fence + " " + code + " " + fence
}

func htmlTable(n *html.Node) string {
	var rows [][]string
	caption := ""
	collectRows(n, &rows, &caption)
	t := table(rows)
	if caption == "" {
		return t
	}
	return caption + "\n\n" + t
}

// collectRows gathers a table's rows, through its row groups but not into
// a table nested in a cell (that one is the cell's content).
func collectRows(n *html.Node, rows *[][]string, caption *string) {
	for _, child := range elementChildren(n) {
		switch strings.ToLower(child.Data) {
		case "thead", "tbody", "tfoot":
			collectRows(child, rows, caption)
		case "caption":
			*caption = render(child, false)
		case "tr":
			var row []string
			for _, cell := range elementChildren(child) {
				if name := strings.ToLower(cell.Data); name != "td" && name != "th" {
					continue
				}
				row = append(row, render(cell, false))
				span := 1
				if s, ok := attr(cell, "colspan"); ok {
					if v, err := strconv.Atoi(strings.TrimSpace(s)); err == nil {
						span = v
					}
				}
				for range min(max(span, 1), 64) - 1 {
					row = append(row, "")
				}
			}
			*rows = append(*rows, row)
		}
	}
}

func htmlImage(n *html.Node) string {
	get := func(name string) string { v, _ := attr(n, name); return v }
	alt := strings.NewReplacer("[", "", "]", "").Replace(strings.Join(strings.Fields(get("alt")), " "))
	src := get("src")
	if lazy := get("data-src"); lazy != "" && (src == "" || isDataURI(src)) {
		src = lazy
	}
	if isDataURI(src) {
		// `data:image/png;base64,iVBOR…` -> `data:image/png;base64...`
		head, _, _ := strings.Cut(src, ",")
		src = head + "..."
	}
	if alt == "" && src == "" {
		return ""
	}
	return "![" + alt + "](" + linkTarget(src) + ")"
}

func isDataURI(src string) bool { return len(src) >= 5 && strings.EqualFold(src[:5], "data:") }
