package paper

import (
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"textus/backend/ai"
	"textus/backend/ai/aicompat"
)

// diskFolders reads the folders straight from disk, every file by its
// slash path.
type diskFolders struct{}

func (diskFolders) Readable(dir string, _ bool) []string {
	var out []string
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			rel, _ := filepath.Rel(dir, path)
			out = append(out, filepath.ToSlash(rel))
		}
		return nil
	})
	return out
}

func (diskFolders) Resolve(folder, path string) (string, string, bool) {
	abs := filepath.Join(folder, filepath.FromSlash(path))
	if _, err := os.Stat(abs); err != nil {
		return "", "", false
	}
	return abs, path, true
}

func (diskFolders) Text(folder, path string) (string, bool) {
	raw, err := os.ReadFile(filepath.Join(folder, filepath.FromSlash(path)))
	return string(raw), err == nil
}

// guidance is what the user asks of their writing, in the tests.
const guidance = "Write plainly, for engineers."

// briefWriter researches, outlines, drafts, reviews and opens a brief on
// the tide logs a user attached - every stage's step, told apart by the
// tool it is offered - counting its steps. What is written is told what
// the user asks of their writing; research isn't.
func briefWriter(t *testing.T, steps *atomic.Int64) Model {
	return stepFunc(func(instructions string, turns []aicompat.AgentTurn, tools []aicompat.ToolSpec) (aicompat.StepResult, error) {
		steps.Add(1)
		first, last := turns[0], turns[len(turns)-1]
		guided := strings.Contains(instructions, guidance)
		researches := offers(tools, addNoteTool) || len(tools) == 0 && strings.HasPrefix(first.Text, "Topic: ")
		if guided == researches {
			t.Errorf("guided %t, offered %v: %s", guided, tools, first.Text)
		}
		switch {
		case offers(tools, addNoteTool):
			if last.Kind == aicompat.User {
				return calls(noteCall("n", "Tides were logged hourly.", "The logs.", "attached/my notes.md")), nil
			}
			return aicompat.Reply("Covered."), nil
		case offers(tools, submitOutlineTool):
			if last.Kind == aicompat.User {
				return calls(toolCall("o", submitOutlineTool, Outline{Thesis: "The tides were logged every hour.", Sections: []OutlineSection{{
					Title: "Logs", KeyPoints: []string{"hourly"}, Evidence: []string{"n1"}, TargetWords: 50, Figures: []FigurePlan{},
				}}})), nil
			}
			return aicompat.Reply("Outlined."), nil
		case offers(tools, submitSectionTool):
			if last.Kind == aicompat.User {
				return submitting("s", strings.Repeat("Tides ", 43)+"were logged every hour, the notes say [1](attached/my%20notes.md)."), nil
			}
			return aicompat.Reply("Written."), nil
		case offers(tools, reportIssuesTool):
			if last.Kind == aicompat.User {
				return calls(toolCall("r", reportIssuesTool, map[string]any{"issues": []any{}})), nil
			}
			return aicompat.Reply("Reviewed."), nil
		case researches:
			if !strings.Contains(first.Text, "Tides were logged hourly.") {
				t.Errorf("the attached file isn't in %s", first.Text)
			}
			return aicompat.Reply(`["How often were the tides logged?"]`), nil
		case strings.HasPrefix(first.Text, "Thesis: "):
			return aicompat.Reply(`{"title": "Hourly tides", "abstract": "The logs."}`), nil
		}
		t.Errorf("an unexpected step, offered %v: %s", tools, first.Text)
		return aicompat.Reply(""), nil
	})
}

// phases are the phases among events, in order.
func phases(events []Event) []Phase {
	var out []Phase
	for _, e := range events {
		if p, ok := e.(PhaseEvent); ok {
			out = append(out, p.Phase)
		}
	}
	return out
}

func TestAPaperIsResearchedAndOutlinedThenWrittenFromWhatItKept(t *testing.T) {
	root, conversation := t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(conversation, Attached), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(conversation, Attached, "my notes.md"), []byte("Tides were logged hourly.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var steps atomic.Int64
	var got sent
	s := &Session{
		Root: root, Agent: briefWriter(t, &steps), AtOnce: 2,
		Guidance: func(topic string) string {
			if topic != "Tides" {
				t.Errorf("guidance for %q", topic)
			}
			return guidance
		},
		Send: func(e Event) bool { got.send(e); return true },
	}
	genre, chars := GenreBrief, uint32(300)
	r := Request{
		Topic: "Tides", Genre: &genre, Language: "en", TargetChars: &chars, Conversation: conversation,
		Kit: func(folder string) *Kit {
			return &Kit{Tools: ai.SharedToolRegistry(), Ctx: &ai.ToolContext{}, Folder: folder, Files: diskFolders{}}
		},
	}
	outlined, err := Run(t.Context(), s, r)
	if err != nil {
		t.Fatal(err)
	}
	if outlined.ID == "" || outlined.Stage != StageOutlined || outlined.Genre != GenreBrief || outlined.TargetChars != 300 {
		t.Fatal(outlined)
	}
	events := got.events()
	sameJSON(t, events[:2], []Event{WebSearchEvent{Available: false}, GenreEvent{Genre: GenreBrief, TargetChars: 300, Form: GenreBrief.Form()}})
	ready, ok := events[len(events)-2].(OutlineReadyEvent)
	if !ok || ready.Outline.Thesis != "The tides were logged every hour." {
		t.Fatal(events[len(events)-2])
	}
	if p := phases(events); !slices.Equal(p[len(p)-2:], []Phase{Outlining, AwaitingOutline}) {
		t.Fatal(p)
	}
	kept, err := Load(root, outlined.ID)
	if err != nil || len(kept.Dossier.Notes) != 1 || kept.Outline == nil || kept.Dossier.Sources[0].URL != "attached/my%20notes.md" {
		t.Fatal(kept, err)
	}

	// Asked for again, it goes on from what it kept: nothing is researched
	// or outlined twice.
	before := steps.Load()
	r.ID, r.Conversation = outlined.ID, ""
	again, err := Run(t.Context(), s, r)
	if err != nil || again.Stage != StageOutlined || steps.Load() != before {
		t.Fatal(again, err, steps.Load()-before)
	}

	got = sent{}
	if err := Write(t.Context(), s, outlined.ID, false, nil); err != nil {
		t.Fatal(err)
	}
	events = got.events()
	sameJSON(t, events[0], SourcesEvent{Heading: ReferencesHeading("en", GenreBrief), Sources: []SourceEntry{{N: 1, Entry: "`my notes.md`"}}})
	if p := phases(events); !slices.Equal(p, []Phase{Drafting, Reviewing, Finishing, Done}) {
		t.Fatal(p)
	}
	var markdown string
	for _, e := range events {
		if e, ok := e.(ReadyEvent); ok {
			markdown = e.Markdown
		}
	}
	if !strings.HasPrefix(markdown, "# Hourly tides\n\nTides Tides") || !strings.Contains(markdown, "the notes say [\\[1\\]](#ref-1).") || !strings.HasSuffix(markdown, "\\[1\\] `my notes.md`") {
		t.Fatal(markdown)
	}
	done, err := Status(root, outlined.ID)
	if err != nil || done.Paper.Stage != StageDone || done.Drafted != 1 {
		t.Fatal(done, err)
	}
}

func TestAPaperWithNoOutlineIsntWritten(t *testing.T) {
	root := t.TempDir()
	p := testPaper()
	if err := Save(root, Files{Paper: p}); err != nil {
		t.Fatal(err)
	}
	var steps atomic.Int64
	s := &Session{Root: root, Agent: briefWriter(t, &steps), Guidance: func(string) string { return guidance }, Send: func(Event) bool { return true }}
	if err := Write(t.Context(), s, p.ID, false, nil); err == nil || err.Error() != "This paper has no outline yet." || steps.Load() != 0 {
		t.Fatal(err, steps.Load())
	}
}
