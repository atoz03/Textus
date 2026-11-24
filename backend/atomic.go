package backend

import (
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
)

// writeAtomic replaces path with data whole: a reader, or a crash midway,
// sees the old file or the new one, never a part. The file is written beside
// its destination, synced, then renamed over it.
func writeAtomic(path string, data []byte) error {
	// A link is written through: the file it leads to changes, the link stays.
	if real, err := filepath.EvalSymlinks(path); err == nil {
		path = real
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	keep := false
	defer func() {
		if !keep {
			_ = os.Remove(name)
		}
	}()
	// A document keeps the permissions it had.
	if info, err := os.Stat(path); err == nil {
		_ = tmp.Chmod(info.Mode().Perm())
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(name, path); err != nil {
		return err
	}
	keep = true
	return nil
}

// writeFileAtomic is writeAtomic that first makes the folder path goes in.
func writeFileAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return writeAtomic(path, data)
}

// moveAside renames a file that does not parse to "<name>.corrupt-<n>", for
// recovery by hand, so the next write does not lose it.
func moveAside(path string) {
	for n := 1; ; n++ {
		candidate := fmt.Sprintf("%s.corrupt-%d", path, n)
		if _, err := os.Lstat(candidate); errors.Is(err, fs.ErrNotExist) {
			if err := os.Rename(path, candidate); err != nil {
				log.Printf("textus: cannot move aside %s: %v", path, err)
			} else {
				log.Printf("textus: %s is not valid; moved it to %s", path, candidate)
			}
			return
		}
	}
}
