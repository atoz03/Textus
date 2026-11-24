package backend

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAStoreKeepsWhatItIsGiven(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	s := loadStore(path, "settings", false, true)
	if !s.missingAtLoad {
		t.Error("a new profile is not noticed")
	}
	s.patch(jsonObject{"theme": jsontext.Value(`"dark"`), "fontSize": jsontext.Value(`16`)})
	s.patch(jsonObject{"fontSize": jsontext.Value(`null`)})
	if err := s.flush(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	if !strings.Contains(text, `"schemaVersion": 1`) || !strings.Contains(text, `"theme": "dark"`) || strings.Contains(text, "fontSize") {
		t.Errorf("wrote %s", text)
	}
	again := loadStore(path, "settings", false, true)
	if v, ok := again.get("theme"); !ok || string(v) != `"dark"` {
		t.Errorf("read back %s", v)
	}
}

func TestAnUnreadableStoreIsMovedAside(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ui-state.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := loadStore(path, "state", false, true)
	if v, _ := s.snapshot(); string(v) != "{}" {
		t.Errorf("started with %s", v)
	}
	matches, _ := filepath.Glob(path + ".corrupt-*")
	if len(matches) != 1 {
		t.Errorf("moved aside as %v", matches)
	}
}

func TestHistoryEntriesAreKeptOncePerKey(t *testing.T) {
	dir := t.TempDir()
	s := loadStore(filepath.Join(dir, "clipboard-history.json"), "entries", true, false)
	s.upsertEntry(historyClipboard, jsontext.Value(`{"text":"a","at":1}`))
	s.upsertEntry(historyClipboard, jsontext.Value(`{"text":"b","at":2}`))
	s.upsertEntry(historyClipboard, jsontext.Value(`{"text":"a","at":3}`))
	v, _ := s.snapshot()
	var items []jsontext.Value
	if err := json.Unmarshal(v, &items); err != nil || len(items) != 2 {
		t.Fatalf("entries %s", v)
	}
	if !strings.Contains(string(items[0]), `"a"`) {
		t.Errorf("the latest is not first: %s", v)
	}
}
