package backend

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egoist/mygo"

	"textus/backend/ai"
)

// The user's skill files: a built-in customized or reset, a skill written
// in a reply saved, imported, turned off or deleted, its modes set. Each
// lives in a skills/ folder of the global layer or a project's .textus/.

// maxWrittenSkill is the most a skill file written in a reply may be.
const maxWrittenSkill = 64 << 10

// moveToTrash sends what may hold the user's own work (a skill they
// edited) to the Trash; tests replace it.
var moveToTrash = func(path string) error {
	if _, err := os.Lstat(path); err != nil {
		return nil
	}
	return mygo.Shell.TrashItem(path)
}

// skillFileStem is a file stem for a skill named name, which comes from
// whoever wrote the file: `name: ../../x` must not become a path.
func skillFileStem(name string) string { return safeFileStem(name, "skill", 120) }

// skillFilePath is where the skill name is stored in dir, checked to be a
// file directly inside it as a second line of defence.
func skillFilePath(dir, name string) (string, error) {
	path := filepath.Join(dir, skillFileStem(name)+".md")
	if filepath.Dir(path) != filepath.Clean(dir) {
		return "", codeError("skillName")
	}
	return path, nil
}

// onTaken is what writing a new skill file does where its name's file is
// there: write over it (an imported file of that name), write beside it
// as name-2, name-3… (a copy, an offer saved), or refuse with an error
// code.
type onTaken struct {
	replace bool
	refuse  string
}

var (
	takenReplace  = onTaken{replace: true}
	takenNextFree = onTaken{}
)

func takenRefuse(code string) onTaken { return onTaken{refuse: code} }

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// writeSkillFile writes skill name's file in dir (made if missing), in one
// step: text is given the name the file gets.
func writeSkillFile(dir, name string, taken onTaken, text func(chosen string) string) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	chosen := name
	path, err := skillFilePath(dir, name)
	if err != nil {
		return "", err
	}
	switch {
	case taken.replace:
	case taken.refuse != "":
		if exists(path) {
			return "", codeError(taken.refuse)
		}
	default:
		for n := 2; exists(path); n++ {
			chosen = fmt.Sprintf("%s-%d", name, n)
			if path, err = skillFilePath(dir, chosen); err != nil {
				return "", err
			}
		}
	}
	return path, writeAtomic(path, []byte(text(chosen)))
}

// skillNamed is the file holding the skill name in dir.
func skillNamed(dir, name string) (string, bool) {
	for _, f := range readSkillFiles(dir) {
		if f.skill.Name == name {
			return f.path, true
		}
	}
	return "", false
}

// customizeIn copies the built-in name into skillsDir, where it replaces
// the built-in, marked textus-based-on, and answers its path. An existing
// skill of that name is never overwritten: its path is answered.
func customizeIn(skillsDir, name string) (string, error) {
	builtin, ok := ai.FindBuiltin(name)
	raw, okRaw := ai.RawBuiltin(name)
	if !ok || !okRaw {
		return "", codeError("unknownBuiltin")
	}
	if path, ok := skillNamed(skillsDir, builtin.Name); ok {
		return path, nil
	}
	version := "1"
	if builtin.Version != nil {
		version = *builtin.Version
	}
	// A file of that stem holding some other skill keeps its name.
	return writeSkillFile(skillsDir, builtin.Name, takenNextFree, func(string) string {
		return ai.WithBasedOn(raw, builtin.Name+"@"+version)
	})
}

// resetIn deletes the Customize… copies of the built-in name (those with
// textus-based-on); a same-named skill the user wrote is notACopy. It
// answers the deleted paths, so the frontend can close tabs that would
// write them back.
func resetIn(skillsDir, name string) ([]string, error) {
	if _, ok := ai.FindBuiltin(name); !ok {
		return nil, codeError("unknownBuiltin")
	}
	sameName, copies := 0, []string{}
	for _, f := range readSkillFiles(skillsDir) {
		if f.skill.Name != name {
			continue
		}
		sameName++
		if f.skill.BasedOn != nil && strings.HasPrefix(*f.skill.BasedOn, name+"@") {
			copies = append(copies, f.path)
		}
	}
	if len(copies) == 0 && sameName > 0 {
		return nil, codeError("notACopy")
	}
	for _, path := range copies {
		// The user may have edited their copy: to the Trash.
		if err := moveToTrash(path); err != nil {
			return nil, err
		}
	}
	return copies, nil
}

// saveWrittenIn writes a skill file from a reply into skillsDir with
// modes: a new file, or over the one holding the skill of that name
// already. A skill with warnings (ai.SkillWarnings) is saved only once
// confirmed; until then the error is SKILL_NEEDS_CONFIRM and the warnings,
// ","-joined.
func saveWrittenIn(skillsDir, text string, modes []ai.AgentMode, confirmed bool) (string, error) {
	if len(text) > maxWrittenSkill {
		return "", codeError("skillTooLong")
	}
	skill := ai.ParseSkillFile("", text)
	if skill.Name == "" || skill.Prompt == "" {
		return "", codeError("notSkill")
	}
	if len(modes) == 0 {
		return "", codeError("skillNoMode")
	}
	// The description is shown to the model too, in the list of skills.
	if warnings := ai.SkillWarnings(skill.Description+"\n"+skill.Prompt, modes); !confirmed && len(warnings) > 0 {
		return "", errors.New(ai.SkillNeedsConfirm + ":" + strings.Join(warnings, ","))
	}
	if err := os.MkdirAll(skillsDir, 0o755); err != nil {
		return "", err
	}
	path, ok := skillNamed(skillsDir, skill.Name)
	if !ok {
		var err error
		if path, err = skillFilePath(skillsDir, skill.Name); err != nil {
			return "", err
		}
	}
	return path, writeAtomic(path, []byte(ai.WithMetadata(text, "modes", modeWords(modes))))
}

func modeWords(modes []ai.AgentMode) string {
	words := make([]string, len(modes))
	for i, m := range modes {
		words[i] = m.String()
	}
	return strings.Join(words, " ")
}

// rewriteSkill sets one metadata key in the file of the skill name in
// skillsDir.
func rewriteSkill(skillsDir, name, key, value string) error {
	path, ok := skillNamed(skillsDir, name)
	if !ok {
		return fmt.Errorf("No skill called %s.", name)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return writeAtomic(path, []byte(ai.WithMetadata(string(raw), key, value)))
}

// setModesIn rewrites the modes of the skill name in skillsDir.
func setModesIn(skillsDir, name string, modes []ai.AgentMode) error {
	modes = slices.Clone(modes)
	slices.Sort(modes)
	modes = slices.Compact(modes)
	if len(modes) == 0 {
		return codeError("skillNoMode")
	}
	return rewriteSkill(skillsDir, name, "modes", modeWords(modes))
}

// setOffIn turns the skill name in skillsDir off, or on again.
func setOffIn(skillsDir, name string, off bool) error {
	return rewriteSkill(skillsDir, name, "off", fmt.Sprint(off))
}

// skillFilesNamed are the files of the user's skill name in skillsDir, for
// Delete: every file of that name, as a stray second copy would come back
// otherwise.
func skillFilesNamed(skillsDir, name string) ([]string, error) {
	var files []string
	for _, f := range readSkillFiles(skillsDir) {
		if f.skill.Name == name {
			files = append(files, f.path)
		}
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("No skill called %s.", name)
	}
	return files, nil
}

// ensureGlobalLayer makes the global config folder with its starter files
// and answers it. template is the starter agent.md, written only when there
// is none: all HTML comments, which never reach the model.
func (a *App) ensureGlobalLayer(template string) (string, error) {
	dir := a.globalAgentPath()
	if err := os.MkdirAll(filepath.Join(dir, "skills"), 0o755); err != nil {
		return "", err
	}
	agentMD := filepath.Join(dir, "agent.md")
	if !exists(agentMD) {
		if err := writeAtomic(agentMD, []byte(template)); err != nil {
			return "", err
		}
	}
	return dir, nil
}

// skillsDir is the skills folder of a user's skills from source: every
// document's, or the project's of the document or open folder. A built-in
// has none.
func (a *App) skillsDir(source ai.SkillSource, docPath, workspaceRoot string) (string, error) {
	switch source {
	case ai.SourceGlobal:
		return filepath.Join(a.globalAgentPath(), "skills"), nil
	case ai.SourceProject:
		root, ok := projectRoot(docPath, workspaceRoot)
		if !ok {
			return "", errors.New("This document has no project folder.")
		}
		return filepath.Join(projectDir(root), "skills"), nil
	}
	return "", codeError("unknownBuiltin")
}

// skillChange are the arguments that name a user's skill.
type skillChange struct {
	Name          string         `json:"name"`
	Source        ai.SkillSource `json:"source"`
	DocPath       *string        `json:"docPath"`
	WorkspaceRoot *string        `json:"workspaceRoot"`
}

func init() {
	// The workspace as the frontend needs it: skills, the project folder,
	// style profile, library and memories, read fresh so edits show.
	command("load_agent_workspace", func(c *Call, a struct {
		DocPath      *string `json:"docPath"`
		RootOverride *string `json:"rootOverride"`
	}) (ai.AgentWorkspace, error) {
		return c.app.workspace(deref(a.DocPath), deref(a.RootOverride)), nil
	})
	// Every skill for a document as the `/` menu and Settings list it,
	// hidden ones left out; with neither argument (Settings), the built-in
	// and global skills.
	command("list_skills", func(c *Call, a struct {
		DocPath       *string `json:"docPath"`
		WorkspaceRoot *string `json:"workspaceRoot"`
	}) ([]ai.AgentSkill, error) {
		return ai.ListedSkills(c.app.workspace(deref(a.DocPath), deref(a.WorkspaceRoot)).Skills), nil
	})
	// The global folder made if need be, and its agent.md's path, which the
	// frontend opens in the editor.
	command("ensure_global_agent_md", func(c *Call, a struct {
		Template string `json:"template"`
	}) (string, error) {
		dir, err := c.app.ensureGlobalLayer(a.Template)
		return filepath.Join(dir, "agent.md"), err
	})
	// The global folder made if need be, and shown in the file manager.
	command("open_global_agent_dir", func(c *Call, a struct {
		Template string `json:"template"`
	}) (nothing, error) {
		dir, err := c.app.ensureGlobalLayer(a.Template)
		if err != nil {
			return nil, err
		}
		return nil, mygo.Shell.OpenPath(dir)
	})
	// A picked .md skill file copied into the global skills, under its
	// skill's name, so importing again replaces the old copy; the global
	// layer's skills after, or null when the user cancels.
	command("import_agent_skill", func(c *Call, _ none) (*[]ai.AgentSkill, error) {
		picked, err := pickFile(c, []filter{{"Markdown", []string{"md"}}})
		if err != nil || picked == nil {
			return nil, err
		}
		raw, err := os.ReadFile(*picked)
		if err != nil {
			return nil, err
		}
		skill := ai.ParseSkillFile(fileStem(*picked), string(raw))
		if skill.Prompt == "" {
			return nil, errors.New("That file has no skill content.")
		}
		global := c.app.globalAgentPath()
		if _, err := writeSkillFile(filepath.Join(global, "skills"), skill.Name, takenReplace, func(string) string { return string(raw) }); err != nil {
			return nil, err
		}
		_, skills := readLayer(global, ai.SourceGlobal)
		if skills == nil {
			skills = []ai.AgentSkill{}
		}
		return &skills, nil
	})
	// Customize…: a built-in copied into the global skills, its path.
	command("customize_builtin_skill", func(c *Call, a struct {
		Name string `json:"name"`
	}) (string, error) {
		return customizeIn(filepath.Join(c.app.globalAgentPath(), "skills"), a.Name)
	})
	// Reset to built-in: the Customize… copies deleted, their paths.
	command("reset_builtin_skill", func(c *Call, a struct {
		Name string `json:"name"`
	}) ([]string, error) {
		return resetIn(filepath.Join(c.app.globalAgentPath(), "skills"), a.Name)
	})
	// The panel modes a user's skill applies in; a built-in's are fixed.
	command("set_skill_modes", func(c *Call, a struct {
		skillChange
		Modes []ai.AgentMode `json:"modes"`
	}) (nothing, error) {
		dir, err := c.app.skillsDir(a.Source, deref(a.DocPath), deref(a.WorkspaceRoot))
		if err != nil {
			return nil, err
		}
		return nil, setModesIn(dir, a.Name, a.Modes)
	})
	// A user's skill turned off for now, or on again; a built-in is
	// always on.
	command("set_skill_off", func(c *Call, a struct {
		skillChange
		Off bool `json:"off"`
	}) (nothing, error) {
		dir, err := c.app.skillsDir(a.Source, deref(a.DocPath), deref(a.WorkspaceRoot))
		if err != nil {
			return nil, err
		}
		return nil, setOffIn(dir, a.Name, a.Off)
	})
	// A skill a Create reply wrote, saved into the global skills with the
	// modes picked, over an earlier version; its path.
	command("save_written_skill", func(c *Call, a struct {
		Text      string         `json:"text"`
		Modes     []ai.AgentMode `json:"modes"`
		Confirmed bool           `json:"confirmed"`
	}) (string, error) {
		return saveWrittenIn(filepath.Join(c.app.globalAgentPath(), "skills"), a.Text, a.Modes, a.Confirmed)
	})
	// One of the user's global skills deleted, to the Trash; the deleted
	// paths.
	command("delete_global_skill", func(c *Call, a struct {
		Name string `json:"name"`
	}) ([]string, error) {
		files, err := skillFilesNamed(filepath.Join(c.app.globalAgentPath(), "skills"), a.Name)
		if err != nil {
			return nil, err
		}
		for _, f := range files {
			if err := moveToTrash(f); err != nil {
				return nil, err
			}
		}
		return files, nil
	})
}
