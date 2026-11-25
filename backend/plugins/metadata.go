package plugins

import (
	"path"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"textus/backend/ai"
	"textus/backend/frontmatter"
)

// One document's metadata, read from its text: front matter, headings,
// links, tags, tasks and block ids, outside code and comments. Lines count
// from 0. The shapes are DocumentMeta and its parts in
// src/platform/bindings/plugin-access.ts.

// HeadingMeta is one heading.
type HeadingMeta struct {
	Level int    `json:"level"`
	Text  string `json:"text"`
	Line  int    `json:"line"`
}

// LinkMeta is one link.
type LinkMeta struct {
	// Target is as written: a path, a URL, or a [[wiki link]]'s name.
	Target string `json:"target"`
	Text   string `json:"text"`
	Line   int    `json:"line"`
	Wiki   bool   `json:"wiki"`
}

// TaskMeta is one Markdown task.
type TaskMeta struct {
	Text string `json:"text"`
	Done bool   `json:"done"`
	// Status is the character in its brackets: " ", "x", "-", "/" and the
	// like.
	Status string   `json:"status"`
	Due    *string  `json:"due"`
	Tags   []string `json:"tags"`
	Line   int      `json:"line"`
}

// BlockIDMeta is an Obsidian block id (`^abc123`), the line it marks, and
// that block's first words.
type BlockIDMeta struct {
	ID   string `json:"id"`
	Line int    `json:"line"`
	Text string `json:"text"`
}

// BlockMeta is one block of the document, as the AI's blocks are cut.
type BlockMeta struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`
	Line int    `json:"line"`
}

// DocumentMeta is one document as the index knows it.
type DocumentMeta struct {
	Path  string `json:"path"`
	Rel   string `json:"rel"`
	Title string `json:"title"`
	// Frontmatter is the YAML of its front matter as written; the window
	// reads it.
	Frontmatter *string       `json:"frontmatter"`
	Headings    []HeadingMeta `json:"headings"`
	Links       []LinkMeta    `json:"links"`
	Tags        []string      `json:"tags"`
	Tasks       []TaskMeta    `json:"tasks"`
	BlockIDs    []BlockIDMeta `json:"blockIds"`
	Blocks      *[]BlockMeta  `json:"blocks"`
	ModifiedMs  *float64      `json:"modifiedMs"`
}

// space is what Rust's Unicode \s matched, inside a character class.
const space = `\s\p{Z}\x0B\x{85}`

var (
	headingRe    = regexp.MustCompile(`^[` + space + `]{0,3}(#{1,6})[` + space + `]+(.*?)[` + space + `]*#*[` + space + `]*$`)
	fenceRe      = regexp.MustCompile("^[" + space + "]{0,3}(`{3,}|~{3,})")
	codeSpanRe   = regexp.MustCompile("`+[^`]*`+")
	linkRe       = regexp.MustCompile(`!?\[([^\]\n]*)\]\((<[^>\n]+>|[^)` + space + `]+)`)
	wikiRe       = regexp.MustCompile(`\[\[([^\]\n]+)\]\]`)
	tagRe        = regexp.MustCompile(`(?:^|[` + space + `(])#([\p{L}\p{N}_/-]*[\p{L}_/-][\p{L}\p{N}_/-]*)`)
	taskRe       = regexp.MustCompile(`^[` + space + `]*(?:[-*+]|\p{Nd}+[.)])[` + space + `]+\[([^\]])\][` + space + `]+(.*)$`)
	dueRe        = regexp.MustCompile(`(?:📅[` + space + `]*|due::?[` + space + `]*)(\p{Nd}{4}-\p{Nd}{2}-\p{Nd}{2})`)
	linkTargetRe = regexp.MustCompile(`\]\([^)\n]*\)`)
	blockIDRe    = regexp.MustCompile(`(?:^|[` + space + `])\^([A-Za-z0-9-]+)[` + space + `]*$`)
)

// blockTextChars is how much of a block a block id's entry quotes.
const blockTextChars = 60

// withoutComments is line without its `%%comments%%`, which Obsidian hides
// and doesn't count: open says whether one runs on from an earlier line,
// and is left saying whether one runs on past this one.
func withoutComments(line string, open *bool) string {
	var out strings.Builder
	for i, part := range strings.Split(line, "%%") {
		if i > 0 {
			*open = !*open
		}
		if !*open {
			out.WriteString(part)
		}
	}
	return out.String()
}

// frontMatterOf is text's front matter as YAML and the line the rest
// starts on.
func frontMatterOf(text string) (*string, int) {
	front, ok := frontmatter.Split(text)
	if !ok {
		return nil, 0
	}
	yaml := front.YAML()
	return &yaml, front.BodyLine
}

func tagsIn(text string) []string {
	out := []string{}
	for _, m := range tagRe.FindAllStringSubmatch(text, -1) {
		if tag := strings.TrimRight(m[1], "/"); tag != "" {
			out = append(out, tag)
		}
	}
	return out
}

// firstChars is text's first n characters.
func firstChars(text string, n int) string {
	for i := range text {
		if n == 0 {
			return text[:i]
		}
		n--
	}
	return text
}

// fileStem is a path's last name without its extension, as Rust's
// file_stem: a name that only starts with a dot keeps it.
func fileStem(rel string) string {
	base := path.Base(rel)
	if base == "." || base == "/" {
		return ""
	}
	if i := strings.LastIndexByte(base, '.'); i > 0 {
		return base[:i]
	}
	return base
}

// ParseDocument is what text (the document at path, rel in its folder)
// holds.
func ParseDocument(path, rel, text string) DocumentMeta {
	front, bodyStart := frontMatterOf(text)
	m := DocumentMeta{
		Path: path, Rel: rel, Frontmatter: front, Headings: []HeadingMeta{}, Links: []LinkMeta{},
		Tags: []string{}, Tasks: []TaskMeta{}, BlockIDs: []BlockIDMeta{},
	}
	seenTags := map[string]bool{}
	previousText := ""
	var fence rune
	comment := false
	for i, raw := range strings.Split(text, "\n") {
		if i < bodyStart {
			continue
		}
		rawLine := strings.TrimRight(raw, "\r")
		if f := fenceRe.FindStringSubmatch(rawLine); f != nil {
			c, _ := utf8.DecodeRuneInString(f[1])
			switch fence {
			case 0:
				fence = c
			case c:
				fence = 0
			}
			continue
		}
		if fence != 0 {
			continue
		}
		line := withoutComments(rawLine, &comment)
		if b := blockIDRe.FindStringSubmatchIndex(line); b != nil {
			quoted := strings.TrimSpace(line[:b[0]])
			if quoted == "" {
				quoted = previousText
			}
			m.BlockIDs = append(m.BlockIDs, BlockIDMeta{ID: line[b[2]:b[3]], Line: i, Text: firstChars(quoted, blockTextChars)})
		}
		if t := strings.TrimSpace(line); t != "" {
			previousText = t
		}
		// Code spans hold no links or tags.
		plain := codeSpanRe.ReplaceAllString(line, "")
		if h := headingRe.FindStringSubmatch(plain); h != nil {
			m.Headings = append(m.Headings, HeadingMeta{Level: len(h[1]), Text: h[2], Line: i})
		}
		for _, l := range linkRe.FindAllStringSubmatch(plain, -1) {
			target := strings.TrimRight(strings.TrimLeft(l[2], "<"), ">")
			m.Links = append(m.Links, LinkMeta{Target: target, Text: l[1], Line: i})
		}
		for _, w := range wikiRe.FindAllStringSubmatch(plain, -1) {
			target, label := w[1], w[1]
			// In a table the bar is written `\|`; its backslash isn't the
			// name's.
			if t, l, ok := strings.Cut(w[1], "|"); ok {
				target, label = strings.TrimSuffix(t, `\`), l
			}
			m.Links = append(m.Links, LinkMeta{Target: strings.TrimSpace(target), Text: strings.TrimSpace(label), Line: i, Wiki: true})
		}
		// A heading's # is not a tag; a link's `(#anchor)` is none either.
		var tagText string
		if last := len(m.Headings) - 1; strings.HasPrefix(strings.TrimLeftFunc(plain, unicode.IsSpace), "#") && last >= 0 && m.Headings[last].Line == i {
			tagText = m.Headings[last].Text
		} else {
			tagText = linkTargetRe.ReplaceAllString(plain, "]")
		}
		for _, tag := range tagsIn(tagText) {
			if key := strings.ToLower(tag); !seenTags[key] {
				seenTags[key] = true
				m.Tags = append(m.Tags, tag)
			}
		}
		if t := taskRe.FindStringSubmatch(line); t != nil {
			task := TaskMeta{Text: t[2], Done: t[1] != " ", Status: t[1], Tags: tagsIn(t[2]), Line: i}
			if d := dueRe.FindStringSubmatch(t[2]); d != nil {
				task.Due = &d[1]
			}
			m.Tasks = append(m.Tasks, task)
		}
	}
	if len(m.Headings) > 0 {
		m.Title = m.Headings[0].Text
	} else {
		m.Title = fileStem(rel)
	}
	return m
}

// BlocksOf are text's blocks, with the line each starts on.
func BlocksOf(text string) []BlockMeta {
	doc := ai.DocumentFromMarkdown(text)
	out := make([]BlockMeta, len(doc.Blocks))
	for i, b := range doc.Blocks {
		out[i] = BlockMeta{ID: b.ID, Kind: b.Kind, Line: strings.Count(text[:b.Start], "\n")}
	}
	return out
}

// DocumentText is a document's place and text, as the window read it (an
// Android folder opened without access to all files).
type DocumentText struct {
	Path string `json:"path"`
	Rel  string `json:"rel"`
	Text string `json:"text"`
}

// OfTexts is each document's metadata; with blocks, its blocks too.
func OfTexts(documents []DocumentText, blocks bool) []DocumentMeta {
	out := make([]DocumentMeta, len(documents))
	for i, d := range documents {
		out[i] = ParseDocument(d.Path, d.Rel, d.Text)
		if blocks {
			b := BlocksOf(d.Text)
			out[i].Blocks = &b
		}
	}
	return out
}
