package backend

import (
	"bufio"
	"bytes"
	"cmp"
	"encoding/binary"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"
	"unicode/utf16"
)

// The tree's Trash with a way back: where each item went is kept, so an
// undo can put it back where it was. macOS says where it put an item
// (trash_place_mac.go); on Linux and Windows the Trash's own records are
// read once before a batch and once after it (trash_place_linux.go,
// trash_place_windows.go), each read here.

// eachUpTo gives each of paths to one in turn, each first allowed by may,
// up to one that fails: what each gave, and why the next one failed.
func eachUpTo[T any](paths []string, may func(string) error, one func(string) (T, error)) ([]T, error) {
	done := make([]T, 0, len(paths))
	for _, path := range paths {
		if err := may(path); err != nil {
			return done, err
		}
		v, err := one(path)
		if err != nil {
			return done, err
		}
		done = append(done, v)
	}
	return done, nil
}

// putBack puts the item the Trash holds at place back at path; refused when
// something is at path now ("exists") or the item is no longer in the Trash
// ("goneFromTrash"). Never over anything.
func putBack(place, path string) error {
	if _, err := os.Lstat(path); err == nil {
		return codeError("exists")
	}
	return putBackFrom(place, path)
}

// renameBack renames place back to path, in the folder it was in: the
// Trash is a folder on the same disk.
func renameBack(place, path string) error {
	if _, err := os.Lstat(place); err != nil {
		return codeError("goneFromTrash")
	}
	return os.Rename(place, path)
}

// held is an item the system's Trash holds, as its records give it.
type held struct {
	id   string // where it is kept: the place it is put back from
	name string // its name where it was
	// parent is the folder it came from, in the system's spelling.
	parent string
	time   int64 // when it went, to tell the newest
}

// folderAndName is a path's folder and name, its folder spelled one way:
// no `\\?\`, "/" between parts, and on Windows in one case.
func folderAndName(path string) (string, string) {
	path = strings.TrimPrefix(path, `\\?\`)
	path = strings.TrimRight(strings.ReplaceAll(path, `\`, "/"), "/")
	dir, name := "", path
	if i := strings.LastIndex(path, "/"); i >= 0 {
		dir, name = path[:i], path[i+1:]
	}
	if runtime.GOOS == "windows" {
		dir = strings.ToLower(dir)
	}
	return dir, name
}

// placesOf is where each path of went is kept among fresh, the items the
// Trash holds that it didn't before: the newest from its folder under its
// name, else under its name less the extension (a list that shows names as
// Explorer does); each item taken once.
func placesOf(went []string, fresh []held) []*string {
	fresh = slices.Clone(fresh)
	slices.SortStableFunc(fresh, func(a, b held) int { return cmp.Compare(b.time, a.time) })
	taken := make([]bool, len(fresh))
	find := func(dir, name string) *string {
		for i, item := range fresh {
			if parent, _ := folderAndName(item.parent + "/x"); !taken[i] && parent == dir && item.name == name {
				taken[i] = true
				return &fresh[i].id
			}
		}
		return nil
	}
	places := make([]*string, len(went))
	type wanted struct{ dir, name string }
	all := make([]wanted, len(went))
	for i, p := range went {
		all[i].dir, all[i].name = folderAndName(p)
		places[i] = find(all[i].dir, all[i].name)
	}
	for i, w := range all {
		if places[i] != nil {
			continue
		}
		if stem := strings.TrimSuffix(w.name, filepath.Ext(w.name)); stem != w.name && stem != "" {
			places[i] = find(w.dir, stem)
		}
	}
	return places
}

// trashInfo reads a freedesktop.org Trash's record of an item
// (info/<name>.trashinfo): the path it came from, relative ones under
// topdir, and when it went. Its paths are POSIX ones, whatever reads them.
func trashInfo(raw []byte, topdir string) (from string, at time.Time, ok bool) {
	section := false
	scan := bufio.NewScanner(bytes.NewReader(raw))
	for scan.Scan() {
		line := strings.TrimSpace(scan.Text())
		if strings.HasPrefix(line, "[") {
			section = line == "[Trash Info]"
			continue
		}
		key, value, found := strings.Cut(line, "=")
		if !section || !found {
			continue
		}
		switch strings.TrimSpace(key) {
		case "Path":
			p, err := url.PathUnescape(strings.TrimSpace(value))
			if err != nil {
				return "", time.Time{}, false
			}
			if !strings.HasPrefix(p, "/") {
				p = path.Join(topdir, p)
			}
			from = p
		case "DeletionDate":
			at, _ = time.ParseInLocation("2006-01-02T15:04:05", strings.TrimSpace(value), time.Local)
		}
	}
	return from, at, from != ""
}

// recycleRecord reads a Windows Recycle Bin's record of an item (its $I
// file, beside the item's $R file): the path it came from and when it
// went, as a FILETIME. Windows 10 and later write version 2, the path's
// length first; earlier ones version 1, a fixed MAX_PATH field.
func recycleRecord(raw []byte) (path string, at int64, ok bool) {
	if len(raw) < 24 {
		return "", 0, false
	}
	version := binary.LittleEndian.Uint64(raw)
	at = int64(binary.LittleEndian.Uint64(raw[16:]))
	var units []byte
	switch version {
	case 1:
		units = raw[24:min(len(raw), 24+520)]
	case 2:
		if len(raw) < 28 {
			return "", 0, false
		}
		n := int(binary.LittleEndian.Uint32(raw[24:]))
		if n <= 0 || 28+2*n > len(raw) {
			return "", 0, false
		}
		units = raw[28 : 28+2*n]
	default:
		return "", 0, false
	}
	text := make([]uint16, 0, len(units)/2)
	for i := 0; i+1 < len(units); i += 2 {
		u := binary.LittleEndian.Uint16(units[i:])
		if u == 0 {
			break
		}
		text = append(text, u)
	}
	if len(text) == 0 {
		return "", 0, false
	}
	return string(utf16.Decode(text)), at, true
}
