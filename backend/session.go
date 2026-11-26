package backend

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"log"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
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
	// Frame is where the window was and how it was left: the backend's
	// alone, kept as the window closes (keepFrame), never taken from the
	// page.
	Frame *keptFrame `json:"frame,omitzero"`
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

// withKeptFrame is session as the page sent it, with the frame the backend
// kept for the window.
func withKeptFrame(sessions map[string]WindowSession, label string, session WindowSession) WindowSession {
	session.Frame = sessions[label].Frame
	return session
}

// setFrame keeps frame as the window label's; whether that changed
// anything.
func setFrame(sessions map[string]WindowSession, label string, frame *keptFrame) bool {
	w, ok := sessions[label]
	if !ok || reflect.DeepEqual(w.Frame, frame) {
		return false
	}
	w.Frame = frame
	sessions[label] = w
	return true
}

// keepFrame keeps a closing window's frame with its documents: the session
// file then has it if this is the last window, which the next launch
// brings back.
func (a *App) keepFrame(label string, frame *keptFrame) {
	a.mu.Lock()
	if !setFrame(a.sessions, label, frame) {
		a.mu.Unlock()
		return
	}
	merged := mergeWindows(a.sessions)
	a.mu.Unlock()
	a.writeSession(merged)
}

// keepOpenFrames keeps every open window's frame, read now, with its
// documents: as the app quits, before any window closes. The windows are
// read with the lock let go, as reading one waits for the main thread.
func (a *App) keepOpenFrames() {
	a.mu.Lock()
	labels := slices.Collect(maps.Keys(a.sessions))
	a.mu.Unlock()
	frames := map[string]*keptFrame{}
	for _, label := range labels {
		if w := a.window(label); w != nil {
			if frame := a.closingFrame(w); frame != nil {
				frames[label] = frame
			}
		}
	}
	a.mu.Lock()
	changed := false
	for label, frame := range frames {
		changed = setFrame(a.sessions, label, frame) || changed
	}
	var merged []WindowSession
	if changed {
		merged = mergeWindows(a.sessions)
	}
	a.mu.Unlock()
	if changed {
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
	a.rememberOpenWindows()
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

func init() {
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
		session := withKeptFrame(app.sessions, c.Label(), a.Session)
		if old, ok := app.sessions[c.Label()]; ok && reflect.DeepEqual(old, session) {
			app.mu.Unlock()
			return nil, nil
		}
		app.sessions[c.Label()] = session
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
