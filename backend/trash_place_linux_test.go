//go:build linux && !android

package backend

import (
	"os"
	"path/filepath"
	"testing"
)

// A home Trash of the test's own: an item trashed while the tree looked
// is found by where it came from, and put back with its record gone.
func TestTheHomeTrashKeepsWhereAnItemWentAndGivesItBack(t *testing.T) {
	data := t.TempDir()
	t.Setenv("XDG_DATA_HOME", data)
	trash := filepath.Join(data, "Trash")
	notes := filepath.Join(t.TempDir(), "notes")
	for _, d := range []string{filepath.Join(trash, "files"), filepath.Join(trash, "info"), notes} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	old := filepath.Join(trash, "info", "old.md.trashinfo")
	if err := os.WriteFile(old, []byte("[Trash Info]\nPath="+filepath.Join(notes, "plan.md")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	trashes := map[string]string{homeTrash(): ""}
	before := recordsIn(trashes)
	// As GLib trashes one: the item in files/, its record in info/.
	plan := filepath.Join(notes, "plan.md")
	if err := os.WriteFile(filepath.Join(trash, "files", "plan.2.md"), []byte("plan"), 0o644); err != nil {
		t.Fatal(err)
	}
	record := filepath.Join(trash, "info", "plan.2.md.trashinfo")
	if err := os.WriteFile(record, []byte("[Trash Info]\nPath="+plan+"\nDeletionDate=2026-11-25T10:32:15\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	places := placesOf([]string{plan}, heldIn(trashes, before))
	want := filepath.Join(trash, "files", "plan.2.md")
	if places[0] == nil || *places[0] != want {
		t.Fatalf("places %v", places)
	}
	if err := putBack(want, plan); err != nil {
		t.Fatal(err)
	}
	if raw, _ := os.ReadFile(plan); string(raw) != "plan" {
		t.Errorf("put back %q", raw)
	}
	if _, err := os.Stat(record); err == nil {
		t.Error("its record stayed")
	}
	if err := putBack(want, filepath.Join(notes, "again.md")); !hasCode(err, "goneFromTrash") {
		t.Errorf("gone: %v", err)
	}
	// What isn't a Trash's item is never renamed.
	stray := filepath.Join(notes, "stray.md")
	if err := os.WriteFile(stray, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := putBack(stray, filepath.Join(notes, "moved.md")); !hasCode(err, "goneFromTrash") {
		t.Errorf("stray: %v", err)
	}
}
