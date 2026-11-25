package plugins

import (
	"encoding/json/v2"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const planDoc = "---\ntitle: Plan\ntags: [a]\n---\n# The plan\n\nSee [goals](goals.md#now) and [[Roadmap|the road]].\nTagged #work and #home/kids, not #123 or `#code`.\n\n```\n# not a heading [[nor a link]]\n```\n\n## Tasks\n- [ ] Write it 📅 2026-11-01 #work\n- [x] Read it due:2026-10-01\n* [ ] Third\n"

func TestReadsWhatANoteHolds(t *testing.T) {
	m := ParseDocument("/r/plan.md", "plan.md", planDoc)
	if m.Title != "The plan" {
		t.Errorf("title %q", m.Title)
	}
	if m.Frontmatter == nil || *m.Frontmatter != "title: Plan\ntags: [a]" {
		t.Errorf("front matter %v", m.Frontmatter)
	}
	if want := []HeadingMeta{{1, "The plan", 4}, {2, "Tasks", 13}}; !slices.Equal(m.Headings, want) {
		t.Errorf("headings %v", m.Headings)
	}
	if len(m.Links) != 2 || m.Links[0].Target != "goals.md#now" || m.Links[0].Wiki ||
		m.Links[1].Target != "Roadmap" || m.Links[1].Text != "the road" || !m.Links[1].Wiki {
		t.Errorf("links %+v", m.Links)
	}
	if !slices.Equal(m.Tags, []string{"work", "home/kids"}) {
		t.Errorf("tags %q", m.Tags)
	}
	if len(m.Tasks) != 3 {
		t.Fatalf("tasks %+v", m.Tasks)
	}
	first, second := m.Tasks[0], m.Tasks[1]
	if first.Due == nil || *first.Due != "2026-11-01" || !slices.Equal(first.Tags, []string{"work"}) || first.Done {
		t.Errorf("first task %+v", first)
	}
	if !second.Done || second.Due == nil || *second.Due != "2026-10-01" {
		t.Errorf("second task %+v", second)
	}
	if m.Tasks[2].Line != 16 {
		t.Errorf("third task's line %d", m.Tasks[2].Line)
	}
	// Handed in by the window (an Android folder), read the same.
	read := OfTexts([]DocumentText{{Path: "content://t/plan.md", Rel: "plan.md", Text: planDoc}}, true)
	if len(read) != 1 {
		t.Fatalf("%d documents", len(read))
	}
	if !slices.Equal(read[0].Tags, m.Tags) || read[0].Blocks == nil || len(*read[0].Blocks) == 0 {
		t.Errorf("handed in: %+v", read[0])
	}
}

func TestCommentsAnchorsAndEscapedBarsAreReadAsObsidianReadsThem(t *testing.T) {
	text := "See [a](#intro) and [[Note\\|shown]] #real\n%% [[Hidden]] #secret\nstill hidden [[Gone]] %% [[Kept]]\n- [/] half done\n"
	m := ParseDocument("/r/a.md", "a.md", text)
	var targets [][2]string
	for _, l := range m.Links {
		targets = append(targets, [2]string{l.Target, l.Text})
	}
	if want := [][2]string{{"#intro", "a"}, {"Note", "shown"}, {"Kept", "Kept"}}; !slices.Equal(targets, want) {
		t.Errorf("links %q", targets)
	}
	if !slices.Equal(m.Tags, []string{"real"}) {
		t.Errorf("tags %q", m.Tags)
	}
	if len(m.Tasks) != 1 || !m.Tasks[0].Done {
		t.Errorf("tasks %+v", m.Tasks)
	}
}

func TestBlockIDsCarryTheirLineAndBlock(t *testing.T) {
	m := ParseDocument("/r/a.md", "a.md", "A paragraph ^para1\n\n- one\n- two\n\n^list-2\n")
	if want := []BlockIDMeta{{"para1", 0, "A paragraph"}, {"list-2", 5, "- two"}}; !slices.Equal(m.BlockIDs, want) {
		t.Errorf("block ids %+v", m.BlockIDs)
	}
}

func TestANoteWithoutAHeadingIsNamedAfterItsFile(t *testing.T) {
	m := ParseDocument("/r/a/b note.md", "a/b note.md", "Just text.")
	if m.Title != "b note" || m.Frontmatter != nil {
		t.Errorf("%q %v", m.Title, m.Frontmatter)
	}
}

func TestBlocksCarryTheirLines(t *testing.T) {
	var lines []int
	for _, b := range BlocksOf("# A\n\nPara one\n\nPara two\n") {
		lines = append(lines, b.Line)
	}
	if !slices.Equal(lines, []int{0, 2, 4}) {
		t.Errorf("lines %v", lines)
	}
}

// filesIn are the notes in dir, as the backend's walk lists them.
func filesIn(t *testing.T, dir string) []IndexFile {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []IndexFile
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".md") {
			out = append(out, IndexFile{Path: filepath.Join(dir, e.Name()), Rel: e.Name()})
		}
	}
	return out
}

func TestACallerIsSentOnlyWhatChangedSinceItAsked(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "a.md"), "# A")
	write(t, filepath.Join(dir, "b.md"), "# B")
	var index Index
	ask := func(after *uint64) MetadataAll { return index.Refresh(dir, filesIn(t, dir), false, after) }
	first := ask(nil)
	if !first.Whole || len(first.Documents) != 2 {
		t.Fatalf("first: %+v", first)
	}
	quiet := ask(&first.Generation)
	if quiet.Whole || len(quiet.Documents) != 0 || len(quiet.Gone) != 0 {
		t.Errorf("quiet: %+v", quiet)
	}
	write(t, filepath.Join(dir, "b.md"), "# B, longer now")
	if err := os.Remove(filepath.Join(dir, "a.md")); err != nil {
		t.Fatal(err)
	}
	again := ask(&quiet.Generation)
	if len(again.Documents) != 1 || !strings.HasSuffix(again.Documents[0].Path, "b.md") {
		t.Errorf("changed: %+v", again.Documents)
	}
	if len(again.Gone) != 1 || !strings.HasSuffix(again.Gone[0], "a.md") {
		t.Errorf("gone: %q", again.Gone)
	}
	// A caller that has seen nothing gets the whole index.
	if !ask(nil).Whole {
		t.Error("a new caller doesn't get the whole index")
	}
	// Other folders push this one out; indexed again, a caller still
	// holding its old generation gets the whole index, not "no change".
	held := first.Generation
	for range rootsKept {
		other := t.TempDir()
		index.Refresh(other, filesIn(t, other), false, nil)
	}
	write(t, filepath.Join(dir, "c.md"), "# C")
	back := ask(&held)
	if !back.Whole || len(back.Documents) != 2 {
		t.Errorf("back: %+v", back)
	}
}

func TestAWalkCutShortReportsNoFileItMissedAsGone(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"a.md", "b.md", "c.md"} {
		write(t, filepath.Join(dir, name), "# Note")
	}
	var index Index
	first := index.Refresh(dir, filesIn(t, dir), false, nil)
	if len(first.Documents) != 3 {
		t.Fatalf("first: %+v", first)
	}
	// The next walk stops after one file: the other two are still there.
	if err := os.Remove(filepath.Join(dir, "c.md")); err != nil {
		t.Fatal(err)
	}
	cut := index.Refresh(dir, filesIn(t, dir)[:1], true, &first.Generation)
	for _, path := range cut.Gone {
		if !strings.HasSuffix(path, "c.md") {
			t.Errorf("%s is reported gone", path)
		}
	}
	// A whole walk says what really went.
	whole := index.Refresh(dir, filesIn(t, dir), false, &first.Generation)
	if len(whole.Gone) != 1 || !strings.HasSuffix(whole.Gone[0], "c.md") {
		t.Errorf("gone: %q", whole.Gone)
	}
	if got := index.Refresh(dir, filesIn(t, dir), false, nil); len(got.Documents) != 2 {
		t.Errorf("%d documents", len(got.Documents))
	}
}

func TestFrontMatterIsReadAsTheAppDoes(t *testing.T) {
	raw, err := os.ReadFile("../../src/utils/frontmatter-cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Name string  `json:"name"`
		Text string  `json:"text"`
		YAML *string `json:"yaml"`
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		got, _ := frontMatterOf(c.Text)
		if (got == nil) != (c.YAML == nil) || got != nil && *got != *c.YAML {
			t.Errorf("%s: %v", c.Name, got)
		}
	}
}
