package extract

import (
	"io"
	"math"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/ledongthuc/pdf"
	"golang.org/x/text/unicode/norm"
)

// A page's content stream played as far as text goes: the graphics and
// text state that place each string, and where each one lands, which
// says where lines and words break.

// maxFormDepth bounds forms drawn inside forms: real ones nest a level or
// two.
const maxFormDepth = 8

// matrix is a PDF matrix [a b c d e f]: points are rows, x' = ax + cy + e.
type matrix [6]float64

var identity = matrix{1, 0, 0, 1, 0, 0}

func (m matrix) times(n matrix) matrix {
	return matrix{
		m[0]*n[0] + m[1]*n[2], m[0]*n[1] + m[1]*n[3],
		m[2]*n[0] + m[3]*n[2], m[2]*n[1] + m[3]*n[3],
		m[4]*n[0] + m[5]*n[2] + n[4], m[4]*n[1] + m[5]*n[3] + n[5],
	}
}

func (m matrix) apply(x, y float64) (float64, float64) {
	return m[0]*x + m[2]*y + m[4], m[1]*x + m[3]*y + m[5]
}

func translate(x, y float64) matrix { return matrix{1, 0, 0, 1, x, y} }

func matrixOf(args []operand) (matrix, bool) {
	var m matrix
	if len(args) != 6 {
		return m, false
	}
	for i, a := range args {
		m[i] = a.num
	}
	return m, true
}

// state is what q saves and Q restores: the transformation and the text
// parameters.
type state struct {
	ctm                                            matrix
	font                                           *pdfFont
	size, charSpace, wordSpace, hScale, lead, rise float64
}

type painter struct {
	doc     *pdfDoc
	out     *pdfLines
	st      state
	saved   []state
	tm, tlm matrix
	forms   int
}

// run plays one content stream, its fonts and forms named in resources.
func (p *painter) run(content io.Reader, resources pdf.Value) {
	fonts := map[string]*pdfFont{}
	l := newLexer(content, maxOperands)
	for {
		op, args, ok := l.next()
		if !ok {
			return
		}
		num := func(i int) float64 {
			if i < len(args) {
				return args[i].num
			}
			return 0
		}
		switch op {
		case "q":
			if len(p.saved) < 1024 {
				p.saved = append(p.saved, p.st)
			}
		case "Q":
			if n := len(p.saved); n > 0 {
				p.st, p.saved = p.saved[n-1], p.saved[:n-1]
			}
		case "cm":
			if m, ok := matrixOf(args); ok {
				p.st.ctm = m.times(p.st.ctm)
			}
		case "BT":
			p.tm, p.tlm = identity, identity
		case "Tc":
			p.st.charSpace = num(0)
		case "Tw":
			p.st.wordSpace = num(0)
		case "Tz":
			p.st.hScale = num(0) / 100
		case "TL":
			p.st.lead = num(0)
		case "Ts":
			p.st.rise = num(0)
		case "Tf":
			if len(args) == 2 {
				name := args[0].text
				if _, ok := fonts[name]; !ok {
					fonts[name] = p.doc.font(resources.Key("Font").Key(name))
				}
				p.st.font, p.st.size = fonts[name], args[1].num
			}
		case "Td", "TD":
			if op == "TD" {
				p.st.lead = -num(1)
			}
			p.tlm = translate(num(0), num(1)).times(p.tlm)
			p.tm = p.tlm
		case "Tm":
			if m, ok := matrixOf(args); ok {
				p.tm, p.tlm = m, m
			}
		case "T*":
			p.nextLine()
		case "Tj":
			p.showArgs(args)
		case "'":
			p.nextLine()
			p.showArgs(args)
		case "\"":
			if len(args) == 3 {
				p.st.wordSpace, p.st.charSpace = args[0].num, args[1].num
				p.nextLine()
				p.show(args[2].text)
			}
		case "TJ":
			if len(args) == 1 {
				p.showArray(args[0].items)
			}
		case "Do":
			if len(args) == 1 {
				p.form(resources.Key("XObject").Key(args[0].text), resources)
			}
		}
	}
}

func (p *painter) nextLine() {
	p.tlm = translate(0, -p.st.lead).times(p.tlm)
	p.tm = p.tlm
}

func (p *painter) showArgs(args []operand) {
	if len(args) == 1 && args[0].kind == 's' {
		p.show(args[0].text)
	}
}

// showArray is TJ: strings, and numbers that move the pen back (in
// thousandths of an em), which is how much text sets its spaces.
func (p *painter) showArray(items []operand) {
	for _, item := range items {
		switch item.kind {
		case 's':
			p.show(item.text)
		case 'n':
			by := -item.num / 1000 * p.st.size
			if p.font().vertical {
				p.tm = translate(0, by).times(p.tm)
			} else {
				p.tm = translate(by*p.st.hScale, 0).times(p.tm)
			}
		}
	}
}

func (p *painter) font() *pdfFont {
	if p.st.font == nil {
		p.st.font = plainFont()
	}
	return p.st.font
}

// show draws one string: its glyphs in turn, each moving the pen.
func (p *painter) show(raw string) {
	f := p.font()
	start := p.tm.times(p.st.ctm)
	var text strings.Builder
	f.each(raw, func(g glyph) {
		text.WriteString(g.text)
		spacing := p.st.charSpace
		if g.space {
			spacing += p.st.wordSpace
		}
		if f.vertical {
			p.tm = translate(0, -p.st.size+spacing).times(p.tm)
		} else {
			p.tm = translate((g.width*p.st.size+spacing)*p.st.hScale, 0).times(p.tm)
		}
	})
	end := p.tm.times(p.st.ctm)
	x, y := start.apply(0, p.st.rise)
	ex, ey := end.apply(0, p.st.rise)
	// The size the text is drawn at: the font size through both matrices.
	size := math.Abs(p.st.size) * math.Sqrt(math.Abs(start[0]*start[3]-start[1]*start[2]))
	if f.vertical {
		p.out.add(text.String(), -y, -ey, -x, size)
	} else {
		p.out.add(text.String(), x, ex, y, size)
	}
}

// form draws a form XObject: its own content, through its matrix, with
// its own resources or the page's.
func (p *painter) form(xobject, resources pdf.Value) {
	if xobject.Key("Subtype").Name() != "Form" || p.forms >= maxFormDepth {
		return
	}
	saved, depth := p.st, len(p.saved)
	if m, ok := matrixOfValue(xobject.Key("Matrix")); ok {
		p.st.ctm = m.times(p.st.ctm)
	}
	if own := xobject.Key("Resources"); own.Kind() == pdf.Dict {
		resources = own
	}
	p.forms++
	p.run(p.doc.stream(xobject), resources)
	p.forms--
	// A form's unmatched q's end with it.
	p.st, p.saved = saved, p.saved[:min(depth, len(p.saved))]
}

func matrixOfValue(v pdf.Value) (matrix, bool) {
	if v.Len() != 6 {
		return matrix{}, false
	}
	var m matrix
	for i := range m {
		m[i] = v.Index(i).Float64()
	}
	return m, true
}

// pdfLines writes shown strings as lines: a string off the last one's
// line starts a new line (a paragraph after a wider gap), and one that
// starts past where the last ended is a word of its own.
type pdfLines struct {
	b strings.Builder
	// along and across are where the last string ended, along its line and
	// across it; last is its last character.
	along, across float64
	last          rune
	started       bool
	// accent is a lone accent just drawn, held back: TeX draws é as ´ and
	// then e, back under it.
	accent rune
}

// combining is the combining mark of each spacing accent.
var combining = map[rune]rune{
	'`': 0x300, '´': 0x301, 'ˆ': 0x302, '˜': 0x303, '¯': 0x304, '˘': 0x306,
	'˙': 0x307, '¨': 0x308, '˚': 0x30A, '˝': 0x30B, 'ˇ': 0x30C,
}

// add writes text, which runs from along to end on the line at across, in
// letters size high.
func (w *pdfLines) add(text string, along, end, across, size float64) {
	text = strings.Map(printable, text)
	if text == "" {
		return
	}
	size = max(size, 1)
	first, n := utf8.DecodeRuneInString(text)
	held := w.accent
	w.accent = 0
	switch gap := math.Abs(across - w.across); {
	case held != 0 && gap <= 0.5*size && along < w.along-0.2*size && unicode.IsLetter(first):
		// The letter drawn back under the accent held.
		text = accented(first, held) + text[n:]
	case held != 0:
		w.b.WriteRune(held)
		fallthrough
	case w.started:
		switch {
		case gap > 1.5*size:
			w.b.WriteString("\n\n")
		case gap > 0.5*size:
			w.b.WriteString("\n")
		case along > w.along+wordGap(w.last, first)*size && w.last != ' ' && first != ' ':
			w.b.WriteByte(' ')
		}
	}
	w.along, w.across, w.started = end, across, true
	w.last, _ = utf8.DecodeLastRuneInString(text)
	// An accent ends the string TeX draws before the letter it accents
	// (or is a string of its own): it is held for that letter.
	if combining[w.last] != 0 {
		w.accent = w.last
		text = text[:len(text)-utf8.RuneLen(w.last)]
	}
	w.b.WriteString(text)
}

// String is the text written, an accent still held included.
func (w *pdfLines) String() string {
	if w.accent != 0 {
		w.b.WriteRune(w.accent)
		w.accent = 0
	}
	return w.b.String()
}

// accented is letter under accent, as one character where Unicode has
// one; TeX accents an i or a j without its dot.
func accented(letter, accent rune) string {
	switch letter {
	case 'ı':
		letter = 'i'
	case 'ȷ':
		letter = 'j'
	}
	return norm.NFC.String(string(letter) + string(combining[accent]))
}

// wordGap is the gap, in ems, that parts two words. Chinese and Japanese
// are set without spaces, and justifying a line spreads its characters:
// in a narrow column up to about a quarter of an em, where table cells
// can sit a third of an em apart. Korean spaces its words.
func wordGap(before, after rune) float64 {
	if unspaced(before) && unspaced(after) {
		return 0.3
	}
	return 0.1
}

// unspaced is a character of the scripts set without spaces: Han, kana,
// and their punctuation and full-width forms.
func unspaced(c rune) bool {
	return unicode.In(c, unicode.Han, unicode.Hiragana, unicode.Katakana) ||
		c >= 0x3000 && c <= 0x303F || c >= 0xFF00 && c <= 0xFFEF
}

// printable keeps a glyph's text and drops what a page can't show: control
// characters, and the replacement character of codes nothing maps. A Han
// radical reads as its ideograph.
func printable(c rune) rune {
	switch {
	case c == '\t' || c == '\n' || c == '\r':
		return ' '
	case c < 0x20 || c >= 0x7F && c < 0xA0 || c == unicode.ReplacementChar || c == 0xFEFF:
		return -1
	}
	return ideograph(c)
}
