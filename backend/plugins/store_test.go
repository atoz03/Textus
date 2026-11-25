package plugins

import (
	"encoding/json/jsontext"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func writeFile(path string, data []byte) error { return os.WriteFile(path, data, 0o644) }

func TestAKeyIsAFileNameAndComesBack(t *testing.T) {
	for _, key := range []string{"notes", "v:/a/b.md", "../../etc", "日记 2026", "a%b.c"} {
		file, err := KeyFile(key)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(file, "/") || strings.Contains(file, "..") {
			t.Errorf("%s names a path: %s", key, file)
		}
		if back, ok := FileKey(file); !ok || back != key {
			t.Errorf("%s came back as %q", key, back)
		}
	}
	for _, bad := range []string{"", strings.Repeat("x", maxKeyBytes+1)} {
		if _, err := KeyFile(bad); err == nil {
			t.Errorf("a key of %d bytes is taken", len(bad))
		}
	}
}

func TestKeysAreKeptAndTakenAwayOneFileEach(t *testing.T) {
	place := filepath.Join(t.TempDir(), "p")
	for key, value := range map[string]string{"a": `{"n": 1}`, "v:/doc.md": `"text"`} {
		if err := StoreSet(place, key, jsontext.Value(value), writeFile); err != nil {
			t.Fatal(err)
		}
	}
	if got, err := StoreGet(place, "a"); err != nil || string(got) != `{"n":1}` {
		t.Errorf("a: %s %v", got, err)
	}
	if keys := StoreKeys(place); !slices.Equal(keys, []string{"a", "v:/doc.md"}) {
		t.Errorf("keys %q", keys)
	}
	if err := StoreSet(place, "a", jsontext.Value("null"), writeFile); err != nil {
		t.Fatal(err)
	}
	if got, err := StoreGet(place, "a"); err != nil || got != nil {
		t.Errorf("a taken away: %s %v", got, err)
	}
	if entries, _ := os.ReadDir(filepath.Join(place, "kv")); len(entries) != 1 {
		t.Errorf("%d files", len(entries))
	}
}

func TestFilesStayInTheirFolder(t *testing.T) {
	tmp := t.TempDir()
	place := filepath.Join(tmp, "p")
	f, err := FileIn(place, "board/one.md")
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteWithin(place, f, []byte("# One"), writeFile); err != nil {
		t.Fatal(err)
	}
	for _, out := range []string{"../kv/a.json", "/etc/passwd"} {
		if _, err := FileIn(place, out); err == nil {
			t.Errorf("%s is in the folder", out)
		}
	}
	if got := StoreFiles(filepath.Join(place, "files")); !slices.Equal(got, []string{"board/one.md"}) {
		t.Errorf("files %q", got)
	}
	// A link a shared folder planted is never followed.
	away := filepath.Join(tmp, "away")
	if err := os.MkdirAll(away, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(away, filepath.Join(place, "files/out")); err != nil {
		t.Skip("no links here:", err)
	}
	if _, err := FileIn(place, "out/x.md"); err == nil {
		t.Error("a link out is followed")
	}
	if err := os.MkdirAll(filepath.Join(place, "kv"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(away, "k.json"), filepath.Join(place, "kv/k.json")); err != nil {
		t.Fatal(err)
	}
	if StoreSet(place, "k", jsontext.Value("1"), writeFile) == nil {
		t.Error("a key is written through a link")
	}
	if _, err := StoreGet(place, "k"); err == nil {
		t.Error("a key is read through a link")
	}
}

func TestTheQuotaHoldsAndARewriteCountsOnce(t *testing.T) {
	place := filepath.Join(t.TempDir(), "p")
	big := make([]byte, QuotaBytes-10)
	f, err := FileIn(place, "big.bin")
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteWithin(place, f, big, writeFile); err != nil {
		t.Fatal(err)
	}
	// The same file again replaces itself: still within.
	if err := WriteWithin(place, f, big, writeFile); err != nil {
		t.Errorf("a rewrite: %v", err)
	}
	if StoreSet(place, "more", jsontext.Value(`"`+strings.Repeat("x", 64)+`"`), writeFile) == nil {
		t.Error("the quota doesn't hold")
	}
}

func TestWithNoFolderOpenTheProjectPlaceIsHiddenBesideTheGlobalOnes(t *testing.T) {
	if got := LooseDir("/config/textus", "local.todo"); got != filepath.FromSlash("/config/textus/plugins/.no-folder/local.todo") {
		t.Errorf("%s", got)
	}
	// No plugin's global place can be it: an id never starts with a dot.
	if RequireID(".no-folder") == nil {
		t.Error(".no-folder is an id")
	}
}

func TestAProjectPlaceIsUnderItsTextusFolder(t *testing.T) {
	if got := ProjectPlace("/notes", "catvinci.kanban"); got != filepath.FromSlash("/notes/.textus/plugins/catvinci.kanban") {
		t.Errorf("%s", got)
	}
}
