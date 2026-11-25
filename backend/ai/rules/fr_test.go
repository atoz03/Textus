package rules

import (
	"strings"
	"testing"
)

const F = Fr

func TestFrenchBreakingSpacesBecomeNoBreakAndProfilesDecideTheRest(t *testing.T) {
	// Automatic, no majority: only the breaking spaces are wrong.
	eqStr(t, fixed("fr.space.high-punct", F, "", "Vraiment ? Oui !"), "Vraiment\u202F? Oui\u202F!")
	none(t, hits("fr.space.high-punct", F, "", "Vraiment? Oui!"))
	// A document that spaces: the missing one is added.
	doc := "Quoi\u202F? Non\u202F! Si\u202F; enfin\u202F! Bon?"
	eqPairs(t, hits("fr.space.high-punct", F, "", doc), one("?", "\u202F?"))
	// fr-CA: no space before ? ; but a no-break one before :
	eqStr(t, fixed("fr.space.high-punct", F, "fr-CA", "Vraiment ? Note : oui"), "Vraiment? Note\u00A0: oui")
	none(t, hits("fr.space.high-punct", F, "fr-CA", "Vraiment? Oui!"))
	eqStr(t, fixed("fr.space.high-punct", F, "fr/eu", "Il dit : « oui »"), "Il dit: «oui»")
	eqStr(t, fixed("fr.space.high-punct", F, "fr-FR", "Il dit «oui»"), "Il dit «\u202Foui\u202F»")
	for _, ok := range []string{"Rendez-vous à 13:52.", "Voir http://a.fr ou :) ou C:/x", "Quoi ?! Oui."} {
		for _, h := range hits("fr.space.high-punct", F, "fr-FR", ok) {
			if strings.Contains(h.orig, ":") && !strings.Contains(h.orig, " ") {
				t.Errorf("%s: %v", ok, h)
			}
		}
	}
}

func TestFrenchOrdinalsAbbreviationsAndLigatures(t *testing.T) {
	eqStr(t, fixed("fr.num.ordinal", F, "", "la 1ère fois, le 2ème, le 3ième, le XIXème siècle"), "la 1re fois, le 2e, le 3e, le XIXe siècle")
	none(t, hits("fr.num.ordinal", F, "", "le Nième et le Xième"))
	eqStr(t, fixed("fr.word.mr", F, "", "J’ai vu Mr Dupont."), "J’ai vu M. Dupont.")
	eqStr(t, fixed("fr.word.euphonic-t", F, "", "Pourquoi a-t'il dit ça ?"), "Pourquoi a-t-il dit ça ?")
	eqStr(t, fixed("fr.word.ligature", F, "", "Le coeur et l'oeuvre de ma soeur"), "Le cœur et l'œuvre de ma sœur")
	none(t, hits("fr.word.ligature", F, "", "Le coefficient et coexister"))
	eqStr(t, fixed("fr.punct.etc", F, "", "des pommes, etc…"), "des pommes, etc.")
}

func TestFrenchSymbolsCapitalsAndConsistency(t *testing.T) {
	eqStr(t, fixed("fr.num.symbol-space", F, "", "Il pèse 58kg et coûte 25€, soit 85%."), "Il pèse 58\u00A0kg et coûte 25\u00A0€, soit 85\u00A0%.")
	none(t, hits("fr.num.symbol-space", F, "fr-CH", "soit 85%."))
	// An ordinary space before % or € becomes a no-break one; one before a
	// unit letter is left alone (« en 2 l'espace »).
	eqStr(t, fixed("fr.num.symbol-space", F, "", "soit 85 % et 25 € en 2 l'espace."), "soit 85\u00A0% et 25\u00A0€ en 2 l'espace.")
	eqStr(t, fixed("fr.word.capital-accent", F, "", "A partir de demain, on part."), "À partir de demain, on part.")
	none(t, hits("fr.word.capital-accent", F, "", "Il a partir."))
	count(t, hits("fr.punct.apostrophe", F, "", "l’eau, l’air, d’abord, l'arbre"), 1)
}

func TestNeverFlagInFrench(t *testing.T) {
	// Both spellings of the 1990 rectifications; CA without spaces.
	if all := findings(F, "fr-CA", "Il connait la clé; il connaît la clef? Oui! À 13:52.", blockPara); len(all) != 0 {
		t.Errorf("%v", all)
	}
}
