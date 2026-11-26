package extract

import (
	"cmp"
	"math"
	"slices"
	"strconv"
	"strings"
)

// One slide's (or notes page's) shapes: text boxes, tables and pictures,
// read in place order into Markdown.

// point is where a shape's top-left corner is, in EMU: rows first, as a
// slide is read.
type point struct{ y, x int64 }

// unplaced is a shape whose place it inherits from the layout: it sorts
// first.
var unplaced = point{math.MinInt64, math.MinInt64}

type shape struct {
	at       point
	title    bool
	markdown string
}

func sortShapes(shapes []shape) {
	slices.SortStableFunc(shapes, func(a, b shape) int {
		return cmp.Or(cmp.Compare(a.at.y, b.at.y), cmp.Compare(a.at.x, b.at.x))
	})
}

// slideReader reads one slide; bullets is whether body placeholders are
// bulleted, as they are on a slide and are not on a notes page.
type slideReader struct {
	rels    rels
	bullets bool
}

type bulletKind uint8

// How a paragraph is marked as a list item.
const (
	bulletInherited bulletKind = iota
	bulletNone
	bulletChar
	bulletNumber
)

type bullet struct {
	kind  bulletKind
	start int // a numbered list's first number
}

type slideParagraph struct {
	level  int
	bullet bullet
	line   inline
}

// maxSlideLevel is the deepest outline level PowerPoint offers.
const maxSlideLevel = 8

// readSlide is a slide's (or notes page's) title, whether it has one, and
// the Markdown of everything else.
func readSlide(data []byte, r rels, bullets bool) (string, bool, string) {
	reader := slideReader{r, bullets}
	x := newPull(data)
	var shapes []shape
	for n := x.next(); n.kind != nodeEnd; n = x.next() {
		if n.kind == nodeOpen && n.tag.name == "spTree" {
			shapes, _ = reader.tree(x)
		}
	}
	sortShapes(shapes)
	title, titled := "", false
	if i := slices.IndexFunc(shapes, func(s shape) bool { return s.title }); i >= 0 {
		title, titled = shapes[i].markdown, true
		shapes = slices.Delete(shapes, i, i+1)
	}
	return title, titled, joinShapes(shapes)
}

func joinShapes(shapes []shape) string {
	var b blocks
	for _, s := range shapes {
		b.push(s.markdown)
	}
	return b.String()
}

// tree is the shapes of a shape tree or group, through its close, and the
// group's own position.
func (r slideReader) tree(x *pull) ([]shape, point) {
	var shapes []shape
	at := unplaced
	add := func(s shape, ok bool) {
		if ok && s.markdown != "" {
			shapes = append(shapes, s)
		}
	}
	depth := 0
	for {
		n := x.next()
		switch n.kind {
		case nodeOpen:
			switch n.tag.name {
			case "sp":
				add(r.textShape(x))
			case "graphicFrame":
				add(r.frame(x))
			case "pic":
				add(r.picture(x))
			case "grpSp":
				members, groupAt := r.tree(x)
				sortShapes(members)
				add(shape{at: groupAt, markdown: joinShapes(members)}, true)
			case "grpSpPr":
				at = offset(x)
			case "AlternateContent", "Choice":
				depth++
			default:
				x.skip()
			}
		case nodeClose:
			if depth == 0 {
				return shapes, at
			}
			depth--
		case nodeEnd:
			return shapes, at
		}
	}
}

func (r slideReader) textShape(x *pull) (shape, bool) {
	placeholder, isPlaceholder := "", false
	at := unplaced
	var paragraphs []slideParagraph
	x.walk(func(t tag) bool {
		switch {
		case t.name == "ph":
			// A placeholder without a type is the layout's body.
			placeholder, isPlaceholder = t.attr("type")
			if !isPlaceholder {
				placeholder, isPlaceholder = "body", true
			}
		case t.name == "off" && at == unplaced:
			at = position(t)
		case t.name == "txBody":
			paragraphs = r.textBody(x)
			return true
		default:
			return false
		}
		x.skip()
		return true
	})
	title := false
	switch placeholder {
	case "title", "ctrTitle":
		title = true
	// Slide number, date, footer: the template's furniture, the same on
	// every slide. The slide image on a notes page is no text.
	case "sldNum", "dt", "ftr", "hdr", "sldImg":
		return shape{}, false
	}
	if title {
		var words []string
		for _, p := range paragraphs {
			words = append(words, strings.Fields(p.line.renderPlain())...)
		}
		return shape{at: at, title: true, markdown: strings.Join(words, " ")}, true
	}
	bulleted := r.bullets && isPlaceholder && placeholder != "subTitle"
	return shape{at: at, markdown: bodyMarkdown(paragraphs, bulleted)}, true
}

// textBody is the paragraphs of a text body, through its close.
func (r slideReader) textBody(x *pull) []slideParagraph {
	var out []slideParagraph
	for n := x.next(); n.kind == nodeOpen || n.kind == nodeText; n = x.next() {
		switch {
		case n.kind == nodeText:
		case n.tag.name == "p":
			out = append(out, r.paragraph(x))
		default:
			x.skip()
		}
	}
	return out
}

func (r slideReader) paragraph(x *pull) slideParagraph {
	var p slideParagraph
	for n := x.next(); n.kind == nodeOpen || n.kind == nodeText; n = x.next() {
		if n.kind == nodeText {
			continue
		}
		switch n.tag.name {
		case "pPr":
			level, _ := count(n.tag, "lvl")
			p.level = min(level, maxSlideLevel)
			p.bullet = bulletOf(x)
		// A field (slide number, date) is a run.
		case "r", "fld":
			r.run(x, &p.line)
		case "br":
			x.skip()
			p.line.push("\n", marks{}, "")
		default:
			x.skip()
		}
	}
	return p
}

func (r slideReader) run(x *pull, line *inline) {
	var m marks
	link := ""
	for n := x.next(); n.kind == nodeOpen || n.kind == nodeText; n = x.next() {
		if n.kind == nodeText {
			continue
		}
		switch t := n.tag; t.name {
		case "rPr":
			strike, struck := t.attr("strike")
			m = marks{bold: on(t.val("b")), italic: on(t.val("i")), strike: struck && strike != "noStrike"}
			link = r.runLink(x)
		case "t":
			line.push(x.text(), m, link)
		default:
			x.skip()
		}
	}
}

// runLink is the URL of an <a:hlinkClick> inside the <a:rPr> just opened.
func (r slideReader) runLink(x *pull) string {
	link := ""
	x.walk(func(t tag) bool {
		if t.name == "hlinkClick" {
			link = ""
			if id, ok := t.attr("id"); ok {
				link, _ = r.rels.link(id)
			}
		}
		return false
	})
	return link
}

// frame is a table frame. Charts and SmartArt live in parts of their own
// and are not read.
func (r slideReader) frame(x *pull) (shape, bool) {
	at := unplaced
	var rows [][]string
	x.walk(func(t tag) bool {
		switch {
		case t.name == "off" && at == unplaced:
			at = position(t)
			x.skip()
		case t.name == "tr":
			rows = append(rows, nil)
			return false
		case t.name == "tc":
			var cell []string
			x.walk(func(t tag) bool {
				if t.name == "txBody" {
					for _, p := range r.textBody(x) {
						cell = append(cell, p.line.render())
					}
				} else {
					x.skip()
				}
				return true
			})
			if len(rows) > 0 {
				rows[len(rows)-1] = append(rows[len(rows)-1], strings.Join(cell, "\n"))
			}
		default:
			return false
		}
		return true
	})
	if len(rows) == 0 {
		return shape{}, false
	}
	return shape{at: at, markdown: table(rows)}, true
}

// picture is ![alt text](image3.png), falling back on the shape's name
// ("Picture 3") without a description.
func (r slideReader) picture(x *pull) (shape, bool) {
	at := unplaced
	alt, file := "", ""
	x.walk(func(t tag) bool {
		switch {
		case t.name == "cNvPr":
			described := t.val("descr")
			if strings.TrimSpace(described) == "" {
				described = t.val("name")
			}
			alt = imageAlt(described)
		case t.name == "blip":
			file = ""
			if id, ok := t.attr("embed"); ok {
				if target, ok := r.rels.target(id); ok {
					file = target[strings.LastIndex(target, "/")+1:]
				}
			}
		case t.name == "off" && at == unplaced:
			at = position(t)
		}
		return false
	})
	if alt == "" && file == "" {
		return shape{}, false
	}
	return shape{at: at, markdown: "![" + alt + "](" + linkTarget(file) + ")"}, true
}

func bodyMarkdown(paragraphs []slideParagraph, bulleted bool) string {
	var b blocks
	// Each level's next number (0: no numbered list there): every item of
	// a list carries its start, and going up a level ends the lists below.
	var next []int
	for _, p := range paragraphs {
		text := p.line.render()
		if len(next) > p.level+1 {
			next = next[:p.level+1]
		}
		for len(next) < p.level+1 {
			next = append(next, 0)
		}
		switch {
		case p.bullet.kind == bulletNumber:
			number := p.bullet.start
			if next[p.level] != 0 {
				number = next[p.level]
			}
			next[p.level] = number + 1
			b.pushItem(p.level, strconv.Itoa(number)+".", text)
			continue
		case p.bullet.kind == bulletChar, p.bullet.kind == bulletInherited && bulleted:
			b.pushItem(p.level, "-", text)
		default:
			b.push(text)
		}
		next[p.level] = 0
	}
	return b.String()
}

// bulletOf is the bullet an <a:pPr> sets, through its close.
func bulletOf(x *pull) bullet {
	var b bullet
	x.walk(func(t tag) bool {
		switch t.name {
		case "buNone":
			b = bullet{kind: bulletNone}
		case "buChar", "buBlip":
			b = bullet{kind: bulletChar}
		case "buAutoNum":
			start, ok := count(t, "startAt")
			if !ok {
				start = 1
			}
			b = bullet{bulletNumber, start}
		}
		return false
	})
	return b
}

// offset is the first <a:off> inside the element just opened, through its
// close.
func offset(x *pull) point {
	at := unplaced
	x.walk(func(t tag) bool {
		if t.name == "off" && at == unplaced {
			at = position(t)
		}
		return false
	})
	return at
}

func position(t tag) point {
	coord := func(name string) int64 {
		v, _ := strconv.ParseInt(t.val(name), 10, 64)
		return v
	}
	return point{coord("y"), coord("x")}
}

func on(value string) bool { return value == "1" || value == "true" }
