// Package rules is the deterministic writing-rules engine: the mechanical
// conventions of the document languages, checked and fixed in code, since
// models can't count characters or see a half-width comma. Each rule has
// an id and a priority.
package rules

import (
	"slices"
	"strings"
)

// Level is the grammar check's strictness setting (grammarStrictness).
type Level int

const (
	LevelTypos Level = iota
	LevelStandard
	LevelStrict
)

// LevelFromSetting reads the setting's value; "" for unset.
func LevelFromSetting(value string) Level {
	switch value {
	case "typos":
		return LevelTypos
	case "strict":
		return LevelStrict
	}
	return LevelStandard
}

// Finding is one thing a rule found.
type Finding struct {
	Rule string `json:"rule"`
	// Start and End are Unicode-scalar offsets into the checked text (the
	// paragraph for Check, the Markdown source for CheckDocument).
	Start      int      `json:"start"`
	End        int      `json:"end"`
	Original   string   `json:"original"`
	Suggestion *string  `json:"suggestion"`
	Severity   Severity `json:"severity"`
	Fix        Fix      `json:"fix"`
	Priority   Priority `json:"priority"`
	Message    string   `json:"message"`
	// Line is the 1-based line in the document (CheckDocument only).
	Line *int `json:"line,omitzero"`
}

func (f *Finding) info() *ruleInfo { return info(f.Rule) }

// suggestion is the finding's replacement, "" for none.
func (f *Finding) suggestion() string { return deref(f.Suggestion) }

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

type blockKind int

const (
	blockParagraph blockKind = iota
	blockHeading
	blockListItem
	blockQuote
	blockTableCell
	// blockUnknown is a paragraph from the grammar check: no Markdown
	// context.
	blockUnknown
)

// Block is what kind of block a text is - some rules apply only to
// headings, some skip lists, quotations or table cells.
type Block struct {
	kind  blockKind
	level int
}

var (
	blockPara  = Block{kind: blockParagraph}
	blockItem  = Block{kind: blockListItem}
	blockQuot  = Block{kind: blockQuote}
	blockCell  = Block{kind: blockTableCell}
	blockUnkno = Block{kind: blockUnknown}
)

func headingBlock(level int) Block { return Block{kind: blockHeading, level: level} }

func (b Block) isHeading() bool { return b.kind == blockHeading }

// ctx is what a rule knows besides the text.
type ctx struct {
	lang Lang
	// messages is the language a shared rule's message is written in: the
	// app's, so a writer reads it in the language they chose - else the
	// text's.
	messages Lang
	profile  *Profile
	// stats are convention counts for the document (or, in the grammar
	// check, the paragraph): what "the document's own majority" means.
	stats *Stats
	block Block
	// library is the library's names and aliases (the project's people,
	// places, terms).
	library []string
}

// out collects a rule's findings, dropping any that touch a protected span.
type out struct {
	t     *Text
	found []Finding
}

func newOut(t *Text) *out { return &out{t: t} }

func (o *out) add(rule string, start, end int, suggestion *string, message string) {
	if start >= end || end > o.t.Len() || o.t.guarded(start, end) {
		return
	}
	original := o.t.slice(start, end)
	if suggestion != nil && *suggestion == original {
		return
	}
	in := info(rule)
	o.found = append(o.found, Finding{
		Rule: rule, Start: start, End: end, Original: original, Suggestion: suggestion,
		Severity: in.severity, Fix: in.fix, Priority: in.priority, Message: message,
	})
}

// fix is a finding with a replacement.
func (o *out) fix(rule string, start, end int, to, message string) {
	o.add(rule, start, end, &to, message)
}

// flag is a finding that only flags.
func (o *out) flag(rule string, start, end int, message string) {
	o.add(rule, start, end, nil, message)
}

// runRules runs every rule for c.lang over one block.
func runRules(t *Text, c *ctx, o *out) {
	sharedRun(t, c, o)
	switch c.lang {
	case ZhHans, ZhHant:
		zhRun(t, c, o)
	case En:
		enRun(t, c, o)
	case Ja:
		jaRun(t, c, o)
	case Ko:
		koRun(t, c, o)
	case Es:
		esRun(t, c, o)
	case Fr:
		frRun(t, c, o)
	case De:
		deRun(t, c, o)
	case Ru:
		ruRun(t, c, o)
	case Pt:
		ptRun(t, c, o)
	}
}

// allowed is whether a rule runs at level. Document-level consistency info
// runs at "standard" too, and so do rules gated on a profile the user
// chose.
func allowed(in *ruleInfo, level Level, docLevel bool) bool {
	switch level {
	case LevelTypos:
		return in.category == catSpelling && in.priority != P2
	case LevelStandard:
		return in.priority != P2 || in.gated || docLevel && in.category == catConsistency
	}
	return true
}

// tidy sorts findings by position; of two that would both rewrite the same
// characters, the one with the higher priority (then the earlier one)
// stays. Flag-only findings never block a fix.
func tidy(found []Finding) []Finding {
	slices.SortStableFunc(found, func(a, b Finding) int {
		if a.Start != b.Start {
			return a.Start - b.Start
		}
		if a.Priority != b.Priority {
			return int(a.Priority) - int(b.Priority)
		}
		return a.End - b.End
	})
	kept := []Finding{}
	for _, f := range found {
		clash := f.Suggestion != nil && slices.ContainsFunc(kept, func(k Finding) bool {
			return k.Suggestion != nil && k.Start < max(f.End, f.Start+1) && f.Start < max(k.End, k.Start+1)
		})
		same := slices.ContainsFunc(kept, func(k Finding) bool {
			return k.Rule == f.Rule && k.Start == f.Start && k.End == f.End
		})
		if !clash && !same {
			kept = append(kept, f)
		}
	}
	return kept
}

// Check is one paragraph's findings for the grammar check, at level, with
// the shared rules' messages in the text's own language.
func Check(text string, conv *Conventions, level Level, library []string) []Finding {
	return CheckFor(text, conv, level, library, "")
}

// CheckFor is Check with the shared rules' messages in the UI language ui
// ("" for the text's).
func CheckFor(text string, conv *Conventions, level Level, library []string, ui string) []Finding {
	var messages Lang
	if ui != "" {
		for _, l := range Langs {
			if l.UICode() == ui {
				messages = l
				break
			}
		}
	}
	lang, _ := Guess(text)
	return checkAs(text, lang, conv, level, library, siteGC, messages)
}

// checkAs checks text read as lang ("" when too short to tell), keeping
// what runs at site; messages "" for the text's language.
func checkAs(text string, lang Lang, conv *Conventions, level Level, library []string, site uint8, messages Lang) []Finding {
	t := NewText(text)
	if lang == "" {
		// Too short to tell the language: only what holds in every one.
		profile := AutomaticProfile(En)
		c := &ctx{lang: En, messages: or(messages, En), profile: &profile, stats: &Stats{}, block: blockUnkno, library: library}
		o := newOut(t)
		sharedNFC(t, c, o)
		sharedDoubleSpace(t, c, o)
		return finish(o.found, level, site, false)
	}
	profile := conv.ProfileFor(lang)
	stats := &Stats{}
	stats.count(t, lang)
	c := &ctx{lang: lang, messages: or(messages, lang), profile: &profile, stats: stats, block: blockUnkno, library: library}
	o := newOut(t)
	runRules(t, c, o)
	return finish(o.found, level, site, false)
}

func or(l, fallback Lang) Lang {
	if l == "" {
		return fallback
	}
	return l
}

func finish(found []Finding, level Level, site uint8, docLevel bool) []Finding {
	return tidy(slices.DeleteFunc(found, func(f Finding) bool {
		in := f.info()
		return in.sites&site == 0 || !allowed(in, level, docLevel)
	}))
}

// Apply is text with the findings' suggestions applied (char offsets),
// skipping any that overlap one already applied.
func Apply(text string, findings []Finding) string {
	chars := []rune(text)
	var fixes []*Finding
	for i := range findings {
		if f := &findings[i]; f.Suggestion != nil && f.End <= len(chars) {
			fixes = append(fixes, f)
		}
	}
	slices.SortStableFunc(fixes, func(a, b *Finding) int { return a.Start - b.Start })
	var b strings.Builder
	at := 0
	for _, f := range fixes {
		if f.Start < at {
			continue
		}
		b.WriteString(string(chars[at:f.Start]))
		b.WriteString(*f.Suggestion)
		at = f.End
	}
	b.WriteString(string(chars[at:]))
	return b.String()
}
