package backend

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/egoist/mygo"
)

// The sidebar's file operations: new documents and folders, moves, renames
// and the trash. Every window hears of a change ("entries-changed").

func documentName(name string) string {
	if isMarkdown(name) {
		return name
	}
	return name + ".md"
}

// namesAType: the name ends in what reads as a file type ("x.png").
func namesAType(name string) bool {
	ext := strings.TrimPrefix(filepath.Ext(name), ".")
	if ext == "" || len(ext) > 8 {
		return false
	}
	alpha := false
	for _, c := range ext {
		switch {
		case c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z':
			alpha = true
		case c >= '0' && c <= '9':
		default:
			return false
		}
	}
	return alpha
}

func existingDir(dir string) (string, error) {
	info, err := os.Stat(dir)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", codeError("notFolder")
	}
	return dir, nil
}

// itself is path with its folder resolved, the entry itself not followed.
func itself(path string) (string, error) {
	name := filepath.Base(path)
	parent := filepath.Dir(path)
	folder, err := filepath.EvalSymlinks(parent)
	if err != nil {
		return "", err
	}
	folder, err = filepath.Abs(folder)
	if err != nil {
		return "", err
	}
	me := filepath.Join(folder, name)
	if _, err := os.Lstat(me); err != nil {
		return "", err
	}
	return me, nil
}

// entriesChanged is what a window did to the folder, for the other
// windows: their trees read the folders again and their tabs follow a
// move. Its TypeScript side is `EntriesChanged` in
// src/app/useEntriesFromOtherWindows.ts.
type entriesChanged struct {
	From    string     `json:"from"`
	Moved   *[2]string `json:"moved"`
	Trashed *string    `json:"trashed"`
	// Made: put back from the Trash, or copied.
	Made *string `json:"made"`
}

// moveTarget is where path lands when moved into into (named name, else
// its own name), or why it can't: where it already is, or into itself or
// below.
func moveTarget(path, into string, name *string) (string, error) {
	to := filepath.Join(into, filepath.Base(path))
	if name != nil {
		n, err := typedName(*name)
		if err != nil {
			return "", err
		}
		to = filepath.Join(into, n)
	}
	if to == path || (name == nil && filepath.Dir(path) == into) {
		return "", codeError("alreadyThere")
	}
	if _, under := pathUnder(into, path); under {
		return "", codeError("moveIntoItself")
	}
	return to, nil
}

// moveInto moves the file or folder path into the folder into, under its
// own name or name exactly as given (an undo putting it back as it was).
// With keepBoth, a name taken there is numbered ("plan 2.md"), as a file
// manager keeps both; else it is refused. Its new path is in the spelling
// of into as given, not the real one, so the window's tabs still match.
func moveInto(path, into string, name *string, keepBoth bool) (string, error) {
	from, err := itself(path)
	if err != nil {
		return "", err
	}
	if _, err := existingDir(into); err != nil {
		return "", err
	}
	real, err := filepath.EvalSymlinks(into)
	if err != nil {
		return "", err
	}
	if real, err = filepath.Abs(real); err != nil {
		return "", err
	}
	to, err := moveTarget(from, real, name)
	if err != nil {
		return "", err
	}
	// A change of case only is the same file where case is not told apart.
	caseOnly := false
	if strings.EqualFold(to, from) {
		fromInfo, ferr := os.Lstat(from)
		toInfo, terr := os.Lstat(to)
		caseOnly = ferr == nil && terr == nil && os.SameFile(fromInfo, toInfo)
	}
	if _, err := os.Lstat(to); !caseOnly && !errors.Is(err, fs.ErrNotExist) {
		if !keepBoth {
			return "", codeError("exists")
		}
		info, err := os.Stat(from)
		isDir := err == nil && info.IsDir()
		to = filepath.Join(real, freeName(real, filepath.Base(to), numberedFor(isDir)))
	}
	if err := moveEntry(from, to); err != nil {
		return "", err
	}
	return filepath.Join(into, filepath.Base(to)), nil
}

// trashing is what tree_trash_entries did: where the Trash keeps each entry
// that went (nil where the system doesn't say), and why the next one didn't
// go. Its TypeScript side is `Trashing` in src/platform/bindings/files.ts.
type trashing struct {
	Places []*string `json:"places"`
	Error  *string   `json:"error"`
}

// trashEntries sends paths to the Trash in turn, up to the first that isn't
// there or can't go, never the folder root the tree is showing or one
// holding it.
func trashEntries(paths []string, root string) trashing {
	targets := make([]string, 0, len(paths))
	var missing error
	for _, p := range paths {
		target, err := itself(p)
		if err != nil {
			missing = err
			break
		}
		targets = append(targets, target)
	}
	shown, _ := filepath.EvalSymlinks(root)
	places, err := trashKeepingPlaces(targets, func(target string) error {
		if _, under := pathUnder(shown, target); shown != "" && under {
			return codeError("trashShownFolder")
		}
		return nil
	})
	out := trashing{Places: places}
	if err == nil {
		err = missing
	}
	if err != nil {
		text := err.Error()
		out.Error = &text
	}
	if out.Places == nil {
		out.Places = []*string{}
	}
	return out
}

func existsError(err error) error {
	if errors.Is(err, fs.ErrExist) {
		return codeError("exists")
	}
	return err
}

// moveEntry renames from to to; across disks it copies, then puts the
// original in the trash.
func moveEntry(from, to string) error {
	err := os.Rename(from, to)
	if err == nil {
		return nil
	}
	var linkErr *os.LinkError
	if !errors.As(err, &linkErr) || !errors.Is(linkErr.Err, syscall.EXDEV) {
		return err
	}
	if _, err := os.Lstat(to); err == nil {
		return codeError("exists")
	}
	info, err := os.Lstat(from)
	if err != nil {
		return err
	}
	made := []string{to}
	if info.IsDir() {
		if err := os.Mkdir(to, info.Mode().Perm()); err != nil {
			return err
		}
		if err := copyTree(from, to, func(string) bool { return false }, &made); err != nil {
			undoMade(made)
			return err
		}
	} else if err := copyFileNew(from, to); err != nil {
		return err
	}
	if err := mygo.Shell.TrashItem(from); err != nil {
		undoMade(made)
		return err
	}
	return nil
}

func init() {
	command("tree_move_entry", func(c *Call, a struct {
		Path     string  `json:"path"`
		Into     string  `json:"into"`
		Name     *string `json:"name"`
		KeepBoth bool    `json:"keepBoth"`
	}) (string, error) {
		to, err := moveInto(a.Path, a.Into, a.Name, a.KeepBoth)
		if err != nil {
			return "", err
		}
		c.app.emitAll("entries-changed", entriesChanged{From: c.Label(), Moved: &[2]string{a.Path, to}})
		return to, nil
	})
	command("tree_rename_entry", func(c *Call, a struct {
		Path string `json:"path"`
		Name string `json:"name"`
	}) (string, error) {
		info, err := os.Stat(a.Path)
		if err != nil {
			return "", err
		}
		name, err := typedName(a.Name)
		if err != nil {
			return "", err
		}
		wasDoc := !info.IsDir() && documentName(a.Path) == a.Path
		if wasDoc && !namesAType(name) {
			name = documentName(name)
		}
		to := filepath.Join(filepath.Dir(a.Path), name)
		if to == a.Path {
			return a.Path, nil
		}
		// A change of case only is the same file where case is not told apart.
		caseOnly := false
		if strings.EqualFold(to, a.Path) {
			if toInfo, err := os.Stat(to); err == nil && os.SameFile(info, toInfo) {
				caseOnly = true
			}
		}
		if _, err := os.Lstat(to); !caseOnly && !errors.Is(err, fs.ErrNotExist) {
			return "", codeError("exists")
		}
		if err := os.Rename(a.Path, to); err != nil {
			return "", err
		}
		c.app.emitAll("entries-changed", entriesChanged{From: c.Label(), Moved: &[2]string{a.Path, to}})
		return to, nil
	})
	// To the system's Trash (Recycle Bin) in turn, up to one that can't
	// go, never deleted: where the Trash keeps each, for tree_put_back,
	// where the system says.
	command("tree_trash_entries", func(c *Call, a struct {
		Paths []string `json:"paths"`
		Root  string   `json:"root"`
	}) (trashing, error) {
		done := trashEntries(a.Paths, a.Root)
		for _, path := range a.Paths[:len(done.Places)] {
			c.app.emitAll("entries-changed", entriesChanged{From: c.Label(), Trashed: &path})
		}
		return done, nil
	})
	// What tree_trash_entries sent to the Trash (kept at place) put back at
	// path, never over anything there.
	command("tree_put_back", func(c *Call, a struct {
		Place string `json:"place"`
		Path  string `json:"path"`
	}) (nothing, error) {
		if err := putBack(a.Place, a.Path); err != nil {
			return nil, err
		}
		c.app.emitAll("entries-changed", entriesChanged{From: c.Label(), Made: &a.Path})
		return nil, nil
	})
	command("move_picture_to_trash", func(c *Call, a struct {
		DocPath *string `json:"docPath"`
		Path    string  `json:"path"`
	}) (nothing, error) {
		if !isPicture(a.Path) {
			return nil, errors.New("Only a picture file goes to the Trash this way.")
		}
		file, err := filepath.EvalSymlinks(a.Path)
		if err != nil {
			return nil, errors.New("The old picture isn't there: " + err.Error())
		}
		if info, err := os.Stat(file); err != nil || !info.Mode().IsRegular() {
			return nil, errors.New("The old picture isn't a file.")
		}
		within := func(dir string) bool {
			real, err := filepath.EvalSymlinks(dir)
			if err != nil {
				return false
			}
			_, under := pathUnder(file, real)
			return under
		}
		inDoc := a.DocPath != nil && within(filepath.Dir(*a.DocPath))
		if !inDoc && !within(c.app.draftAssetsDir()) {
			return nil, errors.New("The old picture is outside the document's folder; it was left where it is.")
		}
		return nil, mygo.Shell.TrashItem(file)
	})
}
