package backend

import (
	"encoding/json/v2"
	"os"
	"path/filepath"
	"testing"
)

func TestANameWithSlashesMakesTheFoldersOnTheWay(t *testing.T) {
	dir := t.TempDir()
	doc, err := createDocument(dir, "2026/March/plan")
	if err != nil {
		t.Fatal(err)
	}
	if doc.Path != filepath.Join(dir, "2026", "March", "plan.md") {
		t.Errorf("path %s", doc.Path)
	}
	// The topmost folder made is what an undo takes away.
	if doc.Made != filepath.Join(dir, "2026") {
		t.Errorf("made %s", doc.Made)
	}
	// An existing folder on the way is used, not refused.
	sub, err := createFolder(dir, "2026/April")
	if err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(sub.Path); err != nil || !info.IsDir() || sub.Made != sub.Path {
		t.Errorf("folder %+v: %v", sub, err)
	}
	for _, bad := range []string{"../out", "a/../../out", "/abs", "a//b", "a/", " / "} {
		if _, err := createDocument(dir, bad); err == nil {
			t.Errorf("%q made", bad)
		}
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(dir), "out.md")); err == nil {
		t.Error("a name walked out of the folder")
	}
}

func TestAnEntryThatCantBeMadeLeavesNoFolderMadeForIt(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "kept"), 0o755); err != nil {
		t.Fatal(err)
	}
	taken := func(string) error { return os.ErrExist }
	_, err := createEntry(dir, "kept/new/deep/plan", func(n string) string { return n }, taken)
	if !hasCode(err, "exists") {
		t.Errorf("err %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "kept", "new")); err == nil {
		t.Error("a folder made for it was left")
	}
	if info, err := os.Stat(filepath.Join(dir, "kept")); err != nil || !info.IsDir() {
		t.Error("a folder that was there went")
	}
}

func TestAMoveNeverLandsInItselfOrInPlace(t *testing.T) {
	a := filepath.FromSlash("/w/a")
	at := func(p string) string { return filepath.FromSlash(p) }
	name := func(n string) *string { return &n }
	if to, err := moveTarget(a, at("/w/b"), nil); err != nil || to != at("/w/b/a") {
		t.Errorf("into b: %s %v", to, err)
	}
	for _, into := range []string{"/w", "/w/a", "/w/a/sub"} {
		if _, err := moveTarget(a, at(into), nil); err == nil {
			t.Errorf("into %s allowed", into)
		}
	}
	// "/w/ab" is not inside "/w/a".
	if _, err := moveTarget(a, at("/w/ab"), nil); err != nil {
		t.Error(err)
	}
	// Named, it may stay in its folder: a rename taken back.
	if to, err := moveTarget(a, at("/w"), name("b")); err != nil || to != at("/w/b") {
		t.Errorf("named b: %s %v", to, err)
	}
	if _, err := moveTarget(a, at("/w"), name("a")); err == nil {
		t.Error("onto itself allowed")
	}
	if _, err := moveTarget(a, at("/w/b"), name("../x")); err == nil {
		t.Error("a name walking out allowed")
	}
}

func TestEntriesAreMadeMovedAndKeptBoth(t *testing.T) {
	root := t.TempDir()
	doc, err := createDocument(root, "Notes")
	if err != nil || filepath.Base(doc.Path) != "Notes.md" {
		t.Fatalf("%+v %v", doc, err)
	}
	if _, err := createDocument(root, "Notes"); err == nil {
		t.Error("made over another")
	}
	sub, err := createFolder(root, "Sub")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := createFolder(root, "Sub"); err == nil {
		t.Error("made a folder over another")
	}
	moved, err := moveInto(doc.Path, sub.Path, nil, false)
	if err != nil || moved != filepath.Join(sub.Path, "Notes.md") {
		t.Fatalf("moved %s %v", moved, err)
	}
	// A second Notes.md at the top can't be moved onto the first...
	again, err := createDocument(root, "Notes")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := moveInto(again.Path, sub.Path, nil, false); !hasCode(err, "exists") {
		t.Errorf("moved onto another: %v", err)
	}
	// ...kept both, it is numbered beside it.
	both, err := moveInto(again.Path, sub.Path, nil, true)
	if err != nil || both != filepath.Join(sub.Path, "Notes 2.md") {
		t.Errorf("kept both %s %v", both, err)
	}
	if _, err := moveInto(sub.Path, sub.Path, nil, false); err == nil {
		t.Error("moved into itself")
	}
}

func TestTheFolderTheTreeShowsNeverGoesToTheTrash(t *testing.T) {
	// A Trash of the test's own is read, never the user's.
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	root := t.TempDir()
	shown := trashEntries([]string{root}, root)
	if len(shown.Places) != 0 || shown.Error == nil {
		t.Errorf("%+v", shown)
	}
	// One that isn't there stops the rest before anything goes.
	missing := trashEntries([]string{filepath.Join(root, "gone.md"), root}, root)
	if len(missing.Places) != 0 || missing.Error == nil {
		t.Errorf("%+v", missing)
	}
}

func scratchCopies(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if real, err := filepath.EvalSymlinks(root); err == nil {
		root = real
	}
	return root
}

func TestACopyIsNamedAsNamingSays(t *testing.T) {
	root := scratchCopies(t)
	downloads, notes := filepath.Join(root, "downloads"), filepath.Join(root, "notes")
	for _, d := range []string{filepath.Join(downloads, "my.photos"), notes} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(p, s string) {
		if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(downloads, "report.pdf"), "new")
	write(filepath.Join(notes, "report.pdf"), "old")
	nothing := func(string) bool { return false }
	numbered := copyNaming{}
	expect := func(from, into string, naming copyNaming, want string) {
		t.Helper()
		got, err := copyEntry(from, into, naming, nothing)
		if err != nil || got != want {
			t.Errorf("copy of %s: %q %v, want %q", filepath.Base(from), got, err, want)
		}
	}
	report := filepath.Join(downloads, "report.pdf")
	expect(report, notes, numbered, "report 2.pdf")
	expect(report, notes, numbered, "report 3.pdf")
	if raw, _ := os.ReadFile(filepath.Join(notes, "report.pdf")); string(raw) != "old" {
		t.Error("copied over the one there")
	}
	// The same bytes are copied again too: a copy is what was asked for.
	expect(filepath.Join(notes, "report.pdf"), notes, numbered, "report 4.pdf")
	// A folder after its whole name, a dot in it or not.
	folder := filepath.Join(downloads, "my.photos")
	expect(folder, notes, numbered, "my.photos")
	expect(folder, notes, numbered, "my.photos 2")

	pattern := func(p string) copyNaming { return copyNaming{beside: &p} }
	plan := filepath.Join(notes, "plan.md")
	write(plan, "p")
	expect(plan, notes, pattern("{name} copy"), "plan copy.md")
	expect(plan, notes, pattern("{name} copy"), "plan copy 2.md")
	expect(plan, notes, pattern("{name} 副本"), "plan 副本.md")
	expect(folder, downloads, pattern("{name} copy"), "my.photos copy")
	// Its own name refused when taken, never numbered or over it.
	other := filepath.Join(root, "other")
	if err := os.Mkdir(other, 0o755); err != nil {
		t.Fatal(err)
	}
	own := copyNaming{own: true}
	expect(plan, other, own, "plan.md")
	if _, err := copyEntry(plan, other, own, nothing); !hasCode(err, "exists") {
		t.Errorf("own name over another: %v", err)
	}
}

func TestAFolderIsNeverCopiedIntoItself(t *testing.T) {
	root := scratchCopies(t)
	book := filepath.Join(root, "book")
	if err := os.MkdirAll(filepath.Join(book, "chapters"), 0o755); err != nil {
		t.Fatal(err)
	}
	nothing := func(string) bool { return false }
	for _, into := range []string{book, filepath.Join(book, "chapters")} {
		if _, err := copyEntry(book, into, copyNaming{}, nothing); !hasCode(err, "copyIntoItself") {
			t.Errorf("into %s: %v", into, err)
		}
	}
	// Into its own folder, beside itself: a numbered copy.
	if got, err := copyEntry(book, root, copyNaming{}, nothing); err != nil || got != "book 2" {
		t.Errorf("beside itself: %q %v", got, err)
	}
}

func TestCopyNamingReadsTheThreeShapes(t *testing.T) {
	var a struct {
		Naming copyNaming `json:"naming"`
	}
	for raw, want := range map[string]copyNaming{
		`{"naming":"own"}`:      {own: true},
		`{"naming":"numbered"}`: {},
	} {
		if err := json.Unmarshal([]byte(raw), &a); err != nil || a.Naming.own != want.own || a.Naming.beside != nil {
			t.Errorf("%s: %+v %v", raw, a.Naming, err)
		}
	}
	if err := json.Unmarshal([]byte(`{"naming":{"beside":"{name} copy"}}`), &a); err != nil || a.Naming.beside == nil || *a.Naming.beside != "{name} copy" {
		t.Errorf("beside: %+v %v", a.Naming, err)
	}
	if err := json.Unmarshal([]byte(`{"naming":"twice"}`), &a); err == nil {
		t.Error("an unknown naming read")
	}
}
