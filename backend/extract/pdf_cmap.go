package extract

import (
	"io"
	"sort"
	"unicode/utf16"
)

// CMaps: a font's ToUnicode map (codes to text) and a composite font's
// own encoding (codes to CIDs), with the byte lengths their codes take.

// cmapKey is a code and how many bytes it is written in.
type cmapKey struct {
	code uint32
	n    int
}

// cmapRange is the codes lo-hi of n bytes: as a codespace range; mapped
// to text counting up from dst, or listed in each; or to CIDs counting up
// from cid.
type cmapRange struct {
	lo, hi uint32
	n      int
	dst    string
	each   []string
	cid    uint32
}

type cmap struct {
	space     []cmapRange
	text      map[cmapKey]string
	textRange []cmapRange
	cids      map[cmapKey]uint32
	cidRange  []cmapRange
}

func parseCMap(r io.Reader) *cmap {
	m := &cmap{text: map[cmapKey]string{}, cids: map[cmapKey]uint32{}}
	l := newLexer(r, maxCMapOperands)
	for {
		op, args, ok := l.next()
		if !ok {
			break
		}
		// Each block's entries are pairs (bfchar, cidchar, codespacerange)
		// or triples (bfrange, cidrange) of operands.
		switch op {
		case "endcodespacerange":
			for i := 0; i+1 < len(args); i += 2 {
				if lo, hi, n, ok := codeSpan(args[i], args[i+1]); ok {
					m.space = append(m.space, cmapRange{lo: lo, hi: hi, n: n})
				}
			}
		case "endbfchar":
			for i := 0; i+1 < len(args); i += 2 {
				if code, n, ok := codeOf(args[i]); ok && args[i+1].kind == 's' {
					m.text[cmapKey{code, n}] = utf16Text(args[i+1].text)
				}
			}
		case "endbfrange":
			for i := 0; i+2 < len(args); i += 3 {
				lo, hi, n, ok := codeSpan(args[i], args[i+1])
				dst := args[i+2]
				if !ok || dst.kind != 's' && dst.kind != '[' {
					continue
				}
				r := cmapRange{lo: lo, hi: hi, n: n, dst: dst.text}
				for _, d := range dst.items {
					r.each = append(r.each, d.text)
				}
				m.textRange = append(m.textRange, r)
			}
		case "endcidchar":
			for i := 0; i+1 < len(args); i += 2 {
				if code, n, ok := codeOf(args[i]); ok && args[i+1].kind == 'n' {
					m.cids[cmapKey{code, n}] = uint32(max(args[i+1].num, 0))
				}
			}
		case "endcidrange":
			for i := 0; i+2 < len(args); i += 3 {
				if lo, hi, n, ok := codeSpan(args[i], args[i+1]); ok && args[i+2].kind == 'n' {
					m.cidRange = append(m.cidRange, cmapRange{lo: lo, hi: hi, n: n, cid: uint32(max(args[i+2].num, 0))})
				}
			}
		}
	}
	for _, ranges := range [][]cmapRange{m.textRange, m.cidRange} {
		sort.Slice(ranges, func(i, j int) bool {
			return ranges[i].n < ranges[j].n || ranges[i].n == ranges[j].n && ranges[i].lo < ranges[j].lo
		})
	}
	return m
}

// codeOf is a code written as a string of one to four bytes.
func codeOf(o operand) (uint32, int, bool) {
	if o.kind != 's' || len(o.text) < 1 || len(o.text) > 4 {
		return 0, 0, false
	}
	return bigEndian(o.text), len(o.text), true
}

func codeSpan(lo, hi operand) (uint32, uint32, int, bool) {
	a, n, ok1 := codeOf(lo)
	b, m, ok2 := codeOf(hi)
	if !ok1 || !ok2 || n != m || b < a {
		return 0, 0, 0, false
	}
	return a, b, n, true
}

func bigEndian(s string) uint32 {
	var v uint32
	for i := 0; i < len(s); i++ {
		v = v<<8 | uint32(s[i])
	}
	return v
}

// find is the range of ranges (sorted) holding code: the last one
// starting at or before it, or one of the few before that when ranges
// overlap.
func find(ranges []cmapRange, code uint32, n int) *cmapRange {
	i := sort.Search(len(ranges), func(i int) bool {
		return ranges[i].n > n || ranges[i].n == n && ranges[i].lo > code
	})
	for j := i - 1; j >= 0 && j >= i-8 && ranges[j].n == n; j-- {
		if code >= ranges[j].lo && code <= ranges[j].hi {
			return &ranges[j]
		}
	}
	return nil
}

// textOf is what the map says code (n bytes) reads as.
func (m *cmap) textOf(code uint32, n int) (string, bool) {
	if t, ok := m.text[cmapKey{code, n}]; ok {
		return t, true
	}
	r := find(m.textRange, code, n)
	switch {
	case r == nil:
		return "", false
	case r.each != nil:
		if i := int(code - r.lo); i < len(r.each) {
			return utf16Text(r.each[i]), true
		}
		return "", false
	}
	return utf16Text(countUp(r.dst, code-r.lo)), true
}

func (m *cmap) cidOf(code uint32, n int) (uint32, bool) {
	if c, ok := m.cids[cmapKey{code, n}]; ok {
		return c, true
	}
	if r := find(m.cidRange, code, n); r != nil {
		return r.cid + code - r.lo, true
	}
	return 0, false
}

// countUp is dst (big-endian bytes) plus by, carrying into the bytes
// before the last as a bfrange's text counts up.
func countUp(dst string, by uint32) string {
	out := []byte(dst)
	for i := len(out) - 1; i >= 0 && by > 0; i-- {
		sum := uint32(out[i]) + by&0xFF
		out[i] = byte(sum)
		by = by>>8 + sum>>8
	}
	return string(out)
}

// utf16Text is a CMap's text: UTF-16BE, as the format asks, or one byte
// a character when it is written in an odd number of bytes.
func utf16Text(s string) string {
	if len(s) >= 2 && s[0] == 0xFE && s[1] == 0xFF {
		s = s[2:]
	}
	if len(s)%2 == 1 {
		runes := make([]rune, len(s))
		for i := 0; i < len(s); i++ {
			runes[i] = rune(s[i])
		}
		return string(runes)
	}
	units := make([]uint16, len(s)/2)
	for i := range units {
		units[i] = uint16(s[2*i])<<8 | uint16(s[2*i+1])
	}
	return string(utf16.Decode(units))
}
