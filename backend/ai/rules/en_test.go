package rules

import (
	"strings"
	"testing"
)

const E = En

func count(t *testing.T, got []pair, n int, context ...any) {
	t.Helper()
	if len(got) != n {
		t.Errorf("%v: got %d hits %v, want %d", context, len(got), got, n)
	}
}

func TestMisspellingsKeepCaseAndSpareQuotes(t *testing.T) {
	eqStr(t, fixed("en.word.misspelling", E, "", "Alot of people definately recieve it."), "A lot of people definitely receive it.")
	eqStr(t, fixed("en.word.misspelling", E, "", "We could of won."), "We could have won.")
	none(t, hits("en.word.misspelling", E, "", "We could of course win."))
	none(t, hits("en.word.misspelling", E, "", "Never write “alot” in a report."))
	// Real words that look like misspellings stay.
	none(t, hits("en.word.misspelling", E, "", "The calender rolls the cloth; a wether grazed."))
}

func TestApostrophePluralsButNotPossessives(t *testing.T) {
	eqStr(t, fixed("en.punct.apostrophe-plural", E, "", "In the 1990's we bought two PDF's."), "In the 1990s we bought two PDFs.")
	for _, ok := range []string{"NASA's mission was clear.", "Mind your p's and q's; she got A's.", "The 1990s were loud."} {
		none(t, hits("en.punct.apostrophe-plural", E, "", ok), ok)
	}
}

func TestDoubleHyphensButNotFlags(t *testing.T) {
	eqStr(t, fixed("en.punct.double-hyphen", E, "", "It was late--very late."), "It was late—very late.")
	eqStr(t, fixed("en.punct.double-hyphen", E, "", "It was late -- very late."), "It was late — very late.")
	none(t, hits("en.punct.double-hyphen", E, "", "Run the tool with --verbose to see more."))
}

func TestArticlesBySound(t *testing.T) {
	eqStr(t, fixed("en.word.article", E, "", "It took a hour, an user said, a apple and an university."),
		"It took an hour, a user said, an apple and a university.")
	for _, ok := range []string{"an honest man", "a one-time fee", "a European city", "an umbrella", "a SQL query", "Plan A is fine", "an FAQ page", "a unanimous vote"} {
		none(t, hits("en.word.article", E, "", ok), ok)
	}
}

func TestHeadingsAndSentenceStarts(t *testing.T) {
	eqPairs(t, hitsIn("en.heading.end-punct", E, "", "Getting started:", headingBlock(2)), one(":", ""))
	none(t, hitsIn("en.heading.end-punct", E, "", "Why now?", headingBlock(2)))
	none(t, hitsIn("en.heading.end-punct", E, "", "To be continued...", headingBlock(2)))
	count(t, hits("en.num.sentence-start", E, "", "We met. 12 people came."), 1)
	none(t, hits("en.num.sentence-start", E, "", "We met. 2024 was busy."))
	none(t, hitsIn("en.num.sentence-start", E, "", "3 eggs", blockItem))
}

func TestSpellingVarietyByMajorityOrPreset(t *testing.T) {
	eqPairs(t, hits("en.spelling.variety", E, "", "The colour of the centre and the favourite flavor."), one("flavor", "flavour"))
	none(t, hits("en.spelling.variety", E, "", "The color and the colour."))
	eqPairs(t, hits("en.spelling.variety", E, "en-GB/govuk", "We organize the center."),
		[]pair{{"organize", "organise", true}, {"center", "centre", true}})
	// Oxford spelling keeps -ize; proper nouns stay.
	eqPairs(t, hits("en.spelling.variety", E, "en-GB/oxford", "We organize the center."), one("center", "centre"))
	none(t, hits("en.spelling.variety", E, "en-US/general", "He joined the Labour Party."))
}

func TestSerialCommaOnlyWithAPreset(t *testing.T) {
	text := "We bought apples, pears and plums."
	none(t, hits("en.punct.serial-comma", E, "", text))
	eqStr(t, fixed("en.punct.serial-comma", E, "en-US/general", text), "We bought apples, pears, and plums.")
	eqStr(t, fixed("en.punct.serial-comma", E, "en-US/news", "apples, pears, and plums"), "apples, pears and plums")
}

func TestConsistencyOfDashesQuotesAndClocks(t *testing.T) {
	eqPairs(t, hits("en.punct.dash-style", E, "", "One—two and three—four, five—six, then seven — eight."), one(" — ", "—"))
	count(t, hits("en.punct.quote-style", E, "", "He said “yes” and “no” and then \"maybe\" twice “again”."), 2)
	count(t, hits("en.num.clock", E, "", "Doors at 7 pm, show at 19:30."), 1)
	none(t, hits("en.num.clock", E, "", "Doors at 7 pm, show at 8 pm."))
}

func TestWordListsAndStyleHints(t *testing.T) {
	eqStr(t, fixed("en.word.etc", E, "", "Fruit such as apples, pears, etc. are fine."), "Fruit such as apples, pears are fine.")
	eqStr(t, fixed("en.word.wordy", E, "", "In order to start, prior to noon."), "To start, before noon.")
	eqStr(t, fixed("en.word.inclusive", E, "", "Add it to the whitelist."), "Add it to the allowlist.")
	none(t, hits("en.word.inclusive", E, "", "The film “Fireman” opened."))
	if len(hits("en.word.plain", E, "en-GB/govuk", "We will accomplish this.")) == 0 {
		t.Error("plain words: no hit under GOV.UK")
	}
	none(t, hits("en.word.plain", E, "", "We will accomplish this."))
	count(t, hits("en.word.please", E, "en-US/tech-google", "Please click Save."), 1)
	eqStr(t, fixed("en.govuk.style", E, "en-GB/govuk", "Fish & chips, e.g. cod."), "Fish and chips, for example cod.")
	count(t, hits("en.style.caps", E, "", "THIS IS VERY IMPORTANT for the API."), 1)
	none(t, hits("en.style.caps", E, "", "The USA and the EU signed."))
	count(t, hits("en.style.passive", E, "", "The report was written by Ann."), 1)
	none(t, hits("en.style.passive", E, "", "She was tired and interested."))
	count(t, hits("en.style.ai-vocab", E, "", "We delve into a tapestry of ideas."), 2)
	none(t, hits("en.style.ai-vocab", E, "", "We used robust statistics and financial leverage."))
	eqStr(t, fixed("en.num.minus", E, "", "It fell to -5 degrees."), "It fell to −5 degrees.")
	none(t, hits("en.num.minus", E, "", "Pages 3-5 on 2024-01-02."))
	count(t, hits("en.word.slash", E, "", "Sign and/or return the document(s)."), 2)
}

func TestNeverFlagInEnglish(t *testing.T) {
	text := "To boldly go, the data is clear: she had had enough, and that that was it."
	if all := findings(E, "", text, blockPara); len(all) != 0 {
		t.Errorf("%v", all)
	}
	long := strings.TrimSuffix(strings.Repeat("word ", 30), " ") + "."
	count(t, hits("en.style.sentence-length", E, "", long), 1)
}
