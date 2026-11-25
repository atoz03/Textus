package backend

import (
	"path/filepath"

	"textus/backend/plugins"
	"textus/backend/plugins/git"
)

// A plugin's view of the open folder's Git history (backend/plugins/git):
// each call checks the folder as a read of it is, then runs the user's
// own git.

// gitTrustedFile is the list of repositories the writer trusts to run what
// their settings name, in the app's data.
const gitTrustedFile = "git-trusted.json"

func (a *App) gitTrusted() string { return filepath.Join(a.dirs.Data, gitTrustedFile) }

// gitArgs name the plugin and the folder its window has open.
type gitArgs struct {
	ID   string `json:"id"`
	Root string `json:"root"`
}

// gitCall is a plugin's git call: the folder checked, then the work.
func gitCall[T any](c *Call, g gitArgs, work func(s plugins.State, trusted string) (T, error)) (T, error) {
	if err := c.app.checkedRoot(g.ID, g.Root); err != nil {
		var zero T
		return zero, err
	}
	return work(c.app.pluginState(), c.app.gitTrusted())
}

// gitSync is a call to the remote, which answers nothing.
func gitSync(call func(s plugins.State, trusted, id, root string) error) func(c *Call, g gitArgs) (nothing, error) {
	return func(c *Call, g gitArgs) (nothing, error) {
		return gitCall(c, g, func(s plugins.State, trusted string) (nothing, error) {
			return nil, call(s, trusted, g.ID, g.Root)
		})
	}
}

func init() {
	command("plugin_git_status", func(c *Call, g gitArgs) (*git.Status, error) {
		return gitCall(c, g, func(s plugins.State, trusted string) (*git.Status, error) {
			return git.GetStatus(s, trusted, g.ID, g.Root)
		})
	})
	command("plugin_git_log", func(c *Call, a struct {
		gitArgs
		Path  string  `json:"path"`
		Limit *uint32 `json:"limit"`
	}) ([]git.Commit, error) {
		return gitCall(c, a.gitArgs, func(s plugins.State, trusted string) ([]git.Commit, error) {
			return git.Log(s, trusted, a.ID, a.Root, a.Path, a.Limit)
		})
	})
	command("plugin_git_show", func(c *Call, a struct {
		gitArgs
		Hash string `json:"hash"`
		Path string `json:"path"`
	}) (*string, error) {
		return gitCall(c, a.gitArgs, func(s plugins.State, trusted string) (*string, error) {
			return git.Show(s, trusted, a.ID, a.Root, a.Hash, a.Path)
		})
	})
	command("plugin_git_commit", func(c *Call, a struct {
		gitArgs
		Message string   `json:"message"`
		Paths   []string `json:"paths"`
	}) (string, error) {
		return gitCall(c, a.gitArgs, func(s plugins.State, trusted string) (string, error) {
			return git.MakeCommit(s, trusted, a.ID, a.Root, a.Message, a.Paths)
		})
	})
	// Takes the remote's branches (git.sync).
	command("plugin_git_fetch", gitSync(git.Fetch))
	// Sends the branch to its remote branch (git.sync).
	command("plugin_git_push", gitSync(git.Push))
	// Takes the remote branch's commits in, undone on a conflict (git.sync).
	command("plugin_git_pull", gitSync(git.Pull))
	// The writer agreed to Git running what the repository of root sets
	// up, hooks included. Asked by the app itself, never on a plugin's word.
	command("git_trust_repository", func(c *Call, a struct {
		Root string `json:"root"`
	}) (nothing, error) {
		return nil, git.Trust(c.app.gitTrusted(), a.Root)
	})
}
