package backend

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"sort"
	"strings"
)

// WindowSession is what one editor window holds, kept to reopen it at the
// next launch: its documents, its layout and its sidebar's folder.
type WindowSession struct {
	Paths  []string       `json:"paths"`
	Layout jsontext.Value `json:"layout,omitzero"`
	Folder *string        `json:"folder,omitzero"`
	// Opened are documents handed over as it opens (a file manager, a second
	// launch), put in front; never written to the session file.
	Opened []string `json:"opened,omitempty"`
}

func (s WindowSession) kept() bool { return len(s.Paths) > 0 || s.Folder != nil }

func sessionOfPaths(paths []string) WindowSession { return WindowSession{Paths: paths} }

func (a *App) sessionFile() string {
	return filepath.Join(a.dirs.Config, fileName("session"))
}

// readSessionWindows is the windows the last session kept.
func (a *App) readSessionWindows() []WindowSession {
	raw, err := os.ReadFile(a.sessionFile())
	if err != nil {
		return nil
	}
	var windows []WindowSession
	if err := json.Unmarshal(raw, &windows); err != nil {
		moveAside(a.sessionFile())
		return nil
	}
	return slices.DeleteFunc(windows, func(w WindowSession) bool { return !w.kept() })
}

func (a *App) writeSession(windows []WindowSession) {
	if windows == nil {
		windows = []WindowSession{}
	}
	for i := range windows {
		windows[i].Opened = nil
		if windows[i].Paths == nil {
			windows[i].Paths = []string{}
		}
	}
	raw, err := json.Marshal(windows)
	if err == nil {
		err = writeFileAtomic(a.sessionFile(), raw)
	}
	if err != nil {
		log.Printf("textus: cannot keep the window session: %v", err)
	}
}

// mergeWindows is the session of the windows open: "main" first, each
// document only in the first window holding it.
func mergeWindows(sessions map[string]WindowSession) []WindowSession {
	labels := make([]string, 0, len(sessions))
	for label := range sessions {
		labels = append(labels, label)
	}
	sort.Slice(labels, func(i, j int) bool {
		if (labels[i] == "main") != (labels[j] == "main") {
			return labels[i] == "main"
		}
		return labels[i] < labels[j]
	})
	seen := map[string]bool{}
	var out []WindowSession
	for _, label := range labels {
		w := sessions[label]
		var paths []string
		for _, p := range w.Paths {
			if !seen[p] {
				seen[p] = true
				paths = append(paths, p)
			}
		}
		w.Paths = paths
		if w.kept() {
			out = append(out, w)
		}
	}
	return out
}

// withDocuments puts documents handed over at launch into the session that
// reopens: in the window whose folder holds them all, else the first.
func withDocuments(saved []WindowSession, paths []string) []WindowSession {
	if len(saved) == 0 {
		return []WindowSession{{Paths: paths, Opened: paths}}
	}
	at := 0
	for i, w := range saved {
		if w.Folder != nil && !slices.ContainsFunc(paths, func(p string) bool {
			_, under := pathUnder(p, *w.Folder)
			return !under
		}) {
			at = i
			break
		}
	}
	for _, path := range paths {
		for i := range saved {
			saved[i].Paths = slices.DeleteFunc(saved[i].Paths, func(p string) bool { return p == path })
		}
		saved[at].Paths = append(saved[at].Paths, path)
	}
	saved[at].Opened = paths
	return saved
}

func (a *App) forgetWindowSession(label string) {
	a.mu.Lock()
	if _, ok := a.sessions[label]; !ok {
		a.mu.Unlock()
		return
	}
	delete(a.sessions, label)
	var merged []WindowSession
	write := !a.quitting.Load() && len(a.sessions) > 0
	if write {
		merged = mergeWindows(a.sessions)
	}
	a.mu.Unlock()
	if write {
		a.writeSession(merged)
	}
}

// beginQuit keeps every window's documents before closing them one by one
// shrinks the session.
func (a *App) beginQuit() {
	a.quitting.Store(true)
	a.mu.Lock()
	var merged []WindowSession
	any := len(a.sessions) > 0
	if any {
		merged = mergeWindows(a.sessions)
	}
	a.mu.Unlock()
	if any {
		a.writeSession(merged)
	}
}

// quit asks every window to close, so each unsaved document gets its
// prompt; the app exits once the last one is gone.
func (a *App) quit() {
	a.beginQuit()
	windows := a.allWindows()
	if len(windows) == 0 {
		a.exit()
		return
	}
	for _, w := range windows {
		w.win.Close()
	}
}

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
	help := a.pendingShowHelp != nil
	a.mu.Unlock()
	launching := a.openingAtLaunch.Load() && !a.uiReady.Load()
	drafts := label == "main" && a.draftsWaiting()
	return queued || launching || help || drafts
}

func (a *App) queuePending(paths []string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, p := range paths {
		a.pending = append(a.pending, sessionOfPaths([]string{p}))
	}
}

// queueWindowsToOpen opens a window for each session: the one opening at
// launch takes the first.
func (a *App) queueWindowsToOpen(windows []WindowSession) {
	if len(windows) == 0 {
		return
	}
	a.mu.Lock()
	a.pending = append(a.pending, windows...)
	a.mu.Unlock()
	extra := len(windows)
	if !a.uiReady.Load() {
		extra--
	}
	for range extra {
		if err := a.openNewWindow(); err != nil {
			log.Printf("textus: cannot open a window: %v", err)
		}
	}
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
	if err := a.buildEditorWindow(label, nil, false); err != nil {
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
	command("window_session_current", func(c *Call, _ none) (*WindowSession, error) {
		c.app.mu.Lock()
		defer c.app.mu.Unlock()
		if w, ok := c.app.sessions[c.Label()]; ok {
			if w.Paths == nil {
				w.Paths = []string{}
			}
			return &w, nil
		}
		return nil, nil
	})
	command("update_window_session", func(c *Call, a struct {
		Session WindowSession `json:"session"`
	}) (nothing, error) {
		app := c.app
		app.mu.Lock()
		if old, ok := app.sessions[c.Label()]; ok && reflect.DeepEqual(old, a.Session) {
			app.mu.Unlock()
			return nil, nil
		}
		app.sessions[c.Label()] = a.Session
		merged := mergeWindows(app.sessions)
		app.mu.Unlock()
		app.writeSession(merged)
		return nil, nil
	})
	command("cancel_session_quit", func(c *Call, _ none) (nothing, error) {
		app := c.app
		app.quitting.Store(false)
		// A window's prompt cancelled the quit: no relaunch either.
		relaunchAfterQuit.Store(false)
		app.mu.Lock()
		merged := mergeWindows(app.sessions)
		app.mu.Unlock()
		app.writeSession(merged)
		return nil, nil
	})
}
