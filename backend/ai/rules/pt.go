package rules

import (
	"slices"
	"strings"
)

// Portuguese rules, Brazilian by default, with the AO90 spelling on. Never
// flagged: optional accents (amámos/amamos, fôrma/forma), pôde/pode, pôr/por.

func ptRun(t *Text, c *ctx, o *out) {
	ptQuoteStyle(t, c, o)
	ptPreAO90(t, c, o)
	ptHyphen(t, c, o)
	ptOrdinal(t, c, o)
	ptMonths(t, c, o)
	ptOfficial(t, c, o)
	ptGroup(t, c, o)
}

// ptQuoteStyle is pt.quote.style: quotes against the variant (pt-BR “ ”,
// pt-PT « »); in Automatic only the minority system of the document.
func ptQuoteStyle(t *Text, c *ctx, o *out) {
	s := c.stats
	var toGuillemets bool
	switch {
	case c.profile.Explicit:
		toGuillemets = c.profile.regionIs("PT")
	case minority(s.dqCurly, s.dqGuillemet):
		toGuillemets = true
	case minority(s.dqGuillemet, s.dqCurly):
		toGuillemets = false
	default:
		return
	}
	fromOpen, fromClose, open, close, msg := '«', '»', "“", "”", "No português do Brasil as aspas são “ ”."
	if toGuillemets {
		fromOpen, fromClose, open, close, msg = '“', '”', "«", "»", "Em português europeu as aspas são « »."
	}
	for i := 0; i < t.Len(); {
		if t.Chars[i] == fromOpen {
			if j := slices.Index(t.Chars[i+1:], fromClose); j >= 0 {
				j += i + 1
				o.fix("pt.quote.style", i, i+1, open, msg)
				o.fix("pt.quote.style", j, j+1, close, msg)
				i = j + 1
				continue
			}
		}
		i++
	}
}

var ptPreAO90Words = [][2]string{
	{"idéia", "ideia"}, {"idéias", "ideias"}, {"heróico", "heroico"}, {"heróica", "heroica"},
	{"vôo", "voo"}, {"vôos", "voos"}, {"crêem", "creem"}, {"lêem", "leem"}, {"dêem", "deem"},
	{"vêem", "veem"}, {"enjôo", "enjoo"}, {"abençôo", "abençoo"}, {"pára", "para"}, {"pêlo", "pelo"},
	{"pólo", "polo"}, {"lingüiça", "linguiça"}, {"freqüente", "frequente"}, {"freqüência", "frequência"},
	{"conseqüência", "consequência"}, {"tranqüilo", "tranquilo"}, {"agüentar", "aguentar"},
	{"cinqüenta", "cinquenta"}, {"bilíngüe", "bilíngue"}, {"assembléia", "assembleia"},
	{"européia", "europeia"}, {"européias", "europeias"}, {"jibóia", "jiboia"}, {"estréia", "estreia"},
	{"platéia", "plateia"}, {"colméia", "colmeia"}, {"paranóico", "paranoico"}, {"geléia", "geleia"},
	{"acção", "ação"}, {"acções", "ações"}, {"óptimo", "ótimo"}, {"óptima", "ótima"},
	{"director", "diretor"}, {"directora", "diretora"}, {"actual", "atual"}, {"actualmente", "atualmente"},
	{"colecção", "coleção"}, {"direcção", "direção"}, {"efectivo", "efetivo"}, {"exacto", "exato"},
	{"exactamente", "exatamente"}, {"correcto", "correto"}, {"projecto", "projeto"}, {"projectos", "projetos"},
	{"objectivo", "objetivo"}, {"objectivos", "objetivos"}, {"adopção", "adoção"}, {"baptismo", "batismo"},
	{"eléctrico", "elétrico"}, {"eléctrica", "elétrica"}, {"selecção", "seleção"}, {"protecção", "proteção"},
	{"acto", "ato"}, {"actos", "atos"},
}

// ptPreAO90 is pt.spell.pre-ao90: spellings the 1990 agreement replaced
// (idéia, vôo, lingüiça; acção, óptimo, director) - unless pt.ao90=off.
func ptPreAO90(t *Text, c *ctx, o *out) {
	if c.profile.opt("pt.ao90") == "off" {
		return
	}
	for _, p := range ptPreAO90Words {
		for _, m := range findWord(t, p[0], true) {
			o.fix("pt.spell.pre-ao90", m[0], m[1], matchCase(t.slice(m[0], m[1]), p[1]),
				"Grafia anterior ao Acordo Ortográfico de 1990: «"+p[1]+"».")
		}
	}
}

var ptHyphens = [][2]string{
	{"anti-religioso", "antirreligioso"}, {"anti-semita", "antissemita"}, {"auto-escola", "autoescola"},
	{"auto-estrada", "autoestrada"}, {"contra-regra", "contrarregra"}, {"extra-escolar", "extraescolar"},
	{"mini-saia", "minissaia"}, {"infra-estrutura", "infraestrutura"}, {"semi-árido", "semiárido"},
	{"ultra-som", "ultrassom"}, {"anti-social", "antissocial"}, {"co-autor", "coautor"},
	{"hei-de", "hei de"}, {"há-de", "há de"}, {"hás-de", "hás de"}, {"hão-de", "hão de"},
	{"fim-de-semana", "fim de semana"}, {"dia-a-dia", "dia a dia"},
}

// ptHyphen is pt.spell.hyphen: the AO90 hyphenation list.
func ptHyphen(t *Text, c *ctx, o *out) {
	if c.profile.opt("pt.ao90") == "off" {
		return
	}
	for _, p := range ptHyphens {
		for _, m := range findWord(t, p[0], true) {
			o.fix("pt.spell.hyphen", m[0], m[1], matchCase(t.slice(m[0], m[1]), p[1]), "Com o Acordo de 1990: «"+p[1]+"».")
		}
	}
}

var ptDegreeOrdinalRe = MustRe(`\b(\d{1,3})°`)

// ptOrdinal is pt.num.ordinal: 1° (a degree sign) as an ordinal -> 1º (BR)
// or 1.º (PT). Temperatures and angles stay.
func ptOrdinal(t *Text, c *ctx, o *out) {
	before := []string{"o", "a", "no", "na", "do", "da", "ao", "pelo", "pela", "os", "as", "em"}
	after := []string{"lugar", "andar", "ano", "dia", "edição", "turno", "período", "semestre", "trimestre",
		"colocado", "capítulo", "artigo", "parágrafo", "grau"}
	for _, g := range t.captures(ptDegreeOrdinalRe) {
		if !g[0].OK || !g[1].OK {
			continue
		}
		a, b := g[0].A, g[0].B
		if t.atIs(b, func(r rune) bool { return strings.ContainsRune("CFcf", r) }) {
			continue
		}
		prevWord := ""
		if ws := strings.Fields(t.slice(0, a)); len(ws) > 0 {
			prevWord = toLower(ws[len(ws)-1])
		}
		nextWord := ""
		if ws := strings.Fields(t.slice(b, t.Len())); len(ws) > 0 {
			nextWord = toLower(strings.TrimFunc(ws[0], func(r rune) bool { return !IsAlphabetic(r) }))
		}
		if !(slices.Contains(before, prevWord) || slices.Contains(after, nextWord)) ||
			strings.HasPrefix(nextWord, "graus") || nextWord == "celsius" {
			continue
		}
		mark := "º"
		if c.profile.regionIs("PT") {
			mark = ".º"
		}
		o.fix("pt.num.ordinal", a, b, t.slice(g[1].A, g[1].B)+mark, "O ordinal usa o indicador «º», não o símbolo de grau «°».")
	}
}

var ptMonthNames = []string{"Janeiro", "Fevereiro", "Março", "Abril", "Maio", "Junho", "Julho", "Agosto", "Setembro",
	"Outubro", "Novembro", "Dezembro", "Segunda-feira", "Terça-feira", "Quarta-feira", "Quinta-feira", "Sexta-feira",
	"Sábado", "Domingo"}

// ptMonths is pt.word.months: capitalised months and weekdays mid-sentence
// (AO90).
func ptMonths(t *Text, _ *ctx, o *out) {
	capitalisedNames(t, "pt.word.months", ptMonthNames, "Com o Acordo de 1990, meses e dias da semana escrevem-se com minúscula.", o)
}

var (
	ptAddressRe    = MustRe(`\b(?:Vossa Excelência|Excelentíssim[oa]s?|Ilustríssim[oa]s?|Digníssim[oa]s?|Vossa Senhoria)\b|\b(?:Dr\.|Doutor[a]?)\s\p{Lu}`)
	ptPaddedDateRe = MustRe(`\b0(\d) de (?:janeiro|fevereiro|março|abril|maio|junho|julho|agosto|setembro|outubro|novembro|dezembro)`)
)

// ptOfficial is pt-BR/official: forms of address the Decreto 9.758/2019
// replaced with "senhor/senhora", and 01 de janeiro -> 1º de janeiro.
func ptOfficial(t *Text, c *ctx, o *out) {
	if !(c.profile.is("official") && c.profile.regionIs("BR")) {
		return
	}
	for _, m := range t.find(ptAddressRe) {
		o.flag("pt.official.address", m[0], m[1], "O Decreto 9.758/2019 adota «senhor/senhora» como forma de tratamento.")
	}
	for _, g := range t.captures(ptPaddedDateRe) {
		if !g[0].OK || !g[1].OK {
			continue
		}
		day := t.slice(g[1].A, g[1].B)
		if day == "1" {
			day = "1º"
		}
		o.fix("pt.official.date", g[0].A, g[1].B, day, "Datas oficiais sem zero à esquerda; o primeiro dia do mês é «1º».")
	}
}

var ptGroupedRe = MustRe(`\b\d{1,3}(?:([. \u00A0])\d{3})+\b`)

// ptGroup is pt.num.group: `1.000` and `1 000` grouping mixed in one
// document.
func ptGroup(t *Text, c *ctx, o *out) {
	s := c.stats
	var target rune
	switch {
	case minority(s.groupSpace, s.groupDot):
		target = '.'
	case minority(s.groupDot, s.groupSpace):
		target = '\u00A0'
	default:
		return
	}
	for _, g := range t.captures(ptGroupedRe) {
		if !g[0].OK || !g[1].OK {
			continue
		}
		a, b := g[0].A, g[0].B
		sep := t.Chars[g[1].A]
		isSpace := sep != '.'
		if b-a < 6 || isSpace == (target != '.') {
			continue
		}
		fixed := make([]rune, 0, b-a)
		for _, ch := range t.Chars[a:b] {
			if ch == sep {
				ch = target
			}
			fixed = append(fixed, ch)
		}
		o.fix("pt.num.group", a, b, string(fixed), "Separador de milhares diferente do resto do documento.")
	}
}
