package rules

// English rules. Automatic has no national norm, so conventions style
// guides disagree on (serial comma, dashes, quotes, -ize/-ise) are checked
// for consistency only unless the user chooses a preset.

func enRun(t *Text, c *ctx, o *out) {
	enMisspelling(t, c, o)
	enApostrophePlural(t, c, o)
	enDoubleHyphen(t, c, o)
	enArticle(t, c, o)
	enHeadingEnd(t, c, o)
	enSpellingVariety(t, c, o)
	enSerialComma(t, c, o)
	enDashStyle(t, c, o)
	enQuoteStyle(t, c, o)
	enSentenceStart(t, c, o)
	enClock(t, c, o)
	enEtc(t, c, o)
	enPhrases(t, c, o)
	enCaps(t, c, o)
	enSentenceLength(t, c, o)
	enPassive(t, c, o)
	enAIVocab(t, c, o)
	enMinus(t, c, o)
	enSlash(t, c, o)
}
