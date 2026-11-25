package git

import (
	"encoding/json/v2"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"textus/backend/plugins"
)

// state has plugin a.b on with granted.
func state(t *testing.T, granted ...string) plugins.State {
	t.Helper()
	if granted == nil {
		granted = []string{}
	}
	raw, err := json.Marshal(map[string]any{"plugins": map[string]any{"a.b": map[string]any{"enabled": true, "granted": granted}}})
	if err != nil {
		t.Fatal(err)
	}
	s, err := plugins.ParseState(raw)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// nowhere is a list that trusts no repository: none of the tests' runs
// anything.
const nowhere = "/nowhere/git-trusted.json"

func needGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git isn't installed")
	}
}

func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func write(t *testing.T, path, text string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func realTemp(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func mustContain(t *testing.T, err error, part string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), part) {
		t.Fatalf("want an error saying %q, got %v", part, err)
	}
}
