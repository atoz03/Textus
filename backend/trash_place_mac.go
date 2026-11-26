//go:build darwin && !ios

package backend

import "github.com/egoist/mygo"

// macOS says where it put an item in the Trash, under the name it took
// there ("plan 2.md" beside another plan.md).

func trashKeepingPlaces(paths []string, may func(string) error) ([]*string, error) {
	return eachUpTo(paths, may, func(path string) (*string, error) {
		place, err := mygo.Shell.TrashItemResult(path)
		if err != nil || place == "" {
			return nil, err
		}
		return &place, nil
	})
}

// putBackFrom: the Trash is a folder on the item's disk, so a rename puts
// it back.
func putBackFrom(place, path string) error { return renameBack(place, path) }
