package paper

import (
	"encoding/json/v2"
	"fmt"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"textus/backend/ai/aicompat"
)

// outlineOf is testOutline with n copies of its section, each words long.
func outlineOf(n int, words uint32) Outline {
	o := testOutline()
	section := o.Sections[0]
	o.Sections = make([]OutlineSection, n)
	for i := range o.Sections {
		o.Sections[i] = OutlineSection{
			Title:       section.Title,
			KeyPoints:   slices.Clone(section.KeyPoints),
			Evidence:    slices.Clone(section.Evidence),
			TargetWords: words,
			Figures:     slices.Clone(section.Figures),
		}
	}
	return o
}

// paperShape is the shape of a paper of targetChars characters with
// images generated pictures left.
func paperShape(targetChars, images uint32) shape {
	return shapeOf(GenrePaper.Profile().Scale, targetChars, images)
}

func TestAnOutlineIsCheckedForEverythingAtOnce(t *testing.T) {
	d := testDossier()
	if found := outlineProblems(ptr(outlineOf(4, 2000)), &d, 8000, paperShape(3200, 6)); len(found) != 0 {
		t.Fatal(found)
	}

	bad := outlineOf(4, 2000)
	bad.Thesis = " "
	bad.Sections[1].Evidence = []string{"n9"}
	bad.Sections[2].KeyPoints = []string{" "}
	for i := range bad.Sections {
		bad.Sections[i].Figures = nil
	}
	bad.Sections[0].Figures = []FigurePlan{{Kind: Generated, Spec: "a"}, {Kind: Generated, Spec: "b"}}
	found := strings.Join(outlineProblems(&bad, &d, 8000, paperShape(3200, 1)), "\n")
	for _, want := range []string{
		"`thesis` is empty",
		`notes that don't exist: ["n9"]`,
		`Section 3 ("Mechanism") has no key points`,
		"2 generated pictures, but only 1",
	} {
		if !strings.Contains(found, want) {
			t.Errorf("no %q in %s", want, found)
		}
	}
	// Two figures for four sections is enough; lengths add to 8000.
	if strings.Contains(found, "figures for") {
		t.Fatal(found)
	}

	found = strings.Join(outlineProblems(ptr(outlineOf(2, 100)), &d, 8000, paperShape(3200, 6)), "\n")
	if !strings.Contains(found, "2 sections") || !strings.Contains(found, "add up to 200") {
		t.Fatal(found)
	}
}

func ptr[T any](v T) *T { return &v }

func TestEnglishLengthsAreInWordsAndChineseInCharacters(t *testing.T) {
	paper := testPaper()
	if got := targetUnits(&paper); got != 8000 {
		t.Fatal(got)
	}
	paper.Language = "en-GB"
	if got := targetUnits(&paper); got != 1333 {
		t.Fatal(got)
	}
}

func TestTheOutlineIsSentBackUntilItHolds(t *testing.T) {
	var n atomic.Int64
	model := stepFunc(func(_ string, turns []aicompat.AgentTurn, _ []aicompat.ToolSpec) (aicompat.StepResult, error) {
		step := n.Add(1) - 1
		if text := turns[0].Text; !strings.Contains(text, "n1 - Green roofs lower surface temperature") {
			t.Errorf("the dossier isn't in %s", text)
		}
		submit := func(o Outline) aicompat.StepResult {
			raw, _ := json.Marshal(o)
			return calls(aicompat.AgentTurn{Kind: aicompat.ToolCalled, CallID: fmt.Sprintf("c%d", step), Name: submitOutlineTool, Arguments: string(raw)})
		}
		switch step {
		case 0:
			return submit(outlineOf(1, 8000)), nil
		case 1:
			if out := lastResult(t, turns); !strings.Contains(out, "Not kept") {
				t.Errorf("not sent back: %s", out)
			}
			return submit(outlineOf(9, 900)), nil
		}
		return aicompat.Reply("Outlined."), nil
	})
	paper, dossier := testPaper(), testDossier()
	made, err := MakeOutline(t.Context(), model, &paper, &dossier, ignore)
	if err != nil || len(made.Sections) != 9 {
		t.Fatal(made, err)
	}
}

func TestAnOutlineMissingAFieldIsSentBackAsUnreadable(t *testing.T) {
	var n atomic.Int64
	model := stepFunc(func(_ string, turns []aicompat.AgentTurn, _ []aicompat.ToolSpec) (aicompat.StepResult, error) {
		switch n.Add(1) {
		case 1:
			return calls(toolCall("c1", submitOutlineTool, map[string]any{"thesis": "T", "sections": []any{map[string]any{"title": "A"}}})), nil
		case 2:
			if out := lastResult(t, turns); !strings.HasPrefix(out, "The outline couldn't be read: missing field `keyPoints`") {
				t.Errorf("got %s", out)
			}
		}
		return aicompat.Reply("Done."), nil
	})
	paper, dossier := testPaper(), testDossier()
	if _, err := MakeOutline(t.Context(), model, &paper, &dossier, ignore); err == nil || !strings.Contains(err.Error(), "No outline that holds") {
		t.Fatal(err)
	}
}

func TestAnEditedOutlineDropsWhatItCantUseAndNeedsTitles(t *testing.T) {
	d := testDossier()
	edited := outlineOf(2, 500)
	edited.Sections[0].Evidence = []string{"n1", "n42"}
	edited.Sections[0].KeyPoints = []string{"Albedo", "  "}
	edited.Sections[1].Figures[0].Spec = " "
	kept, err := AsEdited(edited, d)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(kept.Sections[0].Evidence, []string{"n1"}) || !slices.Equal(kept.Sections[0].KeyPoints, []string{"Albedo"}) || len(kept.Sections[1].Figures) != 0 {
		t.Fatal(kept)
	}
	// What was passed in stays as it was.
	if len(edited.Sections[0].Evidence) != 2 {
		t.Fatal(edited)
	}

	untitled := outlineOf(1, 500)
	untitled.Sections[0].Title = " "
	if _, err := AsEdited(untitled, d); err == nil {
		t.Fatal("untitled")
	}
	if _, err := AsEdited(Outline{}, d); err == nil {
		t.Fatal("empty")
	}
}

func TestAnOutlinesShapeFollowsThePapersLength(t *testing.T) {
	short := paperShape(5000, 2)
	if short.minSections != 4 || short.maxSections != 6 || short.maxFigures != 7 || short.maxWeb != 2 {
		t.Fatal(short)
	}
	long := paperShape(10000, 0)
	if long.minSections != 10 || long.maxSections != 12 {
		t.Fatal(long)
	}
	if got := paperShape(1000, 0).maxSections; got != 3 {
		t.Fatal(got)
	}

	// Twelve sections and seven web pictures for 5,000 characters, as the
	// first real run planned, is sent back.
	crowded := outlineOf(12, 400)
	for i := range 7 {
		crowded.Sections[i].Figures = []FigurePlan{{Kind: WebPicture, Spec: "roof"}}
	}
	d := testDossier()
	found := strings.Join(outlineProblems(&crowded, &d, 5000, short), "\n")
	for _, want := range []string{"12 sections: a paper this long has 4-6", "12 figures: at most 7", "7 web pictures: at most 2"} {
		if !strings.Contains(found, want) {
			t.Errorf("no %q in %s", want, found)
		}
	}
}

func TestShorterGenresResearchLessAndHaveFewerSectionsAndFigures(t *testing.T) {
	article := shapeOf(GenreArticle.Profile().Scale, 3000, 3)
	if article.minSections != 3 || article.maxSections != 5 || article.figureEvery != 3 {
		t.Fatal(article)
	}
	thread := shapeOf(GenreThread.Profile().Scale, 1500, 6)
	if thread.minSections != 1 || thread.maxSections != 1 || thread.maxFigures != 0 || thread.maxWeb != 0 || thread.images != 0 {
		t.Fatal(thread)
	}
	if paperShape(5000, 2) != shapeOf(GenreReport.Profile().Scale, 5000, 2) {
		t.Fatal("a report isn't shaped as a paper")
	}
	if GenreBudget(GenreThread).Limits.Searches >= PaperBudget().Limits.Searches {
		t.Fatal("a thread researches as much as a paper")
	}
	for _, genre := range Genres {
		chars := genre.Profile().Chars
		if genre.Fit(0) != chars.least || genre.Fit(^uint32(0)) != chars.most {
			t.Error(genre)
		}
	}
}

func TestABriefIsShortResearchedLightlyAndWrittenStraightOn(t *testing.T) {
	brief := GenreBrief.Profile()
	if brief.Chars.most > 1500 || brief.Scale.Questions.least != 1 {
		t.Fatal(brief.Chars, brief.Scale.Questions)
	}
	if form := GenreBrief.Form(); form.Checkpoint || form.Headings {
		t.Fatal(form)
	}
	s := shapeOf(brief.Scale, 600, 6)
	if s.maxSections != 1 || s.maxFigures != 0 {
		t.Fatal(s)
	}
	eight := uint32(8000)
	if genre, chars := readChoice(`{"genre": "brief", "chars": 5000}`, &eight); genre != GenreBrief || chars != 1500 {
		t.Fatal(genre, chars)
	}
}

// toolArgs is a fragment of a streaming tool call's arguments.
func toolArgs(delta string) aicompat.ProviderEvent {
	return aicompat.ProviderEvent{Kind: aicompat.EventToolArgs, CallID: "c", Text: delta}
}

func TestTheOutlineIsToldAsItsArgumentsStream(t *testing.T) {
	var got sent
	watch := newOutlineWatch(got.send)
	start := aicompat.ProviderEvent{Kind: aicompat.EventToolStart, CallID: "c", Name: "submit"}
	watch.push(start)
	// Fragments cut anywhere: inside a key, inside a value, inside an escape.
	for _, part := range []string{
		`{"the`,
		`sis": "Green \"cools\""`,
		`, "sec`,
		`tions": [{"tit`,
		`le": "一、背`,
		`景", "targetWords": 700, "figures": [{"kind": "chart", "spec": "title"}]}`,
	} {
		watch.push(toolArgs(part))
	}
	thesis := `Green "cools"`
	sameJSON(t, got.events(), []Event{OutlineDraftEvent{Attempt: 1, Thesis: &thesis, Titles: []string{}}})
	// Held back while too soon after the last; the next try starts over.
	watch.push(start)
	watch.push(toolArgs(`{"sections": [{"title": "A"}, {"title": 3}]}`))
	time.Sleep(260 * time.Millisecond)
	watch.push(toolArgs(`[{"title": "B"}`))
	events := got.events()
	sameJSON(t, events[len(events)-1], OutlineDraftEvent{Attempt: 2, Titles: []string{"A", "B"}})
}
