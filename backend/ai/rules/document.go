package rules

import (
	"strconv"
	"strings"
	"unicode/utf8"
)

// Whole-document checks: the Markdown parsed into blocks of prose (code,
// math, front matter, HTML never reach a rule), each in its own language,
// plus the rules only a document has: headings, image alt text, link text,
// 公文.

// scriptFamily is which script family a text is written in, by its
// letters; -1 for none.
func scriptFamily(text string) int {
	var scores [4]int
	for _, c := range text {
		switch {
		case IsCJK(c):
			// CJK characters carry about a word each, Latin letters a fifth.
			scores[0] += 5
		case IsHangul(c):
			scores[1] += 3
		case isCyrillic(c):
			scores[2]++
		case isLatinLetter(c):
			scores[3]++
		}
	}
	best := 0
	for f := 1; f < len(scores); f++ {
		// The last of equal scores, as Rust's max_by_key.
		if scores[f] >= scores[best] {
			best = f
		}
	}
	if scores[best] == 0 {
		return -1
	}
	return best
}

// blockLanguage is a block's language ("" for none): its own when it says
// so clearly, else the document's. A short block in the document's script
// is the document's language (a Japanese heading of kanji isn't Chinese).
func blockLanguage(text string, doc Lang) Lang {
	letters := 0
	for _, c := range text {
		if IsAlphabetic(c) {
			letters++
		}
	}
	own, ok := Detect(text)
	if doc == "" {
		if ok {
			return own
		}
		guess, _ := Guess(text)
		return guess
	}
	cjkFamily := func(l Lang) bool { return l == ZhHans || l == ZhHant || l == Ja }
	switch {
	case ok && own == doc:
		return own
	// Chinese and Japanese share ideographs: a block the detector calls
	// Chinese in a Japanese document (or the other script in a Chinese one)
	// is the document's language.
	case ok && cjkFamily(own) && cjkFamily(doc) && (own.IsChinese() || doc == Ja):
		return doc
	case ok && (letters >= 40 || scriptFamily(text) != scriptFamily(docSample(doc))):
		return own
	case ok:
		return doc
	}
	if f := scriptFamily(text); f >= 0 && f != scriptFamily(docSample(doc)) {
		guess, _ := Guess(text)
		return guess
	}
	return doc
}

// docSample is a word in the language's script, for comparing script
// families.
func docSample(lang Lang) string {
	switch lang {
	case ZhHans, ZhHant, Ja:
		return "中"
	case Ko:
		return "한"
	case Ru:
		return "я"
	}
	return "a"
}

// DocReport is what CheckDocument found, and under which conventions.
type DocReport struct {
	// Lang is the document's language ("": too little prose to tell).
	Lang     Lang
	Profile  *Profile
	Findings []Finding
}

// CheckDocument checks a whole Markdown document: every block in its own
// language, with the whole document's convention counts. Offsets are chars
// into markdown. A suggestion whose span crosses Markdown syntax is
// dropped, the flag kept.
func CheckDocument(markdown string, conv *Conventions, level Level, library []string) DocReport {
	return checkDocumentWith(markdown, "", conv, level, library)
}

// CheckDocumentWith checks a draft for the document context: the draft
// judged against the document's own convention majority, the document
// itself not checked.
func CheckDocumentWith(markdown, context string, conv *Conventions, level Level, library []string) DocReport {
	return checkDocumentWith(markdown, context, conv, level, library)
}

// checkDocumentWith is CheckDocument with context (the rest of the
// document, the text before a cursor; "" for none) counted into the
// convention majority but not checked.
func checkDocumentWith(markdown, context string, conv *Conventions, level Level, library []string) DocReport {
	parsed := parseMarkdown(markdown)
	texts := make([]string, len(parsed.blocks))
	for i, b := range parsed.blocks {
		texts[i] = string(b.text)
	}
	prose := strings.Join(texts, "\n")
	docLang, ok := Detect(prose)
	if !ok {
		docLang, _ = Guess(prose)
	}
	langs := make([]Lang, len(texts))
	ts := make([]*Text, len(texts))
	stats := map[Lang]*Stats{}
	count := func(t *Text, lang Lang) {
		if stats[lang] == nil {
			stats[lang] = &Stats{}
		}
		stats[lang].count(t, lang)
	}
	for i, s := range texts {
		langs[i] = blockLanguage(s, docLang)
		ts[i] = NewText(s)
		if langs[i] != "" {
			count(ts[i], langs[i])
		}
	}
	if context != "" {
		for _, b := range parseMarkdown(context).blocks {
			s := string(b.text)
			lang, ok := Detect(s)
			if !ok {
				lang = docLang
			}
			if lang != "" {
				count(NewText(s), lang)
			}
		}
	}
	src := []rune(markdown)
	sourceSlice := func(s, e int) string {
		s, e = min(s, len(src)), min(e, len(src))
		return string(src[s:max(s, e)])
	}
	var found []Finding
	for i, t := range ts {
		lang := langs[i]
		if lang == "" {
			continue
		}
		profile := conv.ProfileFor(lang)
		st := stats[lang]
		if st == nil {
			st = &Stats{}
		}
		c := &ctx{lang: lang, messages: lang, profile: &profile, stats: st, block: parsed.blocks[i].kind, library: library}
		o := newOut(t)
		runRules(t, c, o)
		at := parsed.blocks[i].at
		for _, f := range o.found {
			s, e := at[f.Start], at[f.End-1]+1
			source := sourceSlice(s, e)
			if source != f.Original {
				// A space beside Markdown syntax (`**粗体** 和`) is how CJK
				// emphasis is made to parse, not a spacing choice.
				if f.info().category == catSpacing {
					continue
				}
				f.Suggestion = nil
				f.Fix = FixNone
			}
			f.Start, f.End, f.Original = s, e, source
			found = append(found, f)
		}
	}
	var docProfile *Profile
	if docLang != "" {
		p := conv.ProfileFor(docLang)
		docProfile = &p
		found = append(found, documentRules(parsed, src, docLang, docProfile)...)
	}
	findings := finish(found, level, siteGC|siteDOC, true)
	// Line numbers: newlines before each offset.
	lineAt := make([]int, len(src)+1)
	lineAt[0] = 1
	for i, c := range src {
		lineAt[i+1] = lineAt[i]
		if c == '\n' {
			lineAt[i+1]++
		}
	}
	for i := range findings {
		line := lineAt[min(findings[i].Start, len(src))]
		findings[i].Line = &line
	}
	return DocReport{Lang: docLang, Profile: docProfile, Findings: findings}
}

// docFinding is a flag-only finding of a document rule over src[start:end].
func docFinding(rule string, src []rune, start, end int, message string) Finding {
	in := info(rule)
	s, e := min(start, len(src)), min(end, len(src))
	return Finding{
		Rule: rule, Start: start, End: end, Original: string(src[s:max(s, e)]),
		Severity: in.severity, Fix: FixNone, Priority: in.priority, Message: message,
	}
}

// documentRules are the rules that need the whole document.
func documentRules(parsed *parsedDoc, src []rune, lang Lang, profile *Profile) []Finding {
	var found []Finding
	span := func(i int) (int, int) {
		at := parsed.blocks[i].at
		return at[0], at[len(at)-1] + 1
	}
	// Heading structure: skipped levels, more than three levels.
	prev := 0
	deepReported := false
	for _, h := range parsed.headings {
		level, i := h[0], h[1]
		s, e := span(i)
		if prev > 0 && level > prev+1 {
			found = append(found, docFinding("shared.heading-structure", src, s, e,
				message("shared.heading-structure.skip", lang, strconv.Itoa(level), strconv.Itoa(prev))))
		}
		if level > 3 && !deepReported {
			found = append(found, docFinding("shared.heading-structure", src, s, e,
				message("shared.heading-structure.depth", lang)))
			deepReported = true
		}
		prev = level
	}
	// Image alt text.
	for _, img := range parsed.images {
		alt := strings.TrimSpace(img.alt)
		key := ""
		switch {
		case alt == "":
			key = "shared.image-alt.empty"
		case utf8.RuneCountInString(alt) > 155:
			key = "shared.image-alt.long"
		case lang == En && (strings.HasPrefix(toLower(alt), "image of") || strings.HasPrefix(toLower(alt), "picture of")):
			key = "shared.image-alt.redundant"
		}
		if key != "" {
			found = append(found, docFinding("shared.image-alt", src, img.start, img.end, message(key, lang)))
		}
	}
	// Link text that says nothing about where it goes.
	if lang == En {
		vague := []string{"click here", "here", "read more", "this link", "more", "link", "click"}
		for _, l := range parsed.links {
			text := toLower(strings.TrimSpace(l.text))
			for _, v := range vague {
				if text == v {
					found = append(found, docFinding("en.link.text", src, l.start, l.end,
						"Link text should say where the link goes, not “click here”."))
					break
				}
			}
		}
	}
	// 公文: a document titled 报告 must not ask for approval.
	if profile.is("gongwen") && profile.mainland() {
		title := ""
		if len(parsed.headings) > 0 {
			title = string(parsed.blocks[parsed.headings[0][1]].text)
		} else if len(parsed.blocks) > 0 {
			title = string(parsed.blocks[0].text)
		}
		if strings.Contains(title, "报告") && !strings.Contains(title, "请示") {
			for _, b := range parsed.blocks {
				text := string(b.text)
				for _, ask := range []string{"请批准", "请批示", "请审批"} {
					n := utf8.RuneCountInString(ask)
					for k := 0; ; {
						j := strings.Index(text[k:], ask)
						if j < 0 {
							break
						}
						a := utf8.RuneCountInString(text[:k+j])
						found = append(found, docFinding("zh.gongwen.report-request", src, b.at[a], b.at[a+n-1]+1,
							"报告中不得夹带请示事项：需要批准的事项请另行请示。"))
						k += j + len(ask)
					}
				}
			}
		}
	}
	return found
}
