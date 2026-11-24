package backend

import (
	"os"
	"testing"
)

func TestTheMenuTableReads(t *testing.T) {
	table, err := os.ReadFile("../src/registry/menu/menu.json")
	if err != nil {
		t.Fatal(err)
	}
	formats, err := os.ReadFile("../src/registry/export-formats.json")
	if err != nil {
		t.Fatal(err)
	}
	m := newMenuState(table, formats)
	if len(m.bar) == 0 || len(m.pandoc) == 0 || m.texts["recent-clear"] == "" {
		t.Fatalf("read %d menus, %d formats", len(m.bar), len(m.pandoc))
	}
	settings, ok := m.findItem("settings")
	if !ok || !settings.OrOpen || settings.accelerator() != "CmdOrCtrl+," {
		t.Errorf("settings item %+v", settings)
	}
	if _, ok := m.findItem("no-such-item"); ok {
		t.Error("found an item that is not there")
	}
	if got := accelerator("mod+shift+s"); got != "CmdOrCtrl+Shift+s" {
		t.Errorf("accelerator %q", got)
	}
}

func TestRecentLists(t *testing.T) {
	list := pushFront([]string{"a", "b", "c"}, "b", 10)
	if len(list) != 3 || list[0] != "b" || list[1] != "a" || list[2] != "c" {
		t.Errorf("push front %v", list)
	}
	if got := pushFront([]string{"a", "b"}, "c", 2); len(got) != 2 || got[0] != "c" || got[1] != "a" {
		t.Errorf("capped %v", got)
	}
	t.Setenv("HOME", "/home/someone")
	if got := abbreviateHome("/home/someone/notes/a.md"); got != "~/notes/a.md" {
		t.Errorf("home %q", got)
	}
	if got := abbreviateHome("/home/someone-else/a.md"); got != "/home/someone-else/a.md" {
		t.Errorf("another home %q", got)
	}
	if got := folderMenuText("/home/someone/notes/"); got != "~/notes/" {
		t.Errorf("folder %q", got)
	}
}
