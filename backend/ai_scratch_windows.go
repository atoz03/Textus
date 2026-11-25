//go:build windows

package backend

import "io/fs"

// ownedPrivately: the temporary folder is the user's own on Windows.
func ownedPrivately(fs.FileInfo) bool { return true }
