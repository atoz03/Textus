package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"textus/backend/plugins"
)

func cloneOf(t *testing.T, remote, name, into string) string {
	t.Helper()
	dir := filepath.Join(into, name)
	// Windows' git writes files out with CRLF unless told not to.
	gitIn(t, into, "clone", "-q", "-c", "core.autocrlf=false", remote, name)
	for _, kv := range [][2]string{{"user.email", "a@b.c"}, {"user.name", "A"}, {"commit.gpgsign", "false"}, {"pull.rebase", "false"}} {
		gitIn(t, dir, "config", kv[0], kv[1])
	}
	return dir
}

func commitIn(t *testing.T, dir, file, text string) {
	t.Helper()
	write(t, filepath.Join(dir, file), text)
	gitIn(t, dir, "add", file)
	gitIn(t, dir, "commit", "-qm", file)
}

// remote is a bare remote with one commit on main, and two clones of it.
func remote(t *testing.T) (string, string) {
	t.Helper()
	needGit(t)
	base := realTemp(t)
	bare := filepath.Join(base, "remote.git")
	gitIn(t, base, "init", "-q", "--bare", "-b", "main", "remote.git")
	seed := cloneOf(t, bare, "seed", base)
	commitIn(t, seed, "a.md", "one\n")
	gitIn(t, seed, "push", "-q", "origin", "main")
	return cloneOf(t, bare, "mine", base), cloneOf(t, bare, "theirs", base)
}

var syncing = []string{"files.read", "git.sync"}

func TestEachNeedsGitSync(t *testing.T) {
	mine, _ := remote(t)
	s := state(t, "files.read", "git.commit")
	for _, call := range []func(plugins.State, string, string, string) error{Fetch, Push, Pull} {
		mustContain(t, call(s, nowhere, "a.b", mine), "git.sync")
	}
}

func TestPushSendsTheCommitsStatusCountedAndPullBringsThem(t *testing.T) {
	mine, theirs := remote(t)
	commitIn(t, mine, "b.md", "two\n")
	s := state(t, syncing...)
	before, err := GetStatus(s, nowhere, "a.b", mine)
	if err != nil {
		t.Fatal(err)
	}
	if before.Upstream == nil || *before.Upstream != "origin/main" || before.Ahead != 1 || before.Behind != 0 {
		t.Fatalf("before = %+v", before)
	}
	if err := Push(s, nowhere, "a.b", mine); err != nil {
		t.Fatal(err)
	}
	if err := Fetch(s, nowhere, "a.b", theirs); err != nil {
		t.Fatal(err)
	}
	behind, err := GetStatus(s, nowhere, "a.b", theirs)
	if err != nil || behind.Ahead != 0 || behind.Behind != 1 {
		t.Fatalf("behind = %+v, %v", behind, err)
	}
	if err := Pull(s, nowhere, "a.b", theirs); err != nil {
		t.Fatal(err)
	}
	if got := read(t, filepath.Join(theirs, "b.md")); got != "two\n" {
		t.Fatalf("b.md = %q", got)
	}
}

func TestABranchWithNoRemoteBranchGoesToOriginAndFollowsIt(t *testing.T) {
	mine, _ := remote(t)
	gitIn(t, mine, "checkout", "-q", "-b", "draft")
	commitIn(t, mine, "c.md", "three\n")
	s := state(t, syncing...)
	if err := Push(s, nowhere, "a.b", mine); err != nil {
		t.Fatal(err)
	}
	after, err := GetStatus(s, nowhere, "a.b", mine)
	if err != nil || after.Upstream == nil || *after.Upstream != "origin/draft" || after.Ahead != 0 {
		t.Fatalf("after = %+v, %v", after, err)
	}
}

func TestAPullThatConflictsIsUndoneAndSaysSo(t *testing.T) {
	mine, theirs := remote(t)
	commitIn(t, theirs, "a.md", "theirs\n")
	gitIn(t, theirs, "push", "-q", "origin", "main")
	commitIn(t, mine, "a.md", "mine\n")
	mustContain(t, Pull(state(t, syncing...), nowhere, "a.b", mine), "conflict")
	if got := read(t, filepath.Join(mine, "a.md")); got != "mine\n" {
		t.Fatalf("a.md = %q", got)
	}
	if exists(filepath.Join(mine, ".git", "MERGE_HEAD")) {
		t.Fatal("the merge was left stopped")
	}
}

func TestAMergeUnderWayIsLeftToTheUser(t *testing.T) {
	mine, theirs := remote(t)
	commitIn(t, theirs, "a.md", "theirs\n")
	gitIn(t, theirs, "push", "-q", "origin", "main")
	commitIn(t, mine, "a.md", "mine\n")
	gitIn(t, mine, "fetch", "-q")
	// The user's own merge, stopped on its conflict.
	merge := exec.Command("git", "merge", "-q", "origin/main")
	merge.Dir = mine
	_ = merge.Run()
	if !exists(filepath.Join(mine, ".git", "MERGE_HEAD")) {
		t.Fatal("the merge didn't stop")
	}
	mustContain(t, Pull(state(t, syncing...), nowhere, "a.b", mine), "under way")
	if !exists(filepath.Join(mine, ".git", "MERGE_HEAD")) {
		t.Fatal("the user's merge was undone")
	}
}

func TestARemoteThatNamesItsOwnProgramAsksForTrustFirst(t *testing.T) {
	mine, _ := remote(t)
	for _, kv := range [][2]string{{"remote.origin.uploadpack", "touch ran; git-upload-pack"}, {"protocol.ext.allow", "always"}} {
		gitIn(t, mine, "config", kv[0], kv[1])
		err := Fetch(state(t, syncing...), nowhere, "a.b", mine)
		if err == nil || !strings.HasPrefix(err.Error(), Untrusted) {
			t.Fatalf("%s: %v", kv[0], err)
		}
		gitIn(t, mine, "config", "--unset", kv[0])
	}
	if _, err := os.Stat(filepath.Join(mine, "ran")); err == nil {
		t.Fatal("the remote's program ran")
	}
}
