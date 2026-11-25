package rules

import (
	_ "embed"
	"fmt"
	"slices"
	"strings"
	"sync"
)

// English style: sentence starts and length, times, wordy, exclusive or
// vague phrases, capitals, the passive, and words AI text overuses.

var enNumStartRe = MustRe(`\d[\d,.]*`)

// enSentenceStart is en.num.sentence-start: a sentence starting with a
// numeral (not a year, not a heading or list item).
func enSentenceStart(t *Text, c *ctx, o *out) {
	if c.block.isHeading() || c.block == blockItem || c.block == blockCell || c.profile.is("govuk") {
		return
	}
	for _, m := range t.find(enNumStartRe) {
		a, b := m[0], m[1]
		if !t.sentenceStart(a) || t.beforeIs(a, func(r rune) bool { return !isSpace(r) }) && a > 0 {
			continue
		}
		num := t.slice(a, b)
		// A byte count, as the Rust had it.
		year := len(num) == 4 && (strings.HasPrefix(num, "1") || strings.HasPrefix(num, "2"))
		if !year && t.atEq(b, ' ') {
			o.flag("en.num.sentence-start", a, b, "Spell out a number that starts a sentence, or reword.")
		}
	}
}

var (
	enT24Re = MustRe(`\b(?:1[3-9]|2[0-3]):[0-5]\d\b`)
	enT12Re = MustRe(`(?i)\b\d{1,2}(?::\d{2})?\s?(?:a\.?m\.?|p\.?m\.?)`)
)

// enClock is en.num.clock: 12-hour and 24-hour times in one document.
func enClock(t *Text, c *ctx, o *out) {
	s := c.stats
	if s.clock12 == 0 || s.clock24 == 0 {
		return
	}
	re := enT12Re
	if s.clock24 < s.clock12 {
		re = enT24Re
	}
	for _, m := range t.find(re) {
		o.flag("en.num.clock", m[0], m[1], "12-hour and 24-hour times are mixed in this document.")
	}
}

var enWordy = [][2]string{
	{"in order to", "to"},
	{"a number of", "some"},
	{"at this point in time", "now"},
	{"at the present time", "now"},
	{"prior to", "before"},
	{"due to the fact that", "because"},
	{"owing to the fact that", "because"},
	{"in light of the fact that", "because"},
	{"in the event that", "if"},
	{"with regard to", "about"},
	{"in spite of the fact that", "although"},
	{"despite the fact that", "although"},
	{"for the purpose of", "for"},
	{"has the ability to", "can"},
	{"in the near future", "soon"},
	{"a large number of", "many"},
	{"the majority of", "most"},
	{"on a daily basis", "daily"},
	{"in close proximity to", "near"},
	{"until such time as", "until"},
	{"each and every", "every"},
	{"first and foremost", "first"},
	{"end result", "result"},
	{"past history", "history"},
	{"advance planning", "planning"},
	{"basic fundamentals", "fundamentals"},
}

var enInclusive = [][2]string{
	{"whitelist", "allowlist"},
	{"blacklist", "blocklist"},
	{"whitelisted", "allowlisted"},
	{"blacklisted", "blocklisted"},
	{"manpower", "workforce"},
	{"mankind", "humankind"},
	{"man-hours", "person-hours"},
	{"chairman", "chair"},
	{"policeman", "police officer"},
	{"policemen", "police officers"},
	{"fireman", "firefighter"},
	{"firemen", "firefighters"},
	{"stewardess", "flight attendant"},
	{"mailman", "mail carrier"},
	{"grandfathered", "exempted"},
	{"sanity check", "quick check"},
	{"dummy value", "placeholder value"},
}

var enGovukAvoid = [][2]string{
	{"facilitate", "help"},
	{"utilise", "use"},
	{"utilize", "use"},
	{"leverage", "use"},
	{"empower", "allow"},
	{"streamline", "simplify"},
	{"deliver", "make"},
	{"deploy", "use"},
	{"liaise", "work with"},
	{"overarching", "main"},
	{"robust", "strong"},
	{"tackle", "deal with"},
	{"e.g.", "for example"},
	{"eg", "for example"},
	{"i.e.", "that is"},
	{"ie", "that is"},
}

var enAcronyms = []string{
	"USA", "UK", "EU", "UN", "NASA", "FAQ", "API", "PDF", "HTML", "CSS", "URL", "CEO", "CFO",
	"CTO", "HR", "IT", "AI", "OK", "TV", "GDP", "NATO", "FBI", "CIA", "BBC", "USB", "CPU", "GPU",
	"RAM", "SQL", "JSON", "XML", "HTTP", "HTTPS", "ID", "PIN", "ATM", "DIY", "ASAP", "FYI",
	"RSVP", "NHS", "GOV",
}

var enAdjectival = []string{
	"interested", "tired", "excited", "worried", "pleased", "surprised", "bored", "married",
	"supposed", "used", "based", "located", "concerned", "involved", "prepared", "scared",
	"confused", "satisfied", "disappointed", "amazed", "relaxed", "annoyed", "qualified",
	"experienced", "detailed", "advanced", "complicated", "dedicated", "limited", "related",
	"red", "bed", "need", "seed", "feed", "speed", "indeed", "shed",
}

var enAIVocabWords = []string{
	"delve", "delves", "delving", "tapestry", "testament to", "pivotal", "seamless", "seamlessly",
	"robust", "leverage", "leverages", "leveraging",
}

// findPhrase is every whole-phrase occurrence of phrase, case-insensitive.
func findPhrase(t *Text, phrase string) [][2]int { return findWord(t, phrase, true) }

// enPhrases are the word-list suggestions (wordy, plain, please, inclusive,
// GOV.UK style), none inside quotations, dialogue or titles of works.
func enPhrases(t *Text, c *ctx, o *out) {
	fixList := func(rule string, list [][2]string, why func(string) string) {
		for _, p := range list {
			for _, m := range findPhrase(t, p[0]) {
				if !t.inQuotes(m[0], m[1]) {
					o.fix(rule, m[0], m[1], matchCase(t.slice(m[0], m[1]), p[1]), why(p[1]))
				}
			}
		}
	}
	fixList("en.word.wordy", enWordy, func(to string) string { return "Wordy: “" + to + "” says the same." })
	fixList("en.word.inclusive", enInclusive, func(to string) string { return "Consider “" + to + "”." })
	p := c.profile
	if p.is("govuk") {
		for _, pw := range plainWords() {
			for _, m := range findPhrase(t, pw[0]) {
				if !t.inQuotes(m[0], m[1]) {
					o.fix("en.word.plain", m[0], m[1], matchCase(t.slice(m[0], m[1]), pw[1]), "Plain English: “"+pw[1]+"”.")
				}
			}
		}
		fixList("en.govuk.style", enGovukAvoid, func(to string) string { return "GOV.UK style: “" + to + "”." })
		for i := range t.Len() {
			if t.Chars[i] == '&' && t.beforeEq(i, ' ') && t.atEq(i+1, ' ') {
				o.fix("en.govuk.style", i, i+1, "and", "GOV.UK style: “and”, not “&”.")
			}
		}
	}
	if p.is("tech-google") || p.is("tech-microsoft") {
		for _, m := range findWord(t, "please", true) {
			o.flag("en.word.please", m[0], m[1], "Technical instructions don’t need “please”.")
		}
	}
}

//go:embed data/en-plain-words.csv
var plainWordsCSV string

// plainWords is the plain-words list (plainlanguage.gov archive, public
// domain): (avoid, prefer - the first alternative).
var plainWords = sync.OnceValue(func() [][2]string {
	var list [][2]string
	lines := Lines(plainWordsCSV)
	if len(lines) > 0 {
		lines = lines[1:]
	}
	for _, line := range lines {
		avoid, rest, ok := strings.Cut(line, ",")
		if !ok {
			continue
		}
		prefer := strings.TrimLeft(rest, `"`)
		first := prefer
		if k := strings.IndexAny(prefer, `,"(;`); k >= 0 {
			first = prefer[:k]
		}
		first = strings.TrimSpace(first)
		if !strings.Contains(avoid, "/") && first != "" && avoid != "" {
			list = append(list, [2]string{strings.TrimSpace(avoid), first})
		}
	}
	return list
})

// enCaps is en.style.caps: runs of three or more all-caps words that
// aren't acronyms; "!!" and "?!" outside fiction.
func enCaps(t *Text, c *ctx, o *out) {
	words := t.words()
	shouty := func(w [2]int) bool {
		s := t.slice(w[0], w[1])
		return runeCount(s) >= 2 && !strings.ContainsFunc(s, func(r rune) bool { return !isUpper(r) }) &&
			!slices.Contains(enAcronyms, s)
	}
	for i := 0; i < len(words); {
		j := i
		for j < len(words) && shouty(words[j]) {
			j++
		}
		if j-i >= 3 {
			o.flag("en.style.caps", words[i][0], words[j-1][1], "Several words in capitals read as shouting.")
			i = j
		} else {
			i++
		}
	}
	if !c.profile.is("fiction") {
		for _, m := range append(findStr(t, "!!"), findStr(t, "?!")...) {
			o.flag("en.style.caps", m[0], m[1], "Doubled exclamation marks are rarely needed outside fiction.")
		}
	}
}

// enSentenceLength is en.style.sentence-length: sentences over 25 words
// (info), over 40 (60 in fiction) flagged more firmly in the message.
func enSentenceLength(t *Text, c *ctx, o *out) {
	if c.block.isHeading() || c.block == blockItem || c.block == blockCell {
		return
	}
	limit := 40
	if c.profile.is("fiction") {
		limit = 60
	}
	start := 0
	for i := 0; i <= t.Len(); i++ {
		end := i == t.Len() || strings.ContainsRune(".!?", t.Chars[i]) && t.atNoneOr(i+1, isSpace)
		if !end {
			continue
		}
		words := 0
		for _, w := range splitRunes(t.Chars[start:i], isSpace) {
			if slices.ContainsFunc(w, IsAlphanumeric) {
				words++
			}
		}
		if words > 25 && !t.inQuotes(start, i) {
			msg := fmt.Sprintf("Long sentence (%d words): consider splitting it.", words)
			if words > limit {
				msg = fmt.Sprintf("Very long sentence (%d words): split it.", words)
			}
			a := start
			for a < i && isSpace(t.Chars[a]) {
				a++
			}
			o.flag("en.style.sentence-length", a, i, msg)
		}
		start = i + 1
	}
}

// splitRunes is Rust's slice::split: the pieces between separators,
// empty ones included.
func splitRunes(rs []rune, sep func(rune) bool) [][]rune {
	var out [][]rune
	a := 0
	for i, r := range rs {
		if sep(r) {
			out = append(out, rs[a:i])
			a = i + 1
		}
	}
	return append(out, rs[a:])
}

var enPassiveRe = MustRe(`(?i)\b(?:am|is|are|was|were|be|been|being)\s+(?:\w+ly\s+)?(\w+ed|written|given|taken|made|done|seen|known|shown|found|told|built|sent|held|kept|left|paid|sold|brought|bought|thought|caught|taught|chosen|driven|eaten|forgotten|hidden|spoken|stolen|broken|frozen)\b`)

// enPassive is en.style.passive ("strict" only): be + past participle.
func enPassive(t *Text, _ *ctx, o *out) {
	for _, g := range t.captures(enPassiveRe) {
		if !g[0].OK || !g[1].OK {
			continue
		}
		if slices.Contains(enAdjectival, toLower(t.slice(g[1].A, g[1].B))) {
			continue
		}
		o.flag("en.style.passive", g[0].A, g[0].B, "Passive voice: say who does it, if that helps the reader.")
	}
}

// enAIVocab is en.style.ai-vocab: words that cluster in generated text:
// info per sighting, a warning at two or more in one paragraph. Technical
// senses are left alone.
func enAIVocab(t *Text, _ *ctx, o *out) {
	var found [][2]int
	for _, w := range enAIVocabWords {
		for _, m := range findPhrase(t, w) {
			a, b := m[0], m[1]
			next := trimStart(toLower(string(t.Chars[b:min(b+12, t.Len())])))
			prev := toLower(string(t.Chars[max(a-12, 0):a]))
			technical := strings.HasPrefix(w, "robust") &&
				(strings.HasPrefix(next, "statistic") || strings.HasPrefix(next, "estimat") || strings.HasPrefix(next, "regression")) ||
				strings.HasPrefix(w, "leverag") && (strings.Contains(prev, "financial") || strings.HasPrefix(next, "ratio"))
			if !technical && !t.inQuotes(a, b) {
				found = append(found, m)
			}
		}
	}
	msg := "A word common in generated text: is a plainer, more specific word better here?"
	if len(found) >= 2 {
		msg = "Several words common in generated text in one paragraph: prefer plainer, more specific words."
	}
	for _, m := range found {
		o.flag("en.style.ai-vocab", m[0], m[1], msg)
	}
}
