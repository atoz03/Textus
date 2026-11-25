package paper

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
)

// The file operations the store needs, on Textus's own data only: a file
// written whole, a folder copied whole, and a paper's folder moved.

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// writeAtomic replaces path with data whole: a reader, or a crash midway,
// sees the old file or the new one, never a part.
func writeAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	_, err = tmp.Write(data)
	if err == nil {
		err = tmp.Sync()
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(name, path)
	}
	if err != nil {
		_ = os.Remove(name)
	}
	return err
}

// copyWhole copies from (a file or a folder) to to, or leaves nothing at to
// when the copy fails. Something already at to is never copied over.
func copyWhole(from, to string) error {
	if exists(to) {
		return fs.ErrExist
	}
	if err := copyEntry(from, to); err != nil {
		_ = os.RemoveAll(to)
		return err
	}
	return nil
}

// copyEntry copies a file, or a folder and what is in it; links are left
// out.
func copyEntry(from, to string) error {
	info, err := os.Lstat(from)
	if err != nil {
		return err
	}
	switch {
	case info.IsDir():
		if err := os.Mkdir(to, 0o755); err != nil {
			return err
		}
		entries, err := os.ReadDir(from)
		if err != nil {
			return err
		}
		for _, e := range entries {
			if err := copyEntry(filepath.Join(from, e.Name()), filepath.Join(to, e.Name())); err != nil {
				return err
			}
		}
		return nil
	case !info.Mode().IsRegular():
		return nil
	}
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(to, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// moveOrCopy moves from to to, its folder made first; to another volume,
// it is copied whole and the original removed once the copy is.
func moveOrCopy(from, to string) error {
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		return err
	}
	err := os.Rename(from, to)
	if err == nil {
		return nil
	}
	if !crossesDevices(err) || exists(to) {
		return err
	}
	if err := copyWhole(from, to); err != nil {
		return err
	}
	return os.RemoveAll(from)
}

// crossesDevices reports whether a rename failed for going to another
// volume.
func crossesDevices(err error) bool {
	// ERROR_NOT_SAME_DEVICE, on Windows.
	const notSameDevice = syscall.Errno(17)
	return errors.Is(err, syscall.EXDEV) || runtime.GOOS == "windows" && errors.Is(err, notSameDevice)
}
