//go:build windows

package backend

import (
	"os"
	"os/user"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egoist/mygo"
)

// Windows' Recycle Bin keeps the items of a drive in its
// $Recycle.Bin\<the user's SID>: each as $R<id><ext>, beside its record
// $I<id><ext>.

// recycleBinsFor is the user's Recycle Bin folder on each drive of paths;
// a network share has none.
func recycleBinsFor(paths []string) []string {
	u, err := user.Current()
	if err != nil {
		return nil
	}
	var bins []string
	for _, path := range paths {
		drive := filepath.VolumeName(path)
		if len(drive) != 2 || drive[1] != ':' {
			continue
		}
		bin := strings.ToUpper(drive) + `\$Recycle.Bin\` + u.Uid
		if !slices.Contains(bins, bin) {
			bins = append(bins, bin)
		}
	}
	return bins
}

// recordsIn is every record the Recycle Bin folders hold.
func recordsIn(bins []string) map[string]bool {
	seen := map[string]bool{}
	for _, bin := range bins {
		entries, _ := os.ReadDir(bin)
		for _, e := range entries {
			seen[filepath.Join(bin, e.Name())] = true
		}
	}
	return seen
}

// heldIn is the items the Recycle Bin folders hold whose records aren't
// among before.
func heldIn(bins []string, before map[string]bool) []held {
	var fresh []held
	for _, bin := range bins {
		entries, _ := os.ReadDir(bin)
		for _, e := range entries {
			record := filepath.Join(bin, e.Name())
			id, isRecord := strings.CutPrefix(e.Name(), "$I")
			if !isRecord || before[record] {
				continue
			}
			raw, err := os.ReadFile(record)
			if err != nil {
				continue
			}
			from, at, ok := recycleRecord(raw)
			if !ok {
				continue
			}
			fresh = append(fresh, held{
				id:     filepath.Join(bin, "$R"+id),
				name:   filepath.Base(from),
				parent: filepath.Dir(from),
				time:   at,
			})
		}
	}
	return fresh
}

func trashKeepingPlaces(paths []string, may func(string) error) ([]*string, error) {
	bins := recycleBinsFor(paths)
	before := recordsIn(bins)
	done, err := eachUpTo(paths, may, func(path string) (struct{}, error) {
		return struct{}{}, mygo.Shell.TrashItem(path)
	})
	return placesOf(paths[:len(done)], heldIn(bins, before)), err
}

// putBackFrom renames the item back out of the Recycle Bin and takes its
// record away, as Restore does; a place that isn't a Recycle Bin's item is
// one gone from it.
func putBackFrom(place, path string) error {
	bin := filepath.Dir(place)
	id, isItem := strings.CutPrefix(filepath.Base(place), "$R")
	if !isItem || !strings.EqualFold(filepath.Base(filepath.Dir(bin)), "$Recycle.Bin") {
		return codeError("goneFromTrash")
	}
	record := filepath.Join(bin, "$I"+id)
	if _, err := os.Lstat(record); err != nil {
		return codeError("goneFromTrash")
	}
	if err := renameBack(place, path); err != nil {
		return err
	}
	_ = os.Remove(record)
	return nil
}
