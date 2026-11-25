package plugins

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"
)

// A plugin's own data, in a global place and a project one (with
// project.data; with no folder open, a hidden one of the app's), each with
// keys (kv/, a file per key so a write touches one key) and files
// (files/), held to a quota.

const (
	// QuotaBytes is the most a plugin keeps in one place.
	QuotaBytes = 50 * 1024 * 1024
	// maxKeyBytes is the longest a key may be.
	maxKeyBytes = 200
)

// StoreScope is which of a plugin's two places: "global" or "project"
// (StoreScope in src/platform/bindings/plugin-access.ts).
type StoreScope string

const (
	ScopeGlobal  StoreScope = "global"
	ScopeProject StoreScope = "project"
)

// ProjectPlace is a plugin's place in the folder root.
func ProjectPlace(root, id string) string {
	return filepath.Join(root, ".textus", "plugins", id)
}

// GlobalPlace is a plugin's global place, in the global agent folder.
func GlobalPlace(global, id string) string { return filepath.Join(global, "plugins", id) }

// LooseDir is the project place of a window with no folder open: hidden
// beside the plugins' global places, under a name no plugin id can take.
func LooseDir(global, id string) string {
	return filepath.Join(global, "plugins", ".no-folder", id)
}

// Unlinked refuses a link anywhere from base down to path: a shared folder
// may hold one that leads a plugin's storage elsewhere on the disk.
func Unlinked(base, path string) error {
	rest, ok := within(path, base)
	if !ok {
		return errors.New("Outside the plugin's storage.")
	}
	at := filepath.Clean(base)
	for _, part := range parts(rest) {
		at = filepath.Join(at, part)
		if info, err := os.Lstat(at); err == nil && info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%s is a link; plugin storage doesn't follow links.", at)
		}
	}
	return nil
}

func isKeyByte(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || b == '-' || b == '_'
}

// KeyFile is a key as a file name: letters, digits, `-` and `_` kept,
// other bytes as `%XX`, so no key names a path and each comes back.
func KeyFile(key string) (string, error) {
	if key == "" || len(key) > maxKeyBytes {
		return "", fmt.Errorf("A key is 1 to %d bytes.", maxKeyBytes)
	}
	var b strings.Builder
	for i := 0; i < len(key); i++ {
		if c := key[i]; isKeyByte(c) {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	b.WriteString(".json")
	return b.String(), nil
}

func hexDigit(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	}
	return 0, false
}

// FileKey is the key a file name holds; false for no key's file.
func FileKey(name string) (string, bool) {
	stem, ok := strings.CutSuffix(name, ".json")
	if !ok {
		return "", false
	}
	var out []byte
	for i := 0; i < len(stem); i++ {
		if stem[i] != '%' {
			out = append(out, stem[i])
			continue
		}
		if i+2 >= len(stem) {
			return "", false
		}
		hi, ok1 := hexDigit(stem[i+1])
		lo, ok2 := hexDigit(stem[i+2])
		if !ok1 || !ok2 {
			return "", false
		}
		out = append(out, hi<<4|lo)
		i += 2
	}
	if !utf8.Valid(out) {
		return "", false
	}
	return string(out), true
}

// SizeOf is how much dir holds, every file in it.
func SizeOf(dir string) int64 {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	var total int64
	for _, e := range entries {
		if e.IsDir() {
			total += SizeOf(filepath.Join(dir, e.Name()))
		} else if info, err := e.Info(); err == nil {
			total += info.Size()
		}
	}
	return total
}

// WriteWithin writes data to path in place unless that takes it over the
// quota; write does the writing (atomically).
func WriteWithin(place, path string, data []byte, write func(path string, data []byte) error) error {
	var old int64
	if info, err := os.Stat(path); err == nil {
		old = info.Size()
	}
	// A file written again replaces itself: its old size doesn't count.
	if max(SizeOf(place)-old, 0)+int64(len(data)) > QuotaBytes {
		return errors.New("The plugin's storage is full (50 MB).")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return write(path, data)
}

// StoreGet is what the plugin kept under key in place, nil for nothing.
func StoreGet(place, key string) (jsontext.Value, error) {
	name, err := KeyFile(key)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(place, "kv", name)
	if err := Unlinked(place, path); err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil, nil
	case err != nil:
		return nil, err
	}
	var v jsontext.Value
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, err
	}
	return v, nil
}

// StoreSet keeps value under key in place; null takes the key away.
func StoreSet(place, key string, value jsontext.Value, write func(path string, data []byte) error) error {
	name, err := KeyFile(key)
	if err != nil {
		return err
	}
	path := filepath.Join(place, "kv", name)
	if err := Unlinked(place, path); err != nil {
		return err
	}
	if len(value) == 0 || value.Kind() == 'n' {
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return nil
	}
	compact := value.Clone()
	if err := compact.Compact(); err != nil {
		return err
	}
	return WriteWithin(place, path, compact, write)
}

// StoreKeys is every key kept in place, in order.
func StoreKeys(place string) []string {
	keys := []string{}
	entries, _ := os.ReadDir(filepath.Join(place, "kv"))
	for _, e := range entries {
		if key, ok := FileKey(e.Name()); ok {
			keys = append(keys, key)
		}
	}
	slices.Sort(keys)
	return keys
}

// FileIn is rel inside the place's files/, made first so a new one
// resolves.
func FileIn(place, rel string) (string, error) {
	files := filepath.Join(place, "files")
	if err := Unlinked(place, files); err != nil {
		return "", err
	}
	if err := os.MkdirAll(files, 0o755); err != nil {
		return "", err
	}
	path, err := Inside(files, rel)
	if err != nil {
		return "", err
	}
	if err := Unlinked(place, filepath.Join(files, rel)); err != nil {
		return "", err
	}
	return path, nil
}

// StoreFiles are the files under dir, as `/`-separated paths from it, in
// order.
func StoreFiles(dir string) []string {
	out := []string{}
	var walk func(at string)
	walk = func(at string) {
		entries, err := os.ReadDir(at)
		if err != nil {
			return
		}
		for _, e := range entries {
			path := filepath.Join(at, e.Name())
			if e.IsDir() {
				walk(path)
			} else if rel, ok := within(path, dir); ok {
				out = append(out, filepath.ToSlash(rel))
			}
		}
	}
	walk(dir)
	slices.Sort(out)
	return out
}
