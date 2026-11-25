package paper

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// A paper kept with its project's data in Textus's config folder,
// `papers/<id>/` - or in the scratch space while there is no project - so
// a stopped run can resume and the user can read what the research found.

const (
	papersFolder = "papers"
	paperFile    = "paper.json"
	dossierFile  = "dossier.json"
	outlineFile  = "outline.json"
	// researchFile is the research while it is under way: its questions and
	// what each helper that finished found, so a stop mid-research costs
	// only the questions still open.
	researchFile = "research.json"
	// filesFolder holds the paper's attachments, as `files/attached/...`.
	filesFolder   = "files"
	materialsFile = "materials.json"
	// Attached is the folder of a conversation's - and a paper's -
	// attached files, as the file tools address them (`attached/...`).
	Attached = "attached"
)

// PapersFolder is the folder of a project's data its papers are kept in.
const PapersFolder = papersFolder

// Files is everything kept for one paper. A paper not yet researched has
// an empty dossier; one not yet outlined, no outline.
type Files struct {
	Paper   Paper
	Dossier Dossier
	Outline *Outline
}

// NewID is a new paper's id: lowercase hex of the time, so ids sort by
// age.
func NewID() string { return strconv.FormatInt(time.Now().UnixNano(), 16) }

// validID: letters and digits only, so an id names one folder under
// papers/ and can't climb out of it.
func validID(id string) bool {
	if id == "" || len(id) > 32 {
		return false
	}
	for _, c := range id {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}

// Dir is `<base>/papers/<id>/`.
func Dir(base, id string) (string, error) {
	if !validID(id) {
		return "", fmt.Errorf("Not a paper id: %q", id)
	}
	return filepath.Join(base, papersFolder, id), nil
}

// RootIn is the folder a paper is kept under: project, or scratch while
// there is no project ("" for none). A paper id begun in scratch moves to
// the project the first time it is asked for there.
func RootIn(scratch, project, id string) (string, error) {
	if project == "" {
		return scratch, nil
	}
	if id != "" {
		from, err := Dir(scratch, id)
		if err != nil {
			return "", err
		}
		to, err := Dir(project, id)
		if err != nil {
			return "", err
		}
		if isDir(from) && !exists(to) {
			if err := moveOrCopy(from, to); err != nil {
				return "", err
			}
		}
	}
	return project, nil
}

// Save writes the paper's files under root, each whole or not at all, so
// a crash mid-save leaves the previous version.
func Save(root string, files Files) error {
	dir, err := Dir(root, files.Paper.ID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := writeJSON(filepath.Join(dir, paperFile), files.Paper); err != nil {
		return err
	}
	if err := writeJSON(filepath.Join(dir, dossierFile), files.Dossier); err != nil {
		return err
	}
	if files.Outline != nil {
		return writeJSON(filepath.Join(dir, outlineFile), files.Outline)
	}
	return nil
}

// SaveResearch writes the research so far, whole or not at all; none (no
// questions) takes the file away.
func SaveResearch(root, id string, progress ResearchProgress) error {
	if len(progress.Questions) == 0 {
		return ClearResearch(root, id)
	}
	dir, err := Dir(root, id)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return writeJSON(filepath.Join(dir, researchFile), progress)
}

// LoadResearch is the research a stopped run kept; empty when there is
// none, or it can't be read - researched again then, as before it was
// kept.
func LoadResearch(root, id string) ResearchProgress {
	var progress ResearchProgress
	if dir, err := Dir(root, id); err == nil {
		if ok, err := readJSON(filepath.Join(dir, researchFile), &progress); !ok || err != nil {
			return ResearchProgress{}
		}
	}
	return progress
}

// ClearResearch takes the research in progress away: whole, it is the
// dossier.
func ClearResearch(root, id string) error {
	dir, err := Dir(root, id)
	if err != nil {
		return err
	}
	if err := os.Remove(filepath.Join(dir, researchFile)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

func sectionFile(index int) string { return fmt.Sprintf("section-%d.md", index+1) }

// SaveSection keeps section index's draft, as `section-<index + 1>.md`
// beside the paper, for review and for a run that stopped to go on from.
func SaveSection(root, id string, index int, markdown string) error {
	dir, err := Dir(root, id)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return writeAtomic(filepath.Join(dir, sectionFile(index)), []byte(markdown))
}

// ClearSections takes every section draft kept beside paper id away.
func ClearSections(root, id string) error {
	dir, err := Dir(root, id)
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	for _, e := range entries {
		if name := e.Name(); strings.HasPrefix(name, "section-") && strings.HasSuffix(name, ".md") {
			if err := os.Remove(filepath.Join(dir, name)); err != nil {
				return err
			}
		}
	}
	return nil
}

// LoadSection is section index's draft, false if it wasn't written.
func LoadSection(root, id string, index int) (string, bool, error) {
	dir, err := Dir(root, id)
	if err != nil {
		return "", false, err
	}
	text, err := os.ReadFile(filepath.Join(dir, sectionFile(index)))
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return "", false, nil
	case err != nil:
		return "", false, err
	}
	return string(text), true, nil
}

// KeepAttachments is paper id's files folder, with the files attached to
// it in its attached/, or "" when it has none. They are copied there from
// the conversation's scratch folder ("" for none) the first time, so a
// paper going on still has them once that folder is gone; an older
// paper's materials.json is written out as files once.
func KeepAttachments(root, id, conversation string) (string, error) {
	dir, err := Dir(root, id)
	if err != nil {
		return "", err
	}
	// A folder of their own, so the file tools see the attachments and
	// not the paper's own files beside them.
	files := filepath.Join(dir, filesFolder)
	attached := filepath.Join(files, Attached)
	if !exists(attached) && conversation != "" {
		if from := filepath.Join(conversation, Attached); isDir(from) {
			if err := os.MkdirAll(files, 0o755); err != nil {
				return "", err
			}
			if err := copyWhole(from, attached); err != nil {
				return "", err
			}
		}
	}
	if err := writeOutMaterials(dir, attached); err != nil {
		return "", err
	}
	if !isDir(attached) {
		return "", nil
	}
	return files, nil
}

// material is a file attached as text by an older Textus, kept in
// materials.json.
type material struct {
	Name    string `json:"name"`
	Content string `json:"content"`
}

func writeOutMaterials(dir, attached string) error {
	path := filepath.Join(dir, materialsFile)
	var materials []material
	if ok, err := readJSON(path, &materials); err != nil || !ok {
		return err
	}
	if err := os.MkdirAll(attached, 0o755); err != nil {
		return err
	}
	for _, m := range materials {
		name := m.Name[strings.LastIndexAny(m.Name, `/\`)+1:]
		name = strings.TrimLeft(name, ".")
		if name == "" {
			name = "material.md"
		}
		if err := writeAtomic(filepath.Join(attached, name), []byte(m.Content)); err != nil {
			return err
		}
	}
	return os.Remove(path)
}

// Load is the paper id kept under root.
func Load(root, id string) (Files, error) {
	dir, err := Dir(root, id)
	if err != nil {
		return Files{}, err
	}
	var files Files
	if ok, err := readJSON(filepath.Join(dir, paperFile), &files.Paper); err != nil {
		return Files{}, err
	} else if !ok {
		return Files{}, fmt.Errorf("No paper %s in this project", id)
	}
	if _, err := readJSON(filepath.Join(dir, dossierFile), &files.Dossier); err != nil {
		return Files{}, err
	}
	var outline Outline
	if ok, err := readJSON(filepath.Join(dir, outlineFile), &outline); err != nil {
		return Files{}, err
	} else if ok {
		files.Outline = &outline
	}
	return files, nil
}

// writeJSON writes value to path whole, indented for a file people may
// read by hand.
func writeJSON(path string, value any) error {
	raw, err := json.Marshal(value, jsontext.WithIndent("  "), json.Deterministic(true))
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return writeAtomic(path, raw)
}

// readJSON reads path into v: false when the file isn't there, an error
// when it is but can't be read.
func readJSON(path string, v any) (bool, error) {
	raw, err := os.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return false, nil
	case err != nil:
		return false, err
	}
	if err := json.Unmarshal(raw, v); err != nil {
		return false, fmt.Errorf("%s: %v", path, err)
	}
	return true, nil
}
