package backend

import (
	"math"
	"runtime"

	"github.com/egoist/mygo"
)

// Where an editor window opens: where the last one closed if still on a
// screen, else centred on the main screen, and never bigger than its
// screen; and how it was left, maximized or full screen.

// screenArea is a screen's work area (without the menu bar, Dock or
// taskbar), in the window's own units.
type screenArea struct{ x, y, width, height float64 }

// How much of the window's top edge must be on a screen for the window to
// count as there: enough of the title bar to take hold of and drag.
const gripWidth, gripHeight = 120.0, 40.0

// firstRunWidth and firstRunHeight are a window's size before any has
// closed, fitted to the screen as it opens.
const firstRunWidth, firstRunHeight = 1200, 800

// place is a window's position and size: saved is the last window's
// position, the first of screens the main one, least the least size.
func place(saved *[2]float64, size [2]float64, screens []screenArea, least [2]float64) (*[2]float64, [2]float64) {
	if len(screens) == 0 {
		return saved, size
	}
	screen, held := screens[0], false
	if saved != nil {
		for _, s := range screens {
			if grips(s, saved[0], saved[1], size[0]) {
				screen, held = s, true
				break
			}
		}
	}
	w := max(min(size[0], screen.width), least[0])
	h := max(min(size[1], screen.height), least[1])
	var x, y float64
	if held {
		// Where it was, moved in only as far as it now runs off.
		x = max(min(saved[0], screen.x+screen.width-w), screen.x)
		y = max(min(saved[1], screen.y+screen.height-h), screen.y)
	} else {
		x = screen.x + (screen.width-w)/2
		y = screen.y + (screen.height-h)/2
	}
	return &[2]float64{math.Round(x), math.Round(y)}, [2]float64{w, h}
}

// grips reports whether enough of a window at (x, y), width wide, has its
// top edge on screen to be taken hold of.
func grips(s screenArea, x, y, width float64) bool {
	left := max(x, s.x)
	right := min(x+width, s.x+s.width)
	return right-left >= gripWidth && y >= s.y && y+gripHeight <= s.y+s.height
}

// windowMode is how a window was left.
type windowMode string

const (
	modeNormal     windowMode = "normal"
	modeMaximized  windowMode = "maximized"
	modeFullscreen windowMode = "fullscreen"
)

// keptFrame is a window's normal bounds (its page area's size and its outer
// position) and how it was left; maximized or full screen, the bounds are
// those it goes back to. Kept with each window's documents in session.json
// (WindowSession.Frame), and for the last window closed in the UI state
// (editorWindowSize, editorWindowPosition and editorWindowMode,
// src/storage/ui-state-types.ts).
type keptFrame struct {
	Size [2]float64 `json:"size"`
	// Position is nil where the system doesn't say (Wayland), or for a
	// window it placed.
	Position *[2]float64 `json:"position,omitzero"`
	Mode     windowMode  `json:"mode"`
}

// mode is how the window was left; one never said was normal.
func (k keptFrame) mode() windowMode {
	if k.Mode == modeMaximized || k.Mode == modeFullscreen {
		return k.Mode
	}
	return modeNormal
}

// modeOf is how a window was left, as the system reads it. macOS calls a
// window zoomed (maximized) whenever it fills the screen, sized by hand
// too, and the bounds it would go back to are then stale: there its real
// frame is kept, as a normal window's.
func modeOf(fullscreen, maximized bool) windowMode {
	switch {
	case fullscreen:
		return modeFullscreen
	case maximized && runtime.GOOS != "darwin":
		return modeMaximized
	}
	return modeNormal
}

// liveFrame is what a window's bounds and state read now.
type liveFrame struct {
	frame     keptFrame
	minimized bool
}

// normalBounds is the bounds a window would go back to, when it is in none
// of the states that hide them.
func normalBounds(live liveFrame) *keptFrame {
	if live.minimized || live.frame.mode() != modeNormal {
		return nil
	}
	f := live.frame
	return &f
}

// onClose is what to keep of a closing window. Maximized or full screen its
// size is the screen's, so the normal bounds last seen are kept with the
// mode.
func onClose(live liveFrame, normal *keptFrame) *keptFrame {
	if live.minimized {
		return normal
	}
	if live.frame.mode() == modeNormal {
		f := live.frame
		return &f
	}
	kept := live.frame
	if normal != nil {
		kept = *normal
	}
	kept.Mode = live.frame.mode()
	return &kept
}

// reopen is a kept frame fitted to today's screens by place, its mode
// unchanged: a maximized window whose screen is gone maximizes on the main
// one.
func reopen(kept keptFrame, screens []screenArea, least [2]float64) keptFrame {
	position, size := place(kept.Position, kept.Size, screens, least)
	return keptFrame{Size: size, Position: position, Mode: kept.mode()}
}

// screens is the work areas of the screens, the main one first.
func screens() []screenArea {
	var out []screenArea
	for _, d := range mygo.Screen.Displays() {
		r := d.WorkArea
		out = append(out, screenArea{float64(r.X), float64(r.Y), float64(r.Width), float64(r.Height)})
	}
	return out
}

// editorLeast is an editor window's least size.
var editorLeast = [2]float64{editorMinWidth, editorMinHeight}

// readLive reads a window's bounds and state now.
func readLive(win *mygo.Window) liveFrame {
	width, height := win.ContentSize()
	frame := keptFrame{
		Size: [2]float64{float64(width), float64(height)},
		Mode: modeOf(win.IsFullScreen(), win.IsMaximized()),
	}
	// Wayland never tells an app where its window is.
	if !isWayland() {
		x, y := win.Position()
		frame.Position = &[2]float64{float64(x), float64(y)}
	}
	return liveFrame{frame: frame, minimized: win.IsMinimized()}
}

// noteBounds keeps a moved or resized editor window's bounds, while they
// are its normal ones: what it goes back to, kept when it closes
// maximized or full screen.
func (a *App) noteBounds(w *appWindow) {
	if bounds := normalBounds(readLive(w.win)); bounds != nil {
		a.seedBounds(w.label, *bounds)
	}
}

func (a *App) seedBounds(label string, bounds keptFrame) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.normalFrames == nil {
		a.normalFrames = map[string]keptFrame{}
	}
	a.normalFrames[label] = bounds
}

func (a *App) forgetBounds(label string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.normalFrames, label)
}

// closingFrame is what to keep of an editor window as it closes.
func (a *App) closingFrame(w *appWindow) *keptFrame {
	a.mu.Lock()
	normal, ok := a.normalFrames[w.label]
	a.mu.Unlock()
	var seen *keptFrame
	if ok {
		seen = &normal
	}
	return onClose(readLive(w.win), seen)
}
