package ai

import (
	"slices"
	"strings"
	"unicode"
)

// Which language a text is in: script ratios, the Simplified/Traditional
// pair table, and frequent-word lists for the Latin-script languages.

// lang is one of the ten document languages Textus AI knows.
type lang string

const (
	langEn     lang = "en"
	langZhHans lang = "zh-Hans"
	langZhHant lang = "zh-Hant"
	langJa     lang = "ja"
	langKo     lang = "ko"
	langEs     lang = "es"
	langFr     lang = "fr"
	langDe     lang = "de"
	langRu     lang = "ru"
	langPt     lang = "pt"
)

// uiCode is the UI language whose words a text in l uses ("zh" is
// Simplified Chinese).
func (l lang) uiCode() string {
	if l == langZhHans {
		return "zh"
	}
	return string(l)
}

func (l lang) isChinese() bool { return l == langZhHans || l == langZhHant }

// uiLanguages are the app's UI languages, with the English name a prompt
// calls each by.
var uiLanguages = []struct{ code, english string }{
	{"en", "English"}, {"zh", "Chinese"}, {"zh-Hant", "Traditional Chinese"}, {"ja", "Japanese"},
	{"ko", "Korean"}, {"es", "Spanish"}, {"fr", "French"}, {"de", "German"}, {"ru", "Russian"},
	{"pt", "Portuguese"},
}

// englishName is a UI language's English name, "" for an unknown code.
func englishName(code string) string {
	for _, l := range uiLanguages {
		if l.code == code {
			return l.english
		}
	}
	return ""
}

// Characters that exist in only one of the two Chinese scripts, pair by
// pair, common enough that any real paragraph has a few.
const (
	hansOnly = "们这个说为学国时会来对发经过还见长门问开关买东车电话体点变让从后里与实现观应该样间题结论议认识历区页写读书图数据计划试验价钱业产统级网络报告"
	hantOnly = "們這個說為學國時會來對發經過還見長門問開關買東車電話體點變讓從後裡與實現觀應該樣間題結論議認識歷區頁寫讀書圖數據計劃試驗價錢業產統級網絡報告"
)

// chineseScript is Simplified or Traditional, by the characters only one
// script has; Simplified when nothing tells.
func chineseScript(text string) lang {
	hans, hant := 0, 0
	for _, r := range text {
		if strings.ContainsRune(hansOnly, r) {
			hans++
		}
		if strings.ContainsRune(hantOnly, r) {
			hant++
		}
	}
	if hant > hans {
		return langZhHant
	}
	return langZhHans
}

// latinWords tell the Latin-script languages apart: words frequent in one
// language and rare in the others.
var latinWords = []struct {
	lang  lang
	words []string
}{
	{langEn, strings.Fields("the and of to is that it with for this are was be on not you have we they which will from but")},
	{langEs, strings.Fields("el los las y del una es con se más pero muy pues aquí también está están sus hay fue este la lo por su al como")},
	{langFr, strings.Fields("le les des et est une pour dans pas sur du avec au ce qui nous vous sont mais plus être cette aux la je tu il elle avant très sans")},
	{langDe, strings.Fields("der die das und ist nicht ein eine zu mit den von auf für sich dem auch wir es sind wird werden")},
	{langPt, strings.Fields("os do da em um uma não é com dos das mais mas como também são está foi pelo pela nos ao seu e ali depois isso muito ainda o na você pra seus suas")},
}

type scriptCounts struct{ han, kana, hangul, cyrillic, latin int }

func countScripts(text string) (c scriptCounts) {
	for _, r := range text {
		switch {
		case r >= 0x4e00 && r <= 0x9fff, r >= 0x3400 && r <= 0x4dbf:
			c.han++
		case r >= 0x3040 && r <= 0x30ff:
			c.kana++
		case r >= 0xac00 && r <= 0xd7af, r >= 0x1100 && r <= 0x11ff:
			c.hangul++
		case r >= 0x0400 && r <= 0x04ff:
			c.cyrillic++
		case isAlphabetic(r) && (r < 0x80 || r >= 0xc0 && r <= 0x24f):
			c.latin++
		}
	}
	return c
}

// isAlphabetic is the Unicode Alphabetic property.
func isAlphabetic(r rune) bool {
	return unicode.IsLetter(r) || unicode.Is(unicode.Nl, r) || unicode.Is(unicode.Other_Alphabetic, r)
}

// isAlphanumeric is Alphabetic or a number.
func isAlphanumeric(r rune) bool { return isAlphabetic(r) || unicode.IsNumber(r) }

// lines are text's lines without their line ends, as a reader counts them:
// no empty last line after a final line break.
func lines(text string) []string {
	out := strings.Split(text, "\n")
	if out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	for i, l := range out {
		out[i] = strings.TrimSuffix(l, "\r")
	}
	return out
}

// proseOnly drops code spans and fences, URLs and math: they say nothing
// about the language and would skew every ratio towards Latin.
func proseOnly(text string) string {
	var out strings.Builder
	inFence := false
	for _, line := range lines(text) {
		trimmed := strings.TrimLeftFunc(line, unicode.IsSpace)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		inCode := false
		for i, part := range strings.Split(line, "`") {
			if i > 0 {
				inCode = !inCode
			}
			if inCode {
				continue
			}
			for _, word := range strings.Fields(part) {
				if !strings.Contains(word, "://") && !strings.HasPrefix(word, "$") {
					out.WriteString(word)
					out.WriteByte(' ')
				}
			}
		}
		out.WriteByte('\n')
	}
	return out.String()
}

// countAny is how many of text's characters are among chars.
func countAny(text, chars string) int {
	n := 0
	for _, r := range text {
		if strings.ContainsRune(chars, r) {
			n++
		}
	}
	return n
}

// detect is the best guess at the language of text; false when there is
// too little prose to tell (a number, a single name).
func detect(text string) (lang, bool) {
	prose := proseOnly(text)
	c := countScripts(prose)
	cjk := c.han + c.kana
	total := cjk + c.hangul + c.cyrillic + c.latin
	switch {
	case total < 6:
		return "", false
	case c.hangul*3 > total:
		return langKo, true
	// Japanese mixes kanji with kana; even kanji-heavy prose has particles.
	case c.kana >= 3 && c.kana*8 > cjk:
		return langJa, true
	// CJK characters carry roughly a word each, Latin letters a fifth of
	// one - so Chinese with English terms still counts as Chinese.
	case cjk*5 >= c.latin && cjk >= 4:
		return chineseScript(prose), true
	case c.cyrillic > c.latin:
		return langRu, true
	}
	lower := strings.ToLower(prose)
	words := strings.FieldsFunc(lower, func(r rune) bool { return !isAlphabetic(r) })
	// No evidence, or a tie between languages: too little to tell, rather
	// than defaulting to English or whichever list came first.
	best, bestScore, tied := langEn, 0.0, false
	for _, l := range latinWords {
		score := 0.0
		for _, w := range words {
			if slices.Contains(l.words, w) {
				score++
			}
		}
		// Letters only one of these languages uses.
		switch l.lang {
		case langEs:
			score += float64(countAny(lower, "ñ¿¡")) * 2
		case langPt:
			// ã õ, ção, and digraphs Spanish doesn't have.
			score += float64(countAny(lower, "ãõ"))*2 + float64(strings.Count(lower, "ção")) +
				float64(strings.Count(lower, "lh")+strings.Count(lower, "nh"))
		case langDe:
			score += float64(countAny(lower, "ßäöü")) * 1.5
		case langFr:
			score += float64(countAny(lower, "èêàœùû")) * 1.5
		}
		if score > bestScore {
			best, bestScore, tied = l.lang, score, false
		} else if score > 0 && score == bestScore {
			tied = true
		}
	}
	return best, bestScore > 0 && !tied
}

// guess is detect, or, for a text too short to tell, a guess from the
// scripts alone: kana is Japanese, Hangul Korean, ideographs Chinese,
// Cyrillic Russian. Short Latin-script text stays unknown.
func guess(text string) (lang, bool) {
	if l, ok := detect(text); ok {
		return l, true
	}
	c := countScripts(text)
	switch {
	case c.kana > 0:
		return langJa, true
	case c.hangul > 0 && c.hangul >= c.han:
		return langKo, true
	case c.han > 0:
		return chineseScript(text), true
	case c.cyrillic > c.latin:
		return langRu, true
	}
	return "", false
}
