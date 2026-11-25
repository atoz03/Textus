package rules

import (
	"slices"
	"strings"
	"unicode"
)

// The text a rule looks at: one block of plain text with char offsets and a
// mask of spans no rule may touch (URLs, e-mail addresses, paths, inline
// code and math, and their placeholders).

// Placeholder stands in for an inline code span or inline math inside a
// block's text, so rules see a boundary there and never its contents.
const Placeholder = '\uFFFC'

func isHan(c rune) bool {
	return c >= 0x4E00 && c <= 0x9FFF || c >= 0x3400 && c <= 0x4DBF || c >= 0xF900 && c <= 0xFAFF ||
		c >= 0x20000 && c <= 0x2EBEF || c >= 0x2F800 && c <= 0x2FA1F || c == 0x3007
}

func isKana(c rune) bool {
	return c >= 0x3041 && c <= 0x309F || c >= 0x30A0 && c <= 0x30FF || c >= 0x31F0 && c <= 0x31FF
}

func isHalfwidthKana(c rune) bool { return c >= 0xFF65 && c <= 0xFF9F }

// IsHangul is a Hangul syllable, jamo or compatibility jamo.
func IsHangul(c rune) bool {
	return c >= 0xAC00 && c <= 0xD7AF || c >= 0x1100 && c <= 0x11FF || c >= 0x3130 && c <= 0x318F
}

// IsCJK is a Chinese or Japanese letter: an ideograph or kana.
func IsCJK(c rune) bool { return isHan(c) || isKana(c) }

// isFullwidthPunct is full-width punctuation proper: the CJK symbols block
// and the full-width forms of ASCII punctuation (，。、；：？！（）《》「」…).
func isFullwidthPunct(c rune) bool {
	return c >= 0x3001 && c <= 0x3003 || c >= 0x3008 && c <= 0x3011 || c >= 0x3014 && c <= 0x301F ||
		c >= 0xFF01 && c <= 0xFF0F || c >= 0xFF1A && c <= 0xFF20 || c >= 0xFF3B && c <= 0xFF40 ||
		c >= 0xFF5B && c <= 0xFF64
}

// isCJKText is a character of CJK running text: a CJK letter or a
// full-width mark.
func isCJKText(c rune) bool { return IsCJK(c) || isFullwidthPunct(c) }

func isASCIIAlpha(c rune) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }
func isASCIIDigit(c rune) bool { return c >= '0' && c <= '9' }
func isASCIIAlnum(c rune) bool { return isASCIIAlpha(c) || isASCIIDigit(c) }

func isLatinLetter(c rune) bool {
	return isASCIIAlpha(c) || c >= 0xC0 && c <= 0xD6 || c >= 0xD8 && c <= 0xF6 || c >= 0xF8 && c <= 0x24F ||
		c >= 0x1E00 && c <= 0x1EFF
}

func isCyrillic(c rune) bool { return c >= 0x0400 && c <= 0x04FF }

// isWordChar is a letter or digit of a script that spaces its words (Latin,
// Cyrillic, Greek, Hangul) - what "a word" means to the word rules.
func isWordChar(c rune) bool { return IsAlphanumeric(c) && !IsCJK(c) || c == '_' }

// isLatinOrDigit is a Latin letter or digit - the "half-width" side of
// CJK-Latin spacing.
func isLatinOrDigit(c rune) bool { return isASCIIAlnum(c) || isLatinLetter(c) }

// isUpper and isLower are Rust's char::is_uppercase / is_lowercase.
func isUpper(c rune) bool { return unicode.IsUpper(c) || unicode.Is(unicode.Other_Uppercase, c) }
func isLower(c rune) bool { return unicode.IsLower(c) || unicode.Is(unicode.Other_Lowercase, c) }

// isSpace is Rust's char::is_whitespace.
func isSpace(c rune) bool { return unicode.IsSpace(c) }

// isNumeric is Rust's char::is_numeric.
func isNumeric(c rune) bool { return unicode.IsNumber(c) }

var (
	urlRe   = MustRe(`(?i)\b(?:https?|ftp|file|mailto)://[^\s<>\x22'“”‘’「」『』（）()\[\]{}，。、；！？\p{Han}\p{Hiragana}\p{Katakana}\p{Hangul}]+|\bwww\.[a-z0-9-]+\.[^\s<>\x22'“”（）()\[\]{}，。、；！？\p{Han}\p{Hiragana}\p{Katakana}\p{Hangul}]+`)
	emailRe = MustRe(`[A-Za-z0-9_.+-]+@[A-Za-z0-9-]+(?:\.[A-Za-z0-9-]+)+`)
	codeRe  = MustRe("`[^`\\n]+`")
	// A path: one with a root (`/`, `~/`, `./`, `C:\`), several separators,
	// or a file name with an extension - so "and/or" and "he/she" stay words.
	pathRe = MustRe(`(?:[A-Za-z]:\\|~/|\.{1,2}/|/)[A-Za-z0-9_.@~+-]+(?:[/\\][A-Za-z0-9_.@~+-]*)*|[A-Za-z0-9_.@~+-]+(?:[/\\][A-Za-z0-9_@~+-]+){2,}[/\\]?[A-Za-z0-9_.@~+-]*|[A-Za-z0-9_@~+-]+(?:[/\\][A-Za-z0-9_@~+-]+)*[/\\][A-Za-z0-9_@~+-]+\.[A-Za-z0-9]{1,8}\b`)
	mathRe = MustRe(`\$[^\s$\d][^$\n]*?\$`)
)

// Text is one block of plain text as the rules see it.
type Text struct {
	S         string
	Chars     []rune
	Protected []bool
}

// NewText reads s, marking the spans no rule may touch.
func NewText(s string) *Text {
	t := &Text{S: s, Chars: []rune(s)}
	t.Protected = make([]bool, len(t.Chars))
	for _, re := range []*Re{urlRe, emailRe, codeRe, pathRe} {
		// A root slash starts a path only at the start of a token: the "/or"
		// of "and/or" and the "/2" of "1/2" are not paths.
		for _, m := range re.FindAll(t.Chars) {
			a, b := m[0], m[1]
			if c := t.Chars[a]; (c == '/' || c == '\\') && a > 0 && IsAlphanumeric(t.Chars[a-1]) {
				continue
			}
			// A sentence's own full stop or comma after a URL isn't part of it.
			for b > a && strings.ContainsRune(".,;:!?)", t.Chars[b-1]) {
				b--
			}
			t.protect(a, b)
		}
	}
	for _, m := range mathRe.FindAll(t.Chars) {
		if c, ok := t.At(m[1]); !ok || !isASCIIDigit(c) {
			t.protect(m[0], m[1])
		}
	}
	for i, c := range t.Chars {
		if c == Placeholder {
			t.Protected[i] = true
		}
	}
	return t
}

// Len is the text's length in chars.
func (t *Text) Len() int { return len(t.Chars) }

func (t *Text) protect(a, b int) {
	for i := a; i < b; i++ {
		t.Protected[i] = true
	}
}

// find is every match of re, as char ranges.
func (t *Text) find(re *Re) [][2]int { return re.FindAll(t.Chars) }

// captures is every match of re, as its groups' char ranges.
func (t *Text) captures(re *Re) [][]Span {
	var out [][]Span
	re.each(t.Chars, func(g []Span) { out = append(out, g) })
	return out
}

// At is the char at i, false past the end.
func (t *Text) At(i int) (rune, bool) {
	if i < 0 || i >= len(t.Chars) {
		return 0, false
	}
	return t.Chars[i], true
}

// before is the char before index i, false at the start.
func (t *Text) before(i int) (rune, bool) { return t.At(i - 1) }

// atIs and beforeIs are whether there is a char there and it passes f.
func (t *Text) atIs(i int, f func(rune) bool) bool {
	c, ok := t.At(i)
	return ok && f(c)
}

func (t *Text) beforeIs(i int, f func(rune) bool) bool { return t.atIs(i-1, f) }

// atNoneOr and beforeNoneOr are true where there is no char, or it passes f.
func (t *Text) atNoneOr(i int, f func(rune) bool) bool {
	c, ok := t.At(i)
	return !ok || f(c)
}

func (t *Text) beforeNoneOr(i int, f func(rune) bool) bool { return t.atNoneOr(i-1, f) }

// atEq and beforeEq are whether the char there is c.
func (t *Text) atEq(i int, c rune) bool {
	d, ok := t.At(i)
	return ok && d == c
}

func (t *Text) beforeEq(i int, c rune) bool { return t.atEq(i-1, c) }

// slice is the chars a..b as a string.
func (t *Text) slice(a, b int) string { return string(t.Chars[a:b]) }

// guarded is whether any char in a..b is protected.
func (t *Text) guarded(a, b int) bool {
	a, b = min(a, t.Len()), min(b, t.Len())
	return slices.Contains(t.Protected[a:b], true)
}

// inQuotes is whether a..b sits inside a quotation or dialogue: an opening
// quote before it on the same line with no closing one in between, and a
// closing one after it.
func (t *Text) inQuotes(a, b int) bool {
	pairs := [][2]rune{{'“', '”'}, {'「', '」'}, {'『', '』'}, {'«', '»'}, {'„', '“'}, {'‘', '’'}, {'»', '«'}}
	after := t.Chars[min(b, t.Len()):]
	for _, p := range pairs {
		open, close := p[0], p[1]
		i := slices.IndexFunc(reversed(t.Chars[:a]), func(c rune) bool { return c == open || c == '\n' })
		if i < 0 {
			continue
		}
		i = a - 1 - i
		opened := t.Chars[i] == open && !slices.Contains(t.Chars[i+1:a], close)
		if opened && slices.Contains(after, close) {
			return true
		}
	}
	straight := 0
	for _, c := range t.Chars[:a] {
		if c == '"' {
			straight++
		}
	}
	return straight%2 == 1 && slices.Contains(after, '"')
}

// reversed is a reversed copy of cs.
func reversed(cs []rune) []rune {
	out := slices.Clone(cs)
	slices.Reverse(out)
	return out
}

// sentenceStart is whether i starts a sentence: nothing but spaces, opening
// quotes or brackets since the start or since a sentence-ending mark.
func (t *Text) sentenceStart(i int) bool {
	for j := i; j > 0; j-- {
		c := t.Chars[j-1]
		if isSpace(c) || strings.ContainsRune("\"“«„([¿¡—–-'‘", c) {
			continue
		}
		return strings.ContainsRune(".!?…:。！？", c)
	}
	return true
}

// wordAt is the word (letters, digits, apostrophes inside) that starts at a.
func (t *Text) wordAt(a int) (int, int) {
	b := a
	for b < t.Len() && (isWordChar(t.Chars[b]) ||
		(t.Chars[b] == '\'' || t.Chars[b] == '’') && b+1 < t.Len() && isWordChar(t.Chars[b+1]) && b > a) {
		b++
	}
	return a, b
}

// words are the text's words as (start, end) char ranges.
func (t *Text) words() [][2]int {
	var out [][2]int
	for i := 0; i < t.Len(); {
		if isWordChar(t.Chars[i]) && !t.beforeIs(i, isWordChar) {
			a, b := t.wordAt(i)
			out = append(out, [2]int{a, b})
			i = max(b, i+1)
		} else {
			i++
		}
	}
	return out
}

// foldEq is Rust's a.to_lowercase().eq(b.to_lowercase()) for one char.
func foldEq(a, b rune) bool { return a == b || unicode.ToLower(a) == unicode.ToLower(b) }

// findWord is every occurrence of word in t as a whole word (no word
// character on either side), matched exactly or, with ci, ignoring case.
func findWord(t *Text, word string, ci bool) [][2]int {
	w := []rune(word)
	n := len(w)
	var out [][2]int
	if n == 0 || n > t.Len() {
		return out
	}
	needLeft, needRight := isWordChar(w[0]), isWordChar(w[n-1])
	for i := 0; i <= t.Len()-n; i++ {
		match := true
		for k := 0; k < n; k++ {
			if a, b := t.Chars[i+k], w[k]; a != b && !(ci && foldEq(a, b)) {
				match = false
				break
			}
		}
		if match && !(needLeft && t.beforeIs(i, isWordChar)) && !(needRight && t.atIs(i+n, isWordChar)) {
			out = append(out, [2]int{i, i + n})
		}
	}
	return out
}

// findStr is every occurrence of needle in t, as a substring.
func findStr(t *Text, needle string) [][2]int {
	w := []rune(needle)
	n := len(w)
	if n == 0 || n > t.Len() {
		return nil
	}
	var out [][2]int
	for i := 0; i <= t.Len()-n; i++ {
		if slices.Equal(t.Chars[i:i+n], w) {
			out = append(out, [2]int{i, i + n})
		}
	}
	return out
}

// matchCase is replacement cased like original: Capitalised or ALL CAPS.
func matchCase(original, replacement string) string {
	var letters []rune
	for _, c := range original {
		if IsAlphabetic(c) {
			letters = append(letters, c)
		}
	}
	if len(letters) > 1 && !slices.ContainsFunc(letters, func(c rune) bool { return !isUpper(c) }) {
		return strings.ToUpper(replacement)
	}
	if len(letters) > 0 && isUpper(letters[0]) {
		rs := []rune(replacement)
		if len(rs) == 0 {
			return ""
		}
		return strings.ToUpper(string(rs[0])) + string(rs[1:])
	}
	return replacement
}

// toLower is Rust's str::to_lowercase.
func toLower(s string) string { return strings.ToLower(s) }

// firstRune is s's first char, false for "".
func firstRune(s string) (rune, bool) {
	for _, c := range s {
		return c, true
	}
	return 0, false
}

// lastRune is s's last char, false for "".
func lastRune(s string) (rune, bool) {
	rs := []rune(s)
	if len(rs) == 0 {
		return 0, false
	}
	return rs[len(rs)-1], true
}

// runeCount is s's length in chars.
func runeCount(s string) int { return len([]rune(s)) }

// trimEnd is Rust's str::trim_end: s without trailing white space.
func trimEnd(s string) string { return strings.TrimRightFunc(s, unicode.IsSpace) }

// trimStart is Rust's str::trim_start.
func trimStart(s string) string { return strings.TrimLeftFunc(s, unicode.IsSpace) }
