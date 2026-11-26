package backend

import (
	"bytes"
	"encoding/base64"
	"encoding/json/v2"
	"errors"
	"fmt"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"

	_ "golang.org/x/image/bmp"
	_ "golang.org/x/image/tiff"
	_ "golang.org/x/image/webp"
)

// DirEntryInfo is one entry of a folder, for the sidebar's tree.
type DirEntryInfo struct {
	Name       string   `json:"name"`
	Path       string   `json:"path"`
	IsDir      bool     `json:"is_dir"`
	ModifiedMs *float64 `json:"modified_ms"`
	Hidden     bool     `json:"hidden"`
	Ignored    bool     `json:"ignored"`
}

func requireNonEmpty(path string) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("empty path")
	}
	return nil
}

// secretFiles are the app's files that hold keys and tokens: no command
// reads or writes them for a page.
var secretFiles = []string{"provider_keys.json", "auth-claude.json", "auth-codex.json", "custom_endpoint.json", "plugin_secrets.json"}

func (a *App) notASecret(path string) error {
	config, err := filepath.EvalSymlinks(a.dirs.Config)
	if err != nil {
		return nil
	}
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		dir, derr := filepath.EvalSymlinks(filepath.Dir(path))
		if derr != nil {
			return nil
		}
		real = filepath.Join(dir, filepath.Base(path))
	}
	name := strings.ToLower(filepath.Base(real))
	if !samePath(filepath.Dir(real), config) {
		return nil
	}
	for _, s := range secretFiles {
		if name == s || strings.HasPrefix(name, s+".") {
			return codeError("secretsFile")
		}
	}
	return nil
}

// readUTF8 reads a text file, which must be UTF-8.
func readUTF8(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if !utf8.Valid(raw) {
		return "", errors.New("stream did not contain valid UTF-8")
	}
	return string(raw), nil
}

func listDir(dir string) ([]DirEntryInfo, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	ignored := ignoredIn(dir)
	items := make([]DirEntryInfo, 0, len(entries))
	for _, e := range entries {
		path := filepath.Join(dir, e.Name())
		isDir := e.IsDir()
		var modified *float64
		hidden := strings.HasPrefix(e.Name(), ".")
		if info, err := e.Info(); err == nil {
			modified = modifiedMs(info)
			hidden = hidden || markedHidden(info)
		}
		items = append(items, DirEntryInfo{
			Name: e.Name(), Path: path, IsDir: isDir, ModifiedMs: modified,
			Hidden: hidden, Ignored: ignored(path, isDir),
		})
	}
	slices.SortStableFunc(items, func(a, b DirEntryInfo) int {
		if a.IsDir != b.IsDir {
			if a.IsDir {
				return -1
			}
			return 1
		}
		return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})
	return items, nil
}

// intoFreeName makes the first of names that make can create in dir;
// one that exists is taken as it is when reuse says so.
func intoFreeName(dir string, names []string, make func(path string) error, reuse func(path string) bool) (string, error) {
	for _, candidate := range names {
		path := filepath.Join(dir, candidate)
		err := make(path)
		if err == nil {
			return candidate, nil
		}
		if errors.Is(err, fs.ErrExist) {
			if reuse(path) {
				return candidate, nil
			}
			continue
		}
		return "", err
	}
	first := ""
	if len(names) > 0 {
		first = names[0]
	}
	return "", fmt.Errorf("no free name for %s: %w", first, fs.ErrExist)
}

func numberedNames(name string, style numberedStyle, skipOne bool) []string {
	names := make([]string, 0, 64)
	for n := 0; n < 10_000; n++ {
		if skipOne && n == 1 {
			continue
		}
		names = append(names, numbered(name, n, style))
	}
	return names
}

// writeNewFile writes data under the first free name like name in dir
// ("a-2.png"); with reuse, a file already holding the same bytes is taken.
func writeNewFile(dir, name string, data []byte, reuse bool) (string, error) {
	claimed := false
	got, err := intoFreeName(dir, numberedNames(name, numberedDash, false), func(path string) error {
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err != nil {
			return err
		}
		claimed = true
		return f.Close()
	}, func(path string) bool {
		if !reuse {
			return false
		}
		held, err := os.ReadFile(path)
		return err == nil && bytes.Equal(held, data)
	})
	if err != nil {
		return "", err
	}
	if claimed {
		path := filepath.Join(dir, got)
		if err := writeAtomic(path, data); err != nil {
			_ = os.Remove(path)
			return "", err
		}
	}
	return got, nil
}

// copyFileNew copies the file from to to, a path where nothing is yet; a
// copy that fails leaves nothing there.
func copyFileNew(from, to string) error {
	f, err := os.OpenFile(to, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	in, err := os.Open(from)
	if err != nil {
		f.Close()
		_ = os.Remove(to)
		return err
	}
	defer in.Close()
	if _, err := io.Copy(f, in); err != nil {
		f.Close()
		_ = os.Remove(to)
		return err
	}
	return f.Close()
}

func sameBytes(a, b string) bool {
	x, err1 := os.ReadFile(a)
	y, err2 := os.ReadFile(b)
	return err1 == nil && err2 == nil && bytes.Equal(x, y)
}

// copyTree copies the folder from into to, which exists, skipping secret
// files; it lists what it made, for undoing a copy that failed.
func copyTree(from, to string, secret func(string) bool, made *[]string) error {
	entries, err := os.ReadDir(from)
	if err != nil {
		return err
	}
	for _, e := range entries {
		path := filepath.Join(from, e.Name())
		target := path
		if e.Type()&fs.ModeSymlink != 0 {
			real, err := filepath.EvalSymlinks(path)
			if err != nil {
				continue
			}
			if info, err := os.Stat(real); err != nil || !info.Mode().IsRegular() {
				continue
			}
			target = real
		}
		if secret(target) {
			continue
		}
		dest := filepath.Join(to, e.Name())
		if e.IsDir() {
			if err := os.Mkdir(dest, 0o755); err != nil {
				return err
			}
			*made = append(*made, dest)
			if err := copyTree(path, dest, secret, made); err != nil {
				return err
			}
			continue
		}
		if err := copyFileNew(target, dest); err != nil {
			return err
		}
		*made = append(*made, dest)
	}
	return nil
}

// undoMade removes what a failed copy made, deepest first.
func undoMade(made []string) {
	for i := len(made) - 1; i >= 0; i-- {
		_ = os.Remove(made[i])
	}
}

// copyNaming is how a copy is named: as itself, refused when that is
// taken; numbered when taken ("report 2.pdf"), as a file manager keeps
// both; or as a copy beside the original in the words of the pattern, as
// Finder names one ("{name} copy": "report copy.pdf", then "report copy
// 2.pdf"). Its TypeScript side is `CopyNaming` in
// src/platform/bindings/files.ts: "own", "numbered" or {"beside": pattern}.
type copyNaming struct {
	own    bool
	beside *string
}

func (n *copyNaming) UnmarshalJSON(raw []byte) error {
	var word string
	if err := json.Unmarshal(raw, &word); err == nil {
		switch word {
		case "own":
			*n = copyNaming{own: true}
			return nil
		case "numbered":
			*n = copyNaming{}
			return nil
		}
		return fmt.Errorf("unknown copy naming %q", word)
	}
	var beside struct {
		Beside *string `json:"beside"`
	}
	if err := json.Unmarshal(raw, &beside); err != nil || beside.Beside == nil {
		return fmt.Errorf("unknown copy naming %s", raw)
	}
	*n = copyNaming{beside: beside.Beside}
	return nil
}

// names is the names a copy of name tries, in order.
func (n copyNaming) names(name string, isDir bool) []string {
	switch {
	case n.own:
		return []string{name}
	case n.beside != nil:
		stem, ext := name, ""
		if i := strings.LastIndex(name, "."); !isDir && i > 0 {
			stem, ext = name[:i], name[i:]
		}
		pattern := *n.beside
		if !strings.Contains(pattern, "{name}") {
			pattern = "{name} copy"
		}
		return numberedNames(strings.ReplaceAll(pattern, "{name}", stem)+ext, numberedFor(isDir), true)
	}
	return numberedNames(name, numberedFor(isDir), true)
}

// copyEntry copies the file or folder from (a folder with all it holds)
// into the folder into, named as naming says, never over anything there;
// secret is a place never copied from (the app's keys). It returns the
// name the copy took.
func copyEntry(from, into string, naming copyNaming, secret func(string) bool) (string, error) {
	from, err := filepath.EvalSymlinks(from)
	if err != nil {
		return "", err
	}
	into, err = filepath.EvalSymlinks(into)
	if err != nil {
		return "", err
	}
	if info, err := os.Stat(into); err != nil || !info.IsDir() {
		return "", codeError("notFolder")
	}
	name := filepath.Base(from)
	if _, err := os.Lstat(filepath.Join(into, name)); naming.own && err == nil {
		return "", codeError("exists")
	}
	info, err := os.Stat(from)
	if err != nil {
		return "", err
	}
	never := func(string) bool { return false }
	if !info.IsDir() {
		got, err := intoFreeName(into, naming.names(name, false), func(to string) error {
			return copyFileNew(from, to)
		}, never)
		return got, existsError(err)
	}
	if _, under := pathUnder(into, from); under {
		return "", codeError("copyIntoItself")
	}
	madeName, err := intoFreeName(into, naming.names(name, true), func(to string) error {
		return os.Mkdir(to, 0o755)
	}, never)
	if err != nil {
		return "", existsError(err)
	}
	to := filepath.Join(into, madeName)
	made := []string{to}
	if err := copyTree(from, to, secret, &made); err != nil {
		// Half a copy is no copy: what was made here a moment ago goes.
		undoMade(made)
		return "", err
	}
	return madeName, nil
}

func init() {
	command("list_dir", func(c *Call, a struct {
		Path string `json:"path"`
	}) ([]DirEntryInfo, error) {
		if err := requireNonEmpty(a.Path); err != nil {
			return nil, err
		}
		return listDir(a.Path)
	})
	command("file_mtime_ms", func(c *Call, a struct {
		Path string `json:"path"`
	}) (*float64, error) {
		if err := requireNonEmpty(a.Path); err != nil {
			return nil, err
		}
		info, err := os.Stat(a.Path)
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		return modifiedMs(info), nil
	})
	command("read_text_file", func(c *Call, a struct {
		Path string `json:"path"`
	}) (string, error) {
		if err := requireNonEmpty(a.Path); err != nil {
			return "", err
		}
		if err := c.app.notASecret(a.Path); err != nil {
			return "", err
		}
		return readUTF8(a.Path)
	})
	command("read_binary_file_base64", func(c *Call, a struct {
		Path string `json:"path"`
	}) (string, error) {
		if err := requireNonEmpty(a.Path); err != nil {
			return "", err
		}
		if err := c.app.notASecret(a.Path); err != nil {
			return "", err
		}
		raw, err := os.ReadFile(a.Path)
		if err != nil {
			return "", err
		}
		return base64.StdEncoding.EncodeToString(raw), nil
	})
	command("write_text_file", func(c *Call, a struct {
		Path     string `json:"path"`
		Contents string `json:"contents"`
	}) (nothing, error) {
		if err := requireNonEmpty(a.Path); err != nil {
			return nil, err
		}
		if err := c.app.notASecret(a.Path); err != nil {
			return nil, err
		}
		return nil, writeFileAtomic(a.Path, []byte(a.Contents))
	})
	command("write_base64_file", func(c *Call, a struct {
		Path string `json:"path"`
		Data string `json:"data"`
	}) (nothing, error) {
		if err := requireNonEmpty(a.Path); err != nil {
			return nil, err
		}
		if err := c.app.notASecret(a.Path); err != nil {
			return nil, err
		}
		raw, err := base64.StdEncoding.DecodeString(a.Data)
		if err != nil {
			return nil, err
		}
		return nil, writeAtomic(a.Path, raw)
	})
	// The file tree's copy (a paste, Duplicate, a drag with Option or Ctrl,
	// files dropped from a file manager): path copied into the folder into,
	// named as naming says. The copy's path is in into's spelling.
	command("tree_copy_entry", func(c *Call, a struct {
		Path   string     `json:"path"`
		Into   string     `json:"into"`
		Naming copyNaming `json:"naming"`
	}) (string, error) {
		if err := requireNonEmpty(a.Path); err != nil {
			return "", err
		}
		if err := requireNonEmpty(a.Into); err != nil {
			return "", err
		}
		if err := c.app.notASecret(a.Path); err != nil {
			return "", err
		}
		config := c.app.dirs.Config
		name, err := copyEntry(a.Path, a.Into, a.Naming, func(p string) bool {
			return inAnyDir(p, config)
		})
		if err != nil {
			return "", err
		}
		return filepath.Join(a.Into, name), nil
	})
	// Rows of the tree dragged out of the window, for a file manager or
	// another app to take: MyGo starts the system's drag of files only from
	// its own native UI, never from a page, so nothing takes them
	// (canDragFilesOut in src/platform/capabilities.ts).
	command("start_file_drag_out", func(c *Call, a struct {
		Paths []string `json:"paths"`
	}) (bool, error) {
		return false, nil
	})
}
