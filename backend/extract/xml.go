// Package extract reads documents of other formats as Markdown: Word's
// .docx (docx.go), opened as a new document or read for the AI.
package extract

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"io"
	"path"
	"strings"
)

// A pull parser over one XML part: every element arrives as an open
// answered by one close, and skip passes over one. Names are local names,
// since OOXML files may bind namespaces to any prefix.

const (
	// maxDepth is deeper than any real document nests; a crafted one stops
	// here, not in a stack overflow (the readers nest a call per element).
	maxDepth = 256
	// maxPartBytes is the most one part of a package unpacks to: a small
	// file can't unpack to gigabytes.
	maxPartBytes = 128 << 20
)

// tag is an element as opened: its local name and attributes.
type tag struct {
	name  string
	attrs []xml.Attr
}

// attr is the attribute whose local name is name (w:val, r:id: val, id).
func (t tag) attr(name string) (string, bool) {
	for _, a := range t.attrs {
		if a.Name.Local == name {
			return a.Value, true
		}
	}
	return "", false
}

func (t tag) val(name string) string {
	v, _ := t.attr(name)
	return v
}

// isOn reads an OOXML on/off property: <w:b/> and <w:b w:val="true"/> are
// on, <w:b w:val="0"/> - written to switch off a style's bold - is off.
func (t tag) isOn() bool {
	switch v, ok := t.attr("val"); {
	case !ok:
		return true
	default:
		return v != "0" && v != "false" && v != "off" && v != "none"
	}
}

type nodeKind uint8

const (
	nodeOpen nodeKind = iota
	nodeClose
	nodeText
	nodeEnd
)

type node struct {
	kind nodeKind
	tag  tag
	text string
}

type pull struct {
	d     *xml.Decoder
	depth int
	ended bool
}

func newPull(data []byte) *pull {
	d := xml.NewDecoder(bytes.NewReader(data))
	d.Strict = false
	return &pull{d: d}
}

// next is the next node; a truncated or malformed part reads as far as it
// is well-formed rather than failing the whole file.
func (p *pull) next() node {
	for !p.ended {
		tok, err := p.d.RawToken()
		if err != nil {
			p.ended = true
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			p.depth++
			if p.depth > maxDepth {
				p.ended = true
				break
			}
			return node{kind: nodeOpen, tag: tag{name: t.Name.Local, attrs: t.Attr}}
		case xml.EndElement:
			p.depth = max(p.depth-1, 0)
			return node{kind: nodeClose}
		case xml.CharData:
			return node{kind: nodeText, text: string(t)}
		}
	}
	return node{kind: nodeEnd}
}

// skip consumes the rest of the element just opened, through its close.
func (p *pull) skip() {
	depth := 0
	for {
		switch n := p.next(); n.kind {
		case nodeOpen:
			depth++
		case nodeClose:
			if depth == 0 {
				return
			}
			depth--
		case nodeEnd:
			return
		}
	}
}

// walk walks the element just opened through its close. open is offered
// each element inside and answers whether it read that one through
// itself; the ones it didn't are walked into, their children offered in
// turn.
func (p *pull) walk(open func(t tag) bool) {
	depth := 0
	for {
		switch n := p.next(); n.kind {
		case nodeOpen:
			if !open(n.tag) {
				depth++
			}
		case nodeClose:
			if depth == 0 {
				return
			}
			depth--
		case nodeEnd:
			return
		}
	}
}

// text is all the text inside the element just opened, through its close.
func (p *pull) text() string {
	var out strings.Builder
	depth := 0
	for {
		switch n := p.next(); n.kind {
		case nodeOpen:
			depth++
		case nodeClose:
			if depth == 0 {
				return out.String()
			}
			depth--
		case nodeText:
			out.WriteString(n.text)
		case nodeEnd:
			return out.String()
		}
	}
}

// rels are the relationships of one OOXML part (_rels/*.rels): what an
// r:id on a hyperlink or a picture points at.
type rels []rel

type rel struct {
	id, target, kind string
	external         bool
}

func parseRels(data []byte) rels {
	var out rels
	p := newPull(data)
	for n := p.next(); n.kind != nodeEnd; n = p.next() {
		if n.kind == nodeOpen && n.tag.name == "Relationship" {
			out = append(out, rel{
				id:       n.tag.val("Id"),
				target:   n.tag.val("Target"),
				kind:     n.tag.val("Type"),
				external: n.tag.val("TargetMode") == "External",
			})
		}
	}
	return out
}

// link is the URL a hyperlink's r:id names. An internal target (a
// bookmark, another part) is no link a reader can follow.
func (r rels) link(id string) (string, bool) {
	for _, x := range r {
		if x.id == id && x.external {
			return x.target, true
		}
	}
	return "", false
}

func (r rels) target(id string) (string, bool) {
	for _, x := range r {
		if x.id == id {
			return x.target, true
		}
	}
	return "", false
}

// ofKind is the first target whose relationship type ends with /kind
// (notesSlide, footnotes).
func (r rels) ofKind(kind string) (string, bool) {
	for _, x := range r {
		if x.kind[strings.LastIndex(x.kind, "/")+1:] == kind {
			return x.target, true
		}
	}
	return "", false
}

// resolvePart is a relationship target as the zip entry it names:
// ../media/a.png from word/ is media/a.png.
func resolvePart(dir, target string) string {
	if absolute, ok := strings.CutPrefix(target, "/"); ok {
		return absolute
	}
	var parts []string
	for _, p := range strings.Split(dir, "/") {
		if p != "" {
			parts = append(parts, p)
		}
	}
	for _, p := range strings.Split(target, "/") {
		switch p {
		case "..":
			if len(parts) > 0 {
				parts = parts[:len(parts)-1]
			}
		case ".", "":
		default:
			parts = append(parts, p)
		}
	}
	return strings.Join(parts, "/")
}

// zipEntry is the bytes of one entry, or false when the archive lacks it
// (most parts are optional) or it unpacks past maxPartBytes.
func zipEntry(z *zip.Reader, name string) ([]byte, bool) {
	f := zipFile(z, name)
	if f == nil {
		return nil, false
	}
	r, err := f.Open()
	if err != nil {
		return nil, false
	}
	defer r.Close()
	data, err := io.ReadAll(io.LimitReader(r, maxPartBytes+1))
	if err != nil || len(data) > maxPartBytes {
		return nil, false
	}
	return data, true
}

// zipFile is the entry of a part, nil for none: the one by its name, else
// one whose name differs only in case or in its slashes. Part names ignore
// case, and some tools write them with backslashes (xl\workbook.xml).
func zipFile(z *zip.Reader, name string) *zip.File {
	var near *zip.File
	loose := strings.ReplaceAll(name, `\`, "/")
	for _, f := range z.File {
		if f.Name == name {
			return f
		}
		if near == nil && strings.EqualFold(strings.ReplaceAll(f.Name, `\`, "/"), loose) {
			near = f
		}
	}
	return near
}

// partRels is the folder a part is in, which its relationships' targets
// are relative to, and those relationships: xl/workbook.xml's are in
// xl/_rels/workbook.xml.rels.
func partRels(z *zip.Reader, part string) (string, rels) {
	dir, name := path.Split(part)
	data, _ := zipEntry(z, dir+"_rels/"+name+".rels")
	return dir, parseRels(data)
}
