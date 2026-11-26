package extract

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode"

	"github.com/ledongthuc/pdf"
)

// PDF to text: the text layer in the order it is drawn, broken into lines
// and paragraphs where the page puts it. Headings aren't guessed from font
// sizes, which would misfire. ledongthuc/pdf (a fork of rsc.io/pdf) reads
// the file's objects; the fonts, the spacing and the lines are read here,
// the parser's own text has no spaces wherever a page sets them by
// moving the pen.

const (
	// maxContentBytes is the most the streams of one document unpack to:
	// far more than any real document's, far less than a crafted one's.
	maxContentBytes = 512 << 20
	// maxPages and maxTreeDepth bound the page tree, which a crafted file
	// can make loop.
	maxPages     = 100_000
	maxTreeDepth = 64
)

var (
	errPDFUnreadable = errors.New("This PDF can't be read.")
	errPDFNoText     = errors.New("This PDF has no text layer - it's likely a scan, so there is nothing to read.")
	errPDFEncrypted  = errors.New("This PDF is encrypted, and Textus can't read it.")
)

// PDFText is the text of the PDF at path.
func PDFText(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return PDFTextOf(data)
}

// PDFTextOf is the text of a PDF held in memory, such as a download.
func PDFTextOf(data []byte) (text string, err error) {
	// The parser panics on a malformed file: that ends this read, never
	// the app's.
	defer func() {
		if recover() != nil {
			text, err = "", errPDFUnreadable
		}
	}()
	view, ok := viewOf(data)
	if !ok {
		return "", errPDFUnreadable
	}
	r, err := pdf.NewReader(view, int64(len(view.data)))
	if err != nil {
		if errors.Is(err, pdf.ErrInvalidPassword) || strings.Contains(err.Error(), "encrypt") {
			return "", errPDFEncrypted
		}
		return "", errPDFUnreadable
	}
	pages := pdfPages(r)
	doc := &pdfDoc{fonts: map[string]*pdfFont{}, left: maxContentBytes}
	var out strings.Builder
	read, hasText := 0, false
	for i, page := range pages {
		if doc.left <= 0 {
			// Said out loud: a silently cut document reads as "the model saw
			// all of it".
			fmt.Fprintf(&out, "[... pages %d-%d are not shown: the PDF holds more than can be read at once]", i+1, len(pages))
			break
		}
		text, ok := doc.pageText(page)
		hasText = hasText || strings.TrimSpace(text) != ""
		out.WriteString(text)
		if ok {
			read++
		} else {
			fmt.Fprintf(&out, "\n\n[page %d can't be read]", i+1)
		}
		out.WriteString("\n\n")
	}
	switch {
	case read == 0:
		return "", errPDFUnreadable
	case !hasText:
		return "", errPDFNoText
	}
	return tidyLines(ligatures.Replace(out.String())), nil
}

// ligatures are letters a font draws as one glyph, which some writers
// (Chrome's, for one) map back to the one character, ﬁ for fi: a search
// for "office" misses "oﬃce".
var ligatures = strings.NewReplacer("ﬀ", "ff", "ﬁ", "fi", "ﬂ", "fl", "ﬃ", "ffi", "ﬄ", "ffl", "ﬅ", "st", "ﬆ", "st")

// pdfView is a file as the parser takes it: from its header, which reads
// as %PDF-1.7 whatever version it says (the parser knows no later one,
// and nothing in a file points at its header), to its last %%EOF, past
// which some files carry junk.
type pdfView struct{ data []byte }

const pdfHeader = "%PDF-1.7\n"

func viewOf(data []byte) (*pdfView, bool) {
	start := bytes.Index(data[:min(len(data), 1024)], []byte("%PDF-"))
	if start < 0 {
		return nil, false
	}
	data = data[start:]
	if end := bytes.LastIndex(data, []byte("%%EOF")); end >= 0 {
		data = data[:end+5]
	}
	return &pdfView{data}, len(data) > len(pdfHeader)
}

func (v *pdfView) ReadAt(p []byte, off int64) (int, error) {
	if off < 0 || off >= int64(len(v.data)) {
		return 0, io.EOF
	}
	n := copy(p, v.data[off:])
	for i := off; i < int64(len(pdfHeader)) && i < off+int64(n); i++ {
		p[i-off] = pdfHeader[i]
	}
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

// pdfPage is a page and the resources it has or inherits.
type pdfPage struct{ page, resources pdf.Value }

func pdfPages(r *pdf.Reader) []pdfPage {
	var pages []pdfPage
	visited := 0
	var walk func(node, resources pdf.Value, depth int)
	walk = func(node, resources pdf.Value, depth int) {
		// A broken node loses its own pages, not the document's.
		defer func() { _ = recover() }()
		visited++
		if depth > maxTreeDepth || visited > maxPages || node.Kind() != pdf.Dict {
			return
		}
		if own := node.Key("Resources"); own.Kind() == pdf.Dict {
			resources = own
		}
		kids := node.Key("Kids")
		if kids.Kind() != pdf.Array || node.Key("Type").Name() == "Page" {
			pages = append(pages, pdfPage{node, resources})
			return
		}
		for i := 0; i < kids.Len() && visited <= maxPages; i++ {
			walk(kids.Index(i), resources, depth+1)
		}
	}
	walk(r.Trailer().Key("Root").Key("Pages"), pdf.Value{}, 0)
	return pages
}

// pdfDoc is what reading one document keeps: its fonts, read once each,
// and how much more its streams may unpack to.
type pdfDoc struct {
	fonts map[string]*pdfFont
	left  int64
}

// pageText is a page's text, and false when part of it couldn't be read.
func (d *pdfDoc) pageText(pg pdfPage) (text string, ok bool) {
	out := &pdfLines{}
	defer func() {
		if recover() != nil {
			text, ok = out.String(), false
		}
	}()
	p := &painter{doc: d, out: out, st: state{ctm: identity, hScale: 1}, tm: identity, tlm: identity}
	p.run(d.contents(pg.page.Key("Contents")), pg.resources)
	return out.String(), true
}

// contents is a page's content: one stream, or several read as one, a
// line end between them as readers put one.
func (d *pdfDoc) contents(c pdf.Value) io.Reader {
	if c.Kind() != pdf.Array {
		return d.stream(c)
	}
	var parts []io.Reader
	for i := 0; i < c.Len(); i++ {
		parts = append(parts, d.stream(c.Index(i)), strings.NewReader("\n"))
	}
	return io.MultiReader(parts...)
}

// stream is a stream's data as far as the document's budget goes. One the
// parser can't unpack (a filter it doesn't know) panics: the page it draws
// can't be read.
func (d *pdfDoc) stream(v pdf.Value) io.Reader {
	if v.Kind() != pdf.Stream {
		return strings.NewReader("")
	}
	return &budgeted{v.Reader(), d}
}

// fontStream is one of a font's streams, empty when it can't be unpacked:
// the font is read without it.
func (d *pdfDoc) fontStream(v pdf.Value) (r io.Reader) {
	defer func() {
		if recover() != nil {
			r = strings.NewReader("")
		}
	}()
	return d.stream(v)
}

func (d *pdfDoc) font(v pdf.Value) (f *pdfFont) {
	if v.Kind() != pdf.Dict {
		return plainFont()
	}
	// The same font is named on every page: read it once.
	key := v.String()
	if f, ok := d.fonts[key]; ok {
		return f
	}
	defer func() {
		if recover() != nil {
			f = plainFont()
		}
		d.fonts[key] = f
	}()
	return newFont(v, d.fontStream)
}

type budgeted struct {
	r   io.Reader
	doc *pdfDoc
}

func (b *budgeted) Read(p []byte) (int, error) {
	if b.doc.left <= 0 {
		return 0, io.EOF
	}
	if int64(len(p)) > b.doc.left {
		p = p[:b.doc.left]
	}
	n, err := b.r.Read(p)
	b.doc.left -= int64(n)
	return n, err
}

// tidyLines trims each line's end and folds the runs of blank lines a
// text layer is full of into one, leading ones dropped: one buffer, as
// the text can be megabytes.
func tidyLines(text string) string {
	var out strings.Builder
	out.Grow(len(text))
	blank := false
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimRightFunc(line, unicode.IsSpace)
		if line == "" {
			blank = out.Len() > 0
			continue
		}
		if out.Len() > 0 {
			out.WriteByte('\n')
		}
		if blank {
			out.WriteByte('\n')
			blank = false
		}
		out.WriteString(line)
	}
	return out.String()
}
