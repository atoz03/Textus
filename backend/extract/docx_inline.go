package extract

import (
	"fmt"
	"strings"
)

// Inside a paragraph: runs and their marks, links, note references, breaks
// and pictures, as Markdown inline text.

// inline reads one element inside a paragraph, whose open was just read.
func (d *docx) inline(x *pull, t tag, link string, line *inline, boxed *blocks) {
	switch t.name {
	case "r":
		d.run(x, link, line, boxed)
	case "hyperlink":
		if url, ok := t.attr("id"); ok {
			if target, ok := d.rels.link(url); ok {
				link = target
			}
		}
		d.inlineChildren(x, link, line, boxed)
	case "Fallback", "del", "moveFrom":
		x.skip()
	default:
		// fldSimple, smartTag, ins, sdt...: their runs are the text.
		d.inlineChildren(x, link, line, boxed)
	}
}

func (d *docx) inlineChildren(x *pull, link string, line *inline, boxed *blocks) {
	for n := x.next(); n.kind == nodeOpen || n.kind == nodeText; n = x.next() {
		if n.kind == nodeOpen {
			d.inline(x, n.tag, link, line, boxed)
		}
	}
}

func (d *docx) run(x *pull, link string, line *inline, boxed *blocks) {
	var m marks
	for n := x.next(); n.kind == nodeOpen || n.kind == nodeText; n = x.next() {
		if n.kind == nodeText {
			continue
		}
		switch t := n.tag; t.name {
		case "rPr":
			m = d.runProps(x)
		case "t":
			line.push(d.text(x.text(), link != ""), m, link)
		case "tab":
			x.skip()
			line.push("\t", m, link)
		case "br", "cr":
			x.skip()
			// A page break ends nothing a reader of the text sees.
			if t.val("type") != "page" {
				line.push("\n", m, link)
			}
		case "footnoteReference", "endnoteReference":
			x.skip()
			kind := strings.TrimSuffix(t.name, "Reference")
			if id, ok := t.attr("id"); ok {
				line.pushMarkdown(fmt.Sprintf("[^%d]", d.cite(kind, id)))
			}
		case "drawing", "pict", "AlternateContent", "Choice":
			if image, ok := d.drawing(x, boxed); ok {
				line.pushMarkdown(image)
			}
		default:
			x.skip()
		}
	}
}

func (d *docx) runProps(x *pull) marks {
	var m marks
	for n := x.next(); n.kind == nodeOpen || n.kind == nodeText; n = x.next() {
		if n.kind == nodeText {
			continue
		}
		switch t := n.tag; t.name {
		case "b":
			m.bold = t.isOn()
		case "i":
			m.italic = t.isOn()
		case "strike", "dstrike":
			m.strike = t.isOn()
		case "rStyle":
			// The built-in character styles a writer applies instead of
			// the B and I buttons.
			if style, ok := t.attr("val"); ok {
				switch d.styles.name(style) {
				case "strong":
					m.bold = true
				case "emphasis":
					m.italic = true
				}
			}
		}
		x.skip()
	}
	return m
}

// drawing is a picture as ![alt text](media/image1.png); text boxes in it
// go to boxed.
func (d *docx) drawing(x *pull, boxed *blocks) (string, bool) {
	alt, target, found := "", "", false
	depth := 0
loop:
	for {
		n := x.next()
		switch n.kind {
		case nodeOpen:
			switch t := n.tag; t.name {
			case "docPr", "cNvPr":
				if alt == "" {
					for _, key := range []string{"descr", "title"} {
						if text := t.val(key); strings.TrimSpace(text) != "" {
							alt = imageAlt(text)
							break
						}
					}
				}
				x.skip()
			case "blip", "imagedata":
				id, ok := t.attr("embed")
				if !ok {
					id, ok = t.attr("id")
				}
				if ok && !found {
					target, found = d.rels.target(id)
				}
				x.skip()
			case "txbxContent":
				d.blocks(x, boxed)
			case "Fallback":
				x.skip()
			default:
				depth++
			}
		case nodeClose:
			if depth == 0 {
				break loop
			}
			depth--
		case nodeEnd:
			break loop
		}
	}
	if !found {
		return "", false
	}
	// An external picture (TargetMode="External") is a URL already.
	if !strings.Contains(target, "://") && d.picture != nil {
		if src, ok := d.picture(target); ok {
			target = src
		}
	}
	return "![" + alt + "](" + linkTarget(target) + ")", true
}
