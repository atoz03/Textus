package git

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"unicode/utf8"
)

// What stands between a folder's own Git settings and a program run on this
// computer. A repository's .git/config can name programs Git starts by
// itself (a filter on `git status`, a file-system monitor, a hook on
// commit), and a folder that came from elsewhere - an archive, a shared or
// synced folder - brings its .git along. Two guards: the settings Git can
// be told to ignore are switched off on every call, and a repository that
// sets any of the rest runs no Git until the writer says they trust it.

// quiet are the settings switched off on every call: none of what Textus
// asks of Git needs them.
var quiet = []string{
	"-c", "core.fsmonitor=false",
	"-c", "safe.bareRepository=explicit",
	"-c", "diff.external=",
	"-c", "core.pager=cat",
	// `ext::` remotes run a command; none of what Textus fetches needs one.
	"-c", "protocol.ext.allow=never",
}

// Untrusted starts the error a call gives in a repository not trusted yet;
// what it would run follows (UNTRUSTED in src/plugins/host/broker-git.ts).
const Untrusted = "git-untrusted: "

// setting is one `key = value` of a Git config file, its section and key
// lowercased (filter.lfs.clean: section "filter", key "clean").
type setting struct{ section, key, value string }

func asciiLower(s string) string {
	return strings.Map(func(r rune) rune {
		if 'A' <= r && r <= 'Z' {
			return r + 'a' - 'A'
		}
		return r
	}, s)
}

func settings(text string) []setting {
	section := ""
	var found []setting
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if head, ok := strings.CutPrefix(line, "["); ok {
			if i := strings.IndexAny(head, " \"]."); i >= 0 {
				head = head[:i]
			}
			section = asciiLower(head)
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			key, value = line, "true"
		}
		found = append(found, setting{section, asciiLower(strings.TrimSpace(key)), strings.TrimSpace(value)})
	}
	return found
}

// runsAProgram is whether a setting makes Git start a program, or read more
// settings from a file of the repository's choosing.
func runsAProgram(section, key, value string) bool {
	switch section {
	case "core":
		switch key {
		case "fsmonitor":
			// The built-in monitor (true) and none (false) run nothing of
			// the repository's.
			return value != "true" && value != "false" && value != ""
		case "hookspath", "sshcommand", "editor", "pager", "askpass", "gitproxy":
			return true
		}
	case "filter":
		return key == "clean" || key == "smudge" || key == "process"
	case "diff":
		return key == "external" || key == "textconv" || key == "command"
	case "merge":
		return key == "driver"
	case "gpg":
		return key == "program"
	case "credential":
		return key == "helper"
	case "remote":
		// Fetch and push start these on a local remote, through a shell.
		return key == "uploadpack" || key == "receivepack"
	case "protocol":
		// protocol.ext.allow (or protocol.allow) lets an `ext::` remote run
		// its command.
		return key == "allow"
	case "sequence":
		return key == "editor"
	case "include", "includeif":
		return key == "path"
	case "alias", "pager":
		return true
	}
	return false
}

// Sources are the repository's own files Git reads settings and hooks
// from.
type Sources struct {
	Configs []string
	Hooks   string
}

// risks is what sources would make Git run, named for the writer: settings
// as section.key, hooks as "hook: name". committing counts the hooks, which
// only a commit runs. Empty for a repository that runs nothing.
func risks(sources Sources, committing bool) []string {
	found := []string{}
	for _, config := range sources.Configs {
		text, err := os.ReadFile(config)
		if err != nil || !utf8.Valid(text) {
			continue
		}
		for _, s := range settings(string(text)) {
			if runsAProgram(s.section, s.key, s.value) {
				found = append(found, s.section+"."+s.key)
			}
		}
	}
	if committing {
		found = append(found, hooksIn(sources.Hooks)...)
	}
	slices.Sort(found)
	return slices.Compact(found)
}

// hooksIn are the hooks a commit would run: the files there that aren't
// Git's samples.
func hooksIn(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var hooks []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !utf8.ValidString(name) || strings.HasSuffix(name, ".sample") || strings.HasPrefix(name, ".") {
			continue
		}
		hooks = append(hooks, "hook: "+name)
	}
	return hooks
}

// trustMu keeps two answers from writing the list at once.
var trustMu sync.Mutex

func trustedList(store string) map[string][]string {
	all := map[string][]string{}
	if text, err := os.ReadFile(store); err == nil {
		if json.Unmarshal(text, &all) != nil {
			all = map[string][]string{}
		}
	}
	return all
}

// trusted is whether the writer trusts the repository at top with what it
// runs: the list keeps, by top folder, what each ran when they said so,
// and one that gains a new setting is asked about again.
func trusted(store, top string, risks []string) bool {
	trustMu.Lock()
	defer trustMu.Unlock()
	known, ok := trustedList(store)[top]
	if !ok {
		return false
	}
	for _, r := range risks {
		if !slices.Contains(known, r) {
			return false
		}
	}
	return true
}

func trust(store, top string, risks []string) error {
	trustMu.Lock()
	defer trustMu.Unlock()
	all := trustedList(store)
	known := all[top]
	if known == nil {
		known = []string{}
	}
	for _, r := range risks {
		if !slices.Contains(known, r) {
			known = append(known, r)
		}
	}
	all[top] = known
	text, err := json.Marshal(all, jsontext.WithIndent("  "), json.Deterministic(true))
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(store), 0o755); err != nil {
		return err
	}
	return os.WriteFile(store, text, 0o644)
}

// guard lets Git work in the repository at top only while nothing there
// starts a program the writer hasn't agreed to (store, the list they keep).
func guard(store, top string, committing bool) error {
	found := risks(sources(top), committing)
	if len(found) == 0 || trusted(store, top, found) {
		return nil
	}
	return errors.New(Untrusted + strings.Join(found, ", "))
}

// sources is where the repository at top keeps its settings and hooks.
func sources(top string) Sources {
	dir := func(flag string) (string, bool) {
		out, err := run(top, []string{"rev-parse", flag}, nil)
		if err != nil {
			return "", false
		}
		return joined(top, strings.TrimSpace(lossy(out))), true
	}
	common, ok := dir("--git-common-dir")
	if !ok {
		common = filepath.Join(top, ".git")
	}
	own, ok := dir("--git-dir")
	if !ok {
		own = common
	}
	return Sources{
		Configs: []string{filepath.Join(common, "config"), filepath.Join(own, "config.worktree")},
		Hooks:   filepath.Join(common, "hooks"),
	}
}

// Trust is the writer agreeing to Git running what the repository of root
// sets up, hooks included: asked by the app itself, never on a plugin's
// word. store is the list of trusted repositories.
func Trust(store, root string) error {
	top := top(root)
	if top == "" {
		return errors.New("The folder isn't in a Git repository.")
	}
	return trust(store, top, risks(sources(top), true))
}
