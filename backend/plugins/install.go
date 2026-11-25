package plugins

import (
	"archive/zip"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Installing a plugin from a folder or a .textusplugin zip. A manifest is
// checked in full (Check) before a plugin is put in place or loaded;
// listing reads only its id and version, so Settings can still show what
// is wrong.

// MaxPackageBytes: a package bigger than this, unpacked, is refused.
const MaxPackageBytes = 200 * 1024 * 1024

// maxWrittenBytes is the most a plugin made in the conversation may hold,
// all files together.
const maxWrittenBytes = 2 * 1024 * 1024

// Installed is one installed plugin, as Settings › Plugins lists it
// (InstalledPlugin in src/platform/bindings/plugins.ts).
type Installed struct {
	ID string `json:"id"`
	// Dir is where its files are.
	Dir string `json:"dir"`
	// Manifest is textus-plugin.json as written.
	Manifest string `json:"manifest"`
	// Dev: loaded from a folder in developer mode, not copied in.
	Dev bool `json:"dev"`
}

// ReadManifest is a folder's manifest with its id checked: enough to list
// it. Answers the id and the text.
func ReadManifest(dir string) (string, string, error) {
	raw, err := os.ReadFile(filepath.Join(dir, Manifest))
	if err != nil {
		return "", "", fmt.Errorf("No %s in %s.", Manifest, dir)
	}
	var head struct {
		ID      *string `json:"id"`
		Version *string `json:"version"`
	}
	if err := json.Unmarshal(raw, &head); err != nil {
		return "", "", fmt.Errorf("%s: %v", Manifest, err)
	}
	switch {
	case head.ID == nil:
		return "", "", fmt.Errorf("%s: missing field `id`", Manifest)
	case head.Version == nil:
		return "", "", fmt.Errorf("%s: missing field `version`", Manifest)
	}
	if err := RequireID(*head.ID); err != nil {
		return "", "", err
	}
	if strings.TrimSpace(*head.Version) == "" {
		return "", "", fmt.Errorf("%s: no version.", Manifest)
	}
	return *head.ID, string(raw), nil
}

// ReadCheckedManifest is a folder's manifest checked in full: what
// installing or loading needs.
func ReadCheckedManifest(dir string) (string, string, error) {
	id, text, err := ReadManifest(dir)
	if err != nil {
		return "", "", err
	}
	if err := Check(text); err != nil {
		return "", "", fmt.Errorf("%s: %v", Manifest, err)
	}
	return id, text, nil
}

// AgentCommands are the programs a manifest's agents run:
// contributes.agents, as written.
func AgentCommands(manifest string) string {
	var m struct {
		Contributes struct {
			Agents jsontext.Value `json:"agents"`
		} `json:"contributes"`
	}
	if json.Unmarshal([]byte(manifest), &m) != nil || m.Contributes.Agents == nil {
		return ""
	}
	return compact(m.Contributes.Agents)
}

// PluginDirIn is the folder holding the manifest: dir itself, or its one
// subfolder.
func PluginDirIn(dir string) (string, error) {
	if isFile(filepath.Join(dir, Manifest)) {
		return dir, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	var subdirs []string
	for _, e := range entries {
		path := filepath.Join(dir, e.Name())
		if info, err := os.Stat(path); err == nil && info.IsDir() {
			subdirs = append(subdirs, path)
		}
	}
	if len(subdirs) == 1 && isFile(filepath.Join(subdirs[0], Manifest)) {
		return subdirs[0], nil
	}
	return "", Coded("pluginNoManifest")
}

func isFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

// CopyDir copies a plugin folder: not a developer's checkout's git folder,
// secrets or installed packages, and no link, which could point anywhere.
// total counts what was copied, against MaxPackageBytes.
func CopyDir(from, to string, total *int64) error {
	return copyTree(from, to, func(e fs.DirEntry) error {
		info, err := e.Info()
		if err != nil {
			return err
		}
		*total += info.Size()
		if *total > MaxPackageBytes {
			return Coded("pluginTooBig")
		}
		return nil
	})
}

// copyTree copies the plugin's files under from to to, folders and files
// alone; check, when given, sees each file first and may refuse it.
func copyTree(from, to string, check func(fs.DirEntry) error) error {
	if err := os.MkdirAll(to, 0o755); err != nil {
		return err
	}
	entries, err := os.ReadDir(from)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if NotPluginFile(e.Name()) {
			continue
		}
		source, target := filepath.Join(from, e.Name()), filepath.Join(to, e.Name())
		switch {
		case e.Type().IsDir():
			if err := copyTree(source, target, check); err != nil {
				return err
			}
		case e.Type().IsRegular():
			if check != nil {
				if err := check(e); err != nil {
					return err
				}
			}
			if err := copyFile(source, target); err != nil {
				return err
			}
		}
	}
	return nil
}

func copyFile(from, to string) error {
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(to, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// enclosedName is a zip entry's name as a path under the folder it is
// unpacked in; false for one that would land outside it.
func enclosedName(name string) (string, bool) {
	if strings.ContainsRune(name, 0) {
		return "", false
	}
	slashed := strings.ReplaceAll(name, "\\", "/")
	if strings.HasPrefix(slashed, "/") || filepath.IsAbs(name) || filepath.VolumeName(name) != "" {
		return "", false
	}
	var out []string
	for _, p := range strings.Split(slashed, "/") {
		switch p {
		case "", ".":
		case "..":
			return "", false
		default:
			out = append(out, p)
		}
	}
	return filepath.Join(out...), true
}

// Unzip unpacks a package into to.
func Unzip(file, to string) error {
	archive, err := zip.OpenReader(file)
	if err != nil {
		return errors.New("Not a plugin package (.textusplugin).")
	}
	defer archive.Close()
	var total int64
	for _, entry := range archive.File {
		rel, ok := enclosedName(entry.Name)
		if !ok {
			return Coded("pluginBadPackage")
		}
		// The folder Finder's Compress adds beside the plugin's own.
		if hasName(rel, "__MACOSX") {
			continue
		}
		target := filepath.Join(to, rel)
		if entry.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		n, err := unzipOne(entry, target, MaxPackageBytes+1-total)
		if err != nil {
			return err
		}
		total += n
		if total > MaxPackageBytes {
			return Coded("pluginTooBig")
		}
	}
	return nil
}

func hasName(rel, name string) bool {
	for _, p := range parts(rel) {
		if p == name {
			return true
		}
	}
	return false
}

func unzipOne(entry *zip.File, target string, most int64) (int64, error) {
	r, err := entry.Open()
	if err != nil {
		return 0, err
	}
	defer r.Close()
	out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return 0, err
	}
	n, err := io.Copy(out, io.LimitReader(r, most))
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	return n, err
}

// SwapIn puts the folder fresh at target: the version there is moved aside
// first and put back if the move in fails, so an update never leaves no
// version at all.
func SwapIn(fresh, target, aside string) error {
	had := Exists(target)
	if had {
		if err := os.Rename(target, aside); err != nil {
			return err
		}
	}
	if err := os.Rename(fresh, target); err != nil {
		// Put back; if that fails too, say where the one that worked is.
		if had && os.Rename(aside, target) != nil {
			return fmt.Errorf("%v (the previous version is at %s)", err, aside)
		}
		return err
	}
	if had {
		_ = os.RemoveAll(aside)
	}
	return nil
}

// File is a plugin's file as text, by path in the plugin, as Textus AI
// writes them (PluginFile in src/platform/bindings/plugins.ts).
type File struct {
	Path string `json:"path"`
	Text string `json:"text"`
}

// PlainRelative is whether path is a plain relative path inside the
// plugin: no root, no "..", no drive, nothing hidden.
func PlainRelative(path string) bool {
	if path == "" || strings.Contains(path, "\\") || filepath.IsAbs(path) || filepath.VolumeName(path) != "" {
		return false
	}
	for _, p := range strings.Split(path, "/") {
		if p == "" || p == "." || p == ".." || strings.HasPrefix(p, ".") {
			return false
		}
	}
	return true
}

// WrittenID is the id of a plugin written in the conversation: the user's
// own, `local.…`, so it never replaces anyone else's plugin.
func WrittenID(manifest string) (string, error) {
	var m struct {
		ID *string `json:"id"`
	}
	if json.Unmarshal([]byte(manifest), &m) != nil || m.ID == nil {
		return "", fmt.Errorf("%s has no id.", Manifest)
	}
	if !strings.HasPrefix(*m.ID, "local.") {
		return "", fmt.Errorf("A plugin written in the conversation needs an id starting with local. - %s is someone else's.", *m.ID)
	}
	return *m.ID, nil
}

// CheckWritten checks files written in the conversation before they are
// laid out: within the size, with a manifest, the user's own.
func CheckWritten(files []File) error {
	total := 0
	for _, f := range files {
		total += len(f.Text)
	}
	if total > maxWrittenBytes {
		return Coded("pluginTooBig")
	}
	for _, f := range files {
		if f.Path == Manifest {
			_, err := WrittenID(f.Text)
			return err
		}
	}
	return Coded("pluginNoManifest")
}

// LayOut writes files into folder, each checked to stay in it.
func LayOut(folder string, files []File) error {
	for _, f := range files {
		if !PlainRelative(f.Path) {
			return Coded("pluginBadPackage")
		}
		target := filepath.Join(folder, filepath.FromSlash(f.Path))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(target, []byte(f.Text), 0o644); err != nil {
			return err
		}
	}
	return nil
}
