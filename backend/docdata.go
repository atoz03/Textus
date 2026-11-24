package backend

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// What is kept beside the documents, in the app's data folder: each
// document's undo history, its own notes and tracked changes, and the AI's
// proposals waiting on it.

const (
	undoKeptFor   = 30 * 24 * time.Hour
	undoKeptBytes = 50 * 1024 * 1024
)

type undoFile struct {
	Path    string `json:"path"`
	Hash    string `json:"hash"` // the text the history ends at, as the editor hashes it
	History string `json:"history"`
}

func undoFileFor(dir, path string) string {
	return filepath.Join(dir, pathKey(path, 16)+".json")
}

func readUndo(file string) (undoFile, bool) {
	raw, err := os.ReadFile(file)
	if err != nil {
		return undoFile{}, false
	}
	var u undoFile
	if json.Unmarshal(raw, &u) != nil {
		moveAside(file)
		return undoFile{}, false
	}
	return u, true
}

func saveUndo(dir, path, hash, history string) error {
	raw, err := json.Marshal(undoFile{path, hash, history})
	if err != nil {
		return err
	}
	if err := writeFileAtomic(undoFileFor(dir, path), raw); err != nil {
		return err
	}
	sweepUndo(dir, time.Now())
	return nil
}

func loadUndo(dir, path, hash string) *string {
	u, ok := readUndo(undoFileFor(dir, path))
	if !ok || u.Path != path || u.Hash != hash {
		return nil
	}
	return &u.History
}

func moveUndo(dir, from, to string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	for _, e := range entries {
		u, ok := readUndo(filepath.Join(dir, e.Name()))
		if !ok {
			continue
		}
		moved, ok := movedPath(u.Path, from, to)
		if !ok {
			continue
		}
		old := undoFileFor(dir, u.Path)
		u.Path = moved
		raw, err := json.Marshal(u)
		if err != nil {
			return err
		}
		if err := writeFileAtomic(undoFileFor(dir, u.Path), raw); err != nil {
			return err
		}
		_ = os.Remove(old)
	}
	return nil
}

// sweepUndo deletes the oldest histories past a month, or past the size
// all may take.
func sweepUndo(dir string, now time.Time) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	type kept struct {
		when time.Time
		size int64
		path string
	}
	var files []kept
	var total int64
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		files = append(files, kept{info.ModTime(), info.Size(), filepath.Join(dir, e.Name())})
		total += info.Size()
	}
	sort.Slice(files, func(i, j int) bool { return files[i].when.Before(files[j].when) })
	for _, f := range files {
		if now.Sub(f.when) <= undoKeptFor && total <= undoKeptBytes {
			break
		}
		if os.Remove(f.path) == nil {
			total -= f.size
		}
	}
}

// OwnPart is what is kept for a document: its own notes (a list), or its
// tracked changes (an object).
type OwnPart string

const (
	partNotes     OwnPart = "notes"
	partRevisions OwnPart = "revisions"
)

func partFileIn(data, path string, part OwnPart) string {
	hash := pathKey(path, 32)
	name := hash + ".json"
	if part == partRevisions {
		name = hash + ".revisions.json"
	}
	return filepath.Join(data, "own-notes", name)
}

// isTooBroad: the home folder or a drive root, which a walk or a project
// must not take whole.
func isTooBroad(path string) bool {
	canon, err := filepath.EvalSymlinks(path)
	if err != nil {
		canon = path
	}
	canon = filepath.Clean(canon)
	if filepath.Dir(canon) == canon {
		return true
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	if h, err := filepath.EvalSymlinks(home); err == nil {
		home = h
	}
	return filepath.Clean(home) == canon
}

// projectDir is a project's own folder, ".textus".
func projectDir(root string) string { return filepath.Join(root, ".textus") }

// projectsOf are the folders above a document that hold a project, nearest
// first, then the window's folder.
func projectsOf(path string, folder *string) []string {
	dir := filepath.Dir(path)
	var out []string
	for d := dir; ; d = filepath.Dir(d) {
		if isTooBroad(d) {
			break
		}
		if info, err := os.Stat(projectDir(d)); err == nil && info.IsDir() {
			out = append(out, d)
		}
		if filepath.Dir(d) == d {
			break
		}
	}
	if folder != nil {
		if _, under := pathUnder(dir, *folder); under && !isTooBroad(*folder) {
			found := false
			for _, d := range out {
				if d == *folder {
					found = true
				}
			}
			if !found {
				out = append(out, *folder)
			}
		}
	}
	return out
}

// keptFileIn is where a part of a document is kept: a document's notes in
// its project (.textus/notes/<path>.json) where it has one, else in the
// app's data folder.
func keptFileIn(data, path string, part OwnPart, folder *string) string {
	if part == partNotes {
		var files []string
		for _, root := range projectsOf(path, folder) {
			rel, ok := pathUnder(path, root)
			if !ok || len(rel) == 0 {
				continue
			}
			files = append(files, filepath.Join(append([]string{projectDir(root), "notes"}, rel...)...)+".json")
		}
		for _, f := range files {
			if info, err := os.Stat(f); err == nil && info.Mode().IsRegular() {
				return f
			}
		}
		if len(files) > 0 {
			return files[0]
		}
	}
	return partFileIn(data, path, part)
}

// adoptOldNotes moves notes kept in the data folder into the project that
// now holds the document.
func adoptOldNotes(data, path, kept string) {
	old := partFileIn(data, path, partNotes)
	if old == kept {
		return
	}
	if info, err := os.Stat(old); err != nil || !info.Mode().IsRegular() {
		return
	}
	if _, err := os.Lstat(kept); err == nil {
		return
	}
	if os.MkdirAll(filepath.Dir(kept), 0o755) != nil {
		return
	}
	if os.Rename(old, kept) != nil && copyFile(old, kept) == nil {
		_ = os.Remove(old)
	}
}

func copyFile(from, to string) error {
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(to, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		_ = os.Remove(to)
		return err
	}
	return out.Close()
}

// carry renames what was kept under old to new, unless new has its own.
func carry(old, new string) {
	if _, err := os.Lstat(old); err != nil {
		return
	}
	if _, err := os.Lstat(new); err == nil {
		return
	}
	_ = os.MkdirAll(filepath.Dir(new), 0o755)
	_ = os.Rename(old, new)
}

// movedPairs are the documents a move moved: the one document, or every
// file under a moved folder, each as (old, new).
func movedPairs(from, to string) [][2]string {
	info, err := os.Stat(to)
	if err != nil || !info.IsDir() {
		return [][2]string{{from, to}}
	}
	var pairs [][2]string
	_ = filepath.WalkDir(to, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		old, ok1 := movedPath(path, to, from)
		now, ok2 := movedPath(path, to, to)
		if ok1 && ok2 {
			pairs = append(pairs, [2]string{old, now})
		}
		return nil
	})
	return pairs
}

func pendingFileIn(data, doc string) string {
	return filepath.Join(data, "pending", pathKey(doc, 16)+".json")
}

func moveDocumentData(data, from, to string, folder *string) error {
	for _, pair := range movedPairs(from, to) {
		old, now := pair[0], pair[1]
		notes := keptFileIn(data, now, partNotes, folder)
		carry(keptFileIn(data, old, partNotes, folder), notes)
		adoptOldNotes(data, old, notes)
		carry(partFileIn(data, old, partRevisions), partFileIn(data, now, partRevisions))
		was, is := pendingFileIn(data, old), pendingFileIn(data, now)
		carry(was, is)
		carry(was+".pictures", is+".pictures")
	}
	return moveUndo(filepath.Join(data, "undo"), from, to)
}

func init() {
	command("undo_file_save", func(c *Call, a struct {
		Path    string `json:"path"`
		Hash    string `json:"hash"`
		History string `json:"history"`
	}) (nothing, error) {
		return nil, saveUndo(filepath.Join(c.app.dirs.Data, "undo"), a.Path, a.Hash, a.History)
	})
	command("undo_file_load", func(c *Call, a struct {
		Path string `json:"path"`
		Hash string `json:"hash"`
	}) (*string, error) {
		return loadUndo(filepath.Join(c.app.dirs.Data, "undo"), a.Path, a.Hash), nil
	})
	command("document_data_move", func(c *Call, a struct {
		From   string  `json:"from"`
		To     string  `json:"to"`
		Folder *string `json:"folder"`
	}) (nothing, error) {
		if err := moveDocumentData(c.app.dirs.Data, a.From, a.To, a.Folder); err != nil {
			return nil, err
		}
		c.app.moveConversations(a.From, a.To)
		return nil, nil
	})
	command("own_notes_read", func(c *Call, a struct {
		Path   string  `json:"path"`
		Part   OwnPart `json:"part"`
		Folder *string `json:"folder"`
	}) (*string, error) {
		file := keptFileIn(c.app.dirs.Data, a.Path, a.Part, a.Folder)
		if a.Part == partNotes {
			adoptOldNotes(c.app.dirs.Data, a.Path, file)
		}
		raw, err := os.ReadFile(file)
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		s := string(raw)
		return &s, nil
	})
	command("own_notes_write", func(c *Call, a struct {
		Path   string  `json:"path"`
		Part   OwnPart `json:"part"`
		Value  string  `json:"value"`
		Folder *string `json:"folder"`
	}) (nothing, error) {
		v := jsontext.Value(a.Value)
		if !v.IsValid() {
			return nil, errors.New("not valid JSON")
		}
		file := keptFileIn(c.app.dirs.Data, a.Path, a.Part, a.Folder)
		var empty bool
		switch {
		case v.Kind() == '[' && a.Part == partNotes:
			var list []jsontext.Value
			_ = json.Unmarshal(v, &list)
			empty = len(list) == 0
		case v.Kind() == '{' && a.Part == partRevisions:
			var obj jsonObject
			_ = json.Unmarshal(v, &obj)
			empty = len(obj) == 0
		default:
			return nil, errors.New("not what this part keeps")
		}
		if empty {
			if err := os.Remove(file); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return nil, err
			}
			return nil, nil
		}
		return nil, writeFileAtomic(file, []byte(a.Value))
	})
	command("own_notes_adopt", func(c *Call, a struct {
		Folder string `json:"folder"`
	}) (nothing, error) {
		if isTooBroad(a.Folder) {
			return nil, nil
		}
		data := c.app.dirs.Data
		w := walk(a.Folder, walkLimits, isTextFile)
		for _, f := range w.files {
			if info, err := os.Stat(partFileIn(data, f.path, partNotes)); err == nil && info.Mode().IsRegular() {
				adoptOldNotes(data, f.path, keptFileIn(data, f.path, partNotes, &a.Folder))
			}
		}
		return nil, nil
	})
}
