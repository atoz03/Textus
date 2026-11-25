package ai

import (
	"slices"
	"strings"
	"unicode/utf8"

	"textus/backend/ai/rules"
)

// Which language a document is in, and which one a request asks to
// translate into - what picks a pack's language.

// DocumentLanguage is the pack language for a document: read from its
// text, else the UI language's, else none.
func DocumentLanguage(document, uiLanguage string) DocLanguage {
	if code := detectPackLanguage(document); code != "" {
		return DocLanguage{code, Detected}
	}
	for _, l := range PackLanguages {
		if l.UI == uiLanguage {
			return DocLanguage{l.Code, FromUILanguage}
		}
	}
	return DocLanguage{}
}

// detectPackLanguage is the pack language of a document's prose; "" when
// there is too little to tell.
func detectPackLanguage(text string) string {
	lang, ok := rules.Detect(text)
	if !ok {
		return ""
	}
	return strings.ToLower(string(lang))
}

// languageNames are the pack languages' names in the UI languages, lower
// case. Latin and Cyrillic entries match at a word start, so inflected
// forms count; CJK anywhere.
var languageNames = []struct {
	code  string
	names []string
}{
	{"en", []string{"english", "英语", "英文", "英語", "영어", "inglés", "ingles", "anglais", "englisch", "англ", "inglês"}},
	{"zh-hans", []string{"simplified chinese", "chinese", "mandarin", "中文", "汉语", "汉文", "简体", "簡體", "中国語", "중국어", "chino", "chinois", "chinesisch", "китайск", "chinês"}},
	{"zh-hant", []string{"traditional chinese", "繁体", "繁體", "正體", "台湾", "台灣", "臺灣", "번체", "chino tradicional", "chinois traditionnel", "traditionelles chinesisch", "traditionellem chinesisch", "традиционный китайский", "chinês tradicional"}},
	{"ja", []string{"japanese", "日语", "日文", "日語", "日本語", "일본어", "japonés", "japones", "japonais", "japanisch", "японск", "japonês"}},
	{"ko", []string{"korean", "韩语", "韩文", "韓語", "韓文", "韓国語", "한국어", "coreano", "coréen", "koreanisch", "корейск"}},
	{"es", []string{"spanish", "西班牙语", "西班牙文", "西班牙語", "スペイン語", "스페인어", "español", "castellano", "espagnol", "spanisch", "испанск", "espanhol"}},
	{"fr", []string{"french", "法语", "法文", "法語", "フランス語", "프랑스어", "francés", "frances", "français", "französisch", "французск", "francês"}},
	{"de", []string{"german", "德语", "德文", "德語", "ドイツ語", "독일어", "alemán", "aleman", "allemand", "deutsch", "немецк", "alemão"}},
	{"ru", []string{"russian", "俄语", "俄文", "俄語", "ロシア語", "러시아어", "ruso", "russe", "russisch", "русск", "russo"}},
	{"pt", []string{"portuguese", "葡萄牙语", "葡萄牙文", "葡萄牙語", "ポルトガル語", "포르투갈어", "portugués", "portugais", "portugiesisch", "португальск", "português", "portugues"}},
}

// targetMarkers are words just before a translation's target ("into",
// "成", "に"…), to pick it when a request names two languages.
var targetMarkers = []string{
	"into ", "to ", "成", "为", "為", "到", "に", "로 ", "으로 ", "al ", "a ", "en ", "auf ",
	"ins ", "на ", "para ", "em ", "para o ", "vers ", "in ",
}

type nameHit struct {
	start, end int
	code       string
}

// TargetLanguage is the pack language a translation request asks for,
// when it names one - or, naming several, marks one as the target.
func TargetLanguage(request string) string {
	lower := strings.ToLower(request)
	var found []nameHit
	for _, l := range languageNames {
		for _, name := range l.names {
			first, _ := utf8.DecodeRuneInString(name)
			latin := rules.IsAlphabetic(first) && first < 0x2e80
			for from := 0; ; {
				at := strings.Index(lower[from:], name)
				if at < 0 {
					break
				}
				at += from
				prev, _ := utf8.DecodeLastRuneInString(lower[:at])
				if !latin || at == 0 || !rules.IsAlphanumeric(prev) {
					found = append(found, nameHit{at, at + len(name), l.code})
				}
				from = at + len(name)
			}
		}
	}
	// A name inside a longer one ("chinese" in "traditional chinese")
	// loses to it.
	all := slices.Clone(found)
	found = slices.DeleteFunc(found, func(h nameHit) bool {
		return slices.ContainsFunc(all, func(o nameHit) bool {
			return o.start <= h.start && h.end <= o.end && o.end-o.start > h.end-h.start
		})
	})
	// "繁體中文" names 繁體 and 中文 side by side: one name.
	slices.SortFunc(found, func(a, b nameHit) int {
		if a.start != b.start {
			return a.start - b.start
		}
		if a.end != b.end {
			return a.end - b.end
		}
		return strings.Compare(a.code, b.code)
	})
	var merged []nameHit
	for _, h := range found {
		if n := len(merged); n > 0 && h.start <= merged[n-1].end {
			last := &merged[n-1]
			switch {
			case last.code == "zh-hant" && h.code == "zh-hans":
				last.end = max(last.end, h.end)
				continue
			case last.code == "zh-hans" && h.code == "zh-hant":
				*last = nameHit{last.start, max(last.end, h.end), "zh-hant"}
				continue
			}
		}
		merged = append(merged, h)
	}
	var codes []string
	for _, m := range merged {
		codes = append(codes, m.code)
	}
	codes = slices.Compact(codes)
	switch len(codes) {
	case 0:
		return ""
	case 1:
		return codes[0]
	}
	var marked []string
	for _, m := range merged {
		if slices.ContainsFunc(targetMarkers, func(t string) bool { return strings.HasSuffix(lower[:m.start], t) }) {
			marked = append(marked, m.code)
		}
	}
	if len(marked) > 0 && !slices.ContainsFunc(marked, func(c string) bool { return c != marked[0] }) {
		return marked[0]
	}
	return ""
}
