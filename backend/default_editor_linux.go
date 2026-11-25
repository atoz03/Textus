package backend

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"textus/backend/programs"
)

// applicationsDirs are the applications folders under $XDG_DATA_HOME, then
// each of $XDG_DATA_DIRS.
func applicationsDirs() []string {
	var dirs []string
	if home := os.Getenv("XDG_DATA_HOME"); home != "" {
		dirs = append(dirs, home)
	} else if home := os.Getenv("HOME"); home != "" {
		dirs = append(dirs, filepath.Join(home, ".local", "share"))
	}
	data := os.Getenv("XDG_DATA_DIRS")
	if data == "" {
		data = "/usr/local/share:/usr/share"
	}
	dirs = append(dirs, strings.Split(data, ":")...)
	for i, dir := range dirs {
		dirs[i] = filepath.Join(dir, "applications")
	}
	return dirs
}

// desktopID is Textus's desktop entry; none in an AppImage, whose entry
// from a packaged copy would open that copy, not this one.
func desktopID() (string, bool) {
	if os.Getenv("APPIMAGE") != "" {
		return "", false
	}
	return installedDesktopID(applicationsDirs(), func(path string) bool {
		info, err := os.Stat(path)
		return err == nil && info.Mode().IsRegular()
	})
}

func editorStatus(ctx context.Context) defaultEditorStatus {
	id, ok := desktopID()
	if !ok {
		return defaultEditorStatus{}
	}
	out, err := programs.Command(ctx, "xdg-mime", "query", "default", markdownMIMETypes[0]).Output()
	return defaultEditorStatus{Supported: true, IsDefault: err == nil && strings.TrimSpace(string(out)) == id}
}

func makeDefaultEditor(ctx context.Context) error {
	id, ok := desktopID()
	if !ok {
		return errors.New("no desktop entry for Textus is installed")
	}
	out, err := programs.Command(ctx, "xdg-mime", xdgMimeDefaultArgs(id)...).CombinedOutput()
	if err != nil {
		if said := strings.TrimSpace(string(out)); said != "" {
			return errors.New(said)
		}
		return errors.New("xdg-mime: " + err.Error())
	}
	return nil
}
