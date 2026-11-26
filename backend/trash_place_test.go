package backend

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
	"time"
	"unicode/utf16"
)

// Windows' list: each item found by the folder it came from and its name,
// else its name less the extension, newest first, once each.
func TestEachTrashedPathIsFoundByItsFolderAndName(t *testing.T) {
	went := []string{`C:\n\a\plan.md`, `C:\n\b\plan.md`, `C:\n\b\report.pdf`, `C:\n\b\gone.md`}
	fresh := []held{
		{id: "$R1", parent: `\\?\C:\n\b`, name: "plan.md", time: 5},
		{id: "$R2", parent: `C:\n\a`, name: "plan.md", time: 5},
		{id: "$R0", parent: `C:\n\a`, name: "plan.md", time: 1},
		{id: "$R3", parent: `C:\n\b`, name: "report", time: 5},
	}
	got := placesOf(went, fresh)
	want := []string{"$R2", "$R1", "$R3", ""}
	for i, w := range want {
		g := ""
		if got[i] != nil {
			g = *got[i]
		}
		if g != w {
			t.Errorf("%s: %q, want %q", went[i], g, w)
		}
	}
}

// A folder standing in for the Trash: the user's own is never touched.
func TestPutBackReturnsAnItemAndRefusesATakenPlaceOrAGoneOne(t *testing.T) {
	tmp := t.TempDir()
	trash, notes := filepath.Join(tmp, ".Trash"), filepath.Join(tmp, "notes")
	for _, d := range []string{trash, notes} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	kept := filepath.Join(trash, "plan 10.32.15.md")
	home := filepath.Join(notes, "plan.md")
	if err := os.WriteFile(kept, []byte("plan"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(home, []byte("another"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := putBack(kept, home); !hasCode(err, "exists") {
		t.Errorf("over another: %v", err)
	}
	if err := os.Remove(home); err != nil {
		t.Fatal(err)
	}
	if err := renameBack(kept, home); err != nil {
		t.Fatal(err)
	}
	if raw, _ := os.ReadFile(home); string(raw) != "plan" {
		t.Errorf("put back %q", raw)
	}
	if err := renameBack(kept, filepath.Join(notes, "again.md")); !hasCode(err, "goneFromTrash") {
		t.Errorf("gone: %v", err)
	}
}

func TestATrashInfoRecordSaysWhereAnItemCameFrom(t *testing.T) {
	raw := []byte("[Trash Info]\nPath=/home/me/My%20Notes/plan%E2%80%94v2.md\nDeletionDate=2026-11-25T10:32:15\n")
	path, at, ok := trashInfo(raw, "")
	if !ok || path != "/home/me/My Notes/plan—v2.md" {
		t.Errorf("%q %v", path, ok)
	}
	if want := time.Date(2026, 11, 25, 10, 32, 15, 0, time.Local); !at.Equal(want) {
		t.Errorf("at %v", at)
	}
	// A disk's own Trash keeps the path under the disk's top.
	if path, _, ok := trashInfo([]byte("[Trash Info]\nPath=notes/a.md\n"), "/media/usb"); !ok || path != "/media/usb/notes/a.md" {
		t.Errorf("relative %q", path)
	}
	if _, _, ok := trashInfo([]byte("[Other]\nPath=/a.md\n"), ""); ok {
		t.Error("a path outside [Trash Info] read")
	}
}

func TestARecycleBinRecordSaysWhereAnItemCameFrom(t *testing.T) {
	path := `C:\Users\me\Notes\plan.md`
	units := append(utf16.Encode([]rune(path)), 0)
	two := make([]byte, 28+2*len(units))
	binary.LittleEndian.PutUint64(two, 2)
	binary.LittleEndian.PutUint64(two[16:], 133700000000000000)
	binary.LittleEndian.PutUint32(two[24:], uint32(len(units)))
	for i, u := range units {
		binary.LittleEndian.PutUint16(two[28+2*i:], u)
	}
	if got, at, ok := recycleRecord(two); !ok || got != path || at != 133700000000000000 {
		t.Errorf("version 2: %q %d %v", got, at, ok)
	}
	one := make([]byte, 24+520)
	binary.LittleEndian.PutUint64(one, 1)
	for i, u := range units {
		binary.LittleEndian.PutUint16(one[24+2*i:], u)
	}
	if got, _, ok := recycleRecord(one); !ok || got != path {
		t.Errorf("version 1: %q %v", got, ok)
	}
	if _, _, ok := recycleRecord(two[:30]); ok {
		t.Error("a cut record read")
	}
}
