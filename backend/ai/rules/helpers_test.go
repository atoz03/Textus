package rules

import (
	"fmt"
	"slices"
	"testing"
)

// Helpers for the per-language rule tests, as the Rust tests had them.

// pair is one finding as the tests compare it: what it covers, and its
// replacement (fix false for a flag).
type pair struct {
	orig, sugg string
	fix        bool
}

func (p pair) String() string {
	if !p.fix {
		return fmt.Sprintf("(%q, None)", p.orig)
	}
	return fmt.Sprintf("(%q, %q)", p.orig, p.sugg)
}

// findings are every rule's findings on text read as lang under profile
// ("" for Automatic), at every level, in block.
func findings(lang Lang, profile, text string, block Block) []Finding {
	conv := &Conventions{Project: ProjectConventions{Options: map[string]string{}}}
	if profile != "" {
		conv.Setting = map[string]any{string(lang): profile}
	}
	t := NewText(text)
	p := conv.ProfileFor(lang)
	stats := &Stats{}
	stats.count(t, lang)
	c := &ctx{lang: lang, messages: lang, profile: &p, stats: stats, block: block, library: []string{"Elizabeth", "林黛玉"}}
	o := newOut(t)
	runRules(t, c, o)
	return o.found
}

// hits are rule's findings on text, as pairs.
func hits(rule string, lang Lang, profile, text string) []pair {
	return hitsIn(rule, lang, profile, text, blockUnkno)
}

func hitsIn(rule string, lang Lang, profile, text string, block Block) []pair {
	var out []pair
	for _, f := range findings(lang, profile, text, block) {
		if f.Rule == rule {
			out = append(out, pair{f.Original, deref(f.Suggestion), f.Suggestion != nil})
		}
	}
	return out
}

// fixed is text with every fix of rule applied.
func fixed(rule string, lang Lang, profile, text string) string {
	var found []Finding
	for _, f := range findings(lang, profile, text, blockUnkno) {
		if f.Rule == rule {
			found = append(found, f)
		}
	}
	return Apply(text, found)
}

// one is the pair list for one expected fix; flagged for one flag.
func one(original, suggestion string) []pair { return []pair{{original, suggestion, true}} }

func flagged(original string) []pair { return []pair{{original, "", false}} }

func eqPairs(t *testing.T, got, want []pair, context ...any) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Errorf("%v\n got: %v\nwant: %v", fmt.Sprint(context...), got, want)
	}
}

func eqStr(t *testing.T, got, want string, context ...any) {
	t.Helper()
	if got != want {
		t.Errorf("%v\n got: %q\nwant: %q", fmt.Sprint(context...), got, want)
	}
}

func none(t *testing.T, got []pair, context ...any) {
	t.Helper()
	if len(got) != 0 {
		t.Errorf("%v: expected no hits, got %v", fmt.Sprint(context...), got)
	}
}
