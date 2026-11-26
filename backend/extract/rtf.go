package extract

import (
	"bytes"
	"errors"
	"os"
	"strconv"
	"strings"
	"unicode/utf16"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/charmap"
)

// RTF to Markdown: paragraphs, emphasis, headings, list items, tables,
// links and footnotes. Groups inherit their parent's state and restore it
// on "}"; groups holding no reading text (font table, pictures, \*) are
// skipped.

// ErrUnreadable is a file that isn't what its extension says: an .rtf
// that doesn't start as RTF does.
var ErrUnreadable = errors.New("This file can't be read.")

// RtfMarkdown is the Markdown of an .rtf file.
func RtfMarkdown(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if !bytes.HasPrefix(data, []byte(`{\rtf`)) {
		return "", ErrUnreadable
	}
	return rtfToMarkdown(data), nil
}

// rtfDest is where a group's text goes.
type rtfDest uint8

const (
	destBody rtfDest = iota
	destFootnote
	// destFieldInstruction is a field's instruction (HYPERLINK
	// "https://..."), read for its URL.
	destFieldInstruction
	// destListText is the number or bullet Word and TextEdit write before
	// a list item.
	destListText
	// destStylesheet is read for which style numbers are headings.
	destStylesheet
)

// rtfPara is what \pard resets: the properties of the paragraph being
// written.
type rtfPara struct {
	heading   int // 1-6; 0 for none
	listLevel int
	// inTable: \intbl, the paragraph is (in) a table cell.
	inTable bool
	// style is \sN: the paragraph's style - or, inside the stylesheet, the
	// style an entry defines.
	style    int
	hasStyle bool
}

type rtfGroup struct {
	marks marks
	skip  bool
	// uc is how many characters stand in for each \u: skipped, as a reader
	// that understands \u must.
	uc   int
	dest rtfDest
	link string
	para rtfPara
}

// rtfSkipped are groups whose text is not the document's.
var rtfSkipped = map[string]bool{
	"fonttbl": true, "colortbl": true, "info": true, "pict": true, "object": true, "header": true,
	"headerl": true, "headerr": true, "headerf": true, "footer": true, "footerl": true,
	"footerr": true, "footerf": true, "listtable": true, "listoverridetable": true, "rsidtbl": true,
	"xmlnstbl": true, "generator": true, "themedata": true, "colorschememapping": true,
	"latentstyles": true, "datastore": true, "filetbl": true, "revtbl": true,
	"expandedcolortbl": true, "pgdsctbl": true, "shppict": true, "nonshppict": true,
	"mmathPr": true, "bkmkstart": true, "bkmkend": true, "author": true, "operator": true,
}

type rtfReader struct {
	stack    []rtfGroup
	encoding encoding.Encoding
	// bytes are \'hh bytes not yet decoded: a two-byte character (GBK,
	// Shift-JIS) arrives as two escapes, so they are decoded together.
	bytes []byte
	// skipChars are the characters still to skip after a \u.
	skipChars int
	// high is a UTF-16 high surrogate waiting for its pair; 0 for none.
	high      uint16
	out       blocks
	paragraph inline
	listText  string
	// styles are style numbers' lowercase names ("heading 1").
	styles    map[int]string
	styleName string
	// The table being read: its rows, the current row's cells, and the
	// paragraphs of the current cell.
	table       [][]string
	row         []string
	cell        string
	notes       []*inline
	instruction string
	fieldLink   string
}

func rtfToMarkdown(input []byte) string {
	r := &rtfReader{
		stack:    []rtfGroup{{uc: 1}},
		encoding: charmap.Windows1252,
		styles:   map[int]string{},
	}
	r.run(input)
	r.endParagraph()
	r.endTable()
	text := r.out.String()
	for i, note := range r.notes {
		text += "\n\n[^" + strconv.Itoa(i+1) + "]: " + note.render()
	}
	return strings.TrimSpace(text)
}

// group is the innermost group; the root one is never closed.
func (r *rtfReader) group() *rtfGroup { return &r.stack[len(r.stack)-1] }

func (r *rtfReader) run(input []byte) {
	for i := 0; i < len(input); {
		switch c := input[i]; c {
		case '{':
			r.flushBytes()
			r.stack = append(r.stack, *r.group())
			i++
		case '}':
			r.flushBytes()
			if len(r.stack) > 1 {
				closed := r.stack[len(r.stack)-1]
				r.stack = r.stack[:len(r.stack)-1]
				r.close(closed)
			}
			i++
		case '\\':
			i = r.control(input, i+1)
		case '\r', '\n':
			i++
		default:
			i++
			if r.takeSkippedChar() {
				continue
			}
			if c >= 0x80 {
				r.bytes = append(r.bytes, c)
			} else {
				r.flushBytes()
				r.emit(string(rune(c)))
			}
		}
	}
	r.flushBytes()
}

// control reads a control word or symbol starting at i (just past the
// backslash) and answers where the next token starts.
func (r *rtfReader) control(input []byte, i int) int {
	if i >= len(input) {
		return i
	}
	first := input[i]
	if first == '\'' {
		if i+3 <= len(input) {
			if b, err := strconv.ParseUint(string(input[i+1:i+3]), 16, 8); err == nil && !r.takeSkippedChar() {
				r.bytes = append(r.bytes, byte(b))
			}
		}
		return i + 3
	}
	r.flushBytes()
	if !isASCIILetter(rune(first)) {
		if r.takeSkippedChar() {
			return i + 1
		}
		switch first {
		case '\\', '{', '}':
			r.emit(string(rune(first)))
		case '~':
			r.emit("\u00a0")
		case '_':
			r.emit("\u2011")
		case '*':
			r.group().skip = true
		case '\n', '\r':
			r.endParagraph()
		}
		return i + 1
	}

	start := i
	for i < len(input) && isASCIILetter(rune(input[i])) {
		i++
	}
	word := string(input[start:i])
	numberStart := i
	if i < len(input) && input[i] == '-' {
		i++
	}
	for i < len(input) && input[i] >= '0' && input[i] <= '9' {
		i++
	}
	param, err := strconv.ParseInt(string(input[numberStart:i]), 10, 32)
	hasParam := err == nil
	if i < len(input) && input[i] == ' ' {
		i++
	}
	if word == "bin" {
		// Raw binary data of param bytes: not text, and it may hold braces
		// that would derail the groups.
		return i + max(int(param), 0)
	}
	r.word(word, int(param), hasParam)
	return i
}

func (r *rtfReader) word(word string, param int, hasParam bool) {
	on := !hasParam || param != 0
	g := r.group()
	switch word {
	case "par", "sect", "page":
		r.endParagraph()
	case "line":
		r.emit("\n")
	case "tab":
		r.emit("\t")
	case "cell":
		r.endCell()
	case "row":
		r.endRow()
	case "pard":
		g.para = rtfPara{}
	case "intbl":
		g.para.inTable = true
	case "s":
		g.para.style, g.para.hasStyle = param, hasParam
	case "plain":
		g.marks = marks{}
	case "b":
		g.marks.bold = on
	case "i":
		g.marks.italic = on
	case "strike", "striked":
		g.marks.strike = on
	case "outlinelevel":
		g.para.heading = 0
		if hasParam && param >= 0 && param < 6 {
			g.para.heading = param + 1
		}
	case "ilvl":
		g.para.listLevel = 0
		if hasParam && param >= 0 {
			g.para.listLevel = param
		}
	case "uc":
		g.uc = 1
		if hasParam && param >= 0 {
			g.uc = param
		}
	case "u":
		if hasParam {
			// \u is a signed 16-bit number: \u-3913 is U+F0B7.
			r.unicode(uint16(param))
		}
		r.skipChars = g.uc
	case "ansicpg":
		if e := codePage(param); hasParam && e != nil {
			r.encoding = e
		}
	case "mac":
		r.encoding = charmap.Macintosh
	case "footnote":
		// Straight into the paragraph: the group is usually {\*\footnote,
		// skipped until this word claims it.
		r.notes = append(r.notes, &inline{})
		r.paragraph.pushMarkdown("[^" + strconv.Itoa(len(r.notes)) + "]")
		g.dest, g.skip = destFootnote, false
	case "field":
		r.instruction, r.fieldLink = "", ""
	case "fldinst":
		g.dest, g.skip = destFieldInstruction, false
	case "fldrslt":
		g.link, g.skip = r.fieldLink, false
		r.fieldLink = ""
	case "stylesheet":
		g.dest, g.skip = destStylesheet, false
	case "listtext", "pntext":
		r.listText = ""
		g.dest, g.skip = destListText, false
	default:
		if text, ok := rtfSymbols[word]; ok {
			r.emit(text)
		} else if rtfSkipped[word] {
			g.skip = true
		}
	}
}

// rtfSymbols are the control words that stand for a character.
var rtfSymbols = map[string]string{
	"emdash": "\u2014", "endash": "\u2013", "bullet": "\u2022", "lquote": "\u2018",
	"rquote": "\u2019", "ldblquote": "\u201c", "rdblquote": "\u201d", "emspace": " ",
	"enspace": " ", "qmspace": " ",
}

func (r *rtfReader) close(closed rtfGroup) {
	parent := r.group().dest
	if closed.dest == destFieldInstruction && parent != destFieldInstruction {
		r.fieldLink, _ = hyperlink(r.instruction)
	}
	// {\s1 ... heading 1;}, one entry of the stylesheet.
	if closed.dest == destStylesheet && parent == destStylesheet {
		name := r.styleName
		r.styleName = ""
		if closed.para.hasStyle {
			r.styles[closed.para.style] = strings.ToLower(strings.TrimSpace(strings.TrimRight(strings.TrimSpace(name), ";")))
		}
	}
}

func (r *rtfReader) takeSkippedChar() bool {
	if r.skipChars > 0 {
		r.skipChars--
		return true
	}
	return false
}

func (r *rtfReader) unicode(unit uint16) {
	if unit >= 0xD800 && unit < 0xDC00 {
		r.high = unit
		return
	}
	units := []uint16{unit}
	if r.high != 0 {
		units = []uint16{r.high, unit}
		r.high = 0
	}
	r.emit(string(utf16.Decode(units)))
}
