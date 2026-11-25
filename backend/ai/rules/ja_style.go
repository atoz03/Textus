package rules

import (
	"fmt"
	"strings"
)

// Japanese style: polite and plain forms mixed, ra-nuki, repeated
// particles, double negatives, redundancy, long sentences, double
// honorifics and spelling variants.

var (
	jaDesuEndRe  = MustRe(`(?:です|ます|でした|ました|ません|でしょう|ましょう)[。！？]`)
	jaDearuEndRe = MustRe(`(?:である|であった|ではない|だった|のだ|なのだ)[。！？]`)
)

// jaRegisterMix is ja.style.register-mix: です・ます and である in one
// document's body - the minority's sentence endings flagged. Not
// quotations, list items or headings.
func jaRegisterMix(t *Text, c *ctx, o *out) {
	if c.block.isHeading() || c.block == blockItem || c.block == blockCell || c.block == blockQuot {
		return
	}
	s := c.stats
	var re *Re
	var msg string
	switch {
	case minority(s.jaDesu, s.jaDearu):
		re, msg = jaDesuEndRe, "です・ます調とである調が混在しています（本文の多くは「である」調）。"
	case minority(s.jaDearu, s.jaDesu):
		re, msg = jaDearuEndRe, "です・ます調とである調が混在しています（本文の多くは「です・ます」調）。"
	default:
		return
	}
	for _, m := range t.find(re) {
		if !t.inQuotes(m[0], m[1]) {
			o.flag("ja.style.register-mix", m[0], m[1]-1, msg)
		}
	}
}

var jaRanukiStems = []string{"見", "来", "食べ", "寝", "着", "出", "起き", "決め", "考え", "信じ", "変え", "投げ", "降り",
	"借り", "浴び", "答え", "教え"}

// jaRanuki is ja.word.ranuki: ら抜き言葉 from a closed list (見れる ->
// 見られる). Not 見れば (a conditional), not in dialogue or fiction.
func jaRanuki(t *Text, c *ctx, o *out) {
	if c.profile.is("fiction") {
		return
	}
	for _, stem := range jaRanukiStems {
		for _, m := range findStr(t, stem+"れ") {
			a, b := m[0], m[1]
			rest := string(t.Chars[b:min(b+4, t.Len())])
			conj := false
			for _, s := range []string{"る", "ない", "ます", "ません", "た", "なかった", "て"} {
				if strings.HasPrefix(rest, s) {
					conj = true
				}
			}
			// 出れ in 出れる only after a word boundary: 思い出れ is not.
			joined := t.beforeIs(a, func(r rune) bool { return isHan(r) || r == 'い' && stem == "出" })
			if conj && !joined && !t.inQuotes(a, b) {
				o.fix("ja.word.ranuki", a, b, stem+"られ", "ら抜き言葉です。「"+stem+"られ…」が標準的です。")
			}
		}
	}
}

// jaNoRepeat is ja.style.no-repeat: three or more の in one clause.
func jaNoRepeat(t *Text, _ *ctx, o *out) {
	start := 0
	for i := 0; i <= t.Len(); i++ {
		if i < t.Len() && !strings.ContainsRune("、。，．！？「」", t.Chars[i]) {
			continue
		}
		// The particle の follows a noun: not the の of その, この, どの, もの
		// (after hiragana).
		n := 0
		for k := start; k < i; k++ {
			if t.Chars[k] == 'の' && k > 0 && !(t.Chars[k-1] >= 0x3041 && t.Chars[k-1] <= 0x309F) {
				n++
			}
		}
		if n >= 3 {
			o.flag("ja.style.no-repeat", start, i, "「の」が三回以上続いています。言い換えを検討してください。")
		}
		start = i + 1
	}
}

var jaDoubleNegRe = MustRe(`ないわけではない|なくはない|ないこともない|ないとは言えない|ないではない`)

// jaDoubleNegative is ja.style.double-negative.
func jaDoubleNegative(t *Text, _ *ctx, o *out) {
	for _, m := range t.find(jaDoubleNegRe) {
		o.flag("ja.style.double-negative", m[0], m[1], "二重否定は意味が取りにくくなります。")
	}
}

var (
	jaAboutRe = MustRe(`約[^。、]{1,10}?(?:くらい|ぐらい|ほど|程度)`)
	jaEachRe  = MustRe(`各[^。、]{1,10}?ごとに`)
	jaNthRe   = MustRe(`第\d+(?:番|回|日|位)目`)
)

// jaRedundant is ja.word.redundant: することができる, 約…くらい, 各…ごとに,
// 第…目.
func jaRedundant(t *Text, _ *ctx, o *out) {
	for _, m := range findStr(t, "することができ") {
		o.fix("ja.word.redundant", m[0], m[1], "でき", "「することができる」は「できる」で十分です。")
	}
	for _, m := range append(t.find(jaAboutRe), t.find(jaEachRe)...) {
		o.flag("ja.word.redundant", m[0], m[1], "意味が重なっています（「約」と「くらい」、「各」と「ごとに」）。どちらかで十分です。")
	}
	for _, m := range t.find(jaNthRe) {
		o.fix("ja.word.redundant", m[1]-1, m[1], "", "「第…目」は意味が重なっています。「目」を取ります。")
	}
}

// jaSentenceLength is ja.style.sentence-length: over the profile's limit
// (100, 公用文 60).
func jaSentenceLength(t *Text, c *ctx, o *out) {
	if c.profile.is("fiction") || c.block.isHeading() {
		return
	}
	limit := 100
	if c.profile.is("koyobun") {
		limit = 60
	}
	start := 0
	for i := 0; i <= t.Len(); i++ {
		if i < t.Len() && !strings.ContainsRune("。！？．", t.Chars[i]) {
			continue
		}
		n := 0
		for _, r := range t.Chars[start:i] {
			if !isSpace(r) {
				n++
			}
		}
		if n > limit && !t.inQuotes(start, i) {
			o.flag("ja.style.sentence-length", start, i, fmt.Sprintf("一文が長すぎます（%d文字）。%d文字以内を目安に分けてください。", n, limit))
		}
		start = i + 1
	}
}

var jaDoubleHonorificRe = MustRe(`[おご御][\p{Han}\p{Hiragana}]{1,6}?になられ`)

// jaKeigo is ja.keigo.patterns: double honorifics (お〜になられる) and
// させていただく three or more times in a passage.
func jaKeigo(t *Text, _ *ctx, o *out) {
	for _, m := range t.find(jaDoubleHonorificRe) {
		o.flag("ja.keigo.patterns", m[0], m[1], "二重敬語です。「お〜になる」で十分です。")
	}
	if uses := findStr(t, "させていただ"); len(uses) >= 3 {
		for _, m := range uses[2:] {
			o.flag("ja.keigo.patterns", m[0], m[1], "「させていただく」が多用されています。")
		}
	}
}

// jaVariantsRule is ja.word.okurigana and ja.word.kodomo: two spellings of
// one word in a document (取り扱い/取扱い, 子ども/子供) - the minority
// flagged.
func jaVariantsRule(t *Text, c *ctx, o *out) {
	v := c.stats.variants
	for _, fam := range jaVariants {
		counts := make([]int, len(fam.forms))
		best := 0
		for k, f := range fam.forms {
			counts[k] = v[f]
			// The most used, the first on a tie.
			if counts[k] > counts[best] {
				best = k
			}
		}
		top, used, atTop := counts[best], 0, 0
		for _, n := range counts {
			if n > 0 {
				used++
			}
			if n == top {
				atTop++
			}
		}
		if used < 2 || atTop > 1 {
			continue
		}
		rule := "ja.word.okurigana"
		if fam.family == "子ども" {
			rule = "ja.word.kodomo"
		}
		for k, form := range fam.forms {
			if k == best {
				continue
			}
			for _, m := range findStr(t, form) {
				o.fix(rule, m[0], m[1], fam.forms[best], "表記が混在しています（本文の多くは「"+fam.forms[best]+"」）。")
			}
		}
	}
}
