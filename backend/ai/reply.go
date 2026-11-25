package ai

import (
	"slices"
	"strconv"
	"strings"
	"unicode"

	"textus/backend/ai/rules"
)

// A completion reply made fit to show: wrapping and invented figures
// dropped, and cut to its first clause or sentence as the writer types.

// cleanReply removes what never belongs in a suggestion: stray tags, line
// breaks at either end outside code, backticks a plain-text context
// doesn't use. A reply still running over several lines outside code is
// dropped whole.
func cleanReply(before, after, text string, inCode bool) string {
	var b strings.Builder
	rest := text
	for {
		open := strings.IndexByte(rest, '<')
		if open < 0 {
			break
		}
		b.WriteString(rest[:open])
		candidate := rest[open:]
		end := strings.IndexByte(candidate, '>')
		if end < 0 || !isTagName(strings.TrimLeft(candidate[1:end], "/")) {
			b.WriteByte('<')
			rest = candidate[1:]
			continue
		}
		// A tag the document itself uses stays.
		if tag := candidate[:end+1]; strings.Contains(before, tag) || strings.Contains(after, tag) {
			b.WriteString(tag)
		}
		rest = candidate[end+1:]
	}
	b.WriteString(rest)
	out := b.String()
	if inCode {
		return out
	}
	out = strings.Trim(out, "\n\r")
	if strings.Contains(out, "\n") {
		// One exception: an answer the model stated twice, with talk in
		// between, is its answer.
		var said []string
		for _, l := range lines(out) {
			if l = strings.TrimSpace(l); l != "" {
				said = append(said, l)
			}
		}
		if len(said) < 2 || said[0] != said[len(said)-1] {
			return ""
		}
		out = said[0]
	}
	out = unwrapQuotes(out)
	if strings.Contains(before, "`") || strings.Contains(after, "`") {
		return out
	}
	return strings.ReplaceAll(out, "`", "")
}

// isTagName: an ASCII letter, then letters, digits, _ and -.
func isTagName(name string) bool {
	if name == "" || !(name[0] >= 'a' && name[0] <= 'z' || name[0] >= 'A' && name[0] <= 'Z') {
		return false
	}
	return !strings.ContainsFunc(name, func(r rune) bool {
		return !(r < 0x80 && (unicode.IsLetter(r) || unicode.IsDigit(r)) || r == '_' || r == '-')
	})
}

// figures are the numbers a text writes, "2,300" and "2300" as one.
func figures(s string) []string {
	var out []string
	for _, t := range strings.FieldsFunc(s, func(r rune) bool { return !isASCIIDigit(r) && r != ',' && r != '.' }) {
		if t = strings.ReplaceAll(strings.Trim(t, ",."), ",", ""); strings.ContainsFunc(t, isASCIIDigit) {
			out = append(out, t)
		}
	}
	return out
}

// inventsFigures is whether a suggestion carries a figure (a time, a
// count, a status code) the text around the cursor doesn't. The next
// number in a counted sequence is fine.
func inventsFigures(before, after, text string) bool {
	known := figures(before + "\n" + after)
	next, counted := uint64(0), false
	for _, k := range known {
		if n, err := strconv.ParseUint(k, 10, 64); err == nil && (!counted || n+1 > next) {
			next, counted = n+1, true
		}
	}
	for _, f := range figures(text) {
		if !slices.Contains(known, f) && !(counted && f == strconv.FormatUint(next, 10)) {
			return true
		}
	}
	return false
}

// unwrapQuotes takes off ASCII quotes the model wrapped a suggestion in
// whole, around Chinese or Japanese text that never quotes with them.
func unwrapQuotes(text string) string {
	for _, q := range []string{"'", `"`} {
		if len(text) >= 2 && strings.HasPrefix(text, q) && strings.HasSuffix(text, q) {
			if inner := text[1 : len(text)-1]; !strings.Contains(inner, q) && strings.ContainsFunc(inner, isUnspacedScript) {
				return inner
			}
		}
	}
	return text
}

// firstClause is text up to the end of its first clause once it has a few
// words, and never more than a short line: past that the model went its
// own way.
func firstClause(text string) string {
	const cjkMin, cjkMax, wordsMin, wordsMax = 6, 24, 3, 15
	cs := []rune(text)
	unspaced := 0
	for _, r := range cs {
		if isUnspacedScript(r) {
			unspaced++
		}
	}
	cjk := unspaced*2 > len(cs)
	letters, words, inWord := 0, 0, false
	for i, r := range cs {
		switch {
		case cjk:
			if isAlphanumeric(r) {
				letters++
			}
		case isAlphanumeric(r):
			if !inWord {
				words++
			}
			inWord = true
		default:
			inWord = false
		}
		enough, tooLong := words >= wordsMin, words > wordsMax
		if cjk {
			enough, tooLong = letters >= cjkMin, letters > cjkMax
		}
		// The clause and its mark: the writer goes on from there.
		if enough && strings.ContainsRune("，,；;：:", r) {
			return string(cs[:i+1])
		}
		if tooLong {
			cut := string(cs[:i])
			if cjk {
				return cut
			}
			// Before the word it was in, for the spaced scripts.
			return strings.TrimRightFunc(strings.TrimRightFunc(cut, isAlphanumeric), unicode.IsSpace)
		}
	}
	return text
}

// firstSentence is text up to the end of its first sentence. A full stop
// counts before a space or the end, not inside "3.5"; closing quotes after
// it stay with it.
func firstSentence(text string) string {
	const closers = "”’\"')）」』]"
	cs := []rune(text)
	for i, r := range cs {
		if i == 0 || !strings.ContainsRune("。！？….!?", r) {
			continue
		}
		end := i + 1
		for end < len(cs) && strings.ContainsRune(closers, cs[end]) {
			end++
		}
		// A full stop of the spaced scripts ends a sentence before a space
		// or the end only: "3.5", "e.g." mid-sentence go on.
		spaced := r == '.' || r == '!' || r == '?'
		if !spaced || end == len(cs) || unicode.IsSpace(cs[end]) {
			return string(cs[:end])
		}
	}
	return text
}

// finishCompletion is each candidate of the reply cleaned and fitted to
// the cursor, the separator lines kept, each later candidate opening with
// exactly one line break.
func finishCompletion(before, after, raw string, inCode bool) string {
	parts := strings.Split(raw, CandidateSeparator)
	out := make([]string, len(parts))
	for i, part := range parts {
		lead := ""
		if i > 0 {
			lead, part = "\n", strings.TrimLeft(part, "\n")
		}
		body := strings.TrimRight(part, "\n")
		tail := ""
		if i+1 != len(parts) || inCode {
			tail = part[len(body):]
		}
		body = cleanReply(before, after, body, inCode)
		switch {
		case strings.TrimSpace(body) == "" || !inCode && inventsFigures(before, after, body):
			out[i] = lead + tail
		case inCode:
			out[i] = lead + fitToCursor(before, body) + tail
		default:
			// The conventions the text before the cursor already follows.
			out[i] = lead + rules.Completion(before, fitToCursor(before, body)) + tail
		}
	}
	return strings.Join(out, CandidateSeparator)
}

// FinishSuggestion is the model's reply as the editor gets it: each
// candidate cleaned and fitted to the cursor; as the writer types, only
// its first clause.
func FinishSuggestion(before, after, raw string, hints CompletionHints) string {
	inCode := deref(hints.Block) == CodeBlockHint
	text := finishCompletion(before, after, raw, inCode)
	asYouType := deref(hints.Length) != "long" && (hints.Candidates == nil || *hints.Candidates <= 1)
	if !asYouType || inCode {
		return text
	}
	return firstClause(firstSentence(text))
}
