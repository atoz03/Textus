package backend

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// Importing into the reference library: files, a folder, or a zip of them,
// copied into the project's references/ folder. Only what the tools can
// read is kept; a zip is unpacked into a folder named after it, never
// outside it. Nothing already there is overwritten.

const (
	// maxImportedFiles are the most files one import brings in, and
	// maxUnpackedBytes the most bytes a zip may unpack to.
	maxImportedFiles = 2_000
	maxUnpackedBytes = 1 << 30
)

// importedReferences is what an import did (ImportedReferences in
// src/platform/bindings/ai-project.ts).
type importedReferences struct {
	// Imported are the files copied or unpacked into references/.
	Imported uint32 `json:"imported"`
	// Skipped are the files left out: not a kind the tools read, too
	// large, or past a limit.
	Skipped uint32 `json:"skipped"`
}

// readableName reports whether the tools can read a file named name: text
// or a document.
func readableName(name string) bool {
	k := fileKindOf(name)
	return k == kindText || k == kindDocument
}

// freePath is want, or `want (2)`, `want (3)`… - whichever is free.
func freePath(want string) string {
	dir := filepath.Dir(want)
	return filepath.Join(dir, freeName(dir, filepath.Base(want), numberedParen))
}

// referenceComponent is name as one safe path component.
func referenceComponent(name string) string {
	cleaned := strings.Map(func(c rune) rune {
		if strings.ContainsRune(`\/:*?"<>|`, c) {
			return '-'
		}
		return c
	}, name)
	cleaned = strings.TrimLeft(strings.TrimSpace(cleaned), ".")
	if cleaned == "" {
		return "references"
	}
	return cleaned
}

type referenceImporter struct {
	into string
	done importedReferences
}

func (im *referenceImporter) full() bool { return im.done.Imported >= maxImportedFiles }

// put writes rel under references/ with write.
func (im *referenceImporter) put(rel string, write func(to string) error) {
	if im.full() {
		im.done.Skipped++
		return
	}
	to := filepath.Join(im.into, rel)
	err := os.MkdirAll(filepath.Dir(to), 0o755)
	if err == nil {
		err = write(freePath(to))
	}
	if err != nil {
		im.done.Skipped++
		return
	}
	im.done.Imported++
}

func (im *referenceImporter) file(from, rel string) {
	if sizeOf(from) > maxSourceBytes {
		im.done.Skipped++
		return
	}
	im.put(rel, func(to string) error { return copyFile(from, to) })
}

// folder is a folder's readable files, kept in their subfolders under its
// name.
func (im *referenceImporter) folder(dir string) {
	name := referenceComponent(filepath.Base(dir))
	walked := walkReadableFiles(dir, false)
	for _, rel := range walked.files {
		im.file(filepath.Join(dir, rel), filepath.Join(name, rel))
	}
	if walked.truncated {
		im.done.Skipped++
	}
}

// enclosedName is a zip entry's name as a path that stays inside the
// folder it is unpacked into; false for an absolute name or one whose
// `..` would leave it.
func enclosedName(name string) (string, bool) {
	name = strings.ReplaceAll(name, `\`, "/")
	if name == "" || strings.ContainsRune(name, 0) || strings.HasPrefix(name, "/") || len(name) >= 2 && name[1] == ':' {
		return "", false
	}
	depth := 0
	for _, part := range strings.Split(name, "/") {
		switch part {
		case "", ".":
		case "..":
			if depth--; depth < 0 {
				return "", false
			}
		default:
			depth++
		}
	}
	return path.Clean(name), true
}

// hiddenEntry reports whether a zip entry's name has a hidden part, or
// macOS's resource forks: a `.` or `..` part counts, as the path's own
// components (a `.` inside the path is no part).
func hiddenEntry(name string) bool {
	for i, part := range strings.Split(strings.ReplaceAll(name, `\`, "/"), "/") {
		if part == "" || part == "." && i > 0 {
			continue
		}
		if part == "__MACOSX" || strings.HasPrefix(part, ".") {
			return true
		}
	}
	return false
}

// zip is a zip's readable entries, under a folder named after it. Entry
// names that would leave that folder (`../`, absolute) are skipped.
func (im *referenceImporter) zip(archive string) error {
	r, err := zip.OpenReader(archive)
	if err != nil && !errors.Is(err, zip.ErrInsecurePath) {
		return fmt.Errorf("Not a zip file Textus can open: %v", err)
	}
	defer r.Close()
	name := referenceComponent(fileStem(archive))
	var unpacked uint64
	for _, entry := range r.File {
		if entry.FileInfo().IsDir() || strings.HasSuffix(entry.Name, "/") {
			continue
		}
		rel, ok := enclosedName(entry.Name)
		if !ok {
			im.done.Skipped++
			continue
		}
		// macOS's resource forks and hidden files are not material.
		if hiddenEntry(entry.Name) || !readableName(rel) {
			im.done.Skipped++
			continue
		}
		size := entry.UncompressedSize64
		if size > maxSourceBytes || unpacked+size > maxUnpackedBytes {
			im.done.Skipped++
			continue
		}
		unpacked += size
		im.put(filepath.Join(name, filepath.FromSlash(rel)), func(to string) error {
			in, err := entry.Open()
			if err != nil {
				return err
			}
			defer in.Close()
			out, err := os.OpenFile(to, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
			if err != nil {
				return err
			}
			// Never more than the entry says, whatever the archive claims.
			if _, err := io.Copy(out, io.LimitReader(in, int64(size))); err != nil {
				out.Close()
				_ = os.Remove(to)
				return err
			}
			return out.Close()
		})
	}
	return nil
}

// importReferences imports sources (files, folders, zips) into root's
// references/ folder.
func importReferences(root string, sources []string) (importedReferences, error) {
	im := &referenceImporter{into: filepath.Join(root, referencesDir)}
	if err := os.MkdirAll(im.into, 0o755); err != nil {
		return im.done, err
	}
	for _, source := range sources {
		name := filepath.Base(source)
		info, statErr := os.Stat(source)
		switch {
		// The library itself, or a folder in it: already imported.
		case within(source, im.into):
			im.done.Skipped++
		case statErr == nil && info.IsDir():
			im.folder(source)
		case fileExt(source) == "zip":
			if err := im.zip(source); err != nil {
				return im.done, err
			}
		case readableName(name):
			im.file(source, referenceComponent(name))
		default:
			im.done.Skipped++
		}
	}
	return im.done, nil
}

// within reports whether path is dir or inside it, by its parts.
func within(path, dir string) bool {
	rel, err := filepath.Rel(filepath.Clean(dir), filepath.Clean(path))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}
