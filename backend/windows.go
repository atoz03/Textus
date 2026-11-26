package backend

import (
	"fmt"
	"log"
	"maps"
	"math"
	"os"
	"runtime"
	"slices"
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

// windowOf is the app's window w is; for a tab's web page, the editor
// window the page is laid over.
func (a *App) windowOf(w *mygo.Window) *appWindow {
	if w == nil {
		return nil
	}
	if p := a.webPageOf(w); p != nil {
		return p.owner
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
	// contentSize: width and height are the page area's, not the window's.
	contentSize bool
	minW, minH  int
	at          *[2]float64
	alwaysOnTop bool
	// parent keeps the window above it (a chat window on Wayland).
	parent *mygo.Window
	// tweak changes the options last: a window out of sight, or one
	// without a frame.
	tweak func(o *mygo.WindowOptions)
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
		UseContentSize:  spec.contentSize,
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
	if spec.tweak != nil {
		spec.tweak(&opts)
	}
	w := &appWindow{label: spec.label}
	a.mu.Lock()
	a.windows[spec.label] = w
	a.mu.Unlock()
	w.win = mygo.NewWindow(opts)
	// MyGo centres a window asked for at (0, 0): one left in the corner
	// goes back there.
	if spec.at != nil && opts.X == 0 && opts.Y == 0 && !isWayland() {
		w.win.SetPosition(0, 0)
	}
	a.wire(w)
	a.appStateChanged()
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
		// While it can still be read: its frame for the next launch, in the
		// UI state and with its documents. Quit kept both already.
		if isEditorWindow(w.label) && !a.quitting.Load() {
			frame := a.closingFrame(w)
			a.rememberEditorWindow(frame)
			a.keepFrame(w.label, frame)
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
		a.appStateChanged()
	})
	win.OnBlur(func() {
		_ = w.emit(eventBlur, false)
		a.appStateChanged()
	})
	win.OnResize(func() {
		width, height := win.Size()
		_ = w.emit(eventResized, map[string]int{"width": width, "height": height})
		if isEditorWindow(w.label) {
			a.noteBounds(w)
		}
	})
	win.OnMove(func() {
		x, y := win.Position()
		_ = w.emit(eventMoved, map[string]int{"x": x, "y": y})
		if isEditorWindow(w.label) {
			a.noteBounds(w)
		}
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

// bringForward shows the window in front, restored if it was minimized.
func (w *appWindow) bringForward() {
	if w.win.IsMinimized() {
		w.win.Restore()
	}
	w.win.Show()
	w.win.Focus()
}

// windowGone forgets a closed window, and ends the app when it was the
// last one of a quit, or the last of the user's that leaves it nothing to
// run for (afterWindowGone).
func (a *App) windowGone(w *appWindow) {
	a.mu.Lock()
	delete(a.windows, w.label)
	a.recentFocus = removeString(a.recentFocus, w.label)
	left := slices.Collect(maps.Keys(a.windows))
	a.mu.Unlock()
	a.forgetWindowSession(w.label)
	a.forgetChatWindow(w.label)
	a.cliWindowGone(w.label)
	a.trees.windowGone(w.label)
	a.forgetBounds(w.label)
	// A session its page never took goes with it.
	a.takeSessionFor(w.label)
	switch kindOf(w.label) {
	case kindEditor:
		// Its floating views go with it (it runs their plugins), and its
		// tabs' web pages.
		a.closeFloatingOwnedBy(w.label)
		a.closeWebPagesOf(w.label)
	case kindFloating:
		a.forgetFloatingWindow(w.label)
	}
	a.appStateChanged()
	switch afterWindowGone(runtime.GOOS, a.readFlag(backgroundSetting), a.quitting.Load(), w.label, left) {
	case endApp:
		a.exit()
	case quitApp:
		// Not inside MyGo's report of the close: quit closes windows too.
		go a.quit()
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

// buildEditorWindow opens an editor window: as frame was left, fitted to
// today's screens, or else the size the last one closed at (a small one for
// a single document), no bigger than the main screen, where the system
// puts it or at.
func (a *App) buildEditorWindow(label string, at *[2]float64, small bool, frame *keptFrame) error {
	url := "/"
	if small {
		url = "/index.html?window=small"
	}
	spec := windowSpec{
		label: label, url: url, title: "Textus",
		contentSize: true, minW: editorMinWidth, minH: editorMinHeight,
		at: at,
	}
	size := [2]float64{smallWidth, smallHeight}
	if frame != nil {
		opening := reopen(*frame, screens(), editorLeast)
		if isWayland() {
			opening.Position = nil
		}
		a.seedBounds(label, keptFrame{Size: opening.Size, Position: opening.Position, Mode: modeNormal})
		size, spec.at = opening.Size, opening.Position
		spec.tweak = func(o *mygo.WindowOptions) {
			o.Maximized = opening.mode() == modeMaximized
			o.FullScreen = opening.mode() == modeFullscreen
		}
	} else {
		if !small {
			size = [2]float64{firstRunWidth, firstRunHeight}
			if w, h, ok := a.editorWindowSize(); ok {
				size = [2]float64{w, h}
			}
		}
		_, size = place(nil, size, screens(), editorLeast)
	}
	spec.width, spec.height = int(math.Round(size[0])), int(math.Round(size[1]))
	_, err := a.openWindow(spec)
	return err
}

// buildWindowAsLeft opens a window that comes back as it was left (frame,
// fitted to today's screens), or else opens as a new one does.
func (a *App) buildWindowAsLeft(label string, frame *keptFrame) error {
	return a.buildEditorWindow(label, nil, false, frame)
}

func (a *App) openNewWindow() error {
	return a.buildEditorWindow(a.nextWindowLabel(), nil, false, nil)
}

// buildMainWindow opens the first window as the first window restored was
// left (restored), else the last window closed, else at the first run's
// size, centred.
func (a *App) buildMainWindow(restored *keptFrame) error {
	frame := restored
	if frame == nil {
		frame = a.readEditorFrame()
	}
	if frame == nil {
		frame = &keptFrame{Size: [2]float64{firstRunWidth, firstRunHeight}, Mode: modeNormal}
	}
	return a.buildWindowAsLeft("main", frame)
}

// reopenWithoutWindows: the app opened again with no window shown (the Dock
// icon, a second launch in the background): the last editor window, or the
// documents a fresh launch would restore.
func (a *App) reopenWithoutWindows() {
	if w := a.lastEditor(); w != nil {
		w.bringForward()
		return
	}
	var windows []WindowSession
	if a.readFlag("restoreSessionOnStartup") {
		windows = a.readSessionWindows()
	}
	if len(windows) == 0 {
		if err := a.buildWindowAsLeft(a.nextWindowLabel(), a.readEditorFrame()); err != nil {
			log.Printf("textus: cannot open a window: %v", err)
		}
		return
	}
	a.queueWindowsToOpen(windows)
}
