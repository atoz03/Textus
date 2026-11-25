package rules

import "strings"

// Japanese rules, JTF style by default, with 公用文, fiction and technical
// profiles. Never flagged: no space between Japanese and Latin text, a
// single … or ― in 公用文, ！？ in fiction or headings. Spaces are never
// inserted.

func jaRun(t *Text, c *ctx, o *out) {
	jaWidthKana(t, c, o)
	jaHalfwidth(t, c, o)
	jaWidthAlnum(t, c, o)
	jaSpaceTyped(t, c, o)
	jaSpaceFullwidth(t, c, o)
	jaAfterExclaim(t, c, o)
	jaTilde(t, c, o)
	jaCounter(t, c, o)
	jaDotsDash(t, c, o)
	jaSingleLeader(t, c, o)
	jaRegisterMix(t, c, o)
	jaRanuki(t, c, o)
	jaNoRepeat(t, c, o)
	jaDoubleNegative(t, c, o)
	jaRedundant(t, c, o)
	jaSentenceLength(t, c, o)
	jaPeriodBracket(t, c, o)
	jaKeigo(t, c, o)
	jaChoon(t, c, o)
	jaVariantsRule(t, c, o)
	jaKanjiDigit(t, c, o)
}

// isJa is Japanese running text: kana, kanji, Japanese punctuation and
// brackets.
func isJa(c rune) bool {
	return IsCJK(c) || isFullwidthPunct(c) || strings.ContainsRune("ー々〆…―", c)
}
