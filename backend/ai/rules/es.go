package rules

import (
	"slices"
	"strings"
)

// Spanish rules, after the RAE (summarised). Automatic has no national
// profile: es-ES, es-419 and the EU differ on grouping and percent
// spacing, which are suggestions only.

func esRun(t *Text, c *ctx, o *out) {
	esInverted(t, c, o)
	esDoubleFinal(t, c, o)
	esPeriodOutside(t, c, o)
	esMonosyllable(t, c, o)
	esSolo(t, c, o)
	esMonths(t, c, o)
	etcDots(t, "es.punct.etc", "Tras «etc.» no se añaden puntos suspensivos.", o)
	esGroup(t, c, o)
	esOrdinal(t, c, o)
	esPercent(t, c, o)
	esEx(t, c, o)
	esWordUnit(t, c, o)
	esRaya(t, c, o)
}

// esInverted is es.punct.inverted: a sentence ending in ? or ! with no
// opening ¿ or ¡ anywhere in it. The question may start mid-sentence ("Si
// llueve, ¿vamos?"), so this flags only. Not headings.
func esInverted(t *Text, c *ctx, o *out) {
	if c.block.isHeading() {
		return
	}
	start := 0
	for i, ch := range t.Chars {
		if strings.ContainsRune(".…\n;", ch) {
			start = i + 1
			continue
		}
		if ch != '?' && ch != '!' {
			continue
		}
		// The end of a run like "?!" or "!!!".
		if t.atIs(i+1, func(r rune) bool { return r == '?' || r == '!' }) {
			continue
		}
		opener := '¡'
		if ch == '?' {
			opener = '¿'
		}
		sentence := t.Chars[start:i]
		if slices.ContainsFunc(sentence, IsAlphabetic) && !slices.Contains(sentence, opener) &&
			!slices.Contains(sentence, '¿') && !slices.Contains(sentence, '¡') && !t.guarded(start, i+1) {
			o.flag("es.punct.inverted", i, i+1, "Falta el signo de apertura «"+string(opener)+"».")
		}
		start = i + 1
	}
}

// esDoubleFinal is es.punct.double-final: `?.` `!.` `….` with nothing
// between -> drop the period. `?».`, `?)`, `?…` are right.
func esDoubleFinal(t *Text, _ *ctx, o *out) {
	for i := 1; i < t.Len(); i++ {
		if t.Chars[i] == '.' && strings.ContainsRune("?!…", t.Chars[i-1]) && !t.atEq(i+1, '.') {
			o.fix("es.punct.double-final", i-1, i+1, string(t.Chars[i-1]), "Tras «?», «!» o «…» no se escribe punto.")
		}
	}
}

var esAbbreviations = []string{"etc", "Sr", "Sra", "Dr", "Dra", "pág", "núm", "art", "Ud", "Uds", "vol", "cap", "admón"}

// wordBefore is the run of letters that ends at i (exclusive).
func wordBefore(t *Text, i int) string {
	a := i
	for a > 0 && IsAlphabetic(t.Chars[a-1]) {
		a--
	}
	return t.slice(a, i)
}

// esPeriodOutside is es.quote.period-outside: `.»` `."` `.”` -> the period
// after the quote.
func esPeriodOutside(t *Text, _ *ctx, o *out) {
	for i := 1; i < t.Len(); i++ {
		q := t.Chars[i]
		if !strings.ContainsRune(`»"”`, q) || t.Chars[i-1] != '.' {
			continue
		}
		if t.atIs(i+1, IsAlphanumeric) {
			continue // an opening straight quote
		}
		if slices.Contains(esAbbreviations, wordBefore(t, i-1)) || t.beforeEq(i-1, '.') {
			continue
		}
		fixed := string(q) + "."
		if t.atEq(i+1, '.') {
			fixed = string(q)
		}
		o.fix("es.quote.period-outside", i-1, i+1, fixed, "El punto va después de las comillas de cierre: «…».")
	}
}

// esMonosyllable is es.word.monosyllable: monosyllables take no accent
// (2010 Ortografía): fué, dió, vió, guión, truhán, rió...
func esMonosyllable(t *Text, _ *ctx, o *out) {
	for _, p := range [][2]string{
		{"fué", "fue"}, {"fuí", "fui"}, {"dió", "dio"}, {"vió", "vio"}, {"guión", "guion"}, {"truhán", "truhan"},
		{"rió", "rio"}, {"crió", "crio"}, {"guió", "guio"}, {"lió", "lio"}, {"fié", "fie"},
	} {
		for _, m := range findWord(t, p[0], true) {
			o.fix("es.word.monosyllable", m[0], m[1], matchCase(t.slice(m[0], m[1]), p[1]), "Los monosílabos no llevan tilde: «"+p[1]+"».")
		}
	}
}

// esSolo is es.word.solo (es.soloAccent=flag): sólo, éste, ése, aquél.
func esSolo(t *Text, c *ctx, o *out) {
	if c.profile.opt("es.soloAccent") != "flag" {
		return
	}
	for _, p := range [][2]string{
		{"sólo", "solo"}, {"éste", "este"}, {"ésta", "esta"}, {"éstos", "estos"}, {"éstas", "estas"}, {"ése", "ese"},
		{"ésa", "esa"}, {"ésos", "esos"}, {"ésas", "esas"}, {"aquél", "aquel"}, {"aquélla", "aquella"},
	} {
		for _, m := range findWord(t, p[0], true) {
			o.fix("es.word.solo", m[0], m[1], matchCase(t.slice(m[0], m[1]), p[1]),
				"La RAE recomienda escribir sin tilde el adverbio «solo» y los demostrativos.")
		}
	}
}

var esMonthNames = []string{"Enero", "Febrero", "Marzo", "Abril", "Mayo", "Junio", "Julio", "Agosto", "Septiembre",
	"Setiembre", "Octubre", "Noviembre", "Diciembre", "Lunes", "Martes", "Miércoles", "Jueves", "Viernes", "Sábado",
	"Domingo", "Primavera", "Verano", "Otoño", "Invierno"}

// capitalisedNames flags capitalised month or day names mid-sentence (es
// and pt): not at a sentence start, not part of a name (Viernes Santo,
// Domingo de Ramos, Sete de Setembro), not after another capitalised word.
func capitalisedNames(t *Text, rule string, names []string, msg string, o *out) {
	startsUpper := func(w string) bool { r, ok := firstRune(w); return ok && isUpper(r) }
	for _, name := range names {
		for _, m := range findWord(t, name, false) {
			a, b := m[0], m[1]
			if t.sentenceStart(a) || t.inQuotes(a, b) {
				continue
			}
			nextCap := false
			for j := b; j < t.Len(); j++ {
				if t.Chars[j] != ' ' {
					nextCap = isUpper(t.Chars[j])
					break
				}
			}
			nextCap = nextCap && t.atEq(b, ' ')
			before := strings.Fields(t.slice(0, a))
			slices.Reverse(before)
			prevCap := len(before) > 0 && startsUpper(before[0]) && !strings.ContainsAny(lastRuneString(before[0]), ".:!?")
			deName := len(before) > 1 && before[0] == "de" && startsUpper(before[1])
			if nextCap || prevCap || deName {
				continue
			}
			o.fix(rule, a, b, toLower(t.slice(a, b)), msg)
		}
	}
}

// lastRuneString is s's last char as a string, "" for "".
func lastRuneString(s string) string {
	if r, ok := lastRune(s); ok {
		return string(r)
	}
	return ""
}

func esMonths(t *Text, _ *ctx, o *out) {
	capitalisedNames(t, "es.word.months", esMonthNames, "Los meses, los días y las estaciones se escriben con minúscula.", o)
}

var etcRe = MustRe(`\betc(?:\.{2,}|\.?…\.?)`)

// etcDots is es.punct.etc and fr.punct.etc: etc... / etc.… -> etc.
func etcDots(t *Text, rule, msg string, o *out) {
	for _, m := range t.find(etcRe) {
		o.fix(rule, m[0], m[1], "etc.", msg)
	}
}

var esGroupedRe = MustRe(`\b\d{1,3}(?:\.\d{3})+\b|\b\d{1,3}(?:,\d{3}){2,}\b`)

// esGroup is es.num.group: `.` or `,` as a thousands separator in 5+
// digit numbers -> a (no-break) space. Four-digit numbers and identifiers
// never. es-419 groups with commas, so there it is never flagged.
func esGroup(t *Text, c *ctx, o *out) {
	if c.profile.regionIs("419") {
		return
	}
	for _, m := range t.find(esGroupedRe) {
		a, b := m[0], m[1]
		digits := 0
		for _, r := range t.Chars[a:b] {
			if isASCIIDigit(r) {
				digits++
			}
		}
		continues := t.atIs(b, func(r rune) bool { return strings.ContainsRune(".,-/", r) }) && t.atIs(b+1, IsAlphanumeric)
		if digits < 5 || continues || t.beforeIs(a, func(r rune) bool { return IsAlphanumeric(r) || r == '.' || r == ',' }) {
			continue
		}
		fixed := strings.Map(func(r rune) rune {
			if r == '.' || r == ',' {
				return '\u00A0'
			}
			return r
		}, t.slice(a, b))
		o.fix("es.num.group", a, b, fixed, "Las cifras de más de cuatro dígitos se separan en grupos de tres con un espacio, no con punto ni coma.")
	}
}

var esOrdinalRe = MustRe(`\b(\d+)([ºª])`)

// esOrdinal is es.num.ordinal: 1º -> 1.º
func esOrdinal(t *Text, _ *ctx, o *out) {
	for _, g := range t.captures(esOrdinalRe) {
		if !g[0].OK || !g[1].OK || t.beforeEq(g[0].A, '.') {
			continue
		}
		a, b := g[0].A, g[0].B
		o.fix("es.num.ordinal", a, b, t.slice(g[1].A, g[1].B)+"."+string(t.Chars[b-1]), "Las abreviaturas de los ordinales llevan punto: «1.º».")
	}
}

// esPercent is es.num.percent: 10% -> 10 % (RAE) under es-ES, or where the
// document mostly spaces it.
func esPercent(t *Text, c *ctx, o *out) {
	if !c.profile.regionIs("ES") && !minority(c.stats.pctTight, c.stats.pctSpaced) {
		return
	}
	for i := 1; i < t.Len(); i++ {
		if t.Chars[i] == '%' && isASCIIDigit(t.Chars[i-1]) {
			o.fix("es.num.percent", i-1, i+1, string(t.Chars[i-1])+"\u00A0%", "El signo de porcentaje se separa de la cifra con un espacio: «10 %».")
		}
	}
}

var esExRe = MustRe(`\b([Ee]x)-(\p{Ll}+)`)

// esEx is es.word.ex: ex-presidente -> expresidente (ex-SS and multi-word
// bases stay).
func esEx(t *Text, _ *ctx, o *out) {
	for _, g := range t.captures(esExRe) {
		if !g[0].OK || !g[1].OK || !g[2].OK {
			continue
		}
		o.fix("es.word.ex", g[0].A, g[0].B, t.slice(g[1].A, g[1].B)+t.slice(g[2].A, g[2].B), "El prefijo «ex-» se une a la palabra: «expresidente».")
	}
}

var esWordUnitRe = MustRe(`(?i)\b(?:uno|dos|tres|cuatro|cinco|seis|siete|ocho|nueve|diez|veinte|treinta|cuarenta|cincuenta|cien|mil)\s(?:km|kg|cm|mm|m|g|l|h|min|s)\b`)

// esWordUnit is es.num.word-unit: a number word with a unit symbol (veinte
// km).
func esWordUnit(t *Text, _ *ctx, o *out) {
	for _, m := range t.find(esWordUnitRe) {
		o.flag("es.num.word-unit", m[0], m[1], "Con símbolos de unidad, la cantidad va en cifras: «20 km».")
	}
}

// esRaya is es.dialogue.raya: a dialogue paragraph opening with - or – ->
// —, and no space after the opening raya.
func esRaya(t *Text, c *ctx, o *out) {
	if c.block == blockItem || c.block == blockCell || t.Len() < 3 {
		return
	}
	ch, d := t.Chars[0], t.Chars[1]
	if (ch == '-' || ch == '–') && (IsAlphabetic(d) || d == '¿' || d == '¡' || d == ' ' && IsAlphabetic(t.Chars[2])) {
		end := 1
		if d == ' ' {
			end = 2
		}
		o.fix("es.dialogue.raya", 0, end, "—", "Los diálogos se abren con raya (—), sin espacio después.")
	} else if ch == '—' && d == ' ' {
		o.fix("es.dialogue.raya", 0, 2, "—", "Tras la raya de diálogo no se deja espacio.")
	}
}
