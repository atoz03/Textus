package rules

import (
	"slices"
	"strings"
)

// English spelling: common misspellings, plural apostrophes, a or an, and
// British and American spelling mixed.

var enMisspellings = [][2]string{
	{"alot", "a lot"},
	{"irregardless", "regardless"},
	{"definately", "definitely"},
	{"definitly", "definitely"},
	{"seperate", "separate"},
	{"seperately", "separately"},
	{"recieve", "receive"},
	{"recieved", "received"},
	{"occured", "occurred"},
	{"occurence", "occurrence"},
	{"untill", "until"},
	{"wich", "which"},
	{"accomodate", "accommodate"},
	{"accomodation", "accommodation"},
	{"acheive", "achieve"},
	{"beleive", "believe"},
	{"beleif", "belief"},
	{"comming", "coming"},
	{"concious", "conscious"},
	{"embarass", "embarrass"},
	{"enviroment", "environment"},
	{"existance", "existence"},
	{"goverment", "government"},
	{"grammer", "grammar"},
	{"independant", "independent"},
	{"neccessary", "necessary"},
	{"necessery", "necessary"},
	{"noticable", "noticeable"},
	{"occassion", "occasion"},
	{"persue", "pursue"},
	{"posession", "possession"},
	{"publically", "publicly"},
	{"recomend", "recommend"},
	{"reccomend", "recommend"},
	{"refered", "referred"},
	{"relevent", "relevant"},
	{"sucess", "success"},
	{"succesful", "successful"},
	{"tommorow", "tomorrow"},
	{"tommorrow", "tomorrow"},
	{"truely", "truly"},
	{"wierd", "weird"},
	{"thier", "their"},
	{"arguement", "argument"},
	{"begining", "beginning"},
	{"buisness", "business"},
	{"commitee", "committee"},
	{"completly", "completely"},
	{"foward", "forward"},
	{"freind", "friend"},
	{"garantee", "guarantee"},
	{"harrass", "harass"},
	{"immediatly", "immediately"},
	{"knowlege", "knowledge"},
	{"liason", "liaison"},
	{"millenium", "millennium"},
	{"mispell", "misspell"},
	{"paralell", "parallel"},
	{"prefered", "preferred"},
	{"privelege", "privilege"},
	{"rythm", "rhythm"},
	{"seige", "siege"},
	{"threshhold", "threshold"},
	{"tounge", "tongue"},
	{"whereever", "wherever"},
	{"writting", "writing"},
}

var enAnWords = []string{"hour", "hours", "hourly", "honest", "honestly", "honesty", "honor", "honors", "honour", "honourable", "honorable", "heir", "heirs", "heiress"}

var enAWords = []string{"user", "users", "union", "unit", "united", "unique", "university", "universal", "usual", "usually", "useful", "use", "used", "usage", "uniform", "utility", "euro", "european", "eulogy", "ewe", "one", "once", "one-time", "unicorn", "uranium", "urinal", "usurper", "utopia", "ubiquitous", "unanimous", "unary"}

var enCouldOfRe = MustRe(`(?i)\b(could|should|would|must|might)\s+of\b`)

// enMisspelling is en.word.misspelling: non-words and common misspellings,
// and "could of" (not "could of course"). Not inside a quotation of the
// error.
func enMisspelling(t *Text, _ *ctx, o *out) {
	for _, p := range enMisspellings {
		wrong, right := p[0], p[1]
		for _, m := range findWord(t, wrong, true) {
			if !t.inQuotes(m[0], m[1]) {
				o.fix("en.word.misspelling", m[0], m[1], matchCase(t.slice(m[0], m[1]), right), "Misspelling: “"+right+"”.")
			}
		}
	}
	for _, g := range t.captures(enCouldOfRe) {
		if !g[0].OK || !g[1].OK {
			continue
		}
		a, b := g[0].A, g[0].B
		rest := toLower(string(t.Chars[b:min(b+8, t.Len())]))
		if strings.HasPrefix(trimStart(rest), "course") || t.inQuotes(a, b) {
			continue
		}
		modal := t.slice(g[1].A, g[1].B)
		lm := toLower(modal)
		o.fix("en.word.misspelling", a, b, modal+" have", "“"+lm+" of” is a mishearing of “"+lm+" have”.")
	}
}

var (
	enDecadeAposRe = MustRe(`\b(1[0-9]|20)(\d0)['’]s\b`)
	enAbbrAposRe   = MustRe(`(?i:\b(?:many|several|few|all|some|these|those|multiple|two|three|four|five|six|seven|eight|nine|ten|\d+)\s+)([A-Z]{2,})['’]s\b`)
)

// enApostrophePlural is en.punct.apostrophe-plural: 1990's -> 1990s; "two
// PDF's" -> PDFs (only after a number or quantifier - "NASA's mission" is
// a possessive). Single capital letters (A's) are left alone.
func enApostrophePlural(t *Text, _ *ctx, o *out) {
	for _, m := range t.find(enDecadeAposRe) {
		fixed := strings.Map(func(r rune) rune {
			if r == '\'' || r == '’' {
				return -1
			}
			return r
		}, t.slice(m[0], m[1]))
		o.fix("en.punct.apostrophe-plural", m[0], m[1], fixed, "Plurals take no apostrophe: “1990s”.")
	}
	for _, g := range t.captures(enAbbrAposRe) {
		if !g[1].OK {
			continue
		}
		x, y := g[1].A, g[1].B
		o.fix("en.punct.apostrophe-plural", x, y+2, t.slice(x, y)+"s", "Plurals of abbreviations take no apostrophe: “PDFs”.")
	}
}

// enArticle is en.word.article: a/an by sound, from closed lists and the
// first letter. Acronyms are skipped (their pronunciation varies: "a SQL",
// "an SQL").
func enArticle(t *Text, _ *ctx, o *out) {
	words := t.words()
	for k := 0; k+1 < len(words); k++ {
		a1, b1, a2, b2 := words[k][0], words[k][1], words[k+1][0], words[k+1][1]
		if a2 == b1 || slices.ContainsFunc(t.Chars[b1:a2], func(r rune) bool { return r != ' ' }) {
			continue
		}
		art := t.slice(a1, b1)
		if art != "a" && art != "an" && art != "A" && art != "An" {
			continue
		}
		word := t.slice(a2, b2)
		first, ok := firstRune(word)
		if !ok {
			first = ' '
		}
		// A capital A mid-sentence is a letter ("Plan A is"), not an
		// article.
		if !isLower(first) || strings.HasPrefix(art, "A") && !t.sentenceStart(a1) {
			continue // acronyms, names, "Plan A is"
		}
		lw := toLower(word)
		base := lw
		if k := strings.IndexAny(lw, "-'"); k >= 0 {
			base = lw[:k]
		}
		vowelLetter := strings.ContainsRune("aeiou", first)
		var wantsAn bool
		switch {
		case slices.Contains(enAnWords, base) || slices.Contains(enAnWords, lw):
			wantsAn = true
		case slices.Contains(enAWords, base) || slices.Contains(enAWords, lw) ||
			strings.HasPrefix(lw, "uni") || strings.HasPrefix(lw, "eu") || strings.HasPrefix(lw, "one") ||
			strings.HasPrefix(lw, "use") || strings.HasPrefix(lw, "usu") || strings.HasPrefix(lw, "uti"):
			wantsAn = false
		case first == 'u':
			// un-, up-, us-, ul-, um-: a vowel sound.
			wantsAn = true
		default:
			wantsAn = vowelLetter
		}
		isAn := strings.EqualFold(art, "an")
		if wantsAn != isAn {
			fixed := "A"
			switch {
			case art == "a" && wantsAn:
				fixed = "an"
			case art == "A" && wantsAn:
				fixed = "An"
			case art == "an" && !wantsAn:
				fixed = "a"
			}
			o.fix("en.word.article", a1, b1, fixed, "“"+word+"” takes “"+toLower(fixed)+"”.")
		}
	}
}

// enSpellingVariety is en.spelling.variety: US and UK spellings mixed - the
// minority flagged, or (with a preset) the other variety's forms. Oxford
// spelling keeps -ize; GOV.UK uses -ise. Proper nouns (Labour Party) are
// skipped.
func enSpellingVariety(t *Text, c *ctx, o *out) {
	p, s := c.profile, c.stats
	var uk bool
	switch {
	case p.regionIs("GB") || p.regionIs("AU"):
		uk = true
	case p.regionIs("US"):
		uk = false
	case minority(s.enUS, s.enUK):
		uk = true
	case minority(s.enUK, s.enUS):
		uk = false
	default:
		return
	}
	for _, wr := range t.words() {
		a, b := wr[0], wr[1]
		w := t.slice(a, b)
		if r, ok := firstRune(w); ok && isUpper(r) && !t.sentenceStart(a) {
			continue
		}
		if t.inQuotes(a, b) {
			continue
		}
		lw := toLower(w)
		i := slices.IndexFunc(enSpelling, func(pair [2]string) bool {
			if uk {
				return pair[0] == lw
			}
			return pair[1] == lw
		})
		if i < 0 {
			continue
		}
		us, gb := enSpelling[i][0], enSpelling[i][1]
		// Oxford spelling: British, but -ize.
		if uk && p.is("oxford") && strings.Contains(us, "iz") {
			continue
		}
		to := us
		if uk {
			to = gb
		}
		why := "most of this document"
		if p.Explicit {
			why = "the chosen style"
		}
		o.fix("en.spelling.variety", a, b, matchCase(w, to), "Spelling variety: "+why+" uses “"+to+"”.")
	}
}
