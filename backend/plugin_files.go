package backend

import (
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"textus/backend/plugins"
)

// What a running plugin may reach of files, each checked against
// state.json: its own files, the file its custom editor opened, and the
// open folder with files.read or files.write. Bytes go back as Go's []byte,
// base64 in JSON (callBytes in src/platform/ipc-call.ts).

// maxPluginRead is the most a read hands over at once.
const maxPluginRead = 256 * 1024 * 1024

// appToolsOwner is the owner a window tool's call names, where a plugin's
// names its id: the app's own AI tools, which no plugin is.
const appToolsOwner = "textus"

// requireNotAppData refuses the app's own folders (other plugins' storage,
// settings, keys), which no plugin reaches through the workspace calls -
// under any spelling of them (samepath.go).
func (a *App) requireNotAppData(path string) error {
	if inAnyDir(path, a.dirs.Data, a.dirs.Config) {
		return errors.New("The app's own files are not part of the folder.")
	}
	return nil
}

// grantedPath is path for plugin id to use with permission: granted,
// inside root and none of the app's own data.
func (a *App) grantedPath(id, permission, root, path string) (string, error) {
	resolved, err := plugins.FolderPath(a.pluginState(), id, permission, root, path)
	if err != nil {
		return "", err
	}
	if err := a.requireNotAppData(resolved); err != nil {
		return "", err
	}
	return resolved, nil
}

// checkedRoot is a folder a plugin reads: it holds files.read (the app's
// own AI tools ask as textus), the folder is there, and it is not one of
// the app's own.
func (a *App) checkedRoot(id, root string) error {
	if id != appToolsOwner && !a.pluginState().Allows(id, "files.read") {
		return plugins.Refused(id, "files.read")
	}
	if info, err := os.Stat(root); root == "" || err != nil || !info.IsDir() {
		return codeError("noFolder")
	}
	return a.requireNotAppData(root)
}

// readPluginBytes is a file's bytes, refused past maxPluginRead.
func readPluginBytes(path string) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if info.Size() > maxPluginRead {
		return nil, codeError("fileTooBig")
	}
	return os.ReadFile(path)
}

// readOrEmpty is a file's bytes, none when nothing is there.
func readOrEmpty(path string) ([]byte, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return []byte{}, nil
	}
	return raw, err
}

// FsEntry is one entry of a folder listing (FsEntry in
// src/platform/bindings/plugin-access.ts).
type FsEntry struct {
	Name       string   `json:"name"`
	Path       string   `json:"path"`
	IsDir      bool     `json:"isDir"`
	Size       uint64   `json:"size"`
	ModifiedMs *float64 `json:"modifiedMs"`
}

func fsEntryOf(name, path string, info fs.FileInfo) FsEntry {
	return FsEntry{Name: name, Path: path, IsDir: info.IsDir(), Size: uint64(max(info.Size(), 0)), ModifiedMs: modifiedMs(info)}
}

// VersionedText is what a read for a change hands over: the text, and a
// token naming that version of the file, which the write gives back
// (VersionedText in src/platform/bindings/plugin-access.ts).
type VersionedText struct {
	Text  string `json:"text"`
	Token string `json:"token"`
}

// sameFile is whether a and b are one file; true when either isn't there,
// as there is then nothing to overwrite.
func sameFile(a, b string) bool {
	x, errX := os.Lstat(a)
	y, errY := os.Lstat(b)
	if errX != nil || errY != nil {
		return true
	}
	return os.SameFile(x, y)
}

type pluginFileArgs struct {
	ID   string `json:"id"`
	Root string `json:"root"`
	Path string `json:"path"`
}

func init() {
	// One of the plugin's own files. Read even while it is off, so
	// Settings can show its icon.
	command("plugin_file", func(c *Call, a struct {
		ID  string `json:"id"`
		Rel string `json:"rel"`
	}) ([]byte, error) {
		home, err := c.app.pluginHome(c.app.pluginState(), a.ID)
		if err != nil {
			return nil, err
		}
		path, err := plugins.Inside(home, a.Rel)
		if err != nil {
			return nil, err
		}
		return readPluginBytes(path)
	})
	// The file a custom editor was opened on, which the user chose to open.
	command("plugin_read_resource", func(c *Call, a struct {
		ID   string `json:"id"`
		Path string `json:"path"`
	}) ([]byte, error) {
		if err := c.app.pluginState().Running(a.ID); err != nil {
			return nil, err
		}
		real, err := plugins.Canonical(a.Path)
		if err != nil {
			return nil, err
		}
		if err := c.app.requireNotAppData(real); err != nil {
			return nil, err
		}
		return readPluginBytes(a.Path)
	})
	command("plugin_fs_read", func(c *Call, a pluginFileArgs) ([]byte, error) {
		path, err := c.app.grantedPath(a.ID, "files.read", a.Root, a.Path)
		if err != nil {
			return nil, err
		}
		return readPluginBytes(path)
	})
	// A folder's entries, hidden ones left out.
	command("plugin_fs_list", func(c *Call, a pluginFileArgs) ([]FsEntry, error) {
		if !c.app.pluginState().Allows(a.ID, "files.read") {
			return nil, plugins.Refused(a.ID, "files.read")
		}
		dir, err := plugins.PluginPath(a.Root, a.Path, a.ID)
		if err != nil {
			return nil, err
		}
		if err := c.app.requireNotAppData(dir); err != nil {
			return nil, err
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			return nil, err
		}
		out := []FsEntry{}
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), ".") {
				continue
			}
			info, err := e.Info()
			if err != nil {
				continue
			}
			out = append(out, fsEntryOf(e.Name(), filepath.Join(dir, e.Name()), info))
		}
		slices.SortStableFunc(out, func(x, y FsEntry) int {
			return strings.Compare(strings.ToLower(x.Name), strings.ToLower(y.Name))
		})
		return out, nil
	})
	// Writes a file in the folder, making the folders it is in. Contents
	// are text, or base64 with base64.
	command("plugin_fs_write", func(c *Call, a struct {
		pluginFileArgs
		Contents string `json:"contents"`
		Base64   *bool  `json:"base64"`
	}) (nothing, error) {
		path, err := c.app.grantedPath(a.ID, "files.write", a.Root, a.Path)
		if err != nil {
			return nil, err
		}
		data := []byte(a.Contents)
		if a.Base64 != nil && *a.Base64 {
			if data, err = base64.StdEncoding.DecodeString(a.Contents); err != nil {
				return nil, err
			}
		}
		return nil, writeFileAtomic(path, data)
	})
	// Renames or moves a file or folder within the folder, never over
	// something that is there.
	command("plugin_fs_rename", func(c *Call, a struct {
		ID   string `json:"id"`
		Root string `json:"root"`
		From string `json:"from"`
		To   string `json:"to"`
	}) (nothing, error) {
		if !c.app.pluginState().Allows(a.ID, "files.write") {
			return nil, plugins.Refused(a.ID, "files.write")
		}
		from, err := plugins.PluginPath(a.Root, a.From, a.ID)
		if err != nil {
			return nil, err
		}
		to, err := plugins.PluginPath(a.Root, a.To, a.ID)
		if err != nil {
			return nil, err
		}
		for _, p := range []string{from, to} {
			if err := c.app.requireNotAppData(p); err != nil {
				return nil, err
			}
		}
		// A change of case only is the same file on macOS and Windows.
		caseOnly := strings.EqualFold(to, from) && sameFile(from, to)
		if _, err := os.Stat(to); err == nil && !caseOnly {
			return nil, fmt.Errorf("%s is already there.", to)
		}
		if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
			return nil, err
		}
		return nil, moveEntry(from, to)
	})
	// Moves a file or folder in the folder to the system Trash - never a
	// delete the user can't undo.
	command("plugin_fs_trash", func(c *Call, a pluginFileArgs) (nothing, error) {
		path, err := c.app.pluginTrashPath(a)
		if err != nil {
			return nil, err
		}
		if _, err := os.Lstat(path); err != nil {
			return nil, err
		}
		return nil, moveToTrash(path)
	})
	// The checks of plugin_fs_trash alone, and the file they allow: iOS
	// moves it to Recently Deleted itself, as the sidebar does.
	command("plugin_fs_trash_path", func(c *Call, a pluginFileArgs) (string, error) {
		return c.app.pluginTrashPath(a)
	})
	// Makes a folder (and the folders it is in) in the folder.
	command("plugin_fs_mkdir", func(c *Call, a pluginFileArgs) (nothing, error) {
		path, err := c.app.grantedPath(a.ID, "files.write", a.Root, a.Path)
		if err != nil {
			return nil, err
		}
		return nil, os.MkdirAll(path, 0o755)
	})
	// The file at path, or nil when nothing is there.
	command("plugin_fs_stat", func(c *Call, a pluginFileArgs) (*FsEntry, error) {
		path, err := c.app.grantedPath(a.ID, "files.read", a.Root, a.Path)
		if err != nil {
			return nil, err
		}
		info, err := os.Stat(path)
		if err != nil {
			return nil, nil
		}
		entry := fsEntryOf(filepath.Base(path), path, info)
		return &entry, nil
	})
	// A new file holding text, and the folders it is in; refused when
	// something is there already. The check and the making are one step,
	// so two plugins making the same note can't both win.
	command("plugin_fs_create", func(c *Call, a struct {
		pluginFileArgs
		Text string `json:"text"`
	}) (nothing, error) {
		path, err := c.app.grantedPath(a.ID, "files.write", a.Root, a.Path)
		if err != nil {
			return nil, err
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, err
		}
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if errors.Is(err, fs.ErrExist) {
			return nil, fmt.Errorf("%s is already there.", path)
		} else if err != nil {
			return nil, err
		}
		_, err = f.WriteString(a.Text)
		if closeErr := f.Close(); err == nil {
			err = closeErr
		}
		return nil, err
	})
	// A text file and its version; "" and the empty file's version when it
	// isn't there yet.
	command("plugin_fs_read_versioned", func(c *Call, a pluginFileArgs) (VersionedText, error) {
		path, err := c.app.grantedPath(a.ID, "files.read", a.Root, a.Path)
		if err != nil {
			return VersionedText{}, err
		}
		if info, err := os.Stat(path); err == nil && info.Size() > maxPluginRead {
			return VersionedText{}, codeError("fileTooBig")
		}
		raw, err := readOrEmpty(path)
		if err != nil {
			return VersionedText{}, err
		}
		return VersionedText{Text: strings.ToValidUTF8(string(raw), "�"), Token: plugins.Token(raw)}, nil
	})
	// Writes text only if the file is still the version token names: false,
	// and nothing written, when it changed since it was read.
	command("plugin_fs_write_if", func(c *Call, a struct {
		pluginFileArgs
		Text  string `json:"text"`
		Token string `json:"token"`
	}) (bool, error) {
		path, err := c.app.grantedPath(a.ID, "files.write", a.Root, a.Path)
		if err != nil {
			return false, err
		}
		now, err := readOrEmpty(path)
		if err != nil {
			return false, err
		}
		if plugins.Token(now) != a.Token {
			return false, nil
		}
		return true, writeFileAtomic(path, []byte(a.Text))
	})
	// An Obsidian vault's own settings file, <vault>/.obsidian/<name>, as
	// text; nil when it isn't there. The one hidden folder plugins may read
	// from, and only a file at its top: a plugin's saved data lives deeper
	// and holds its tokens.
	command("plugin_app_settings", func(c *Call, a struct {
		ID    string `json:"id"`
		Vault string `json:"vault"`
		Name  string `json:"name"`
	}) (*string, error) {
		if !c.app.pluginState().Allows(a.ID, "files.read") {
			return nil, plugins.Refused(a.ID, "files.read")
		}
		if !plugins.SettingsFileName(a.Name) {
			return nil, fmt.Errorf("%s isn't a settings file's name.", a.Name)
		}
		path, err := plugins.Inside(a.Vault, filepath.Join(".obsidian", a.Name))
		if err != nil {
			return nil, err
		}
		if err := c.app.requireNotAppData(path); err != nil {
			return nil, err
		}
		raw, err := os.ReadFile(path)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			return nil, nil
		case err != nil:
			return nil, err
		case len(raw) > maxPluginRead:
			return nil, codeError("fileTooBig")
		}
		text := strings.ToValidUTF8(string(raw), "�")
		return &text, nil
	})
	// Every Markdown document in the folder, in the sidebar's order, with
	// its text.
	command("plugin_fs_documents", func(c *Call, a struct {
		ID   string `json:"id"`
		Root string `json:"root"`
	}) (WorkspaceDocuments, error) {
		if err := c.app.checkedRoot(a.ID, a.Root); err != nil {
			return WorkspaceDocuments{}, err
		}
		return documentsIn(a.Root), nil
	})
	// The folder's documents' lines holding query, ignoring case, in the
	// sidebar's order: at most limit (200 unless asked, and maxSearchHits).
	command("plugin_fs_search", func(c *Call, a struct {
		ID    string  `json:"id"`
		Root  string  `json:"root"`
		Query string  `json:"query"`
		Limit *uint32 `json:"limit"`
	}) (SearchResult, error) {
		if err := c.app.checkedRoot(a.ID, a.Root); err != nil {
			return SearchResult{}, err
		}
		limit := 200
		if a.Limit != nil {
			limit = int(*a.Limit)
		}
		limit = min(max(limit, 1), maxSearchHits)
		return searchIn(a.Root, a.Query, limit, time.Now().Add(searchTime)), nil
	})
}

// pluginTrashPath is the path a plugin may move to the Trash: granted
// files.write, inside root, not root or a folder holding it, and none of
// the app's own data.
func (a *App) pluginTrashPath(args pluginFileArgs) (string, error) {
	path, err := plugins.TrashPath(a.pluginState(), args.ID, args.Root, args.Path)
	if err != nil {
		return "", err
	}
	if err := a.requireNotAppData(path); err != nil {
		return "", err
	}
	return path, nil
}
