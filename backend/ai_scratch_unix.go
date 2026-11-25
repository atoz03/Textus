//go:build !windows

package backend

import (
	"io/fs"
	"os"
	"syscall"
)

// ownedPrivately reports whether a folder is the user's own and closed to
// others.
func ownedPrivately(info fs.FileInfo) bool {
	st, ok := info.Sys().(*syscall.Stat_t)
	return ok && int(st.Uid) == os.Geteuid() && info.Mode().Perm()&0o077 == 0
}
