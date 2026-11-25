package backend

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"textus/backend/ai"
)

// testScratch keeps the conversations' scratch folders in a test folder,
// and the Trash out of it.
func testScratch(t *testing.T) {
	t.Helper()
	base, trash := scratchBase, tryTrash
	dir := filepath.Join(t.TempDir(), "textus-agent")
	scratchBase = func() (string, error) { return dir, privateDir(dir) }
	tryTrash = func(string) bool { return false }
	t.Cleanup(func() { scratchBase, tryTrash = base, trash })
}

// changesFolder is a folder with a.md and open.md, an app whose data is
// its own, and a conversation.
func changesFolder(t *testing.T) (string, *App, string) {
	t.Helper()
	testScratch(t)
	dir := project(t, map[string]string{"a.md": "One two three.\n", "open.md": "Open.\n", ".textus/x": ""})
	a := &App{dirs: Dirs{Config: t.TempDir(), Data: t.TempDir()}}
	return dir, a, "test-" + changeID()
}

// changeOf is the change a call recorded.
func changeOf(t *testing.T, recorded *string) string {
	t.Helper()
	if recorded == nil {
		t.Fatal("nothing recorded")
	}
	args, _ := ai.ParseArgs(*recorded)
	return args.StrOr("change")
}

func TestChangesLandOnDiskInsideTheFolderAndUndoTogether(t *testing.T) {
	dir, a, conversation := changesFolder(t)
	open := filepath.Join(dir, "open.md")
	edits := a.newFileEdits(dir, conversation, open, false)
	file := filepath.Join(dir, "a.md")
	_, edit := edits.run(`{"path":"a.md","old":"two","new":"2"}`, editFileChange)
	if readText(t, file) != "One 2 three.\n" {
		t.Errorf("got %q", readText(t, file))
	}
	// A second change to the same file builds on the first.
	_, again := edits.run(`{"path":"a.md","old":"three","new":"3"}`, editFileChange)
	if readText(t, file) != "One 2 3.\n" {
		t.Errorf("got %q", readText(t, file))
	}
	_, made := edits.run(`{"path":"new/b.md","content":"B"}`, writeFileChange)
	if readText(t, filepath.Join(dir, "new", "b.md")) != "B" {
		t.Error("the new file is not there")
	}
	// Refused: the open document, hidden folders, outside the folder, a
	// file that isn't text, a missing passage.
	for args, why := range map[string]string{
		`{"path":"open.md","content":"x"}`:          "propose_edit",
		`{"path":".textus/agent.md","content":"x"}`: "start with",
		`{"path":"../out.md","content":"x"}`:        "without `..`",
		`{"path":"pic.png","content":"x"}`:          "isn't a text file",
	} {
		if text, recorded := edits.run(args, writeFileChange); !strings.Contains(text, why) || recorded != nil {
			t.Errorf("%s: %s", args, text)
		}
	}
	if text, _ := edits.run(`{"path":"a.md","old":"four","new":"4"}`, editFileChange); !strings.Contains(text, "isn't in the file") {
		t.Errorf("got %s", text)
	}
	if readText(t, open) != "Open.\n" {
		t.Error("the open document changed")
	}
	// The reply's changes undo together, the file made set aside, and
	// redo.
	ids := []string{changeOf(t, edit), changeOf(t, again), changeOf(t, made)}
	changes, problems, err := a.setFileChanges(conversation, ids, false)
	if err != nil || len(problems) != 0 {
		t.Fatalf("%v, %v", problems, err)
	}
	for _, c := range changes {
		if c.Applied {
			t.Errorf("%s is still applied", c.ID)
		}
	}
	if readText(t, file) != "One two three.\n" || exists(filepath.Join(dir, "new", "b.md")) {
		t.Errorf("not undone: %q", readText(t, file))
	}
	if _, problems, err := a.setFileChanges(conversation, ids, true); err != nil || len(problems) != 0 {
		t.Fatalf("%v, %v", problems, err)
	}
	if readText(t, file) != "One 2 3.\n" || readText(t, filepath.Join(dir, "new", "b.md")) != "B" {
		t.Errorf("not redone: %q", readText(t, file))
	}
	got, err := run(a, "ai_file_changes", `{"conversation":"`+conversation+`","ids":["`+ids[0]+`","nope"]}`)
	if err != nil || !strings.Contains(got, `"before":"One two three.\n"`) || strings.Count(got, `"id"`) != 1 {
		t.Errorf("got %s, %v", got, err)
	}
}

func TestAskedFirstAChangeWaitsAndAnEditedFileIsLeftAlone(t *testing.T) {
	dir, a, conversation := changesFolder(t)
	edits := a.newFileEdits(dir, conversation, "", true)
	file := filepath.Join(dir, "a.md")
	text, recorded := edits.run(`{"path":"a.md","old":"two","new":"2"}`, editFileChange)
	if !strings.Contains(text, "as a revision") || readText(t, file) != "One two three.\n" {
		t.Errorf("got %s", text)
	}
	// A file it makes is made, marked as its own until kept.
	if made, _ := edits.run(`{"path":"c.md","content":"New.\n"}`, writeFileChange); !strings.Contains(made, "until the user keeps or discards it") || readText(t, filepath.Join(dir, "c.md")) != "New.\n" {
		t.Errorf("got %s", made)
	}
	ids := []string{changeOf(t, recorded)}
	changes, _, err := a.setFileChanges(conversation, ids, true)
	if err != nil || !changes[0].Applied || readText(t, file) != "One 2 three.\n" {
		t.Fatalf("got %+v, %v", changes, err)
	}
	// Changed by hand since: undo leaves it.
	writeText(t, file, "Mine.\n")
	changes, problems, err := a.setFileChanges(conversation, ids, false)
	if err != nil || !changes[0].Applied || len(problems) != 1 || readText(t, file) != "Mine.\n" {
		t.Errorf("got %+v, %v, %v", changes, problems, err)
	}
}

func TestAWindowsFileIsEditedAsReadAndKeepsItsLineEndings(t *testing.T) {
	dir, a, conversation := changesFolder(t)
	edits := a.newFileEdits(dir, conversation, filepath.Join(dir, "open.md"), false)
	file := filepath.Join(dir, "w.md")
	writeText(t, file, "First line.\r\nSecond line.\r\n")
	edits.run(`{"path":"w.md","old":"First line.\nSecond","new":"One.\nTwo"}`, editFileChange)
	if got := readText(t, file); got != "One.\r\nTwo line.\r\n" {
		t.Errorf("got %q", got)
	}
	edits.run(`{"path":"w.md","content":"A\nB\n"}`, writeFileChange)
	if got := readText(t, file); got != "A\r\nB\r\n" {
		t.Errorf("got %q", got)
	}
}

func TestALinkCannotLeadAChangeOutOfTheFolder(t *testing.T) {
	dir, a, conversation := changesFolder(t)
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(dir, "out")); err != nil {
		t.Skip(err)
	}
	if err := os.Symlink(filepath.Join(dir, ".textus"), filepath.Join(dir, "in")); err != nil {
		t.Fatal(err)
	}
	edits := a.newFileEdits(dir, conversation, "", false)
	if text, recorded := edits.run(`{"path":"out/x.md","content":"x"}`, writeFileChange); recorded != nil || !strings.Contains(text, "outside the folder") {
		t.Errorf("got %s", text)
	}
	if text, recorded := edits.run(`{"path":"in/x.md","content":"x"}`, writeFileChange); recorded != nil || !strings.Contains(text, "start with") {
		t.Errorf("got %s", text)
	}
}

func TestARevisionKeptBeforeIsBuiltOnOrWaitedFor(t *testing.T) {
	dir, a, conversation := changesFolder(t)
	file := filepath.Join(dir, "a.md")
	kept, _ := keptFile(file+changesSuffix, "", a.dirs.Data)
	writeText(t, kept, `{"incoming":[{"after":"One 2 three.\n"},{"after":"clash","conflict":true}]}`)
	// Asked first, a change builds on the revision waiting.
	asked := a.newFileEdits(dir, conversation, "", true)
	if _, recorded := asked.run(`{"path":"a.md","old":"three","new":"3"}`, editFileChange); recorded == nil {
		t.Fatal("not made")
	}
	if changes, _, _ := a.setFileChanges(conversation, nil, true); len(changes) != 0 {
		t.Errorf("got %v", changes)
	}
	if got := asked.made[0].After; got != "One 2 3.\n" {
		t.Errorf("got %q", got)
	}
	// Written straight in, it waits for the user.
	straight := a.newFileEdits(dir, conversation, "", false)
	if text, recorded := straight.run(`{"path":"a.md","old":"One","new":"1"}`, editFileChange); recorded != nil || !strings.Contains(text, "still waits") {
		t.Errorf("got %s", text)
	}
	note := waitingNote(a.dirs.Data, file)
	if !strings.Contains(note, "With your revision it reads:\nOne 2 three.") || !strings.Contains(note, "met other edits") {
		t.Errorf("got %q", note)
	}
}
