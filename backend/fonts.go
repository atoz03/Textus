package backend

import (
	"cmp"
	"context"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"
)

// The font families installed here, for Settings' Interface font and the
// document's text font (src/settings/sections/font-choices.ts). Each font
// file says its names, weight and slant in its `name`, `OS/2` and `post`
// tables, read here as the Rust builds (1.4.7 and before) read them with
// fontdb: from the system's font folders on macOS and Windows, and from the
// files fontconfig knows on Linux.

// FontFamily is an installed family: the name CSS knows it by, what it is
// called in the CJK languages that name it themselves (微软雅黑 for
// Microsoft YaHei), and its files. src/platform/bindings/app.ts FontFamily.
type FontFamily struct {
	Name  string      `json:"name"`
	Local []LocalName `json:"local"`
	Files []FontFile  `json:"files"`
}

// FontFile is a face's file, for an `@font-face`: WKWebView was seen not to
// find the fonts in ~/Library/Fonts by name.
type FontFile struct {
	Path   string `json:"path"`
	Weight int    `json:"weight"`
	Italic bool   `json:"italic"`
}

// LocalName is a family's name in a CJK language, its language spelled as
// Settings › Language spells it: "zh", "zh-Hant", "ja" or "ko".
type LocalName struct {
	Language string `json:"language"`
	Name     string `json:"name"`
}

// fontFamilies lists each installed family once, sorted as people read
// them. Only the Mac's web view needs the files: WebView2 and WebKitGTK
// find installed fonts by name.
func fontFamilies() []FontFamily {
	return familiesOf(readFontFiles(fontFiles()), runtime.GOOS == "darwin")
}

// fontFiles are the font files installed here: on Linux and the BSDs the
// files fontconfig lists, whose folders are configured (a distribution's,
// Flatpak's, a user's own); elsewhere, or without fc-list, the files in the
// usual font folders.
func fontFiles() []string {
	if runtime.GOOS != "darwin" && runtime.GOOS != "windows" {
		if files := fontconfigFiles(); len(files) > 0 {
			return files
		}
	}
	home, _ := os.UserHomeDir()
	return walkFontDirs(fontDirs(runtime.GOOS, os.Getenv, home))
}

// fontconfigFiles are the files fc-list lists, each once; none without it.
func fontconfigFiles() []string {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "fc-list", "--format", "%{file}\n").Output()
	if err != nil {
		return nil
	}
	var files []string
	for line := range strings.SplitSeq(string(out), "\n") {
		if path := strings.TrimSpace(line); path != "" {
			files = append(files, path)
		}
	}
	slices.Sort(files)
	return slices.Compact(files)
}

// fontDirs are the folders fonts are installed in on goos, with getenv and
// home the environment's.
func fontDirs(goos string, getenv func(string) string, home string) []string {
	var dirs []string
	switch goos {
	case "darwin":
		dirs = []string{"/System/Library/Fonts", "/Library/Fonts", "/Network/Library/Fonts"}
		// The fonts macOS downloads when they are first wanted, PingFang
		// among them: a folder per version of its catalog.
		assets, _ := filepath.Glob("/System/Library/AssetsV2/com_apple_MobileAsset_Font*")
		dirs = append(dirs, assets...)
		if home != "" {
			dirs = append(dirs, filepath.Join(home, "Library", "Fonts"))
		}
	case "windows":
		dirs = []string{filepath.Join(cmp.Or(getenv("WINDIR"), getenv("SystemRoot"), `C:\Windows`), "Fonts")}
		// Fonts installed for one user only.
		for _, appData := range []string{getenv("LOCALAPPDATA"), getenv("APPDATA")} {
			if appData != "" {
				dirs = append(dirs, filepath.Join(appData, "Microsoft", "Windows", "Fonts"))
			}
		}
	default:
		data := getenv("XDG_DATA_HOME")
		if data == "" && home != "" {
			data = filepath.Join(home, ".local", "share")
		}
		if data != "" {
			dirs = append(dirs, filepath.Join(data, "fonts"))
		}
		if home != "" {
			dirs = append(dirs, filepath.Join(home, ".fonts"))
		}
		for dir := range strings.SplitSeq(cmp.Or(getenv("XDG_DATA_DIRS"), "/usr/local/share:/usr/share"), ":") {
			if dir != "" {
				dirs = append(dirs, filepath.Join(dir, "fonts"))
			}
		}
	}
	return dirs
}

// A walk goes deep enough for macOS's downloaded fonts
// (com_apple_MobileAsset_Font7/<id>.asset/AssetData/), not round a folder
// that leads back into itself, and stops at a number no system has.
const (
	maxFontDepth = 6
	maxFontFiles = 20_000
)

// walkFontDirs are the font files in dirs and the folders inside them,
// each once, sorted. A link is followed to a file, never to a folder.
func walkFontDirs(dirs []string) []string {
	seen := map[string]bool{}
	var files []string
	for _, dir := range dirs {
		dir = filepath.Clean(dir)
		_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			switch {
			case len(files) >= maxFontFiles:
				return fs.SkipAll
			case err != nil:
				// An unreadable folder costs its own fonts only.
				return nil
			case d.IsDir():
				if rel, err := filepath.Rel(dir, path); err == nil && strings.Count(rel, string(filepath.Separator)) >= maxFontDepth {
					return fs.SkipDir
				}
				return nil
			case !isFontFile(path) || seen[path]:
				return nil
			case d.Type()&fs.ModeSymlink != 0:
				if info, err := os.Stat(path); err != nil || !info.Mode().IsRegular() {
					return nil
				}
			case !d.Type().IsRegular():
				return nil
			}
			seen[path] = true
			files = append(files, path)
			return nil
		})
	}
	slices.Sort(files)
	return files
}

// isFontFile: a TrueType or OpenType font, or a collection of them, by its
// name.
func isFontFile(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".ttf", ".otf", ".ttc", ".otc":
		return true
	}
	return false
}

// fontEntry is a face and the file it is in.
type fontEntry struct {
	path string
	face fontFace
}

// readFontFiles reads the faces of files, a few files at a time, in the
// files' order. A file that isn't a font, or is damaged, adds none.
func readFontFiles(files []string) []fontEntry {
	faces := make([][]fontFace, len(files))
	next := make(chan int)
	var wg sync.WaitGroup
	for range min(8, runtime.NumCPU()) {
		wg.Go(func() {
			for i := range next {
				faces[i], _ = readFaces(files[i])
			}
		})
	}
	for i := range files {
		next <- i
	}
	close(next)
	wg.Wait()
	var entries []fontEntry
	for i, list := range faces {
		for _, face := range list {
			entries = append(entries, fontEntry{path: files[i], face: face})
		}
	}
	return entries
}

// familiesOf groups faces by family, each family once by the name CSS
// knows it (the first spelling met), sorted as people read them; hidden
// system families (macOS's ".SF NS" and the like) are left out. withFiles
// gives a face its file when an `@font-face` can name it: a face alone in a
// .ttf or .otf, not one inside a collection.
func familiesOf(entries []fontEntry, withFiles bool) []FontFamily {
	at := map[string]int{}
	list := []FontFamily{}
	for _, e := range entries {
		name := e.face.family
		if name == "" || strings.HasPrefix(name, ".") {
			continue
		}
		key := strings.ToLower(name)
		i, ok := at[key]
		if !ok {
			i = len(list)
			at[key] = i
			list = append(list, FontFamily{Name: name, Local: []LocalName{}, Files: []FontFile{}})
		}
		family := &list[i]
		for _, local := range e.face.local {
			if !slices.ContainsFunc(family.Local, func(l LocalName) bool { return l.Language == local.Language }) {
				family.Local = append(family.Local, local)
			}
		}
		ext := strings.ToLower(filepath.Ext(e.path))
		if withFiles && e.face.index == 0 && (ext == ".ttf" || ext == ".otf") {
			family.Files = append(family.Files, FontFile{Path: e.path, Weight: e.face.weight, Italic: e.face.italic})
		}
	}
	slices.SortFunc(list, func(a, b FontFamily) int {
		return cmp.Or(strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)), strings.Compare(a.Name, b.Name))
	})
	return list
}
