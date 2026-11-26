package backend

import (
	"log"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
)

// What a launch opens, and what the system hands over later: documents,
// folders and the last session's windows, each waiting for the window
// built for it.

// launchOpens decides what the launch opens: the documents it was given,
// else the last session's windows.
func (a *App) launchOpens(args []string, cwd string) (paths []string, windows []WindowSession) {
	paths = pathsFromArgv(args, cwd)
	a.noteArgvPaths(paths)
	a.mu.Lock()
	paths = append(paths, a.launchOpened...)
	a.launchOpened = nil
	a.launchDone = true
	a.mu.Unlock()
	if len(paths) == 0 && a.readFlag("restoreSessionOnStartup") {
		windows = a.readSessionWindows()
	}
	a.openingAtLaunch.Store(len(paths) > 0 || len(windows) > 0)
	return paths, windows
}

// pathsFromArgv is the documents a command line names, as absolute paths.
func pathsFromArgv(args []string, cwd string) []string {
	var out []string
	for i, arg := range args {
		if i == 0 || strings.HasPrefix(arg, "-") || strings.HasPrefix(strings.ToLower(arg), "textus://") {
			continue
		}
		// Linux launchers may pass a file URL.
		if u, err := url.Parse(arg); err == nil && u.Scheme == "file" {
			arg = filepath.FromSlash(u.Path)
			if runtime.GOOS == "windows" {
				arg = strings.TrimPrefix(arg, `\`)
			}
		}
		out = append(out, resolveAgainst(cwd, arg))
	}
	return out
}

// windowRestores says whether a window opens with something to restore,
// whose first frame is then held back (startup-restore.ts).
func (a *App) windowRestores(label string) bool {
	a.mu.Lock()
	queued := len(a.pending) > 0
	_, own := a.pendingSessions[label]
	help := a.pendingShowHelp != nil
	a.mu.Unlock()
	launching := a.openingAtLaunch.Load() && !a.uiReady.Load()
	drafts := label == "main" && a.draftsWaiting()
	return queued || own || launching || help || drafts
}

func (a *App) queuePending(paths []string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, p := range paths {
		a.pending = append(a.pending, sessionOfPaths([]string{p}))
	}
}

// queueWindowsToOpen opens a window for each session, where it was left:
// launching, the first window, built already where the first session's
// was, takes that one.
func (a *App) queueWindowsToOpen(windows []WindowSession) {
	if len(windows) == 0 {
		return
	}
	if !a.uiReady.Load() {
		a.mu.Lock()
		a.pending = append(a.pending, windows[0])
		a.mu.Unlock()
		windows = windows[1:]
	}
	for _, session := range windows {
		label := a.nextWindowLabel()
		a.mu.Lock()
		if a.pendingSessions == nil {
			a.pendingSessions = map[string]WindowSession{}
		}
		a.pendingSessions[label] = session
		a.mu.Unlock()
		if err := a.buildWindowAsLeft(label, session.Frame); err != nil {
			log.Printf("textus: cannot open a window: %v", err)
			a.takeSessionFor(label)
		}
	}
}

// takeSessionFor is the session the window label was built for, once.
func (a *App) takeSessionFor(label string) (WindowSession, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	session, ok := a.pendingSessions[label]
	delete(a.pendingSessions, label)
	return session, ok
}

// queuePathsToOpen opens documents and folders handed to the app: folders
// each in a window, documents as tabs of the focused window, or, still
// launching, of the window that mounts first.
func (a *App) queuePathsToOpen(paths []string) {
	var files, folders []string
	for _, p := range paths {
		if info, err := os.Stat(p); err == nil && info.IsDir() {
			folders = append(folders, p)
		} else {
			files = append(files, p)
		}
	}
	ready := a.uiReady.Load()
	if len(folders) > 0 {
		if ready {
			for _, f := range folders {
				a.openFolderWindow(f)
			}
		} else {
			windows := make([]WindowSession, len(folders))
			for i, f := range folders {
				windows[i] = WindowSession{Folder: &f}
			}
			windows[0].Paths = files
			a.queueWindowsToOpen(windows)
			return
		}
	}
	if len(files) == 0 {
		return
	}
	if ready {
		if label := a.focusedEditorWindow(""); label != "" {
			_ = a.emitTo(label, "open-paths-as-tabs", files)
			if w := a.window(label); w != nil {
				w.win.Restore()
				w.win.Focus()
			}
			return
		}
	}
	a.mu.Lock()
	pendingEmpty := len(a.pending) == 0
	a.mu.Unlock()
	if pendingEmpty && (!ready || a.lastEditor() == nil) && a.readFlag("restoreSessionOnStartup") {
		if saved := a.readSessionWindows(); len(saved) > 0 {
			a.queueWindowsToOpen(withDocuments(saved, files))
			return
		}
	}
	a.mu.Lock()
	a.pending = append(a.pending, sessionOfPaths(files))
	a.mu.Unlock()
	if ready {
		if err := a.openNewWindow(); err != nil {
			log.Printf("textus: cannot open a window: %v", err)
		}
	}
}

// openFolderWindow opens a window whose sidebar shows path.
func (a *App) openFolderWindow(path string) {
	label := a.nextWindowLabel()
	a.mu.Lock()
	a.pendingFolders = append(a.pendingFolders, [2]string{label, path})
	a.mu.Unlock()
	if err := a.buildEditorWindow(label, nil, false, nil); err != nil {
		a.takeFolderFor(label)
	}
}

func (a *App) takeFolderFor(label string) *string {
	a.mu.Lock()
	defer a.mu.Unlock()
	for i, pair := range a.pendingFolders {
		if pair[0] == label {
			a.pendingFolders = slices.Delete(a.pendingFolders, i, i+1)
			return &pair[1]
		}
	}
	return nil
}

// openedFromSystem takes documents the system opens with the app (Finder,
// a second launch): kept for the launch while it is under way.
func (a *App) openedFromSystem(paths []string) {
	a.mu.Lock()
	if !a.launchDone {
		a.launchOpened = append(a.launchOpened, paths...)
		a.mu.Unlock()
		return
	}
	a.mu.Unlock()
	a.queuePathsToOpen(paths)
}

func init() {
	command("take_pending_window", func(c *Call, _ none) (*WindowSession, error) {
		c.app.uiReady.Store(true)
		if w, ok := c.app.takeSessionFor(c.Label()); ok {
			if w.Paths == nil {
				w.Paths = []string{}
			}
			return &w, nil
		}
		c.app.mu.Lock()
		defer c.app.mu.Unlock()
		if len(c.app.pending) == 0 {
			return nil, nil
		}
		w := c.app.pending[0]
		c.app.pending = c.app.pending[1:]
		if w.Paths == nil {
			w.Paths = []string{}
		}
		return &w, nil
	})
	command("take_pending_show_help", func(c *Call, _ none) (*string, error) {
		c.app.mu.Lock()
		defer c.app.mu.Unlock()
		help := c.app.pendingShowHelp
		c.app.pendingShowHelp = nil
		return help, nil
	})
	command("take_pending_menu_event", func(c *Call, _ none) (*string, error) {
		c.app.mu.Lock()
		defer c.app.mu.Unlock()
		ev := c.app.pendingMenuEvent
		c.app.pendingMenuEvent = nil
		return ev, nil
	})
	command("take_pending_open_folder", func(c *Call, _ none) (*string, error) {
		return c.app.takeFolderFor(c.Label()), nil
	})
	command("open_paths", func(c *Call, a struct {
		Paths []string `json:"paths"`
	}) (nothing, error) {
		c.app.queuePathsToOpen(a.Paths)
		return nil, nil
	})
	command("open_path_in_new_window", func(c *Call, a struct {
		Path string `json:"path"`
	}) (nothing, error) {
		c.app.queuePending([]string{a.Path})
		return nil, c.app.openNewWindow()
	})
	command("open_folder_in_new_window", func(c *Call, a struct {
		Path string `json:"path"`
	}) (nothing, error) {
		c.app.openFolderWindow(a.Path)
		return nil, nil
	})
}
