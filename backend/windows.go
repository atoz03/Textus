package backend

import (
	"fmt"
	"log"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/egoist/mygo"
)

type windowKind int

const (
	kindEditor     windowKind = iota + 1 // holds documents: "main", "window-<n>"
	kindChat                             // a detached AI chat
	kindFloating                         // a plugin's view floating on the desktop
	kindPdfExport                        // off screen, laying a document out for PDF
	kindDragPill                         // the tab being dragged between windows
	kindBackground                       // where plugins run in the background
)

// kindOf is the kind of window label names; 0 for a label no window of
// ours has.
func kindOf(label string) windowKind {
	numbered := func(prefix string) bool {
		n, ok := strings.CutPrefix(label, prefix)
		if !ok || n == "" {
			return false
		}
		for _, c := range n {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '.') {
				return false
			}
		}
		return true
	}
	switch {
	case label == "main" || numbered("window-"):
		return kindEditor
	case numbered("chat-"):
		return kindChat
	case strings.HasPrefix(label, "floating-"):
		return kindFloating
	case strings.HasPrefix(label, "pdf-export-"):
		return kindPdfExport
	case label == "drag-pill":
		return kindDragPill
	case label == "background-host":
		return kindBackground
	}
	return 0
}

func isEditorWindow(label string) bool { return kindOf(label) == kindEditor }

// appWindow is a window of the app, known by its label as the frontend
// knows it.
type appWindow struct {
	label string
	win   *mygo.Window
	// closeListened: the page asks before its window closes (close-flow.ts):
	// a close is sent to it as close-requested, and it destroys the window.
	closeListened atomic.Bool
	destroying    atomic.Bool
}

// Editor windows' least size, and the size of a "small" one.
const (
	editorMinWidth, editorMinHeight = 480, 320
	smallWidth, smallHeight         = 640, 720
)

func (a *App) window(label string) *appWindow {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.windows[label]
}

func (a *App) windowOf(w *mygo.Window) *appWindow {
	if w == nil {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, aw := range a.windows {
		if aw.win == w {
			return aw
		}
	}
	return nil
}

func (a *App) allWindows() []*appWindow {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]*appWindow, 0, len(a.windows))
	for _, w := range a.windows {
		out = append(out, w)
	}
	return out
}

func (a *App) nextWindowLabel() string {
	return "window-" + strconv.FormatUint(uint64(a.windowCounter.Add(1)), 10)
}

// noteFocused puts an editor window first in the order they were last
// focused in.
func (a *App) noteFocused(label string) {
	if !isEditorWindow(label) {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	recent := []string{label}
	for _, l := range a.recentFocus {
		if l != label {
			recent = append(recent, l)
		}
	}
	a.recentFocus = recent
}

// lastEditor is the editor window focused last, or any editor window.
func (a *App) lastEditor() *appWindow {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, l := range a.recentFocus {
		if w, ok := a.windows[l]; ok {
			return w
		}
	}
	for label, w := range a.windows {
		if isEditorWindow(label) {
			return w
		}
	}
	return nil
}

// focusedEditorWindow is the editor window a menu command or an open from
// the system goes to: origin when a window's own menu ran the command, else
// the focused window, else the editor focused last.
func (a *App) focusedEditorWindow(origin string) string {
	label := ""
	if origin != "" && a.window(origin) != nil {
		label = origin
	} else if w := a.windowOf(mygo.FocusedWindow()); w != nil {
		label = w.label
	}
	if isEditorWindow(label) {
		return label
	}
	// A chat window has no document: its editor window's.
	if editor, ok := a.chats.editorFor(label); ok && a.window(editor) != nil {
		return editor
	}
	if w := a.lastEditor(); w != nil {
		return w.label
	}
	return ""
}

// isWayland: where Wayland places windows, the app cannot know or choose
// where a window goes.
func isWayland() bool {
	if runtime.GOOS != "linux" {
		return false
	}
	backend := os.Getenv("GDK_BACKEND")
	return os.Getenv("WAYLAND_DISPLAY") != "" && (backend == "" || strings.Contains(backend, "wayland"))
}

// windowSpec says how to open a window of the app.
type windowSpec struct {
	label         string
	url           string
	title         string
	width, height int
	minW, minH    int
	at            *[2]float64
	alwaysOnTop   bool
	// parent keeps the window above it (a chat window on Wayland).
	parent *mygo.Window
}

// openWindow creates a window of the app's frontend, with its boot snapshot
// and the app's own title bar.
func (a *App) openWindow(spec windowSpec) (*appWindow, error) {
	if a.window(spec.label) != nil {
		return nil, fmt.Errorf("a window %s is open already", spec.label)
	}
	script, background := a.bootScript(spec.label)
	opts := mygo.WindowOptions{
		Title:           spec.title,
		URL:             spec.url,
		Width:           spec.width,
		Height:          spec.height,
		MinWidth:        spec.minW,
		MinHeight:       spec.minH,
		TitleBarStyle:   mygo.TitleBarHidden,
		BackgroundColor: background,
		AlwaysOnTop:     spec.alwaysOnTop,
		Parent:          spec.parent,
		Page:            mygo.PageOptions{PreloadScript: script},
	}
	if runtime.GOOS != "darwin" {
		// Windows and Linux: the page draws the caption buttons and the
		// menu (WindowBar); the window keeps its shadow and resizing edges.
		opts.HideWindowButtons = true
	}
	if spec.at != nil && !isWayland() {
		opts.X, opts.Y = int(spec.at[0]), int(spec.at[1])
	}
	w := &appWindow{label: spec.label}
	a.mu.Lock()
	a.windows[spec.label] = w
	a.mu.Unlock()
	w.win = mygo.NewWindow(opts)
	a.wire(w)
	return w, nil
}

// wire hands the window's own events to its page and keeps the app's state
// in step with it.
func (a *App) wire(w *appWindow) {
	win := w.win
	page := win.Page()
	if page != nil {
		page.OnWillNavigate(func(e *mygo.NavigateEvent) {
			if !mayLoad(e.URL) {
				e.PreventDefault()
				openOutside(e.URL)
			}
		})
	}
	win.OnClose(func(e *mygo.CloseEvent) {
		if isEditorWindow(w.label) {
			a.rememberEditorWindow(w)
		}
		if w.closeListened.Load() && !w.destroying.Load() {
			e.PreventDefault()
			_ = w.emit(eventCloseRequested, nil)
		}
	})
	win.OnClosed(func() { a.windowGone(w) })
	win.OnFocus(func() {
		a.noteFocused(w.label)
		_ = w.emit(eventFocus, true)
	})
	win.OnBlur(func() { _ = w.emit(eventBlur, false) })
	win.OnResize(func() {
		width, height := win.Size()
		_ = w.emit(eventResized, map[string]int{"width": width, "height": height})
	})
	win.OnMove(func() {
		x, y := win.Position()
		_ = w.emit(eventMoved, map[string]int{"x": x, "y": y})
	})
	win.OnFileDrop(func(e *mygo.FileDropEvent) {
		_ = w.emit(eventDragDrop, map[string]any{
			"paths":    e.Paths,
			"position": map[string]int{"x": e.X, "y": e.Y},
		})
	})
	if runtime.GOOS == "darwin" {
		win.OnEnterFullScreen(func() { _ = w.emit("window-fullscreen", true) })
		win.OnLeaveFullScreen(func() { _ = w.emit("window-fullscreen", false) })
	}
}

// destroy closes the window without asking its page again.
func (w *appWindow) destroy() {
	w.destroying.Store(true)
	w.win.Close()
}

// windowGone forgets a closed window, and quits when it was the last one of
// a quit.
func (a *App) windowGone(w *appWindow) {
	a.mu.Lock()
	delete(a.windows, w.label)
	a.recentFocus = removeString(a.recentFocus, w.label)
	others := len(a.windows)
	a.mu.Unlock()
	a.forgetWindowSession(w.label)
	a.forgetChatWindow(w.label)
	if others == 0 && a.quitting.Load() {
		a.exit()
	}
}

func removeString(list []string, s string) []string {
	out := list[:0:0]
	for _, v := range list {
		if v != s {
			out = append(out, v)
		}
	}
	return out
}

// buildEditorWindow opens an editor window: the size the last one closed
// at, or a small one for a single document.
func (a *App) buildEditorWindow(label string, at *[2]float64, small bool) error {
	width, height := smallWidth, smallHeight
	url := "/index.html?window=small"
	if !small {
		width, height = 800, 600
		if w, h, ok := a.editorWindowSize(); ok {
			width, height = int(w), int(h)
		}
		url = "/"
	}
	_, err := a.openWindow(windowSpec{
		label: label, url: url, title: "Textus",
		width: width, height: height, minW: editorMinWidth, minH: editorMinHeight,
		at: at,
	})
	return err
}

func (a *App) openNewWindow() error {
	return a.buildEditorWindow(a.nextWindowLabel(), nil, false)
}

// buildMainWindow opens the first window: where the last window closed, at
// its size.
func (a *App) buildMainWindow() error {
	var at *[2]float64
	if x, y, ok := a.editorWindowPosition(); ok && !isWayland() {
		at = &[2]float64{x, y}
	}
	return a.buildEditorWindow("main", at, false)
}

// reopenWithoutWindows: the app opened again with no window shown (the Dock
// icon, a second launch in the background): the last editor window, or the
// documents a fresh launch would restore.
func (a *App) reopenWithoutWindows() {
	if w := a.lastEditor(); w != nil {
		w.win.Restore()
		w.win.Show()
		w.win.Focus()
		return
	}
	var windows []WindowSession
	if a.readFlag("restoreSessionOnStartup") {
		windows = a.readSessionWindows()
	}
	if len(windows) == 0 {
		if err := a.openNewWindow(); err != nil {
			log.Printf("textus: cannot open a window: %v", err)
		}
		return
	}
	a.queueWindowsToOpen(windows)
}
