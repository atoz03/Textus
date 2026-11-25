package ai

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"textus/backend/frontmatter"
)

// The project's library and style profile as a request uses them: the
// entries parsed, the ones the text in hand names picked, the section
// they make. A library entry goes to the model only when `always`, or when
// its name or an alias comes up.

const (
	// MaxLibraryChars is the most library text one request carries, in
	// characters (not bytes, so CJK gets as much as English).
	MaxLibraryChars = 8_000
	// MaxStyleChars is the most of a style profile a request carries.
	MaxStyleChars = 6_000
)

// LibraryInclude is when an entry goes with a request.
type LibraryInclude string

const (
	IncludeAlways   LibraryInclude = "always"
	IncludeDetected LibraryInclude = "detected"
	IncludeNever    LibraryInclude = "never"
)

// LibraryEntry is one `.textus/library/*.md` file
// (src/platform/bindings/ai-project.ts LibraryEntry).
type LibraryEntry struct {
	Name    string         `json:"name"`
	Aliases []string       `json:"aliases"`
	Include LibraryInclude `json:"include"`
	// File is the path relative to the project root, for "open this entry".
	File string `json:"file"`
	Body string `json:"body"`
	// Remembered is set on an entry Textus AI wrote, which the Memory tab
	// also lists.
	Remembered *Remembered `json:"remembered"`
}

// ParseList reads `[a, b]` or `a, b` (also with Chinese commas).
func ParseList(value string) []string {
	value = strings.TrimRight(strings.TrimLeft(strings.TrimSpace(value), "["), "]")
	var out []string
	for _, s := range strings.FieldsFunc(value, func(r rune) bool { return r == ',' || r == '，' || r == '、' }) {
		if s = strings.Trim(strings.Trim(strings.TrimSpace(s), `"`), "'"); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func unquoteValue(s string) string { return strings.Trim(strings.Trim(s, `"`), "'") }

// ParseLibraryEntry reads one entry: file is its path in the project, stem
// its name when the front matter gives none.
func ParseLibraryEntry(file, stem, content string) LibraryEntry {
	entry := LibraryEntry{
		Name: stem, Aliases: []string{}, Include: IncludeDetected, File: file,
		Body: strings.TrimSpace(StripHTMLComments(content)), Remembered: RememberedFromFile(content),
	}
	front, ok := frontmatter.Split(content)
	if !ok {
		return entry
	}
	for i, line := range front.Lines {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		switch strings.TrimSpace(key) {
		case "name":
			if value != "" {
				entry.Name = unquoteValue(value)
			}
		case "aliases":
			if value != "" {
				entry.Aliases = append([]string{}, ParseList(value)...)
				continue
			}
			// A block list on the lines below.
			entry.Aliases = []string{}
			for _, l := range front.Lines[i+1:] {
				item, ok := strings.CutPrefix(strings.TrimLeft(l, " \t"), "- ")
				if !ok {
					break
				}
				if item = unquoteValue(strings.TrimSpace(item)); item != "" {
					entry.Aliases = append(entry.Aliases, item)
				}
			}
		case "include":
			switch strings.Trim(value, `"`) {
			case "always":
				entry.Include = IncludeAlways
			case "never":
				entry.Include = IncludeNever
			default:
				entry.Include = IncludeDetected
			}
		}
	}
	entry.Body = strings.TrimSpace(StripHTMLComments(front.Body))
	return entry
}

// LibraryPick is what PickLibrary chose for one request.
type LibraryPick struct {
	Included []*LibraryEntry
	// LeftOut are entries that came up but no longer fit.
	LeftOut []*LibraryEntry
}

// PickLibrary is the entries a request should carry: `always` ones, plus
// `detected` ones named in context, up to MaxLibraryChars.
func PickLibrary(library []LibraryEntry, context string) LibraryPick {
	haystack := strings.ToLower(context)
	var pick LibraryPick
	used := 0
	for i := range library {
		entry := &library[i]
		if entry.Body == "" {
			continue
		}
		hit := false
		switch entry.Include {
		case IncludeAlways:
			hit = true
		case IncludeDetected:
			for _, term := range append([]string{entry.Name}, entry.Aliases...) {
				if utf8.RuneCountInString(term) >= 2 && strings.Contains(haystack, strings.ToLower(term)) {
					hit = true
					break
				}
			}
		}
		if !hit {
			continue
		}
		if size := utf8.RuneCountInString(entry.Body); used+size <= MaxLibraryChars {
			used += size
			pick.Included = append(pick.Included, entry)
		} else {
			pick.LeftOut = append(pick.LeftOut, entry)
		}
	}
	return pick
}

// WritingContextSection is the prompt section for the style profile and
// the relevant library entries, shared by the agent and completion; "" for
// neither. Each header says how the material binds, or the model
// over-applied profile rules and recited entries as exposition.
func WritingContextSection(style *string, entries []*LibraryEntry) string {
	var parts []string
	if style != nil {
		parts = append(parts, "---the author's style profile (.textus/style.md: how this author writes, drafted from their own writing with \"Learn my style\" or written by them; follow it in all the text you write for this project's documents, whatever their language - it shapes that text, never the language you reply in. Its concrete limits - sentence length, words and punctuation to avoid - are rules; its habits apply where they read naturally and never override the request or the sense of the text: a habit of the present tense doesn't turn a plan into a fact, a habit of speaking to \"you\" doesn't turn the author's own account of what they did into the reader's, a length limit doesn't let a sentence drop what it covered, and an expansion still comes out longer)---\n"+*style)
	}
	if len(entries) > 0 {
		var body []string
		for _, e := range entries {
			aka := ""
			if len(e.Aliases) > 0 {
				aka = fmt.Sprintf(" (also: %s)", strings.Join(e.Aliases, ", "))
			}
			body = append(body, fmt.Sprintf("## %s%s\n%s", e.Name, aka, e.Body))
		}
		parts = append(parts, "---project library (.textus/library): established facts about the people, places and terms mentioned here - stay consistent with them, add nothing about them that neither this nor the text gives, and treat them as background: use them as constraints, not as text to recite---\n"+strings.Join(body, "\n\n"))
	}
	return strings.Join(parts, "\n\n")
}

// LibraryUse is which library entries reached Textus AI, for the Memory
// tab's marks: sent or left out, and whether from the last request or from
// the document's text alone (src/platform/bindings/ai-memory.ts LibraryUse).
type LibraryUse struct {
	Included    []string `json:"included"`
	LeftOut     []string `json:"leftOut"`
	LastRequest bool     `json:"lastRequest"`
	Cap         int      `json:"cap"`
}

// UseOf is the marks a pick makes.
func UseOf(pick LibraryPick, lastRequest bool) LibraryUse {
	files := func(entries []*LibraryEntry) []string {
		out := []string{}
		for _, e := range entries {
			out = append(out, e.File)
		}
		return out
	}
	return LibraryUse{Included: files(pick.Included), LeftOut: files(pick.LeftOut), LastRequest: lastRequest, Cap: MaxLibraryChars}
}

// LibraryNames are the library's names and aliases, which
// shared.library-name checks.
func LibraryNames(library []LibraryEntry) []string {
	var names []string
	for _, e := range library {
		names = append(append(names, e.Name), e.Aliases...)
	}
	return names
}
