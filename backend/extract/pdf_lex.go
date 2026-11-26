package extract

import (
	"bufio"
	"io"
	"strconv"
	"strings"
)

// The tokens of a content stream or a CMap: operands (numbers, strings,
// names, arrays, dictionaries) and the operators they are given to. The
// limits are a malformed or hostile stream's, never a real one's: nothing
// read here nests by recursion, and nothing grows without bound.

const (
	// maxOperands is more than any content operator takes; a pile larger
	// than that lost its operator, and is dropped.
	maxOperands = 64
	// maxCMapOperands: a CMap gives its mappings as one long run of
	// operands per block.
	maxCMapOperands = 1 << 17
	maxArrayItems   = 1 << 16
	maxStringBytes  = 1 << 20
	maxNesting      = 32
)

// operand is a number, a string's bytes, a name, or an array's items. A
// dictionary is read past (kind 'd'): no operator read here needs one.
type operand struct {
	kind  byte // 'n', 's', '/', '[', 'd', or 'k' for true, false and null
	num   float64
	text  string
	items []operand
}

type lexer struct {
	r    *bufio.Reader
	cap  int
	args []operand
	// open are the arrays and dictionaries being read, innermost last;
	// deeper ones only count their closes.
	open  []operand
	drown int
}

func newLexer(r io.Reader, maxArgs int) *lexer {
	return &lexer{r: bufio.NewReaderSize(r, 64<<10), cap: maxArgs}
}

// next is the next operator and its operands; false at the end of the
// stream.
func (l *lexer) next() (string, []operand, bool) {
	l.args = l.args[:0]
	for {
		o, word, ok := l.token()
		if !ok {
			return "", nil, false
		}
		switch word {
		case "":
			l.add(o)
		case "[", "<<":
			kind := byte('[')
			if word == "<<" {
				kind = 'd'
			}
			if l.drown > 0 || len(l.open) >= maxNesting {
				l.drown++
			} else {
				l.open = append(l.open, operand{kind: kind})
			}
		case "]", ">>":
			if l.drown > 0 {
				l.drown--
			} else if n := len(l.open); n > 0 {
				done := l.open[n-1]
				l.open = l.open[:n-1]
				l.add(done)
			}
		case "BI":
			l.skipImage()
			l.args = l.args[:0]
		default:
			// A word inside an array or a dictionary is no operator.
			if len(l.open) == 0 && l.drown == 0 && word != "{" && word != "}" {
				return word, l.args, true
			}
		}
	}
}

// add puts a finished operand into the array being read, or among the
// operands.
func (l *lexer) add(o operand) {
	if l.drown > 0 {
		return
	}
	if n := len(l.open); n > 0 {
		if top := &l.open[n-1]; top.kind == '[' && len(top.items) < maxArrayItems {
			top.items = append(top.items, o)
		}
		return
	}
	if len(l.args) >= l.cap {
		l.args = l.args[:0]
	}
	l.args = append(l.args, o)
}

func isPDFSpace(c byte) bool {
	return c == 0 || c == '\t' || c == '\n' || c == '\f' || c == '\r' || c == ' '
}

func isPDFDelimiter(c byte) bool { return strings.IndexByte("()<>[]{}/%", c) >= 0 }

// token is the next operand, or a word: an operator, true/false/null, or
// one of [ ] << >> { }.
func (l *lexer) token() (operand, string, bool) {
	for {
		c, err := l.r.ReadByte()
		if err != nil {
			return operand{}, "", false
		}
		switch {
		case isPDFSpace(c):
		case c == '%':
			for c != '\n' && c != '\r' && err == nil {
				c, err = l.r.ReadByte()
			}
		case c == '(':
			return operand{kind: 's', text: l.literal()}, "", true
		case c == '<' && l.peek('<'):
			return operand{}, "<<", true
		case c == '<':
			return operand{kind: 's', text: l.hex()}, "", true
		case c == '>' && l.peek('>'):
			return operand{}, ">>", true
		case c == '/':
			return operand{kind: '/', text: l.name()}, "", true
		case c == '[' || c == ']' || c == '{' || c == '}':
			return operand{}, string(c), true
		case c == ')' || c == '>':
			// Stray: skipped.
		default:
			word := l.word(c)
			switch {
			case word == "true" || word == "false" || word == "null":
				return operand{kind: 'k', text: word}, "", true
			case strings.IndexByte("+-.0123456789", word[0]) >= 0:
				return operand{kind: 'n', num: number(word)}, "", true
			}
			return operand{}, word, true
		}
	}
}

// peek consumes the next byte if it is c.
func (l *lexer) peek(c byte) bool {
	if next, err := l.r.Peek(1); err == nil && next[0] == c {
		l.r.ReadByte()
		return true
	}
	return false
}

// word is a run of regular characters starting with first.
func (l *lexer) word(first byte) string {
	var b strings.Builder
	b.WriteByte(first)
	for {
		c, err := l.r.ReadByte()
		if err != nil {
			break
		}
		if isPDFSpace(c) || isPDFDelimiter(c) {
			l.r.UnreadByte()
			break
		}
		if b.Len() < 256 {
			b.WriteByte(c)
		}
	}
	return b.String()
}

// number reads as PDF readers do: a malformed one ("--5", "1.2.3") is
// its valid start, or 0.
func number(word string) float64 {
	if n, err := strconv.ParseFloat(word, 64); err == nil {
		return n
	}
	end := 0
	for end < len(word) && strings.IndexByte("+-.0123456789", word[end]) >= 0 {
		end++
	}
	for ; end > 0; end-- {
		if n, err := strconv.ParseFloat(word[:end], 64); err == nil {
			return n
		}
	}
	return 0
}

func (l *lexer) name() string {
	var b strings.Builder
	for {
		c, err := l.r.ReadByte()
		if err != nil {
			break
		}
		if isPDFSpace(c) || isPDFDelimiter(c) {
			l.r.UnreadByte()
			break
		}
		if c == '#' {
			if hex, err := l.r.Peek(2); err == nil {
				if v, err := strconv.ParseUint(string(hex), 16, 8); err == nil {
					l.r.Discard(2)
					c = byte(v)
				}
			}
		}
		if b.Len() < 256 {
			b.WriteByte(c)
		}
	}
	return b.String()
}

// literal is a (string) after its opening parenthesis: balanced
// parentheses kept, escapes decoded, any line end read as \n.
func (l *lexer) literal() string {
	var b []byte
	put := func(c byte) {
		if len(b) < maxStringBytes {
			b = append(b, c)
		}
	}
	depth := 0
	for {
		c, err := l.r.ReadByte()
		if err != nil {
			return string(b)
		}
		switch c {
		case '(':
			depth++
		case ')':
			if depth == 0 {
				return string(b)
			}
			depth--
		case '\r':
			l.peek('\n')
			c = '\n'
		case '\\':
			if c, err = l.r.ReadByte(); err != nil {
				return string(b)
			}
			if e := strings.IndexByte("nrtbf", c); e >= 0 {
				c = "\n\r\t\b\f"[e]
			} else if c >= '0' && c <= '7' {
				v := int(c - '0')
				for range 2 {
					next, err := l.r.Peek(1)
					if err != nil || next[0] < '0' || next[0] > '7' {
						break
					}
					l.r.ReadByte()
					v = v*8 + int(next[0]-'0')
				}
				c = byte(v)
			} else if c == '\r' || c == '\n' {
				// A line end escaped: the string goes on unbroken.
				if c == '\r' {
					l.peek('\n')
				}
				continue
			}
		}
		put(c)
	}
}

// hex is a <hex string> after its <; an odd last digit is followed by 0.
func (l *lexer) hex() string {
	var b []byte
	var high byte
	half := false
	for {
		c, err := l.r.ReadByte()
		if err != nil || c == '>' {
			break
		}
		v, ok := hexDigit(c)
		if !ok {
			continue
		}
		if half {
			if len(b) < maxStringBytes {
				b = append(b, high<<4|v)
			}
		} else {
			high = v
		}
		half = !half
	}
	if half && len(b) < maxStringBytes {
		b = append(b, high<<4)
	}
	return string(b)
}

func hexDigit(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	}
	return 0, false
}

// skipImage passes over an inline image (BI <dictionary> ID <data> EI):
// its data runs to an EI between white space, which binary data rarely
// holds.
func (l *lexer) skipImage() {
	for {
		_, word, ok := l.token()
		if !ok {
			return
		}
		if word == "ID" {
			break
		}
	}
	// a and b are the two bytes before c.
	var a, b byte
	for n := 0; ; n++ {
		c, err := l.r.ReadByte()
		if err != nil {
			return
		}
		if n >= 2 && isPDFSpace(a) && b == 'E' && c == 'I' {
			if next, err := l.r.Peek(1); err != nil || isPDFSpace(next[0]) || isPDFDelimiter(next[0]) {
				return
			}
		}
		a, b = b, c
	}
}
