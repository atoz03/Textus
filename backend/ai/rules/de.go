package rules

import (
	"slices"
	"strings"
)

// German rules, Duden-style by default. Never flagged: a comma before
// *und*, `%` compounds (5%-Klausel, 100%ig), the infinitive comma after
// *versuchen*, range-dash style (only inconsistency), Swiss `ss`.

func deRun(t *Text, c *ctx, o *out) {
	deQuoteStyle(t, c, o)
	deReportingComma(t, c, o)
	deDash(t, c, o)
	deAbbrSpace(t, c, o)
	deRemovedVariant(t, c, o)
	dePercent(t, c, o)
	deSubordinate(t, c, o)
	deAdversative(t, c, o)
	deDateZeroPad(t, c, o)
	deGroup(t, c, o)
	deEszett(t, c, o)
	deGenderChars(t, c, o)
}

// deQuoteStyle is de.quote.style: straight or English curly quotes -> „…“
// (de-CH «…»). Book-style »…« is a valid house style and stays.
func deQuoteStyle(t *Text, c *ctx, o *out) {
	swiss := c.profile.regionIs("CH") || !c.profile.Explicit && c.stats.dqGuillemet > c.stats.dqLow && c.stats.dqGuillemet > 0
	open, close, msg := "„", "“", "Deutsche Anführungszeichen: „…“."
	if swiss {
		open, close, msg = "«", "»", "In der Schweiz stehen Anführungszeichen als «…»."
	}
	// English pairs: “…” (U+201C … U+201D). An English opening “ starts a
	// word; the German closing “ ends one.
	for i := 0; i < t.Len(); {
		opens := t.Chars[i] == '“' && t.beforeNoneOr(i, func(r rune) bool { return isSpace(r) || r == '(' || r == '[' })
		if opens {
			if j := slices.Index(t.Chars[i+1:], '”'); j >= 0 {
				j += i + 1
				o.fix("de.quote.style", i, i+1, open, msg)
				o.fix("de.quote.style", j, j+1, close, msg)
				i = j + 1
				continue
			}
		}
		i++
	}
	var marks []int
	for i, ch := range t.Chars {
		if ch == '"' && !t.Protected[i] {
			marks = append(marks, i)
		}
	}
	if len(marks)%2 == 0 {
		for k := 0; k < len(marks); k += 2 {
			o.fix("de.quote.style", marks[k], marks[k]+1, open, msg)
			o.fix("de.quote.style", marks[k+1], marks[k+1]+1, close, msg)
		}
	}
	if c.profile.regionIs("CH") {
		for i, ch := range t.Chars {
			switch {
			case ch == '„':
				o.fix("de.quote.style", i, i+1, "«", msg)
			case ch == '“' && slices.Contains(t.Chars[:i], '„'):
				o.fix("de.quote.style", i, i+1, "»", msg)
			}
		}
	}
}

const deSaying = "sagte|sagt|rief|ruft|fragte|fragt|meinte|meint|antwortete|antwortet|erwiderte|flüsterte|schrie|erklärte|betonte|murmelte|seufzte|lachte"

var (
	deReportNoCommaRe = MustRe(`([“«\x22])( )(?:` + deSaying + `)\b`)
	deReportPeriodRe  = MustRe(`(\.)[“«\x22],? (?:` + deSaying + `)\b`)
)

// deReportingComma is de.quote.reporting-comma: „Komm her“ sagte er ->
// „Komm her“, sagte er; and the quote's own full stop goes: „Komm her.“,
// sagte er.
func deReportingComma(t *Text, _ *ctx, o *out) {
	who := []string{"er", "sie", "es", "ich", "wir", "ihr", "du", "man", "der", "die", "das", "mein"}
	for _, g := range t.captures(deReportNoCommaRe) {
		if !g[0].OK || !g[1].OK {
			continue
		}
		q, end := g[1].A, g[0].B
		// Direct speech is followed by who said it ("sagte er", "rief
		// Anna"); „Unterhaltungen“ meint im Folgenden... is a quoted word.
		next := ""
		if f := strings.Fields(t.slice(end, t.Len())); len(f) > 0 {
			next = f[0]
		}
		bare := toLower(strings.TrimFunc(next, func(r rune) bool { return !IsAlphabetic(r) }))
		first, ok := firstRune(next)
		if !slices.Contains(who, bare) && !(ok && isUpper(first)) {
			continue
		}
		o.fix("de.quote.reporting-comma", q, q+1, string(t.Chars[q])+",", "Nach der wörtlichen Rede steht vor dem Begleitsatz ein Komma.")
	}
	for _, g := range t.captures(deReportPeriodRe) {
		if g[1].OK {
			o.fix("de.quote.reporting-comma", g[1].A, g[1].A+1, "", "Der Punkt der wörtlichen Rede entfällt, wenn der Begleitsatz folgt.")
		}
	}
}

// deDash is de.punct.dash: " - " between words -> " – " (ranges between
// digits stay).
func deDash(t *Text, _ *ctx, o *out) {
	for i := 1; i+1 < t.Len(); i++ {
		if t.Chars[i] == '-' && t.Chars[i-1] == ' ' && t.Chars[i+1] == ' ' &&
			i >= 2 && IsAlphabetic(t.Chars[i-2]) && t.atIs(i+2, IsAlphabetic) {
			o.fix("de.punct.dash", i, i+1, "–", "Als Gedankenstrich steht der Halbgeviertstrich (–).")
		}
	}
}

var deAbbrs = [][2]string{
	{"z.B.", "z.\u202FB."}, {"d.h.", "d.\u202Fh."}, {"u.a.", "u.\u202Fa."}, {"i.A.", "i.\u202FA."},
	{"u.U.", "u.\u202FU."}, {"z.T.", "z.\u202FT."}, {"o.Ä.", "o.\u202FÄ."}, {"v.a.", "v.\u202Fa."},
	{"m.E.", "m.\u202FE."}, {"s.o.", "s.\u202Fo."}, {"s.u.", "s.\u202Fu."},
	{"i.d.R.", "i.\u202Fd.\u202FR."}, {"u.Ä.", "u.\u202FÄ."},
}

// deAbbrSpace is de.word.abbr-space: z.B. -> z. B. (narrow no-break
// space). usw. and usf. are single units.
func deAbbrSpace(t *Text, _ *ctx, o *out) {
	for _, p := range deAbbrs {
		for _, m := range findStr(t, p[0]) {
			if t.beforeIs(m[0], IsAlphanumeric) {
				continue
			}
			o.fix("de.word.abbr-space", m[0], m[1], p[1],
				"Mehrteilige Abkürzungen mit (schmalem) Leerzeichen: „"+strings.ReplaceAll(p[1], "\u202F", " ")+"“.")
		}
	}
}

// deRemovedVariant is de.word.removed-variant: spellings the 2024
// Regelwerk removed.
func deRemovedVariant(t *Text, _ *ctx, o *out) {
	for _, p := range [][2]string{
		{"Spagetti", "Spaghetti"}, {"Jogurt", "Joghurt"}, {"Tunfisch", "Thunfisch"}, {"Ketschup", "Ketchup"},
		{"Majonäse", "Mayonnaise"}, {"Portmonee", "Portemonnaie"}, {"Grislibär", "Grizzlybär"},
	} {
		for _, m := range findStr(t, p[0]) {
			o.fix("de.word.removed-variant", m[0], m[1], p[1], "Seit 2024 nicht mehr zulässig: „"+p[1]+"“.")
		}
	}
}

// dePercent is de.num.percent: 5% -> 5 % (no-break). Never compounds or
// derivatives.
func dePercent(t *Text, _ *ctx, o *out) {
	for i := 1; i < t.Len(); i++ {
		if t.Chars[i] != '%' || !isASCIIDigit(t.Chars[i-1]) || t.atIs(i+1, func(r rune) bool { return r == '-' || IsAlphabetic(r) }) {
			continue
		}
		o.fix("de.num.percent", i-1, i+1, string(t.Chars[i-1])+"\u00A0%", "Zwischen Zahl und Prozentzeichen steht ein (geschütztes) Leerzeichen.")
	}
}

var deSubordinateRe = MustRe(`(\p{L}+) (dass|weil|obwohl|ob|damit)\b( \p{L}+ \p{L}+)?`)

// deSubordinate is de.comma.subordinate ("strict"): no comma before dass,
// weil, obwohl, ob, damit mid-sentence. Not so dass/sodass, ohne dass, als
// ob, und ob, damit as an adverb, or after und/oder.
func deSubordinate(t *Text, _ *ctx, o *out) {
	beforeOK := []string{"so", "ohne", "als", "und", "oder", "außer", "anstatt", "statt", "bis", "aber", "sondern", "nur"}
	for _, g := range t.captures(deSubordinateRe) {
		if !g[1].OK || !g[2].OK {
			continue
		}
		prev := toLower(t.slice(g[1].A, g[1].B))
		conj := t.slice(g[2].A, g[2].B)
		if slices.Contains(beforeOK, prev) || conj == "damit" && !g[3].OK {
			continue
		}
		o.fix("de.comma.subordinate", g[1].B, g[2].A, ", ", "Vor „"+conj+"“ steht in der Regel ein Komma.")
	}
}

var deSondernRe = MustRe(`(\p{L}+) (sondern)\b`)

// deAdversative is de.comma.adversative ("strict"): no comma before
// sondern.
func deAdversative(t *Text, _ *ctx, o *out) {
	for _, g := range t.captures(deSondernRe) {
		if g[1].OK && g[2].OK {
			o.fix("de.comma.adversative", g[1].B, g[2].A, ", ", "Vor „sondern“ steht ein Komma.")
		}
	}
}

var deDateRe = MustRe(`\b(\d{1,2})\.(\d{1,2})\.(\d{4})\b`)

// padTwo is s padded with zeros to two chars.
func padTwo(s string) string {
	if runeCount(s) < 2 {
		return strings.Repeat("0", 2-runeCount(s)) + s
	}
	return s
}

// deDateZeroPad is de.date.zero-pad (DIN 5008, or de.dateZeroPad=on):
// 1.5.2026 -> 01.05.2026.
func deDateZeroPad(t *Text, c *ctx, o *out) {
	if !(c.profile.is("din5008") || c.profile.opt("de.dateZeroPad") == "on") {
		return
	}
	for _, g := range t.captures(deDateRe) {
		if !g[0].OK || !g[1].OK || !g[2].OK || !g[3].OK {
			continue
		}
		ds, ms := t.slice(g[1].A, g[1].B), t.slice(g[2].A, g[2].B)
		// Byte lengths, as the Rust had them.
		if len(ds) == 2 && len(ms) == 2 {
			continue
		}
		o.fix("de.date.zero-pad", g[0].A, g[0].B, padTwo(ds)+"."+padTwo(ms)+"."+t.slice(g[3].A, g[3].B), "DIN 5008: Tag und Monat zweistellig.")
	}
}

var deBigRe = MustRe(`\b(\d{5,})\b ?(€|Euro|EUR|Dollar|km|kg|t|Menschen|Einwohner|Mitarbeiter|Besucher|Stück|Jahre)\b`)

// deGroup is de.num.group: numbers of five or more digits before a unit or
// a counted noun -> grouped with a narrow space (never postcodes, years
// or codes).
func deGroup(t *Text, _ *ctx, o *out) {
	for _, g := range t.captures(deBigRe) {
		if !g[1].OK {
			continue
		}
		digits := t.slice(g[1].A, g[1].B)
		var grouped strings.Builder
		k := 0
		for _, ch := range digits {
			// A byte length against a char index, as the Rust had it: the
			// same for ASCII digits.
			if k > 0 && (len(digits)-k)%3 == 0 {
				grouped.WriteRune('\u202F')
			}
			grouped.WriteRune(ch)
			k++
		}
		o.fix("de.num.group", g[1].A, g[1].B, grouped.String(), "Zahlen ab fünf Stellen werden in Dreiergruppen gegliedert.")
	}
}

var deEszettWords = [][2]string{
	{"Strasse", "Straße"}, {"strasse", "straße"}, {"gross", "groß"}, {"grosse", "große"}, {"grossen", "großen"},
	{"Grösse", "Größe"}, {"Gruss", "Gruß"}, {"Grüsse", "Grüße"}, {"Fuss", "Fuß"}, {"heissen", "heißen"},
	{"heisst", "heißt"}, {"weiss", "weiß"}, {"schliesslich", "schließlich"}, {"ausserdem", "außerdem"},
	{"aussen", "außen"}, {"süss", "süß"}, {"Massnahme", "Maßnahme"}, {"Massnahmen", "Maßnahmen"},
	{"gemäss", "gemäß"}, {"mässig", "mäßig"}, {"fliessen", "fließen"}, {"Fleiss", "Fleiß"}, {"bloss", "bloß"},
	{"draussen", "draußen"},
}

// deEszett is de.word.eszett: ss after a long vowel (Strasse) -> ß - only
// where the document also writes ß (a text without any could be Swiss),
// never under de-CH, never for names.
func deEszett(t *Text, c *ctx, o *out) {
	if c.profile.regionIs("CH") || !(c.stats.deEszett > 0 || c.profile.Explicit) {
		return
	}
	for _, p := range deEszettWords {
		for _, m := range findWord(t, p[0], false) {
			o.fix("de.word.eszett", m[0], m[1], p[1], "Nach langem Vokal steht ß: „"+p[1]+"“.")
		}
	}
}

var deGenderRe = MustRe(`\p{L}+[*:_·]innen\b`)

// deGenderChars is de.word.gender-chars (de.genderChars=flag):
// Lehrer*innen and similar.
func deGenderChars(t *Text, c *ctx, o *out) {
	if c.profile.opt("de.genderChars") != "flag" {
		return
	}
	for _, m := range t.find(deGenderRe) {
		o.flag("de.word.gender-chars", m[0], m[1], "Genderzeichen im Wortinneren sind im Regelwerk nicht vorgesehen.")
	}
}
