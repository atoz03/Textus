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

type entriesChanged struct {
	From    string     `json:"from"`
	Moved   *[2]string `json:"moved"`
	Trashed *string    `json:"trashed"`
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

// newEntry is a document or folder to make in Dir under the name the user
// typed.
type newEntry struct {
	Dir  string `json:"dir"`
	Name string `json:"name"`
}

// create makes the entry with make, at the name named gives the typed one;
// one already there is an error the page names.
func (e newEntry) create(named func(string) string, make func(path string) error) (string, error) {
	dir, err := existingDir(e.Dir)
	if err != nil {
		return "", err
	}
	name, err := typedName(e.Name)
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, named(name))
	if err := make(path); err != nil {
		return "", existsError(err)
	}
	return path, nil
}

func init() {
	command("tree_create_document", func(c *Call, a newEntry) (string, error) {
		return a.create(documentName, func(path string) error {
			f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
			if err != nil {
				return err
			}
			return f.Close()
		})
	})
	command("tree_create_folder", func(c *Call, a newEntry) (string, error) {
		return a.create(func(name string) string { return name }, func(path string) error {
			return os.Mkdir(path, 0o755)
		})
	})
	command("tree_move_entry", func(c *Call, a struct {
		Path string `json:"path"`
		Into string `json:"into"`
	}) (string, error) {
		from, err := itself(a.Path)
		if err != nil {
			return "", err
		}
		if _, err := existingDir(a.Into); err != nil {
			return "", err
		}
		into, err := filepath.EvalSymlinks(a.Into)
		if err != nil {
			return "", err
		}
		if filepath.Dir(from) == into {
			return "", codeError("alreadyThere")
		}
		if _, under := pathUnder(into, from); under {
			return "", codeError("moveIntoItself")
		}
		to := filepath.Join(into, filepath.Base(from))
		if _, err := os.Lstat(to); !errors.Is(err, fs.ErrNotExist) {
			return "", codeError("exists")
		}
		if err := moveEntry(from, to); err != nil {
			return "", err
		}
		// In the window's spelling of the folder, so its tabs still match.
		moved := filepath.Join(a.Into, filepath.Base(from))
		c.app.emitAll("entries-changed", entriesChanged{From: c.Label(), Moved: &[2]string{a.Path, moved}})
		return moved, nil
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
	command("tree_trash_entry", func(c *Call, a struct {
		Path string `json:"path"`
		Root string `json:"root"`
	}) (nothing, error) {
		target, err := itself(a.Path)
		if err != nil {
			return nil, err
		}
		if root, err := filepath.EvalSymlinks(a.Root); err == nil {
			if _, under := pathUnder(root, target); under {
				return nil, codeError("trashShownFolder")
			}
		}
		if err := mygo.Shell.TrashItem(target); err != nil {
			return nil, err
		}
		c.app.emitAll("entries-changed", entriesChanged{From: c.Label(), Trashed: &a.Path})
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
