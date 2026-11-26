package extract

import (
	"io"
	"math"
	"strconv"
	"strings"

	"github.com/ledongthuc/pdf"
	"golang.org/x/text/encoding/charmap"
)

// A font as text is read from it: what each code stands for, from the
// font's ToUnicode map first and its encoding after, and how far each
// moves the pen, from its widths.

const (
	// averageWidth stands in for widths a font doesn't give (the
	// standard 14 fonts need none): about half an em.
	averageWidth = 500
	// maxWidths bounds what one font's widths take.
	maxWidths = 1 << 16
)

type pdfFont struct {
	// composite is a Type0 font: codes of one to four bytes, widths by CID.
	composite bool
	vertical  bool
	// encoding is a composite font's own CMap, whose codespace splits the
	// codes and whose CIDs index the widths; without one a code is two
	// bytes and its own CID (Identity-H).
	encoding *cmap
	// utf16 is a composite font whose codes are UTF-16 (UniGB-UCS2-H...).
	utf16   bool
	unicode *cmap
	// byCode is a simple font's text for each code, by its encoding.
	byCode  [256]string
	widths  map[uint32]float64
	spans   []widthSpan
	missing float64
	// scale takes glyph space to text space: 1/1000, or a Type 3 font's
	// own matrix.
	scale float64
}

// widthSpan is the CIDs lo-hi, all of width w.
type widthSpan struct {
	lo, hi uint32
	w      float64
}

// glyph is one code as shown: its text, its width in text space (before
// the font size), and whether it is the one-byte code 32, which word
// spacing widens.
type glyph struct {
	text  string
	width float64
	space bool
}

// plainFont reads codes as Latin-1: before any font is chosen, or for one
// that can't be read.
func plainFont() *pdfFont {
	f := &pdfFont{widths: map[uint32]float64{}, missing: averageWidth, scale: 0.001}
	for c := range f.byCode {
		f.byCode[c] = string(rune(c))
	}
	return f
}

// newFont reads a font dictionary; open reads one of its streams.
func newFont(v pdf.Value, open func(pdf.Value) io.Reader) *pdfFont {
	f := plainFont()
	if tu := v.Key("ToUnicode"); tu.Kind() == pdf.Stream {
		f.unicode = parseCMap(open(tu))
	}
	if v.Key("Subtype").Name() == "Type0" {
		f.composite = true
		switch enc := v.Key("Encoding"); enc.Kind() {
		case pdf.Name:
			name := enc.Name()
			f.vertical = strings.HasSuffix(name, "-V")
			f.utf16 = strings.Contains(name, "UCS2") || strings.Contains(name, "UTF16")
		case pdf.Stream:
			f.encoding = parseCMap(open(enc))
			f.vertical = enc.Key("WMode").Int64() == 1
		}
		f.cidWidths(v.Key("DescendantFonts").Index(0))
		return f
	}
	f.simpleEncoding(v, open)
	f.simpleWidths(v)
	return f
}

// simpleEncoding fills byCode: by the base encoding the font names, or else
// by the glyph names of an embedded Type 1 font's own encoding; then by the
// names a /Differences array gives some codes.
func (f *pdfFont) simpleEncoding(v pdf.Value, open func(pdf.Value) io.Reader) {
	enc := v.Key("Encoding")
	base := enc
	if enc.Kind() == pdf.Dict {
		base = enc.Key("BaseEncoding")
	}
	var names [256]string
	if table := map[string]*charmap.Charmap{"WinAnsiEncoding": charmap.Windows1252, "MacRomanEncoding": charmap.Macintosh}[base.Name()]; table != nil {
		for c := range f.byCode {
			f.byCode[c] = string(table.DecodeByte(byte(c)))
		}
	} else if program := v.Key("FontDescriptor").Key("FontFile"); program.Kind() == pdf.Stream {
		names = builtInNames(open(program))
	}
	diffs := enc.Key("Differences")
	for i, code := 0, 0; i < diffs.Len(); i++ {
		switch d := diffs.Index(i); d.Kind() {
		case pdf.Integer:
			code = int(d.Int64())
		case pdf.Name:
			if code >= 0 && code < 256 {
				names[code] = d.Name()
			}
			code++
		}
	}
	if names == [256]string{} {
		return
	}
	for c, text := range glyphText(&names) {
		if text != "" {
			f.byCode[c] = text
		}
	}
}

// unicodeName reads the glyph names that spell out their character:
// uni0041 (one or more four-digit groups) and u1F600.
func unicodeName(name string) (string, bool) {
	name, _, _ = strings.Cut(name, ".")
	var digits []string
	switch {
	case strings.HasPrefix(name, "uni") && len(name) > 3 && (len(name)-3)%4 == 0:
		for i := 3; i < len(name); i += 4 {
			digits = append(digits, name[i:i+4])
		}
	case strings.HasPrefix(name, "u") && len(name) >= 5 && len(name) <= 7:
		digits = []string{name[1:]}
	default:
		return "", false
	}
	var out []rune
	for _, d := range digits {
		n, err := strconv.ParseUint(d, 16, 32)
		if err != nil || n > 0x10FFFF || n >= 0xD800 && n < 0xE000 {
			return "", false
		}
		out = append(out, rune(n))
	}
	return string(out), true
}

func (f *pdfFont) simpleWidths(v pdf.Value) {
	if v.Key("Subtype").Name() == "Type3" {
		if m := v.Key("FontMatrix"); m.Index(0).Float64() != 0 {
			f.scale = math.Abs(m.Index(0).Float64())
		}
	}
	widths := v.Key("Widths")
	if widths.Kind() != pdf.Array {
		if strings.Contains(v.Key("BaseFont").Name(), "Courier") {
			f.missing = 600
		}
		return
	}
	f.missing = v.Key("FontDescriptor").Key("MissingWidth").Float64()
	first := int(v.Key("FirstChar").Float64())
	for i := 0; i < widths.Len() && first+i < 256; i++ {
		if first+i >= 0 {
			f.widths[uint32(first+i)] = widths.Index(i).Float64()
		}
	}
}

// cidWidths reads a CIDFont's widths: DW for every CID, and W's lists
// (c [w1 w2...]) and spans (c1 c2 w).
func (f *pdfFont) cidWidths(d pdf.Value) {
	f.missing = 1000
	if dw := d.Key("DW"); dw.Kind() == pdf.Integer || dw.Kind() == pdf.Real {
		f.missing = dw.Float64()
	}
	w := d.Key("W")
	for i := 0; i+1 < w.Len() && len(f.widths) < maxWidths && len(f.spans) < maxWidths; {
		first := uint32(max(w.Index(i).Float64(), 0))
		if list := w.Index(i + 1); list.Kind() == pdf.Array {
			for j := 0; j < list.Len() && len(f.widths) < maxWidths; j++ {
				f.widths[first+uint32(j)] = list.Index(j).Float64()
			}
			i += 2
			continue
		}
		last := uint32(max(w.Index(i+1).Float64(), 0))
		f.spans = append(f.spans, widthSpan{first, last, w.Index(i + 2).Float64()})
		i += 3
	}
}

// each reads the codes of a shown string.
func (f *pdfFont) each(raw string, do func(g glyph)) {
	for i := 0; i < len(raw); {
		code, n := f.code(raw[i:])
		do(glyph{text: f.text(raw[i:i+n], code, n), width: f.width(code, n) * f.scale, space: n == 1 && code == 32})
		i += n
	}
}

// code is the code s starts with, and its length in bytes.
func (f *pdfFont) code(s string) (uint32, int) {
	if !f.composite || len(s) < 2 {
		return uint32(s[0]), 1
	}
	if f.encoding != nil && len(f.encoding.space) > 0 {
		for n := 1; n <= 4 && n <= len(s); n++ {
			c := bigEndian(s[:n])
			for _, r := range f.encoding.space {
				if r.n == n && c >= r.lo && c <= r.hi {
					return c, n
				}
			}
		}
		return uint32(s[0]), 1
	}
	c := bigEndian(s[:2])
	if f.utf16 && c >= 0xD800 && c < 0xDC00 && len(s) >= 4 {
		return bigEndian(s[:4]), 4
	}
	return c, 2
}

func (f *pdfFont) text(raw string, code uint32, n int) string {
	if f.unicode != nil {
		if t, ok := f.unicode.textOf(code, n); ok {
			return t
		}
		// A simple font's codes, mapped as two bytes by some writers.
		if t, ok := f.unicode.textOf(code, 2); ok && !f.composite {
			return t
		}
	}
	switch {
	case !f.composite:
		return f.byCode[code&0xFF]
	case f.utf16:
		return utf16Text(raw)
	}
	return ""
}

// width is the glyph's width in glyph space. A UTF-16 font's codes name
// no CIDs without its CMap's data, so they take the default.
func (f *pdfFont) width(code uint32, n int) float64 {
	if f.utf16 {
		return f.missing
	}
	cid := code
	if f.encoding != nil {
		cid, _ = f.encoding.cidOf(code, n)
	}
	if w, ok := f.widths[cid]; ok {
		return w
	}
	for _, s := range f.spans {
		if cid >= s.lo && cid <= s.hi {
			return s.w
		}
	}
	return f.missing
}
