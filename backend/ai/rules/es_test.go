package rules

import "testing"

const S = Es

func TestSpanishInvertedMarksAndFinalPoints(t *testing.T) {
	eqPairs(t, hits("es.punct.inverted", S, "", "Vienes mañana? Sí."), flagged("?"))
	for _, ok := range []string{"¿Vienes mañana?", "Si llueve, ¿vamos?", "¡Qué bien!", "¿Qué?!"} {
		none(t, hits("es.punct.inverted", S, "", ok), ok)
	}
	none(t, hitsIn("es.punct.inverted", S, "", "Por qué leer?", headingBlock(2)))
	eqStr(t, fixed("es.punct.double-final", S, "", "¿Vienes?. Ya…."), "¿Vienes? Ya…")
	none(t, hits("es.punct.double-final", S, "", "Dijo «¿vienes?». Y (¿quién?)…"))
}

func TestSpanishQuotesAccentsAndCapitals(t *testing.T) {
	eqStr(t, fixed("es.quote.period-outside", S, "", "Dijo «ya voy.» Luego"), "Dijo «ya voy». Luego")
	none(t, hits("es.quote.period-outside", S, "", "Trajo libros, cuadernos, etc.» y más"))
	eqStr(t, fixed("es.word.monosyllable", S, "", "Fué él quien dió el guión."), "Fue él quien dio el guion.")
	// Diacritic pairs keep their accent.
	none(t, hits("es.word.monosyllable", S, "", "Tú y él, sí, más té."))
	none(t, hits("es.word.solo", S, "", "Sólo uno."))
	eqStr(t, fixed("es.word.months", S, "", "Llegó el Lunes 3 de Marzo."), "Llegó el lunes 3 de marzo.")
	for _, ok := range []string{"Enero fue frío.", "En Viernes Santo salimos.", "Habló Plácido Domingo ayer."} {
		none(t, hits("es.word.months", S, "", ok), ok)
	}
}

func TestSpanishNumbersAndEtc(t *testing.T) {
	eqStr(t, fixed("es.punct.etc", S, "", "Frutas, verduras, etc..."), "Frutas, verduras, etc.")
	eqStr(t, fixed("es.num.group", S, "", "Hay 12.500 personas"), "Hay 12 500 personas")
	for _, ok := range []string{"En 1.500 casos", "El año 2026", "Código 12.500.3a"} {
		none(t, hits("es.num.group", S, "", ok), ok)
	}
	none(t, hits("es.num.group", S, "es-419", "Hay 12.500 personas"))
	eqStr(t, fixed("es.num.ordinal", S, "", "el 1º y la 2ª"), "el 1.º y la 2.ª")
	eqStr(t, fixed("es.num.percent", S, "es-ES", "un 10% más"), "un 10 % más")
	none(t, hits("es.num.percent", S, "", "un 10% más"))
	eqStr(t, fixed("es.word.ex", S, "", "el ex-presidente y la ex-SS"), "el expresidente y la ex-SS")
	count(t, hits("es.num.word-unit", S, "", "Corrió veinte km."), 1)
}

func TestSpanishDialogueRaya(t *testing.T) {
	eqStr(t, fixed("es.dialogue.raya", S, "", "- Hola, dijo."), "—Hola, dijo.")
	eqStr(t, fixed("es.dialogue.raya", S, "", "— Hola."), "—Hola.")
	none(t, hits("es.dialogue.raya", S, "", "—Hola."))
	none(t, hitsIn("es.dialogue.raya", S, "", "- uno", blockItem))
}
