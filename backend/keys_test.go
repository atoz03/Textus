package backend

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// ownerOnly says whether only its owner can read the file. Windows keeps no
// Unix permission bits (Go reads any file that isn't read-only as 0666);
// there the user's profile folder, which holds the app's, keeps others out.
func ownerOnly(info os.FileInfo) bool {
	return runtime.GOOS == "windows" || info.Mode().Perm() == 0o600
}

func TestKeysAreKeptForTheirOwnerOnly(t *testing.T) {
	a := &App{dirs: Dirs{Config: t.TempDir()}}
	set := func(id, key string) {
		t.Helper()
		if err := a.changeKeys(func(keys map[string]string) bool { keys[id] = key; return true }); err != nil {
			t.Fatal(err)
		}
	}
	set("anthropic", "sk-ant")
	set("__image_host", "tok")
	if key, err := a.providerKey("anthropic"); err != nil || key != "sk-ant" {
		t.Errorf("%q %v", key, err)
	}
	if key, _ := a.providerKey("openai"); key != "" {
		t.Errorf("openai: %q", key)
	}
	info, err := os.Stat(a.keysPath())
	if err != nil || !ownerOnly(info) {
		t.Fatalf("%v %v", info.Mode(), err)
	}
	// Clearing one keeps the other.
	if err := a.changeKeys(func(keys map[string]string) bool { delete(keys, "anthropic"); return true }); err != nil {
		t.Fatal(err)
	}
	if key, _ := a.providerKey("__image_host"); key != "tok" {
		t.Errorf("%q", key)
	}
}

func TestAKeyFileThatDoesNotParseIsMovedAside(t *testing.T) {
	a := &App{dirs: Dirs{Config: t.TempDir()}}
	if err := os.WriteFile(a.keysPath(), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if key, err := a.providerKey("anthropic"); err != nil || key != "" {
		t.Errorf("%q %v", key, err)
	}
	matches, _ := filepath.Glob(a.keysPath() + "*")
	if len(matches) != 1 || matches[0] == a.keysPath() {
		t.Errorf("%v", matches)
	}
}

func TestAKeyFileThatCannotBeReadIsNotWrittenOver(t *testing.T) {
	a := &App{dirs: Dirs{Config: t.TempDir()}}
	// A folder where the file should be: reading fails, so a save must too.
	if err := os.Mkdir(a.keysPath(), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := a.changeKeys(func(keys map[string]string) bool { keys["x"] = "y"; return true }); err == nil {
		t.Error("saved over what couldn't be read")
	}
}
