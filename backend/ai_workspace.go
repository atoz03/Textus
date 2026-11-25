package backend

import (
	"encoding/json/v2"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"textus/backend/ai"
	"textus/backend/ai/rules"
	"textus/backend/frontmatter"
)

// Where a request's workspace comes from: the project folder found for
// the document, and the global and project layers loaded over the built-in
// skills (ai.AgentWorkspace). The global layer is the config folder's
// textus/, laid out as a project's .textus/ is (agent.md, skills/,
// memory/); a project skill shadows a global one of the same name.

const (
	// maxAgentsChars is the most of AGENTS.md sent, in characters.
	maxAgentsChars = 8_000
	// mostBeside and mostTemplates are the most files named beside the
	// document, and in a templates folder.
	mostBeside, mostTemplates = 30, 10
)

// templateFolders hold templates, by the names people give them.
var templateFolders = []string{"templates", "Templates", "_templates", "模板", "範本"}

// loadWorkspace is the merged workspace for a document: the built-in
// skills, then global's layer (global "" for none), then the project's
// .textus/ on top.
func loadWorkspace(global, docPath, rootOverride string) ai.AgentWorkspace {
	ws := ai.AgentWorkspace{
		Instructions: []string{}, Library: []ai.LibraryEntry{}, RootSource: ai.RootNone,
		Conventions: rules.ProjectConventions{Profiles: []string{}, Options: map[string]string{}},
	}
	for _, s := range ai.BuiltinSkills() {
		ws.Skills = append(ws.Skills, s.Clone())
	}
	if global != "" {
		instructions, skills := readLayer(global, ai.SourceGlobal)
		if instructions != nil {
			ws.Instructions = append(ws.Instructions, *instructions)
		}
		for _, s := range skills {
			ws.Skills = ai.PushLayerSkill(ws.Skills, s)
		}
	}
	resolved, found := resolveRoot(docPath, rootOverride)
	picked := strings.TrimSpace(rootOverride)
	info, err := os.Stat(picked)
	chosen := picked != "" && err == nil && info.IsDir()
	switch {
	case !found:
		ws.RootSource = ai.RootNone
	case isTooBroad(resolved):
		ws.RootSource = ai.RootTooBroad
	case chosen:
		ws.RootSource = ai.RootChosen
	default:
		ws.RootSource = ai.RootEnclosingProject
	}
	root := ""
	if found && ws.RootSource != ai.RootTooBroad {
		root = resolved
		ws.Root = &root
		ws.Style = readStyle(root)
		if ws.Style != nil {
			ws.Conventions = rules.ProjectConventionsFromStyle(*ws.Style)
		}
		ws.Library = readLibrary(root)
		ws.AgentsMD = readAgentsMD(root)
		ws.Nearby = nearbyFiles(root, docPath)
		instructions, skills := readLayer(projectDir(root), ai.SourceProject)
		ws.ProjectInstructions = instructions
		// Project skills shadow same-named global and built-in ones.
		for _, s := range skills {
			ws.Skills = ai.PushLayerSkill(ws.Skills, s)
		}
	}
	ws.Memories = memoryDirs{personal: global, project: root}.load()
	return ws
}

// workspace is the workspace for a document as a request sees it,
// Remember automatically included.
func (a *App) workspace(docPath, rootOverride string) ai.AgentWorkspace {
	ws := loadWorkspace(a.globalAgentPath(), docPath, rootOverride)
	ws.Remember = a.readFlag("rememberAutomatically")
	return ws
}

// workspaceCache holds the last workspace a completion or grammar check
// loaded: they fire on every typing pause, one document at a time.
var workspaceCache struct {
	sync.Mutex
	key [2]string
	at  time.Time
	ws  ai.AgentWorkspace
}

// cachedWorkspace is the workspace re-read at most every ten seconds.
func (a *App) cachedWorkspace(docPath, rootOverride string) ai.AgentWorkspace {
	workspaceCache.Lock()
	defer workspaceCache.Unlock()
	key := [2]string{docPath, rootOverride}
	if workspaceCache.key == key && time.Since(workspaceCache.at) < 10*time.Second {
		return workspaceCache.ws
	}
	ws := a.workspace(docPath, rootOverride)
	workspaceCache.key, workspaceCache.at, workspaceCache.ws = key, time.Now(), ws
	return ws
}

// conventionsFor are the conventions for documents in ws: the global
// setting as stored in settings.json, and the style profile's front matter.
func (a *App) conventionsFor(ws *ai.AgentWorkspace) *rules.Conventions {
	conv := &rules.Conventions{Project: ws.Conventions}
	if raw := a.readSetting(rules.SettingKey); raw != nil {
		var v any
		if json.Unmarshal(raw, &v) == nil {
			conv.Setting = v
		}
	}
	return conv
}

// readLayer is one layer: its agent.md and every skill under skills/,
// marked as from source. Missing files or folders are empty layers.
func readLayer(dir string, source ai.SkillSource) (*string, []ai.AgentSkill) {
	var instructions *string
	if raw, err := os.ReadFile(filepath.Join(dir, "agent.md")); err == nil {
		if text := strings.TrimSpace(ai.StripHTMLComments(string(raw))); text != "" {
			instructions = &text
		}
	}
	var skills []ai.AgentSkill
	for _, f := range readSkillFiles(filepath.Join(dir, "skills")) {
		f.skill.Source = source
		ai.MarkOverride(&f.skill)
		skills = append(skills, f.skill)
	}
	return instructions, skills
}

// skillFile is a skill and the file it was read from.
type skillFile struct {
	path  string
	skill ai.AgentSkill
}

// readSkillFiles is every skill file in a skills/ folder, in file-name
// order; files with no instructions are skipped.
func readSkillFiles(dir string) []skillFile {
	var out []skillFile
	for _, path := range frontmatter.MDFiles(dir, true) {
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		skill := ai.ParseSkillFile(strings.TrimSuffix(filepath.Base(path), ".md"), string(raw))
		if skill.Prompt != "" {
			out = append(out, skillFile{path, skill})
		}
	}
	return out
}

// readStyle is the style profile, `.textus/style.md`, comments stripped
// and cut to its first ai.MaxStyleChars.
func readStyle(root string) *string {
	raw, err := os.ReadFile(projectFileIn(root, "style.md"))
	if err != nil {
		return nil
	}
	text := strings.TrimSpace(ai.StripHTMLComments(string(raw)))
	if text == "" {
		return nil
	}
	text = firstChars(text, ai.MaxStyleChars)
	return &text
}

// projectFileIn is `.textus/<rel>` of root.
func projectFileIn(root, rel string) string { return filepath.Join(projectDir(root), rel) }

// firstChars is text cut to its first most characters.
func firstChars(text string, most int) string {
	if utf8.RuneCountInString(text) <= most {
		return text
	}
	return string([]rune(text)[:most])
}

// readLibrary is `.textus/library/*.md`, in file-name order. An entry with
// nothing in it yet is still listed (else a new entry seemed to fail);
// requests leave it out.
func readLibrary(root string) []ai.LibraryEntry {
	entries := []ai.LibraryEntry{}
	for _, path := range frontmatter.MDFiles(filepath.Join(projectDir(root), "library"), true) {
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		stem := strings.TrimSuffix(filepath.Base(path), ".md")
		entries = append(entries, ai.ParseLibraryEntry(workspaceDirName+"/library/"+stem+".md", stem, string(raw)))
	}
	return entries
}

// readAgentsMD is the folder's AGENTS.md, comments taken out.
func readAgentsMD(root string) *string {
	raw, err := os.ReadFile(filepath.Join(root, "AGENTS.md"))
	if err != nil {
		return nil
	}
	text := strings.TrimSpace(ai.StripHTMLComments(string(raw)))
	if text == "" {
		return nil
	}
	text = firstChars(text, maxAgentsChars)
	return &text
}

// nearbyFiles are the text files beside doc and in the folder's templates
// folder, from root, sorted; doc itself left out.
func nearbyFiles(root, doc string) []string {
	var found []string
	if doc != "" {
		found = append(found, textsIn(root, filepath.Dir(doc), doc, mostBeside)...)
	}
	for _, name := range templateFolders {
		dir := filepath.Join(root, name)
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			found = append(found, textsIn(root, dir, doc, mostTemplates)...)
			break
		}
	}
	return slices.Compact(found)
}

func textsIn(root, dir, doc string, most int) []string {
	if _, inside := pathUnder(dir, root); !inside {
		return nil
	}
	entries, _ := os.ReadDir(dir)
	var names []string
	for _, e := range entries {
		path := filepath.Join(dir, e.Name())
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() || path == doc || strings.HasPrefix(e.Name(), ".") || !isAgentTextFile(path) {
			continue
		}
		if rel, err := filepath.Rel(root, path); err == nil {
			names = append(names, filepath.ToSlash(rel))
		}
	}
	slices.Sort(names)
	if len(names) > most {
		names = names[:most]
	}
	return names
}
