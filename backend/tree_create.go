package backend

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// New document and new folder for the sidebar's file tree, and the folders
// on the way a name with slashes gives. Nothing here overwrites: a taken
// name is refused, and leaves no folder made for it behind.

// Created is what a new document or folder made: its path, and the topmost
// entry made for it (the first folder on the way that wasn't there, else
// the entry itself), which an undo takes away. Its TypeScript side is
// `Created` in src/platform/bindings/files.ts.
type Created struct {
	Path string `json:"path"`
	Made string `json:"made"`
}

// typedPath is a typed name that may name folders on the way
// ("2026/March/plan"): those folders and the last name, each one name that
// can't walk out.
func typedPath(name string) ([]string, string, error) {
	text := strings.TrimSpace(name)
	if runtime.GOOS == "windows" {
		text = strings.ReplaceAll(text, `\`, "/")
	}
	parts := strings.Split(text, "/")
	names := make([]string, 0, len(parts))
	for _, part := range parts {
		n, err := typedName(part)
		if err != nil {
			return nil, "", err
		}
		names = append(names, n)
	}
	return names[:len(names)-1], names[len(names)-1], nil
}

// wayDown is dir, then the folders on the way, made where missing: the
// folder they lead to, and the topmost one made ("" when none was).
func wayDown(dir string, folders []string) (at, made string, err error) {
	at, err = existingDir(dir)
	if err != nil {
		return "", "", err
	}
	for _, folder := range folders {
		at = filepath.Join(at, folder)
		err := os.Mkdir(at, 0o755)
		if err == nil {
			if made == "" {
				made = at
			}
			continue
		}
		if info, serr := os.Stat(at); errors.Is(err, fs.ErrExist) && serr == nil && info.IsDir() {
			continue
		}
		takeBack(filepath.Dir(at), made)
		return "", "", existsError(err)
	}
	return at, made, nil
}

// takeBack takes away the folders wayDown made, from at up to top: an
// entry that couldn't be made leaves none behind. Only empty ones go.
func takeBack(at, top string) {
	if top == "" {
		return
	}
	for dir := at; ; dir = filepath.Dir(dir) {
		if _, under := pathUnder(dir, top); !under || os.Remove(dir) != nil || dir == top {
			return
		}
	}
}

// createEntry makes name in dir with make, and the folders on the way;
// entry is the file name the last name gives.
func createEntry(dir, name string, entry func(string) string, make func(path string) error) (Created, error) {
	folders, last, err := typedPath(name)
	if err != nil {
		return Created{}, err
	}
	at, made, err := wayDown(dir, folders)
	if err != nil {
		return Created{}, err
	}
	path := filepath.Join(at, entry(last))
	if err := make(path); err != nil {
		takeBack(at, made)
		return Created{}, existsError(err)
	}
	if made == "" {
		made = path
	}
	return Created{Path: path, Made: made}, nil
}

func createDocument(dir, name string) (Created, error) {
	return createEntry(dir, name, documentName, func(path string) error {
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err != nil {
			return err
		}
		return f.Close()
	})
}

func createFolder(dir, name string) (Created, error) {
	return createEntry(dir, name, func(name string) string { return name }, func(path string) error {
		return os.Mkdir(path, 0o755)
	})
}

func init() {
	// An empty Markdown document in dir, and the folders on the way a name
	// with slashes gives.
	command("tree_create_document", func(c *Call, a struct {
		Dir  string `json:"dir"`
		Name string `json:"name"`
	}) (Created, error) {
		return createDocument(a.Dir, a.Name)
	})
	// A folder in dir, and the folders on the way a name with slashes gives.
	command("tree_create_folder", func(c *Call, a struct {
		Dir  string `json:"dir"`
		Name string `json:"name"`
	}) (Created, error) {
		return createFolder(a.Dir, a.Name)
	})
}
