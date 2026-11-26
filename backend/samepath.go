package backend

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// The checks that keep the app's own files and the user's credentials out
// of reach compare places, not spellings: macOS and Windows ignore case,
// macOS reaches its data volume through /System/Volumes/Data as well (a
// firmlink, which EvalSymlinks doesn't follow), and /var, /tmp or any
// folder may be a link. So a path is compared by name once resolved and
// spelled alike, and, where it exists, by identity (os.SameFile), which
// also covers what names can't: Unicode normalization on macOS, short
// names and junctions on Windows.

// resolvedPath is path made absolute, with the links in the part of it
// that exists followed; the rest is kept as named.
func resolvedPath(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return filepath.Clean(path)
	}
	var missing []string
	for p := abs; ; {
		if real, err := filepath.EvalSymlinks(p); err == nil {
			for i := len(missing) - 1; i >= 0; i-- {
				real = filepath.Join(real, missing[i])
			}
			return real
		}
		parent := filepath.Dir(p)
		if parent == p {
			return abs
		}
		missing = append(missing, filepath.Base(p))
		p = parent
	}
}

// macDataVolume is where macOS's firmlinks lead, in lower case:
// /Users is /System/Volumes/Data/Users.
const macDataVolume = "/system/volumes/data"

// spelledFor is a resolved path as goos compares names: in one case on
// macOS and Windows, and on macOS without the data volume's prefix.
func spelledFor(goos, path string) string {
	if goos != "darwin" && goos != "windows" {
		return path
	}
	path = strings.ToLower(path)
	if goos == "darwin" {
		if path == macDataVolume {
			return "/"
		}
		if rest, ok := strings.CutPrefix(path, macDataVolume+"/"); ok {
			return "/" + rest
		}
	}
	return path
}

// inAnyDir reports whether path is one of dirs or lies under one.
func inAnyDir(path string, dirs ...string) bool {
	p := resolvedPath(path)
	name := spelledFor(runtime.GOOS, p)
	var there []os.FileInfo
	for _, dir := range dirs {
		if dir == "" {
			continue
		}
		d := resolvedPath(dir)
		if _, ok := pathUnder(name, spelledFor(runtime.GOOS, d)); ok {
			return true
		}
		if info, err := os.Stat(d); err == nil {
			there = append(there, info)
		}
	}
	if len(there) == 0 {
		return false
	}
	// The folders holding path, path first: one of them is a dir by
	// identity when path is in it under another spelling.
	for {
		if info, err := os.Stat(p); err == nil {
			for _, dir := range there {
				if os.SameFile(info, dir) {
					return true
				}
			}
		}
		parent := filepath.Dir(p)
		if parent == p {
			return false
		}
		p = parent
	}
}

// samePath reports whether a and b name one file or folder.
func samePath(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	x, y := resolvedPath(a), resolvedPath(b)
	if spelledFor(runtime.GOOS, x) == spelledFor(runtime.GOOS, y) {
		return true
	}
	ix, errX := os.Stat(x)
	iy, errY := os.Stat(y)
	return errX == nil && errY == nil && os.SameFile(ix, iy)
}
