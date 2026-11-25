package rules

import (
	"slices"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// Rules for every language. Messages are in the language of the text.

func sharedRun(t *Text, c *ctx, o *out) {
	sharedNFC(t, c, o)
	sharedSpaceBeforePunct(t, c, o)
	sharedDoubleSpace(t, c, o)
	sharedDoubledWord(t, c, o)
	sharedResidue(t, c, o)
	sharedLibraryName(t, c, o)
}

func isEmoji(c rune) bool {
	return c >= 0x1F000 && c <= 0x1FAFF || c >= 0x2600 && c <= 0x27BF || c >= 0x2B00 && c <= 0x2BFF || c == '\uFE0F'
}

// isJamoTail is a conjoining Hangul vowel or final: decomposed Korean
// syllables.
func isJamoTail(c rune) bool { return c >= 0x1160 && c <= 0x11FF }

// isCompatIdeograph is a compatibility ideograph NFC would change: left to
// zh.compat-ideograph, since Japanese names can depend on them.
func isCompatIdeograph(c rune) bool {
	return c >= 0xF900 && c <= 0xFAFF || c >= 0x2F800 && c <= 0x2FA1F
}

// isCombiningMark is General_Category=Mark.
func isCombiningMark(c rune) bool { return unicode.Is(unicode.M, c) }

// sharedNFC is shared.nfc: invisible and control characters, and
// decomposed accents or Hangul that NFC composes. A zero-width joiner
// inside an emoji sequence is part of the emoji.
func sharedNFC(t *Text, c *ctx, o *out) {
	n := t.Len()
	for i := 0; i < n; {
		ch := t.Chars[i]
		invisible := false
		switch {
		case ch == '\u200B' || ch == '\u2060' || ch == '\uFEFF' || ch == '\u200E' || ch == '\u200F':
			invisible = true
		case ch == '\u200D':
			invisible = !(t.beforeIs(i, isEmoji) && t.atIs(i+1, isEmoji))
		case unicode.IsControl(ch):
			invisible = ch != '\t' && ch != '\n' && ch != '\r'
		}
		if invisible {
			o.fix("shared.nfc", i, i+1, "", message("shared.nfc.invisible", c.messages))
			i++
			continue
		}
		// A base character and the combining marks after it.
		j := i + 1
		for j < n && (isCombiningMark(t.Chars[j]) || isJamoTail(t.Chars[j])) {
			j++
		}
		if j > i+1 && !slices.ContainsFunc(t.Chars[i:j], isCompatIdeograph) {
			run := string(t.Chars[i:j])
			if composed := norm.NFC.String(run); composed != run {
				o.fix("shared.nfc", i, j, composed, message("shared.nfc.compose", c.messages))
			}
		}
		i = j
	}
}

// sharedSpaceBeforePunct is shared.space-before-punct: "word ," -> "word,".
// Not French (which spaces before ; : ! ?), not ratios ("1 : 5"),
// emoticons, ellipses, or marks that start a token (".NET", ":root").
func sharedSpaceBeforePunct(t *Text, c *ctx, o *out) {
	switch c.lang {
	case En, Es, De, Ru, Pt, Ko:
	default:
		return
	}
	n := t.Len()
	for i := 1; i < n; i++ {
		ch := t.Chars[i]
		if !strings.ContainsRune(",.;:?!", ch) {
			continue
		}
		// Korean colons belong to ko.punct.colon-space.
		if c.lang == Ko && ch == ':' {
			continue
		}
		if t.Chars[i-1] != ' ' {
			continue
		}
		s := i - 1
		for s > 0 && t.Chars[s-1] == ' ' {
			s--
		}
		if s == 0 {
			continue
		}
		before := t.Chars[s-1]
		if !(IsAlphanumeric(before) || strings.ContainsRune(`)]"”’»“`, before)) {
			continue
		}
		next, hasNext := t.At(i + 1)
		if hasNext && (IsAlphanumeric(next) || strings.ContainsRune(`.)(-/\=_*`, next)) {
			continue
		}
		if hasNext && strings.ContainsRune("DPpOo3|", next) {
			continue
		}
		// "1 : 5", "Halle 2 ; Stand 4": a mark between digits is a ratio or
		// a separator someone spaced on purpose.
		if isASCIIDigit(before) && (ch == ':' || ch == ';') {
			continue
		}
		if ch == '.' && (t.beforeEq(s, '.') || hasNext && next == '.') {
			continue
		}
		o.fix("shared.space-before-punct", s, i+1, string(ch), message("shared.space-before-punct", c.messages))
	}
}

// sharedDoubleSpace is shared.double-space: two or more spaces between
// words. A typewriter's two spaces after a sentence are the army
// correspondence preset's rule.
func sharedDoubleSpace(t *Text, c *ctx, o *out) {
	n := t.Len()
	for i := 1; i < n; {
		if t.Chars[i] == ' ' && t.Chars[i-1] != ' ' && !isSpace(t.Chars[i-1]) {
			j := i
			for j < n && t.Chars[j] == ' ' {
				j++
			}
			afterSentence := strings.ContainsRune(".?!", t.Chars[i-1])
			if j-i >= 2 && j < n && !isSpace(t.Chars[j]) && !(afterSentence && c.profile.is("army")) {
				fixed := string(t.Chars[i-1]) + " " + string(t.Chars[j])
				o.fix("shared.double-space", i-1, j+1, fixed, message("shared.double-space", c.messages))
			}
			i = j
		} else {
			i++
		}
	}
}

// doublingAllowed is a doubled word that is grammatical or idiomatic.
func doublingAllowed(lang Lang, word string) bool {
	var list []string
	switch lang {
	case En:
		list = []string{"had", "that", "is", "do", "does", "did", "very", "so", "no", "ha", "bye", "now",
			"there", "really", "far", "knock", "tut", "chop", "boo", "hush", "pom", "yeah"}
	case Fr:
		list = []string{"nous", "vous", "si"}
	case De:
		list = []string{"das", "die", "der", "sie", "es", "den"}
	case Es, Pt:
		list = []string{"que", "se"}
	case Ru:
		list = []string{"что", "так", "да", "нет"}
	}
	return slices.Contains(list, word)
}

// sharedDoubledWord is shared.doubled-word: "the the" -> "the" (Latin and
// Cyrillic scripts only; reduplication is grammar in Chinese and
// Japanese). Suggest only.
func sharedDoubledWord(t *Text, c *ctx, o *out) {
	switch c.lang {
	case ZhHans, ZhHant, Ja, Ko:
		return
	}
	words := t.words()
	for k := 0; k+1 < len(words); k++ {
		a1, b1, a2, b2 := words[k][0], words[k][1], words[k+1][0], words[k+1][1]
		if a2 == b1 || slices.ContainsFunc(t.Chars[b1:a2], func(ch rune) bool { return ch != ' ' }) {
			continue
		}
		w1, w2 := t.slice(a1, b1), t.slice(a2, b2)
		if toLower(w1) != toLower(w2) {
			continue
		}
		alphabetic := !strings.ContainsFunc(w1, func(ch rune) bool {
			return !(isLatinLetter(ch) || isCyrillic(ch) || ch == '\'' || ch == '’')
		})
		if !alphabetic || doublingAllowed(c.lang, toLower(w1)) {
			continue
		}
		// "Bora Bora", "Sing Sing": a capitalised pair is a name.
		capital := func(w string) bool { r, ok := firstRune(w); return ok && isUpper(r) }
		if capital(w1) && capital(w2) {
			continue
		}
		o.fix("shared.doubled-word", a1, b2, w1, message("shared.doubled-word", c.messages, w1))
	}
}

var residuePhrases = []string{
	"as an ai language model",
	"as an ai model",
	"as a large language model",
	"i hope this helps",
	"let me know if you'd like",
	"let me know if you would like",
	"certainly! here",
	"sure! here",
	"here is the revised",
	"here's the revised",
	"希望这对您有帮助",
	"希望对你有所帮助",
	"希望对您有所帮助",
	"希望这能帮到你",
	"希望這對您有幫助",
	"作为一个ai语言模型",
	"作为一个人工智能",
	"作為一個人工智慧",
	"以下是修改后的",
	"以下是修改後的",
	"ai 언어 모델로서",
	"como modelo de lenguaje",
	"en tant que modèle de langage",
	"als ki-sprachmodell",
	"как языковая модель",
	"como um modelo de linguagem",
}

var placeholderRe = MustRe(`(?i)\[(?:your|insert|company|recipient|date|name|first name|last name|title|address|phone|email)[^\]\n]{0,30}\]|\b(?:INSERT|PLACEHOLDER)_[A-Z_]+\b|\b(?:20[0-9X]{2}|XXXX)-XX-XX\b|[［\[【](?:您的|你的|貴方の|あなたの)?(?:姓名|名字|名前|公司名称|公司名稱|会社名|日期|日付|联系方式|聯絡方式)[］\]】]|XXXX年XX月XX日|20XX年|○○年○○月○○日`)

// sharedResidue is shared.residue: chatbot phrases and placeholders left in
// body text. Quoted text is someone talking about the phrase, not residue.
func sharedResidue(t *Text, c *ctx, o *out) {
	// Lowercased char by char; İ is the one char whose lowercase is two,
	// and with it the offsets would no longer line up.
	if !slices.Contains(t.Chars, 'İ') {
		lower := make([]rune, t.Len())
		for i, ch := range t.Chars {
			lower[i] = unicode.ToLower(ch)
		}
		for _, phrase := range residuePhrases {
			p := []rune(phrase)
			for i := 0; i+len(p) <= len(lower); i++ {
				if slices.Equal(lower[i:i+len(p)], p) && !t.inQuotes(i, i+len(p)) {
					o.flag("shared.residue", i, i+len(p), message("shared.residue", c.messages))
				}
			}
		}
	}
	for _, m := range t.find(placeholderRe) {
		if !t.inQuotes(m[0], m[1]) {
			o.flag("shared.residue", m[0], m[1], message("shared.residue", c.messages))
		}
	}
}

func editDistance(a, b []rune) int {
	prev := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i, ca := range a {
		cur := []int{i + 1}
		for j, cb := range b {
			sub := prev[j]
			if ca != cb {
				sub++
			}
			cur = append(cur, min(sub, prev[j+1]+1, cur[j]+1))
		}
		prev = cur
	}
	return prev[len(b)]
}

func lowerRunes(rs []rune) []rune {
	out := make([]rune, len(rs))
	for i, r := range rs {
		out[i] = unicode.ToLower(r)
	}
	return out
}

// sharedLibraryName is shared.library-name: a near miss of a library name
// ("Elisabeth" for "Elizabeth", "林黛王" for "林黛玉"): one or two edits in a
// Latin name, one character with the surname right in a three-character
// Chinese one.
func sharedLibraryName(t *Text, c *ctx, o *out) {
	if len(c.library) == 0 {
		return
	}
	known := make([]string, len(c.library))
	for i, n := range c.library {
		known[i] = toLower(n)
	}
	words := t.words()
	for _, name := range c.library {
		nc := []rune(name)
		if len(nc) >= 4 && !slices.ContainsFunc(nc, func(r rune) bool { return !isLatinLetter(r) }) && isUpper(nc[0]) {
			limit := 1
			if len(nc) > 6 {
				limit = 2
			}
			for _, wr := range words {
				a, b := wr[0], wr[1]
				w := slices.Clone(t.Chars[a:b])
				if len(w) > 2 && (w[len(w)-2] == '\'' || w[len(w)-2] == '’') && w[len(w)-1] == 's' {
					w = w[:len(w)-2]
				}
				diff := len(w) - len(nc)
				if diff < 0 {
					diff = -diff
				}
				if len(w) < 3 || !isUpper(w[0]) || w[0] != nc[0] || slices.Contains(known, toLower(string(w))) || diff > limit {
					continue
				}
				if d := editDistance(lowerRunes(w), lowerRunes(nc)); d >= 1 && d <= limit {
					o.fix("shared.library-name", a, a+len(w), name, message("shared.library-name", c.messages, name))
				}
			}
		} else if len(nc) == 3 && !slices.ContainsFunc(nc, func(r rune) bool { return !isHan(r) }) {
			n := t.Len()
			for i := 0; i+3 <= n; i++ {
				w := t.Chars[i : i+3]
				if slices.ContainsFunc(w, func(r rune) bool { return !isHan(r) }) || w[0] != nc[0] {
					continue
				}
				diff := 0
				for k := range 3 {
					if w[k] != nc[k] {
						diff++
					}
				}
				clear := !slices.ContainsFunc(findStr(t, name), func(m [2]int) bool { return !(m[1] <= i || m[0] >= i+3) })
				if diff == 1 && !slices.Contains(known, string(w)) && clear {
					o.fix("shared.library-name", i, i+3, name, message("shared.library-name", c.messages, name))
				}
			}
		}
	}
}
