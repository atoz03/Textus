package backend

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// What Textus AI keeps between sessions that isn't a conversation: the
// undecided proposals of each document, in the app data's pending/ (never
// in the user's folder), and the log of revision choices beside the
// personal memories. The contents are the frontend's opaque JSON.

const (
	pendingFolder = "pending" // as docdata.go's pendingFileIn keeps them
	// globalAgentDir is the global layer's folder in the config folder,
	// laid out as a project's .textus/ is (agent.md, skills/, memory/).
	globalAgentDir    = "textus"
	memoryFolder      = "memory"
	choicesFile       = ".revision-choices.json"
	maxChoicesBytes   = 64 << 10
	workspaceDirName  = ".textus"
	keptPictureMaxLen = 16
)

// enclosingProject is the nearest folder from folder upwards that is a
// project: one holding .textus/, or the folder above .textus/ itself (for
// a file inside it). It stops before the home folder and a drive root,
// which are never a project.
func enclosingProject(folder string) (string, bool) {
	for dir := folder; ; dir = filepath.Dir(dir) {
		if filepath.Base(dir) == workspaceDirName {
			return filepath.Dir(dir), true
		}
		if isTooBroad(dir) {
			return "", false
		}
		if info, err := os.Stat(projectDir(dir)); err == nil && info.IsDir() {
			return dir, true
		}
		if filepath.Dir(dir) == dir {
			return "", false
		}
	}
}

// resolveRoot is the folder opened or chosen, else the nearest enclosing
// folder with .textus/, too broad or not. A lone document's own folder is
// not one: it may be Downloads or Desktop, so the agent works in the
// conversation's scratch folder instead.
func resolveRoot(docPath, rootOverride string) (string, bool) {
	if picked := strings.TrimSpace(rootOverride); picked != "" {
		if info, err := os.Stat(picked); err == nil && info.IsDir() {
			return picked, true
		}
	}
	if docPath != "" {
		if dir := filepath.Dir(docPath); dir != "." && dir != "" {
			return enclosingProject(dir)
		}
	}
	return "", false
}

// projectRoot is the project folder a request works in: the resolved
// root, unless it is too broad to treat as one (a home folder, a drive
// root).
func projectRoot(docPath, rootOverride string) (string, bool) {
	root, ok := resolveRoot(docPath, rootOverride)
	return root, ok && !isTooBroad(root)
}

// picturesDir is the folder beside a kept file its pictures are kept in.
func picturesDir(file string) string { return file + ".pictures" }

// keptFile is where a document's undecided proposals are kept. What an
// older Textus kept in the project's .textus/pending/ moves there the
// first time. False for a path that isn't absolute.
func keptFile(docPath, workspaceRoot, data string) (string, bool) {
	if !filepath.IsAbs(docPath) {
		return "", false
	}
	file := pendingFileIn(data, docPath)
	if old, ok := olderKeptFile(docPath, workspaceRoot); ok {
		if info, err := os.Stat(old); err == nil && info.Mode().IsRegular() {
			if _, err := os.Stat(file); errors.Is(err, os.ErrNotExist) {
				os.MkdirAll(filepath.Dir(file), 0o755)
				os.Rename(old, file)
				os.Rename(picturesDir(old), picturesDir(file))
			}
		}
	}
	return file, true
}

// olderKeptFile is where an older Textus kept them: the project's
// .textus/pending/, mirroring the document's place in the project.
func olderKeptFile(doc, workspaceRoot string) (string, bool) {
	root, ok := projectRoot(doc, workspaceRoot)
	if !ok {
		return "", false
	}
	if info, err := os.Stat(projectDir(root)); err != nil || !info.IsDir() {
		return "", false
	}
	// Only the plain names of folders and the file: nothing that climbs out.
	rel, ok := pathUnder(doc, root)
	if !ok || len(rel) == 0 || slices.ContainsFunc(rel, func(p string) bool { return p == "." || p == ".." || p == "" }) {
		return "", false
	}
	return filepath.Join(append([]string{projectDir(root), pendingFolder}, rel...)...) + ".json", true
}

// keptPictureID is a picture id as the image module makes them: lower case
// letters and digits only, so it names a file and nothing else.
func keptPictureID(id string) bool {
	if id == "" || len(id) > keptPictureMaxLen {
		return false
	}
	return !strings.ContainsFunc(id, func(c rune) bool { return !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9') })
}

func (a *App) choicesPath() string {
	return filepath.Join(a.dirs.Config, globalAgentDir, memoryFolder, choicesFile)
}

func init() {
	// The proposals kept for the document, or null when there are none -
	// their pictures waiting again.
	command("kept_proposals_read", func(c *Call, a struct {
		DocPath       string  `json:"docPath"`
		WorkspaceRoot *string `json:"workspaceRoot"`
	}) (jsontext.Value, error) {
		file, ok := keptFile(a.DocPath, deref(a.WorkspaceRoot), c.app.dirs.Data)
		if !ok {
			return jsontext.Value("null"), nil
		}
		raw, err := os.ReadFile(file)
		if err != nil || !jsontext.Value(raw).IsValid() {
			return jsontext.Value("null"), nil
		}
		// Their pictures waiting again, for the frontend to fetch by id.
		restorePictures(picturesDir(file))
		return raw, nil
	})
	// Keeps kept for the document, with the pictures among pictures; null
	// removes what was kept.
	command("kept_proposals_write", func(c *Call, a struct {
		DocPath       string         `json:"docPath"`
		WorkspaceRoot *string        `json:"workspaceRoot"`
		Kept          jsontext.Value `json:"kept"`
		Pictures      []string       `json:"pictures"`
	}) (nothing, error) {
		file, ok := keptFile(a.DocPath, deref(a.WorkspaceRoot), c.app.dirs.Data)
		if !ok {
			return nil, errors.New("not a document path")
		}
		none := len(a.Kept) == 0 || string(a.Kept) == "null"
		ids := a.Pictures
		if none {
			ids = nil
		}
		if err := keepPictures(picturesDir(file), ids); err != nil {
			return nil, err
		}
		if none {
			if err := os.Remove(file); err != nil && !errors.Is(err, os.ErrNotExist) {
				return nil, err
			}
			return nil, nil
		}
		return nil, writeFileAtomic(file, a.Kept)
	})
	command("revision_choices_read", func(c *Call, _ none) (jsontext.Value, error) {
		raw, err := os.ReadFile(c.app.choicesPath())
		if err != nil || jsontext.Value(raw).Kind() != '{' || !jsontext.Value(raw).IsValid() {
			return jsontext.Value("null"), nil
		}
		return raw, nil
	})
	command("revision_choices_write", func(c *Call, a struct {
		Log jsontext.Value `json:"log"`
	}) (nothing, error) {
		var log map[string]jsontext.Value
		if json.Unmarshal(a.Log, &log) != nil || log == nil {
			return nil, errors.New("Not a revision choices log.")
		}
		if len(a.Log) > maxChoicesBytes {
			return nil, errors.New("The revision choices log is too large.")
		}
		return nil, writeFileAtomic(c.app.choicesPath(), a.Log)
	})
}

// deref is *s, "" for none.
func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
