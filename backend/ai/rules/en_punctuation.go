package rules

import (
	"strings"
	"unicode/utf8"
)

// English punctuation: dashes, the serial comma, quotes, the end of a
// heading, etc., minus signs and slashes.

// majorityDash is the document's dash, for replacing "--": a spaced en
// dash where the document mostly uses those, else an em dash (the spacing
// as typed).
func majorityDash(c *ctx) string {
	s := c.stats
	if s.dashEnSpaced > s.dashEmSpaced+s.dashEmTight {
		return "–"
	}
	return "—"
}

// enDoubleHyphen is en.punct.double-hyphen: "--" in prose -> the
// document's dash. Not command-line flags ("--verbose").
func enDoubleHyphen(t *Text, c *ctx, o *out) {
	n := t.Len()
	for i := 0; i+1 < n; {
		if t.Chars[i] == '-' && t.Chars[i+1] == '-' && !t.atEq(i+2, '-') && !t.beforeEq(i, '-') {
			flag := t.beforeIs(i, isSpace) && t.atIs(i+2, IsAlphanumeric)
			prose := func(r rune) bool { return IsAlphanumeric(r) || r == ' ' }
			if !flag && t.beforeIs(i, prose) && t.atIs(i+2, prose) {
				dash := majorityDash(c)
				o.fix("en.punct.double-hyphen", i, i+2, dash, "Use a dash (“"+dash+"”), not two hyphens.")
			}
			i += 2
		} else {
			i++
		}
	}
}

// enHeadingEnd is en.heading.end-punct: a heading ending in "." or ":"
// (not "...").
func enHeadingEnd(t *Text, c *ctx, o *out) {
	if !c.block.isHeading() {
		return
	}
	n := runeCount(trimEnd(t.S))
	if n >= 2 && (t.Chars[n-1] == '.' || t.Chars[n-1] == ':') && t.Chars[n-2] != '.' {
		o.fix("en.heading.end-punct", n-1, n, "", "Headings take no full stop or colon at the end.")
	}
}

var (
	enSerialYesRe = MustRe(`\b\w+, \w+(?: \w+)?, (?:and|or) \w+`)
	enSerialNoRe  = MustRe(`\b\w+, \w+(?: \w+)? (?:and|or) \w+`)
)

// lastIndexEither is the byte index of the last a in s, else of the last
// b; -1 for neither.
func lastIndexEither(s, a, b string) int {
	if k := strings.LastIndex(s, a); k >= 0 {
		return k
	}
	return strings.LastIndex(s, b)
}

// enSerialComma is en.punct.serial-comma: only against a chosen preset,
// never in Automatic.
func enSerialComma(t *Text, c *ctx, o *out) {
	p := c.profile
	if !p.Explicit {
		return
	}
	var wants bool
	switch p.Preset {
	case "general", "oxford", "apa", "mla", "tech-google", "tech-microsoft":
		wants = true
	case "news", "govuk":
		wants = false
	default:
		return
	}
	if wants {
		for _, m := range t.find(enSerialNoRe) {
			span := t.slice(m[0], m[1])
			if k := lastIndexEither(span, " and ", " or "); k >= 0 {
				at := m[0] + utf8.RuneCountInString(span[:k])
				o.fix("en.punct.serial-comma", at, at+1, ", ", "The chosen style uses the serial comma.")
			}
		}
	} else {
		for _, m := range t.find(enSerialYesRe) {
			span := t.slice(m[0], m[1])
			if k := lastIndexEither(span, ", and ", ", or "); k >= 0 {
				at := m[0] + utf8.RuneCountInString(span[:k])
				o.fix("en.punct.serial-comma", at, at+2, " ", "The chosen style doesn’t use the serial comma.")
			}
		}
	}
}

// enDashStyle is en.punct.dash-style: em dashes closed up, em dashes
// spaced and spaced en dashes mixed - the minority flagged. Ranges
// (1990–1995) aren't dashes.
func enDashStyle(t *Text, c *ctx, o *out) {
	s := c.stats
	total := s.dashEmTight + s.dashEmSpaced + s.dashEnSpaced
	// The most used, the later on a tie (as Rust's max_by_key).
	major, count := "", -1
	for _, k := range []struct {
		name  string
		count int
	}{{"em-tight", s.dashEmTight}, {"em-spaced", s.dashEmSpaced}, {"en-spaced", s.dashEnSpaced}} {
		if k.count >= count {
			major, count = k.name, k.count
		}
	}
	if !minority(total-count, count) {
		return
	}
	target := " – "
	switch major {
	case "em-tight":
		target = "—"
	case "em-spaced":
		target = " — "
	}
	for i := range t.Len() {
		ch := t.Chars[i]
		spaced := t.beforeEq(i, ' ') && t.atEq(i+1, ' ')
		var kind string
		switch {
		case ch == '—' && spaced:
			kind = "em-spaced"
		case ch == '—' && t.beforeIs(i, IsAlphabetic) && t.atIs(i+1, IsAlphabetic):
			kind = "em-tight"
		case ch == '–' && spaced:
			kind = "en-spaced"
		default:
			continue
		}
		if kind != major {
			a, b := i, i+1
			if t.beforeEq(i, ' ') {
				a, b = i-1, i+2
			}
			o.fix("en.punct.dash-style", a, b, target, "Dash style differs from the rest of the document.")
		}
	}
}

// enQuoteStyle is en.punct.quote-style: straight and curly double quotes
// (or apostrophes) mixed - the minority flagged.
func enQuoteStyle(t *Text, c *ctx, o *out) {
	s := c.stats
	if minority(s.dqStraight, s.dqCurly) {
		var marks []int
		for i, ch := range t.Chars {
			if ch == '"' && !t.Protected[i] {
				marks = append(marks, i)
			}
		}
		if len(marks)%2 == 0 {
			msg := "Quote style differs from the rest of the document (curly quotes)."
			for k := 0; k < len(marks); k += 2 {
				o.fix("en.punct.quote-style", marks[k], marks[k]+1, "“", msg)
				o.fix("en.punct.quote-style", marks[k+1], marks[k+1]+1, "”", msg)
			}
		}
	} else if minority(s.dqCurly, s.dqStraight) {
		for i, ch := range t.Chars {
			if ch == '“' || ch == '”' {
				o.fix("en.punct.quote-style", i, i+1, "\"", "Quote style differs from the rest of the document (straight quotes).")
			}
		}
	}
	apostrophe := func(i int) bool { return t.beforeIs(i, IsAlphabetic) && t.atIs(i+1, IsAlphabetic) }
	msg := "Apostrophe style differs from the rest of the document."
	if minority(s.aposStraight, s.aposCurly) {
		for i, ch := range t.Chars {
			if ch == '\'' && apostrophe(i) {
				o.fix("en.punct.quote-style", i, i+1, "’", msg)
			}
		}
	} else if minority(s.aposCurly, s.aposStraight) {
		for i, ch := range t.Chars {
			if ch == '’' && apostrophe(i) {
				o.fix("en.punct.quote-style", i, i+1, "'", msg)
			}
		}
	}
}

var enEtcAfterRe = MustRe(`(?i)\b(?:such as|for example|for instance|e\.g\.|including)\b[^.!?;]*?,?\s+(etc\.?)`)

// enEtc is en.word.etc: "etc." after "such as", "for example", "e.g.".
func enEtc(t *Text, _ *ctx, o *out) {
	for _, g := range t.captures(enEtcAfterRe) {
		if !g[1].OK {
			continue
		}
		start := g[1].A
		for start > 0 && (t.Chars[start-1] == ' ' || t.Chars[start-1] == ',') {
			start--
		}
		o.fix("en.word.etc", start, g[1].B, "", "“Such as” and “for example” already say the list is incomplete: drop “etc.”.")
	}
}

// enMinus is en.num.minus: a hyphen as a minus sign before a number ->
// U+2212.
func enMinus(t *Text, _ *ctx, o *out) {
	for i := 0; i+1 < t.Len(); i++ {
		if t.Chars[i] != '-' || !isASCIIDigit(t.Chars[i+1]) ||
			!t.beforeNoneOr(i, func(r rune) bool { return r == ' ' || r == '(' }) ||
			i >= 2 && t.Chars[i-2] == ' ' && t.Chars[i-1] == '-' {
			continue
		}
		// "from -5 to -10" yes; "pages 3-5", dates and codes have no space
		// before.
		prevWordIsNumber := i > 1 && isASCIIDigit(t.Chars[i-2])
		for j := i - 2; prevWordIsNumber && j >= 0 && !isSpace(t.Chars[j]); j-- {
			if !isASCIIDigit(t.Chars[j]) {
				prevWordIsNumber = false
			}
		}
		if !prevWordIsNumber {
			o.fix("en.num.minus", i, i+1, "−", "A minus sign (−) reads better than a hyphen.")
		}
	}
}

var enSlashRe = MustRe(`(?i)\b(?:and/or|he/she|she/he|his/her|her/his|him/her|s/he)\b|\b\w+\(s\)`)

// enSlash is en.word.slash: and/or, he/she, s/he, document(s).
func enSlash(t *Text, _ *ctx, o *out) {
	for _, m := range t.find(enSlashRe) {
		if !t.inQuotes(m[0], m[1]) {
			o.flag("en.word.slash", m[0], m[1], "Slashed or bracketed alternatives are hard to read: choose one, or write both out.")
		}
	}
}
