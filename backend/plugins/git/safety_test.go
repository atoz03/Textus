package git

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func sourcesIn(t *testing.T, dir, config string) Sources {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "hooks"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dir, "config"), config)
	return Sources{Configs: []string{filepath.Join(dir, "config")}, Hooks: filepath.Join(dir, "hooks")}
}

func TestAnEverydayRepositoryRunsNothing(t *testing.T) {
	config := "[core]\n\trepositoryformatversion = 0\n\tfsmonitor = true\n" +
		"[remote \"origin\"]\n\turl = git@example.com:a/b.git\n" +
		"[branch \"main\"]\n\tremote = origin\n[user]\n\tname = A\n"
	from := sourcesIn(t, t.TempDir(), config)
	write(t, filepath.Join(from.Hooks, "pre-commit.sample"), "")
	if got := risks(from, true); len(got) != 0 {
		t.Fatalf("risks = %v", got)
	}
}

func TestSettingsThatStartAProgramAreNamed(t *testing.T) {
	config := "[core]\n  fsmonitor = /tmp/run.sh\n  sshCommand = evil\n" +
		"[filter \"x\"]\n  clean = cat /etc/passwd\n  required\n" +
		"# [diff]\n; external = no\n[include]\n  path = ../other\n" +
		"[alias]\n  st = !sh -c id\n"
	from := sourcesIn(t, t.TempDir(), config)
	want := []string{"alias.st", "core.fsmonitor", "core.sshcommand", "filter.clean", "include.path"}
	if got := risks(from, false); !slices.Equal(got, want) {
		t.Fatalf("risks = %v, want %v", got, want)
	}
}

func TestHooksCountForACommitOnly(t *testing.T) {
	from := sourcesIn(t, t.TempDir(), "[core]\n\tbare = false\n")
	write(t, filepath.Join(from.Hooks, "pre-commit"), "#!/bin/sh\n")
	if got := risks(from, false); len(got) != 0 {
		t.Fatalf("risks reading = %v", got)
	}
	if got := risks(from, true); !slices.Equal(got, []string{"hook: pre-commit"}) {
		t.Fatalf("risks committing = %v", got)
	}
}

func TestTrustIsForWhatARepositoryRanThen(t *testing.T) {
	store := filepath.Join(t.TempDir(), "deep", "trusted.json")
	top := "/work/notes"
	filter := []string{"filter.clean"}
	if trusted(store, top, filter) {
		t.Fatal("trusted before asked")
	}
	if err := trust(store, top, filter); err != nil {
		t.Fatal(err)
	}
	if !trusted(store, top, filter) || trusted(store, "/work/other", filter) {
		t.Fatal("trust is for that repository")
	}
	// A setting it gained since is asked about again.
	more := []string{"filter.clean", "core.sshcommand"}
	if trusted(store, top, more) {
		t.Fatal("a new setting is trusted unasked")
	}
	if err := trust(store, top, more); err != nil {
		t.Fatal(err)
	}
	if !trusted(store, top, more) || !trusted(store, top, filter) {
		t.Fatal("trust kept")
	}
}
