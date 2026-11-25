package rules

import (
	"strings"
	"testing"
)

func TestNFCStripsInvisiblesAndComposesButKeepsEmojiJoiners(t *testing.T) {
	eqStr(t, fixed("shared.nfc", En, "", "zero\u200Bwidth and e\u0301té"), "zerowidth and été")
	// ZWJ inside a family emoji stays; a stray one goes.
	none(t, hits("shared.nfc", En, "", "the 👩\u200D👩\u200D👧 emoji"))
	eqStr(t, fixed("shared.nfc", En, "", "a\u200Db"), "ab")
	// Decomposed Hangul composes.
	eqStr(t, fixed("shared.nfc", Ko, "", "\u1112\u1161\u11AB글"), "한글")
	// Compatibility ideographs are zh.compat-ideograph's, not NFC's here.
	none(t, hits("shared.nfc", Ja, "", "\uFA19社"))
}

func TestSpacesBeforePunctuationGoExceptWhereTheyBelong(t *testing.T) {
	eqStr(t, fixed("shared.space-before-punct", En, "", "Hello , world ! Is it ?"), "Hello, world! Is it?")
	eqStr(t, fixed("shared.space-before-punct", Ru, "", "Да , конечно ."), "Да, конечно.")
	for _, ok := range []string{
		"Le prix : dix euros ; voilà !",
		"Ratio 1 : 5 and smile :) or :D",
		"Use .NET and the :root selector",
		"Wait ... what",
	} {
		lang := En
		if strings.HasPrefix(ok, "Le") {
			lang = Fr
		}
		none(t, hits("shared.space-before-punct", lang, "", ok), ok)
	}
	// Korean colons are ko.punct.colon-space's.
	none(t, hits("shared.space-before-punct", Ko, "", "장소 : 광화문"))
	eqStr(t, fixed("shared.space-before-punct", Ko, "", "좋습니다 ."), "좋습니다.")
}

func TestDoubleSpacesAndTheArmyException(t *testing.T) {
	eqStr(t, fixed("shared.double-space", En, "", "two  spaces here"), "two spaces here")
	none(t, hits("shared.double-space", En, "en-US/army", "End.  Next sentence."))
	if n := len(hits("shared.double-space", En, "en-US/army", "a  b")); n != 1 {
		t.Errorf("got %d hits, want 1", n)
	}
}

func TestDoubledWordsWithTheirGuards(t *testing.T) {
	eqPairs(t, hits("shared.doubled-word", En, "", "Check the the value."), one("the the", "the"))
	eqPairs(t, hits("shared.doubled-word", Es, "", "Es de de verdad."), one("de de", "de"))
	for _, ok := range []string{"She had had enough.", "He said that that was fine.", "We flew to Bora Bora.", "the end. The next"} {
		none(t, hits("shared.doubled-word", En, "", ok), ok)
	}
	none(t, hits("shared.doubled-word", De, "", "Ich weiß, dass das das Beste ist."))
	none(t, hits("shared.doubled-word", Fr, "", "Nous nous levons tôt."))
	// Never in CJK: 常常, 看看 are grammar.
	none(t, hits("shared.doubled-word", ZhHans, "", "我常常 常常去看看。"))
}

func TestResidueButNotWhenQuoted(t *testing.T) {
	for _, c := range []struct {
		lang Lang
		text string
		n    int
	}{
		{En, "The plan is ready. I hope this helps!", 1},
		{En, "Best regards, [Your Name]", 1},
		{ZhHans, "会议定于 2025-XX-XX 举行。希望这对您有帮助！", 2},
		{En, "Never end with “I hope this helps” in a report.", 0},
	} {
		if n := len(hits("shared.residue", c.lang, "", c.text)); n != c.n {
			t.Errorf("%s: got %d hits, want %d", c.text, n, c.n)
		}
	}
}

func TestNearMissesOfLibraryNames(t *testing.T) {
	eqPairs(t, hits("shared.library-name", En, "", "Then Elisabeth left."), one("Elisabeth", "Elizabeth"))
	eqPairs(t, hits("shared.library-name", En, "", "Elizbeth's coat."), one("Elizbeth", "Elizabeth"))
	none(t, hits("shared.library-name", En, "", "Elizabeth and Edinburgh."))
	eqPairs(t, hits("shared.library-name", ZhHans, "", "林黛王走进来。"), one("林黛王", "林黛玉"))
	none(t, hits("shared.library-name", ZhHans, "", "林黛玉走进来，林子里很静。"))
}
