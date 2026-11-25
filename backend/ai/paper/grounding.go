package paper

import (
	"fmt"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"textus/backend/ai"
	"textus/backend/ai/rules"
	"textus/backend/ai/web"
)

// Whether a note says what its sources said: its quote found in their
// words, its figures among theirs, and the passage it rests on kept beside
// it. The words are what the sources showed the researcher (web.Plan's
// Seen); a source never shown (one the provider's own search cited, an
// address a plugin gave) can't be checked, and isn't.

const (
	// excerptChars is the most of a source's words kept beside a note.
	excerptChars = 400
	// excerptMinScore is the least a passage must share with a note to be
	// its excerpt: a figure counts for figureWeight, a pair of letters or
	// characters for one.
	excerptMinScore = 12
	figureWeight    = 8
)

// plain is text as letters and digits only, lowercased: what a quote is
// compared by, so spacing, punctuation and quote marks don't matter.
func plain(text string) string {
	var out strings.Builder
	for _, c := range text {
		if rules.IsAlphanumeric(c) {
			out.WriteString(strings.ToLower(string(c)))
		}
	}
	return out.String()
}

// hasQuote reports whether quote is in words, spacing and punctuation
// aside.
func hasQuote(words, quote string) bool {
	quote = plain(quote)
	return quote == "" || strings.Contains(plain(words), quote)
}

var (
	// number is a number as written, with what scales it: "1,200" with its
	// thousands, "3.5", "3,5" (a decimal comma), "2024", "1.2万", "3 亿",
	// "4.5 million".
	number = regexp.MustCompile(`(\d{1,3}(?:,\d{3})+(?:\.\d+)?|\d+,\d{1,2}\b|\d+(?:\.\d+)?)(?:[` + space + `]?(万|亿|(?i:thousand|million|billion)\b))?`)
	// citation is a citation, `[3]`, whose number is no figure.
	citation = regexp.MustCompile(`\[\d{1,3}\](?:\([^)` + space + `]*\))?`)
	// trailingCitations are citations right after a sentence's end -
	// "。[1](…)", ". [2][3]" - which belong to the sentence before them.
	trailingCitations = regexp.MustCompile(`^(?:[ \t]*\[\d{1,3}\](?:\([^)` + space + `]*\))?)+`)
)

// figureValue is a number's value as one form: "1,200" and "1200" alike,
// "3,5" and "3.50" as "3.5", "1.2万" as "12000". False for what isn't a
// number.
func figureValue(digits, scale string) (string, bool) {
	_, after, hasComma := strings.Cut(digits, ",")
	decimalComma := !strings.Contains(digits, ".") && hasComma && len(strings.SplitN(after, ",", 2)[0]) < 3
	plainDigits := strings.ReplaceAll(digits, ",", "")
	if decimalComma {
		plainDigits = strings.ReplaceAll(digits, ",", ".")
	}
	times := uint64(1)
	switch strings.ToLower(scale) {
	case "万":
		times = 10_000
	case "亿":
		times = 100_000_000
	case "thousand":
		times = 1_000
	case "million":
		times = 1_000_000
	case "billion":
		times = 1_000_000_000
	}
	value, err := strconv.ParseFloat(plainDigits, 64)
	if err != nil {
		return "", false
	}
	scaled := value * float64(times)
	var text string
	switch {
	case scaled == math.Trunc(scaled) && scaled < 1e18:
		text = strconv.FormatUint(uint64(scaled), 10)
	case times == 1:
		text = plainDigits
	default:
		text = strconv.FormatFloat(scaled, 'f', -1, 64)
	}
	if strings.Contains(text, ".") {
		text = strings.TrimRight(strings.TrimRight(text, "0"), ".")
	}
	return text, true
}

// figureWritten is a figure as one value, and as written.
type figureWritten struct{ value, written string }

// figuresWritten are the figures text gives, each as its value and as
// written, leaving out whole numbers under ten, which are written as words
// as often as not.
func figuresWritten(text string) []figureWritten {
	text = citation.ReplaceAllLiteralString(text, " ")
	var out []figureWritten
	for _, m := range number.FindAllStringSubmatch(text, -1) {
		figure, ok := figureValue(m[1], m[2])
		if !ok {
			continue
		}
		n, err := strconv.ParseUint(figure, 10, 64)
		small := !strings.Contains(figure, ".") && err == nil && n < 10
		if !small && !slices.ContainsFunc(out, func(f figureWritten) bool { return f.value == figure }) {
			out = append(out, figureWritten{figure, strings.TrimSpace(m[0])})
		}
	}
	return out
}

// figures are the figures text gives, each as one value - "1,200" and
// "1200" alike, "3,5" and "3.50" as "3.5", "1.2万" as "12000".
func figures(text string) []string {
	var out []string
	for _, f := range figuresWritten(text) {
		out = append(out, f.value)
	}
	return out
}

// missingFigures are the figures of text that none of words gives, as
// text writes them.
func missingFigures(text string, words []string) []string {
	given := map[string]bool{}
	for _, w := range words {
		for _, f := range figures(w) {
			given[f] = true
		}
	}
	var out []string
	for _, f := range figuresWritten(text) {
		if !given[f.value] {
			out = append(out, f.written)
		}
	}
	return out
}

// pairs are text's neighbouring pairs of letters or characters.
func pairs(text string) map[[2]rune]bool {
	chars := []rune(plain(text))
	out := map[[2]rune]bool{}
	for i := 0; i+1 < len(chars); i++ {
		out[[2]rune{chars[i], chars[i+1]}] = true
	}
	return out
}

// sentences are text's sentences and lines, trimmed: a sentence ends at a
// full stop, question or exclamation mark followed by a space or the end -
// so "3.5" stays whole - or at one of the CJK marks, which need none.
// Citations written after the mark stay with the sentence they follow.
func sentences(text string) []string {
	var out []string
	start := 0
	for i, c := range text {
		if i < start {
			continue
		}
		next, size := utf8.DecodeRuneInString(text[i+utf8.RuneLen(c):])
		atEnd := size == 0
		nextIsSpace := atEnd || unicode.IsSpace(next)
		nextCites := !atEnd && next == '['
		ends := c == '。' || c == '！' || c == '？' || c == '\n' ||
			(c == '.' || c == '!' || c == '?') && (nextIsSpace || nextCites)
		if !ends {
			continue
		}
		end := i + utf8.RuneLen(c)
		if c != '\n' {
			if m := trailingCitations.FindStringIndex(text[end:]); m != nil {
				end += m[1]
			}
		}
		out = append(out, strings.TrimSpace(text[start:end]))
		start = end
	}
	out = append(out, strings.TrimSpace(text[start:]))
	return slices.DeleteFunc(out, func(s string) bool { return s == "" })
}

// excerpt is the passage of words a note saying said most likely rests on
// - the sentence sharing most of its figures and wording, with the next one
// when it is short - or false when no passage shares enough.
func excerpt(words, said string) (string, bool) {
	wantedPairs, wantedFigures := pairs(said), figures(said)
	list := sentences(words)
	if len(list) == 0 {
		return "", false
	}
	score := func(p string) int {
		n := 0
		for _, f := range figures(p) {
			if slices.Contains(wantedFigures, f) {
				n += figureWeight
			}
		}
		for pair := range pairs(p) {
			if wantedPairs[pair] {
				n++
			}
		}
		return n
	}
	best, top := 0, -1
	for i, p := range list {
		// The first of those scoring most.
		if s := score(p); s > top {
			best, top = i, s
		}
	}
	if top < excerptMinScore {
		return "", false
	}
	text := list[best]
	if utf8.RuneCountInString(text) < excerptChars/2 && best+1 < len(list) {
		text += " " + list[best+1]
	}
	return ai.Clip(text, excerptChars), true
}

// unsupportedFigure starts a section's problem with a figure its sources
// don't give - a soft problem, as its length is.
const unsupportedFigure = "A figure its sources don't give"

// maxUnsupported is the most such problems told at once.
const maxUnsupported = 5

// citedSentence is a section's sentence that cites, with the numbers it
// cites.
type citedSentence struct {
	text  string
	cited []uint32
}

// citedSentences are a section's sentences that cite, each with the
// numbers it cites: its prose only - code, pictures and marks aside.
func citedSentences(markdown string) []citedSentence {
	var text strings.Builder
	inFence := false
	for _, line := range lines(markdown) {
		trimmed := strings.TrimLeftFunc(line, unicode.IsSpace)
		if strings.HasPrefix(trimmed, "```") {
			inFence = !inFence
		} else if !(inFence || strings.HasPrefix(trimmed, "![") || strings.HasPrefix(trimmed, "<!--")) {
			text.WriteString(line)
			text.WriteByte('\n')
		}
	}
	var out []citedSentence
	for _, sentence := range sentences(text.String()) {
		var cited []uint32
		for _, c := range citedLink.FindAllStringSubmatch(sentence, -1) {
			if n, err := strconv.ParseUint(c[1], 10, 32); err == nil && !slices.Contains(cited, uint32(n)) {
				cited = append(cited, uint32(n))
			}
		}
		if len(cited) > 0 {
			out = append(out, citedSentence{sentence, cited})
		}
	}
	return out
}

// figuresBySource are the figures each source's notes give - their claims,
// evidence, quotes and excerpts, less the figures their sources were found
// not to give.
func figuresBySource(notes []Note) map[uint32]map[string]bool {
	out := map[uint32]map[string]bool{}
	for _, note := range notes {
		said := fmt.Sprintf("%s %s %s %s", note.Claim, note.Evidence, deref(note.Quote), deref(note.Excerpt))
		unmatched := map[string]bool{}
		for _, u := range note.Unmatched {
			for _, f := range figures(u) {
				unmatched[f] = true
			}
		}
		var given []string
		for _, f := range figures(said) {
			if !unmatched[f] {
				given = append(given, f)
			}
		}
		for _, n := range note.Sources {
			if out[n] == nil {
				out[n] = map[string]bool{}
			}
			for _, f := range given {
				out[n][f] = true
			}
		}
	}
	return out
}

// unsupportedFigures are a section's cited sentences that give a figure
// none of the sources they cite gave in their notes, as problems for its
// writer. Code, pictures and marks aside; a sentence that cites nothing
// isn't read.
func unsupportedFigures(markdown string, given map[uint32]map[string]bool) []Problem {
	var out []Problem
	for _, s := range citedSentences(markdown) {
		var missing []string
		for _, f := range figuresWritten(s.text) {
			if !slices.ContainsFunc(s.cited, func(n uint32) bool { return given[n][f.value] }) {
				missing = append(missing, f.written)
			}
		}
		if len(missing) == 0 {
			continue
		}
		bare := citedLink.ReplaceAllLiteralString(s.text, "")
		var cites strings.Builder
		for _, n := range s.cited {
			fmt.Fprintf(&cites, "[%d]", n)
		}
		out = append(out, softly(fmt.Sprintf("%s: \"%s\" has %s but the notes of %s don't - give the figure as the notes do, cite the source that gives it, or leave it out.",
			unsupportedFigure, ai.Clip(strings.TrimSpace(bare), 120), strings.Join(missing, ", "), cites.String())))
		if len(out) == maxUnsupported {
			break
		}
	}
	return out
}

// sourcePassage is the passage of its sources a note rests on, marked as
// the web's words: material to read, never instructions.
func sourcePassage(note *Note) string {
	if note.Excerpt == nil {
		return ""
	}
	var cited strings.Builder
	for _, n := range note.Sources {
		fmt.Fprintf(&cited, "[%d]", n)
	}
	// A page can't close the block early.
	passage := web.Unclosing(*note.Excerpt)
	return fmt.Sprintf("\n  <external-content source=\"%s\">\n  %s\n  </external-content>", cited.String(), passage)
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
