package backend

import (
	"bufio"
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
)

// skippedDirs are build output and dependencies: left out of every walk,
// whatever the ignore files say.
var skippedDirs = map[string]bool{
	"node_modules": true, "target": true, "dist": true, "build": true,
	"__pycache__": true, "venv": true, ".venv": true,
}

type limits struct {
	files   int // files kept
	entries int // everything looked at
	depth   int
	time    time.Duration
}

var walkLimits = limits{files: 50_000, entries: 500_000, depth: 24, time: 3 * time.Second}

type walkedFile struct {
	path string
	rel  string // from the walked folder, /-separated
}

type walkResult struct {
	files     []walkedFile // in the sidebar's tree order
	truncated bool         // a limit was hit
}

// walk lists the files under root that keep accepts, as fd finds them:
// hidden files, files the ignore files (.gitignore, .ignore) leave out and
// build output are skipped; links are not followed.
func walk(root string, lim limits, keep func(path string) bool) walkResult {
	deadline := time.Now().Add(lim.time)
	var out walkResult
	visited := 0
	var visit func(dir string, rel []string, rules []*ignoreRules) bool
	visit = func(dir string, rel []string, rules []*ignoreRules) bool {
		if len(rel) >= lim.depth {
			return true
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			return true
		}
		rules = withRulesOf(dir, rules)
		for _, e := range entries {
			visited++
			if visited > lim.entries || time.Now().After(deadline) {
				out.truncated = true
				return false
			}
			name := e.Name()
			if strings.HasPrefix(name, ".") {
				continue
			}
			path := filepath.Join(dir, name)
			isDir := e.IsDir()
			if isDir && skippedDirs[name] {
				continue
			}
			if ignoredBy(rules, path, isDir) {
				continue
			}
			if isDir {
				if !visit(path, append(rel, name), rules) {
					return false
				}
				continue
			}
			if !e.Type().IsRegular() || !keep(path) {
				continue
			}
			if len(out.files) >= lim.files {
				out.truncated = true
				return false
			}
			out.files = append(out.files, walkedFile{path: path, rel: strings.Join(append(rel[:len(rel):len(rel)], name), "/")})
		}
		return true
	}
	visit(root, nil, parentRules(root))
	keys := make(map[string][]byte, len(out.files))
	for _, f := range out.files {
		keys[f.rel] = sortKey(f.rel)
	}
	sort.Slice(out.files, func(i, j int) bool {
		a, b := out.files[i].rel, out.files[j].rel
		if c := bytes.Compare(keys[a], keys[b]); c != 0 {
			return c < 0
		}
		return a < b
	})
	return out
}

// ignoredIn says which entries of dir the walk leaves out by the ignore
// files or as build output (hidden ones aside), for the tree to dim.
func ignoredIn(dir string) func(path string, isDir bool) bool {
	rules := withRulesOf(dir, parentRules(dir))
	return func(path string, isDir bool) bool {
		if isDir && skippedDirs[filepath.Base(path)] {
			return true
		}
		return ignoredBy(rules, path, isDir)
	}
}

// sortKey orders paths as the sidebar's tree: folders before files, case
// folded, digit runs by value.
func sortKey(rel string) []byte {
	key := make([]byte, 0, len(rel)+8)
	parts := strings.Split(rel, "/")
	for i, name := range parts {
		if i < len(parts)-1 {
			key = append(key, 0)
		} else {
			key = append(key, 1)
		}
		rest := name
		for rest != "" {
			if rest[0] >= '0' && rest[0] <= '9' {
				end := strings.IndexFunc(rest, func(r rune) bool { return r < '0' || r > '9' })
				if end < 0 {
					end = len(rest)
				}
				digits := strings.TrimLeft(rest[:end], "0")
				key = append(key, '0', byte(min(len(digits), 255)))
				key = append(key, digits...)
				rest = rest[end:]
				continue
			}
			r, size := rune(rest[0]), 1
			if r >= 0x80 {
				for _, c := range rest {
					r = c
					break
				}
				size = len(string(r))
			}
			key = append(key, string(unicode.ToLower(r))...)
			rest = rest[size:]
		}
		key = append(key, 0)
	}
	return key
}

// modifiedMs is a file's time, in ms since the epoch.
func modifiedMs(info fs.FileInfo) *float64 {
	ms := float64(info.ModTime().UnixMilli())
	return &ms
}

// ignoreRules are the patterns of the ignore files of one folder.
type ignoreRules struct {
	dir      string
	patterns []ignorePattern
}

type ignorePattern struct {
	re      *regexp.Regexp
	negate  bool
	dirOnly bool
	// anchored patterns match the path from dir; the others any name.
	anchored bool
}

// parentRules are the ignore files of the folders above root, outermost
// first.
func parentRules(root string) []*ignoreRules {
	var dirs []string
	for d := filepath.Dir(filepath.Clean(root)); ; d = filepath.Dir(d) {
		dirs = append(dirs, d)
		if filepath.Dir(d) == d {
			break
		}
	}
	var rules []*ignoreRules
	for i := len(dirs) - 1; i >= 0; i-- {
		rules = withRulesOf(dirs[i], rules)
	}
	return rules
}

func withRulesOf(dir string, rules []*ignoreRules) []*ignoreRules {
	r := &ignoreRules{dir: dir}
	for _, name := range []string{".gitignore", ".ignore"} {
		r.patterns = append(r.patterns, readIgnoreFile(filepath.Join(dir, name))...)
	}
	if len(r.patterns) == 0 {
		return rules
	}
	return append(rules[:len(rules):len(rules)], r)
}

func readIgnoreFile(path string) []ignorePattern {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []ignorePattern
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		if p, ok := parseIgnoreLine(scanner.Text()); ok {
			out = append(out, p)
		}
	}
	return out
}

func parseIgnoreLine(line string) (ignorePattern, bool) {
	line = strings.TrimSuffix(line, "\r")
	// Trailing spaces go unless escaped.
	for strings.HasSuffix(line, " ") && !strings.HasSuffix(line, `\ `) {
		line = line[:len(line)-1]
	}
	if line == "" || strings.HasPrefix(line, "#") {
		return ignorePattern{}, false
	}
	var p ignorePattern
	if strings.HasPrefix(line, "!") {
		p.negate = true
		line = line[1:]
	} else if strings.HasPrefix(line, `\!`) || strings.HasPrefix(line, `\#`) {
		line = line[1:]
	}
	if strings.HasSuffix(line, "/") {
		p.dirOnly = true
		line = strings.TrimRight(line, "/")
	}
	if line == "" {
		return ignorePattern{}, false
	}
	if strings.Contains(line, "/") {
		p.anchored = true
		line = strings.TrimPrefix(line, "/")
	}
	re, err := regexp.Compile("^" + globRegexp(line) + "$")
	if err != nil {
		return ignorePattern{}, false
	}
	p.re = re
	return p, true
}

// globRegexp turns a gitignore glob into a regular expression.
func globRegexp(glob string) string {
	var b strings.Builder
	for i := 0; i < len(glob); i++ {
		c := glob[i]
		switch {
		case strings.HasPrefix(glob[i:], "**/"):
			b.WriteString("(?:.*/)?")
			i += 2
		case strings.HasPrefix(glob[i:], "/**") && i+3 == len(glob):
			b.WriteString("/.*")
			i += 2
		case strings.HasPrefix(glob[i:], "**"):
			b.WriteString(".*")
			i++
		case c == '*':
			b.WriteString("[^/]*")
		case c == '?':
			b.WriteString("[^/]")
		case c == '[':
			end := strings.IndexByte(glob[i+1:], ']')
			if end < 0 {
				b.WriteString(`\[`)
				continue
			}
			class := glob[i+1 : i+1+end]
			if strings.HasPrefix(class, "!") {
				class = "^" + class[1:]
			}
			b.WriteString("[" + strings.ReplaceAll(class, `\`, `\\`) + "]")
			i += end + 1
		case c == '\\' && i+1 < len(glob):
			i++
			b.WriteString(regexp.QuoteMeta(string(glob[i])))
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	return b.String()
}

// ignoredBy says whether the rules leave path out: the last pattern that
// matches decides, the deepest folder's last.
func ignoredBy(rules []*ignoreRules, path string, isDir bool) bool {
	ignored := false
	name := filepath.Base(path)
	for _, r := range rules {
		rel, ok := pathUnder(path, r.dir)
		if !ok || len(rel) == 0 {
			continue
		}
		relPath := strings.Join(rel, "/")
		for _, p := range r.patterns {
			if p.dirOnly && !isDir {
				continue
			}
			subject := name
			if p.anchored {
				subject = relPath
			}
			if p.re.MatchString(subject) {
				ignored = !p.negate
			}
		}
	}
	return ignored
}

// fileExt is the lower-case extension of path, without its dot.
func fileExt(path string) string {
	return strings.ToLower(strings.TrimPrefix(filepath.Ext(path), "."))
}

func isMarkdown(path string) bool {
	switch fileExt(path) {
	case "md", "markdown", "mdx":
		return true
	}
	return false
}

// isTextFile: what Textus opens as text, Markdown and plain.
func isTextFile(path string) bool {
	switch fileExt(path) {
	case "md", "markdown", "mdx", "txt", "text":
		return true
	}
	return false
}
