package rules

import (
	"slices"
	"strings"
	"unicode"
)

// Which language a text is in: script ratios, the Simplified/Traditional
// pair table, and frequent-word lists for the Latin-script languages.
// Shared with the inline completion, so both agree on what a text "is".

// Lang is one of the ten document languages Textus AI knows; its value is
// the key profiles and settings use.
type Lang string

const (
	En     Lang = "en"
	ZhHans Lang = "zh-Hans"
	ZhHant Lang = "zh-Hant"
	Ja     Lang = "ja"
	Ko     Lang = "ko"
	Es     Lang = "es"
	Fr     Lang = "fr"
	De     Lang = "de"
	Ru     Lang = "ru"
	Pt     Lang = "pt"
)

// Langs are the ten, in the order the engine lists them.
var Langs = []Lang{ZhHans, ZhHant, En, Ja, Ko, Es, Fr, De, Ru, Pt}

// LangFromCode is the language of a profile key, any case.
func LangFromCode(code string) (Lang, bool) {
	for _, l := range Langs {
		if strings.EqualFold(string(l), code) {
			return l, true
		}
	}
	return "", false
}

// UICode is the UI language whose words a text in l uses ("zh" is
// Simplified Chinese).
func (l Lang) UICode() string {
	if l == ZhHans {
		return "zh"
	}
	return string(l)
}

// IsChinese is either Chinese script.
func (l Lang) IsChinese() bool { return l == ZhHans || l == ZhHant }

// UILanguages are the app's UI languages, with the English name a prompt
// calls each by.
var UILanguages = []struct{ Code, English string }{
	{"en", "English"}, {"zh", "Chinese"}, {"zh-Hant", "Traditional Chinese"}, {"ja", "Japanese"},
	{"ko", "Korean"}, {"es", "Spanish"}, {"fr", "French"}, {"de", "German"}, {"ru", "Russian"},
	{"pt", "Portuguese"},
}

// EnglishName is a UI language's English name, "" for an unknown code.
func EnglishName(code string) string {
	for _, l := range UILanguages {
		if l.Code == code {
			return l.English
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

// ChineseScript is Simplified or Traditional, by the characters only one
// script has; Simplified when nothing tells.
func ChineseScript(text string) Lang {
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
		return ZhHant
	}
	return ZhHans
}

// latinWords tell the Latin-script languages apart: words frequent in one
// language and rare in the others.
var latinWords = []struct {
	lang  Lang
	words []string
}{
	{En, strings.Fields("the and of to is that it with for this are was be on not you have we they which will from but")},
	{Es, strings.Fields("el los las y del una es con se más pero muy pues aquí también está están sus hay fue este la lo por su al como")},
	{Fr, strings.Fields("le les des et est une pour dans pas sur du avec au ce qui nous vous sont mais plus être cette aux la je tu il elle avant très sans")},
	{De, strings.Fields("der die das und ist nicht ein eine zu mit den von auf für sich dem auch wir es sind wird werden")},
	{Pt, strings.Fields("os do da em um uma não é com dos das mais mas como também são está foi pelo pela nos ao seu e ali depois isso muito ainda o na você pra seus suas")},
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
		case IsAlphabetic(r) && (r < 0x80 || r >= 0xc0 && r <= 0x24f):
			c.latin++
		}
	}
	return c
}

// IsAlphabetic is the Unicode Alphabetic property (Rust's
// char::is_alphabetic).
func IsAlphabetic(r rune) bool {
	return unicode.IsLetter(r) || unicode.Is(unicode.Nl, r) || unicode.Is(unicode.Other_Alphabetic, r)
}

// IsAlphanumeric is Alphabetic or a number (Rust's char::is_alphanumeric).
func IsAlphanumeric(r rune) bool { return IsAlphabetic(r) || unicode.IsNumber(r) }

// Lines are text's lines without their line ends, as Rust's str::lines
// counts them: no empty last line after a final line break.
func Lines(text string) []string {
	if text == "" {
		return nil
	}
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
	for _, line := range Lines(text) {
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

// Detect is the best guess at the language of text; false when there is
// too little prose to tell (a number, a single name).
func Detect(text string) (Lang, bool) {
	prose := proseOnly(text)
	c := countScripts(prose)
	cjk := c.han + c.kana
	total := cjk + c.hangul + c.cyrillic + c.latin
	switch {
	case total < 6:
		return "", false
	case c.hangul*3 > total:
		return Ko, true
	// Japanese mixes kanji with kana; even kanji-heavy prose has particles.
	case c.kana >= 3 && c.kana*8 > cjk:
		return Ja, true
	// CJK characters carry roughly a word each, Latin letters a fifth of
	// one - so Chinese with English terms still counts as Chinese.
	case cjk*5 >= c.latin && cjk >= 4:
		return ChineseScript(prose), true
	case c.cyrillic > c.latin:
		return Ru, true
	}
	lower := strings.ToLower(prose)
	words := strings.FieldsFunc(lower, func(r rune) bool { return !IsAlphabetic(r) })
	// No evidence, or a tie between languages: too little to tell, rather
	// than defaulting to English or whichever list came first.
	best, bestScore, tied := En, 0.0, false
	for _, l := range latinWords {
		score := 0.0
		for _, w := range words {
			if slices.Contains(l.words, w) {
				score++
			}
		}
		// Letters only one of these languages uses.
		switch l.lang {
		case Es:
			score += float64(countAny(lower, "ñ¿¡")) * 2
		case Pt:
			// ã õ, ção, and digraphs Spanish doesn't have.
			score += float64(countAny(lower, "ãõ"))*2 + float64(strings.Count(lower, "ção")) +
				float64(strings.Count(lower, "lh")+strings.Count(lower, "nh"))
		case De:
			score += float64(countAny(lower, "ßäöü")) * 1.5
		case Fr:
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

// Guess is Detect, or, for a text too short to tell, a guess from the
// scripts alone: kana is Japanese, Hangul Korean, ideographs Chinese,
// Cyrillic Russian. Short Latin-script text stays unknown.
func Guess(text string) (Lang, bool) {
	if l, ok := Detect(text); ok {
		return l, true
	}
	c := countScripts(text)
	switch {
	case c.kana > 0:
		return Ja, true
	case c.hangul > 0 && c.hangul >= c.han:
		return Ko, true
	case c.han > 0:
		return ChineseScript(text), true
	case c.cyrillic > c.latin:
		return Ru, true
	}
	return "", false
}
