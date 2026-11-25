package rules

import "testing"

const R = Ru

func TestRussianQuotes(t *testing.T) {
	eqStr(t, fixed("ru.quote.style", R, "", "Он сказал \"привет\" и “пока”."), "Он сказал «привет» и «пока».")
	eqStr(t, fixed("ru.quote.style", R, "", "«Роман «Война и мир» хорош»"), "«Роман „Война и мир“ хорош»")
	none(t, hits("ru.quote.style", R, "", "Файл \"config.json\" и `\"x\"`"))
	eqStr(t, fixed("ru.quote.period-outside", R, "", "Он сказал «да.» Потом «нет,» и ушёл"), "Он сказал «да». Потом «нет», и ушёл")
	count(t, hits("ru.quote.exclaim-period", R, "", "Он крикнул «Стой!»."), 1)
}

func TestRussianDashesAbbreviationsAndNumbers(t *testing.T) {
	eqStr(t, fixed("ru.punct.dash", R, "", "Москва - столица."), "Москва\u00A0— столица.")
	none(t, hits("ru.punct.dash", R, "", "в 1990 - 1995 годах"))
	eqStr(t, fixed("ru.punct.comma-dash", R, "", "Он пришёл —, и всё"), "Он пришёл, — и всё")
	eqStr(t, fixed("ru.word.abbr", R, "", "т.е. и т.д."), "т.\u00A0е. и т.\u00A0д.")
	eqStr(t, fixed("ru.num.ending", R, "", "5-ый, 2-ой, 5-ого, 3-ий"), "5-й, 2-й, 5-го, 3-й")
	eqStr(t, fixed("ru.num.unit", R, "", "Вес 5кг, путь 10 км. и всё"), "Вес 5\u00A0кг, путь 10 км и всё")
	none(t, hits("ru.num.unit", R, "", "В 2026 г. было 5 кг."))
	eqStr(t, fixed("ru.num.group", R, "", "Всего 1,000,000 рублей"), "Всего 1\u00A0000\u00A0000 рублей")
	none(t, hits("ru.num.group", R, "", "Это 1,5 литра"))
	eqStr(t, fixed("ru.dialogue.dash", R, "", "- Привет, — сказал он."), "—\u00A0Привет, — сказал он.")
}

func TestNeverFlagInRussian(t *testing.T) {
	text := "Неужели?.. Да!.. Ёлка и елка. Пьесу «В бой!» поставили."
	if all := findings(R, "", text, blockPara); len(all) != 0 {
		t.Errorf("%v", all)
	}
}
