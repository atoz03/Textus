package paper

import (
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"

	"textus/backend/ai/aicompat"
)

func TestAReviewReportIsReadInOrderAndCappedPerSection(t *testing.T) {
	point := func(n int, problem string) map[string]any {
		return map[string]any{"section": n, "problem": problem, "fix": "fix it"}
	}
	args := asJSON(t, map[string]any{"issues": []any{
		point(2, "a"), point(2, "b"), point(1, "c"), point(2, "d"), point(2, "e"), point(1, " "),
	}})
	issues, err := readIssues(args, 2)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, i := range issues {
		got = append(got, fmt.Sprintf("%d %s", i.section, i.problem))
	}
	if want := []string{"1 a", "1 b", "0 c", "1 d"}; !slices.Equal(got, want) {
		t.Fatal(got)
	}
	if _, err := readIssues(asJSON(t, map[string]any{"issues": []any{point(3, "x")}}), 2); err == nil || !strings.Contains(err.Error(), "no section 3") {
		t.Fatal(err)
	}
	if issues, err := readIssues(`{"issues": []}`, 2); err != nil || len(issues) != 0 {
		t.Fatal(issues, err)
	}
	if _, err := readIssues("{}", 2); err == nil {
		t.Fatal("a report without issues was read")
	}
}

// reviser reviews by faulting sections 1 and 2, and revises section 1 well
// and section 2 never in a form that holds.
func reviser(t *testing.T) Model {
	return stepFunc(func(_ string, turns []aicompat.AgentTurn, tools []aicompat.ToolSpec) (aicompat.StepResult, error) {
		text := turns[0].Text
		last := turns[len(turns)-1]
		call := func(name string, args any) aicompat.StepResult {
			return calls(toolCall(fmt.Sprintf("c%d", len(turns)), name, args))
		}
		done := last.Kind == aicompat.ToolResult && (strings.HasPrefix(last.Output, "Kept") || strings.HasPrefix(last.Output, "Reported"))
		switch {
		case done || len(turns) > 6:
			return aicompat.Reply("Done."), nil
		case tools[0].Name == reportIssuesTool:
			if !strings.Contains(text, `<section number="2">`) {
				t.Errorf("the second section isn't in %s", text)
			}
			return call(reportIssuesTool, map[string]any{"issues": []any{
				map[string]any{"section": 1, "problem": "Repeats section 2", "fix": "Cut the overlap"},
				map[string]any{"section": 2, "problem": "Figure never mentioned", "fix": "Refer to it"},
			}}), nil
		}
		if !strings.Contains(text, "A reviewer who read the whole paper") {
			t.Errorf("not told of the review: %s", text)
		}
		// Section 2's revision cites nothing: a hard problem, never kept.
		markdown := "Roofs cool."
		if strings.Contains(text, "Cut the overlap") {
			markdown = sectionText(97, aChart)
		}
		return call(submitSectionTool, map[string]any{"markdown": markdown}), nil
	})
}

func TestFaultedSectionsAreRevisedOnceAndAFailedRevisionKeepsTheDraft(t *testing.T) {
	plan := outlineOf(2, 100)
	paper, dossier := english(), draftingDossier()
	sections := []string{"## Mechanism\n\nFirst draft.", "## Mechanism\n\nSecond draft."}
	issues, err := review(t.Context(), reviser(t), &paper, &plan, sections)
	if err != nil || len(issues) != 2 {
		t.Fatal(issues, err)
	}
	var mu sync.Mutex
	var revising []int
	changed := revise(t.Context(), reviser(t), &paper, &plan, &dossier, sections, issues, func(i int) {
		mu.Lock()
		revising = append(revising, i)
		mu.Unlock()
	}, ignore, nil)
	if !slices.Equal(changed, []int{0}) {
		t.Fatal(changed)
	}
	if !strings.HasPrefix(sections[0], "## Mechanism\n\nGreen Green") {
		t.Fatal(sections[0])
	}
	if sections[1] != "## Mechanism\n\nSecond draft." {
		t.Fatal(sections[1])
	}
	slices.Sort(revising)
	if !slices.Equal(revising, []int{0, 1}) {
		t.Fatal(revising)
	}
}

func TestASectionsCitedSentencesGoToTheCheckerWithTheirNotes(t *testing.T) {
	notes := []Note{{
		Claim:     "Roofs were up to 30 °C cooler.",
		Evidence:  "12 roofs.",
		Sources:   []uint32{1},
		Excerpt:   ptr("Measured on 12 roofs, up to 30 °C cooler."),
		Grounding: ReadGrounded,
	}}
	section := "Green roofs cool cities. They are always 30 °C cooler [1](https://a.example/1) [2](https://b.example/2).\n\n```chart\n{\"source\": \"[1](https://a.example/1)\"}\n```"
	text, sentences, ok := claimsMessage(section, notes)
	if !ok || !slices.Equal(sentences, []string{"They are always 30 °C cooler [1] [2]."}) {
		t.Fatal(sentences, ok)
	}
	want := "<sentence number=\"1\">\nThey are always 30 °C cooler [1] [2].\nIts sources' notes:\n  [1]\n  - Roofs were up to 30 °C cooler. Evidence: 12 roofs.\n    <external-content source=\"[1]\">\n    Measured on 12 roofs, up to 30 °C cooler.\n    </external-content>\n  [2] (no notes)\n</sentence>"
	if text != want {
		t.Fatalf("got  %q\nwant %q", text, want)
	}
	if _, _, ok := claimsMessage("Nothing cited.", notes); ok {
		t.Fatal("a section citing nothing was checked")
	}
}

func TestVerdictsBecomePointsTheWorstFirst(t *testing.T) {
	sentences := []string{"A [1].", "B [2].", "C [1].", "D [3].", "E [1]."}
	args := asJSON(t, map[string]any{"verdicts": []any{
		map[string]any{"sentence": 1, "verdict": "overstated", "why": "The notes say up to 30 °C.", "fix": "Say up to."},
		map[string]any{"sentence": 2, "verdict": "supported"},
		map[string]any{"sentence": 3, "verdict": "unsupported"},
		map[string]any{"sentence": 4, "verdict": "wrong_source", "why": "[1] says it.", "fix": "Cite [1]."},
		map[string]any{"sentence": 5, "verdict": "overstated"},
	}})
	issues, err := readVerdicts(args, 2, sentences)
	if err != nil {
		t.Fatal(err)
	}
	var problems []string
	for _, i := range issues {
		problems = append(problems, i.problem)
		if i.section != 2 {
			t.Fatal(i)
		}
	}
	want := []string{
		`"C [1]." isn't supported by its sources' notes.`,
		`"D [3]." cites a source that doesn't say it: [1] says it.`,
		`"A [1]." says more than its sources' notes: The notes say up to 30 °C.`,
	}
	if !slices.Equal(problems, want) {
		t.Fatal(problems)
	}
	if issues[2].fix != "Say up to." || !strings.HasPrefix(issues[0].fix, "Say only what its notes say") {
		t.Fatal(issues)
	}
	if _, err := readVerdicts(`{"verdicts": [{"sentence": 9, "verdict": "supported"}]}`, 0, sentences); err == nil {
		t.Fatal("a sentence there isn't was read")
	}
	if _, err := readVerdicts(`{"verdicts": [{"sentence": 1, "verdict": "maybe"}]}`, 0, sentences); err == nil {
		t.Fatal("a verdict there isn't was read")
	}
}

func TestTheReviewBringsTheChecksPointsWithItsOwn(t *testing.T) {
	// Reviews with nothing to report; checks sentences, faulting the first.
	checker := stepFunc(func(_ string, turns []aicompat.AgentTurn, tools []aicompat.ToolSpec) (aicompat.StepResult, error) {
		if len(turns) > 1 {
			return aicompat.Reply("Done."), nil
		}
		args := map[string]any{"issues": []any{}}
		if tools[0].Name == reportSupportTool {
			args = map[string]any{"verdicts": []any{
				map[string]any{"sentence": 1, "verdict": "overstated", "why": "Up to 30 °C.", "fix": "Say up to."},
			}}
		}
		return calls(toolCall("c", tools[0].Name, args)), nil
	})
	paper := Paper{ID: "abc", Topic: "Roofs", Genre: GenrePaper, Language: "en", TargetChars: 4000, Budget: PaperBudget()}
	dossier := Dossier{Notes: []Note{{Claim: "Roofs were up to 30 °C cooler.", Sources: []uint32{1}}}}
	sections := []string{
		"## A\n\nRoofs are always 30 °C cooler [1](https://a.example/1).",
		"## B\n\nNothing cited here.",
	}
	issues := reviewAll(t.Context(), checker, &paper, &Outline{}, &dossier, sections, true)
	if len(issues) != 1 {
		t.Fatal(issues)
	}
	// Off, as it is unless the user turns it on: the review's alone.
	if unchecked := reviewAll(t.Context(), checker, &paper, &Outline{}, &dossier, sections, false); len(unchecked) != 0 {
		t.Fatal(unchecked)
	}
	if issues[0].section != 0 || issues[0].problem != `"Roofs are always 30 °C cooler [1]." says more than its sources' notes: Up to 30 °C.` {
		t.Fatal(issues[0])
	}
}
