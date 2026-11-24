package extract

import (
	"archive/zip"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Word (.docx) to Markdown: headings, lists, tables, emphasis, links,
// pictures by alt text, and notes as [^1] references. Follows mammoth's
// default style map, as MarkItDown (MIT) does, but reads the parts directly.

// SaveImage saves one picture of a document being opened (its file name in
// the .docx and its bytes) and answers the src to link it by; false links
// it by its path inside the file.
type SaveImage func(name string, data []byte) (string, bool)

// DocxMarkdown is the Markdown the AI reads: the text as written, pictures
// by their path inside the file.
func DocxMarkdown(path string) (string, error) { return convertDocx(path, nil) }

// DocxDocument is the Markdown of a Word file opened as a document. Unlike
// the AI's, the text is escaped, line breaks stay breaks, and each picture
// goes to save, whose src the document links to.
func DocxDocument(path string, save SaveImage) (string, error) {
	return convertDocx(path, save)
}

type noteKey struct{ kind, id string }

type docx struct {
	styles    styles
	numbering *numbering
	rels      rels
	// notes are footnote and endnote text by kind and w:id.
	notes map[noteKey]string
	// cited are the notes in the order the body refers to them; a note's
	// number is its place here.
	cited []noteKey
	// picture, when the file is opened as a document, is the src a
	// picture's relationship target is saved as. It also means the text is
	// escaped.
	picture func(target string) (string, bool)
	// oneLine counts the table cells and notes the reader is inside, whose
	// paragraphs are written on one line: a line break there is no break.
	oneLine int
}

func convertDocx(path string, save SaveImage) (string, error) {
	z, err := zip.OpenReader(path)
	if err != nil {
		return "", errors.New("This .docx is not a readable Word file.")
	}
	defer z.Close()
	body, ok := zipEntry(&z.Reader, "word/document.xml")
	if !ok {
		return "", errors.New("This .docx has no document body.")
	}
	part := func(name string) []byte {
		data, _ := zipEntry(&z.Reader, name)
		return data
	}
	doc := &docx{
		styles:    parseStyles(part("word/styles.xml")),
		numbering: parseNumbering(part("word/numbering.xml")),
		notes:     map[noteKey]string{},
	}
	if save != nil {
		// The pictures are read out of the file as the text reaches them;
		// one used twice is saved once.
		type savedAs struct {
			src string
			ok  bool
		}
		saved := map[string]savedAs{}
		doc.picture = func(target string) (string, bool) {
			if s, ok := saved[target]; ok {
				return s.src, s.ok
			}
			entry := resolvePart("word", target)
			var s savedAs
			if data, ok := zipEntry(&z.Reader, entry); ok {
				s.src, s.ok = save(entry[strings.LastIndex(entry, "/")+1:], data)
			}
			saved[target] = s
			return s.src, s.ok
		}
	}
	// Notes first, so a reference in the body can number itself.
	for _, kind := range []string{"footnote", "endnote"} {
		doc.rels = parseRels(part("word/_rels/" + kind + "s.xml.rels"))
		doc.readNotes(part("word/"+kind+"s.xml"), kind)
	}
	doc.rels = parseRels(part("word/_rels/document.xml.rels"))

	var out blocks
	doc.blocks(newPull(body), &out)
	text := out.String()
	// Definitions after the body, as a Markdown reader expects them.
	for i, key := range doc.cited {
		if note, ok := doc.notes[key]; ok {
			text += fmt.Sprintf("\n\n[^%d]: %s", i+1, note)
		}
	}
	return strings.TrimSpace(text), nil
}

// opening is whether the file is opened as a document rather than read
// for the AI.
func (d *docx) opening() bool { return d.picture != nil }

// text is a run's text as Markdown text, inLink when it is a link's.
func (d *docx) text(text string, inLink bool) string {
	if d.opening() {
		return escapeText(text, inLink)
	}
	return text
}

// lines is a paragraph's rendered runs as the lines of one block. Opened
// as a document, a line break is a hard break, and no line may start a
// block of its own.
func (d *docx) lines(text string) string {
	if !d.opening() {
		return text
	}
	text = escapeLineStarts(text)
	if d.oneLine > 0 {
		return text
	}
	return strings.ReplaceAll(text, "\n", "\\\n")
}

// blocks reads paragraphs and tables until the close of the element they
// sit in. Content controls, custom XML and inserted revisions are looked
// through.
func (d *docx) blocks(x *pull, out *blocks) {
	depth := 0
	for {
		switch n := x.next(); n.kind {
		case nodeOpen:
			switch n.tag.name {
			case "p":
				d.paragraph(x, out)
			case "tbl":
				out.push(d.table(x))
			// mc:AlternateContent carries a drawing twice, the Fallback for
			// old readers; reading both doubles it.
			case "Fallback", "sectPr", "del":
				x.skip()
			default:
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

// listRef is a paragraph's list: its level and w:numId.
type listRef struct {
	level int
	id    string
	ok    bool
}

// paraProps is what <w:pPr> says about a paragraph.
type paraProps struct {
	style   string
	outline int // a heading's level, 0 for none
	list    listRef
}

func (d *docx) paragraph(x *pull, out *blocks) {
	var props paraProps
	var line inline
	// Text boxes anchored in this paragraph: paragraphs of their own,
	// written after it.
	var boxed blocks
	for n := x.next(); n.kind == nodeOpen || n.kind == nodeText; n = x.next() {
		switch {
		case n.kind == nodeText:
		case n.tag.name == "pPr":
			props = paragraphProps(x)
		default:
			d.inline(x, n.tag, "", &line, &boxed)
		}
	}
	heading := props.outline
	if heading == 0 && props.style != "" {
		heading = d.styles.heading(props.style)
	}
	list := props.list
	if !list.ok && props.style != "" {
		list = d.styles.numbering(props.style)
	}
	switch {
	case heading > 0 && line.renderPlain() != "":
		text := line.renderPlain()
		if d.opening() {
			text = escapeClosingHashes(text)
		}
		out.push(strings.Repeat("#", min(heading, 6)) + " " + text)
	case list.ok && list.id != "0":
		text := d.lines(line.render())
		marker := "-"
		if !d.numbering.isBullet(list.id, list.level) {
			marker = strconv.Itoa(d.numbering.nextNumber(list.id, list.level)) + "."
		}
		out.pushItem(list.level, marker, text)
	case props.style != "" && d.styles.isQuote(props.style):
		var quoted []string
		for _, l := range lines(d.lines(line.render())) {
			quoted = append(quoted, "> "+l)
		}
		out.push(strings.Join(quoted, "\n"))
	default:
		out.push(d.lines(line.render()))
	}
	if !boxed.empty() {
		out.push(boxed.String())
	}
}

func (d *docx) table(x *pull) string {
	var rows [][]string
	x.walk(func(t tag) bool {
		switch t.name {
		case "tr":
			rows = append(rows, d.row(x))
		case "tblPr", "tblGrid":
			x.skip()
		default:
			return false
		}
		return true
	})
	return table(rows)
}

func (d *docx) row(x *pull) []string {
	var cells []string
	x.walk(func(t tag) bool {
		switch t.name {
		case "tc":
			text, span := d.cell(x)
			cells = append(cells, text)
			// A merged cell keeps the columns after it in place.
			for range span - 1 {
				cells = append(cells, "")
			}
		case "trPr", "tblPrEx":
			x.skip()
		default:
			return false
		}
		return true
	})
	return cells
}

// cell is a cell's text and how many grid columns it spans.
func (d *docx) cell(x *pull) (string, int) {
	span := 1
	var content blocks
	d.oneLine++
	defer func() { d.oneLine-- }()
	for n := x.next(); n.kind == nodeOpen || n.kind == nodeText; n = x.next() {
		if n.kind == nodeText {
			continue
		}
		switch n.tag.name {
		case "tcPr":
			span = cellSpan(x)
		case "p":
			d.paragraph(x, &content)
		case "tbl":
			content.push(d.table(x))
		default:
			x.skip()
		}
	}
	return content.String(), span
}

func (d *docx) readNotes(data []byte, kind string) {
	x := newPull(data)
	for n := x.next(); n.kind != nodeEnd; n = x.next() {
		if n.kind != nodeOpen || n.tag.name != kind {
			continue
		}
		var content blocks
		d.oneLine++
		d.blocks(x, &content)
		d.oneLine--
		// A definition is one paragraph; the separator "notes" Word keeps
		// beside the real ones are empty.
		if text := strings.Join(strings.Fields(content.String()), " "); text != "" {
			d.notes[noteKey{kind, n.tag.val("id")}] = text
		}
	}
}

// cite is the number of note id, numbering it on its first reference.
func (d *docx) cite(kind, id string) int {
	key := noteKey{kind, id}
	for i, cited := range d.cited {
		if cited == key {
			return i + 1
		}
	}
	d.cited = append(d.cited, key)
	return len(d.cited)
}

// count is a non-negative whole number attribute.
func count(t tag, name string) (int, bool) {
	n, err := strconv.Atoi(t.val(name))
	if err != nil || n < 0 {
		return 0, false
	}
	return n, true
}

func paragraphProps(x *pull) paraProps {
	var props paraProps
	level, id, hasID := 0, "", false
	depth := 0
	for {
		n := x.next()
		switch n.kind {
		case nodeOpen:
			switch n.tag.name {
			case "pStyle":
				props.style = n.tag.val("val")
			case "outlineLvl":
				props.outline = headingOfOutline(n.tag)
			case "ilvl":
				level, _ = count(n.tag, "val")
			case "numId":
				id, hasID = n.tag.attr("val")
			// The paragraph mark's own formatting, and revisions.
			case "rPr", "pPrChange":
				x.skip()
				continue
			}
			depth++
		case nodeClose:
			if depth == 0 {
				props.list = listRef{level, id, hasID}
				return props
			}
			depth--
		case nodeEnd:
			props.list = listRef{level, id, hasID}
			return props
		}
	}
}

// headingOfOutline reads <w:outlineLvl w:val="0"/> as a top heading; 9 is
// body text.
func headingOfOutline(t tag) int {
	if level, ok := count(t, "val"); ok && level < 6 {
		return level + 1
	}
	return 0
}

func cellSpan(x *pull) int {
	span, depth := 1, 0
	for {
		n := x.next()
		switch n.kind {
		case nodeOpen:
			if n.tag.name == "gridSpan" {
				if v, ok := count(n.tag, "val"); ok {
					span = min(max(v, 1), 64)
				} else {
					span = 1
				}
			}
			depth++
		case nodeClose:
			if depth == 0 {
				return span
			}
			depth--
		case nodeEnd:
			return span
		}
	}
}

// imageAlt is alt text on one line, without the brackets that would end
// it early.
func imageAlt(text string) string {
	return strings.Join(strings.Fields(strings.NewReplacer("[", " ", "]", " ").Replace(text)), " ")
}
