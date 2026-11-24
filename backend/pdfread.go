package backend

import (
	"bytes"
	"compress/zlib"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// A reader of the PDFs the webviews print, enough to find each page and
// what it inherits (pdfnumbers.go): classic cross-reference tables and
// cross-reference streams, object streams, incremental updates.

var errPDF = errors.New("not a PDF Textus can read")

type pdfKind uint8

const (
	pdfNull pdfKind = iota
	pdfBool
	pdfNumber
	pdfString
	pdfName
	pdfArray
	pdfDict
	pdfRef
)

// pdfValue is one PDF object. Leaves keep their text as written, so a
// dictionary written back says what it said.
type pdfValue struct {
	kind     pdfKind
	raw      string     // a leaf: 12, (text), <0a>, /Name, true, null
	list     []pdfValue // an array's items
	keys     []string   // a dictionary's keys, decoded, in order
	vals     []pdfValue
	num, gen int // a reference
}

func (v pdfValue) get(key string) (pdfValue, bool) {
	for i, k := range v.keys {
		if k == key {
			return v.vals[i], true
		}
	}
	return pdfValue{}, false
}

// with is a copy of dictionary v with key set to val.
func (v pdfValue) with(key string, val pdfValue) pdfValue {
	out := pdfValue{kind: pdfDict, keys: append([]string(nil), v.keys...), vals: append([]pdfValue(nil), v.vals...)}
	for i, k := range out.keys {
		if k == key {
			out.vals[i] = val
			return out
		}
	}
	out.keys = append(out.keys, key)
	out.vals = append(out.vals, val)
	return out
}

// without is a copy of dictionary v without keys.
func (v pdfValue) without(keys ...string) pdfValue {
	out := pdfValue{kind: pdfDict}
	for i, k := range v.keys {
		if !containsString(keys, k) {
			out.keys = append(out.keys, k)
			out.vals = append(out.vals, v.vals[i])
		}
	}
	return out
}

func (v pdfValue) named(name string) bool { return v.kind == pdfName && pdfNameText(v.raw) == name }

func (v pdfValue) number() (float64, bool) {
	if v.kind != pdfNumber {
		return 0, false
	}
	f, err := strconv.ParseFloat(v.raw, 64)
	return f, err == nil
}

func (v pdfValue) integer() (int, bool) {
	f, ok := v.number()
	return int(f), ok && f == float64(int(f))
}

func pdfInt(n int) pdfValue     { return pdfValue{kind: pdfNumber, raw: strconv.Itoa(n)} }
func pdfRefTo(n int) pdfValue   { return pdfValue{kind: pdfRef, num: n} }
func pdfNameOf(n string) string { return "/" + n } // names Textus writes are plain

func (v pdfValue) String() string {
	var b strings.Builder
	v.write(&b)
	return b.String()
}

func (v pdfValue) write(b *strings.Builder) {
	switch v.kind {
	case pdfArray:
		b.WriteByte('[')
		for i, item := range v.list {
			if i > 0 {
				b.WriteByte(' ')
			}
			item.write(b)
		}
		b.WriteByte(']')
	case pdfDict:
		b.WriteString("<<")
		for i, k := range v.keys {
			b.WriteString(pdfNameToken(k))
			b.WriteByte(' ')
			v.vals[i].write(b)
			b.WriteByte(' ')
		}
		b.WriteString(">>")
	case pdfRef:
		fmt.Fprintf(b, "%d %d R", v.num, v.gen)
	default:
		if v.raw == "" {
			b.WriteString("null")
		} else {
			b.WriteString(v.raw)
		}
	}
}

// pdfNameText decodes a name token's #xx escapes, without its slash.
func pdfNameText(raw string) string {
	raw = strings.TrimPrefix(raw, "/")
	if !strings.Contains(raw, "#") {
		return raw
	}
	var b strings.Builder
	for i := 0; i < len(raw); i++ {
		if raw[i] == '#' && i+2 < len(raw) {
			if n, err := strconv.ParseUint(raw[i+1:i+3], 16, 8); err == nil {
				b.WriteByte(byte(n))
				i += 2
				continue
			}
		}
		b.WriteByte(raw[i])
	}
	return b.String()
}

// pdfNameToken writes a name back, escaping what a name can't hold.
func pdfNameToken(name string) string {
	var b strings.Builder
	b.WriteByte('/')
	for i := 0; i < len(name); i++ {
		c := name[i]
		if c <= ' ' || c >= 0x7f || c == '#' || pdfDelimiter(c) {
			fmt.Fprintf(&b, "#%02X", c)
		} else {
			b.WriteByte(c)
		}
	}
	return b.String()
}

func pdfSpace(c byte) bool {
	return c == 0 || c == '\t' || c == '\n' || c == '\f' || c == '\r' || c == ' '
}
func pdfDelimiter(c byte) bool { return strings.IndexByte("()<>[]{}/%", c) >= 0 }

type pdfParser struct {
	data []byte
	pos  int
}

func (p *pdfParser) skip() {
	for p.pos < len(p.data) {
		c := p.data[p.pos]
		switch {
		case pdfSpace(c):
			p.pos++
		case c == '%':
			for p.pos < len(p.data) && p.data[p.pos] != '\n' && p.data[p.pos] != '\r' {
				p.pos++
			}
		default:
			return
		}
	}
}

// word is a run of regular characters: a number or a keyword.
func (p *pdfParser) word() string {
	p.skip()
	start := p.pos
	for p.pos < len(p.data) && !pdfSpace(p.data[p.pos]) && !pdfDelimiter(p.data[p.pos]) {
		p.pos++
	}
	return string(p.data[start:p.pos])
}

func (p *pdfParser) expect(keyword string) error {
	if w := p.word(); w != keyword {
		return fmt.Errorf("%w: %q where %q was expected", errPDF, w, keyword)
	}
	return nil
}

func (p *pdfParser) int() (int, error) {
	n, err := strconv.Atoi(p.word())
	if err != nil {
		return 0, errPDF
	}
	return n, nil
}

func (p *pdfParser) value(depth int) (pdfValue, error) {
	if depth > 64 {
		return pdfValue{}, errPDF
	}
	p.skip()
	if p.pos >= len(p.data) {
		return pdfValue{}, errPDF
	}
	start := p.pos
	switch c := p.data[p.pos]; {
	case c == '/':
		p.pos++
		for p.pos < len(p.data) && !pdfSpace(p.data[p.pos]) && !pdfDelimiter(p.data[p.pos]) {
			p.pos++
		}
		return pdfValue{kind: pdfName, raw: string(p.data[start:p.pos])}, nil
	case c == '(':
		return p.literal()
	case c == '<' && p.pos+1 < len(p.data) && p.data[p.pos+1] == '<':
		p.pos += 2
		out := pdfValue{kind: pdfDict}
		for {
			p.skip()
			if bytes.HasPrefix(p.data[p.pos:], []byte(">>")) {
				p.pos += 2
				return out, nil
			}
			key, err := p.value(depth + 1)
			if err != nil || key.kind != pdfName {
				return pdfValue{}, errPDF
			}
			val, err := p.value(depth + 1)
			if err != nil {
				return pdfValue{}, err
			}
			out.keys = append(out.keys, pdfNameText(key.raw))
			out.vals = append(out.vals, val)
		}
	case c == '<':
		end := bytes.IndexByte(p.data[p.pos:], '>')
		if end < 0 {
			return pdfValue{}, errPDF
		}
		p.pos += end + 1
		return pdfValue{kind: pdfString, raw: string(p.data[start:p.pos])}, nil
	case c == '[':
		p.pos++
		out := pdfValue{kind: pdfArray}
		for {
			p.skip()
			if p.pos < len(p.data) && p.data[p.pos] == ']' {
				p.pos++
				return out, nil
			}
			item, err := p.value(depth + 1)
			if err != nil {
				return pdfValue{}, err
			}
			out.list = append(out.list, item)
		}
	}
	w := p.word()
	switch w {
	case "":
		return pdfValue{}, errPDF
	case "true", "false":
		return pdfValue{kind: pdfBool, raw: w}, nil
	case "null":
		return pdfValue{kind: pdfNull, raw: w}, nil
	}
	if _, err := strconv.ParseFloat(w, 64); err != nil {
		return pdfValue{}, fmt.Errorf("%w: %q", errPDF, w)
	}
	// "n g R" is a reference.
	if n, err := strconv.Atoi(w); err == nil {
		back := p.pos
		if g, err := strconv.Atoi(p.word()); err == nil && p.word() == "R" {
			return pdfValue{kind: pdfRef, num: n, gen: g}, nil
		}
		p.pos = back
	}
	return pdfValue{kind: pdfNumber, raw: w}, nil
}

func (p *pdfParser) literal() (pdfValue, error) {
	start := p.pos
	depth := 0
	for p.pos < len(p.data) {
		switch p.data[p.pos] {
		case '\\':
			p.pos++
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				p.pos++
				return pdfValue{kind: pdfString, raw: string(p.data[start:p.pos])}, nil
			}
		}
		p.pos++
	}
	return pdfValue{}, errPDF
}

// pdfDecode is a stream's data with its filters undone: Flate, with the
// PNG predictors cross-reference streams use.
func pdfDecode(dict pdfValue, data []byte) ([]byte, error) {
	filter, ok := dict.get("Filter")
	if !ok {
		return data, nil
	}
	if filter.kind == pdfArray {
		if len(filter.list) != 1 {
			return nil, errPDF
		}
		filter = filter.list[0]
	}
	if !filter.named("FlateDecode") {
		return nil, errPDF
	}
	r, err := zlib.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, errPDF
	}
	out, err := io.ReadAll(io.LimitReader(r, 256<<20))
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) {
		return nil, errPDF
	}
	params, _ := dict.get("DecodeParms")
	if params.kind == pdfArray && len(params.list) == 1 {
		params = params.list[0]
	}
	predictor, _ := params.get("Predictor")
	if n, _ := predictor.integer(); n >= 10 {
		columns := 1
		if c, ok := params.get("Columns"); ok {
			columns, _ = c.integer()
		}
		return unpredictPNG(out, columns)
	}
	return out, nil
}

// unpredictPNG undoes PNG's row filters, one byte a pixel.
func unpredictPNG(data []byte, columns int) ([]byte, error) {
	if columns <= 0 || len(data)%(columns+1) != 0 {
		return nil, errPDF
	}
	rows := len(data) / (columns + 1)
	out := make([]byte, 0, rows*columns)
	prev := make([]byte, columns)
	for r := range rows {
		row := data[r*(columns+1) : (r+1)*(columns+1)]
		filter, cur := row[0], append([]byte(nil), row[1:]...)
		for i := range cur {
			var left, upLeft byte
			if i > 0 {
				left, upLeft = cur[i-1], prev[i-1]
			}
			up := prev[i]
			switch filter {
			case 0:
			case 1:
				cur[i] += left
			case 2:
				cur[i] += up
			case 3:
				cur[i] += byte((int(left) + int(up)) / 2)
			case 4:
				cur[i] += paeth(left, up, upLeft)
			default:
				return nil, errPDF
			}
		}
		out = append(out, cur...)
		prev = cur
	}
	return out, nil
}

func paeth(a, b, c byte) byte {
	p := int(a) + int(b) - int(c)
	pa, pb, pc := abs(p-int(a)), abs(p-int(b)), abs(p-int(c))
	switch {
	case pa <= pb && pa <= pc:
		return a
	case pb <= pc:
		return b
	}
	return c
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
