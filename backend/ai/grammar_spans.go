package ai

import (
	"slices"
	"strings"
	"unicode"
)

// Where a grammar fix goes: the model's quote cut down to what it
// changes, and dropped when it changes nothing or writes text twice.

func isFullWidthForm(c rune) bool { return c == 0x3000 || c >= 0xFF01 && c <= 0xFF5E }

// narrow cuts a located fix down to what it changes, grown back to whole
// words in spaced scripts ("a honest" -> "an honest" becomes "a" -> "an").
// An insertion takes a neighbouring word so the span is never empty.
// False for a no-op.
func narrow(original, suggestion []rune) (offset, length int, replacement string, ok bool) {
	// A model's straight quote over the author's curly one isn't a change
	// worth an underline - but a half-width comma in Chinese is the fix.
	same := func(a, b rune) bool {
		return a == b || !isFullWidthForm(a) && !isFullWidthForm(b) && NormalizeChar(a) == NormalizeChar(b)
	}
	most := min(len(original), len(suggestion))
	p := 0
	for p < most && same(original[p], suggestion[p]) {
		p++
	}
	q := 0
	for q < most-p && same(original[len(original)-1-q], suggestion[len(suggestion)-1-q]) {
		q++
	}
	if p+q == len(original) && p+q == len(suggestion) {
		return 0, 0, "", false
	}
	mid := func() ([]rune, []rune) {
		return original[p : len(original)-q], suggestion[p : len(suggestion)-q]
	}
	firstIsWord := func(r []rune) bool { return len(r) > 0 && isWordChar(r[0]) }
	lastIsWord := func(r []rune) bool { return len(r) > 0 && isWordChar(r[len(r)-1]) }
	for {
		o, s := mid()
		if p > 0 && isWordChar(original[p-1]) && (firstIsWord(o) || firstIsWord(s)) {
			p--
		} else {
			break
		}
	}
	for {
		o, s := mid()
		if q > 0 && isWordChar(original[len(original)-q]) && (lastIsWord(o) || lastIsWord(s)) {
			q--
		} else {
			break
		}
	}
	spacedWord := func(c rune) bool { return !unicode.IsSpace(c) && !isUnspacedScript(c) }
	// Only spacing or punctuation changes next to a word in a spaced
	// script ("it's" -> "its"): the span takes the words on both sides, so
	// the card shows words, not a lone apostrophe or space.
	o, s := mid()
	if !slices.ContainsFunc(o, isWordChar) && !slices.ContainsFunc(s, isWordChar) &&
		(p > 0 && isWordChar(original[p-1]) || q > 0 && isWordChar(original[len(original)-q])) {
		for p > 0 && spacedWord(original[p-1]) {
			p--
		}
		for q > 0 && spacedWord(original[len(original)-q]) {
			q--
		}
	}
	if p+q == len(original) {
		switch {
		case q > 0 && spacedWord(original[p]):
			// The next word.
			for q > 0 && !unicode.IsSpace(original[len(original)-q]) {
				q--
			}
		case p > 0 && spacedWord(original[p-1]):
			// The previous word.
			for p > 0 && !unicode.IsSpace(original[p-1]) {
				p--
			}
		case p > 0:
			p--
		case q > 0:
			q--
		default:
			return 0, 0, "", false
		}
	}
	o, s = mid()
	if slices.Equal(o, s) {
		return 0, 0, "", false
	}
	return p, len(o), string(s), true
}

// wordsOf are the lower-cased words of the spaced-script text in chars.
func wordsOf(chars []rune) []string {
	var words []string
	for _, w := range strings.FieldsFunc(string(chars), func(c rune) bool { return !(isWordChar(c) || isApostrophe(c)) }) {
		if strings.ContainsFunc(w, isAlphabetic) {
			words = append(words, strings.ToLower(w))
		}
	}
	return words
}

type wordPair struct{ a, b string }

// writesTextTwice is whether applying replacement over start..end writes
// text twice: a new doubled word ("uns uns") or a pair of words, one
// longer than three letters, that the paragraph didn't already have twice.
func writesTextTwice(paragraph []rune, start, end int, replacement string) bool {
	corrected := append(append(slices.Clone(paragraph[:start]), []rune(replacement)...), paragraph[end:]...)
	old, fixed := wordsOf(paragraph), wordsOf(corrected)
	doubled := func(w []string) int {
		n := 0
		for i := 1; i < len(w); i++ {
			if w[i-1] == w[i] {
				n++
			}
		}
		return n
	}
	if doubled(fixed) > doubled(old) {
		return true
	}
	pairs := func(w []string) []wordPair {
		var out []wordPair
		for i := 1; i < len(w); i++ {
			if len([]rune(w[i-1])) > 3 || len([]rune(w[i])) > 3 {
				out = append(out, wordPair{w[i-1], w[i]})
			}
		}
		return out
	}
	oldPairs, newPairs := pairs(old), pairs(fixed)
	count := func(all []wordPair, p wordPair) int {
		n := 0
		for _, x := range all {
			if x == p {
				n++
			}
		}
		return n
	}
	written := wordsOf([]rune(replacement))
	return slices.ContainsFunc(newPairs, func(p wordPair) bool {
		return slices.Contains(written, p.a) && slices.Contains(written, p.b) &&
			count(newPairs, p) >= 2 && count(newPairs, p) > count(oldPairs, p)
	})
}

// ValidateIssues turns the model's issues into verified spans: located
// (dropped if missing or ambiguous), narrowed, and dropped when a no-op or
// writing text twice; of overlapping spans the first stays. Original is
// always the span's text.
func ValidateIssues(issues []GrammarIssue, paragraph string) []GrammarIssue {
	chars := []rune(paragraph)
	var kept []GrammarIssue
	for _, issue := range issues {
		var start, length int
		var replacement string
		if issue.Original != nil {
			quote := []rune(*issue.Original)
			at, ok := locate(chars, &issue, quote)
			if !ok {
				continue
			}
			offset, n, r, ok := narrow(chars[at:at+len(quote)], []rune(issue.Suggestion))
			if !ok {
				continue
			}
			start, length, replacement = at+offset, n, r
		} else {
			// Older shape (offsets, no quote): trust in-bounds offsets.
			if !(issue.Start < issue.End && issue.End <= len(chars)) || string(chars[issue.Start:issue.End]) == issue.Suggestion {
				continue
			}
			start, length, replacement = issue.Start, issue.End-issue.Start, issue.Suggestion
		}
		if writesTextTwice(chars, start, start+length, replacement) {
			continue
		}
		span := string(chars[start : start+length])
		issue.Start, issue.End, issue.Original, issue.Suggestion = start, start+length, &span, replacement
		kept = append(kept, issue)
	}
	slices.SortStableFunc(kept, func(a, b GrammarIssue) int { return a.Start - b.Start })
	out := []GrammarIssue{}
	lastEnd := 0
	for _, i := range kept {
		if i.Start >= lastEnd {
			out = append(out, i)
			lastEnd = i.End
		}
	}
	return out
}
