package rules

import (
	"strings"
)

// French rules. Spacing before ; : ! ? and inside « » differs by country,
// so Automatic follows the document's majority and only turns a breaking
// space into a no-break one. Either 1990 spelling and times such as 13:52
// pass.

func frRun(t *Text, c *ctx, o *out) {
	frHighPunct(t, c, o)
	frOrdinal(t, c, o)
	frMr(t, c, o)
	frEuphonicT(t, c, o)
	frLigature(t, c, o)
	frSymbolSpace(t, c, o)
	etcDots(t, "fr.punct.etc", "Après « etc. », pas de points de suspension.", o)
	frCapitalAccent(t, c, o)
	frApostrophe(t, c, o)
	frDialogue(t, c, o)
}

const (
	nbsp  = '\u00A0'
	nnbsp = '\u202F'
)

// frSpace is what goes before ; ! ? (and :); frUnset leaves it as typed.
type frSpace int

const (
	frUnset frSpace = iota
	frNone
	frThin
	frFull
)

// frHighPunct is fr.space.high-punct: by the profile, else the document's
// majority. A regular space before ; : ! ? or inside « » becomes no-break;
// a missing one is added only when the document clearly spaces. Not
// times, URLs, emoticons, `?!`.
func frHighPunct(t *Text, c *ctx, o *out) {
	s := c.stats
	spacing := c.profile.opt("fr.spacing")
	if spacing == "" && c.profile.Explicit {
		switch {
		case c.profile.Preset == "eu":
			spacing = "eu"
		case c.profile.Region == "CA":
			spacing = "fr-CA"
		case c.profile.Region == "CH":
			spacing = "fr-CH"
		default:
			spacing = "fr-FR"
		}
	}
	spacedCount := s.frSpace + s.frNbsp + s.frNnbsp
	// Before ; ! ?, before :, inside « ».
	var semi, colon, guillemet frSpace
	switch spacing {
	case "fr-FR":
		semi, colon, guillemet = frThin, frFull, frThin
	case "fr-CA":
		semi, colon, guillemet = frNone, frFull, frFull
	case "fr-CH":
		semi, colon, guillemet = frNone, frNone, frFull
	case "eu":
		semi, colon, guillemet = frNone, frNone, frNone
	default:
		// No clear majority, or the document leaves no space: only a
		// breaking space is wrong (it can wrap before the mark).
		if minority(s.frNone, spacedCount) {
			semi, colon, guillemet = frThin, frFull, frThin
		}
	}
	thinOr := func(want frSpace) string {
		// Keep the document's own no-break choice.
		if want == frThin && s.frNbsp <= s.frNnbsp {
			return string(nnbsp)
		}
		return string(nbsp)
	}
	msgNB := "Devant ce signe, l’espace doit être insécable."
	msgNone := "Pas d’espace devant ce signe dans la convention choisie."
	for i, ch := range t.Chars {
		var want frSpace
		switch ch {
		case ';', '!', '?':
			want = semi
		case ':':
			want = colon
		default:
			continue
		}
		p, ok := t.before(i)
		if !ok || p == '!' || p == '?' {
			continue
		}
		next, hasNext := t.At(i + 1)
		if isASCIIDigit(p) && hasNext && isASCIIDigit(next) {
			continue // 13:52
		}
		if hasNext && (strings.ContainsRune(")(DP-/", next) || IsAlphanumeric(next)) {
			continue // emoticons, URLs, "http:", "C:"
		}
		switch {
		case p == ' ' && want == frNone:
			o.fix("fr.space.high-punct", i-1, i+1, string(ch), msgNone)
		case p == ' ' && want != frUnset:
			o.fix("fr.space.high-punct", i-1, i+1, thinOr(want)+string(ch), msgNB)
		case p == ' ':
			space := nnbsp
			if s.frNbsp > s.frNnbsp || ch == ':' {
				space = nbsp
			}
			o.fix("fr.space.high-punct", i-1, i+1, string(space)+string(ch), msgNB)
		case (p == nbsp || p == nnbsp) && want == frNone:
			o.fix("fr.space.high-punct", i-1, i+1, string(ch), msgNone)
		case want != frUnset && want != frNone && (IsAlphanumeric(p) || strings.ContainsRune(`»)"”`, p)):
			o.fix("fr.space.high-punct", i, i+1, thinOr(want)+string(ch), "Devant ce signe, on met une espace insécable.")
		}
	}
	// Inside « ».
	for i, ch := range t.Chars {
		var inner rune
		var ok bool
		var at int
		switch {
		case ch == '«':
			inner, ok = t.At(i + 1)
			at = i + 1
		case ch == '»' && i > 0:
			inner, ok = t.before(i)
			at = i - 1
		default:
			continue
		}
		if !ok {
			continue
		}
		switch {
		case inner == ' ' && guillemet == frNone:
			o.fix("fr.space.high-punct", at, at+1, "", "Pas d’espace à l’intérieur des guillemets dans la convention choisie.")
		case inner == ' ':
			o.fix("fr.space.high-punct", at, at+1, string(nbsp), "À l’intérieur des guillemets, l’espace doit être insécable.")
		case guillemet != frUnset && guillemet != frNone && IsAlphanumeric(inner):
			to := thinOr(guillemet) + "»"
			if ch == '«' {
				to = "«" + thinOr(guillemet)
			}
			o.fix("fr.space.high-punct", i, i+1, to, "À l’intérieur des guillemets, on met une espace insécable.")
		}
	}
}

var frOrdinalRe = MustRe(`\b(\d+|[IVXLC]{2,})(ère|ere|ème|ième|è|eme)(s?)\b`)

// frOrdinal is fr.num.ordinal: 1ère -> 1re, 2ème/2ième/2è -> 2e (Nième,
// Xième: letters).
func frOrdinal(t *Text, _ *ctx, o *out) {
	for _, g := range t.captures(frOrdinalRe) {
		if !g[0].OK || !g[1].OK || !g[2].OK || !g[3].OK {
			continue
		}
		num, suffix := t.slice(g[1].A, g[1].B), t.slice(g[2].A, g[2].B)
		to := "e"
		if (suffix == "ère" || suffix == "ere") && num == "1" {
			to = "re"
		}
		o.fix("fr.num.ordinal", g[0].A, g[0].B, num+to+t.slice(g[3].A, g[3].B), "Abréviation de l’ordinal : « "+num+to+" ».")
	}
}

var frMrRe = MustRe(`\bMr\.?\s(\p{Lu})`)

// frMr is fr.word.mr: Mr -> M. before a name (not in English quotations).
func frMr(t *Text, _ *ctx, o *out) {
	for _, g := range t.captures(frMrRe) {
		if !g[0].OK {
			continue
		}
		a := g[0].A
		end := a + 2
		if t.atEq(a+2, '.') {
			end = a + 3
		}
		if !t.inQuotes(a, end) {
			o.fix("fr.word.mr", a, end, "M.", "En français, « Monsieur » s’abrège « M. ».")
		}
	}
}

var frEuphonicRe = MustRe(`-t['’](il|elle|on|ils|elles)\b`)

// frEuphonicT is fr.word.euphonic-t: a-t'il -> a-t-il.
func frEuphonicT(t *Text, _ *ctx, o *out) {
	for _, g := range t.captures(frEuphonicRe) {
		if g[0].OK && g[1].OK {
			o.fix("fr.word.euphonic-t", g[0].A, g[0].B, "-t-"+t.slice(g[1].A, g[1].B), "Le « t » euphonique s’écrit entre traits d’union : « a-t-il ».")
		}
	}
}

var frLigatures = [][2]string{
	{"coeur", "cœur"}, {"coeurs", "cœurs"}, {"oeuvre", "œuvre"}, {"oeuvres", "œuvres"}, {"soeur", "sœur"},
	{"soeurs", "sœurs"}, {"oeil", "œil"}, {"boeuf", "bœuf"}, {"boeufs", "bœufs"}, {"oeuf", "œuf"}, {"oeufs", "œufs"},
	{"voeu", "vœu"}, {"voeux", "vœux"}, {"noeud", "nœud"}, {"noeuds", "nœuds"}, {"manoeuvre", "manœuvre"},
	{"moeurs", "mœurs"}, {"choeur", "chœur"}, {"oesophage", "œsophage"}, {"oedème", "œdème"},
	{"rancoeur", "rancœur"}, {"oecuménique", "œcuménique"},
}

// frLigature is fr.word.ligature: coeur -> cœur (coefficient, coexister
// are other words).
func frLigature(t *Text, _ *ctx, o *out) {
	for _, p := range frLigatures {
		for _, m := range findWord(t, p[0], true) {
			o.fix("fr.word.ligature", m[0], m[1], matchCase(t.slice(m[0], m[1]), p[1]), "S’écrit avec la ligature : « "+p[1]+" ».")
		}
	}
}

var (
	frSymbolRe = MustRe(`(\d)(%|€|\$|£|(?:kg|km|cm|mm|g|m|l|h|min)\b)`)
	// frSpacedSymbolRe is an ordinary space a line can break at, before
	// the two symbols no French word starts with (unit letters after a
	// space are too often words).
	frSpacedSymbolRe = MustRe(`(\d) (%|€)`)
)

// frSymbolSpace is fr.num.symbol-space: 85%, 25€, or 85 % with a breaking
// space -> a no-break space before the symbol (fr-CH and the EU differ
// for %).
func frSymbolSpace(t *Text, c *ctx, o *out) {
	noPctSpace := c.profile.regionIs("CH") || c.profile.is("eu")
	for _, g := range append(t.captures(frSymbolRe), t.captures(frSpacedSymbolRe)...) {
		if !g[0].OK || !g[1].OK || !g[2].OK {
			continue
		}
		a, b := g[0].A, g[0].B
		unit := t.slice(g[2].A, g[2].B)
		if unit == "%" && noPctSpace {
			continue
		}
		if t.atIs(b, func(r rune) bool { return r == '-' || IsAlphanumeric(r) }) {
			continue
		}
		o.fix("fr.num.symbol-space", a, b, string(t.Chars[g[1].A])+string(nbsp)+unit, "Une espace insécable sépare le nombre du symbole.")
	}
}

var frAStartRe = MustRe(`\bA (partir|cette|ce|la|l['’]|demain|bientôt|nouveau|propos|travers|côté|moins|peine|cause|condition|Paris|Lyon|Marseille|Montréal|Bruxelles|Genève)\b`)

// frCapitalAccent is fr.word.capital-accent: "A partir de" at a sentence
// start -> "À".
func frCapitalAccent(t *Text, _ *ctx, o *out) {
	for _, m := range t.find(frAStartRe) {
		if t.sentenceStart(m[0]) {
			o.fix("fr.word.capital-accent", m[0], m[0]+1, "À", "Les majuscules gardent leurs accents : « À ».")
		}
	}
}

// frApostrophe is fr.punct.apostrophe: ' and ’ mixed in one document.
func frApostrophe(t *Text, c *ctx, o *out) {
	s := c.stats
	var from, to rune
	switch {
	case minority(s.aposStraight, s.aposCurly):
		from, to = '\'', '’'
	case minority(s.aposCurly, s.aposStraight):
		from, to = '’', '\''
	default:
		return
	}
	for i := 1; i+1 < t.Len(); i++ {
		if t.Chars[i] == from && IsAlphabetic(t.Chars[i-1]) && IsAlphabetic(t.Chars[i+1]) {
			o.fix("fr.punct.apostrophe", i, i+1, string(to), "Apostrophes droites et typographiques mélangées dans le document.")
		}
	}
}

// frDialogue is fr.dialogue.dash: – and — mixed for speaker changes in one
// document.
func frDialogue(t *Text, c *ctx, o *out) {
	s := c.stats
	var from, to rune
	switch {
	case minority(s.dialogueEn, s.dialogueEm):
		from, to = '–', '—'
	case minority(s.dialogueEm, s.dialogueEn):
		from, to = '—', '–'
	default:
		return
	}
	if t.Len() > 0 && t.Chars[0] == from {
		o.fix("fr.dialogue.dash", 0, 1, string(to), "Tirets de dialogue différents dans le document.")
	}
}
