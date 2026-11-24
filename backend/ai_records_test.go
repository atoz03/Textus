package backend

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func conversation(id string, at int, text string) jsontext.Value {
	return jsontext.Value(fmt.Sprintf(`{"id":%q,"title":%q,"updatedAt":%d,"turns":[{"text":%q}]}`, id, text, at, text))
}

func writeText(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestRecordsCarryToAnotherComputerAndOnlyAdd(t *testing.T) {
	from, to := t.TempDir(), t.TempDir()
	globalA, globalB := filepath.Join(from, "textus"), filepath.Join(to, "textus")
	a := loadChatHistory(filepath.Join(from, "data"))
	a.save(conversation("c1", 10, "tides"))
	a.save(conversation("c2", 20, "gauges, newer there"))
	writeText(t, filepath.Join(globalA, "memory", "likes.md"), "tea")
	writeText(t, filepath.Join(globalA, "memory", ".revision-choices.json"), "{}")
	writeText(t, filepath.Join(globalA, "projects", "novel-1a2b", "memory", "cast.md"), "Ada")

	b := loadChatHistory(filepath.Join(to, "data"))
	b.save(conversation("c2", 5, "gauges, older here"))
	b.save(conversation("c3", 30, "only here"))
	writeText(t, filepath.Join(globalB, "memory", "likes.md"), "coffee")

	raw, err := json.Marshal(collectRecords(a, globalA))
	if err != nil {
		t.Fatal(err)
	}
	var records AiRecords
	if err := json.Unmarshal(raw, &records); err != nil || len(records.Memories) != 2 {
		t.Fatalf("%s %v", raw, err)
	}
	imported, saved := mergeRecords(b, globalB, records)
	if imported != (Imported{Conversations: 2, Memories: 1}) || len(saved) != 2 {
		t.Errorf("%+v %d", imported, len(saved))
	}
	if got := b.get("c2"); got == nil || string(fieldsOf(got).Title) != `"gauges, newer there"` {
		t.Errorf("%s", got)
	}
	if b.get("c3") == nil {
		t.Error("a conversation only here was lost")
	}
	// A memory of the same name here stays as it is.
	for path, want := range map[string]string{
		filepath.Join(globalB, "memory", "likes.md"):                          "coffee",
		filepath.Join(globalB, "projects", "novel-1a2b", "memory", "cast.md"): "Ada",
	} {
		if got, _ := os.ReadFile(path); string(got) != want {
			t.Errorf("%s: %q", path, got)
		}
	}
	// Nothing here: empty lists, not null.
	if raw, _ := json.Marshal(collectRecords(loadChatHistory(t.TempDir()), t.TempDir())); string(raw) != `{"conversations":[],"memories":[]}` {
		t.Errorf("%s", raw)
	}
}

func TestAMemoryPathCanOnlyNameAMemory(t *testing.T) {
	for _, good := range []string{"memory/a.md", "projects/k/memory/a.md"} {
		if _, ok := memoryTarget("/g", good); !ok {
			t.Errorf("%s refused", good)
		}
	}
	for _, bad := range []string{
		"memory/../a.md", "../memory/a.md", "memory/a.txt", "agent.md", "projects/../memory/a.md",
		"memory/sub/a.md", "memory/.hidden.md", "projects/k/skills/a.md", `memory/a\b.md`, "memory/a:b.md", "memory/a\x00.md",
	} {
		if _, ok := memoryTarget("/g", bad); ok {
			t.Errorf("%q taken", bad)
		}
	}
}
