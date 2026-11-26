package git

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"textus/backend/plugins"
)

// The open folder's repository and its remote: fetch, push and pull, with
// the user's own git and its credentials (a credential helper, an SSH
// agent). Git is told to ask for nothing, so a remote that wants a password
// typed fails, saying so. A pull that would leave conflicts is undone: no
// document is left holding conflict markers.

// networkTime is how long a call to the remote may take: a slow network, a
// large push.
const networkTime = 180 * time.Second

// syncPermission is the permission all three need: what leaves or reaches this
// computer is asked for apart from committing.
const syncPermission = "git.sync"

// ready is the repository's top, checked: the permission, a repository,
// and nothing its settings name that runs without the writer's word.
func ready(s plugins.State, store, id, root string) (string, error) {
	if !s.Allows(id, syncPermission) {
		return "", plugins.Refused(id, syncPermission)
	}
	top := top(root)
	if top == "" {
		return "", errors.New("The folder isn't in a Git repository.")
	}
	if err := guard(store, top); err != nil {
		return "", err
	}
	return top, nil
}

// explained is what git said, put so the writer knows what to do: signing
// in is the usual cause, and Textus never asks for a password itself.
func explained(said error) error {
	lower := strings.ToLower(said.Error())
	for _, s := range []string{
		"authentication failed",
		"could not read username",
		"could not read password",
		"terminal prompts disabled",
		"permission denied (publickey",
		"host key verification failed",
	} {
		if strings.Contains(lower, s) {
			return fmt.Errorf("Git couldn't sign in to the remote. Sign in once with Git in a terminal (or your Git app) so your credentials are saved, then try again.\n\n%s", said)
		}
	}
	return said
}

// Fetch takes the remote's branches, as `git fetch --prune` does.
func Fetch(s plugins.State, store, id, root string) error {
	top, err := ready(s, store, id, root)
	if err != nil {
		return err
	}
	if _, err := runWithin(top, []string{"fetch", "--prune"}, nil, networkTime); err != nil {
		return explained(err)
	}
	return nil
}

// Push sends the branch to its remote branch; a branch with none yet goes
// to origin under its own name and follows it from then on.
func Push(s plugins.State, store, id, root string) error {
	top, err := ready(s, store, id, root)
	if err != nil {
		return err
	}
	args := []string{"push"}
	if _, err := runWithin(top, []string{"rev-parse", "--abbrev-ref", "@{upstream}"}, nil, networkTime); err != nil {
		args = []string{"push", "--set-upstream", "origin", "HEAD"}
	}
	if _, err := runWithin(top, args, nil, networkTime); err != nil {
		return explained(err)
	}
	return nil
}

// Pull takes the remote branch's commits into the branch, as the user's
// `git pull` would (their merge or rebase setting). If that stops on a
// conflict, it is undone and the folder is as it was.
func Pull(s plugins.State, store, id, root string) error {
	top, err := ready(s, store, id, root)
	if err != nil {
		return err
	}
	// A merge or rebase the user has under way is theirs: never undone here.
	if stopped(gitDir(top)) {
		return errors.New("A merge or rebase is under way in this folder. Finish or abort it with Git in a terminal (or your Git app), then pull.")
	}
	_, said := runWithin(top, []string{"pull", "--no-edit"}, nil, networkTime)
	if said == nil {
		return nil
	}
	if undoStopped(top) {
		return fmt.Errorf("The remote's changes conflict with this folder's, so nothing was pulled. Resolve it with Git in a terminal (or your Git app).\n\n%s", said)
	}
	return explained(said)
}

// gitDir is the repository's own folder (.git, or where a worktree's
// lives).
func gitDir(top string) string {
	out, err := runWithin(top, []string{"rev-parse", "--git-dir"}, nil, networkTime)
	if err != nil {
		return filepath.Join(top, ".git")
	}
	return joined(top, strings.TrimSpace(lossy(out)))
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// stopped is whether a merge or rebase stands stopped in dir.
func stopped(dir string) bool {
	return exists(filepath.Join(dir, "MERGE_HEAD")) || exists(filepath.Join(dir, "rebase-merge")) ||
		exists(filepath.Join(dir, "rebase-apply"))
}

// undoStopped undoes a merge or rebase a pull left stopped; whether there
// was one.
func undoStopped(top string) bool {
	dir := gitDir(top)
	if exists(filepath.Join(dir, "MERGE_HEAD")) {
		_, _ = runWithin(top, []string{"merge", "--abort"}, nil, networkTime)
		return true
	}
	if exists(filepath.Join(dir, "rebase-merge")) || exists(filepath.Join(dir, "rebase-apply")) {
		_, _ = runWithin(top, []string{"rebase", "--abort"}, nil, networkTime)
		return true
	}
	return false
}
