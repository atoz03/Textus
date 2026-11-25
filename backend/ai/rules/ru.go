package rules

import (
	"slices"
	"strings"
)

// Russian rules. Never flagged: `?..` and `!..`, ё either way, `«В бой!».`
// (a title that ends in ! inside a sentence).

func ruRun(t *Text, c *ctx, o *out) {
	ruQuoteStyle(t, c, o)
	ruPeriodOutside(t, c, o)
	ruExclaimPeriod(t, c, o)
	ruDash(t, c, o)
	ruCommaDash(t, c, o)
	ruAbbr(t, c, o)
	ruEnding(t, c, o)
	ruUnit(t, c, o)
	ruGroup(t, c, o)
	ruPercent(t, c, o)
	ruDialogue(t, c, o)
}

// ruQuoteStyle is ru.quote.style: straight or English quotes in prose ->
// «…»; quotes inside «…» -> „…“.
func ruQuoteStyle(t *Text, _ *ctx, o *out) {
	msg := "В русском тексте кавычки «ёлочки», внутренние — „лапки“."
	var marks []int
	for i, ch := range t.Chars {
		if ch == '"' && !t.Protected[i] {
			marks = append(marks, i)
		}
	}
	if len(marks)%2 == 0 {
		for k := 0; k < len(marks); k += 2 {
			a, b := marks[k], marks[k+1]
			if slices.ContainsFunc(t.Chars[a+1:b], isCyrillic) {
				o.fix("ru.quote.style", a, a+1, "«", msg)
				o.fix("ru.quote.style", b, b+1, "»", msg)
			}
		}
	}
	for i := 0; i < t.Len(); {
		if t.Chars[i] == '“' && t.beforeNoneOr(i, func(r rune) bool { return isSpace(r) || r == '(' }) {
			if j := slices.Index(t.Chars[i+1:], '”'); j >= 0 {
				j += i + 1
				o.fix("ru.quote.style", i, i+1, "«", msg)
				o.fix("ru.quote.style", j, j+1, "»", msg)
				i = j + 1
				continue
			}
		}
		i++
	}
	// «…«…»…»: the inner pair becomes „…“.
	depth, innerOpen := 0, -1
	for i, ch := range t.Chars {
		switch ch {
		case '«':
			depth++
			if depth == 2 {
				innerOpen = i
			}
		case '»':
			if depth == 2 && innerOpen >= 0 {
				o.fix("ru.quote.style", innerOpen, innerOpen+1, "„", msg)
				o.fix("ru.quote.style", i, i+1, "“", msg)
				innerOpen = -1
			}
			depth = max(depth-1, 0)
		}
	}
}

// ruPeriodOutside is ru.quote.period-outside: `.»` -> `».`, `,»` -> `»,`.
func ruPeriodOutside(t *Text, _ *ctx, o *out) {
	msg := "Точка и запятая ставятся после закрывающей кавычки."
	for i := 1; i < t.Len(); i++ {
		p := t.Chars[i-1]
		if t.Chars[i] != '»' || p != '.' && p != ',' || t.beforeEq(i-1, '.') {
			continue
		}
		if t.atEq(i+1, p) {
			o.fix("ru.quote.period-outside", i-1, i+1, "»", msg)
		} else {
			o.fix("ru.quote.period-outside", i-1, i+1, "»"+string(p), msg)
		}
	}
}

// ruExclaimPeriod is ru.quote.exclaim-period: `?».` `!».` - flag only: a
// title ending in ! inside a sentence (пьесу «В бой!».) is right.
func ruExclaimPeriod(t *Text, _ *ctx, o *out) {
	for _, m := range append(findStr(t, "?»."), findStr(t, "!».")...) {
		o.flag("ru.quote.exclaim-period", m[0], m[1], "Если в кавычках вопрос или восклицание, точка после кавычки обычно не нужна (кроме названий).")
	}
}

// ruDash is ru.punct.dash: " - " between words -> " — " with a no-break
// space before it. Ranges between digits and minus signs stay.
func ruDash(t *Text, _ *ctx, o *out) {
	for i := 1; i+1 < t.Len(); i++ {
		if (t.Chars[i] != '-' && t.Chars[i] != '–') || t.Chars[i-1] != ' ' || t.Chars[i+1] != ' ' {
			continue
		}
		left := i >= 2 && (IsAlphabetic(t.Chars[i-2]) || strings.ContainsRune(`»)"`, t.Chars[i-2]))
		right := t.atIs(i+2, func(r rune) bool { return IsAlphabetic(r) || r == '«' })
		if left && right {
			o.fix("ru.punct.dash", i-1, i+1, "\u00A0—", "Тире — длинное (—), перед ним неразрывный пробел.")
		}
	}
}

// ruCommaDash is ru.punct.comma-dash: `—,` -> `, —`.
func ruCommaDash(t *Text, _ *ctx, o *out) {
	for _, m := range findStr(t, "—,") {
		a := m[0]
		if t.beforeEq(a, ' ') {
			a--
		}
		o.fix("ru.punct.comma-dash", a, m[1], ", —", "Запятая ставится перед тире: «, —».")
	}
}

// ruAbbr is ru.word.abbr: т.е. -> т. е., т.д., т.п., т.к., т.н. (no-break
// spaces).
func ruAbbr(t *Text, _ *ctx, o *out) {
	for _, p := range [][2]string{
		{"т.е.", "т.\u00A0е."}, {"т.д.", "т.\u00A0д."}, {"т.п.", "т.\u00A0п."}, {"т.к.", "т.\u00A0к."}, {"т.н.", "т.\u00A0н."},
	} {
		for _, m := range findStr(t, p[0]) {
			if t.beforeIs(m[0], IsAlphabetic) {
				continue
			}
			o.fix("ru.word.abbr", m[0], m[1], p[1], "Сокращение пишется с пробелом: «"+strings.ReplaceAll(p[1], "\u00A0", " ")+"».")
		}
	}
}

var ruEndingRe = MustRe(`(\d)-(ый|ой|ий|ая|ое|ого|ему|ому|ых|ым)\b`)

// ruEnding is ru.num.ending: 5-ый, 2-ой, 5-ого, 3-ий -> 5-й, 2-й, 5-го, 3-й.
func ruEnding(t *Text, _ *ctx, o *out) {
	short := map[string]string{"ый": "й", "ой": "й", "ий": "й", "ая": "я", "ое": "е", "ого": "го", "ему": "му", "ому": "му", "ых": "х", "ым": "м"}
	for _, g := range t.captures(ruEndingRe) {
		if !g[0].OK || !g[2].OK {
			continue
		}
		s, ok := short[t.slice(g[2].A, g[2].B)]
		if !ok {
			continue
		}
		o.fix("ru.num.ending", g[2].A, g[2].B, s, "Наращение после цифры — одна-две буквы: «-"+s+"».")
	}
}

var (
	ruUnitTightRe  = MustRe(`(\d)(кг|км|см|мм|мл|кВт|Вт|ГБ|МБ|мин|ч|м|л)\b`)
	ruUnitPeriodRe = MustRe(`\d\s?(кг|км|см|мм|мл|мин)\.( \p{Ll})`)
)

// ruUnit is ru.num.unit: 5кг -> 5 кг; кг. mid-sentence -> кг. (г. for a
// year is not a unit; a sentence-final period stays.)
func ruUnit(t *Text, _ *ctx, o *out) {
	for _, g := range t.captures(ruUnitTightRe) {
		if !g[0].OK || !g[1].OK || !g[2].OK {
			continue
		}
		o.fix("ru.num.unit", g[0].A, g[0].B, string(t.Chars[g[1].A])+"\u00A0"+t.slice(g[2].A, g[2].B), "Между числом и единицей измерения ставится пробел.")
	}
	for _, g := range t.captures(ruUnitPeriodRe) {
		if g[1].OK {
			o.fix("ru.num.unit", g[1].B, g[1].B+1, "", "После обозначения единицы точка не ставится.")
		}
	}
}

var ruGroupedRe = MustRe(`\b\d{1,3}(?:[.,]\d{3}){2,}\b|\b\d{2,3}\.\d{3}\b`)

// ruGroup is ru.num.group: `1,000,000` or `10.000` as group separators ->
// no-break spaces. `1,5` is a decimal: never.
func ruGroup(t *Text, _ *ctx, o *out) {
	for _, m := range t.find(ruGroupedRe) {
		a, b := m[0], m[1]
		if t.beforeIs(a, func(r rune) bool { return IsAlphanumeric(r) || r == '.' }) ||
			t.atIs(b, func(r rune) bool { return r == '.' || r == ',' }) && t.atIs(b+1, isASCIIDigit) {
			continue
		}
		fixed := strings.Map(func(r rune) rune {
			if r == '.' || r == ',' {
				return '\u00A0'
			}
			return r
		}, t.slice(a, b))
		o.fix("ru.num.group", a, b, fixed, "Разряды отделяются неразрывным пробелом.")
	}
}

// ruPercent is ru.num.percent: `80 %` and `80%` mixed in one document.
func ruPercent(t *Text, c *ctx, o *out) {
	s := c.stats
	spacedMajor := minority(s.pctTight, s.pctSpaced)
	tightMajor := minority(s.pctSpaced, s.pctTight)
	msg := "Знак % в документе пишется то слитно, то раздельно."
	for i := 1; i < t.Len(); i++ {
		if t.Chars[i] != '%' {
			continue
		}
		prev := t.Chars[i-1]
		if spacedMajor && isASCIIDigit(prev) {
			o.fix("ru.num.percent", i-1, i+1, string(prev)+"\u00A0%", msg)
		} else if tightMajor && (prev == ' ' || prev == '\u00A0' || prev == '\u202F') && i >= 2 && isASCIIDigit(t.Chars[i-2]) {
			o.fix("ru.num.percent", i-2, i+1, string(t.Chars[i-2])+"%", msg)
		}
	}
}

// ruDialogue is ru.dialogue.dash: a dialogue paragraph opening with - or –
// -> "— ".
func ruDialogue(t *Text, c *ctx, o *out) {
	if c.block == blockItem || c.block == blockCell || t.Len() < 3 {
		return
	}
	if (t.Chars[0] == '-' || t.Chars[0] == '–') && t.Chars[1] == ' ' && isCyrillic(t.Chars[2]) {
		o.fix("ru.dialogue.dash", 0, 2, "—\u00A0", "Реплика диалога начинается с тире (—).")
	}
}
