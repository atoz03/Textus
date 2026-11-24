package ai

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// The word or clause the cursor is in, and a completion cut to join it:
// the part the writer already typed taken off again.

// isUnspacedScript: scripts written without spaces between words. Hangul
// is not among them: Korean spaces its words.
func isUnspacedScript(r rune) bool {
	return r >= 0x0e00 && r <= 0x109f || r >= 0x1780 && r <= 0x17ff || r >= 0x2e80 && r <= 0x9fff ||
		r >= 0xf900 && r <= 0xfaff || r >= 0xff00 && r <= 0xffef || r >= 0x20000 && r <= 0x3134f
}

func isWordChar(r rune) bool { return isAlphanumeric(r) && !isUnspacedScript(r) }

// wordJoiners hold letters together in one written word: "don't",
// "C'est", "well-known", "3.5", "total_count".
const wordJoiners = "'’-._"

func isApostrophe(r rune) bool { return r == '\'' || r == '’' }

func isASCIIDigit(r rune) bool { return r >= '0' && r <= '9' }

func endsWithApostrophe(s string) bool {
	r, _ := utf8.DecodeLastRuneInString(s)
	return isApostrophe(r)
}

// clauseAtCursor is the Chinese or Japanese typed so far in the clause the
// cursor ends, at most its last ten characters, written out again so a
// broken-off word comes back whole; "" after punctuation or a space.
func clauseAtCursor(before string) string {
	start := len(before)
	for count := 0; start > 0 && count < 10; count++ {
		r, size := utf8.DecodeLastRuneInString(before[:start])
		if !isUnspacedScript(r) || !isAlphanumeric(r) {
			break
		}
		start -= size
	}
	return before[start:]
}

// char is one character of a text and where it starts.
type char struct {
	at int
	r  rune
}

func chars(s string) []char {
	out := make([]char, 0, len(s))
	for at, r := range s {
		out = append(out, char{at, r})
	}
	return out
}

// wordAtCursor is the word the cursor is in or right after, as typed
// ("docu", "don't", "2,300"), or a short elision ending in an apostrophe
// ("l'"); "" after a space, punctuation, a closing apostrophe, or in an
// unspaced script.
func wordAtCursor(before string) string {
	cs := chars(before)
	n := len(cs)
	if n == 0 {
		return ""
	}
	// The token's last letter or digit.
	var end int
	switch last := cs[n-1].r; {
	case isWordChar(last):
		end = n - 1
	case isApostrophe(last) && n >= 2 && isWordChar(cs[n-2].r):
		end = n - 2
	default:
		return ""
	}
	start := end
	for start > 0 {
		c := cs[start-1].r
		// A joiner counts only inside the word, between two of its letters
		// (a comma only between digits: "2,300").
		inside := start >= 2 && isWordChar(cs[start-2].r) &&
			(strings.ContainsRune(wordJoiners, c) || c == ',' && isASCIIDigit(cs[start-2].r) && isASCIIDigit(cs[start].r))
		if !isWordChar(c) && !inside {
			break
		}
		start--
	}
	if end != n-1 && end-start >= 3 {
		return ""
	}
	return before[cs[start].at:]
}

// echoedLen is how many bytes at the start of text repeat the end of
// before. In a spaced script a repeat starts where a word starts; after an
// unspaced script, two or more characters count.
func echoedLen(before, text string) int {
	cs := chars(before)
	n := len(cs)
	for k := max(n-120, 0); k < n; k++ {
		c := cs[k].r
		wordStart := isWordChar(c) && !(k > 0 && isWordChar(cs[k-1].r)) &&
			!(k >= 2 && isApostrophe(cs[k-1].r) && isWordChar(cs[k-2].r))
		unspaced := isUnspacedScript(c) && n-k >= 2
		if (wordStart || unspaced) && strings.HasPrefix(text, before[cs[k].at:]) {
			return len(before) - cs[k].at
		}
	}
	return 0
}

// dropDoubledLead is the reply without what would double up at the join:
// a clause mark the text already ends with ("，" after "，"), or an
// ellipsis it opened with.
func dropDoubledLead(before, text string) string {
	for _, ellipsis := range []string{"……", "...", "…"} {
		if rest, ok := strings.CutPrefix(text, ellipsis); ok {
			text = strings.TrimLeft(rest, " \t")
			break
		}
	}
	end := strings.TrimRight(before, " ")
	last, _ := utf8.DecodeLastRuneInString(end)
	first, size := utf8.DecodeRuneInString(text)
	if end != "" && text != "" && last == first && strings.ContainsRune("，,；;、：:", first) {
		return strings.TrimLeft(text[size:], " \t")
	}
	return text
}

// fitToCursor joins one continuation onto the text before the cursor,
// cutting off the typed letters of the word the model wrote out in full
// ("docu" + "document is" -> "ment is"). A model that left the word out
// gets the space it needs.
func fitToCursor(before, text string) string {
	// Leading spaces are the model's guess at the join, made here instead,
	// except at the start of a line, where they are code indentation.
	if !strings.HasSuffix(before, "\n") {
		text = dropDoubledLead(before, strings.TrimLeft(text, " \t"))
	}
	if clause := clauseAtCursor(before); clause != "" {
		if rest, ok := strings.CutPrefix(text, clause); ok {
			return rest
		}
	}
	if echoed := echoedLen(before, text); echoed > 0 {
		return text[echoed:]
	}
	if text == "" {
		return ""
	}
	first, _ := utf8.DecodeRuneInString(text)
	word := wordAtCursor(before)
	var needsSpace bool
	switch {
	// The model went on without writing out the word at the cursor. If the
	// text already uses that word or the reply's first word whole, it's a
	// new word; otherwise drop it: those were cut-off words, usually
	// continued wrongly.
	case word != "" && isWordChar(first) && !endsWithApostrophe(word):
		next := text[:len(text)-len(strings.TrimLeftFunc(text, isWordChar))]
		if !usedAsWholeWord(before[:len(before)-len(word)], word) && !usedAsWholeWord(before, next) {
			return ""
		}
		needsSpace = true
	// After a word, anything but what attaches to it ("," ")" "'s"
	// "-based") starts something new.
	case word != "":
		needsSpace = !endsWithApostrophe(word) && !unicode.IsSpace(first) && !isUnspacedScript(first) &&
			!strings.ContainsRune(",.;:!?)]}%…'’”-/", first)
	// After punctuation, a word or an opening quote or bracket does.
	default:
		last, _ := utf8.DecodeLastRuneInString(before)
		needsSpace = (isWordChar(first) || strings.ContainsRune("(\"“‘`", first)) &&
			before != "" && strings.ContainsRune(".,;:!?)]}\"'”’…", last)
	}
	if needsSpace {
		return " " + text
	}
	return text
}

// usedAsWholeWord is whether word occurs in text as a whole word, in any
// case: evidence that the same letters at the cursor are a complete word.
func usedAsWholeWord(text, word string) bool {
	text, word = strings.ToLower(text), strings.ToLower(word)
	if word == "" {
		return false
	}
	for from := 0; ; {
		i := strings.Index(text[from:], word)
		if i < 0 {
			return false
		}
		at := from + i
		prev, _ := utf8.DecodeLastRuneInString(text[:at])
		next, _ := utf8.DecodeRuneInString(text[at+len(word):])
		if (at == 0 || !isWordChar(prev)) && (at+len(word) == len(text) || !isWordChar(next)) {
			return true
		}
		from = at + len(word)
	}
}
