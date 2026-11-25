package paper

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"textus/backend/ai/aicompat"
	"textus/backend/ai/web"
)

func TestMain(m *testing.M) {
	strictPrompts = true
	raw, err := os.ReadFile("../../../src/ai/ai-error-kinds.json")
	if err == nil {
		err = aicompat.LoadErrorKinds(raw)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "ai-error-kinds.json:", err)
		os.Exit(1)
	}
	// Retries wait no time in tests.
	sleep = func(ctx context.Context, _ time.Duration) error { return ctx.Err() }
	os.Exit(m.Run())
}

func testPaper() Paper {
	return Paper{
		ID:          "abc123",
		Topic:       "城市热岛效应",
		Genre:       GenrePaper,
		Language:    "zh",
		TargetChars: 8000,
		Budget:      PaperBudget(),
		Stage:       StageResearching,
	}
}

func testOutline() Outline {
	return Outline{
		Thesis: "Green roofs cool cities measurably.",
		Sections: []OutlineSection{{
			Title:       "Mechanism",
			KeyPoints:   []string{"Albedo", "Evapotranspiration"},
			Evidence:    []string{"n1"},
			TargetWords: 1200,
			Figures:     []FigurePlan{{Kind: Chart, Spec: "Surface temperature by roof type, from n1"}},
		}},
	}
}

func testDossier() Dossier {
	quote, url := "up to 30 °C cooler", "https://example.org/study"
	return Dossier{Notes: []Note{{
		ID:        "n1",
		Question:  "How much do green roofs cool?",
		Claim:     "Green roofs lower surface temperature by up to 30 °C.",
		Evidence:  "Measured on 12 roofs over two summers.",
		Quote:     &quote,
		Sources:   []uint32{3, 7},
		Leads:     []Lead{{Kind: DataLead, Description: "Table 2: temperatures per roof", URL: &url}},
		Grounding: Unchecked,
	}}}
}

// stepFunc is a Model scripted by a function of what it is asked.
type stepFunc func(instructions string, turns []aicompat.AgentTurn, tools []aicompat.ToolSpec) (aicompat.StepResult, error)

func (f stepFunc) Step(_ context.Context, instructions string, turns []aicompat.AgentTurn, tools []aicompat.ToolSpec, _ aicompat.EventSink) (aicompat.StepResult, error) {
	return f(instructions, turns, tools)
}

func (stepFunc) AtOnce() int { return AtOnce }

// toolCall is a ToolCall turn calling name with args as JSON.
func toolCall(id, name string, args any) aicompat.AgentTurn {
	raw, err := json.Marshal(args)
	if err != nil {
		panic(err)
	}
	return aicompat.AgentTurn{Kind: aicompat.ToolCalled, CallID: id, Name: name, Arguments: string(raw)}
}

func calls(turns ...aicompat.AgentTurn) aicompat.StepResult { return aicompat.StepResult{Calls: turns} }

// lastResult is the output of turns' last tool result.
func lastResult(t *testing.T, turns []aicompat.AgentTurn) string {
	t.Helper()
	if len(turns) == 0 || turns[len(turns)-1].Kind != aicompat.ToolResult {
		t.Fatalf("no result in %v", turns)
	}
	return turns[len(turns)-1].Output
}

func offers(tools []aicompat.ToolSpec, name string) bool {
	for _, t := range tools {
		if t.Name == name {
			return true
		}
	}
	return false
}

// sent gathers the events a run sends.
type sent struct {
	mu   sync.Mutex
	list []Event
}

func (s *sent) send(e Event) {
	s.mu.Lock()
	s.list = append(s.list, e)
	s.mu.Unlock()
}

func (s *sent) events() []Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Event{}, s.list...)
}

func ignore(Event) {}

// asJSON is v as deterministic JSON, for comparing values whose empty
// lists may be nil on one side.
func asJSON(t *testing.T, v any) string {
	t.Helper()
	raw, err := json.Marshal(v, json.Deterministic(true))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func sameJSON(t *testing.T, got, want any) {
	t.Helper()
	if g, w := asJSON(t, got), asJSON(t, want); g != w {
		t.Fatalf("got  %s\nwant %s", g, w)
	}
}

// value is raw read as JSON, for looking into.
func value(t *testing.T, raw []byte) any {
	t.Helper()
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

// at is v's member at path: keys for objects, ints for arrays.
func at(v any, path ...any) any {
	for _, p := range path {
		switch k := p.(type) {
		case string:
			m, _ := v.(map[string]any)
			v = m[k]
		case int:
			a, _ := v.([]any)
			if k >= len(a) {
				return nil
			}
			v = a[k]
		}
	}
	return v
}

func TestTheShapesSerializeInCamelCaseForTheFrontend(t *testing.T) {
	raw, _ := json.Marshal(testOutline())
	section := at(value(t, raw), "sections", 0)
	if got := asJSON(t, at(section, "keyPoints")); got != `["Albedo","Evapotranspiration"]` {
		t.Fatal(got)
	}
	if at(section, "targetWords") != 1200.0 || at(section, "figures", 0, "kind") != "chart" {
		t.Fatal(string(raw))
	}
	raw, _ = json.Marshal(testPaper())
	paper := value(t, raw)
	if at(paper, "targetChars") != 8000.0 || at(paper, "budget", "limits", "paperLookups") != 30.0 {
		t.Fatal(string(raw))
	}
	for event, want := range map[Event]string{
		SectionEvent{Index: 2, Status: SectionDrafted}: `{"type":"section","index":2,"status":"drafted"}`,
		PhaseEvent{Phase: AwaitingOutline}:             `{"type":"phase","phase":"awaitingOutline"}`,
		ErrorEvent{Message: "m"}:                       `{"type":"error","message":"m","section":null}`,
	} {
		if got := string(Wire(event)); got != want {
			t.Errorf("got %s, want %s", got, want)
		}
		if !jsontext.Value(Wire(event)).IsValid() {
			t.Errorf("not JSON: %s", Wire(event))
		}
	}
}

func TestANoteWithoutAQuoteStillReads(t *testing.T) {
	var note Note
	err := json.Unmarshal([]byte(`{"id": "n2", "question": "q", "claim": "c", "evidence": "e", "sources": [], "leads": []}`), &note)
	if err != nil || note.Quote != nil || note.Grounding != Unchecked {
		t.Fatal(err, note)
	}
}

func TestASavedPaperLoadsBackWhole(t *testing.T) {
	root := t.TempDir()
	outline := testOutline()
	files := Files{Paper: testPaper(), Dossier: testDossier(), Outline: &outline}
	if err := Save(root, files); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"paper.json", "dossier.json", "outline.json"} {
		if !exists(filepath.Join(root, "papers/abc123", name)) {
			t.Fatal(name)
		}
	}
	loaded, err := Load(root, "abc123")
	if err != nil {
		t.Fatal(err)
	}
	sameJSON(t, loaded, files)
}

func TestAPaperNotYetResearchedOrOutlinedLoadsEmpty(t *testing.T) {
	root := t.TempDir()
	files := Files{Paper: testPaper()}
	if err := Save(root, files); err != nil {
		t.Fatal(err)
	}
	if exists(filepath.Join(root, "papers/abc123/outline.json")) {
		t.Fatal("an outline was written")
	}
	loaded, err := Load(root, "abc123")
	if err != nil || loaded.Outline != nil || len(loaded.Dossier.Notes) != 0 {
		t.Fatal(err, loaded)
	}
	sameJSON(t, loaded.Paper, files.Paper)
}

func TestAMissingOrUnsafePaperIDIsAnError(t *testing.T) {
	root := t.TempDir()
	for _, id := range []string{"nothere", "../x", "", "A1", "a/b"} {
		if _, err := Load(root, id); err == nil {
			t.Error(id)
		}
	}
	bad := testPaper()
	bad.ID = "../../escape"
	if err := Save(root, Files{Paper: bad}); err == nil {
		t.Fatal("saved")
	}
	if exists(filepath.Join(root, "papers")) {
		t.Fatal("papers/ was made")
	}
}

func TestAPaperWithoutAProjectIsKeptInTheScratchFolder(t *testing.T) {
	scratch := t.TempDir()
	root, err := RootIn(scratch, "", "abc123")
	if err != nil || root != scratch {
		t.Fatal(root, err)
	}
}

func TestAScratchPaperMovesIntoTheProjectOnceThereIsOne(t *testing.T) {
	scratch, project := t.TempDir(), t.TempDir()
	outline := testOutline()
	files := Files{Paper: testPaper(), Dossier: testDossier(), Outline: &outline}
	if err := Save(scratch, files); err != nil {
		t.Fatal(err)
	}
	if err := SaveSection(scratch, "abc123", 0, "# One"); err != nil {
		t.Fatal(err)
	}
	root, err := RootIn(scratch, project, "abc123")
	if err != nil || root != project {
		t.Fatal(root, err)
	}
	loaded, err := Load(root, "abc123")
	if err != nil {
		t.Fatal(err)
	}
	sameJSON(t, loaded, files)
	if text, ok, err := LoadSection(root, "abc123", 0); text != "# One" || !ok || err != nil {
		t.Fatal(text, ok, err)
	}
	if exists(filepath.Join(scratch, "papers/abc123")) {
		t.Fatal("left in the scratch folder")
	}
}

func TestAPaperAlreadyInTheProjectIsNotReplacedByAScratchOne(t *testing.T) {
	scratch, project := t.TempDir(), t.TempDir()
	kept := Files{Paper: testPaper(), Dossier: testDossier()}
	if err := Save(project, kept); err != nil {
		t.Fatal(err)
	}
	kept.Paper.Topic = "Scratch"
	if err := Save(scratch, kept); err != nil {
		t.Fatal(err)
	}
	root, err := RootIn(scratch, project, "abc123")
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(root, "abc123")
	if err != nil {
		t.Fatal(err)
	}
	sameJSON(t, loaded.Paper, testPaper())
}

func TestNewIDsAreValidAndDiffer(t *testing.T) {
	a := NewID()
	time.Sleep(time.Millisecond)
	b := NewID()
	if a == b {
		t.Fatal(a)
	}
	if _, err := Dir("/p", a); err != nil {
		t.Fatal(err)
	}
}

func TestTheBudgetRefusesOnceAKindIsSpentAndLeavesTheOthers(t *testing.T) {
	budget := NewBudget(BudgetCounts{Searches: 2, Fetches: 1, Images: 1})
	if !budget.Take(Search) || !budget.Take(Search) || budget.Take(Search) || budget.Take(PaperLookup) {
		t.Fatal(budget)
	}
	if got := budget.Remaining(); got != (BudgetCounts{Fetches: 1, Images: 1}) {
		t.Fatal(got)
	}
	if budget.Used.Searches != 2 {
		t.Fatal(budget.Used)
	}
}

func TestWebPlansSharingABudgetSpendItTogether(t *testing.T) {
	shared := NewSharedBudget(NewBudget(BudgetCounts{Fetches: 3}))
	helpers := []*web.Plan{
		web.NewPlan(true, nil, nil, nil).WithBudget(shared),
		web.NewPlan(true, nil, nil, nil).WithBudget(shared),
	}
	// This computer is refused at once, and the read still counts.
	fetch := `{"url":"http://127.0.0.1:8080/"}`
	for _, plan := range []*web.Plan{helpers[0], helpers[1], helpers[0]} {
		if out := plan.Execute(web.WebFetchTool, fetch, ""); !strings.Contains(out, "private network") {
			t.Fatal(out)
		}
	}
	if out := helpers[1].Execute(web.WebFetchTool, fetch, ""); !strings.Contains(out, "That's the 3 pages") {
		t.Fatal(out)
	}
	if left := shared.Snapshot().Remaining().Fetches; left != 0 {
		t.Fatal(left)
	}
}

func TestAKeptPaperSaysHowFarItCameAndANewOutlineDropsOldSections(t *testing.T) {
	root := t.TempDir()
	outline := outlineOf(3, 100)
	files := Files{Paper: testPaper(), Dossier: testDossier(), Outline: &outline}
	files.Paper.Stage = StageDrafting
	if err := Save(root, files); err != nil {
		t.Fatal(err)
	}
	for index, text := range map[int]string{0: "## One", 2: "## Three"} {
		if err := SaveSection(root, "abc123", index, text); err != nil {
			t.Fatal(err)
		}
	}
	progress, err := Status(root, "abc123")
	if err != nil || progress.Drafted != 2 || progress.Paper.Stage != StageDrafting || len(progress.Outline.Sections) != 3 {
		t.Fatal(progress, err)
	}
	if !progress.Form.Checkpoint || !progress.Form.Headings {
		t.Fatal(progress.Form)
	}

	if err := ClearSections(root, "abc123"); err != nil {
		t.Fatal(err)
	}
	if progress, err := Status(root, "abc123"); err != nil || progress.Drafted != 0 {
		t.Fatal(progress, err)
	}
	if _, err := Load(root, "abc123"); err != nil {
		t.Fatal("the paper itself stays:", err)
	}
	// A paper kept before it had a stage reads as begun, and one kept
	// before genres as a paper.
	var old Paper
	budget, _ := json.Marshal(PaperBudget())
	err = json.Unmarshal([]byte(`{"id": "abc123", "topic": "t", "language": "en", "targetChars": 1000, "budget": `+string(budget)+`}`), &old)
	if err != nil || old.Stage != StageResearching || old.Genre != GenrePaper || old.SubHelpers {
		t.Fatal(old, err)
	}
}

func TestAnEditedOutlineIsKeptForDrafting(t *testing.T) {
	root := t.TempDir()
	outline := testOutline()
	paper := testPaper()
	paper.Stage = StageOutlined
	if err := Save(root, Files{Paper: paper, Dossier: testDossier(), Outline: &outline}); err != nil {
		t.Fatal(err)
	}
	if err := SaveSection(root, "abc123", 0, "# Mechanism"); err != nil {
		t.Fatal(err)
	}
	edited := testOutline()
	edited.Sections[0].Title = "  How it works  "
	edited.Sections[0].Evidence = []string{"n1", "n9"}
	saved, err := SaveOutline(root, "abc123", edited)
	if err != nil || saved.Sections[0].Title != "How it works" || len(saved.Sections[0].Evidence) != 1 {
		t.Fatal(saved, err)
	}
	// Sections drafted from the outline before aren't this one's.
	status, err := Status(root, "abc123")
	if err != nil || status.Drafted != 0 || status.Paper.Stage != StageDrafting {
		t.Fatal(status, err)
	}
	sameJSON(t, status.Outline, saved)
	if _, err := SaveOutline(root, "abc123", Outline{}); err == nil {
		t.Fatal("an outline with no sections was kept")
	}
}
