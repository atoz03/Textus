package ai

import (
	"strings"
	"testing"
)

func TestTheChangesAreReadHoweverTheyAreWrapped(t *testing.T) {
	fenced := "Here you go:\n```json\n{\"changes\": [{\"op\": \"add\", \"kind\": \"preference\", \"scope\": \"personal\", \"text\": \"Reply briefly.\"}]}\n```"
	if changes := ParseChanges(fenced); len(changes) != 1 || changes[0].Text != "Reply briefly." {
		t.Errorf("got %+v", changes)
	}
	for _, none := range []string{`{"changes": []}`, "Nothing to remember.", "} not json {"} {
		if changes := ParseChanges(none); len(changes) != 0 {
			t.Errorf("%q: got %+v", none, changes)
		}
	}
}

func TestATidyIsDueOnceEnoughWasAddedOrAWeekWentBy(t *testing.T) {
	const day = 24 * 60 * 60
	last := &LastTidy{At: 0, Count: 8}
	for _, c := range []struct {
		count int
		last  *LastTidy
		now   uint64
		due   bool
	}{
		{5, nil, 0, false},
		{8, nil, 0, true},
		{10, last, day, false},
		{14, last, day, true},
		{9, last, 8 * day, true},
		// A week on with nothing new: nothing to tidy.
		{8, last, 8 * day, false},
	} {
		if got := TidyDue(c.count, c.last, c.now); got != c.due {
			t.Errorf("%d memories at %d: got %v", c.count, c.now, got)
		}
	}
}

// memoriesAged are three personal preferences remembered a day apart, the
// first oldest.
func memoriesAged() Memories {
	var items []Memory
	for i, text := range []string{"Reply in English.", "Always answer in English.", "Use short sentences."} {
		items = append(items, Memory{
			ID: "personal/memory/" + strings.ReplaceAll(strings.TrimSuffix(text, "."), " ", "-"), Scope: ScopePersonal,
			Kind: Preference, Text: text, Remembered: Remembered{Updated: "2026-01-0" + string(rune('1'+i)) + "T00:00:00Z"},
		})
	}
	return Memories{Items: items}
}

func TestATidyOnlyUpdatesAndForgetsWhatItWasShown(t *testing.T) {
	memories := memoriesAged()
	oldest, middle, newest := memories.Items[0].ID, memories.Items[1].ID, memories.Items[2].ID
	answer := `{"changes": [{"op": "forget", "id": "` + oldest + `", "into": "` + newest + `"}, {"op": "forget", "id": "` + newest +
		`", "into": "` + middle + `"}, {"op": "add", "kind": "fact", "text": "New."}, {"op": "forget", "id": "personal/memory/made-up", "into": "` + newest + `"}]}`
	// The older into the newer: kept. The newest into an older one it
	// isn't merged with: refused. An addition, a made-up id: refused.
	if changes := TidyChanges(answer, memories); len(changes) != 1 || changes[0].TheID() != oldest {
		t.Errorf("got %+v", changes)
	}
	// Into one updated in the same answer, a merge: any age.
	merge := `{"changes": [{"op": "update", "id": "` + oldest + `", "text": "Reply in English."}, {"op": "forget", "id": "` + newest + `", "into": "` + oldest + `"}]}`
	if changes := TidyChanges(merge, memories); len(changes) != 2 || changes[0].TheOp() != "update" {
		t.Errorf("got %+v", changes)
	}
	// Shown by scope, oldest first.
	if input := TidyInput(memories); !strings.HasSuffix(input, "- ["+newest+"] (personal, preference) Use short sentences.") ||
		!strings.HasPrefix(input, "- ["+oldest+"]") {
		t.Errorf("got %s", input)
	}
}

func exchangeOf(message string) Exchange {
	return Exchange{Messages: []string{message}, Reply: "Done."}
}

func TestOnlyARequestLongEnoughMayBeAProcedure(t *testing.T) {
	if MayOffer(exchangeOf("Shorter, please.")) || MayOffer(Exchange{}) {
		t.Error("a short request was offered")
	}
	if !MayOffer(exchangeOf("Translate the newsletter into English: keep the names in pinyin, use British spelling, and put a glossary of the terms at the end.")) {
		t.Error("a procedure was not offered")
	}
}

func TestAnOfferNeedsEveryPartAndANameOfItsOwn(t *testing.T) {
	skills := []AgentSkill{ParseSkillFile("translate", "---\nname: translate\ndescription: Translates.\n---\n\nTranslate it.")}
	ok := `{"skill": {"name": "Newsletter translation", "description": "Translates the newsletter.", "instructions": "Keep names in pinyin."}}`
	if offer := ParseOffer(ok, skills); offer == nil || offer.Name != "Newsletter-translation" {
		t.Errorf("got %+v", offer)
	}
	for _, refused := range []string{
		`{"skill": null}`,
		`{"skill": {"name": "Translate", "description": "x", "instructions": "y"}}`,
		`{"skill": {"name": "A", "description": "", "instructions": "y"}}`,
		"no json",
	} {
		if offer := ParseOffer(refused, skills); offer != nil {
			t.Errorf("%s: got %+v", refused, offer)
		}
	}
	input := OfferInput(exchangeOf("Write the weekly report."), skills)
	if !strings.Contains(input, "---the user's skills---\n- translate: Translates.") || !strings.Contains(input, "---the assistant's reply---\nDone.") {
		t.Errorf("got %s", input)
	}
}

func TestAnOfferedSkillFileQuotesItsFrontMatter(t *testing.T) {
	file := OfferedSkillFile(SkillOffer{Name: "Weekly-report", Description: `Turns notes into "the" report.`, Instructions: "Three sections."})
	want := "---\nname: \"Weekly-report\"\ndescription: \"Turns notes into \\\"the\\\" report.\"\nmetadata:\n  textus-invocation: \"both\"\n---\n\nThree sections.\n"
	if file != want {
		t.Errorf("got %q", file)
	}
	if skill := ParseSkillFile("x", file); skill.Name != "Weekly-report" || skill.Description != `Turns notes into "the" report.` {
		t.Errorf("got %+v", skill)
	}
}

func TestTooShortAnAliasAndNormalized(t *testing.T) {
	for alias, short := range map[string]bool{"晚": true, "LW": true, "阿晚": false, "Wan": false, "": true} {
		if TooShortAnAlias(alias) != short {
			t.Errorf("%q", alias)
		}
	}
	if Normalized("Keep it short.") != Normalized("keep it  SHORT") || Normalized("ＡＢＣ，") != "abc" {
		t.Errorf("got %q, %q", Normalized("Keep it short."), Normalized("ＡＢＣ，"))
	}
}
