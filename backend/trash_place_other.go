//go:build !windows && (!linux || android) && (!darwin || ios)

package backend

import "github.com/egoist/mygo"

// Elsewhere nothing says where an item went in the Trash: none comes back.

func trashKeepingPlaces(paths []string, may func(string) error) ([]*string, error) {
	return eachUpTo(paths, may, func(path string) (*string, error) {
		return nil, mygo.Shell.TrashItem(path)
	})
}

func putBackFrom(string, string) error { return codeError("goneFromTrash") }
