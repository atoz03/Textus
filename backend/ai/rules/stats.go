package rules

import "strings"

// How a text writes the conventions sources disagree on, counted over the
// document (or paragraph). Automatic follows the majority and flags only a
// clear minority.

// Stats are the convention counts of a document or paragraph.
type Stats struct {
	// CJK next to a Latin letter or digit, with one space between / none.
	cjkLatinSpaced, cjkLatinTight int
	// Sentence punctuation after a CJK character: full-width / half-width.
	cjkFwPunct, cjkHwPunct int
	// Double quotes: straight, English curly “”, German „“, guillemets «»
	// and »«, corner brackets 「」.
	dqStraight, dqCurly, dqLow, dqGuillemet, dqGuillemetRev, dqCorner int
	// Apostrophes between letters: ' / ’.
	aposStraight, aposCurly int
	// Before French ; : ! ? - nothing, a space, NBSP, NNBSP.
	frNone, frSpace, frNbsp, frNnbsp int
	// … / ... in Latin text.
	ellipsisChar, ellipsisDots int
	// word—word / word — word / word – word.
	dashEmTight, dashEmSpaced, dashEnSpaced int
	// 5 % / 5%.
	pctSpaced, pctTight int
	// 3 pm / 15:00.
	clock12, clock24 int
	// Japanese 、。 / ，．
	jaTouten, jaComma, jaKuten, jaPeriod int
	// Japanese sentence endings: です・ます / である.
	jaDesu, jaDearu int
	// Korean sentence endings: 하십시오체 / 해요체.
	koFormal, koPolite int
	// English spelling families.
	enUS, enUK int
	// German ß present anywhere.
	deEszett int
	// Chinese dashes: —— / ──.
	zhEmDash, zhBoxDash int
	// Katakana words, for ja.word.choon.
	katakana map[string]int
	// Words whose spelling varies, by family (okurigana, 子ども/子供).
	variants map[string]int
	// Number grouping in 5+ digit numbers: "12.500" / "12 500" (NBSP or
	// space) / "12,500".
	groupDot, groupSpace, groupComma int
	// French dialogue paragraphs opening with – / —.
	dialogueEn, dialogueEm int
}

// minority is whether instances counted this are the clear minority against
// other: at least three of the other kind, and at least twice as many. A
// tie, or one or two examples, is no majority - nothing gets flagged.
func minority(this, other int) bool { return this > 0 && other >= 3 && other >= 2*this }

var (
	dqStraightRe = MustRe(`"[^"\n]+"`)
	aposRe       = MustRe(`\p{L}['’]\p{L}`)
	pctRe        = MustRe(`\d([ \u00A0\u202F]?)%`)
	clock12Re    = MustRe(`(?i)\b\d{1,2}(?::\d{2})?\s?(?:a\.?m\.?|p\.?m\.?)(?:\W|\z)`)
	clock24Re    = MustRe(`\b(?:1[3-9]|2[0-3]):[0-5]\d\b`)
	ellipsisRe   = MustRe(`\p{L}\.\.\.`)
	groupedRe    = MustRe(`\b\d{1,3}(?:([.,\u00A0\u202F ])\d{3})+\b`)
	desuRe       = MustRe(`(?:です|ます|でした|ました|ません|でしょう|ましょう)[。！？]`)
	dearuRe      = MustRe(`(?:である|であった|ではない|だった|のだ|なのだ)[。！？]`)
	koFormalRe   = MustRe(`(?:습니다|ㅂ니다|십시오|습니까|입니다|합니다|됩니다)[.?!]`)
	// 해요체 statements and questions. The polite imperative -(으)세요 is
	// left out: manuals in 하십시오체 use it for instructions as a matter of
	// course.
	koPoliteRe = MustRe(`[\p{Hangul}-[세]](?:요|죠)[.?!]`)
)

// enSpelling are US/UK spelling pairs (one family per pair). Proper nouns
// are skipped by the rule; the count only sees lower-case words.
var enSpelling = [][2]string{
	{"color", "colour"}, {"colors", "colours"}, {"favor", "favour"}, {"favorite", "favourite"},
	{"honor", "honour"}, {"labor", "labour"}, {"behavior", "behaviour"}, {"neighbor", "neighbour"},
	{"flavor", "flavour"}, {"humor", "humour"}, {"rumor", "rumour"}, {"center", "centre"},
	{"centers", "centres"}, {"theater", "theatre"}, {"meter", "metre"}, {"liter", "litre"},
	{"fiber", "fibre"}, {"defense", "defence"}, {"offense", "offence"}, {"catalog", "catalogue"},
	{"dialog", "dialogue"}, {"analog", "analogue"}, {"traveled", "travelled"}, {"traveling", "travelling"},
	{"traveler", "traveller"}, {"canceled", "cancelled"}, {"canceling", "cancelling"}, {"modeling", "modelling"},
	{"labeled", "labelled"}, {"gray", "grey"}, {"aluminum", "aluminium"}, {"mold", "mould"},
	{"plow", "plough"}, {"jewelry", "jewellery"}, {"pajamas", "pyjamas"}, {"analyze", "analyse"},
	{"analyzed", "analysed"}, {"paralyze", "paralyse"}, {"organize", "organise"}, {"organized", "organised"},
	{"organization", "organisation"}, {"realize", "realise"}, {"realized", "realised"}, {"recognize", "recognise"},
	{"recognized", "recognised"}, {"apologize", "apologise"}, {"criticize", "criticise"}, {"emphasize", "emphasise"},
	{"prioritize", "prioritise"}, {"summarize", "summarise"}, {"optimize", "optimise"}, {"optimization", "optimisation"},
	{"minimize", "minimise"}, {"maximize", "maximise"}, {"utilize", "utilise"}, {"specialize", "specialise"},
	{"customize", "customise"}, {"finalize", "finalise"}, {"authorize", "authorise"}, {"standardize", "standardise"},
}

// jaVariants are words spelled two ways in Japanese, as families:
// okurigana pairs and 子ども/子供. Each family's forms, the first the more
// common one.
var jaVariants = []struct {
	family string
	forms  []string
}{
	{"取り扱い", []string{"取り扱い", "取扱い"}},
	{"申し込み", []string{"申し込み", "申込み"}},
	{"打ち合わせ", []string{"打ち合わせ", "打合せ"}},
	{"問い合わせ", []string{"問い合わせ", "問合せ"}},
	{"引き続き", []string{"引き続き", "引続き"}},
	{"行う", []string{"行う", "行なう"}},
	{"表す", []string{"表す", "表わす"}},
	{"話し合い", []string{"話し合い", "話合い"}},
	{"書き込み", []string{"書き込み", "書込み"}},
	{"読み込み", []string{"読み込み", "読込み"}},
	{"組み合わせ", []string{"組み合わせ", "組合せ"}},
	{"子ども", []string{"子ども", "子供"}},
}

func isKatakanaLetter(c rune) bool { return c >= 0x30A1 && c <= 0x30FA }

// count adds the counts of one block of text in lang.
func (s *Stats) count(t *Text, lang Lang) {
	chars := t.Chars
	n := len(chars)
	cjkLang := lang == ZhHans || lang == ZhHant || lang == Ja
	for i := 0; i < n; i++ {
		c := chars[i]
		prev, hasPrev := t.before(i)
		next, hasNext := t.At(i + 1)
		if t.Protected[i] {
			continue
		}
		if cjkLang {
			// CJK-Latin boundaries.
			if IsCJK(c) && hasNext && isLatinOrDigit(next) {
				s.cjkLatinTight++
			}
			if isLatinOrDigit(c) && hasNext && IsCJK(next) {
				s.cjkLatinTight++
			}
			if c == ' ' && hasPrev && hasNext &&
				(IsCJK(prev) && isLatinOrDigit(next) || isLatinOrDigit(prev) && IsCJK(next)) {
				s.cjkLatinSpaced++
			}
			if hasPrev && IsCJK(prev) {
				switch c {
				case '，', '。', '；', '：', '？', '！', '、':
					s.cjkFwPunct++
				case ',', ';', '?', '!':
					s.cjkHwPunct++
				case '.':
					if !(hasNext && (isASCIIDigit(next) || next == '.')) {
						s.cjkHwPunct++
					}
				case ':':
					if !(hasNext && isASCIIDigit(next)) {
						s.cjkHwPunct++
					}
				}
			}
		}
		switch c {
		case '“':
			s.dqCurly++
		case '„':
			s.dqLow++
		case '«':
			s.dqGuillemet++
		case '»':
			if hasNext && !isSpace(next) && !(hasPrev && IsAlphanumeric(prev)) {
				s.dqGuillemetRev++
			}
		case '「':
			s.dqCorner++
		case '…':
			if hasPrev && isLatinLetter(prev) {
				s.ellipsisChar++
			}
		case '—':
			switch {
			case hasPrev && hasNext && prev == ' ' && next == ' ':
				s.dashEmSpaced++
			case hasPrev && hasNext && IsAlphabetic(prev) && IsAlphabetic(next) && !IsCJK(prev):
				s.dashEmTight++
			}
		case '–':
			if hasPrev && hasNext && prev == ' ' && next == ' ' {
				s.dashEnSpaced++
			}
		}
		if lang == Fr && strings.ContainsRune(";:!?", c) && i > 0 {
			digitsAround := isASCIIDigit(prev) && hasNext && isASCIIDigit(next)
			emoticon := hasNext && strings.ContainsRune(")(DP-", next)
			if !digitsAround && !emoticon && prev != '!' && prev != '?' {
				switch {
				case prev == ' ':
					s.frSpace++
				case prev == '\u00A0':
					s.frNbsp++
				case prev == '\u202F':
					s.frNnbsp++
				case IsAlphanumeric(prev) || strings.ContainsRune("»)\"”", prev):
					s.frNone++
				}
			}
		}
		if lang == De && c == 'ß' {
			s.deEszett++
		}
		if lang.IsChinese() && c == '—' && hasNext && next == '—' {
			s.zhEmDash++
		}
		if lang.IsChinese() && c == '─' && hasNext && next == '─' {
			s.zhBoxDash++
		}
	}
	for _, m := range t.find(dqStraightRe) {
		if !t.guarded(m[0], m[1]) {
			s.dqStraight++
		}
	}
	for _, m := range t.find(aposRe) {
		if !t.guarded(m[0], m[1]) {
			if chars[m[0]+1] == '\'' {
				s.aposStraight++
			} else {
				s.aposCurly++
			}
		}
	}
	for _, g := range t.captures(pctRe) {
		if !g[1].OK {
			continue
		}
		if g[1].A == g[1].B {
			s.pctTight++
		} else {
			s.pctSpaced++
		}
	}
	s.clock12 += len(t.find(clock12Re))
	s.clock24 += len(t.find(clock24Re))
	s.ellipsisDots += len(t.find(ellipsisRe))
	for _, g := range t.captures(groupedRe) {
		if !g[1].OK {
			continue
		}
		a, b := g[0].A, g[0].B
		if t.guarded(a, b) || b-a < 6 {
			continue
		}
		switch chars[g[1].A] {
		case '.':
			s.groupDot++
		case ',':
			s.groupComma++
		default:
			s.groupSpace++
		}
	}
	switch lang {
	case Ja:
		for _, c := range chars {
			switch c {
			case '、':
				s.jaTouten++
			case '，':
				s.jaComma++
			case '。':
				s.jaKuten++
			case '．':
				s.jaPeriod++
			}
		}
		s.jaDesu += len(t.find(desuRe))
		s.jaDearu += len(t.find(dearuRe))
		for i := 0; i < n; {
			if !isKatakanaLetter(chars[i]) {
				i++
				continue
			}
			start := i
			for i < n && (isKatakanaLetter(chars[i]) || chars[i] == 'ー') {
				i++
			}
			if i-start >= 3 {
				if s.katakana == nil {
					s.katakana = map[string]int{}
				}
				s.katakana[t.slice(start, i)]++
			}
		}
		for _, v := range jaVariants {
			for _, form := range v.forms {
				if hits := len(findStr(t, form)); hits > 0 {
					if s.variants == nil {
						s.variants = map[string]int{}
					}
					s.variants[form] += hits
				}
			}
		}
	case Ko:
		s.koFormal += len(t.find(koFormalRe))
		s.koPolite += len(t.find(koPoliteRe))
	case En:
		for _, w := range t.words() {
			word := t.slice(w[0], w[1])
			if c, _ := firstRune(word); isUpper(c) && !t.sentenceStart(w[0]) {
				continue
			}
			lw := toLower(word)
			isUS, isUK := false, false
			for _, p := range enSpelling {
				isUS = isUS || p[0] == lw
				isUK = isUK || p[1] == lw
			}
			if isUS {
				s.enUS++
			} else if isUK {
				s.enUK++
			}
		}
	case Fr:
		if n > 0 {
			switch chars[0] {
			case '–':
				s.dialogueEn++
			case '—':
				s.dialogueEm++
			}
		}
	}
}
