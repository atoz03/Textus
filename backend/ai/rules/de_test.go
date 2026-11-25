package rules

import "testing"

const D = De

func TestGermanQuotesAndReportingCommas(t *testing.T) {
	eqStr(t, fixed("de.quote.style", D, "", "Er sagte \"Hallo\" und “Tschüss”."), "Er sagte „Hallo“ und „Tschüss“.")
	none(t, hits("de.quote.style", D, "", "Er sagte „Hallo“ und »Tschüss«."))
	eqStr(t, fixed("de.quote.style", D, "de-CH", "Er sagte „Hallo“."), "Er sagte «Hallo».")
	eqStr(t, fixed("de.quote.reporting-comma", D, "", "„Komm her“ sagte er."), "„Komm her“, sagte er.")
	none(t, hits("de.quote.reporting-comma", D, "", "„Unterhaltungen“ meint im Folgenden den KI-Bereich."))
	eqStr(t, fixed("de.quote.reporting-comma", D, "", "„Komm her.“, sagte er."), "„Komm her“, sagte er.")
}

func TestGermanDashesAbbreviationsAndVariants(t *testing.T) {
	eqStr(t, fixed("de.punct.dash", D, "", "Er kam - und ging."), "Er kam – und ging.")
	none(t, hits("de.punct.dash", D, "", "Seiten 10 - 12"))
	eqStr(t, fixed("de.word.abbr-space", D, "", "Obst, z.B. Äpfel"), "Obst, z. B. Äpfel")
	none(t, hits("de.word.abbr-space", D, "", "Obst usw. und z. B. Äpfel"))
	eqStr(t, fixed("de.word.removed-variant", D, "", "Jogurt mit Spagetti"), "Joghurt mit Spaghetti")
}

func TestGermanNumbersAndPercentCompounds(t *testing.T) {
	eqStr(t, fixed("de.num.percent", D, "", "Es sind 5% mehr."), "Es sind 5 % mehr.")
	for _, ok := range []string{"die 5%-Klausel", "100%ig sicher", "eine 25%ige Lösung"} {
		none(t, hits("de.num.percent", D, "", ok), ok)
	}
	eqStr(t, fixed("de.num.group", D, "", "Es kamen 12000 Besucher."), "Es kamen 12 000 Besucher.")
	none(t, hits("de.num.group", D, "", "10115 Berlin, im Jahr 2026"))
	eqStr(t, fixed("de.date.zero-pad", D, "de-DE/din5008", "am 1.5.2026"), "am 01.05.2026")
	none(t, hits("de.date.zero-pad", D, "", "am 1.5.2026"))
}

func TestGermanCommasEszettAndGender(t *testing.T) {
	eqStr(t, fixed("de.comma.subordinate", D, "", "Ich weiß dass er kommt."), "Ich weiß, dass er kommt.")
	for _, ok := range []string{"Er lief, so dass er fiel.", "Er tat es ohne dass sie es merkte.", "Was machst du damit?", "Er tut als ob er schliefe."} {
		none(t, hits("de.comma.subordinate", D, "", ok), ok)
	}
	eqStr(t, fixed("de.comma.adversative", D, "", "Nicht heute sondern morgen."), "Nicht heute, sondern morgen.")
	eqStr(t, fixed("de.word.eszett", D, "", "Die Strasse ist groß."), "Die Straße ist groß.")
	// No ß anywhere: it may be Swiss text.
	none(t, hits("de.word.eszett", D, "", "Die Strasse ist gross."))
	none(t, hits("de.word.eszett", D, "de-CH", "Die Strasse ist groß."))
	none(t, hits("de.word.gender-chars", D, "", "Lehrer*innen"))
}

func TestNeverFlagInGerman(t *testing.T) {
	text := "Er kam, und sie ging. Er versuchte zu helfen. Seiten 10–12, die 5%-Klausel."
	if all := findings(D, "", text, blockPara); len(all) != 0 {
		t.Errorf("%v", all)
	}
}
