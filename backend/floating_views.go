package backend

import (
	"math"
	"net/url"
	"runtime"
	"strings"
	"sync"

	"github.com/egoist/mygo"
)

// Plugins' floating views on the desktop: a small transparent window with
// no frame, above every other, out of the Dock and the taskbar. The editor
// window that opened one runs its plugin and passes its page through, and
// its floating windows close with it.

// floatingLabelPrefix starts every floating window's label: never an
// editor window's.
const floatingLabelPrefix = "floating-"

// floatingWindows is which editor window owns each floating window.
type floatingWindows struct {
	mu     sync.Mutex
	owners map[string]string
}

// floatingLabel is the window's label for a view id: a view's dots and
// anything else but letters, digits and '-' become '_'.
func floatingLabel(view string) string {
	var b strings.Builder
	b.WriteString(floatingLabelPrefix)
	for _, c := range view {
		if c < 128 && (c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-') {
			b.WriteRune(c)
		} else {
			b.WriteByte('_')
		}
	}
	return b.String()
}

// floatingDesktopSupported: the platforms whose windows can be transparent
// without a compositor to ask.
func floatingDesktopSupported() bool {
	return runtime.GOOS == "darwin" || runtime.GOOS == "windows"
}

// cornerOf is by the bottom-right corner of the window's display, clear of
// the Dock and the taskbar.
func cornerOf(w *appWindow, width, height float64) [2]float64 {
	return cornerIn(mygo.Screen.DisplayMatching(w.win.Bounds()).WorkArea, width, height)
}

// cornerIn is where a window of that size sits by the bottom-right corner
// of a display's work area; a display that says nothing has it near the
// top-left.
func cornerIn(area mygo.Rectangle, width, height float64) [2]float64 {
	if area.Width == 0 || area.Height == 0 {
		return [2]float64{40, 40}
	}
	right := float64(area.X + area.Width)
	bottom := float64(area.Y + area.Height)
	return [2]float64{right - width - 32, bottom - height - 32}
}

// openFloatingWindow opens view's floating window for editor window owner,
// at at (screen points), or by the bottom-right corner of its display; an
// open one stays where it is. Its label.
func (a *App) openFloatingWindow(owner *appWindow, view, title string, at *[2]float64, width, height float64) (string, error) {
	label := floatingLabel(view)
	if a.window(label) != nil {
		return label, nil
	}
	place := cornerOf(owner, width, height)
	if at != nil {
		place = *at
	}
	query := url.Values{"view": {"floating"}, "owner": {owner.label}, "key": {view}}
	w, err := a.openWindow(windowSpec{
		// Its title as the plugin names it, for the system (Mission
		// Control, a screen reader); the id stays in the label.
		label: label, url: "/index.html?" + query.Encode(), title: title,
		width: int(math.Round(width)), height: int(math.Round(height)), at: &place,
		tweak: func(o *mygo.WindowOptions) {
			o.Frameless = true
			o.Transparent = true
			o.BackgroundColor = ""
			o.AlwaysOnTop = true
			o.SkipTaskbar = true
			o.DisableResize = true
			o.DisableShadow = true
			o.DisableMinimize = true
			o.DisableMaximize = true
			o.DisableFullScreen = true
			// Shown without taking the focus.
			o.Hidden = true
		},
	})
	if err != nil {
		return "", err
	}
	a.floats.mu.Lock()
	if a.floats.owners == nil {
		a.floats.owners = map[string]string{}
	}
	a.floats.owners[label] = owner.label
	a.floats.mu.Unlock()
	w.win.SetVisibleOnAllWorkspaces(true)
	w.win.ShowInactive()
	return label, nil
}

// closeFloatingOwnedBy closes the floating windows editor window owner
// opened, as it closes.
func (a *App) closeFloatingOwnedBy(owner string) {
	a.floats.mu.Lock()
	var owned []string
	for label, o := range a.floats.owners {
		if o == owner {
			owned = append(owned, label)
		}
	}
	a.floats.mu.Unlock()
	for _, label := range owned {
		if w := a.window(label); w != nil {
			w.destroy()
		}
	}
}

// forgetFloatingWindow forgets a floating window that closed.
func (a *App) forgetFloatingWindow(label string) {
	a.floats.mu.Lock()
	delete(a.floats.owners, label)
	a.floats.mu.Unlock()
}

// resizedBounds is where a floating window of bounds b goes at its new
// size. Folded or unfolded (keepRight nil), its top-left corner stays;
// resized by the user, its bottom and its outer edge do - the right one
// with keepRight, else the left.
func resizedBounds(b mygo.Rectangle, width, height float64, keepRight *bool) mygo.Rectangle {
	wide, high := int(math.Round(width)), int(math.Round(height))
	if keepRight == nil {
		return mygo.Rectangle{X: b.X, Y: b.Y, Width: wide, Height: high}
	}
	x := b.X
	if *keepRight {
		x = b.X + b.Width - wide
	}
	return mygo.Rectangle{X: x, Y: b.Y + b.Height - high, Width: wide, Height: high}
}

func init() {
	// Where a floating view can float over the desktop.
	command("floating_desktop_supported", func(c *Call, _ none) (bool, error) {
		return floatingDesktopSupported(), nil
	})
	command("open_floating_window", func(c *Call, a struct {
		View   string      `json:"view"`
		Title  string      `json:"title"`
		At     *[2]float64 `json:"at"`
		Width  float64     `json:"width"`
		Height float64     `json:"height"`
	}) (string, error) {
		owner, err := c.win()
		if err != nil {
			return "", err
		}
		return c.app.openFloatingWindow(owner, a.View, a.Title, a.At, a.Width, a.Height)
	})
	command("close_floating_window", func(c *Call, a struct {
		View string `json:"view"`
	}) (nothing, error) {
		if w := c.app.window(floatingLabel(a.View)); w != nil {
			w.destroy()
		}
		return nil, nil
	})
	command("resize_floating_window", func(c *Call, a struct {
		View      string  `json:"view"`
		Width     float64 `json:"width"`
		Height    float64 `json:"height"`
		KeepRight *bool   `json:"keepRight"`
	}) (nothing, error) {
		if w := c.app.window(floatingLabel(a.View)); w != nil {
			w.win.SetBounds(resizedBounds(w.win.Bounds(), a.Width, a.Height, a.KeepRight))
		}
		return nil, nil
	})
	// Where the window is now, in screen points, to open there next time.
	command("floating_window_position", func(c *Call, a struct {
		View string `json:"view"`
	}) (*[2]float64, error) {
		w := c.app.window(floatingLabel(a.View))
		if w == nil {
			return nil, nil
		}
		b := w.win.Bounds()
		return &[2]float64{float64(b.X), float64(b.Y)}, nil
	})
	// The editor window a floating view belongs to, brought to the front:
	// a view on the desktop opens Textus from its menu.
	command("show_floating_owner", func(c *Call, a struct {
		Owner string `json:"owner"`
	}) (nothing, error) {
		if !isEditorWindow(a.Owner) {
			return nil, nil
		}
		if w := c.app.window(a.Owner); w != nil {
			if w.win.IsMinimized() {
				w.win.Restore()
			}
			w.win.Show()
			w.win.Focus()
		}
		return nil, nil
	})
}
