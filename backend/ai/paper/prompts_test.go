package paper

import (
	"encoding/json/v2"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"textus/backend/ai/aicompat"
)

func TestAFilesHeadingsBeginItsPrompts(t *testing.T) {
	found := parsePrompts("stage", "A note for editors.\n\n## one\n\nFirst {{x}}.\n\n## two\nLine\n\nand more\n\n")
	want := [][2]string{{"stage.one", "First {{x}}."}, {"stage.two", "Line\n\nand more"}}
	if !slices.Equal(found, want) {
		t.Fatal(found)
	}
}

func TestATemplateIsFilledByNameAndKeepsItsBraces(t *testing.T) {
	got := render(`{{n}} of {{what}}, as {"a": 1} - {{n}} again`, kv{"n", 3}, kv{"what", "them"})
	if got != `3 of them, as {"a": 1} - 3 again` {
		t.Fatal(got)
	}
	if render("no keys") != "no keys" || render("{{open") != "{{open" {
		t.Fatal("changed")
	}
}

// panics runs f and is what it panicked with, "" for nothing.
func panics(f func()) (said string) {
	defer func() {
		if r := recover(); r != nil {
			said, _ = r.(string)
		}
	}()
	f()
	return ""
}

func TestAKeyWithNoValueOrAValueNoKeyTakesIsAMistake(t *testing.T) {
	if said := panics(func() { render("{{missing}}") }); !strings.Contains(said, "has no value") {
		t.Fatal(said)
	}
	if said := panics(func() { render("nothing", kv{"extra", 1}) }); !strings.Contains(said, "takes its value") {
		t.Fatal(said)
	}
}

func TestAPromptTakesInAnotherByItsDottedName(t *testing.T) {
	read := map[string]string{
		"a.one":   "one, {{b.two}} and {{key}}",
		"b.two":   "two and {{c.three}}",
		"c.three": "three",
	}
	if got := takeIn(read["a.one"], read, 0); got != "one, two and three and {{key}}" {
		t.Fatal(got)
	}
}

func TestAFoldersPromptFilesAreReadInPlaceOfTheCompiledOnes(t *testing.T) {
	folder := t.TempDir()
	if err := os.MkdirAll(filepath.Join(folder, "genres"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(folder, "genres/brief.md"), []byte("## when\n\nnever"), 0o644); err != nil {
		t.Fatal(err)
	}
	texts := promptTextsFrom(folder)
	text := func(name string) string {
		i := slices.IndexFunc(texts, func(f [2]string) bool { return f[0] == name })
		return texts[i][1]
	}
	compiled, _ := promptFS.ReadFile("prompts/draft.md")
	if text("genre.brief") != "## when\n\nnever" || text("draft") != string(compiled) {
		t.Fatal(text("genre.brief"))
	}
}

func TestEveryPromptFileIsRead(t *testing.T) {
	// Taking every file in finds every prompt they name.
	if len(allPrompts()) == 0 {
		t.Fatal("no prompts")
	}
	if mark := fingerprint(); len(mark) != 12 || mark != fingerprint() {
		t.Fatal(mark)
	}
	for _, f := range promptTextsFrom("") {
		file, text := f[0], f[1]
		found := parsePrompts(file, text)
		if len(found) == 0 {
			t.Errorf("%s.md has no ## prompts", file)
		}
		for _, p := range found {
			if p[1] == "" {
				t.Errorf("%s is empty", p[0])
			}
			// A `## ` heading inside a prompt would start another: use `###`.
			key := p[0][len(file)+1:]
			if strings.IndexFunc(key, func(c rune) bool {
				return !(c < 128 && (c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9') || strings.ContainsRune("._-", c))
			}) >= 0 {
				t.Errorf("%s.md: \"## %s\" isn't a prompt's name", file, key)
			}
		}
	}
}

func TestEveryGenresProfileIsReadFromItsFile(t *testing.T) {
	for _, genre := range Genres {
		p := genre.Profile()
		if raw, _ := json.Marshal(genre); string(raw) != `"`+p.Name+`"` {
			t.Errorf("%s: %s", genre, raw)
		}
		for _, part := range []string{p.When, p.ResearchAngles, p.ResearchSources, p.Thesis, p.OutlineOrder, p.Voice, p.Cite, p.ReviewFor, p.Opening} {
			if part == "" || strings.Contains(part, "{{") {
				t.Errorf("%s: %q", genre, part)
			}
		}
	}
	// A part one genre takes from another, and a rule taken from shared.md.
	if GenreReview.Profile().Cite != GenrePaper.Profile().Cite {
		t.Fatal("a review cites as it likes")
	}
	if !strings.Contains(GenreReport.Profile().Voice, "no 此外, 综上所述 or 值得注意的是, and dates") {
		t.Fatal(GenreReport.Profile().Voice)
	}
}

func TestAPaperKeptBeforeGenresReadsAsAPaper(t *testing.T) {
	raw, _ := json.Marshal(testPaper())
	var kept map[string]any
	if err := json.Unmarshal(raw, &kept); err != nil {
		t.Fatal(err)
	}
	delete(kept, "genre")
	raw, _ = json.Marshal(kept)
	var read Paper
	if err := json.Unmarshal(raw, &read); err != nil || read.Genre != GenrePaper {
		t.Fatal(read, err)
	}
	if raw, _ := json.Marshal(GenrePaper); string(raw) != `"paper"` {
		t.Fatal(string(raw))
	}
	if err := json.Unmarshal([]byte(`"poem"`), new(Genre)); err == nil {
		t.Fatal("a poem is a genre")
	}
}

func TestTheChosenGenreIsReadFromTheReplyAndFittedToItsLengths(t *testing.T) {
	asked := func(n uint32) *uint32 { return &n }
	for _, c := range []struct {
		reply string
		asked *uint32
		genre Genre
		chars uint32
	}{
		{`{"genre": "report", "chars": 6000}`, asked(8000), GenreReport, 6000},
		{`Sure: {"genre": "report", "chars": 99000}`, asked(8000), GenreReport, 10000},
		{`{"genre": "paper"}`, asked(8000), GenrePaper, 8000},
		{"I can't tell.", asked(3000), GenrePaper, 5000},
		{`{"genre": "poem", "chars": 300}`, asked(8000), GenrePaper, 8000},
	} {
		if genre, chars := readChoice(c.reply, c.asked); genre != c.genre || chars != c.chars {
			t.Errorf("%s: %s %d", c.reply, genre, chars)
		}
	}
}

// told is a model that keeps what it was told and replies reply.
type told struct {
	mu           sync.Mutex
	instructions string
	reply        string
}

func (m *told) model() Model {
	return stepFunc(func(instructions string, _ []aicompat.AgentTurn, _ []aicompat.ToolSpec) (aicompat.StepResult, error) {
		m.mu.Lock()
		m.instructions = instructions
		m.mu.Unlock()
		return aicompat.Reply(m.reply), nil
	})
}

// line is the line of the instructions that tells of genre name.
func (m *told) line(name string) string {
	for line := range strings.SplitSeq(m.instructions, "\n") {
		if strings.HasPrefix(line, `- "`+name+`"`) {
			return line
		}
	}
	return ""
}

func TestTheChoosingStepIsToldEveryGenreAndTheLengthPicked(t *testing.T) {
	m := &told{reply: `{"genre": "report", "chars": 5000}`}
	eight := uint32(8000)
	if genre, chars := Choose(t.Context(), m.model(), "台北为什么这么热", &eight); genre != GenreReport || chars != 5000 {
		t.Fatal(genre, chars)
	}
	for _, genre := range Genres {
		if !strings.Contains(m.instructions, `"`+genre.Profile().Name+`"`) {
			t.Error(genre)
		}
	}
	if !strings.Contains(m.instructions, "about 8000 characters") {
		t.Fatal(m.instructions)
	}
}

func TestTheChoosingStepKeepsThreadsForSocialPostsAndNewsForBriefs(t *testing.T) {
	m := &told{}
	Choose(t.Context(), m.model(), "请帮我写一个 今天的新闻简报", nil)
	if !strings.Contains(m.line("thread"), "only when") || !strings.Contains(m.instructions, "A thread is only for") {
		t.Fatal(m.instructions)
	}
	for _, word := range []string{"新闻稿", "简报", "快讯"} {
		if !strings.Contains(m.line("brief"), word) || strings.Contains(m.line("thread"), word) {
			t.Error(word)
		}
	}
}

func TestTheChoosingStepSendsLiteratureReviewsAndAnalysesToTheirGenres(t *testing.T) {
	m := &told{}
	Choose(t.Context(), m.model(), "写一份储能行业调研报告", nil)
	if strings.Contains(m.line("paper"), "literature review") || !strings.Contains(m.line("review"), "文献综述") {
		t.Fatal(m.instructions)
	}
	if !strings.Contains(m.line("analysis"), "调研报告") || strings.Contains(m.line("report"), "调研报告") {
		t.Fatal(m.instructions)
	}
}
