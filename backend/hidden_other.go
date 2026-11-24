//go:build !windows

package backend

import "io/fs"

// markedHidden: only a name starting with a dot hides a file here.
func markedHidden(fs.FileInfo) bool { return false }
