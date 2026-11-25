// Package git is a plugin's view of the open folder's Git history, read
// with the user's own git (desktop): the branch and what changed, a file's
// commits (through renames), the file as a commit had it, a commit of the
// files named, made as the user would make it (hooks, signing), and the
// remote's fetch, push and pull. Every path is checked as a read of the
// folder is; .git itself is never handed over.
package git

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"

	"textus/backend/plugins"
)

const (
	// maxShowBytes is a commit's text at most: a document, not a build's
	// output.
	maxShowBytes = 8 * 1024 * 1024
	// maxChanges are the changed files listed at most.
	maxChanges = 5000
	maxLog     = 500
)

// Change is a changed file (GitChange in
// src/platform/bindings/plugin-access.ts).
type Change struct {
	Path string `json:"path"`
	// State is modified, added, deleted, renamed, untracked or conflicted.
	State string `json:"state"`
	// Staged: in the index already (git add-ed).
	Staged bool `json:"staged"`
}

// Status is the folder's branch and changes (GitStatus in
// src/platform/bindings/plugin-access.ts).
type Status struct {
	// Branch is nil when HEAD is detached.
	Branch *string `json:"branch"`
	// Upstream is the remote branch it follows (origin/main), nil with
	// none.
	Upstream *string `json:"upstream"`
	// Ahead are the commits here the remote branch hasn't, Behind there
	// that this hasn't.
	Ahead   uint32   `json:"ahead"`
	Behind  uint32   `json:"behind"`
	Changes []Change `json:"changes"`
}

// Commit is one commit of a file's history (GitCommit in
// src/platform/bindings/plugin-access.ts).
type Commit struct {
	Hash string `json:"hash"`
	// At is when it was committed, in milliseconds since 1970.
	At      float64 `json:"at"`
	Author  string  `json:"author"`
	Subject string  `json:"subject"`
	// Path is the file's path in this commit (it may have been renamed
	// since).
	Path string `json:"path"`
}

// top is the repository's top folder, "" when root is in none.
func top(root string) string {
	out, err := run(root, []string{"rev-parse", "--show-toplevel"}, nil)
	if err != nil {
		return ""
	}
	real, err := plugins.Canonical(filepath.FromSlash(strings.TrimSpace(lossy(out))))
	if err != nil {
		return ""
	}
	return real
}

// under is path's part below dir, compared name by name.
func under(path, dir string) (string, bool) {
	path, dir = filepath.Clean(path), filepath.Clean(dir)
	if path == dir {
		return "", true
	}
	prefix := dir
	if !strings.HasSuffix(prefix, string(filepath.Separator)) {
		prefix += string(filepath.Separator)
	}
	return strings.CutPrefix(path, prefix)
}

// fromTop is path (checked for plugin id to read) from the repository's
// top, with forward slashes.
func fromTop(s plugins.State, id, root, top, path string) (string, error) {
	file, err := plugins.FolderPath(s, id, "files.read", root, path)
	if err != nil {
		return "", err
	}
	rel, ok := under(file, top)
	if !ok {
		return "", errors.New("The file isn't in the folder's repository.")
	}
	return filepath.ToSlash(rel), nil
}

// names is how a call names the files git gives: under the folder as the
// window names it, which git names by its real path (/private/var for
// /var, a link's target).
type names struct{ root, realRoot string }

func namesOf(root string) names {
	real, _ := plugins.Canonical(root)
	return names{root, real}
}

// shown is a path git gave (from the top) as the plugin sees it, if it may
// read it.
func shown(s plugins.State, id string, n names, top, rel string) (string, bool) {
	if n.realRoot == "" {
		return "", false
	}
	path := joined(top, rel)
	if rest, ok := under(path, n.realRoot); ok {
		path = filepath.Join(n.root, rest)
	}
	if _, err := plugins.FolderPath(s, id, "files.read", n.root, path); err != nil {
		return "", false
	}
	return path, true
}

func changeState(kind, xy string) string {
	switch {
	case kind == "?":
		return "untracked"
	case kind == "2":
		return "renamed"
	case kind == "u":
		return "conflicted"
	case strings.Contains(xy, "A"):
		return "added"
	case strings.Contains(xy, "D"):
		return "deleted"
	}
	return "modified"
}

// count is a number git printed, 0 when it isn't one.
func count(s string) uint32 {
	n, err := strconv.ParseUint(strings.TrimLeft(s, "+-"), 10, 32)
	if err != nil {
		return 0
	}
	return uint32(n)
}

// GetStatus is the branch and the changed files plugin id may read of root;
// nil when the folder is in no repository. store is the list of trusted
// repositories.
func GetStatus(s plugins.State, store, id, root string) (*Status, error) {
	top := top(root)
	if top == "" {
		return nil, nil
	}
	if err := guard(store, top, false); err != nil {
		return nil, err
	}
	n := namesOf(root)
	out, err := run(root, []string{"status", "--porcelain=v2", "-z", "--branch", "--untracked-files=all", "--", "."}, nil)
	if err != nil {
		return nil, err
	}
	status := &Status{Changes: []Change{}}
	entries := strings.Split(lossy(out), "\x00")
	for i := 0; i < len(entries); i++ {
		entry := entries[i]
		if head, ok := strings.CutPrefix(entry, "# branch.head "); ok {
			if head != "(detached)" {
				status.Branch = &head
			}
			continue
		}
		if upstream, ok := strings.CutPrefix(entry, "# branch.upstream "); ok {
			status.Upstream = &upstream
			continue
		}
		// "+2 -1": two to push, one to pull.
		if ab, ok := strings.CutPrefix(entry, "# branch.ab "); ok {
			counts := strings.Split(ab, " ")
			status.Ahead = count(counts[0])
			if len(counts) > 1 {
				status.Behind = count(counts[1])
			}
			continue
		}
		kind, xy := "", ""
		if entry != "" {
			kind = entry[:1]
		}
		if len(entry) >= 4 {
			xy = entry[2:4]
		}
		// Fields before the path: 1 → 8, 2 → 9 (then the old path), u → 10.
		fields := 0
		switch kind {
		case "1":
			fields = 8
		case "2":
			fields = 9
		case "u":
			fields = 10
		case "?":
			fields, xy = 1, "??"
		default:
			continue
		}
		parts := strings.SplitN(entry, " ", fields+1)
		if len(parts) <= fields {
			continue
		}
		if kind == "2" {
			i++
		}
		if len(status.Changes) < maxChanges {
			if path, ok := shown(s, id, n, top, parts[fields]); ok {
				status.Changes = append(status.Changes, Change{
					Path:   path,
					State:  changeState(kind, xy),
					Staged: kind != "?" && !strings.HasPrefix(xy, "."),
				})
			}
		}
	}
	return status, nil
}

// Log is the commits of the file at path, newest first, through renames;
// at most limit (100 when nil, 500 at most).
func Log(s plugins.State, store, id, root, path string, limit *uint32) ([]Commit, error) {
	top := top(root)
	if top == "" {
		return []Commit{}, nil
	}
	if err := guard(store, top, false); err != nil {
		return nil, err
	}
	rel, err := fromTop(s, id, root, top, path)
	if err != nil {
		return nil, err
	}
	n := namesOf(root)
	want := uint32(100)
	if limit != nil {
		want = min(max(*limit, 1), maxLog)
	}
	args := []string{"log", "--follow", "--format=%x1e%H%x1f%ct%x1f%an%x1f%s", "--name-only", "-z", fmt.Sprintf("-n%d", want), "--", rel}
	out, err := run(top, args, nil)
	if err != nil {
		return nil, err
	}
	commits := []Commit{}
	for _, record := range strings.Split(lossy(out), "\x1e") {
		parts := strings.Split(record, "\x00")
		if len(parts) < 2 {
			continue
		}
		head := strings.SplitN(parts[0], "\x1f", 4)
		if len(head) != 4 {
			continue
		}
		at, err := strconv.ParseFloat(head[1], 64)
		if err != nil {
			continue
		}
		// A path the plugin may not read (renamed in from a hidden folder)
		// shows as the file it is now.
		file, ok := shown(s, id, n, top, strings.TrimLeft(parts[1], "\n"))
		if !ok {
			file = path
		}
		commits = append(commits, Commit{Hash: head[0], At: at * 1000, Author: head[2], Subject: head[3], Path: file})
	}
	return commits, nil
}

func isHash(hash string) bool {
	if len(hash) < 4 || len(hash) > 64 {
		return false
	}
	for _, c := range hash {
		if !('0' <= c && c <= '9' || 'a' <= c && c <= 'f' || 'A' <= c && c <= 'F') {
			return false
		}
	}
	return true
}

// Show is the file at path as commit hash had it; nil when it had no such
// file, or it isn't text.
func Show(s plugins.State, store, id, root, hash, path string) (*string, error) {
	if !isHash(hash) {
		return nil, errors.New("A commit is named by its hash.")
	}
	top := top(root)
	if top == "" {
		return nil, nil
	}
	if err := guard(store, top, false); err != nil {
		return nil, err
	}
	rel, err := fromTop(s, id, root, top, path)
	if err != nil {
		return nil, err
	}
	out, err := run(top, []string{"show", hash + ":" + rel}, nil)
	if err != nil {
		return nil, nil
	}
	if len(out) > maxShowBytes {
		return nil, errors.New("The file is too large.")
	}
	if !utf8.Valid(out) {
		return nil, nil
	}
	text := string(out)
	return &text, nil
}

// MakeCommit commits the files at paths (their changes only, whatever else
// is staged) with message, as the user's git does: the new commit's hash.
func MakeCommit(s plugins.State, store, id, root, message string, paths []string) (string, error) {
	if !s.Allows(id, "git.commit") {
		return "", plugins.Refused(id, "git.commit")
	}
	if strings.TrimSpace(message) == "" || len(paths) == 0 {
		return "", errors.New("A commit needs a message and its files.")
	}
	top := top(root)
	if top == "" {
		return "", errors.New("The folder isn't in a Git repository.")
	}
	if err := guard(store, top, true); err != nil {
		return "", err
	}
	// Files, each one named: a folder would bring along everything in it,
	// the hidden files no plugin may read too.
	for _, p := range paths {
		if info, err := os.Stat(p); err == nil && info.IsDir() {
			return "", errors.New("A commit names its files, not a folder.")
		}
	}
	rels := make([]string, 0, len(paths))
	for _, p := range paths {
		rel, err := fromTop(s, id, root, top, p)
		if err != nil {
			return "", err
		}
		rels = append(rels, rel)
	}
	with := func(command ...string) []string { return append(append(command, "--"), rels...) }
	if _, err := run(top, with("add", "--all"), nil); err != nil {
		return "", err
	}
	if _, err := runWithin(top, with("commit", "--file=-", "--only"), &message, commitTime); err != nil {
		return "", err
	}
	head, err := run(top, []string{"rev-parse", "HEAD"}, nil)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(lossy(head)), nil
}
