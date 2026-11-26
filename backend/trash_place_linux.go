//go:build linux && !android

package backend

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/egoist/mygo"
)

// Linux's Trash is the freedesktop.org one GLib keeps: the home Trash for
// an item on the home folder's disk, else one at the top of the item's own
// disk; each item in files/ with its record in info/.

// homeTrash is the home Trash's folder.
func homeTrash() string {
	data := os.Getenv("XDG_DATA_HOME")
	if data == "" {
		home, _ := os.UserHomeDir()
		data = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(data, "Trash")
}

func deviceOf(path string) (uint64, bool) {
	var st syscall.Stat_t
	if syscall.Stat(path, &st) != nil {
		return 0, false
	}
	return uint64(st.Dev), true
}

// topdirOf is the top of the disk dir is on: the highest folder above it on
// the device dev.
func topdirOf(dir string, dev uint64) string {
	top := dir
	for {
		up := filepath.Dir(top)
		if d, ok := deviceOf(up); up == top || !ok || d != dev {
			return top
		}
		top = up
	}
}

// trashesFor is the Trash folders paths can go to, as GLib chooses them,
// each with the disk top its records' paths are under ("" for the home
// Trash, whose records hold whole paths).
func trashesFor(paths []string) map[string]string {
	trashes := map[string]string{homeTrash(): ""}
	home, _ := os.UserHomeDir()
	homeDev, homeOK := deviceOf(home)
	uid := strconv.Itoa(os.Getuid())
	for _, path := range paths {
		dir := path
		if info, err := os.Lstat(path); err != nil || !info.IsDir() {
			dir = filepath.Dir(path)
		}
		dev, ok := deviceOf(dir)
		if !ok || (homeOK && dev == homeDev) {
			continue
		}
		top := topdirOf(dir, dev)
		trashes[filepath.Join(top, ".Trash", uid)] = top
		trashes[filepath.Join(top, ".Trash-"+uid)] = top
	}
	return trashes
}

// recordsIn is every record the Trash folders hold.
func recordsIn(trashes map[string]string) map[string]bool {
	seen := map[string]bool{}
	for trash := range trashes {
		entries, _ := os.ReadDir(filepath.Join(trash, "info"))
		for _, e := range entries {
			seen[filepath.Join(trash, "info", e.Name())] = true
		}
	}
	return seen
}

// heldIn is the items the Trash folders hold whose records aren't among
// before.
func heldIn(trashes map[string]string, before map[string]bool) []held {
	var fresh []held
	for trash, top := range trashes {
		entries, _ := os.ReadDir(filepath.Join(trash, "info"))
		for _, e := range entries {
			record := filepath.Join(trash, "info", e.Name())
			name, isRecord := strings.CutSuffix(e.Name(), ".trashinfo")
			if !isRecord || before[record] {
				continue
			}
			raw, err := os.ReadFile(record)
			if err != nil {
				continue
			}
			from, at, ok := trashInfo(raw, top)
			if !ok {
				continue
			}
			fresh = append(fresh, held{
				id:     filepath.Join(trash, "files", name),
				name:   filepath.Base(from),
				parent: filepath.Dir(from),
				time:   at.UnixNano(),
			})
		}
	}
	return fresh
}

func trashKeepingPlaces(paths []string, may func(string) error) ([]*string, error) {
	trashes := trashesFor(paths)
	before := recordsIn(trashes)
	done, err := eachUpTo(paths, may, func(path string) (struct{}, error) {
		return struct{}{}, mygo.Shell.TrashItem(path)
	})
	return placesOf(paths[:len(done)], heldIn(trashes, before)), err
}

// putBackFrom renames the item back out of the Trash's files/ and takes its
// record away; a place that isn't a Trash's item is one gone from it.
func putBackFrom(place, path string) error {
	files := filepath.Dir(place)
	record := filepath.Join(filepath.Dir(files), "info", filepath.Base(place)+".trashinfo")
	if filepath.Base(files) != "files" {
		return codeError("goneFromTrash")
	}
	if _, err := os.Lstat(record); err != nil {
		return codeError("goneFromTrash")
	}
	if err := renameBack(place, path); err != nil {
		return err
	}
	_ = os.Remove(record)
	return nil
}
