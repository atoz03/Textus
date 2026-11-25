package ssh

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"
)

// The hosts the user's ~/.ssh/config names, for a plugin to offer: each
// Host without a pattern, through Include-d files. Read only; ssh itself
// reads the config again on each connection, with everything it says.

// maxDepth is how deep Include-s are followed.
const maxDepth = 8

// HostsIn are the named hosts in config (and what it includes, from
// sshDir), in order.
func HostsIn(config, sshDir string) []string {
	found := map[string]bool{}
	readConfig(config, sshDir, 0, found)
	hosts := make([]string, 0, len(found))
	for h := range found {
		hosts = append(hosts, h)
	}
	slices.Sort(hosts)
	return hosts
}

func asciiLower(s string) string {
	return strings.Map(func(r rune) rune {
		if 'A' <= r && r <= 'Z' {
			return r + 'a' - 'A'
		}
		return r
	}, s)
}

func readConfig(file, sshDir string, depth int, found map[string]bool) {
	if depth > maxDepth {
		return
	}
	text, err := os.ReadFile(file)
	if err != nil || !utf8.Valid(text) {
		return
	}
	for _, line := range strings.Split(string(text), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#") {
			continue
		}
		words := strings.Fields(line)
		if len(words) == 0 {
			continue
		}
		// Host=name as well as Host name.
		key, inline, _ := strings.Cut(words[0], "=")
		var values []string
		if inline != "" {
			values = append(values, inline)
		}
		values = append(values, words[1:]...)
		switch asciiLower(key) {
		case "host":
			for _, name := range values {
				if !strings.ContainsAny(name, "*?!") && ValidHost(name) {
					found[name] = true
				}
			}
		case "include":
			for _, pattern := range values {
				for _, path := range included(pattern, sshDir) {
					readConfig(path, sshDir, depth+1, found)
				}
			}
		}
	}
}

// included are an Include pattern's files: ~ the home, relative to
// ~/.ssh, a * in the file name matching any run of characters.
func included(pattern, sshDir string) []string {
	var path string
	if rest, ok := strings.CutPrefix(pattern, "~/"); ok {
		path = filepath.Join(filepath.Dir(sshDir), filepath.FromSlash(rest))
	} else if filepath.IsAbs(filepath.FromSlash(pattern)) {
		path = filepath.FromSlash(pattern)
	} else {
		path = filepath.Join(sshDir, filepath.FromSlash(pattern))
	}
	name := filepath.Base(path)
	if !strings.Contains(name, "*") {
		return []string{path}
	}
	dir := filepath.Dir(path)
	parts := strings.Split(name, "*")
	entries, _ := os.ReadDir(dir)
	var out []string
	for _, e := range entries {
		if matches(e.Name(), parts) {
			out = append(out, filepath.Join(dir, e.Name()))
		}
	}
	slices.Sort(out)
	return out
}

func matches(name string, parts []string) bool {
	first, last := parts[0], parts[len(parts)-1]
	if !strings.HasPrefix(name, first) || !strings.HasSuffix(name, last) || len(name) < len(first)+len(last) {
		return false
	}
	rest := name[len(first) : len(name)-len(last)]
	for _, part := range parts[1 : len(parts)-1] {
		at := strings.Index(rest, part)
		if at < 0 {
			return false
		}
		rest = rest[at+len(part):]
	}
	return true
}
