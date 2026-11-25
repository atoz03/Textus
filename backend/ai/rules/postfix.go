package rules

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

// The safe fixes applied without asking to completions and proposal text,
// only where the document already follows the convention, so deliberate
// half-width punctuation isn't fought.

// ProposalText is proposal post-processing: the P0 "match the document"
// fixes, each only where the rest of the document follows that
// convention. It returns the fixed text and the rules applied.
func ProposalText(document, text string, conv *Conventions) (string, []string) {
	docLang, docOK := Detect(document)
	docReport := checkDocumentWith(document, "", conv, LevelStandard, nil)
	docStats := &Stats{}
	if docOK {
		docStats.count(NewText(document), docLang)
	}
	scriptChars := func(pred func(rune) bool) int {
		n := 0
		for _, c := range document {
			if pred(c) {
				n++
			}
		}
		return n
	}
	conforms := func(rule string) bool {
		if slices.ContainsFunc(docReport.Findings, func(f Finding) bool { return f.Rule == rule }) {
			return false
		}
		switch {
		case rule == "shared.nfc":
			return true
		case strings.HasPrefix(rule, "zh."):
			return docOK && docLang.IsChinese() && docStats.cjkFwPunct >= 3 && scriptChars(IsCJK) >= 20
		case rule == "ja.punct.halfwidth":
			return docOK && docLang == Ja && docStats.jaTouten+docStats.jaKuten+docStats.jaComma+docStats.jaPeriod >= 3
		case strings.HasPrefix(rule, "ja."):
			return docOK && docLang == Ja
		case strings.HasPrefix(rule, "ko."):
			return docOK && docLang == Ko && scriptChars(IsHangul) >= 20
		}
		return false
	}
	// French spacing the user chose, even where the document doesn't follow
	// it yet: models can't type a no-break space. Only the kind of space
	// changes.
	chosenFrench := docOK && docLang == Fr && conv.ProfileFor(Fr).Explicit
	unspaced := func(s string) string {
		return strings.Map(func(r rune) rune {
			if unicode.IsSpace(r) {
				return -1
			}
			return r
		}, s)
	}
	frenchSpace := func(f *Finding) bool {
		return chosenFrench && (f.Rule == "fr.space.high-punct" || f.Rule == "fr.num.symbol-space") &&
			f.Suggestion != nil && unspaced(*f.Suggestion) == unspaced(f.Original)
	}
	report := checkDocumentWith(text, document, conv, LevelStandard, nil)
	var fixes []Finding
	for _, f := range report.Findings {
		if f.info().sites&sitePR != 0 && f.Fix == FixSafe && f.Suggestion != nil && conforms(f.Rule) {
			fixes = append(fixes, f)
		}
	}
	if chosenFrench {
		// Strict: the space before % is an Info-level rule.
		for _, f := range checkDocumentWith(text, document, conv, LevelStrict, nil).Findings {
			if frenchSpace(&f) {
				fixes = append(fixes, f)
			}
		}
		slices.SortStableFunc(fixes, func(a, b Finding) int { return a.Start - b.Start })
	}
	rules := []string{}
	for _, f := range fixes {
		if len(rules) == 0 || rules[len(rules)-1] != f.Rule {
			rules = append(rules, f.Rule)
		}
	}
	return Apply(text, fixes), rules
}

// ProposalArguments is ProposalText on a propose_edit call's JSON
// arguments: the new arguments and the rules applied, ok when its text
// changed. The other fields are kept as written.
func ProposalArguments(arguments, document string, conv *Conventions) (string, []string, bool) {
	var fields map[string]jsontext.Value
	if json.Unmarshal([]byte(arguments), &fields) != nil {
		return "", nil, false
	}
	var text string
	if raw, ok := fields["text"]; !ok || json.Unmarshal(raw, &text) != nil {
		return "", nil, false
	}
	fixed, rules := ProposalText(document, text, conv)
	if fixed == text {
		return "", nil, false
	}
	value, err := json.Marshal(fixed)
	if err != nil {
		return "", nil, false
	}
	fields["text"] = value
	out, err := json.Marshal(fields, json.Deterministic(true))
	if err != nil {
		return "", nil, false
	}
	return string(out), rules, true
}

// tail is the last n chars of s.
func tail(s string, n int) string {
	i := len(s)
	for k := 0; k < n && i > 0; k++ {
		_, size := utf8.DecodeLastRuneInString(s[:i])
		i -= size
	}
	return s[i:]
}

// Completion is completion post-processing, matched to the text before the
// cursor: NFC, full-width CJK punctuation, CJK-Latin spacing, Korean
// particles, French spacing, quote style. Never word changes, never a space
// in Japanese or Korean.
func Completion(before, text string) string {
	if strings.TrimSpace(text) == "" {
		return text
	}
	context := tail(before, 400)
	lang, ok := Guess(context + text)
	b := &Stats{}
	if ok {
		b.count(NewText(context), lang)
	} else {
		lang = ""
	}
	conv := &Conventions{Project: ProjectConventions{Profiles: []string{}, Options: map[string]string{}}}
	allowed := []string{"shared.nfc"}
	switch {
	case lang.IsChinese():
		if b.cjkFwPunct >= 1 && b.cjkHwPunct == 0 {
			allowed = append(allowed, "zh.punct.halfwidth")
		}
		allowed = append(allowed, "zh.punct.ellipsis")
		mode := ""
		if b.cjkLatinSpaced >= 1 && b.cjkLatinTight == 0 {
			mode = "on"
		} else if b.cjkLatinTight >= 1 && b.cjkLatinSpaced == 0 {
			mode = "off"
		}
		if mode != "" {
			conv.Project.Options["cjk-latin-space"] = mode
			allowed = append(allowed, "zh.space.cjk-latin")
		}
	case lang == Ja:
		if b.jaTouten+b.jaKuten+b.jaComma+b.jaPeriod >= 1 {
			allowed = append(allowed, "ja.punct.halfwidth")
		}
		allowed = append(allowed, "ja.punct.dots-dash")
	case lang == Ko:
		allowed = append(allowed, "ko.space.particle")
	case lang == Fr:
		spaced := b.frSpace + b.frNbsp + b.frNnbsp
		spacing := ""
		if b.frNone == 0 && spaced >= 1 {
			spacing = "fr-FR"
		} else if spaced == 0 && b.frNone >= 1 {
			spacing = "fr-CA"
		}
		if spacing != "" {
			conv.Project.Options["fr.spacing"] = spacing
			allowed = append(allowed, "fr.space.high-punct")
		}
	}
	k := runeCount(context)
	var fixes []Finding
	for _, f := range checkAs(context+text, lang, conv, LevelStandard, nil, siteCP, "") {
		if !slices.Contains(allowed, f.Rule) || f.Suggestion == nil {
			continue
		}
		// A fix may reach back into the text before the cursor only by
		// characters it keeps (the CJK char before an added space).
		original, suggestion := []rune(f.Original), []rune(*f.Suggestion)
		for f.Start < k && len(original) > 0 && len(suggestion) > 0 && original[0] == suggestion[0] {
			original, suggestion = original[1:], suggestion[1:]
			f.Start++
		}
		if f.Start < k {
			continue
		}
		f.Start -= k
		f.End -= k
		f.Original = string(original)
		s := string(suggestion)
		f.Suggestion = &s
		fixes = append(fixes, f)
	}
	return matchQuotes(context, Apply(text, fixes))
}

// matchQuotes turns straight and curly double quotes and apostrophes in a
// completion into the kind the text before the cursor uses throughout.
func matchQuotes(before, text string) string {
	count := func(s string, cs ...rune) int {
		n := 0
		for _, c := range s {
			if slices.Contains(cs, c) {
				n++
			}
		}
		return n
	}
	straight, curly := count(before, '"'), count(before, '“', '”')
	out := []rune(text)
	last, hasLast := lastRune(before)
	prevOf := func(i int) (rune, bool) {
		if i == 0 {
			return last, hasLast
		}
		return out[i-1], true
	}
	if curly >= 2 && straight == 0 {
		for i := range out {
			if out[i] == '"' {
				p, ok := prevOf(i)
				if !ok || isSpace(p) || p == '(' || p == '[' || p == '—' {
					out[i] = '“'
				} else {
					out[i] = '”'
				}
			}
		}
	} else if straight >= 2 && curly == 0 {
		for i, c := range out {
			if c == '“' || c == '”' {
				out[i] = '"'
			}
		}
	}
	aposS, aposC := count(before, '\''), count(before, '’')
	for i := range out {
		p, ok := prevOf(i)
		if !(ok && IsAlphabetic(p) && i+1 < len(out) && IsAlphabetic(out[i+1])) {
			continue
		}
		if out[i] == '\'' && aposC >= 2 && aposS == 0 {
			out[i] = '’'
		} else if out[i] == '’' && aposS >= 2 && aposC == 0 {
			out[i] = '\''
		}
	}
	return string(out)
}
