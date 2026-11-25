package plugins

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
)

// Where a plugin's file paths may lead: inside the open folder, past no
// link out of it, into nothing hidden and no other plugin's storage.

// canonical is path made absolute with every link in it followed.
func canonical(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(abs)
}

func isSep(r rune) bool { return r == '/' || runtime.GOOS == "windows" && r == '\\' }

// parts are path's names, without empty ones or ".".
func parts(path string) []string {
	var out []string
	for _, p := range strings.FieldsFunc(path, isSep) {
		if p != "." {
			out = append(out, p)
		}
	}
	return out
}

// within is path's part under dir, compared name by name; false when path
// is neither dir nor under it.
func within(path, dir string) (string, bool) {
	path, dir = filepath.Clean(path), filepath.Clean(dir)
	if path == dir {
		return "", true
	}
	prefix := dir
	if !strings.HasSuffix(prefix, string(filepath.Separator)) {
		prefix += string(filepath.Separator)
	}
	rest, ok := strings.CutPrefix(path, prefix)
	return rest, ok
}

// Inside is path resolved inside root: no "..", and still under root after
// following the links in the part that exists; the rest is kept as named.
func Inside(root, path string) (string, error) {
	base, err := canonical(root)
	if err != nil {
		return "", fmt.Errorf("%s: %v", root, err)
	}
	full := path
	if !filepath.IsAbs(path) {
		full = filepath.Join(base, path)
	}
	for _, p := range strings.FieldsFunc(path, isSep) {
		if p == ".." {
			return "", errors.New("A path with \"..\" is refused.")
		}
	}
	full = filepath.Clean(full)
	existing := full
	var missing []string
	for {
		// A link is there even when it leads nowhere: it is followed, and a
		// link that leads nowhere is refused, never written through.
		if _, err := os.Lstat(existing); err == nil {
			break
		}
		parent := filepath.Dir(existing)
		if parent == existing {
			break
		}
		missing = append(missing, filepath.Base(existing))
		existing = parent
	}
	resolved, err := canonical(existing)
	if err != nil {
		return "", err
	}
	for i := len(missing) - 1; i >= 0; i-- {
		resolved = filepath.Join(resolved, missing[i])
	}
	if _, ok := within(resolved, base); !ok {
		return "", fmt.Errorf("%s is outside the folder.", full)
	}
	return resolved, nil
}

// obsidianSettings are Obsidian's settings a vault shares between
// devices: no secrets, nothing that runs (community-plugins.json and
// plugins/ stay shut).
var obsidianSettings = []string{
	"app.json", "appearance.json", "hotkeys.json", "bookmarks.json", "graph.json", "core-plugins.json", "types.json",
}

func obsidianSetting(rel []string) bool {
	return len(rel) == 2 && rel[0] == ".obsidian" && slices.Contains(obsidianSettings, rel[1])
}

// InFolder is path resolved inside the open folder root: as Inside, and in
// no hidden folder or file of it - .git's hooks and config, an editor's or
// another app's settings, .env - which hold tokens, or which a write could
// turn into code that runs. The folder's own .textus is the user's writing
// (instructions, library, memory, skills), which a sync carries; nothing
// hidden inside it. Obsidian's shared settings are open too.
func InFolder(root, path string) (string, error) {
	resolved, err := Inside(root, path)
	if err != nil {
		return "", err
	}
	base, err := canonical(root)
	if err != nil {
		return "", err
	}
	hidden := true
	if rest, ok := within(resolved, base); ok {
		rel := parts(rest)
		hidden = false
		if !obsidianSetting(rel) {
			for i, n := range rel {
				if strings.HasPrefix(n, ".") && !(i == 0 && n == ".textus") {
					hidden = true
				}
			}
		}
	}
	if hidden {
		return "", errors.New("Files and folders whose names start with \".\" aren't open to plugins.")
	}
	return resolved, nil
}

// PluginPath is path in the open folder for plugin id: as InFolder, and
// not in another plugin's storage there (.textus/plugins/<other>/).
func PluginPath(root, path, id string) (string, error) {
	resolved, err := InFolder(root, path)
	if err != nil {
		return "", err
	}
	base, err := canonical(root)
	if err != nil {
		return "", err
	}
	rest, ok := within(resolved, base)
	if !ok {
		rest = resolved
	}
	if rel := parts(rest); len(rel) >= 3 && rel[0] == ".textus" && rel[1] == "plugins" && rel[2] != id {
		return "", errors.New("Another plugin's files aren't open to this one.")
	}
	return resolved, nil
}

// readOnlyHidden are hidden files other apps keep at a folder's top that
// hold no secrets and that plugins read for them: Obsidian Vim's settings,
// WakaTime's project name, the Git branch checked out, what Git leaves
// out. Read only; the rest of .git stays shut.
var readOnlyHidden = []string{".obsidian.vimrc", ".wakatime-project", ".git/HEAD", ".gitignore"}

// Relative is path from root, with forward slashes; whole if it isn't
// under it.
func Relative(root, path string) string {
	slashed := strings.ReplaceAll(path, "\\", "/")
	root = strings.TrimRight(strings.ReplaceAll(root, "\\", "/"), "/")
	if rest, ok := strings.CutPrefix(slashed, root); ok {
		return strings.TrimLeft(rest, "/")
	}
	return slashed
}

// ReadOnlyHidden is whether path (from root, or whole) is one of the
// hidden files every plugin reads.
func ReadOnlyHidden(root, path string) bool {
	return slices.Contains(readOnlyHidden, Relative(root, path))
}

// neverHidden are hidden names never open to a plugin, wherever they are:
// keys, tokens, what runs, other plugins' storage. One rule with the
// frontend's (src/plugins/store/hidden-files.ts).
var neverHidden = regexp.MustCompile(`(?i)^(?:\.git|\.git-credentials|\.ssh|\.aws|\.gnupg|\.docker|\.kube|\.config|\.textus|\.env.*|\.netrc|\.npmrc|\.pypirc|\.pgpass|\.htpasswd|\.vault-token|\.terraformrc|.*_history|\.?mcp\.json|credentials.*|.*\.(?:pem|key|p12|pfx))$`)

// HiddenFilePath is whether a plugin may ask for `hidden:<path>`: a file
// under a hidden folder or a hidden file of the open folder, named from
// its top, none of whose parts is a secret's.
func HiddenFilePath(path string) bool {
	if len(path) > 200 {
		return false
	}
	for _, c := range path {
		if !(isASCIIAlnum(c) || strings.ContainsRune("_.- /", c)) {
			return false
		}
	}
	names := strings.Split(path, "/")
	if !strings.HasPrefix(names[0], ".") {
		return false
	}
	for _, p := range names {
		if p == "" || p == "." || p == ".." || neverHidden.MatchString(p) {
			return false
		}
	}
	return true
}

// NotPluginFile is a name that is no part of a plugin: hidden (.env, .git)
// or node_modules. What its folder serves, lists, installs and exports
// leaves these out.
func NotPluginFile(name string) bool {
	return strings.HasPrefix(name, ".") || name == "node_modules"
}

// HiddenPart is whether rel passes through such a name (.env,
// .git/config).
func HiddenPart(rel string) bool {
	return slices.ContainsFunc(parts(rel), NotPluginFile)
}

// FolderPath is path for plugin id to use with permission: granted, in the
// folder, nothing hidden but what every plugin reads and the hidden files
// this one was granted by name (hidden:<path>), those only to read. The
// app's own data is the caller's to keep out.
func FolderPath(s State, id, permission, root, path string) (string, error) {
	if !s.Allows(id, permission) {
		return "", Refused(id, permission)
	}
	openHidden := func(rel string) bool {
		return ReadOnlyHidden("", rel) || HiddenFilePath(rel) && s.Allows(id, "hidden:"+rel)
	}
	if permission != "files.read" || !openHidden(Relative(root, path)) {
		return PluginPath(root, path, id)
	}
	resolved, err := Inside(root, path)
	if err != nil {
		return "", err
	}
	// A link there leads to what plugins may read anyway, never to another
	// hidden file (.env).
	base, err := canonical(root)
	if err != nil {
		return "", err
	}
	rest, ok := within(resolved, base)
	if !ok {
		rest = resolved
	}
	if openHidden(strings.Join(parts(rest), "/")) {
		return resolved, nil
	}
	return PluginPath(root, resolved, id)
}

// MayTrash refuses to move the folder shown, or a folder holding it, to
// the Trash.
func MayTrash(path, shown string) error {
	if shown == "" {
		return nil
	}
	root, err := canonical(shown)
	if err != nil {
		return nil
	}
	if _, under := within(root, path); under {
		return Coded("trashShownFolder")
	}
	return nil
}

// TrashPath is path as plugin id may move it to the Trash: granted
// files.write, inside root and not root itself or a folder holding it.
func TrashPath(s State, id, root, path string) (string, error) {
	resolved, err := FolderPath(s, id, "files.write", root, path)
	if err != nil {
		return "", err
	}
	if err := MayTrash(resolved, root); err != nil {
		return "", err
	}
	return resolved, nil
}

// Exists is whether something is at path, a link that leads nowhere too.
func Exists(path string) bool {
	_, err := os.Lstat(path)
	return !errors.Is(err, fs.ErrNotExist)
}

// Canonical is path made absolute with every link in it followed, as the
// checks above compare it.
func Canonical(path string) (string, error) { return canonical(path) }
