package backend

import (
	"encoding/json/v2"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"textus/backend/ai"
)

func TestALoneDocumentHasNoProjectFolderButTextusFilesFindTheirs(t *testing.T) {
	novel := filepath.Join(t.TempDir(), "novel")
	for doc, want := range map[string]string{
		filepath.Join(novel, "chapter-1.md"): "",
		"":                                   "",
		filepath.Join(novel, ".textus", "library", "Lin.md"): novel,
		filepath.Join(novel, ".textus", "style.md"):          novel,
	} {
		if got, _ := resolveRoot(doc, ""); got != want {
			t.Errorf("%q: got %q, want %q", doc, got, want)
		}
	}
	// An explicit root wins over the document's folder, when it is there.
	existing := t.TempDir()
	if got, _ := resolveRoot(filepath.Join(novel, "chapter-1.md"), existing); got != existing {
		t.Errorf("got %q", got)
	}
	// A moved or unmounted folder is skipped, as if none were chosen.
	if got, _ := resolveRoot(filepath.Join(novel, ".textus", "style.md"), "/definitely/not/a/real/folder"); got != novel {
		t.Errorf("got %q", got)
	}
	if got, ok := resolveRoot(filepath.Join(novel, "chapter-1.md"), "   "); ok {
		t.Errorf("got %q", got)
	}
}

func TestTheProjectIsTheNearestFolderAboveWithATextusFolder(t *testing.T) {
	root := t.TempDir()
	novel := filepath.Join(root, "novel")
	writeText(t, filepath.Join(novel, ".textus", "agent.md"), "Novel rules.")
	doc := filepath.Join(novel, "part-1", "scenes", "one.md")
	writeText(t, doc, "# One")
	if got, _ := resolveRoot(doc, ""); got != novel {
		t.Errorf("got %q", got)
	}
	ws := loadWorkspace("", doc, "")
	if ws.RootSource != ai.RootEnclosingProject || deref(ws.ProjectInstructions) != "Novel rules." {
		t.Errorf("got %q, %v", ws.RootSource, ws.ProjectInstructions)
	}
	// A nearer .textus/ wins.
	writeText(t, filepath.Join(novel, "part-1", ".textus", "agent.md"), "Part rules.")
	if got, _ := resolveRoot(doc, ""); got != filepath.Join(novel, "part-1") {
		t.Errorf("got %q", got)
	}
	// With none anywhere above, no project folder: a lone document's own
	// folder (Downloads, Desktop) isn't opened to the agent.
	loose := filepath.Join(root, "loose", "notes.md")
	writeText(t, loose, "x")
	if got, ok := resolveRoot(loose, ""); ok {
		t.Errorf("got %q", got)
	}
	if ws := loadWorkspace("", loose, ""); ws.Root != nil || ws.RootSource != ai.RootNone {
		t.Errorf("got %v, %q", ws.Root, ws.RootSource)
	}
	// The walk up never takes a folder too broad.
	writeText(t, filepath.Join(root, "a", "b", "c.md"), "x")
	for _, dir := range []string{filepath.Join(root, "a", "b"), "/"} {
		if got, ok := enclosingProject(dir); ok {
			t.Errorf("%s: got %q", dir, got)
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		if got, ok := enclosingProject(home); ok {
			t.Errorf("home: got %q", got)
		}
	}
}

func TestLayersOverrideBuiltInsByName(t *testing.T) {
	root := t.TempDir()
	global, project := filepath.Join(root, "agent"), filepath.Join(root, "novel")
	writeText(t, filepath.Join(global, "skills", "proofread.md"), "---\nname: proofread\ndescription: Mine\n---\nCut hard.")
	writeText(t, filepath.Join(global, "skills", "theme.md"), "---\nname: theme\ndescription: Global summary\n---\nGlobal.")
	writeText(t, filepath.Join(project, ".textus", "skills", "theme.md"), "---\nname: theme\ndescription: House style\n---\nAt most 6 words.")
	doc := filepath.Join(project, "chapter.md")
	writeText(t, doc, "# One")
	ws := loadWorkspace(global, doc, "")
	get := func(name string) ai.AgentSkill {
		var found []ai.AgentSkill
		for _, s := range ws.Skills {
			if s.Name == name {
				found = append(found, s)
			}
		}
		if len(found) != 1 {
			t.Fatalf("%d skills called %s", len(found), name)
		}
		return found[0]
	}
	if theme := get("theme"); theme.Source != ai.SourceProject || !theme.OverridesBuiltin || !strings.Contains(theme.Prompt, "6 words") {
		t.Errorf("theme: %+v", theme)
	}
	if proofread := get("proofread"); proofread.Source != ai.SourceGlobal || !proofread.OverridesBuiltin {
		t.Errorf("proofread: %+v", proofread)
	}
	if get("skill").Source != ai.SourceBuiltin || len(ws.Skills) != len(ai.BuiltinSkills()) {
		t.Errorf("%d skills", len(ws.Skills))
	}
	if slices.ContainsFunc(ws.Skills, func(s ai.AgentSkill) bool { return strings.HasPrefix(s.Name, "standards") }) {
		t.Error("packs aren't skills")
	}
	// The built-ins themselves are untouched.
	if builtin, _ := ai.FindBuiltin("theme"); builtin.Source != ai.SourceBuiltin || builtin.OverridesBuiltin {
		t.Errorf("the built-in changed: %+v", builtin)
	}
}

func TestAgentsMDAndTheFilesBesideTheDocument(t *testing.T) {
	root := t.TempDir()
	for rel, text := range map[string]string{
		"AGENTS.md":      "<!-- note -->\nKeep British spelling.",
		"book/01.md":     "One",
		"book/02.md":     "Two",
		"book/.draft.md": "Hidden",
		"book/cover.png": "x",
		"模板/章节.md":       "# 标题",
		"other.md":       "Elsewhere",
	} {
		writeText(t, filepath.Join(root, rel), text)
	}
	if got := readAgentsMD(root); deref(got) != "Keep British spelling." {
		t.Errorf("got %v", got)
	}
	doc := filepath.Join(root, "book", "02.md")
	if got := nearbyFiles(root, doc); !slices.Equal(got, []string{"book/01.md", "模板/章节.md"}) {
		t.Errorf("got %v", got)
	}
	if ai.NearbySection(nil) != "" || !strings.Contains(ai.NearbySection([]string{"book/01.md"}), "- book/01.md") {
		t.Error("the section")
	}
	if got := readAgentsMD(filepath.Join(root, "book")); got != nil {
		t.Errorf("got %v", *got)
	}
}

func TestTheLibraryAndStyleAreReadFromTextusOnly(t *testing.T) {
	dir := t.TempDir()
	for rel, body := range map[string]string{
		".other/library/wu.md":   "---\nname: Wu\n---\nOther Wu.",
		".other/style.md":        "Short sentences.",
		".textus/library/lin.md": "---\nname: Lin\n---\nTextus Lin.",
		".textus/library/new.md": "---\nname: New\n---\n<!-- what to know about New -->\n",
	} {
		writeText(t, filepath.Join(dir, rel), body)
	}
	library := readLibrary(dir)
	var bodies []string
	for _, e := range library {
		bodies = append(bodies, e.Body)
	}
	if !slices.Equal(bodies, []string{"Textus Lin.", ""}) || library[0].File != ".textus/library/lin.md" {
		t.Errorf("got %+v", library)
	}
	if readStyle(dir) != nil {
		t.Error("a style outside .textus was read")
	}
	writeText(t, filepath.Join(dir, ".textus", "style.md"), "Long sentences.")
	if got := readStyle(dir); deref(got) != "Long sentences." {
		t.Errorf("got %v", got)
	}
}

func TestProjectDataKeysReadLikeThePathAndNeverCollideOnLookalikes(t *testing.T) {
	a := projectDataKey("/Users/me/my notes")
	if !strings.HasPrefix(a, "Users-me-my-notes-") || strings.ContainsAny(a, `/\ `) {
		t.Errorf("got %q", a)
	}
	if a == projectDataKey("/Users/me/my-notes") {
		t.Error("lookalikes collide")
	}
}

func TestWhatAnOlderTextusKeptInTheFolderMovesOut(t *testing.T) {
	tmp := t.TempDir()
	root, global := filepath.Join(tmp, "novel"), filepath.Join(tmp, "config", "textus")
	writeText(t, filepath.Join(projectDir(root), "memory", "sub", "likes.md"), "tea")
	memory := adoptProjectData(global, root, "memory")
	if _, inside := pathUnder(memory, global); !inside {
		t.Errorf("%s is not under %s", memory, global)
	}
	if raw, err := os.ReadFile(filepath.Join(memory, "sub", "likes.md")); err != nil || string(raw) != "tea" {
		t.Errorf("got %q, %v", raw, err)
	}
	if _, err := os.Stat(filepath.Join(projectDir(root), "memory")); err == nil {
		t.Error("the old folder is still there")
	}
	// Once there, a stale copy left in the folder isn't taken again.
	if err := os.MkdirAll(filepath.Join(projectDir(root), "memory"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := adoptProjectData(global, root, "memory"); got != memory {
		t.Errorf("got %q", got)
	}
	if _, err := os.Stat(filepath.Join(memory, "likes.md")); err == nil {
		t.Error("the stale folder was taken")
	}
}

// testMemoryDirs are a global config folder and a project folder side by
// side.
func testMemoryDirs(t *testing.T) (memoryDirs, string) {
	base := t.TempDir()
	if err := os.MkdirAll(filepath.Join(base, "novel"), 0o755); err != nil {
		t.Fatal(err)
	}
	return memoryDirs{personal: filepath.Join(base, "agent"), project: filepath.Join(base, "novel")}, base
}

func TestThePromptBudgetCountsCharactersAndKeepsTheProjectFirst(t *testing.T) {
	dirs, _ := testMemoryDirs(t)
	// 1,500 Chinese characters each: 4,500 bytes, but 1,500 against the
	// 4,000-character budget, so two fit.
	for i, scope := range []ai.Scope{ai.ScopePersonal, ai.ScopeProject, ai.ScopeProject} {
		dir, ok := dirs.folder(scope, memoryFolder)
		if !ok {
			t.Fatal(scope)
		}
		writeText(t, filepath.Join(dir, fmt.Sprintf("m%d.md", i)),
			fmt.Sprintf("---\nkind: style\nupdated: 2026-10-1%dT00:00:00Z\n---\n%d%s", i, i, strings.Repeat("字", 1_499)))
	}
	memories := dirs.load()
	if memories.Chars != 3_000 {
		t.Errorf("%d characters", memories.Chars)
	}
	var included []ai.Scope
	for _, m := range memories.Items {
		if m.Included {
			included = append(included, m.Scope)
		}
	}
	if !slices.Equal(included, []ai.Scope{ai.ScopeProject, ai.ScopeProject}) {
		t.Errorf("the project's should win the budget: %v", included)
	}
	// The project's memories live in the app's config folder, not the
	// user's.
	if dir, _ := dirs.folder(ai.ScopeProject, memoryFolder); !strings.HasPrefix(dir, dirs.personal) {
		t.Errorf("project memories in %s", dir)
	}
}

func TestMemoryIDsNeverReachOutsideTheMemoryFolders(t *testing.T) {
	dirs, base := testMemoryDirs(t)
	writeText(t, filepath.Join(base, "novel", "chapter.md"), "keep")
	for _, id := range []string{
		"project/memory/../../chapter", "project/memory/../chapter", "project/memory/a/b", "project/memory/..",
		"project/memory/.hidden", "project/memory/", "project/style/x", "personal/library/x", "other/memory/x",
		`project/memory/..\..\chapter`,
	} {
		if _, err := dirs.locate(id); err == nil {
			t.Errorf("%s was located", id)
		}
	}
	// A link in the memory folder is never followed.
	memory, _ := dirs.folder(ai.ScopeProject, memoryFolder)
	if err := os.MkdirAll(memory, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(base, "novel", "chapter.md"), filepath.Join(memory, "link.md")); err == nil {
		if _, err := dirs.locate("project/memory/link"); err == nil {
			t.Error("a link was located")
		}
	}
	if loc, err := dirs.locate("project/memory/likes"); err != nil || loc.path != filepath.Join(memory, "likes.md") {
		t.Errorf("got %+v, %v", loc, err)
	}
}

func TestMemoryTimestampsAreUTCDatesAndTimes(t *testing.T) {
	for secs, want := range map[int64]string{
		0: "1970-01-01T00:00:00Z", 951_782_400: "2000-02-29T00:00:00Z", 1_792_315_800: "2026-10-18T09:30:00Z",
	} {
		if got := memoryTimestamp(time.Unix(secs, 0)); got != want {
			t.Errorf("%d: got %s, want %s", secs, got, want)
		}
	}
}

func TestASetAsideMemoryIsForgottenAndCanBeRestored(t *testing.T) {
	dirs, _ := testMemoryDirs(t)
	dir, _ := dirs.folder(ai.ScopePersonal, memoryFolder)
	path := filepath.Join(dir, "keep-it-short.md")
	writeText(t, path, "---\nkind: preference\n---\nKeep it short.\n")
	if err := dirs.setAside(path); err != nil {
		t.Fatal(err)
	}
	loaded := dirs.load()
	if len(loaded.Items) != 0 || len(loaded.Forgotten) != 1 || loaded.Forgotten[0].Text != "Keep it short." {
		t.Fatalf("got %+v", loaded)
	}
	// Restored, it is a memory again and no longer forgotten.
	id, err := dirs.restore(loaded.Forgotten[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	loaded = dirs.load()
	if len(loaded.Items) != 1 || loaded.Items[0].ID != id || len(loaded.Forgotten) != 0 {
		t.Errorf("got %+v", loaded)
	}
	// Nothing forgotten by that id any more, and no way out of the folder.
	for _, gone := range []string{"forgotten/" + id, "forgotten/project/memory/../x"} {
		if _, err := dirs.restore(gone); err == nil {
			t.Errorf("%s was restored", gone)
		}
	}
}

// writingServer answers every request with reply, and keeps the last
// request's system instructions.
func writingServer(t *testing.T, replies ...string) (*App, *string, *int) {
	t.Helper()
	instructions, asked := new(string), new(int)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		json.UnmarshalRead(r.Body, &body)
		if len(body.Messages) > 0 {
			*instructions = body.Messages[0].Content
		}
		reply := replies[min(*asked, len(replies)-1)]
		*asked++
		out, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": reply}}}})
		w.Write(out)
	}))
	t.Cleanup(server.Close)
	a := &App{dirs: Dirs{Config: t.TempDir()}, defaults: map[string]bool{"textusAi": true}}
	a.changeEndpoints(func([]ai.CustomEndpoint) []ai.CustomEndpoint {
		return []ai.CustomEndpoint{{ID: "custom", BaseURL: server.URL + "/v1", Model: "m"}}
	})
	return a, instructions, asked
}

func TestACompletionFollowsTheProjectsStyleAndLibrary(t *testing.T) {
	a, instructions, _ := writingServer(t, "walked in.")
	project := t.TempDir()
	writeText(t, filepath.Join(project, ".textus", "style.md"), "Short sentences, no adverbs.")
	writeText(t, filepath.Join(project, ".textus", "library", "lin.md"), "---\nname: Lin\n---\nTeaches Chinese.")
	doc := filepath.Join(project, "one.md")
	args, _ := json.Marshal(map[string]any{"request": map[string]any{
		"provider": "custom", "before": "Lin ", "after": "", "style": nil, "model": nil,
		"hints": map[string]any{"block": "paragraph", "length": "short", "uiLanguage": "en"}, "docPath": doc, "workspaceRoot": nil,
	}})
	if _, err := run(a, "ai_complete", string(args)); err != nil {
		t.Fatal(err)
	}
	for _, part := range []string{"Short sentences, no adverbs.", "Teaches Chinese.", "Write the way the style profile describes."} {
		if !strings.Contains(*instructions, part) {
			t.Errorf("the instructions lack %q", part)
		}
	}
}

func TestAGrammarCheckAsksOnceMoreAfterAMalformedReply(t *testing.T) {
	a, instructions, asked := writingServer(t, "Looks fine to me!",
		`{"corrected": "She has an honest opinion.", "issues": [{"original": "a honest", "suggestion": "an honest", "issue": "vowel sound"}]}`)
	got, err := run(a, "ai_grammar_check", `{"request":{"provider":"custom","paragraph":"She has a honest opinion.","strictness":"standard","model":null,"docPath":null,"workspaceRoot":null,"uiLanguage":"zh"}}`)
	if err != nil {
		t.Fatal(err)
	}
	if want := `[{"start":8,"end":9,"issue":"vowel sound","suggestion":"an","original":"a"}]`; got != want {
		t.Errorf("got %s, want %s", got, want)
	}
	if *asked != 2 || !strings.Contains(*instructions, "one short sentence in Chinese") {
		t.Errorf("asked %d times", *asked)
	}
	// Twice malformed: the error, after the one retry.
	a, _, asked = writingServer(t, "no issues")
	if _, err := run(a, "ai_grammar_check", `{"request":{"provider":"custom","paragraph":"Fine.","strictness":null,"model":null,"docPath":null,"workspaceRoot":null,"uiLanguage":null}}`); err == nil || *asked != 2 {
		t.Errorf("asked %d times: %v", *asked, err)
	}
}
