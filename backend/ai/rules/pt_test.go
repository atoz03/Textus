package rules

import "testing"

const P = Pt

func TestPortugueseSpellingAndHyphens(t *testing.T) {
	eqStr(t, fixed("pt.spell.pre-ao90", P, "", "Uma idéia para o vôo e a acção óptima"), "Uma ideia para o voo e a ação ótima")
	none(t, hits("pt.spell.pre-ao90", P, "", "Ele pôde pôr o facto em contacto"))
	eqStr(t, fixed("pt.spell.hyphen", P, "", "A auto-escola e o fim-de-semana; hei-de ir."), "A autoescola e o fim de semana; hei de ir.")
	none(t, hits("pt.spell.hyphen", P, "", "cor-de-rosa e couve-flor"))
}

func TestPortugueseQuotesOrdinalsAndMonths(t *testing.T) {
	eqStr(t, fixed("pt.quote.style", P, "pt-PT", "Ele disse “sim”."), "Ele disse «sim».")
	eqStr(t, fixed("pt.quote.style", P, "pt-BR", "Ele disse «sim»."), "Ele disse “sim”.")
	none(t, hits("pt.quote.style", P, "", "Ele disse “sim”."))
	eqStr(t, fixed("pt.num.ordinal", P, "", "Ficou em 1° lugar no 3° andar."), "Ficou em 1º lugar no 3º andar.")
	eqStr(t, fixed("pt.num.ordinal", P, "pt-PT", "o 1° dia"), "o 1.º dia")
	none(t, hits("pt.num.ordinal", P, "", "Fazia 30° C e 20 graus."))
	eqStr(t, fixed("pt.word.months", P, "", "Chega em 15 de Setembro, numa Segunda-feira."), "Chega em 15 de setembro, numa segunda-feira.")
	none(t, hits("pt.word.months", P, "", "O feriado Sete de Setembro."))
}

func TestPortugueseOfficialProfile(t *testing.T) {
	o := "pt-BR/official"
	count(t, hits("pt.official.address", P, o, "A Vossa Excelência, o Dr. Silva"), 2)
	none(t, hits("pt.official.address", P, "", "A Vossa Excelência"))
	eqStr(t, fixed("pt.official.date", P, o, "Em 01 de janeiro e 05 de março"), "Em 1º de janeiro e 5 de março")
}

func TestNeverFlagOptionalAccentsInPortuguese(t *testing.T) {
	text := "Amámos e amamos, dêmos e demos, a fôrma e a forma."
	if all := findings(P, "pt-PT", text, blockPara); len(all) != 0 {
		t.Errorf("%v", all)
	}
}
