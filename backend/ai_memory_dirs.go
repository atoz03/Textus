package backend

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"textus/backend/ai"
	"textus/backend/frontmatter"
)

// The memory folders on disk: what is remembered read back, found again by
// its id, and what was deleted or forgotten kept a while to bring back (a
// deleted memory or library entry moves into its folder's .forgotten/, and
// the Memory tab lists the newest with Restore).

const (
	libraryFolder   = "library"
	forgottenFolder = ".forgotten"
	// forgottenKept is how many forgotten files each folder keeps.
	forgottenKept = 30
)

// memoryDirs are the folders memories live in for one document: the
// global config folder (personal) and the project folder, either ""
// when missing.
type memoryDirs struct{ personal, project string }

// memoryDirsFor are the memory folders of a document, as the commands
// resolve them.
func (a *App) memoryDirsFor(docPath, workspaceRoot string) memoryDirs {
	root, _ := projectRoot(docPath, workspaceRoot)
	return memoryDirs{personal: a.globalAgentPath(), project: root}
}

// folder is the folder for scope's memories or library; the library is
// the project's only. Memories are Textus's own, kept out of the user's
// folder; the library is the user's, and stays in it.
func (d memoryDirs) folder(scope ai.Scope, folder string) (string, bool) {
	switch {
	case scope == ai.ScopePersonal && folder == memoryFolder && d.personal != "":
		return filepath.Join(d.personal, memoryFolder), true
	case scope == ai.ScopeProject && folder == memoryFolder && d.project != "":
		if d.personal == "" {
			return filepath.Join(projectDir(d.project), memoryFolder), true
		}
		return adoptProjectData(d.personal, d.project, memoryFolder), true
	case scope == ai.ScopeProject && folder == libraryFolder && d.project != "":
		return filepath.Join(projectDir(d.project), libraryFolder), true
	}
	return "", false
}

// located is a memory's file, found from its id.
type located struct {
	scope  ai.Scope
	folder string
	path   string
}

// locate is the file an id (`<scope>/<folder>/<stem>`) names: only ever a
// .md file directly inside one of the memory folders or the project's
// library, and never a link.
func (d memoryDirs) locate(id string) (located, error) {
	unknown := fmt.Errorf("No memory `%s`.", id)
	parts := strings.SplitN(strings.TrimSpace(id), "/", 3)
	if len(parts) < 2 {
		return located{}, unknown
	}
	scope, ok := ai.ParseScope(parts[0])
	if !ok || parts[1] != memoryFolder && parts[1] != libraryFolder {
		return located{}, unknown
	}
	stem := ""
	if len(parts) == 3 {
		stem = parts[2]
	}
	if stem == "" || strings.HasPrefix(stem, ".") || strings.ContainsAny(stem, `/\`) ||
		strings.ContainsFunc(stem, unicode.IsControl) {
		return located{}, unknown
	}
	dir, ok := d.folder(scope, parts[1])
	if !ok {
		return located{}, unknown
	}
	path := filepath.Join(dir, stem+".md")
	if filepath.Dir(path) != filepath.Clean(dir) {
		return located{}, unknown
	}
	if info, err := os.Lstat(path); err == nil && !info.Mode().IsRegular() {
		return located{}, unknown
	}
	return located{scope, parts[1], path}, nil
}

// load is every memory, personal first, with ai.MaxMemoryChars spent on
// the project's newest first (they win a conflict), then the personal
// ones' newest first.
func (d memoryDirs) load() ai.Memories {
	items := append(d.read(ai.ScopePersonal), d.read(ai.ScopeProject)...)
	order := make([]int, len(items))
	for i := range order {
		order[i] = i
	}
	rank := func(m *ai.Memory) int {
		if m.Scope == ai.ScopePersonal {
			return 1
		}
		return 0
	}
	slices.SortStableFunc(order, func(a, b int) int {
		if r := rank(&items[a]) - rank(&items[b]); r != 0 {
			return r
		}
		return strings.Compare(items[b].Remembered.Updated, items[a].Remembered.Updated)
	})
	chars := 0
	for _, i := range order {
		if size := utf8.RuneCountInString(items[i].Text); chars+size <= ai.MaxMemoryChars {
			chars += size
			items[i].Included = true
		}
	}
	return ai.Memories{Items: items, Chars: chars, Cap: ai.MaxMemoryChars, Forgotten: d.forgotten()}
}

// read is one scope's memories, newest first.
func (d memoryDirs) read(scope ai.Scope) []ai.Memory {
	items := []ai.Memory{}
	dir, ok := d.folder(scope, memoryFolder)
	if !ok {
		return items
	}
	for _, path := range frontmatter.MDFiles(dir, false) {
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		fields, body := ai.FieldsAndBody(string(raw))
		text := strings.TrimSpace(ai.StripHTMLComments(body))
		if text == "" {
			continue
		}
		kind, ok := ai.ParseMemoryKind(fields["kind"])
		if !ok {
			kind = ai.Preference
		}
		items = append(items, ai.Memory{
			ID: string(scope) + "/" + memoryFolder + "/" + fileStem(path), Scope: scope, Kind: kind,
			Text: text, File: path, Remembered: ai.RememberedFromFields(fields),
		})
	}
	slices.SortStableFunc(items, func(a, b ai.Memory) int {
		if c := strings.Compare(b.Remembered.Updated, a.Remembered.Updated); c != 0 {
			return c
		}
		return strings.Compare(a.ID, b.ID)
	})
	return items
}

// memoryID is the id of the file at path in scope's folder.
func memoryID(scope ai.Scope, folder, path string) string {
	return string(scope) + "/" + folder + "/" + fileStem(path)
}

// freeMemoryPath is `<dir>/<stem>.md`, or `<stem>-2.md` and on when that
// is taken.
func freeMemoryPath(dir, stem string) string {
	return filepath.Join(dir, freeName(dir, stem+".md", numberedDash))
}

// writeMemoryFile writes a memory or library file whole, its folder made.
func writeMemoryFile(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return writeFileAtomic(path, []byte(content))
}

// memoryTimestamp is a moment as `2026-10-18T09:30:00Z` (UTC).
func memoryTimestamp(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05Z") }

// setAside moves path (a memory or library file) into its folder's
// .forgotten/, and lets the oldest there go past forgottenKept.
func (d memoryDirs) setAside(path string) error {
	aside := filepath.Join(filepath.Dir(path), forgottenFolder)
	if err := os.MkdirAll(aside, 0o755); err != nil {
		return err
	}
	target := freeMemoryPath(aside, fileStem(path))
	if err := os.Rename(path, target); err != nil {
		return err
	}
	// Its modification time is when it was forgotten.
	now := time.Now()
	_ = os.Chtimes(target, now, now)
	kept := forgottenFiles(aside)
	for len(kept) > forgottenKept {
		_ = os.Remove(kept[len(kept)-1].path)
		kept = kept[:len(kept)-1]
	}
	return nil
}

// forgotten are the forgotten memories and library entries, newest first.
func (d memoryDirs) forgotten() []ai.Forgotten {
	type dated struct {
		secs int64
		f    ai.Forgotten
	}
	var all []dated
	for _, where := range []struct {
		scope  ai.Scope
		folder string
	}{{ai.ScopePersonal, memoryFolder}, {ai.ScopeProject, memoryFolder}, {ai.ScopeProject, libraryFolder}} {
		dir, ok := d.folder(where.scope, where.folder)
		if !ok {
			continue
		}
		for _, file := range forgottenFiles(filepath.Join(dir, forgottenFolder)) {
			raw, err := os.ReadFile(file.path)
			if err != nil {
				continue
			}
			fields, body := ai.FieldsAndBody(string(raw))
			var name *string
			if n, ok := fields["name"]; ok && where.folder == libraryFolder {
				name = &n
			}
			all = append(all, dated{file.secs, ai.Forgotten{
				ID:    "forgotten/" + string(where.scope) + "/" + where.folder + "/" + fileStem(file.path),
				Scope: where.scope, Name: name, Text: strings.TrimSpace(ai.StripHTMLComments(body)),
				Forgotten: memoryTimestamp(time.Unix(file.secs, 0)),
			}})
		}
	}
	slices.SortStableFunc(all, func(a, b dated) int { return compareInt64(b.secs, a.secs) })
	out := make([]ai.Forgotten, 0, len(all))
	for _, d := range all {
		out = append(out, d.f)
	}
	return out
}

func compareInt64(a, b int64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// restore puts a forgotten memory or library entry back where it was, and
// answers its new id.
func (d memoryDirs) restore(id string) (string, error) {
	unknown := fmt.Errorf("Nothing forgotten as `%s`.", id)
	rest, ok := strings.CutPrefix(id, "forgotten/")
	if !ok {
		return "", unknown
	}
	loc, err := d.locate(rest)
	if err != nil {
		return "", unknown
	}
	dir, stem := filepath.Dir(loc.path), fileStem(loc.path)
	aside := filepath.Join(dir, forgottenFolder, stem+".md")
	if info, err := os.Lstat(aside); err != nil || !info.Mode().IsRegular() {
		return "", unknown
	}
	target := freeMemoryPath(dir, stem)
	if err := os.Rename(aside, target); err != nil {
		return "", err
	}
	return memoryID(loc.scope, loc.folder, target), nil
}

// dropForgottenCopy removes the forgotten copy of the file at path that
// holds content: Undo of a forget put the file itself back. A second
// forget of the same name was set aside as `name-2`, so the copy is found
// by content.
func (d memoryDirs) dropForgottenCopy(path, content string) {
	stem := fileStem(path)
	ours := func(copy string) bool {
		name := fileStem(copy)
		n, numbered := strings.CutPrefix(name, stem+"-")
		numbered = numbered && n != "" && strings.Trim(n, "0123456789") == ""
		if name != stem && !numbered {
			return false
		}
		raw, err := os.ReadFile(copy)
		return err == nil && string(raw) == content
	}
	for _, file := range forgottenFiles(filepath.Join(filepath.Dir(path), forgottenFolder)) {
		if ours(file.path) {
			_ = os.Remove(file.path)
			return
		}
	}
}

type forgottenFile struct {
	path string
	secs int64
}

// forgottenFiles are the .md files in a .forgotten/ folder with their
// modification times, newest first.
func forgottenFiles(dir string) []forgottenFile {
	var files []forgottenFile
	for _, path := range frontmatter.MDFiles(dir, false) {
		var secs int64
		if info, err := os.Stat(path); err == nil {
			secs = max(info.ModTime().Unix(), 0)
		}
		files = append(files, forgottenFile{path, secs})
	}
	slices.SortStableFunc(files, func(a, b forgottenFile) int {
		if c := compareInt64(b.secs, a.secs); c != 0 {
			return c
		}
		return strings.Compare(a.path, b.path)
	})
	return files
}
