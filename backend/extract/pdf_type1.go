package extract

import (
	"cmp"
	"fmt"
	"io"
	"strings"

	"github.com/ledongthuc/pdf"
)

// Glyph names: a simple font's codes name its glyphs, by the encoding an
// embedded Type 1 font carries and the /Differences its dictionary lists,
// and a glyph's name says which character it draws. TeX's fonts carry
// their own encodings, and a PDF TeX made without ToUnicode maps (older
// TeX did) reads by nothing else.

// builtInNames is the encoding a Type 1 font program gives in its clear
// text, ahead of the encrypted part: /Encoding 256 array, then
// "dup <code> /<name> put" for each code. A font on StandardEncoding gives
// none.
func builtInNames(program io.Reader) (names [256]string) {
	l := newLexer(program, maxOperands)
	encoding := false
	for {
		op, args, ok := l.next()
		switch {
		case !ok || op == "eexec" || encoding && op == "def":
			return names
		case op == "array" && len(args) == 2 && args[0].kind == '/' && args[0].text == "Encoding":
			encoding = true
		case encoding && op == "put" && len(args) == 2 && args[0].kind == 'n' && args[1].kind == '/':
			if c := args[0].num; c >= 0 && c < 256 {
				names[int(c)] = args[1].text
			}
		}
	}
}

// glyphText is the text of each code's glyph name: uniXXXX and uXXXX
// spell theirs out, and the parser's glyph list (Adobe's) has the rest.
// That list is reached only through a font's /Differences, so the names
// are written into one, in a document of nothing else. A name the list
// doesn't hold reads as its code.
func glyphText(names *[256]string) (text [256]string) {
	var doc strings.Builder
	doc.WriteString("%PDF-1.4\n")
	xref := doc.Len()
	doc.WriteString("xref\n0 1\n0000000000 65535 f \ntrailer\n<< /Size 1 /Encoding << /Differences [0")
	for c, name := range names {
		if t, ok := unicodeName(name); ok {
			text[c], name = t, ""
		}
		doc.WriteString(" /" + pdfName(cmp.Or(name, ".notdef")))
	}
	fmt.Fprintf(&doc, "] >> >>\nstartxref\n%d\n%%%%EOF\n", xref)
	r, err := pdf.NewReader(strings.NewReader(doc.String()), int64(doc.Len()))
	if err != nil {
		return text
	}
	listed := (&pdf.Font{V: r.Trailer()}).Encoder()
	for c, name := range names {
		if name != "" && text[c] == "" {
			text[c] = listed.Decode(string([]byte{byte(c)}))
		}
	}
	return text
}

// pdfName is a name as a PDF file writes it: what would end it, #-escaped.
func pdfName(name string) string {
	var b strings.Builder
	for i := 0; i < len(name); i++ {
		if c := name[i]; c > ' ' && c < 0x7F && c != '#' && !isPDFDelimiter(c) {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "#%02X", c)
		}
	}
	return b.String()
}
