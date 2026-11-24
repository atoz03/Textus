package backend

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProposalsAnOlderTextusKeptInTheProjectMoveToTheAppData(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "novel")
	old := filepath.Join(root, ".textus", "pending", "chapters", "one.md.json")
	os.MkdirAll(filepath.Dir(old), 0o755)
	os.WriteFile(old, []byte("[]"), 0o644)
	os.MkdirAll(picturesDir(old), 0o755)
	doc := filepath.Join(root, "chapters", "one.md")
	data := filepath.Join(dir, "data")

	file, ok := keptFile(doc, root, data)
	if !ok || !strings.HasPrefix(file, filepath.Join(data, "pending")) {
		t.Fatal(file)
	}
	if held, _ := os.ReadFile(file); string(held) != "[]" {
		t.Errorf("%q", held)
	}
	if info, err := os.Stat(picturesDir(file)); err != nil || !info.IsDir() {
		t.Error("the pictures stayed behind")
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Error("the old file is still there")
	}
	// Without the folder given, the document's own project is found.
	if _, ok := projectRoot(doc, ""); !ok {
		t.Error("no project for a document inside one")
	}
}

func TestADocumentOutsideAProjectKeepsThemInTheAppData(t *testing.T) {
	dir := t.TempDir()
	data := filepath.Join(dir, "data")
	file, ok := keptFile(filepath.Join(dir, "loose.md"), "", data)
	if !ok || filepath.Dir(file) != filepath.Join(data, "pending") {
		t.Error(file)
	}
	if _, ok := keptFile("relative.md", "", data); ok {
		t.Error("a relative path was kept")
	}
}

func TestOnlyTheKeptPicturesStay(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "a.json.pictures")
	os.MkdirAll(dir, 0o755)
	for _, name := range []string{"abc1.png", "zz9.webp", "notes.txt"} {
		os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644)
	}
	if err := keepPictures(dir, []string{"abc1"}); err != nil {
		t.Fatal(err)
	}
	left, _ := os.ReadDir(dir)
	if len(left) != 2 || left[0].Name() != "abc1.png" || left[1].Name() != "notes.txt" {
		t.Errorf("%v", left)
	}
	// With nothing else in it, the folder goes too.
	os.Remove(filepath.Join(dir, "notes.txt"))
	keepPictures(dir, nil)
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Error("the empty folder stayed")
	}
}

func TestTheRevisionChoicesLogSitsBesideThePersonalMemories(t *testing.T) {
	a := &App{dirs: Dirs{Config: t.TempDir()}}
	if !strings.HasSuffix(filepath.ToSlash(a.choicesPath()), "textus/memory/.revision-choices.json") {
		t.Error(a.choicesPath())
	}
}
