package paper

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"textus/backend/ai/aicompat"
)

// aChart is a chart that draws, for a section's planned chart.
const aChart = "```chart\n{\"series\": [{\"data\": [1]}]}\n```"

// english is testPaper written in English.
func english() Paper {
	p := testPaper()
	p.Language = "en"
	return p
}

// firstLine is text's first line.
func firstLine(text string) string {
	line, _, _ := strings.Cut(text, "\n")
	return line
}

// submitting is a step handing markdown over as section call id.
func submitting(id, markdown string) aicompat.StepResult {
	return calls(toolCall(id, submitSectionTool, map[string]any{"markdown": markdown}))
}

// writer writes every section, the one titled "Second" only on its second
// try; again counts the tries sent back.
type writer struct {
	t     *testing.T
	again atomic.Int64
}

func (w *writer) Step(_ context.Context, _ string, turns []aicompat.AgentTurn, _ []aicompat.ToolSpec, _ aicompat.EventSink) (aicompat.StepResult, error) {
	text := turns[0].Text
	if !strings.Contains(text, "[3](https://example.org/study)") {
		w.t.Errorf("the note's source isn't in %s", text)
	}
	second := strings.Contains(text, `"Second"`)
	switch last := turns[len(turns)-1]; {
	case last.Kind == aicompat.User:
		words := 97
		if second {
			words = 10
		}
		return submitting(fmt.Sprintf("first-%t", second), sectionText(words, aChart)), nil
	case last.Kind == aicompat.ToolResult && strings.Contains(last.Output, "Not kept"):
		w.again.Add(1)
		return submitting("again", sectionText(97, aChart)), nil
	}
	return aicompat.Reply("Written."), nil
}

func (*writer) AtOnce() int { return AtOnce }

// handing gathers what Draft hands on, as "index heading".
type handing struct {
	mu      sync.Mutex
	started []int
	handed  []string
	texts   map[int]string
}

func (h *handing) start(i int) {
	h.mu.Lock()
	h.started = append(h.started, i)
	h.mu.Unlock()
}

func (h *handing) finish(i int, markdown string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.handed = append(h.handed, fmt.Sprintf("%d %s", i, firstLine(markdown)))
	if h.texts == nil {
		h.texts = map[int]string{}
	}
	h.texts[i] = markdown
	return nil
}

// sorted are what was started and handed on, in index order: sections
// are kept as they are done, whichever finishes first.
func (h *handing) sorted() ([]int, []string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Sorted(slices.Values(h.started)), slices.Sorted(slices.Values(h.handed))
}

func TestSectionsAreHandedOnInOrderUnderTheirHeadings(t *testing.T) {
	plan := outlineOf(3, 100)
	plan.Sections[1].Title = "Second"
	paper, dossier := english(), draftingDossier()
	w := &writer{t: t}
	var h handing
	sections, err := Draft(t.Context(), w, &paper, &plan, &dossier, nil, h.start, h.finish, ignore, nil)
	if err != nil {
		t.Fatal(err)
	}
	started, handed := h.sorted()
	if want := []string{"0 ## Mechanism", "1 ## Second", "2 ## Mechanism"}; !slices.Equal(handed, want) {
		t.Fatal(handed)
	}
	var headings []string
	for _, s := range sections {
		headings = append(headings, firstLine(s))
	}
	if want := []string{"## Mechanism", "## Second", "## Mechanism"}; !slices.Equal(headings, want) {
		t.Fatal(headings)
	}
	if w.again.Load() != 1 {
		t.Fatal(w.again.Load())
	}
	if !slices.Equal(started, []int{0, 1, 2}) {
		t.Fatal(started)
	}
}

func TestASectionDraftIsKeptBesideItsPaper(t *testing.T) {
	root := t.TempDir()
	if err := SaveSection(root, "abc123", 1, "## Two\n\nText."); err != nil {
		t.Fatal(err)
	}
	if text, ok, err := LoadSection(root, "abc123", 1); text != "## Two\n\nText." || !ok || err != nil {
		t.Fatal(text, ok, err)
	}
	if text, ok, err := LoadSection(root, "abc123", 0); ok || err != nil {
		t.Fatal(text, ok, err)
	}
	if err := SaveSection(root, "../x", 0, ""); err == nil {
		t.Fatal("saved outside the papers")
	}
}

func TestADraftGoesOnFromTheSectionsAlreadyWritten(t *testing.T) {
	plan := outlineOf(3, 100)
	plan.Sections[1].Title = "Second"
	paper, dossier := english(), draftingDossier()
	var h handing
	written := map[int]string{0: "## Kept\n\nAs it was."}
	if _, err := Draft(t.Context(), &writer{t: t}, &paper, &plan, &dossier, written, h.start, h.finish, ignore, nil); err != nil {
		t.Fatal(err)
	}
	started, handed := h.sorted()
	if len(handed) != 3 || handed[0] != "0 ## Kept" {
		t.Fatal(handed)
	}
	if !slices.Equal(started, []int{1, 2}) {
		t.Fatal("the kept section is written again:", started)
	}
}

// verbose always hands over a section of words words (and its chart),
// citing its source.
func verbose(words int) Model {
	return stepFunc(func(_ string, turns []aicompat.AgentTurn, _ []aicompat.ToolSpec) (aicompat.StepResult, error) {
		return submitting(fmt.Sprintf("c%d", len(turns)), sectionText(words, aChart)), nil
	})
}

func TestASectionWhoseOnlyProblemIsItsLengthIsKeptAfterTheTries(t *testing.T) {
	plan := outlineOf(1, 100)
	paper, dossier := english(), draftingDossier()
	written, err := writeSection(t.Context(), verbose(127), &paper, &plan, 0, &dossier, nil, ignore, nil)
	if err != nil || !strings.HasPrefix(written, "## Mechanism\n\nGreen Green") {
		t.Fatal(written, err)
	}
}

func TestASectionFarTooLongIsKeptOnlyAsTheLastResort(t *testing.T) {
	plan := outlineOf(1, 100)
	paper, dossier := english(), draftingDossier()
	// Every try was sent back: the last is kept, so the paper goes on.
	kept, err := writeSection(t.Context(), verbose(300), &paper, &plan, 0, &dossier, nil, ignore, nil)
	if err != nil || !strings.HasPrefix(kept, "## Mechanism\n\n") {
		t.Fatal(kept, err)
	}
}

func TestASectionWrittenAsTheReplyIsCheckedAndKept(t *testing.T) {
	// Writes the section as its reply, never calling submit_section.
	replier := stepFunc(func(string, []aicompat.AgentTurn, []aicompat.ToolSpec) (aicompat.StepResult, error) {
		return aicompat.Reply("## Mechanism\n\n" + sectionText(97, aChart)), nil
	})
	plan := outlineOf(1, 100)
	paper, dossier := english(), draftingDossier()
	written, err := writeSection(t.Context(), replier, &paper, &plan, 0, &dossier, nil, ignore, nil)
	// Its own heading is dropped; the outline's goes on.
	if err != nil || !strings.HasPrefix(written, "## Mechanism\n\nGreen Green") {
		t.Fatal(written, err)
	}
	if n := strings.Count(written, "## Mechanism"); n != 1 {
		t.Fatal(n, written)
	}
}

func TestASectionThatNeverHoldsIsRepairedToKeep(t *testing.T) {
	text := "Cool [3](https://wrong.example/) and [7](https://other.example/), as [3] said.\n\n```chart\n{\"type\": \"bar\", \"series\": []}\n```\n\n" + aChart + "\n\nEnd."
	want := "Cool [3](https://example.org/study) and , as [3](https://example.org/study) said.\n\n" + aChart + "\n\nEnd."
	if got := repaired(text, studySource); got != want {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
}

func TestASectionThatNeverHoldsIsKeptRepairedAndThePaperGoesOn(t *testing.T) {
	// Hands its section over with a citation of no source, every try.
	miscites := stepFunc(func(_ string, turns []aicompat.AgentTurn, _ []aicompat.ToolSpec) (aicompat.StepResult, error) {
		body := sectionText(97, "Also [9](https://nowhere.example/).\n\n"+aChart)
		return submitting(fmt.Sprintf("c%d", len(turns)), body), nil
	})
	plan := outlineOf(2, 100)
	paper, dossier := english(), draftingDossier()
	var h handing
	if _, err := Draft(t.Context(), miscites, &paper, &plan, &dossier, nil, h.start, h.finish, ignore, nil); err != nil {
		t.Fatal(err)
	}
	if len(h.texts) != 2 {
		t.Fatal(h.handed)
	}
	for _, text := range h.texts {
		if strings.Contains(text, "nowhere.example") || !strings.Contains(text, "[3](https://example.org/study)") {
			t.Fatal(text)
		}
	}
}

func TestASectionIsShownAsItsWriterStreamsIt(t *testing.T) {
	var got sent
	watch := newSectionWatch(2, got.send)
	watch.push(aicompat.ProviderEvent{Kind: aicompat.EventThinking, Text: "not shown"})
	watch.push(aicompat.ProviderEvent{Kind: aicompat.EventToolStart, CallID: "c", Name: "submit"})
	watch.push(toolArgs(`{"markdown": "Green roofs`))
	// Too soon after the last: held back.
	watch.push(toolArgs(" cool"))
	sameJSON(t, got.events(), []Event{SectionTextEvent{Index: 2, Text: "Green roofs"}})

	// A long section comes whole, for the document to show.
	long := strings.Repeat("长", 5000)
	var whole sent
	watch = newSectionWatch(0, whole.send)
	watch.push(aicompat.ProviderEvent{Kind: aicompat.EventText, Text: long})
	sameJSON(t, whole.events(), []Event{SectionTextEvent{Index: 0, Text: long}})
}

// redrawing hands over a section whose diagram doesn't draw, then - told
// so - one whose diagram does; it keeps what it was told.
type redrawing struct {
	mu   sync.Mutex
	told []string
}

func (r *redrawing) Step(_ context.Context, _ string, turns []aicompat.AgentTurn, _ []aicompat.ToolSpec, _ aicompat.EventSink) (aicompat.StepResult, error) {
	var told []string
	for _, turn := range turns {
		if turn.Kind == aicompat.ToolResult {
			told = append(told, turn.Output)
		}
	}
	r.mu.Lock()
	r.told = told
	r.mu.Unlock()
	if slices.ContainsFunc(told, func(t string) bool { return strings.HasPrefix(t, "Kept.") }) {
		return aicompat.Reply("Done."), nil
	}
	diagram := "A --> B"
	if len(told) == 0 {
		diagram = "A[bad"
	}
	body := sectionText(97, fmt.Sprintf("```mermaid\nflowchart TB\n  %s\n```\n\n%s", diagram, aChart))
	return submitting(fmt.Sprintf("c%d", len(turns)), body), nil
}

func (*redrawing) AtOnce() int { return AtOnce }

func TestADiagramTheWindowCantDrawIsSentBackWithTheRenderersWords(t *testing.T) {
	plan := outlineOf(1, 100)
	paper, dossier := english(), draftingDossier()
	var mu sync.Mutex
	var drawn []int
	draw := func(_ context.Context, blocks []FigureBlock) ([]*string, bool) {
		mu.Lock()
		drawn = append(drawn, len(blocks))
		mu.Unlock()
		errors := make([]*string, len(blocks))
		for i, b := range blocks {
			if strings.Contains(b.Code, "A[bad") {
				errors[i] = ptr("Parse error on line 2")
			}
		}
		return errors, true
	}
	model := &redrawing{}
	written, err := writeSection(t.Context(), model, &paper, &plan, 0, &dossier, nil, ignore, draw)
	if err != nil || !strings.Contains(written, "A --> B") {
		t.Fatal(written, err)
	}
	if len(model.told) == 0 || !strings.Contains(model.told[0], "Diagram 1 doesn't draw (Parse error on line 2)") {
		t.Fatal(model.told)
	}
	// The diagram and the chart, each time.
	if !slices.Equal(drawn, []int{2, 2}) {
		t.Fatal(drawn)
	}
}

func TestAThreadPostTooLongForOnePostIsSentBackSoftly(t *testing.T) {
	thread := english()
	thread.Genre = GenreThread
	posts := "1/ Short [3](https://example.org/a-very-long-address-that-is-not-read)\n---\n2/ " + strings.Repeat("x", 300)
	found := longPosts(posts, &thread)
	if len(found) != 1 || !strings.Contains(found[0].Text, "post 2") || !allSoft(found) {
		t.Fatal(found)
	}
	// An English post has the whole 280 characters.
	if found := longPosts(strings.Repeat("x", 200), &thread); len(found) != 0 {
		t.Fatal(found)
	}
	chinese := testPaper()
	chinese.Genre = GenreThread
	if found := longPosts(strings.Repeat("字", 150), &chinese); len(found) != 1 {
		t.Fatal(found)
	}
	paper := testPaper()
	if found := longPosts(strings.Repeat("x", 300), &paper); len(found) != 0 {
		t.Fatal(found)
	}
}

func TestHelpersAtOnceNeverSpendMoreThanTheBudget(t *testing.T) {
	budget := NewSharedBudget(NewBudget(BudgetCounts{Searches: 10}))
	var taken atomic.Int64
	var wg sync.WaitGroup
	for range 100 {
		wg.Go(func() {
			if budget.TakeKind(Search) {
				taken.Add(1)
			}
		})
	}
	wg.Wait()
	if taken.Load() != 10 || budget.Snapshot().Used.Searches != 10 {
		t.Fatal(taken.Load(), budget.Snapshot().Used)
	}
}
