package backend

import (
	"encoding/json/jsontext"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// A folder's conventions: how the app that keeps it links, where its
// pictures and new notes go. Read from Textus's own .textus/settings.json
// and from an Obsidian vault's settings; the readers of the other apps the
// Tauri build knew (Logseq, Dendron, Foam, VS Code, MkDocs, Hugo, Docusaurus,
// Jekyll, Quarto, git) and plugins' readers are not ported yet.

type Setting[T any] struct {
	Value T      `json:"value"`
	App   string `json:"app"`
	Root  string `json:"root"`
}

type Source struct {
	App   string `json:"app"`
	Root  string `json:"root"`
	Notes bool   `json:"notes"`
}

type Daily struct {
	Folder   string  `json:"folder"`
	Format   string  `json:"format"`
	Template *string `json:"template"`
}

type TemplateFormats struct {
	Date *string `json:"date"`
	Time *string `json:"time"`
}

type Conventions struct {
	Sources         []Source                  `json:"sources"`
	LinkStyle       *Setting[string]          `json:"linkStyle"`
	LinkPath        *Setting[string]          `json:"linkPath"`
	LinkRoot        *Setting[string]          `json:"linkRoot"`
	UpdateLinks     *Setting[string]          `json:"updateLinks"`
	Attachments     *Setting[string]          `json:"attachments"`
	SoftBreaks      *Setting[bool]            `json:"softBreaks"`
	Templates       *Setting[string]          `json:"templates"`
	TemplateFormats *Setting[TemplateFormats] `json:"templateFormats"`
	Daily           *Setting[Daily]           `json:"daily"`
	Weekly          *Setting[Daily]           `json:"weekly"`
	Monthly         *Setting[Daily]           `json:"monthly"`
	NewNotes        *Setting[string]          `json:"newNotes"`
	Ignore          []string                  `json:"ignore"`
	Detected        *Conventions              `json:"detected"`
	decided         bool
}

func newConventions() Conventions { return Conventions{Sources: []Source{}, Ignore: []string{}} }

// or fills what c leaves unset from other, a farther or lesser source.
func (c Conventions) or(o Conventions) Conventions {
	if c.LinkStyle == nil {
		c.LinkStyle = o.LinkStyle
	}
	if c.LinkPath == nil {
		c.LinkPath = o.LinkPath
	}
	if c.LinkRoot == nil {
		c.LinkRoot = o.LinkRoot
	}
	if c.UpdateLinks == nil {
		c.UpdateLinks = o.UpdateLinks
	}
	if c.Attachments == nil {
		c.Attachments = o.Attachments
	}
	if c.SoftBreaks == nil {
		c.SoftBreaks = o.SoftBreaks
	}
	if c.Templates == nil {
		c.Templates = o.Templates
	}
	if c.TemplateFormats == nil {
		c.TemplateFormats = o.TemplateFormats
	}
	if c.Daily == nil {
		c.Daily = o.Daily
	}
	if c.Weekly == nil {
		c.Weekly = o.Weekly
	}
	if c.Monthly == nil {
		c.Monthly = o.Monthly
	}
	if c.NewNotes == nil {
		c.NewNotes = o.NewNotes
	}
	if len(c.Ignore) == 0 {
		c.Ignore = o.Ignore
	}
	c.decided = c.decided || o.decided
	if c.Detected == nil {
		c.Detected = o.Detected
	}
	c.Sources = append(c.Sources, o.Sources...)
	return c
}

// notesRoot is the folder of the nearest app that keeps notes linked by
// name, where a wikilink is looked for.
func (c Conventions) notesRoot() string {
	for _, s := range c.Sources {
		if s.Notes {
			return s.Root
		}
	}
	if c.LinkStyle != nil {
		return c.LinkStyle.Root
	}
	return ""
}

func (c Conventions) hasSettings() bool {
	return c.LinkStyle != nil || c.LinkPath != nil || c.Attachments != nil || c.SoftBreaks != nil ||
		c.Templates != nil || c.Daily != nil || c.NewNotes != nil
}

type at struct{ app, root string }

func set[T any](a at, v T) *Setting[T] { return &Setting[T]{Value: v, App: a.app, Root: a.root} }

func (a at) folder(rel string) string {
	rel = strings.Trim(strings.TrimSpace(rel), "/")
	if rel == "" || rel == "." {
		return a.root
	}
	return filepath.Join(a.root, rel)
}

func (a at) conventions() Conventions {
	c := newConventions()
	notes := a.app == "obsidian" || a.app == "logseq" || a.app == "dendron" || a.app == "foam"
	c.Sources = []Source{{App: a.app, Root: a.root, Notes: notes}}
	return c
}

func readTextus(dir string) (Conventions, bool) {
	settings := readJSONObject(filepath.Join(dir, ".textus", "settings.json"))
	c := docObj(settings, "conventions")
	if c == nil {
		return Conventions{}, false
	}
	a := at{"textus", dir}
	out := a.conventions()
	if v := docStr(c, "linkStyle"); v != nil {
		out.LinkStyle = set(a, *v)
	}
	if v := docStr(c, "linkPath"); v != nil {
		out.LinkPath = set(a, *v)
	}
	if v := docStr(c, "linkRoot"); v != nil {
		out.LinkRoot = set(a, a.folder(*v))
	}
	if v := docStr(c, "updateLinks"); v != nil {
		out.UpdateLinks = set(a, *v)
	}
	if v := docStr(c, "attachments"); v != nil {
		out.Attachments = set(a, *v)
	}
	if v := docBool(c, "softBreaks"); v != nil {
		out.SoftBreaks = set(a, *v)
	}
	if v := docStr(c, "templates"); v != nil {
		out.Templates = set(a, a.folder(*v))
	}
	if v := docStr(c, "newNotes"); v != nil {
		out.NewNotes = set(a, *v)
	}
	if d := docObj(c, "daily"); d != nil {
		daily := Daily{Folder: a.folder(or(docStr(d, "folder"), "")), Format: or(docStr(d, "format"), "YYYY-MM-DD")}
		if t := docStr(d, "template"); t != nil {
			f := a.folder(*t)
			daily.Template = &f
		}
		out.Daily = set(a, daily)
	}
	out.Ignore = docStrings(c, "ignore")
	out.decided = docStr(c, "detected") != nil
	return out, true
}

func readObsidian(dir string) (Conventions, bool) {
	config := filepath.Join(dir, ".obsidian")
	if info, err := os.Stat(config); err != nil || !info.IsDir() {
		return Conventions{}, false
	}
	a := at{"obsidian", dir}
	app := readJSONObject(filepath.Join(config, "app.json"))
	out := a.conventions()
	style := "wiki"
	if v := docBool(app, "useMarkdownLinks"); v != nil && *v {
		style = "markdown"
	}
	out.LinkStyle = set(a, style)
	out.LinkPath = set(a, or(docStr(app, "newLinkFormat"), "shortest"))
	out.Attachments = set(a, or(docStr(app, "attachmentFolderPath"), "/"))
	strict := docBool(app, "strictLineBreaks")
	out.SoftBreaks = set(a, strict == nil || !*strict)
	update := "always"
	if v := docBool(app, "alwaysUpdateLinks"); v != nil && !*v {
		update = "ask"
	}
	out.UpdateLinks = set(a, update)
	switch or(docStr(app, "newFileLocation"), "") {
	case "current":
		out.NewNotes = set(a, "./")
	case "folder":
		out.NewNotes = set(a, or(docStr(app, "newFileFolderPath"), ""))
	default:
		out.NewNotes = set(a, "/")
	}
	out.Ignore = docStrings(app, "userIgnoreFilters")
	templates := readJSONObject(filepath.Join(config, "templates.json"))
	if f := docStr(templates, "folder"); f != nil && strings.TrimSpace(*f) != "" {
		out.Templates = set(a, a.folder(*f))
	}
	daily := readJSONObject(filepath.Join(config, "daily-notes.json"))
	d := Daily{Folder: a.folder(or(docStr(daily, "folder"), "")), Format: "YYYY-MM-DD"}
	if f := docStr(daily, "format"); f != nil && *f != "" {
		d.Format = *f
	}
	if t := docStr(daily, "template"); t != nil && *t != "" {
		name := *t
		if !isMarkdown(name) {
			name += ".md"
		}
		f := a.folder(name)
		d.Template = &f
	}
	out.Daily = set(a, d)
	return out, true
}

var conventionReaders = []func(dir string) (Conventions, bool){readTextus, readObsidian}

const maxAscent = 32

func readFolderConventions(dir string) Conventions {
	ours, theirs := newConventions(), newConventions()
	d := dir
	for range maxAscent {
		for _, read := range conventionReaders {
			found, ok := read(d)
			if !ok {
				continue
			}
			if len(found.Sources) > 0 && found.Sources[0].App == "textus" {
				ours = ours.or(found)
			} else {
				if ours.LinkRoot == nil {
					ours.LinkRoot = found.LinkRoot
				}
				ours.Sources = append(ours.Sources, found.Sources...)
				theirs = theirs.or(found)
			}
		}
		parent := filepath.Dir(d)
		if parent == d {
			break
		}
		d = parent
	}
	theirs.LinkRoot = nil
	if !ours.decided && theirs.hasSettings() {
		ours.Detected = &theirs
	}
	return ours
}

var conventionsCache = struct {
	sync.Mutex
	m map[string]cachedConventions
}{m: map[string]cachedConventions{}}

type cachedConventions struct {
	at time.Time
	c  Conventions
}

// conventionsOfFolder caches what a folder's readers found for a moment:
// a document asks several times as it opens.
func conventionsOfFolder(dir string) Conventions {
	conventionsCache.Lock()
	if hit, ok := conventionsCache.m[dir]; ok && time.Since(hit.at) < 3*time.Second {
		conventionsCache.Unlock()
		return hit.c
	}
	conventionsCache.Unlock()
	found := readFolderConventions(dir)
	conventionsCache.Lock()
	if len(conventionsCache.m) > 64 {
		conventionsCache.m = map[string]cachedConventions{}
	}
	conventionsCache.m[dir] = cachedConventions{time.Now(), found}
	conventionsCache.Unlock()
	return found
}

// frontMatterValue is one key of a document's front matter, looked for in
// its first 8 KiB; a front matter that does not close there has none.
func frontMatterValue(docPath, key string) (string, bool) {
	f, err := os.Open(docPath)
	if err != nil {
		return "", false
	}
	defer f.Close()
	head := make([]byte, 8*1024)
	n, _ := io.ReadFull(f, head)
	text := strings.TrimPrefix(strings.ToValidUTF8(string(head[:n]), "\uFFFD"), "\uFEFF")
	lines := strings.SplitAfter(text, "\n")
	bare := func(l string) string { return strings.TrimRight(l, " \t\r\n") }
	if len(lines) == 0 || bare(lines[0]) != "---" {
		return "", false
	}
	var header []string
	closed := false
	for _, line := range lines[1:] {
		if b := bare(line); b == "---" || b == "..." {
			closed = true
			break
		}
		header = append(header, strings.TrimRight(line, "\r\n"))
	}
	if !closed {
		return "", false
	}
	for _, line := range header {
		rest, ok := strings.CutPrefix(line, key)
		if !ok {
			continue
		}
		value, ok := strings.CutPrefix(rest, ":")
		if !ok {
			continue
		}
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		return value, value != ""
	}
	return "", false
}

func conventionsOfDocument(doc string) Conventions {
	dir := filepath.Dir(doc)
	folder := conventionsOfFolder(dir)
	own := newConventions()
	typora := at{"typora", dir}
	if root, ok := frontMatterValue(doc, "typora-root-url"); ok {
		full := filepath.Join(dir, root)
		if info, err := os.Stat(full); err == nil && info.IsDir() {
			own.LinkRoot = set(typora, full)
		}
	}
	if to, ok := frontMatterValue(doc, "typora-copy-images-to"); ok && to != "upload" {
		if !filepath.IsAbs(to) {
			to = "./" + to
		}
		own.Attachments = set(typora, to)
	}
	return own.or(folder)
}

func conventionsOfPath(path string) Conventions {
	if info, err := os.Stat(path); err == nil && info.IsDir() {
		return conventionsOfFolder(path)
	}
	return conventionsOfDocument(path)
}

func init() {
	command("folder_conventions", func(c *Call, a struct {
		Path string `json:"path"`
	}) (Conventions, error) {
		if err := requireNonEmpty(a.Path); err != nil {
			return Conventions{}, err
		}
		return conventionsOfPath(a.Path), nil
	})
	command("project_settings_read", func(c *Call, a struct {
		Root string `json:"root"`
	}) (jsontext.Value, error) {
		file := filepath.Join(a.Root, ".textus", "settings.json")
		if doc := readJSONObject(file); doc != nil {
			return mustJSON(doc), nil
		}
		raw, err := os.ReadFile(file)
		if err == nil && strings.TrimSpace(string(raw)) != "" {
			return jsontext.Value("null"), nil // there, but not readable: not taken as empty
		}
		return jsontext.Value("{}"), nil
	})
	// Plugins' readers of other apps' settings come with the plugin system,
	// which this build does not have: none is ever pending.
	command("conventions_readers", func(c *Call, _ none) (nothing, error) { return nil, nil })
	command("conventions_pending", func(c *Call, _ none) ([]struct{}, error) { return []struct{}{}, nil })
	command("conventions_answer", func(c *Call, _ none) (nothing, error) { return nil, nil })
}
