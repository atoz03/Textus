// Package frontmatter finds a document's front matter one way everywhere
// (the frontend's reader takes the same cases: src/utils/frontmatter-cases.json):
// a first line of `---`, closed by `---` or `...`; a byte-order mark, CRLF
// line ends and spaces after the fences allowed.
package frontmatter

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode"
)

// FrontMatter is the front matter of a text and what follows it.
type FrontMatter struct {
	// Lines are its lines between the fences, line ends dropped.
	Lines []string
	// Body is the text after the closing fence, as written.
	Body string
	// BodyLine is the line the body starts on (0-based, in the text
	// without its BOM).
	BodyLine int
}

// YAML is its lines joined with "\n".
func (f FrontMatter) YAML() string { return strings.Join(f.Lines, "\n") }

// WithoutBOM is text without a leading byte-order mark.
func WithoutBOM(text string) string { return strings.TrimPrefix(text, "\uFEFF") }

// Split is the front matter text opens with; false without one, or unclosed.
func Split(text string) (FrontMatter, bool) {
	text = WithoutBOM(text)
	lines := strings.SplitAfter(text, "\n")
	bare := func(line string) string {
		return strings.TrimRightFunc(strings.TrimRight(line, "\r\n"), unicode.IsSpace)
	}
	if len(lines) == 0 || text == "" || bare(lines[0]) != "---" {
		return FrontMatter{}, false
	}
	offset := len(lines[0])
	var header []string
	for i, line := range lines[1:] {
		if line == "" {
			break
		}
		offset += len(line)
		if b := bare(line); b == "---" || b == "..." {
			return FrontMatter{Lines: header, Body: text[offset:], BodyLine: i + 2}, true
		}
		header = append(header, strings.TrimRight(line, "\r\n"))
	}
	return FrontMatter{}, false
}

// MDFiles are the .md files in dir (files only, no folder named so), in
// file-name order; none when it can't be read. With links, a link to a
// file counts too (a skill kept in a dotfiles repo); memory folders take
// none.
func MDFiles(dir string, links bool) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var paths []string
	for _, e := range entries {
		path := filepath.Join(dir, e.Name())
		if filepath.Ext(e.Name()) != ".md" {
			continue
		}
		if links {
			if info, err := os.Stat(path); err != nil || !info.Mode().IsRegular() {
				continue
			}
		} else if !e.Type().IsRegular() {
			continue
		}
		paths = append(paths, path)
	}
	slices.Sort(paths)
	return paths
}
