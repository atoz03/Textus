package git

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

// repo is a repository with notes/a.md committed, renamed to notes/b.md
// and committed again, then b.md changed, c.md new and .env changed: its
// top and the notes folder.
func repo(t *testing.T) (string, string) {
	t.Helper()
	needGit(t)
	top := realTemp(t)
	root := filepath.Join(top, "notes")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	gitIn(t, top, "init", "-q", "-b", "main")
	gitIn(t, top, "config", "user.email", "a@b.c")
	gitIn(t, top, "config", "user.name", "A")
	gitIn(t, top, "config", "commit.gpgsign", "false")
	write(t, filepath.Join(root, "a.md"), "one\n")
	write(t, filepath.Join(root, ".env"), "TOKEN=1\n")
	gitIn(t, top, "add", ".")
	gitIn(t, top, "commit", "-qm", "First")
	gitIn(t, top, "mv", "notes/a.md", "notes/b.md")
	gitIn(t, top, "commit", "-qm", "Renamed")
	write(t, filepath.Join(root, "b.md"), "one\ntwo\n")
	write(t, filepath.Join(root, "c.md"), "new\n")
	write(t, filepath.Join(root, ".env"), "TOKEN=2\n")
	return top, root
}

func TestStatusNamesTheBranchAndTheChangesButNoHiddenFile(t *testing.T) {
	_, root := repo(t)
	s, err := GetStatus(state(t, "files.read"), nowhere, "a.b", root)
	if err != nil || s == nil {
		t.Fatalf("status: %v %v", s, err)
	}
	if s.Branch == nil || *s.Branch != "main" {
		t.Fatalf("branch = %v", s.Branch)
	}
	var seen []string
	for _, c := range s.Changes {
		seen = append(seen, filepath.Base(c.Path)+" "+c.State)
	}
	slices.Sort(seen)
	if want := []string{"b.md modified", "c.md untracked"}; !slices.Equal(seen, want) {
		t.Fatalf("changes = %v, want %v", seen, want)
	}
}

func TestPathsAreNamedUnderTheFolderAsGivenNotItsRealPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("links need privileges on Windows")
	}
	_, realRoot := repo(t)
	root := filepath.Join(t.TempDir(), "linked")
	if err := os.Symlink(realRoot, root); err != nil {
		t.Fatal(err)
	}
	s := state(t, "files.read")
	status, err := GetStatus(s, nowhere, "a.b", root)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range status.Changes {
		if !strings.HasPrefix(c.Path, root) {
			t.Fatalf("%s is not under %s", c.Path, root)
		}
	}
	commits, err := Log(s, nowhere, "a.b", root, root+"/b.md", nil)
	if err != nil || !strings.HasPrefix(commits[0].Path, root) {
		t.Fatalf("log: %v %v", commits, err)
	}
}

func TestAFolderInNoRepositoryHasNoStatus(t *testing.T) {
	needGit(t)
	s, err := GetStatus(state(t, "files.read"), nowhere, "a.b", t.TempDir())
	if err != nil || s != nil {
		t.Fatalf("status = %v, %v", s, err)
	}
}

func TestAFilesHistoryFollowsRenamesAndShowsEachVersion(t *testing.T) {
	_, root := repo(t)
	s := state(t, "files.read")
	b := root + "/b.md"
	commits, err := Log(s, nowhere, "a.b", root, b, nil)
	if err != nil {
		t.Fatal(err)
	}
	var subjects []string
	for _, c := range commits {
		subjects = append(subjects, c.Subject)
	}
	if !slices.Equal(subjects, []string{"Renamed", "First"}) {
		t.Fatalf("subjects = %v", subjects)
	}
	first := commits[1]
	if !strings.HasSuffix(first.Path, "a.md") {
		t.Fatalf("first path = %s", first.Path)
	}
	if text, err := Show(s, nowhere, "a.b", root, first.Hash, first.Path); err != nil || text == nil || *text != "one\n" {
		t.Fatalf("show = %v, %v", text, err)
	}
	// Not a file of that commit; not a hash; not a hidden file.
	if text, err := Show(s, nowhere, "a.b", root, first.Hash, b); err != nil || text != nil {
		t.Fatalf("show of a file the commit hadn't = %v, %v", text, err)
	}
	if _, err := Show(s, nowhere, "a.b", root, "--output=x", b); err == nil {
		t.Fatal("a hash that isn't one was taken")
	}
	if _, err := Show(s, nowhere, "a.b", root, first.Hash, root+"/.env"); err == nil {
		t.Fatal("a hidden file was shown")
	}
	if _, err := Log(s, nowhere, "a.b", root, root+"/.env", nil); err == nil {
		t.Fatal("a hidden file's history was given")
	}
}

func TestReadingHistoryNeedsFilesRead(t *testing.T) {
	_, root := repo(t)
	if _, err := Log(state(t), nowhere, "a.b", root, root+"/b.md", nil); err == nil {
		t.Fatal("history read without files.read")
	}
}

func TestACommitTakesTheNamedFilesOnlyAndNeedsItsGrant(t *testing.T) {
	_, root := repo(t)
	c := root + "/c.md"
	if _, err := MakeCommit(state(t, "files.read"), nowhere, "a.b", root, "Add c", []string{c}); err == nil {
		t.Fatal("committed without git.commit")
	}
	s := state(t, "files.read", "git.commit")
	if _, err := MakeCommit(s, nowhere, "a.b", root, "Hidden", []string{root + "/.env"}); err == nil {
		t.Fatal("a hidden file was committed")
	}
	hash, err := MakeCommit(s, nowhere, "a.b", root, "Add c\n\nWith a body.", []string{c})
	if err != nil || !isHash(hash) {
		t.Fatalf("commit = %q, %v", hash, err)
	}
	if commits, err := Log(s, nowhere, "a.b", root, c, nil); err != nil || commits[0].Subject != "Add c" {
		t.Fatalf("log = %v, %v", commits, err)
	}
	status, err := GetStatus(s, nowhere, "a.b", root)
	if err != nil {
		t.Fatal(err)
	}
	var left []string
	for _, c := range status.Changes {
		left = append(left, c.Path)
	}
	// Named in the system's own separator under the folder.
	if want := []string{filepath.Join(root, "b.md")}; !slices.Equal(left, want) {
		t.Fatalf("left = %v, want %v", left, want)
	}
}

func TestARepositoryWhoseSettingsRunAProgramRunsNothingUntilTrusted(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a shell script")
	}
	top, root := repo(t)
	marker := filepath.Join(top, "ran")
	script := filepath.Join(top, "monitor.sh")
	write(t, script, "#!/bin/sh\ntouch '"+marker+"'\n")
	if err := os.Chmod(script, 0o755); err != nil {
		t.Fatal(err)
	}
	gitIn(t, top, "config", "core.fsmonitor", script)
	s := state(t, "files.read", "git.commit")
	store := filepath.Join(top, "trusted.json")
	_, err := GetStatus(s, store, "a.b", root)
	if err == nil || err.Error() != Untrusted+"core.fsmonitor" {
		t.Fatalf("status = %v", err)
	}
	if _, err := Log(s, store, "a.b", root, root+"/b.md", nil); err == nil {
		t.Fatal("history read in an untrusted repository")
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("something ran while it was asked about")
	}
	// Trusted, Git works - and still without the monitor.
	if err := trust(store, top, []string{"core.fsmonitor"}); err != nil {
		t.Fatal(err)
	}
	if status, err := GetStatus(s, store, "a.b", root); err != nil || status == nil {
		t.Fatalf("trusted status = %v, %v", status, err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("the monitor ran: it is off on every call")
	}
}

func TestACommitAsksAboutTheHooksItWouldRun(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a shell script")
	}
	top, root := repo(t)
	hook := filepath.Join(top, ".git", "hooks", "pre-commit")
	write(t, hook, "#!/bin/sh\nexit 0\n")
	if err := os.Chmod(hook, 0o755); err != nil {
		t.Fatal(err)
	}
	s := state(t, "files.read", "git.commit")
	store := filepath.Join(top, "trusted.json")
	files := []string{root + "/c.md"}
	// Reading runs no hook: nothing to ask.
	if _, err := GetStatus(s, store, "a.b", root); err != nil {
		t.Fatal(err)
	}
	_, err := MakeCommit(s, store, "a.b", root, "Add c", files)
	if err == nil || err.Error() != Untrusted+"hook: pre-commit" {
		t.Fatalf("commit = %v", err)
	}
	if err := trust(store, top, []string{"hook: pre-commit"}); err != nil {
		t.Fatal(err)
	}
	if _, err := MakeCommit(s, store, "a.b", root, "Add c", files); err != nil {
		t.Fatal(err)
	}
}

func TestACommitOfAFolderIsRefusedSoHiddenFilesStayOut(t *testing.T) {
	_, root := repo(t)
	s := state(t, "files.read", "git.commit")
	if _, err := MakeCommit(s, nowhere, "a.b", root, "All", []string{root}); err == nil {
		t.Fatal("a folder was committed")
	}
	status, err := GetStatus(s, nowhere, "a.b", root)
	if err != nil || !slices.ContainsFunc(status.Changes, func(c Change) bool { return strings.HasSuffix(c.Path, "c.md") }) {
		t.Fatalf("status = %v, %v", status, err)
	}
}

func TestACallThatTakesTooLongIsStopped(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sleep")
	}
	top, _ := repo(t)
	started := time.Now()
	_, err := runWithin(top, []string{"-c", "alias.wait=!sleep 5", "wait"}, nil, 200*time.Millisecond)
	mustContain(t, err, "too long")
	if time.Since(started) > 3*time.Second {
		t.Fatalf("stopped after %v", time.Since(started))
	}
}

func TestTrustNeedsARepository(t *testing.T) {
	needGit(t)
	store := filepath.Join(t.TempDir(), "trusted.json")
	mustContain(t, Trust(store, t.TempDir()), "isn't in a Git repository")
	top, root := repo(t)
	gitIn(t, top, "config", "core.sshCommand", "ssh -v")
	if err := Trust(store, root); err != nil {
		t.Fatal(err)
	}
	if !trusted(store, top, []string{"core.sshcommand"}) {
		t.Fatal("the repository wasn't trusted")
	}
}
