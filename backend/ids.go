package backend

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"unicode/utf8"
)

// splitPath splits a path into its components, on / and, on Windows, \.
func splitPath(p string) []string {
	return strings.FieldsFunc(p, func(r rune) bool {
		return r == '/' || (runtime.GOOS == "windows" && r == '\\')
	})
}

// pathUnder returns the components of path under dir, comparing whole
// components; false when path is not dir or under it.
func pathUnder(path, dir string) ([]string, bool) {
	p, d := splitPath(path), splitPath(dir)
	if len(p) < len(d) {
		return nil, false
	}
	// The root of an absolute path is a component too.
	if strings.HasPrefix(path, "/") != strings.HasPrefix(dir, "/") {
		return nil, false
	}
	for i := range d {
		if p[i] != d[i] {
			return nil, false
		}
	}
	return p[len(d):], true
}

// movedPath is where path is once from moved to to: path itself was moved,
// or it was under the folder from. False for any other path.
func movedPath(path, from, to string) (string, bool) {
	if path == from {
		return to, true
	}
	rest, ok := pathUnder(path, from)
	if !ok {
		return "", false
	}
	sep := string(filepath.Separator)
	if i := strings.LastIndexAny(to, `/\`); i >= 0 {
		sep = to[i : i+1]
	}
	return strings.TrimRight(to, `/\`) + sep + strings.Join(rest, sep), true
}

// pathKey names a file by path: the first bytes of its SHA-256, in hex.
func pathKey(path string, bytes int) string {
	sum := sha256.Sum256([]byte(path))
	return hex.EncodeToString(sum[:min(bytes, len(sum))])
}

type numberedStyle int

const (
	numberedDash numberedStyle = iota
	numberedParen
	numberedSpace
	numberedSpaceWhole
)

// numberedFor is how a file manager numbers a copy of a file ("plan
// 2.md"), or of a folder after its whole name ("my.photos 2").
func numberedFor(isDir bool) numberedStyle {
	if isDir {
		return numberedSpaceWhole
	}
	return numberedSpace
}

// numbered is name with n in it, as a copy is named: "a-2.png", "a (2).md",
// "a 2.md", or "Folder 2" for a whole name.
func numbered(name string, n int, style numberedStyle) string {
	if n == 0 {
		return name
	}
	if style == numberedSpaceWhole {
		return fmt.Sprintf("%s %d", name, n)
	}
	stem, ext := name, ""
	if i := strings.LastIndex(name, "."); i > 0 {
		stem, ext = name[:i], name[i:]
	}
	switch style {
	case numberedDash:
		return fmt.Sprintf("%s-%d%s", stem, n, ext)
	case numberedParen:
		return fmt.Sprintf("%s (%d)%s", stem, n, ext)
	default:
		return fmt.Sprintf("%s %d%s", stem, n, ext)
	}
}

// freeName is the first of name, then name numbered 2, 3, ... that dir does
// not hold yet.
func freeName(dir, name string, style numberedStyle) string {
	for n := 0; n < 100000; n++ {
		if n == 1 {
			continue
		}
		candidate := numbered(name, n, style)
		if _, err := os.Lstat(filepath.Join(dir, candidate)); os.IsNotExist(err) {
			return candidate
		}
	}
	return name
}

// oneName is name trimmed when it names one entry of a folder: no
// separators, not "." or "..", none of the characters Windows refuses.
func oneName(name string) (string, bool) {
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\`) {
		return "", false
	}
	if runtime.GOOS == "windows" && strings.ContainsAny(name, `<>:"|?*`) {
		return "", false
	}
	return name, true
}

func typedName(name string) (string, error) {
	if n, ok := oneName(name); ok {
		return n, nil
	}
	return "", codeError("badName")
}

// excerptAround is the text around the first term in lower (text in lower
// case), on one line, with an ellipsis where it was cut.
func excerptAround(text, lower, term string) string {
	const before, after = 24, 100
	at := max(strings.Index(lower, term), 0)
	atChar := utf8.RuneCountInString(lower[:at])
	chars := []rune(text)
	start := min(max(atChar-before, 0), len(chars))
	end := min(atChar+utf8.RuneCountInString(term)+after, len(chars))
	if end < start {
		end = start
	}
	out := strings.Join(strings.Fields(string(chars[start:end])), " ")
	if start > 0 {
		out = "…" + out
	}
	if end < len(chars) {
		out += "…"
	}
	return out
}
