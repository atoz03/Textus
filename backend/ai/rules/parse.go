package rules

import (
	"bytes"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	east "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"

	"textus/backend/frontmatter"
)

// A document's Markdown parsed into blocks of prose, each with a map back
// to the source: code, math, front matter and HTML never reach a rule.
// Images and links are kept for the rules on alt and link text.

// blockText is one block of prose and where each of its chars came from.
type blockText struct {
	kind Block
	text []rune
	// at is char index in text -> char offset in the source.
	at []int
}

func (b *blockText) push(c rune, at int) {
	b.text = append(b.text, c)
	b.at = append(b.at, at)
}

type mdImage struct {
	start, end int
	alt        string
}

type mdLink struct {
	start, end int
	text       string
}

// parsedDoc is the parsed document.
type parsedDoc struct {
	blocks []*blockText
	images []mdImage
	links  []mdLink
	// headings are (level, block index) for each heading.
	headings [][2]int
}

// mathSpan is inline ($…$) or display ($$…$$) math.
type mathSpan struct{ ast.BaseInline }

var kindMath = ast.NewNodeKind("Math")

func (n *mathSpan) Kind() ast.NodeKind { return kindMath }

func (n *mathSpan) Dump(source []byte, level int) { ast.DumpHelper(n, source, level, nil, nil) }

// mathParser reads math the way the Rust app's parser (pulldown-cmark)
// did: $$…$$ holds anything, across lines; $…$ opens before a non-space
// and closes after one, not before a digit ($20 and $30 aren't math). An
// escaped dollar neither opens nor closes.
type mathParser struct{}

func (mathParser) Trigger() []byte { return []byte{'$'} }

func (mathParser) Parse(_ ast.Node, block text.Reader, _ parser.Context) ast.Node {
	line, _ := block.PeekLine()
	display := len(line) > 1 && line[1] == '$'
	if !display && (len(line) < 2 || util.IsSpace(line[1])) {
		return nil
	}
	savedLine, savedPos := block.Position()
	if display {
		block.Advance(2)
	} else {
		block.Advance(1)
	}
	for {
		line, _ := block.PeekLine()
		if line == nil {
			block.SetPosition(savedLine, savedPos)
			return nil
		}
		for i := 0; i < len(line); i++ {
			switch {
			case line[i] == '\\':
				i++
			case line[i] != '$':
			case display:
				if i+1 < len(line) && line[i+1] == '$' {
					block.Advance(i + 2)
					return &mathSpan{}
				}
			case i > 0 && !util.IsSpace(line[i-1]) && !(i+1 < len(line) && line[i+1] >= '0' && line[i+1] <= '9'):
				block.Advance(i + 1)
				return &mathSpan{}
			}
		}
		block.AdvanceLine()
	}
}

var markdownParser = sync.OnceValue(func() parser.Parser {
	return goldmark.New(
		goldmark.WithExtensions(extension.Table, extension.Strikethrough, extension.TaskList, extension.Footnote),
		goldmark.WithParserOptions(parser.WithInlineParsers(util.Prioritized(mathParser{}, 150))),
	).Parser()
})

// parseMarkdown parses markdown into its blocks of prose.
func parseMarkdown(markdown string) *parsedDoc {
	src := []byte(markdown)
	// Byte offset -> char offset.
	b2c := make([]int, len(src)+1)
	ci := 0
	for bi, ch := range markdown {
		for k := range utf8.RuneLen(ch) {
			if bi+k < len(src) {
				b2c[bi+k] = ci
			}
		}
		ci++
	}
	b2c[len(src)] = ci
	// Front matter: blanked, keeping every offset, so no block comes from it.
	if fm, ok := frontmatter.Split(markdown); ok {
		for i := range len(markdown) - len(fm.Body) {
			if src[i] != '\n' && src[i] != '\r' {
				src[i] = ' '
			}
		}
	}
	doc := markdownParser().Parse(text.NewReader(src))

	p := &parsedDoc{}
	var current *blockText
	quoteDepth, itemDepth := 0, 0
	var link *mdLink
	closeBlock := func() {
		b := current
		current = nil
		if b == nil || strings.TrimSpace(string(b.text)) == "" {
			return
		}
		if b.kind.isHeading() {
			p.headings = append(p.headings, [2]int{b.kind.level, len(p.blocks)})
		}
		p.blocks = append(p.blocks, b)
	}
	// char is the char offset of byte offset at, clamped to the source.
	char := func(at int) int { return b2c[max(0, min(at, len(src)))] }
	open := func(kind Block) { current = &blockText{kind: kind} }
	placeholder := func(at int) {
		if link != nil {
			link.text += string(Placeholder)
		}
		if current != nil {
			current.push(Placeholder, char(at))
		}
	}
	// pushText adds a text segment: an escaped char maps to its backslash, an
	// entity's chars to its &, everything else char by char.
	pushText := func(seg text.Segment) {
		if seg.IsEmpty() {
			return
		}
		if current == nil {
			open(blockPara)
		}
		var linkText strings.Builder
		emit := func(r rune, at int) {
			current.push(r, b2c[at])
			if link != nil {
				linkText.WriteRune(r)
			}
		}
		for i := seg.Start; i < seg.Stop; {
			if src[i] == '\\' && i+1 < seg.Stop && util.IsPunct(src[i+1]) {
				emit(rune(src[i+1]), i)
				i += 2
				continue
			}
			if src[i] == '&' {
				if n, value := entityAt(src[i:seg.Stop]); n > 0 {
					for _, r := range value {
						emit(r, i)
					}
					i += n
					continue
				}
			}
			r, size := utf8.DecodeRune(src[i:seg.Stop])
			emit(r, i)
			i += size
		}
		if link != nil {
			link.text += linkText.String()
		}
	}

	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		switch n := n.(type) {
		case *ast.FencedCodeBlock, *ast.CodeBlock, *ast.HTMLBlock:
			return ast.WalkSkipChildren, nil
		case *ast.Blockquote:
			if entering {
				quoteDepth++
			} else {
				quoteDepth--
			}
		case *ast.Paragraph, *ast.TextBlock:
			if !entering {
				closeBlock()
			} else if current == nil {
				switch {
				case itemDepth > 0:
					open(blockItem)
				case quoteDepth > 0:
					open(blockQuot)
				default:
					open(blockPara)
				}
			}
		case *ast.Heading:
			closeBlock()
			if entering {
				open(headingBlock(n.Level))
			}
		case *ast.ListItem:
			closeBlock()
			if entering {
				itemDepth++
				open(blockItem)
			} else {
				itemDepth--
			}
		case *east.TableCell:
			closeBlock()
			if entering {
				open(blockCell)
			}
		case *ast.Image:
			if entering {
				p.images = append(p.images, mdImage{char(n.Pos()), char(inlineEnd(n, src)), altText(n, src)})
				if current != nil {
					current.push(Placeholder, char(n.Pos()))
				}
			}
			return ast.WalkSkipChildren, nil
		case *ast.Link:
			if entering {
				link = &mdLink{start: char(n.Pos()), end: char(inlineEnd(n, src))}
			} else if link != nil {
				p.links = append(p.links, *link)
				link = nil
			}
		case *ast.AutoLink:
			if entering {
				// The address is the link's text, as written between < and >.
				label := n.Label(src)
				at := n.Pos() + 1
				link = &mdLink{start: char(n.Pos()), end: char(at + len(label) + 1)}
				pushText(text.NewSegment(at, at+len(label)))
				p.links = append(p.links, *link)
				link = nil
			}
			return ast.WalkSkipChildren, nil
		case *ast.CodeSpan, *mathSpan, *east.FootnoteLink:
			if entering {
				placeholder(n.Pos())
			}
			return ast.WalkSkipChildren, nil
		case *ast.RawHTML:
			if entering && n.Segments.Len() > 0 {
				placeholder(n.Segments.At(0).Start)
			}
			return ast.WalkSkipChildren, nil
		case *east.FootnoteBacklink, *east.TaskCheckBox:
			return ast.WalkSkipChildren, nil
		case *ast.Text:
			if entering {
				pushText(n.Segment)
				if (n.SoftLineBreak() || n.HardLineBreak()) && current != nil {
					current.push('\n', char(n.Segment.Stop))
				}
			}
		case *ast.String:
			if entering && current != nil {
				at := 0
				if k := len(current.at); k > 0 {
					at = current.at[k-1]
				}
				for _, r := range string(n.Value) {
					current.push(r, at)
				}
			}
		}
		return ast.WalkContinue, nil
	})
	closeBlock()
	return p
}

// entityAt is the length and value of the HTML entity or numeric character
// reference b opens with; 0 for none.
func entityAt(b []byte) (int, string) {
	end := -1
	for i := 1; i < len(b) && i < 40; i++ {
		if b[i] == ';' {
			end = i
			break
		}
	}
	if end < 2 {
		return 0, ""
	}
	name := string(b[1:end])
	if name[0] == '#' {
		digits, base := name[1:], 10
		if len(digits) > 0 && (digits[0] == 'x' || digits[0] == 'X') {
			digits, base = digits[1:], 16
		}
		if len(digits) == 0 || base == 10 && len(digits) > 7 || base == 16 && len(digits) > 6 {
			return 0, ""
		}
		v := 0
		for _, d := range digits {
			switch {
			case d >= '0' && d <= '9':
				v = v*base + int(d-'0')
			case base == 16 && d >= 'a' && d <= 'f':
				v = v*base + int(d-'a') + 10
			case base == 16 && d >= 'A' && d <= 'F':
				v = v*base + int(d-'A') + 10
			default:
				return 0, ""
			}
		}
		r := rune(v)
		if v == 0 || !utf8.ValidRune(r) {
			r = utf8.RuneError
		}
		return end + 1, string(r)
	}
	if e, ok := util.LookUpHTML5EntityByName(name); ok {
		return end + 1, string(e.Characters)
	}
	return 0, ""
}

// altText is an image's alt text: its text, without code or markup.
func altText(img ast.Node, src []byte) string {
	var b strings.Builder
	_ = ast.Walk(img, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		switch n := n.(type) {
		case *ast.CodeSpan, *mathSpan, *ast.RawHTML:
			return ast.WalkSkipChildren, nil
		case *ast.Text:
			if entering {
				seg := n.Segment
				for i := seg.Start; i < seg.Stop; {
					if src[i] == '\\' && i+1 < seg.Stop && util.IsPunct(src[i+1]) {
						b.WriteByte(src[i+1])
						i += 2
						continue
					}
					if src[i] == '&' {
						if k, value := entityAt(src[i:seg.Stop]); k > 0 {
							b.WriteString(value)
							i += k
							continue
						}
					}
					r, size := utf8.DecodeRune(src[i:seg.Stop])
					b.WriteRune(r)
					i += size
				}
			}
		}
		return ast.WalkContinue, nil
	})
	return b.String()
}

// inlineStart is where an inline node starts in the source; -1 if unknown.
func inlineStart(n ast.Node) int {
	switch n := n.(type) {
	case *ast.Text:
		return n.Segment.Start
	case *ast.RawHTML:
		if n.Segments.Len() > 0 {
			return n.Segments.At(0).Start
		}
	}
	return n.Pos()
}

// inlineEnd is where a link or image ends in the source: where the inline
// after it starts, else after its label and its (destination) or
// [reference].
func inlineEnd(n ast.Node, src []byte) int {
	if next := n.NextSibling(); next != nil {
		if at := inlineStart(next); at > n.Pos() {
			return at
		}
	}
	i := n.Pos()
	if i < len(src) && src[i] == '!' {
		i++
	}
	if j := closingBracket(src, i); j > 0 {
		i = j
		if i < len(src) && (src[i] == '(' || src[i] == '[') {
			if j := closingBracket(src, i); j > 0 {
				i = j
			}
		}
	}
	return min(i, len(src))
}

// closingBracket is the offset after the bracket that closes the one at i
// ([ or (), skipping escaped chars and <…> destinations; -1 if none.
func closingBracket(src []byte, i int) int {
	if i >= len(src) {
		return -1
	}
	open := src[i]
	close := byte(']')
	if open == '(' {
		close = ')'
	}
	depth := 0
	for ; i < len(src); i++ {
		switch c := src[i]; {
		case c == '\\':
			i++
		case c == '<' && open == '(':
			if j := bytes.IndexByte(src[i:], '>'); j > 0 {
				i += j
			}
		case c == open:
			depth++
		case c == close:
			depth--
			if depth == 0 {
				return i + 1
			}
		}
	}
	return -1
}
