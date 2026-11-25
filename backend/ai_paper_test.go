package backend

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"textus/backend/ai/aicompat"
	"textus/backend/ai/paper"
	"textus/backend/ai/web"
)

// paperStep is a paper.Model scripted by a function of what it is asked.
type paperStep func(instructions string, turns []aicompat.AgentTurn, tools []aicompat.ToolSpec) aicompat.StepResult

func (f paperStep) Step(_ context.Context, instructions string, turns []aicompat.AgentTurn, tools []aicompat.ToolSpec, _ aicompat.EventSink) (aicompat.StepResult, error) {
	return f(instructions, turns, tools), nil
}

func (paperStep) AtOnce() int { return paper.AtOnce }

// paperCalls is a step calling each tool named with its arguments, as
// name, arguments pairs.
func paperCalls(id string, pairs ...any) aicompat.StepResult {
	var calls []aicompat.AgentTurn
	for i := 0; i < len(pairs); i += 2 {
		raw, err := json.Marshal(pairs[i+1])
		if err != nil {
			panic(err)
		}
		calls = append(calls, aicompat.AgentTurn{Kind: aicompat.ToolCalled, CallID: fmt.Sprint(id, i), Name: pairs[i].(string), Arguments: string(raw)})
	}
	return aicompat.StepResult{Calls: calls}
}

// researched is the dossier model's research makes of paper with kit.
func researched(t *testing.T, model paper.Model, p paper.Paper, kit *paper.Kit) paper.Dossier {
	t.Helper()
	budget := paper.NewSharedBudget(paper.PaperBudget())
	plan := web.NewPlan(true, nil, nil, nil).WithBudget(budget).KeepingSeenText()
	dossier, err := paper.Research(t.Context(), model, &p, plan, budget, func(paper.Event) {}, kit, paper.NoProgress())
	if err != nil {
		t.Fatal(err)
	}
	return dossier
}

func researchingPaper(topic string, genre paper.Genre, language string, chars uint32) paper.Paper {
	return paper.Paper{
		ID: "abc123", Topic: topic, Genre: genre, Language: language, TargetChars: chars,
		Budget: paper.PaperBudget(), Stage: paper.StageResearching,
	}
}

func TestAPapersResearchersSearchTheUsersLibraryAndCiteItsFiles(t *testing.T) {
	outside := t.TempDir()
	writeText(t, filepath.Join(outside, "secret.md"), "Not for the paper.")
	root := filepath.Join(outside, "project")
	writeText(t, filepath.Join(root, "references", "Tides 2024", "a.md"), "Tidal barrages store energy at high tide.\n")
	// Searches the library, keeps a note citing the file, and tries to read
	// outside the project folder.
	librarian := paperStep(func(instructions string, turns []aicompat.AgentTurn, tools []aicompat.ToolSpec) aicompat.StepResult {
		noted := slices.ContainsFunc(turns, func(turn aicompat.AgentTurn) bool {
			return turn.Kind == aicompat.ToolCalled && turn.Name == "add_note"
		})
		switch {
		case len(tools) == 0:
			return aicompat.Reply(`["How do tidal barrages store energy?", "Second question about it?"]`)
		case len(turns) == 1:
			if !strings.Contains(instructions, "reference library") {
				t.Error(instructions)
			}
			return paperCalls("s", paper.SearchReferencesTool, map[string]string{"query": "tidal barrages"}, "read_file", map[string]string{"path": "../secret.md"})
		case !noted:
			var results []string
			for _, turn := range turns {
				if turn.Kind == aicompat.ToolResult {
					results = append(results, turn.Output)
				}
			}
			if len(results) != 2 || !strings.Contains(results[0], "references/Tides 2024/a.md:1-1") || !strings.Contains(results[1], "outside the project folder") {
				t.Error(results)
			}
			return paperCalls("n", "add_note", map[string]any{"claim": "Barrages store energy at high tide.", "evidence": "As the notes say.", "sources": []string{"references/Tides 2024/a.md"}})
		}
		return aicompat.Reply("Covered.")
	})
	a := &App{dirs: Dirs{Config: t.TempDir(), Data: t.TempDir()}}
	dossier := researched(t, librarian, researchingPaper("城市热岛效应", paper.GenrePaper, "zh", 8000), a.paperKit(root, "", false))
	if len(dossier.Notes) != 2 || !slices.Equal(dossier.Notes[0].Sources, []uint32{1}) || !slices.Equal(dossier.Notes[1].Sources, []uint32{1}) {
		t.Fatal(dossier.Notes)
	}
	if len(dossier.Sources) != 1 || dossier.Sources[0].URL != "references/Tides%202024/a.md" || dossier.Sources[0].Title != "a.md" {
		t.Fatal(dossier.Sources)
	}

	// Written and listed like any source.
	cited := fmt.Sprintf("Barrages store energy at high tide, by the notes [1](%s).", dossier.Sources[0].URL)
	listed, ok := paper.References([]string{cited}, &dossier, paper.ReferencesHeading("en", paper.GenrePaper))
	if !ok || !strings.HasSuffix(listed, "\\[1\\] a.md. `references/Tides 2024/a.md`") {
		t.Fatal(listed)
	}
	if got := paper.PlainCitations(cited); got != "Barrages store energy at high tide, by the notes [\\[1\\]](#ref-1)." {
		t.Fatal(got)
	}
}

func TestAPapersResearcherReadsAnAttachedFileWithReadFileAndCitesItsPath(t *testing.T) {
	folder := t.TempDir()
	writeText(t, filepath.Join(folder, "attached", "my notes.md"), "Tides were logged hourly.\n")
	// Plans from the attached file, reads it with read_file, and cites it by
	// its path.
	reader := paperStep(func(instructions string, turns []aicompat.AgentTurn, tools []aicompat.ToolSpec) aicompat.StepResult {
		switch {
		case len(tools) == 0:
			if len(turns) == 0 || turns[0].Kind != aicompat.User || !strings.Contains(turns[0].Text, "Tides were logged hourly.") {
				t.Error(turns)
			}
			return aicompat.Reply(`["What do the tide logs show?", "How often were the tides logged?"]`)
		case len(turns) == 1:
			if !strings.Contains(instructions, "attached/my notes.md") {
				t.Error(instructions)
			}
			return paperCalls("r", "read_file", map[string]string{"path": "attached/my notes.md"})
		case len(turns) == 3:
			if read := turns[2]; read.Kind != aicompat.ToolResult || !strings.Contains(read.Output, "Tides were logged hourly.") {
				t.Error(read)
			}
			return paperCalls("n", "add_note", map[string]any{"claim": "Tides were logged hourly.", "evidence": "The logs.", "sources": []string{"attached/my notes.md"}})
		}
		return aicompat.Reply("Covered.")
	})
	a := &App{dirs: Dirs{Config: t.TempDir(), Data: t.TempDir()}}
	dossier := researched(t, reader, researchingPaper("Tides", paper.GenreReport, "en", 4000), a.paperKit("", folder, false))
	if len(dossier.Notes) != 2 || len(dossier.Sources) != 1 {
		t.Fatal(dossier)
	}
	// The note was checked against the file it cites.
	if note := dossier.Notes[0]; note.Grounding != paper.ReadGrounded || note.Excerpt == nil || *note.Excerpt != "Tides were logged hourly." {
		t.Fatal(note)
	}
	if source := dossier.Sources[0]; source.URL != "attached/my%20notes.md" || paper.Entry(source) != "`my notes.md`" {
		t.Fatal(source, paper.Entry(source))
	}
}

// keptPaper is an outlined paper, with the note its outline cites.
func keptPaper() paper.Files {
	outlined := researchingPaper("Tides", paper.GenreReport, "en", 4000)
	outlined.Stage = paper.StageOutlined
	return paper.Files{
		Paper: outlined,
		Dossier: paper.Dossier{
			Notes:   []paper.Note{{ID: "n1", Question: "q", Claim: "Tides were logged hourly.", Evidence: "The logs.", Sources: []uint32{1}, Grounding: paper.Unchecked}},
			Sources: []aicompat.WebSource{{N: 1, URL: "attached/my%20notes.md", Title: "my notes.md"}},
		},
		Outline: &paper.Outline{Thesis: "The tides were logged.", Sections: []paper.OutlineSection{{Title: "Logs", KeyPoints: []string{"logs"}, Evidence: []string{"n1"}, TargetWords: 300}}},
	}
}

// A paper begun with no project folder is kept in the scratch folder, and
// moves to its project's data folder when its card asks for it there;
// the outline the user edits is kept there for drafting.
func TestAPaperBegunInScratchMovesToItsProjectAndKeepsTheEditedOutline(t *testing.T) {
	testScratch(t)
	a := &App{dirs: Dirs{Config: t.TempDir(), Data: t.TempDir()}}
	scratch, err := a.paperRoot("", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := paper.Save(scratch, keptPaper()); err != nil {
		t.Fatal(err)
	}
	if err := paper.SaveSection(scratch, "abc123", 0, "Tides were logged hourly [1](attached/my%20notes.md)."); err != nil {
		t.Fatal(err)
	}
	dir := project(t, map[string]string{"notes.md": "Tides.\n"})
	args := func(rest string) string {
		return fmt.Sprintf(`{"docPath":%q,"workspaceRoot":null,"id":"abc123"%s}`, filepath.Join(dir, "notes.md"), rest)
	}
	// A lone document's folder is no project: the paper stays where it is.
	if _, err := run(a, "ai_paper_status", args("")); err != nil {
		t.Fatal(err)
	}
	if left, _ := paper.Dir(scratch, "abc123"); !exists(left) {
		t.Fatal("moved out of the scratch folder")
	}
	writeText(t, filepath.Join(dir, ".textus", "x"), "")
	got, err := run(a, "ai_paper_status", args(""))
	if err != nil {
		t.Fatal(err)
	}
	var progress paper.PaperProgress
	if err := json.Unmarshal([]byte(got), &progress); err != nil {
		t.Fatal(err)
	}
	if progress.Paper.Stage != paper.StageOutlined || progress.Outline == nil || progress.Drafted != 1 || !progress.Form.Checkpoint {
		t.Fatal(got)
	}
	kept := projectDataDir(a.globalAgentPath(), dir)
	if _, err := paper.Load(kept, "abc123"); err != nil {
		t.Fatal(err)
	}
	if left, _ := paper.Dir(scratch, "abc123"); exists(left) {
		t.Fatal("still in the scratch folder")
	}

	// Notes the dossier doesn't have and empty points are dropped, and the
	// sections drafted from the outline before it go.
	edited := `,"outline":{"thesis":"The tides were logged.","sections":[{"title":" Hourly logs ","keyPoints":["logs",""],"evidence":["n1","n9"],"targetWords":300,"figures":[]}]}`
	if got, err := run(a, "ai_paper_save_outline", args(edited)); err != nil || !strings.Contains(got, `"title":"Hourly logs","keyPoints":["logs"],"evidence":["n1"]`) {
		t.Fatal(got, err)
	}
	files, err := paper.Load(kept, "abc123")
	if err != nil || files.Paper.Stage != paper.StageDrafting || files.Outline.Sections[0].Title != "Hourly logs" {
		t.Fatal(files, err)
	}
	if _, ok, _ := paper.LoadSection(kept, "abc123", 0); ok {
		t.Fatal("the old outline's section is still kept")
	}
	if _, err := run(a, "ai_paper_save_outline", args(`,"outline":{"thesis":"x","sections":[]}`)); err == nil || err.Error() != "The outline needs at least one section." {
		t.Fatal(err)
	}
}

// What the window made of the figures it was sent reaches the writer that
// sent them.
func TestTheWindowsAnswerReachesTheFiguresItWasSent(t *testing.T) {
	a := &App{dirs: Dirs{Config: t.TempDir(), Data: t.TempDir()}}
	draw := paper.ByWindow(func(e paper.Event) bool {
		sent, ok := e.(paper.DrawFiguresEvent)
		if !ok || len(sent.Blocks) != 2 {
			t.Error(e)
			return false
		}
		if _, err := run(a, "ai_paper_figures_drawn", fmt.Sprintf(`{"callId":%q,"errors":[null,"Parse error on line 2"]}`, sent.CallID)); err != nil {
			t.Error(err)
		}
		return true
	})
	errors, ok := draw(t.Context(), []paper.FigureBlock{{Language: "mermaid", Code: "flowchart LR\n  A --> B"}, {Language: "chart", Code: "{}"}})
	if !ok || len(errors) != 2 || errors[0] != nil || errors[1] == nil || *errors[1] != "Parse error on line 2" {
		t.Fatal(errors, ok)
	}
}

// A paper is refused while Textus AI is off, and for a provider Textus
// doesn't know.
func TestAPaperNeedsTextusAIOnAndAKnownProvider(t *testing.T) {
	testScratch(t)
	off := &App{dirs: Dirs{Config: t.TempDir(), Data: t.TempDir()}, defaults: map[string]bool{"textusAi": false}}
	request := `{"request":{"provider":"openai","model":null,"requestId":"r1","topic":"Tides","language":"en","subHelpers":false}}`
	if _, err := run(off, "ai_paper_run", request); err == nil || err.Error() != textusAIOff {
		t.Fatal(err)
	}
	on := &App{dirs: Dirs{Config: t.TempDir(), Data: t.TempDir()}, defaults: map[string]bool{"textusAi": true}}
	unknown := strings.Replace(request, `"openai"`, `"nobody"`, 1)
	if _, err := run(on, "ai_paper_run", unknown); err == nil || err.Error() != "unknown provider: nobody" {
		t.Fatal(err)
	}
	draft := `{"request":{"provider":"nobody","model":null,"requestId":"r2","id":"abc123","checkClaims":false}}`
	if _, err := run(on, "ai_paper_draft", draft); err == nil || err.Error() != "unknown provider: nobody" {
		t.Fatal(err)
	}
}
