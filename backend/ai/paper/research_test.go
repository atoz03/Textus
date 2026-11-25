package paper

import (
	"cmp"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"textus/backend/ai"
	"textus/backend/ai/aicompat"
	"textus/backend/ai/web"
)

// testKit is a researcher's kit with no files and only extra tools.
func testKit(extra ...ai.Tool) *Kit {
	tools := ai.SharedToolRegistry()
	for _, t := range extra {
		tools.Add(t)
	}
	return &Kit{Tools: tools, Ctx: &ai.ToolContext{}}
}

func userText(t *testing.T, turns []aicompat.AgentTurn) string {
	t.Helper()
	if len(turns) == 0 || turns[0].Kind != aicompat.User {
		t.Fatalf("no user turn in %v", turns)
	}
	return turns[0].Text
}

func noteCall(id, claim, evidence string, sources ...string) aicompat.AgentTurn {
	return toolCall(id, addNoteTool, map[string]any{"claim": claim, "evidence": evidence, "sources": sources})
}

func TestQuestionsComeFromAJSONArrayOrAList(t *testing.T) {
	if got := parseQuestions("Here:\n[\"What is it?\", \"What is it?\", \"Who uses it?\"]"); !slices.Equal(got, []string{"What is it?", "Who uses it?"}) {
		t.Fatal(got)
	}
	got := parseQuestions("1. How do green roofs cool?\n- What do the measurements show?\nok")
	if !slices.Equal(got, []string{"How do green roofs cool?", "What do the measurements show?"}) {
		t.Fatal(got)
	}
}

// scripted plans two questions, and as each question's helper keeps one
// good note, tries two bad ones, then stops.
var scripted = stepFunc(func(_ string, turns []aicompat.AgentTurn, tools []aicompat.ToolSpec) (aicompat.StepResult, error) {
	switch {
	case len(tools) == 0:
		return aicompat.Reply(`["How do green roofs cool a street?", "What do city measurements show?"]`), nil
	case len(turns) == 1:
		text := turns[0].Text
		id := func(name string) string { return fmt.Sprintf("%s-%d", name, len(text)) }
		return calls(
			toolCall(id("good"), addNoteTool, map[string]any{"claim": text, "evidence": "2 °C cooler at noon.", "sources": []string{"https://example.org/study"}, "leads": []any{map[string]any{"kind": "data", "description": "hourly table"}}}),
			noteCall(id("unknown"), "x", "y", "7"),
			noteCall(id("bare"), "x", "y"),
		), nil
	}
	return aicompat.Reply("Covered."), nil
})

func TestResearchPlansQuestionsAndKeepsEachHelpersSourcedNotes(t *testing.T) {
	budget := NewSharedBudget(PaperBudget())
	plan := web.NewPlan(true, nil, nil, nil).WithBudget(budget)
	var got sent
	paper := testPaper()
	dossier, err := Research(t.Context(), scripted, &paper, plan, budget, got.send, testKit(), NoProgress())
	if err != nil {
		t.Fatal(err)
	}
	// One note per question kept; the unknown number and the unsourced one
	// were refused.
	if len(dossier.Notes) != 2 || dossier.Notes[0].ID != "n1" || dossier.Notes[1].ID != "n2" {
		t.Fatal(dossier.Notes)
	}
	first := dossier.Notes[0]
	if first.Question != "How do green roofs cool a street?" || !strings.Contains(first.Claim, first.Question) || first.Leads[0].Kind != DataLead {
		t.Fatal(first)
	}
	// Both helpers cited the same page: one number for the whole paper.
	if !slices.Equal(first.Sources, []uint32{1}) || !slices.Equal(dossier.Notes[1].Sources, []uint32{1}) {
		t.Fatal(dossier.Notes)
	}
	if len(dossier.Sources) != 1 || dossier.Sources[0].URL != "https://example.org/study" {
		t.Fatal(dossier.Sources)
	}

	events := got.events()
	sameJSON(t, events[:3], []Event{
		PhaseEvent{Planning},
		// The questions are shown before the helpers start on them.
		QuestionsEvent{[]string{"How do green roofs cool a street?", "What do city measurements show?"}},
		PhaseEvent{Researching},
	})
	var budgets int
	var kept []ActivityEvent
	var done []QuestionDoneEvent
	for _, e := range events {
		switch e := e.(type) {
		case BudgetEvent:
			budgets++
		case ActivityEvent:
			if e.Tool == addNoteTool {
				kept = append(kept, e)
			}
		case QuestionDoneEvent:
			done = append(done, e)
		}
	}
	if budgets != 2 {
		t.Fatal(budgets)
	}
	// Each kept note is told as it is kept; the refused ones aren't.
	slices.SortFunc(kept, func(a, b ActivityEvent) int { return cmp.Compare(a.Question, b.Question) })
	if len(kept) != 2 || kept[0].Question != 0 || !strings.Contains(kept[0].Arguments, "green roofs") {
		t.Fatal(kept)
	}
	slices.SortFunc(done, func(a, b QuestionDoneEvent) int { return cmp.Compare(a.Question, b.Question) })
	if !slices.Equal(done, []QuestionDoneEvent{{Question: 0, Notes: 1}, {Question: 1, Notes: 1}}) {
		t.Fatal(done)
	}
}

func TestANotesClaimIsToldInOneLine(t *testing.T) {
	clipped := clip(strings.Repeat("城", 300))
	if runeCount(clipped) != 120 || !strings.HasSuffix(clipped, "…") {
		t.Fatal(clipped)
	}
}

func TestThePlannersStreamIsSentAsItsTailAFewTimesASecond(t *testing.T) {
	var got sent
	tail := newThinkingTail(got.send)
	tail.push("First   the\nbackground. ")
	// Straight after: held back, but gathered.
	tail.push(strings.Repeat("x", 2000))
	sameJSON(t, got.events(), []Event{ThinkingEvent{"First the background."}})
	for _, c := range [][3]string{{"abcdef", "3", "def"}, {"绿化城市", "2", "城市"}, {"ab", "5", "ab"}} {
		n := int(c[1][0] - '0')
		if got := tailOf(c[0], n); got != c[2] {
			t.Errorf("%s: %s", c[0], got)
		}
	}
}

func tailOf(text string, chars int) string { return tail(text, chars) }

func TestANotesSourcesMustBeKnownNumbersOrAddresses(t *testing.T) {
	plan := web.NewPlan(true, nil, nil, nil)
	// In order: the address numbers the source the others cite.
	for _, c := range []struct {
		cited string
		want  uint32
	}{{"https://a.example/", 1}, {"[1]", 1}, {" 1 ", 1}, {"2", 0}, {"a.example", 0}} {
		if n, ok := plan.SourceNumber(c.cited); n != c.want || ok != (c.want != 0) {
			t.Errorf("%q: %d %v", c.cited, n, ok)
		}
	}
}

func TestResearchThatKeepsNothingFailsWithAReason(t *testing.T) {
	empty := stepFunc(func(_ string, _ []aicompat.AgentTurn, tools []aicompat.ToolSpec) (aicompat.StepResult, error) {
		if len(tools) == 0 {
			return aicompat.Reply(`["First question here?", "Second question here?"]`), nil
		}
		return aicompat.Reply("Nothing found."), nil
	})
	budget := NewSharedBudget(PaperBudget())
	paper := testPaper()
	_, err := Research(t.Context(), empty, &paper, web.NewPlan(true, nil, nil, nil), budget, ignore, testKit(), NoProgress())
	if err == nil || !strings.Contains(err.Error(), "found nothing to write from") {
		t.Fatal(err)
	}
}

func TestEachQuestionGetsAnEvenShareOfTheResearch(t *testing.T) {
	s := shareOf(BudgetCounts{Searches: 40, Fetches: 60, PaperLookups: 30}, 7)
	var spent [3]int
	for range 5 {
		if refused := s.spend(web.SearchPapersTool, &spent); refused != "" {
			t.Fatal(refused)
		}
	}
	if refused := s.spend(web.SearchPapersTool, &spent); !strings.Contains(refused, "share of paper searches (5)") {
		t.Fatal(refused)
	}
	if s.spend(web.WebFetchTool, &spent) != "" || s.spend(addNoteTool, &spent) != "" {
		t.Fatal("refused")
	}
	if spent != [3]int{0, 1, 5} {
		t.Fatal(spent)
	}
}

func TestAShareIsUsedUpOnlyWhenEveryKindIs(t *testing.T) {
	s := shareOf(BudgetCounts{Searches: 4, Fetches: 4, PaperLookups: 2}, 2)
	if s.usedUp([3]int{2, 2, 0}) || !s.usedUp([3]int{2, 2, 1}) {
		t.Fatal(s)
	}
}

func TestSearchPagesAndSearchAPIsAreNotSources(t *testing.T) {
	for _, page := range []string{
		"https://html.duckduckgo.com/html/?q=green+roof",
		"https://lite.duckduckgo.com/lite/?q=green+roof",
		"https://www.google.com/search?q=green+roof",
		"https://www.bing.com/search?q=green+roof",
		"https://www.baidu.com/s?wd=屋顶绿化",
		"https://api.crossref.org/works?query.bibliographic=green+roof",
		"https://api.openalex.org/works?search=green+roof",
		"https://export.arxiv.org/api/query?search_query=all:roof",
		"https://commons.wikimedia.org/w/api.php?action=query",
	} {
		if !isSearchPage(page) {
			t.Error(page)
		}
	}
	for _, source := range []string{
		"https://www.epa.gov/heatislands/using-green-roofs-reduce-heat-islands",
		"https://doi.org/10.3390/su9081451",
		"https://en.wikipedia.org/wiki/Green_roof",
		"https://www.google.com/about/",
		"https://arxiv.org/abs/2607.15677v1",
	} {
		if isSearchPage(source) {
			t.Error(source)
		}
	}
}

func TestANoteCantRestOnASearchPageByAddressOrNumber(t *testing.T) {
	searchCiter := stepFunc(func(_ string, turns []aicompat.AgentTurn, tools []aicompat.ToolSpec) (aicompat.StepResult, error) {
		note := func(id, source string) aicompat.AgentTurn {
			return noteCall(fmt.Sprintf("%s%d", id, len(turns)), "c", "e", source)
		}
		switch {
		case len(tools) == 0:
			return aicompat.Reply(`["First question here?", "Second question here?"]`), nil
		case len(turns) == 1:
			return calls(note("a", "https://lite.duckduckgo.com/lite/?q=green+roof"), note("b", "https://www.epa.gov/heatislands/")), nil
		case len(turns) == 5:
			if !slices.ContainsFunc(turns, func(t aicompat.AgentTurn) bool {
				return t.Kind == aicompat.ToolResult && strings.Contains(t.Output, "is a search page")
			}) {
				t.Error("the search page was taken as a source")
			}
			return calls(note("c", "1")), nil
		}
		return aicompat.Reply("Covered."), nil
	})
	budget := NewSharedBudget(PaperBudget())
	plan := web.NewPlan(true, nil, nil, nil)
	// Number 1 is the search page, as a helper's earlier read left it.
	if n, ok := plan.SourceNumber("https://lite.duckduckgo.com/lite/?q=green+roof"); n != 1 || !ok {
		t.Fatal(n, ok)
	}
	paper := testPaper()
	dossier, err := Research(t.Context(), searchCiter, &paper, plan, budget, ignore, testKit(), NoProgress())
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range dossier.Notes {
		if !slices.Equal(n.Sources, []uint32{2}) {
			t.Fatal(dossier.Notes)
		}
	}
	for _, s := range dossier.Sources {
		if isSearchPage(s.URL) {
			t.Fatal(s)
		}
	}
}

func TestAPaperStopsToAskOnceItsModelCallsAreSpent(t *testing.T) {
	budget := NewSharedBudget(NewBudget(BudgetCounts{ModelSteps: 1}))
	metered := Metered{Inner: scripted, Budget: budget}
	if _, err := metered.Step(t.Context(), "", nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if !metered.Spent() {
		t.Fatal("not spent")
	}
	if _, err := metered.Step(t.Context(), "", nil, nil, nil); err == nil || err.Error() != StepsSpent {
		t.Fatal(err)
	}
}

func TestAPaperHasNoModelCallLimitEvenOneKeptWithTheOldOne(t *testing.T) {
	if PaperBudget().Limits.ModelSteps != ModelSteps {
		t.Fatal(PaperBudget())
	}
	old := PaperBudget()
	old.Limits.ModelSteps, old.Used.ModelSteps = 120, 120
	renewed := old.Renewed()
	if renewed.Limits.ModelSteps != ModelSteps || renewed.Used != old.Used {
		t.Fatal(renewed)
	}
}

// reportPaper is the paper the research tests write: a report on tides.
func reportPaper() Paper {
	return Paper{ID: "abc123", Topic: "Tides", Genre: GenreReport, Language: "en", TargetChars: 4000, Budget: PaperBudget(), Stage: StageResearching}
}

func TestTheToolsPluginsAndMCPServersOfferReachTheResearchers(t *testing.T) {
	pluginUser := stepFunc(func(_ string, turns []aicompat.AgentTurn, tools []aicompat.ToolSpec) (aicompat.StepResult, error) {
		switch {
		case len(tools) == 0:
			return aicompat.Reply(`["What do the gauges read?", "Where are they?"]`), nil
		case len(turns) == 1:
			if !offers(tools, "plugin_echo") {
				t.Errorf("not offered: %v", tools)
			}
			return calls(toolCall("p", "plugin_echo", map[string]any{"q": "gauges"})), nil
		case len(turns) == 3:
			if out := lastResult(t, turns); !strings.Contains(out, "echoed") {
				t.Error(out)
			}
			return calls(noteCall("n", "The gauges read high.", "The plugin said so.", "https://gauges.example/today")), nil
		}
		return aicompat.Reply("Covered."), nil
	})
	budget := NewSharedBudget(PaperBudget())
	plan := web.NewPlan(true, nil, nil, nil).WithBudget(budget)
	kit := testKit(ai.Tool{
		Spec:   aicompat.ToolSpec{Name: "plugin_echo", Description: "Echoes.", Parameters: map[string]any{"type": "object", "properties": map[string]any{}}},
		Access: ai.Proposes,
		Modes:  ai.EveryMode,
		Text:   func(_ *ai.ToolContext, arguments string) string { return "echoed " + arguments },
	})
	var got sent
	paper := reportPaper()
	dossier, err := Research(t.Context(), pluginUser, &paper, plan, budget, got.send, kit, NoProgress())
	if err != nil || len(dossier.Notes) != 2 {
		t.Fatal(dossier, err)
	}
	ran := 0
	for _, e := range got.events() {
		if a, ok := e.(ActivityEvent); ok && a.Tool == "plugin_echo" {
			ran++
		}
	}
	if ran != 2 {
		t.Fatal(ran)
	}
}

func TestAttachmentsAreKeptInThePapersFolderWhenTheScratchFolderIsGone(t *testing.T) {
	root, scratch := t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(scratch, "attached"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(scratch, "attached/a.md"), []byte("A"), 0o644); err != nil {
		t.Fatal(err)
	}
	kept, err := KeepAttachments(root, "abc123", scratch)
	if err != nil || kept == "" {
		t.Fatal(kept, err)
	}
	if err := os.RemoveAll(filepath.Join(scratch, "attached")); err != nil {
		t.Fatal(err)
	}
	// Going on: no conversation, the paper's own copy.
	again, err := KeepAttachments(root, "abc123", "")
	if err != nil || again != kept {
		t.Fatal(again, err)
	}
	// Only the attachments: the paper's own files stay out of the tools' sight.
	if exists(filepath.Join(again, "paper.json")) || filepath.Base(kept) != "files" {
		t.Fatal(kept)
	}
	if text, _ := os.ReadFile(filepath.Join(again, "attached/a.md")); string(text) != "A" {
		t.Fatal(string(text))
	}
	if none, err := KeepAttachments(root, "def456", ""); none != "" || err != nil {
		t.Fatal(none, err)
	}
}

func TestAnOlderPapersMaterialsBecomeAttachedFiles(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "papers/abc123")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "materials.json"), []byte(`[{"name": "notes/my notes.md", "content": "Hourly."}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := KeepAttachments(root, "abc123", ""); err != nil {
		t.Fatal(err)
	}
	if text, _ := os.ReadFile(filepath.Join(dir, "files/attached/my notes.md")); string(text) != "Hourly." {
		t.Fatal(string(text))
	}
	if exists(filepath.Join(dir, "materials.json")) {
		t.Fatal("materials.json is left")
	}
}

// halfway plans two questions; answers the first from a.example and
// fails the second unless second - a run stopped after one question, then
// going on.
func halfway(t *testing.T, second bool) Model {
	return stepFunc(func(_ string, turns []aicompat.AgentTurn, tools []aicompat.ToolSpec) (aicompat.StepResult, error) {
		if len(tools) == 0 {
			if second {
				t.Error("going on planned the research again")
			}
			return aicompat.Reply(`["Why do tides rise?", "Where are tides highest?"]`), nil
		}
		first := strings.Contains(userText(t, turns), "Why do tides rise?")
		if first && second {
			t.Error("going on asked a finished question again")
		}
		if !first && !second {
			return aicompat.StepResult{}, errors.New("stopped")
		}
		if len(turns) > 1 {
			return aicompat.Reply("Covered."), nil
		}
		if first {
			return calls(noteCall("n", "The moon pulls the sea.", "Measured.", "https://a.example/")), nil
		}
		return calls(noteCall("n", "Fundy's tides are highest.", "Measured.", "https://b.example/")), nil
	})
}

func TestResearchStoppedHalfwayGoesOnFromTheQuestionsItFinished(t *testing.T) {
	root := t.TempDir()
	id := reportPaper().ID
	run := func(second bool) (Dossier, error) {
		budget := NewSharedBudget(PaperBudget())
		plan := web.NewPlan(true, nil, nil, nil).WithBudget(budget)
		// A fresh book: something else numbered first, as another run's
		// pages would be.
		plan.AddSource("https://other.example/", "Other")
		progress := Progress{
			Kept: LoadResearch(root, id),
			Keep: func(p ResearchProgress) {
				if err := SaveResearch(root, id, p); err != nil {
					t.Error(err)
				}
			},
		}
		paper := reportPaper()
		return Research(t.Context(), halfway(t, second), &paper, plan, budget, ignore, testKit(), progress)
	}

	// The first run: one question answered, the other failed.
	first, err := run(false)
	if err != nil || len(first.Notes) != 1 {
		t.Fatal(first, err)
	}
	kept := LoadResearch(root, id)
	if len(kept.Questions) != 2 || kept.Answered[0] == nil || kept.Answered[1] != nil {
		t.Fatal(kept)
	}
	if len(kept.Sources) != 1 || kept.Sources[0].URL != "https://a.example/" {
		t.Fatal(kept.Sources)
	}

	// Going on asks only the open question, and the kept note's source
	// keeps pointing at its page under the new book's numbers.
	dossier, err := run(true)
	if err != nil {
		t.Fatal(err)
	}
	var claims []string
	for _, n := range dossier.Notes {
		claims = append(claims, n.Claim)
	}
	if !slices.Equal(claims, []string{"The moon pulls the sea.", "Fundy's tides are highest."}) || dossier.Notes[0].ID != "n1" {
		t.Fatal(dossier.Notes)
	}
	url := func(n uint32) string {
		for _, s := range dossier.Sources {
			if s.N == n {
				return s.URL
			}
		}
		return ""
	}
	if url(dossier.Notes[0].Sources[0]) != "https://a.example/" || url(dossier.Notes[1].Sources[0]) != "https://b.example/" {
		t.Fatal(dossier)
	}

	if err := ClearResearch(root, id); err != nil {
		t.Fatal(err)
	}
	if len(LoadResearch(root, id).Questions) != 0 {
		t.Fatal("research is left")
	}
}

// walk are the files under dir.
func walk(dir string) []string {
	var out []string
	_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			out = append(out, path)
		}
		return nil
	})
	return out
}

func TestResearchWithNoQuestionsLeavesNoFile(t *testing.T) {
	root := t.TempDir()
	if err := SaveResearch(root, "p1", ResearchProgress{Questions: []string{"Why?"}}); err != nil {
		t.Fatal(err)
	}
	before := walk(root)
	if err := SaveResearch(root, "p1", ResearchProgress{}); err != nil {
		t.Fatal(err)
	}
	isResearch := func(p string) bool { return strings.HasSuffix(p, "research.json") }
	if !slices.ContainsFunc(before, isResearch) || slices.ContainsFunc(walk(root), isResearch) {
		t.Fatal(before, walk(root))
	}
}

// delegator: a question's researcher hands three parts over in one step -
// the third refused - then keeps a note of its own; each part's helper
// keeps one.
func delegator(t *testing.T) Model {
	return stepFunc(func(instructions string, turns []aicompat.AgentTurn, tools []aicompat.ToolSpec) (aicompat.StepResult, error) {
		offersDelegate := offers(tools, delegateTool)
		switch {
		case len(tools) == 0:
			return aicompat.Reply(`["What do the gauges read?", "Where are the gauges?"]`), nil
		case strings.Contains(instructions, "they handed you one part"):
			if offersDelegate {
				t.Error("a part's helper hands nothing on")
			}
			if len(turns) > 1 {
				return aicompat.Reply("Covered."), nil
			}
			text := userText(t, turns)
			if !strings.HasPrefix(text, "Your question: Gauge ") {
				t.Error(text)
			}
			part := strings.TrimPrefix(strings.SplitN(text, "\n", 2)[0], "Your question: ")
			return calls(noteCall("n", part+" is answered.", "A reading.", "https://gauges.example/a")), nil
		case len(turns) == 1:
			if !offersDelegate {
				t.Errorf("not offered: %v", tools)
			}
			var handed []aicompat.AgentTurn
			for i, q := range []string{"Gauge A?", "Gauge B?", "Gauge C?"} {
				handed = append(handed, toolCall(fmt.Sprintf("d%d", i), delegateTool, map[string]any{"question": q}))
			}
			return calls(handed...), nil
		case !slices.ContainsFunc(turns, func(t aicompat.AgentTurn) bool { return t.Kind == aicompat.ToolCalled && t.Name == addNoteTool }):
			var results []string
			for _, turn := range turns {
				if turn.Kind == aicompat.ToolResult {
					results = append(results, turn.Output)
				}
			}
			if len(results) != 3 || !strings.Contains(results[0], "kept 1 note") || !strings.Contains(results[0], "Gauge A? is answered. [1]") ||
				!strings.Contains(results[1], "Gauge B? is answered.") || !strings.Contains(results[2], "handed over 2 parts already") {
				t.Errorf("%q", results)
			}
			return calls(noteCall("own", "The gauges read high.", "Both parts.", "1")), nil
		}
		return aicompat.Reply("Covered."), nil
	})
}

func TestAResearcherHandsPartsOfItsQuestionToHelpersOfItsOwn(t *testing.T) {
	budget := NewSharedBudget(PaperBudget())
	plan := web.NewPlan(true, nil, nil, nil).WithBudget(budget)
	var got sent
	paper := reportPaper()
	paper.SubHelpers = true
	dossier, err := Research(t.Context(), delegator(t), &paper, plan, budget, got.send, testKit(), NoProgress())
	if err != nil {
		t.Fatal(err)
	}
	// Per question: the two parts' notes, then the researcher's own.
	if len(dossier.Notes) != 6 || dossier.Notes[0].Question != "Gauge A?" || dossier.Notes[2].Question != "What do the gauges read?" {
		t.Fatal(dossier.Notes)
	}
	handed, counted := 0, false
	for _, e := range got.events() {
		switch e := e.(type) {
		case ActivityEvent:
			if e.Tool == delegateTool {
				handed++
			}
		case QuestionDoneEvent:
			// The parts' notes count for their question on the card.
			counted = counted || e == QuestionDoneEvent{Question: 0, Notes: 3}
		}
	}
	if handed != 4 || !counted {
		t.Fatal(handed, counted)
	}
}

func TestAPartHandedOverIsANarrowerQuestion(t *testing.T) {
	parent := "What do the gauges read?"
	if _, why := readHanded(`{"question": " "}`, parent); why == "" {
		t.Fatal("a blank question was handed over")
	}
	if _, why := readHanded(`{"question": "what do the gauges read?"}`, parent); why == "" {
		t.Fatal("the whole question was handed over")
	}
	h, why := readHanded(`{"question": "Gauge A?", "context": "B is known."}`, parent)
	if why != "" || h.context != "B is known." || !strings.Contains(brief(h), "don't look for it again: B is known.") {
		t.Fatal(h, why)
	}
}

func TestWithoutTheUsersChoiceAResearcherHandsNothingOver(t *testing.T) {
	noHanding := stepFunc(func(instructions string, turns []aicompat.AgentTurn, tools []aicompat.ToolSpec) (aicompat.StepResult, error) {
		if len(tools) == 0 {
			return aicompat.Reply(`["Why do tides rise?", "Where are tides highest?"]`), nil
		}
		if offers(tools, delegateTool) || strings.Contains(instructions, delegateTool) {
			t.Error("offered delegate_task")
		}
		if len(turns) == 1 {
			return calls(noteCall("n", "The moon pulls the sea.", "Measured.", "https://a.example/")), nil
		}
		return aicompat.Reply("Covered."), nil
	})
	budget := NewSharedBudget(PaperBudget())
	plan := web.NewPlan(true, nil, nil, nil).WithBudget(budget)
	paper := reportPaper()
	dossier, err := Research(t.Context(), noHanding, &paper, plan, budget, ignore, testKit(), NoProgress())
	if err != nil || len(dossier.Notes) != 2 {
		t.Fatal(dossier, err)
	}
}

// add_note keeps a note only as its sources said it: a quote they don't
// have is refused, a figure they don't give sent back once, and a note
// kept carries the passage it rests on.
func TestANoteIsCheckedAgainstWhatItsSourcesSaid(t *testing.T) {
	plan := web.NewPlan(true, nil, nil, nil).KeepingSeenText()
	page := plan.AddSource("https://sea.example/tides", "Tides")
	plan.Saw(page, "Tides\nThe gauge logged 1,240 tides in 2023. Spring tides were “a metre higher”.", web.Read)
	snippet := plan.AddSource("https://sky.example/moon", "Moon")
	plan.Saw(snippet, "Moon\nThe moon pulls the sea.", web.Snippet)
	unseen := plan.AddSource("https://plugin.example/gauge", "")
	d := &desk{}
	taker := &noteTaker{question: "q", plan: plan, kit: testKit(), desk: d, send: ignore, sentBack: map[string]bool{}}
	add := func(args map[string]any) string {
		return taker.keep(toolCall("n", addNoteTool, args).Call())
	}
	quoted := add(map[string]any{"claim": "Spring tides run high.", "evidence": "The gauge.", "quote": "two metres higher", "sources": []string{"1"}})
	if !strings.HasPrefix(quoted, "Not kept: the quote isn't in what [1] says") {
		t.Fatal(quoted)
	}
	madeUp := map[string]any{"claim": "The gauge logged 1,240 tides.", "evidence": "Since 2019.", "sources": []string{"1"}}
	if sentBack := add(madeUp); !strings.HasPrefix(sentBack, "Not kept: 2019 isn't in what [1] says") {
		t.Fatal(sentBack)
	}
	for _, args := range []map[string]any{
		madeUp,
		{"claim": "Spring tides run high.", "evidence": "1240 tides in 2023.", "quote": "A metre higher", "sources": []string{"1"}},
		{"claim": "The moon pulls the sea.", "evidence": "Gravity.", "sources": []string{"2"}},
		{"claim": "Gauges read 7.5 m.", "evidence": "The plugin.", "sources": []string{"3"}},
		// An address nothing showed: kept, marked as never read.
		{"claim": "Gauges read 9 m.", "evidence": "A page.", "sources": []string{"https://made-up.example/gauge"}},
	} {
		if got := add(args); got != "Kept." {
			t.Fatal(args, got)
		}
	}
	notes := d.notes
	if notes[4].Grounding != Unread || !slices.Equal(notes[0].Unmatched, []string{"2019"}) || notes[0].Grounding != ReadGrounded {
		t.Fatal(notes)
	}
	if len(notes[1].Unmatched) != 0 || deref(notes[1].Excerpt) != "The gauge logged 1,240 tides in 2023. Spring tides were “a metre higher”." {
		t.Fatal(notes[1])
	}
	if notes[2].Grounding != SnippetGrounded || notes[3].Grounding != Unchecked || notes[3].Excerpt != nil || unseen != 3 {
		t.Fatal(notes[2], notes[3], unseen)
	}
}
