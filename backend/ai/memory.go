package ai

import (
	"fmt"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"textus/backend/ai/rules"
)

// What Textus AI remembers by itself: the user's standing requirements,
// preferences and style, one Markdown file each under `memory/` (personal
// or the project's); facts about the work go in the library. A memory is
// named by its id, e.g. `personal/memory/no-semicolons`. The files are
// read and written in backend/ai_memory*.go.

const (
	// MaxMemoryChars is the most memory text sent with one request, in
	// characters (not bytes, so CJK gets as much).
	MaxMemoryChars = 4_000
	// MaxMemoryTextChars: a memory is a sentence or a few, not a document.
	MaxMemoryTextChars = 600
	// memoryMarkKey and memoryMark mark the front matter of what Textus AI
	// wrote.
	memoryMarkKey = "remembered-by"
	memoryMark    = "textus-ai"
)

// MemoryKind is what a memory is about (src/platform/bindings/ai-project.ts
// MemoryKind).
type MemoryKind string

const (
	// Requirement is something the user asked to be always or never done.
	Requirement MemoryKind = "requirement"
	// Preference is what the user likes or dislikes in replies and edits.
	Preference MemoryKind = "preference"
	// StyleMemory is how the user writes.
	StyleMemory MemoryKind = "style"
)

// ParseMemoryKind reads a kind; false for one that isn't.
func ParseMemoryKind(value string) (MemoryKind, bool) {
	switch k := MemoryKind(strings.TrimSpace(value)); k {
	case Requirement, Preference, StyleMemory:
		return k, true
	}
	return "", false
}

// Scope is whose a memory is: the user's in general (the global config
// folder) or this project's.
type Scope string

const (
	ScopeProject  Scope = "project"
	ScopePersonal Scope = "personal"
)

// ParseScope reads a scope; false for one that isn't.
func ParseScope(value string) (Scope, bool) {
	switch s := Scope(strings.TrimSpace(value)); s {
	case ScopeProject, ScopePersonal:
		return s, true
	}
	return "", false
}

// Remembered is where and when something was remembered
// (src/platform/bindings/ai-project.ts Remembered).
type Remembered struct {
	Created string `json:"created"`
	Updated string `json:"updated"`
	// Conversation and Title are the conversation it came from, Document
	// the document open then.
	Conversation *string `json:"conversation"`
	Title        *string `json:"title"`
	Document     *string `json:"document"`
}

// RememberedFromFile is the record in a file Textus AI wrote
// (`remembered-by: textus-ai`); nil for one the user wrote.
func RememberedFromFile(content string) *Remembered {
	fm, _ := SplitFrontmatter(content)
	if fm == nil || strings.TrimSpace(fm.Fields[memoryMarkKey]) != memoryMark {
		return nil
	}
	r := RememberedFromFields(fm.Fields)
	return &r
}

// RememberedFromFields reads the record's front-matter fields.
func RememberedFromFields(fields map[string]string) Remembered {
	get := func(key string) *string {
		if v := strings.TrimSpace(fields[key]); v != "" {
			return &v
		}
		return nil
	}
	r := Remembered{Conversation: get("source-conversation"), Title: get("source-title"), Document: get("source-document")}
	if c := get("created"); c != nil {
		r.Created = *c
	}
	if u := get("updated"); u != nil {
		r.Updated = *u
	} else {
		r.Updated = r.Created
	}
	return r
}

// Lines are the front-matter lines for this record, the mark first.
func (r Remembered) Lines() string {
	out := fmt.Sprintf("%s: %s\ncreated: %s\nupdated: %s\n", memoryMarkKey, memoryMark, r.Created, r.Updated)
	for _, kv := range []struct {
		key   string
		value *string
	}{{"source-conversation", r.Conversation}, {"source-title", r.Title}, {"source-document", r.Document}} {
		if kv.value != nil {
			out += kv.key + ": " + quotedYAML(*kv.value) + "\n"
		}
	}
	return out
}

// quotedYAML is a one-line YAML double-quoted scalar - what
// SplitFrontmatter unquotes.
func quotedYAML(value string) string {
	line := strings.Map(func(c rune) rune {
		if unicode.IsControl(c) {
			return ' '
		}
		return c
	}, value)
	line = strings.ReplaceAll(line, `\`, `\\`)
	return `"` + strings.ReplaceAll(line, `"`, `\"`) + `"`
}

// Memory is one memory, as the Memory tab and the prompt see it.
type Memory struct {
	ID    string     `json:"id"`
	Scope Scope      `json:"scope"`
	Kind  MemoryKind `json:"kind"`
	Text  string     `json:"text"`
	// File is the file's absolute path, for Edit.
	File       string     `json:"file"`
	Remembered Remembered `json:"remembered"`
	// Included: it fits under the budget and so goes with requests.
	Included bool `json:"included"`
}

// Forgotten is one forgotten memory or library entry, for the Memory tab.
type Forgotten struct {
	// ID is `forgotten/<scope>/<folder>/<stem>`, for Restore.
	ID    string `json:"id"`
	Scope Scope  `json:"scope"`
	// Name is a library entry's name; nil for a memory.
	Name *string `json:"name"`
	Text string  `json:"text"`
	// Forgotten is when it was forgotten, as `2026-10-18T09:30:00Z`.
	Forgotten string `json:"forgotten"`
}

// Memories are every memory for a document - personal first, then the
// project's, newest first in each - and how much of the budget the
// included ones use.
type Memories struct {
	Items []Memory `json:"items"`
	// Chars are the characters of the included memories.
	Chars int `json:"chars"`
	Cap   int `json:"cap"`
	// Forgotten is what was deleted or forgotten lately, newest first.
	Forgotten []Forgotten `json:"forgotten"`
}

// NoMemories is the empty set, as JSON wants it: lists, not null.
func NoMemories() Memories {
	return Memories{Items: []Memory{}, Cap: MaxMemoryChars, Forgotten: []Forgotten{}}
}

// memoryWords are the words a text is about, for telling which memories
// bear on it: Latin words of three letters or more, lower-cased, and each
// pair of neighbouring CJK characters (Chinese has no spaces to split on).
func memoryWords(text string) map[string]bool {
	found := map[string]bool{}
	var word strings.Builder
	var previousCJK rune
	flush := func() {
		if utf8.RuneCountInString(word.String()) >= 3 {
			found[word.String()] = true
		}
		word.Reset()
	}
	for _, c := range text {
		cjk := rules.IsCJK(c)
		if cjk {
			if previousCJK != 0 {
				found[string([]rune{previousCJK, c})] = true
			}
			previousCJK = c
		} else {
			previousCJK = 0
		}
		if rules.IsAlphanumeric(c) && !cjk {
			word.WriteString(strings.ToLower(string(c)))
		} else {
			flush()
		}
	}
	flush()
	return found
}

// SelectFor picks what goes with a request about focus (the user's
// message, the document's title), marking Included again: under the budget
// everything; past it requirements always, then those sharing the most
// words with the request, newer first.
func (m *Memories) SelectFor(focus string) {
	total := 0
	for _, item := range m.Items {
		total += utf8.RuneCountInString(item.Text)
	}
	if total <= m.Cap {
		for i := range m.Items {
			m.Items[i].Included = true
		}
		m.Chars = total
		return
	}
	words := memoryWords(focus)
	shared := make([]int, len(m.Items))
	for i, item := range m.Items {
		for w := range memoryWords(item.Text) {
			if words[w] {
				shared[i]++
			}
		}
	}
	order := make([]int, len(m.Items))
	for i := range order {
		order[i] = i
	}
	b2i := func(b bool) int {
		if b {
			return 1
		}
		return 0
	}
	slices.SortStableFunc(order, func(a, b int) int {
		x, y := m.Items[a], m.Items[b]
		if c := b2i(x.Kind != Requirement) - b2i(y.Kind != Requirement); c != 0 {
			return c
		}
		if c := shared[b] - shared[a]; c != 0 {
			return c
		}
		if c := strings.Compare(y.Remembered.Updated, x.Remembered.Updated); c != 0 {
			return c
		}
		return b2i(x.Scope == ScopePersonal) - b2i(y.Scope == ScopePersonal)
	})
	chars := 0
	for i := range m.Items {
		m.Items[i].Included = false
	}
	for _, i := range order {
		if size := utf8.RuneCountInString(m.Items[i].Text); chars+size <= m.Cap {
			chars += size
			m.Items[i].Included = true
		}
	}
	m.Chars = chars
}
