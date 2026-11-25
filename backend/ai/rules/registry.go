package rules

import "encoding/json/jsontext"

// Every rule the engine ships, with its priority, category, severity, fix
// kind and run sites. Rules only report an id; callers filter on what is
// here.

// Severity is how serious a finding is.
type Severity string

const (
	Error   Severity = "error"
	Warning Severity = "warning"
	Info    Severity = "info"
)

// severityRank orders severities as Rust's derive(Ord) did.
func (s Severity) rank() int {
	switch s {
	case Error:
		return 0
	case Warning:
		return 1
	}
	return 2
}

// Fix is what can be done with a finding's suggestion.
type Fix string

const (
	// FixSafe is safe to apply without review ("fix all", post-processing).
	FixSafe Fix = "safe"
	// FixSuggest is a suggestion to review one by one.
	FixSuggest Fix = "suggest"
	// FixNone has nothing to apply: the finding only flags.
	FixNone Fix = "none"
)

// Priority orders rules: P0 first.
type Priority int

const (
	P0 Priority = iota
	P1
	P2
)

func (p Priority) String() string { return [...]string{"P0", "P1", "P2"}[p] }

// MarshalJSON writes the priority as Rust's serde did: "P0".
func (p Priority) MarshalJSON() ([]byte, error) { return jsontext.AppendQuote(nil, p.String()) }

type category int

const (
	// catSpelling is typo lists and misspellings - the only rules the
	// "typos" level runs.
	catSpelling category = iota
	catPunctuation
	catSpacing
	catCharacters
	catNumbers
	catWords
	catStyle
	catScript
	catStructure
	// catConsistency is mixed conventions in one document.
	catConsistency
)

// Where a rule runs: the grammar check, the document check, completion and
// proposal post-processing.
const (
	siteGC  uint8 = 1
	siteDOC uint8 = 2
	siteCP  uint8 = 4
	sitePR  uint8 = 8
)

type ruleInfo struct {
	id       string
	priority Priority
	category category
	severity Severity
	fix      Fix
	sites    uint8
	// gated rules run only under a profile or option the user chose
	// (gongwen, newmedia, GOV.UK...): a P2 rule of this kind runs at
	// "standard", since choosing the profile asked for it.
	gated bool
}

var registry = []ruleInfo{
	// Shared (all languages)
	{"shared.nfc", P0, catCharacters, Warning, FixSafe, siteGC | siteDOC | siteCP | sitePR, false},
	{"shared.space-before-punct", P0, catSpacing, Error, FixSafe, siteGC | siteDOC, false},
	{"shared.double-space", P1, catSpacing, Warning, FixSafe, siteGC | siteDOC, false},
	{"shared.doubled-word", P1, catSpelling, Warning, FixSuggest, siteGC | siteDOC, false},
	{"shared.residue", P1, catStyle, Warning, FixNone, siteDOC | sitePR, false},
	{"shared.library-name", P1, catSpelling, Warning, FixSuggest, siteGC | siteDOC, false},
	{"shared.heading-structure", P2, catStructure, Info, FixNone, siteDOC, false},
	{"shared.image-alt", P2, catStructure, Info, FixNone, siteDOC, false},

	// Simplified Chinese (and the region-neutral Chinese rules)
	{"zh.punct.halfwidth", P0, catPunctuation, Error, FixSafe, siteGC | siteDOC | siteCP | sitePR, false},
	{"zh.space.fullwidth-adjacent", P0, catSpacing, Error, FixSafe, siteGC | siteDOC | sitePR, false},
	{"zh.punct.ellipsis", P0, catPunctuation, Error, FixSafe, siteGC | siteDOC | siteCP | sitePR, false},
	{"zh.punct.ellipsis-deng", P0, catPunctuation, Error, FixSafe, siteGC | siteDOC, false},
	{"zh.punct.dash", P1, catPunctuation, Warning, FixSuggest, siteGC | siteDOC, false},
	{"zh.punct.stacked", P1, catPunctuation, Warning, FixSuggest, siteGC | siteDOC, false},
	{"zh.heading.end-punct", P1, catPunctuation, Warning, FixSafe, siteGC | siteDOC, false},
	{"zh.quote.straight", P1, catPunctuation, Warning, FixSuggest, siteGC | siteDOC, false},
	{"zh.space.cjk-latin", P1, catSpacing, Warning, FixSuggest, siteGC | siteDOC | siteCP, false},
	{"zh.width.alnum", P0, catCharacters, Error, FixSafe, siteGC | siteDOC | sitePR, false},
	{"zh.num.decimal", P1, catNumbers, Error, FixSuggest, siteGC | siteDOC, false},
	{"zh.num.approx", P1, catNumbers, Warning, FixSuggest, siteGC | siteDOC, false},
	{"zh.title.dunhao", P2, catPunctuation, Warning, FixSuggest, siteGC | siteDOC, false},
	{"zh.num.mixed-date", P1, catNumbers, Warning, FixNone, siteGC | siteDOC, false},
	{"zh.num.short-year", P2, catNumbers, Info, FixNone, siteGC | siteDOC, false},
	{"zh.num.mixed-unit", P1, catNumbers, Warning, FixSuggest, siteGC | siteDOC, false},
	{"zh.date.dunhao", P2, catNumbers, Info, FixSuggest, siteGC | siteDOC, false},
	{"zh.punct.middot", P1, catPunctuation, Warning, FixSuggest, siteGC | siteDOC, false},
	{"zh.word.typo", P0, catSpelling, Error, FixSafe, siteGC | siteDOC, false},
	{"zh.word.variant", P2, catWords, Info, FixSuggest, siteGC | siteDOC, false},
	{"zh.script.mixed", P1, catScript, Warning, FixSuggest, siteGC | siteDOC, false},
	{"zh.num.unit-space", P2, catSpacing, Info, FixSuggest, siteGC | siteDOC, false},
	{"zh.num.ascii-range", P2, catNumbers, Info, FixSuggest, siteGC | siteDOC, false},
	{"zh.style.clause-length", P2, catStyle, Info, FixNone, siteDOC, false},
	{"zh.compat-ideograph", P0, catCharacters, Error, FixSafe, siteGC | siteDOC, false},
	{"zh.gongwen.docnum", P1, catNumbers, Error, FixSuggest, siteGC | siteDOC, true},
	{"zh.gongwen.date", P1, catNumbers, Error, FixSuggest, siteGC | siteDOC, true},
	{"zh.gongwen.title", P1, catStructure, Warning, FixNone, siteDOC, true},
	{"zh.gongwen.report-request", P1, catStructure, Warning, FixNone, siteDOC, true},
	{"zh.ad.absolute", P1, catWords, Warning, FixNone, siteGC | siteDOC, true},
	{"zh.ad.health", P2, catWords, Warning, FixNone, siteGC | siteDOC, true},
	{"zh.ad.unsourced", P2, catWords, Info, FixNone, siteDOC, true},

	// Traditional Chinese
	{"tw.quote.corner", P1, catPunctuation, Warning, FixSuggest, siteGC | siteDOC, false},
	{"tw.punct.middot", P2, catPunctuation, Info, FixSuggest, siteGC | siteDOC, true},
	{"tw.punct.dash", P2, catConsistency, Info, FixSuggest, siteDOC, false},
	{"tw.script.simplified", P1, catScript, Warning, FixSuggest, siteGC | siteDOC, false},
	{"tw.script.hk-mix", P1, catScript, Warning, FixSuggest, siteGC | siteDOC, false},
	{"tw.word.mainland", P2, catWords, Info, FixSuggest, siteGC | siteDOC, false},
	{"tw.gongwen.legal-chars", P2, catWords, Info, FixSuggest, siteGC | siteDOC, true},
	{"tw.gongwen.year", P2, catNumbers, Info, FixSuggest, siteGC | siteDOC, true},
	{"tw.gongwen.markers", P2, catNumbers, Info, FixSuggest, siteGC | siteDOC, true},
	{"tw.gongwen.tai", P2, catWords, Info, FixSuggest, siteGC | siteDOC, true},

	// English
	{"en.word.misspelling", P0, catSpelling, Error, FixSafe, siteGC | siteDOC, false},
	{"en.punct.apostrophe-plural", P1, catPunctuation, Warning, FixSuggest, siteGC | siteDOC, false},
	{"en.punct.double-hyphen", P1, catPunctuation, Warning, FixSuggest, siteGC | siteDOC, false},
	{"en.word.article", P1, catWords, Warning, FixSuggest, siteGC | siteDOC, false},
	{"en.heading.end-punct", P1, catPunctuation, Warning, FixSafe, siteGC | siteDOC, false},
	{"en.spelling.variety", P1, catConsistency, Warning, FixSuggest, siteGC | siteDOC, false},
	{"en.link.text", P2, catWords, Info, FixNone, siteDOC, false},
	{"en.punct.serial-comma", P2, catConsistency, Info, FixSuggest, siteDOC, true},
	{"en.punct.dash-style", P2, catConsistency, Info, FixSuggest, siteDOC, false},
	{"en.punct.quote-style", P2, catConsistency, Info, FixSuggest, siteDOC, false},
	{"en.num.sentence-start", P2, catNumbers, Info, FixNone, siteGC | siteDOC, false},
	{"en.num.clock", P2, catConsistency, Info, FixNone, siteDOC, false},
	{"en.word.etc", P2, catWords, Info, FixSuggest, siteGC | siteDOC, false},
	{"en.word.wordy", P2, catWords, Info, FixSuggest, siteGC | siteDOC, false},
	{"en.word.plain", P2, catWords, Info, FixSuggest, siteGC | siteDOC, true},
	{"en.word.please", P2, catWords, Info, FixNone, siteGC | siteDOC, true},
	{"en.word.inclusive", P2, catWords, Info, FixSuggest, siteGC | siteDOC, false},
	{"en.style.caps", P2, catStyle, Info, FixNone, siteGC | siteDOC, false},
	{"en.style.sentence-length", P2, catStyle, Info, FixNone, siteDOC, false},
	{"en.style.passive", P2, catStyle, Info, FixNone, siteGC | siteDOC, false},
	{"en.style.ai-vocab", P2, catStyle, Info, FixNone, siteDOC, false},
	{"en.num.minus", P2, catNumbers, Info, FixSuggest, siteGC | siteDOC, false},
	{"en.word.slash", P2, catWords, Info, FixNone, siteGC | siteDOC, false},
	{"en.govuk.style", P2, catWords, Info, FixSuggest, siteGC | siteDOC, true},

	// Japanese
	{"ja.width.kana", P0, catCharacters, Error, FixSafe, siteGC | siteDOC | sitePR, false},
	{"ja.punct.halfwidth", P0, catPunctuation, Error, FixSafe, siteGC | siteDOC | siteCP | sitePR, false},
	{"ja.width.alnum", P1, catCharacters, Warning, FixSafe, siteGC | siteDOC, false},
	{"ja.space.typed", P1, catSpacing, Warning, FixSafe, siteGC | siteDOC, false},
	{"ja.space.fullwidth", P1, catSpacing, Warning, FixSafe, siteGC | siteDOC, false},
	{"ja.punct.after-exclaim", P2, catSpacing, Info, FixSuggest, siteGC | siteDOC, false},
	{"ja.punct.tilde", P1, catPunctuation, Warning, FixSafe, siteGC | siteDOC, false},
	{"ja.num.counter", P1, catNumbers, Warning, FixSuggest, siteGC | siteDOC, false},
	{"ja.punct.dots-dash", P1, catPunctuation, Warning, FixSuggest, siteGC | siteDOC | siteCP, false},
	{"ja.punct.single-leader", P2, catPunctuation, Info, FixSuggest, siteGC | siteDOC, false},
	{"ja.style.register-mix", P1, catConsistency, Warning, FixNone, siteDOC, false},
	{"ja.word.ranuki", P2, catWords, Info, FixSuggest, siteGC | siteDOC, false},
	{"ja.style.no-repeat", P2, catStyle, Info, FixNone, siteGC | siteDOC, false},
	{"ja.style.double-negative", P2, catStyle, Info, FixNone, siteGC | siteDOC, false},
	{"ja.word.redundant", P2, catWords, Info, FixSuggest, siteGC | siteDOC, false},
	{"ja.style.sentence-length", P2, catStyle, Info, FixNone, siteDOC, false},
	{"ja.punct.period-bracket", P2, catPunctuation, Info, FixSuggest, siteGC | siteDOC, true},
	{"ja.keigo.patterns", P2, catWords, Info, FixNone, siteGC | siteDOC, false},
	{"ja.word.choon", P2, catConsistency, Info, FixSuggest, siteDOC, false},
	{"ja.word.okurigana", P2, catConsistency, Info, FixSuggest, siteDOC, false},
	{"ja.num.kanji-digit", P1, catNumbers, Warning, FixSuggest, siteGC | siteDOC, false},
	{"ja.word.kodomo", P2, catConsistency, Info, FixSuggest, siteDOC, false},

	// Korean
	{"ko.space.particle", P0, catSpacing, Error, FixSafe, siteGC | siteDOC | siteCP | sitePR, false},
	{"ko.punct.cjk-marks", P0, catPunctuation, Error, FixSafe, siteGC | siteDOC | sitePR, false},
	{"ko.word.myeochil", P0, catSpelling, Error, FixSafe, siteGC | siteDOC, false},
	{"ko.word.dwae", P0, catSpelling, Error, FixSafe, siteGC | siteDOC, false},
	{"ko.word.saisiot", P1, catSpelling, Error, FixSuggest, siteGC | siteDOC, false},
	{"ko.word.ryul", P1, catSpelling, Error, FixSuggest, siteGC | siteDOC, false},
	{"ko.date.format", P1, catNumbers, Warning, FixSuggest, siteGC | siteDOC, false},
	{"ko.punct.colon-space", P1, catSpacing, Warning, FixSafe, siteGC | siteDOC, false},
	{"ko.word.double-passive", P2, catWords, Info, FixNone, siteGC | siteDOC, false},
	{"ko.honorific.thing", P2, catWords, Info, FixNone, siteGC | siteDOC, false},
	{"ko.style.register-mix", P2, catConsistency, Info, FixNone, siteDOC, false},
	{"ko.official.format", P1, catStructure, Warning, FixSuggest, siteGC | siteDOC, true},

	// Spanish
	{"es.punct.inverted", P1, catPunctuation, Warning, FixNone, siteGC | siteDOC, false},
	{"es.punct.double-final", P1, catPunctuation, Warning, FixSafe, siteGC | siteDOC, false},
	{"es.quote.period-outside", P1, catPunctuation, Warning, FixSuggest, siteGC | siteDOC, false},
	{"es.word.monosyllable", P0, catSpelling, Error, FixSafe, siteGC | siteDOC, false},
	{"es.word.solo", P2, catSpelling, Info, FixSuggest, siteGC | siteDOC, true},
	{"es.word.months", P1, catWords, Warning, FixSuggest, siteGC | siteDOC, false},
	{"es.punct.etc", P1, catPunctuation, Warning, FixSafe, siteGC | siteDOC, false},
	{"es.num.group", P2, catNumbers, Info, FixSuggest, siteGC | siteDOC, false},
	{"es.num.ordinal", P2, catNumbers, Info, FixSuggest, siteGC | siteDOC, false},
	{"es.num.percent", P2, catSpacing, Info, FixSuggest, siteGC | siteDOC, false},
	{"es.word.ex", P2, catWords, Info, FixSuggest, siteGC | siteDOC, false},
	{"es.num.word-unit", P2, catNumbers, Info, FixNone, siteGC | siteDOC, false},
	{"es.dialogue.raya", P1, catPunctuation, Warning, FixSuggest, siteGC | siteDOC, false},

	// French
	{"fr.space.high-punct", P1, catSpacing, Warning, FixSuggest, siteGC | siteDOC | siteCP, false},
	{"fr.num.ordinal", P1, catNumbers, Warning, FixSuggest, siteGC | siteDOC, false},
	{"fr.word.mr", P1, catWords, Warning, FixSuggest, siteGC | siteDOC, false},
	{"fr.word.euphonic-t", P0, catSpelling, Error, FixSafe, siteGC | siteDOC, false},
	{"fr.word.ligature", P1, catSpelling, Warning, FixSuggest, siteGC | siteDOC, false},
	{"fr.num.symbol-space", P2, catSpacing, Info, FixSuggest, siteGC | siteDOC, false},
	{"fr.punct.etc", P1, catPunctuation, Warning, FixSafe, siteGC | siteDOC, false},
	{"fr.word.capital-accent", P2, catSpelling, Info, FixSuggest, siteGC | siteDOC, false},
	{"fr.punct.apostrophe", P2, catConsistency, Info, FixSuggest, siteDOC, false},
	{"fr.dialogue.dash", P2, catConsistency, Info, FixSuggest, siteDOC, false},

	// German
	{"de.quote.style", P1, catPunctuation, Warning, FixSuggest, siteGC | siteDOC, false},
	{"de.quote.reporting-comma", P1, catPunctuation, Warning, FixSuggest, siteGC | siteDOC, false},
	{"de.punct.dash", P1, catPunctuation, Warning, FixSuggest, siteGC | siteDOC, false},
	{"de.word.abbr-space", P1, catSpacing, Warning, FixSuggest, siteGC | siteDOC, false},
	{"de.word.removed-variant", P1, catSpelling, Error, FixSuggest, siteGC | siteDOC, false},
	{"de.num.percent", P2, catSpacing, Info, FixSuggest, siteGC | siteDOC, false},
	{"de.comma.subordinate", P2, catPunctuation, Info, FixSuggest, siteGC | siteDOC, false},
	{"de.comma.adversative", P2, catPunctuation, Info, FixSuggest, siteGC | siteDOC, false},
	{"de.date.zero-pad", P2, catNumbers, Info, FixSuggest, siteGC | siteDOC, true},
	{"de.num.group", P2, catNumbers, Info, FixSuggest, siteGC | siteDOC, false},
	{"de.word.eszett", P2, catSpelling, Info, FixSuggest, siteGC | siteDOC, false},
	{"de.word.gender-chars", P2, catWords, Info, FixNone, siteGC | siteDOC, true},

	// Russian
	{"ru.quote.style", P1, catPunctuation, Warning, FixSuggest, siteGC | siteDOC, false},
	{"ru.quote.period-outside", P1, catPunctuation, Warning, FixSuggest, siteGC | siteDOC, false},
	{"ru.quote.exclaim-period", P2, catPunctuation, Info, FixNone, siteGC | siteDOC, false},
	{"ru.punct.dash", P1, catPunctuation, Warning, FixSuggest, siteGC | siteDOC, false},
	{"ru.punct.comma-dash", P1, catPunctuation, Warning, FixSuggest, siteGC | siteDOC, false},
	{"ru.word.abbr", P1, catSpacing, Warning, FixSuggest, siteGC | siteDOC, false},
	{"ru.num.ending", P1, catNumbers, Error, FixSuggest, siteGC | siteDOC, false},
	{"ru.num.unit", P1, catNumbers, Warning, FixSuggest, siteGC | siteDOC, false},
	{"ru.num.group", P2, catNumbers, Info, FixSuggest, siteGC | siteDOC, false},
	{"ru.num.percent", P2, catConsistency, Info, FixSuggest, siteDOC, false},
	{"ru.dialogue.dash", P1, catPunctuation, Warning, FixSuggest, siteGC | siteDOC, false},

	// Portuguese
	{"pt.quote.style", P1, catPunctuation, Warning, FixSuggest, siteGC | siteDOC, false},
	{"pt.spell.pre-ao90", P0, catSpelling, Error, FixSafe, siteGC | siteDOC, false},
	{"pt.spell.hyphen", P1, catSpelling, Warning, FixSuggest, siteGC | siteDOC, false},
	{"pt.num.ordinal", P1, catNumbers, Warning, FixSuggest, siteGC | siteDOC, false},
	{"pt.word.months", P1, catWords, Warning, FixSuggest, siteGC | siteDOC, false},
	{"pt.official.address", P2, catWords, Info, FixNone, siteGC | siteDOC, true},
	{"pt.official.date", P2, catNumbers, Info, FixSuggest, siteGC | siteDOC, true},
	{"pt.num.group", P2, catConsistency, Info, FixSuggest, siteDOC, false}}

var registryByID = func() map[string]*ruleInfo {
	m := make(map[string]*ruleInfo, len(registry))
	for i := range registry {
		m[registry[i].id] = &registry[i]
	}
	return m
}()

// info is a rule's entry; a rule missing from the registry is the
// program's mistake.
func info(id string) *ruleInfo {
	r, ok := registryByID[id]
	if !ok {
		panic("rule " + id + " is not in the registry")
	}
	return r
}
