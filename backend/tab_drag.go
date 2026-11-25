package backend

import (
	"encoding/json/jsontext"
	"errors"
	"log"
	"maps"
	"math"
	"net/url"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo"
)

// Tabs in flight between windows: the floating tab drag, its pill window,
// and the tracking of the pointer outside the windows behind both. macOS
// and Windows only: they tell where the pointer is and whether its button
// is down wherever it goes (pointer_*.go).

const (
	dragPillWidth, dragPillHeight = 200, 34
	// dragTick and trackTick are how often a drag looks at the pointer.
	dragTick  = 15 * time.Millisecond
	trackTick = 40 * time.Millisecond
)

// windowBounds is where an editor window is, in the screen's pixels.
// Mirrors WindowBounds in src/platform/bindings/app.ts.
type windowBounds struct {
	Label  string `json:"label"`
	X      int    `json:"x"`
	Y      int    `json:"y"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
	// ScaleFactor is pixels per point on the window's display, which may
	// differ from another window's.
	ScaleFactor float64 `json:"scaleFactor"`
}

// dragHover is what a window hears while a floating tab is over it: over
// its tab row (row), or its documents. Mirrors DragHover in
// src/app/tabs/useTabDragMerge.ts.
type dragHover struct {
	Title string  `json:"title"`
	Dirty bool    `json:"dirty"`
	X     float64 `json:"x"`
	Y     float64 `json:"y"`
	Row   bool    `json:"row"`
}

// tabDrags is the one floating tab drag, and the windows whose moves are
// tracked.
type tabDrags struct {
	active   atomic.Bool
	mu       sync.Mutex
	tracking map[string]bool
}

// focusRank is how recently an editor window was focused, 0 the latest.
func (a *App) focusRank(label string) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	if i := slices.Index(a.recentFocus, label); i >= 0 {
		return i
	}
	return len(a.recentFocus)
}

// shownEditorWindows are the editor windows that show: what a tab can drop
// on.
func (a *App) shownEditorWindows() []*appWindow {
	var out []*appWindow
	for _, w := range a.allWindows() {
		if isEditorWindow(w.label) && !w.win.IsDestroyed() && w.win.IsVisible() {
			out = append(out, w)
		}
	}
	return out
}

// listWindowBounds is every editor window that shows, the front one first.
func (a *App) listWindowBounds() []windowBounds {
	out := []windowBounds{}
	for _, w := range a.shownEditorWindows() {
		b := w.win.Bounds()
		out = append(out, inPixels(w.label, b, mygo.Screen.DisplayMatching(b).ScaleFactor))
	}
	slices.SortStableFunc(out, func(x, y windowBounds) int { return a.focusRank(x.Label) - a.focusRank(y.Label) })
	return out
}

// inPixels is bounds b, in points, in a display's pixels at its scale.
func inPixels(label string, b mygo.Rectangle, scale float64) windowBounds {
	if scale <= 0 || math.IsNaN(scale) || math.IsInf(scale, 0) {
		scale = 1
	}
	px := func(v int) int { return int(math.Round(float64(v) * scale)) }
	return windowBounds{Label: label, X: px(b.X), Y: px(b.Y), Width: px(b.Width), Height: px(b.Height), ScaleFactor: scale}
}

// dropTarget is the editor window under the pointer, and whether over its
// tab row (the tab joins the row) or its documents (it opens beside them,
// split).
type dropTarget struct {
	label string
	row   bool
}

// windowHit is the window under the point (screen points), the front one
// where windows overlap; nil over none. The row is rowHeight points along a
// window's top, as the window measures it. The drag's source counts, so a
// tab can drop back where it came from.
func (a *App) windowHit(x, y, rowHeight float64) *dropTarget {
	var placed []placedWindow
	for _, w := range a.shownEditorWindows() {
		placed = append(placed, placedWindow{label: w.label, bounds: w.win.Bounds(), rank: a.focusRank(w.label)})
	}
	return hitAmong(placed, x, y, rowHeight)
}

// placedWindow is a window where it is, and how recently it was focused
// (0 the latest).
type placedWindow struct {
	label  string
	bounds mygo.Rectangle
	rank   int
}

func hitAmong(placed []placedWindow, x, y, rowHeight float64) *dropTarget {
	var hit *dropTarget
	best := 0
	for _, w := range placed {
		b := w.bounds
		left, top := float64(b.X), float64(b.Y)
		inside := x >= left && x <= left+float64(b.Width) && y >= top && y <= top+float64(b.Height)
		if inside && (hit == nil || w.rank < best) {
			hit, best = &dropTarget{label: w.label, row: y <= top+rowHeight}, w.rank
		}
	}
	return hit
}

// pointer is where the pointer is, in screen points, and whether its left
// button is down.
func pointer() (float64, float64, bool) {
	p := mygo.Screen.CursorScreenPoint()
	return float64(p.X), float64(p.Y), leftButtonDown()
}

// dragPill is the dragged tab's own small window: a page can't draw
// outside its window. Made fresh for each drag, it never takes a click or
// the focus.
type dragPill struct {
	win    *mygo.Window
	parked bool
}

func (p *dragPill) show(title string, dirty bool, look string, x, y float64) {
	if p.win != nil {
		p.move(x, y)
		return
	}
	dirtyFlag := "0"
	if dirty {
		dirtyFlag = "1"
	}
	query := url.Values{"title": {title}, "dirty": {dirtyFlag}, "look": {look}}
	p.win = mygo.NewWindow(mygo.WindowOptions{
		URL: "/dragpill.html?" + query.Encode(), Width: dragPillWidth, Height: dragPillHeight,
		X: int(x) - dragPillWidth/2, Y: int(y) - dragPillHeight/2,
		Frameless: true, Transparent: true, AlwaysOnTop: true, SkipTaskbar: true,
		DisableResize: true, DisableShadow: true, IgnoreMouseEvents: true, Hidden: true,
	})
	p.win.ShowInactive()
}

func (p *dragPill) move(x, y float64) {
	p.parked = false
	if p.win != nil {
		p.win.SetPosition(int(x)-dragPillWidth/2, int(y)-dragPillHeight/2)
	}
}

// park takes it out of sight while over a tab row, where the window shows
// a preview of the tab instead. Moved away, not hidden: shown again it
// would take the focus on Windows.
func (p *dragPill) park() {
	if p.win != nil && !p.parked {
		p.parked = true
		p.win.SetPosition(-32000, -32000)
	}
}

func (p *dragPill) close() {
	if p.win != nil {
		p.win.Destroy()
	}
}

// floatTab carries a tab dragged out of its window until the button is
// let go: onto a tab row it joins it, over a window's documents it opens
// split there, anywhere else in a window of its own.
func (a *App) floatTab(tab jsonObject, title string, dirty bool, rowHeight float64, look string) {
	defer a.drags.active.Store(false)
	var pill dragPill
	defer pill.close()
	hover := ""
	leave := func(label string) { _ = a.emitTo(label, "drag-hover", nil) }
	var hit *dropTarget
	for ticks := 0; ; ticks++ {
		x, y, down := pointer()
		if !down {
			// One fresh hit test decides: the kept one may be two ticks
			// old. A window shown a preview that isn't the landing one is
			// told it left; the landing one swaps its preview for the tab.
			hit = a.windowHit(x, y, rowHeight)
			if hover != "" && (hit == nil || hit.label != hover) {
				leave(hover)
			}
			a.dropTab(tab, x, y, hit)
			return
		}
		// The pill moves each tick; the costlier hit test runs every third.
		if ticks%3 == 0 {
			hit = a.windowHit(x, y, rowHeight)
		}
		if hit == nil {
			if hover != "" {
				leave(hover)
				hover = ""
			}
			pill.show(title, dirty, look, x, y)
		} else {
			// In a row the window's preview stands for the tab and the pill
			// parks; over the documents it rides on.
			if hit.row {
				pill.park()
			} else {
				pill.show(title, dirty, look, x, y)
			}
			if hover != hit.label {
				if hover != "" {
					leave(hover)
				}
				hover = hit.label
			}
			// Each tick: x lets the preview ride the pointer along the bar.
			_ = a.emitTo(hit.label, "drag-hover", dragHover{Title: title, Dirty: dirty, X: x, Y: y, Row: hit.row})
		}
		time.Sleep(dragTick)
	}
}

// dropTab lands a dragged tab where the button was let go: with the
// window under it, at the slot it hovered or split where it was let go,
// or in a small window of its own there.
func (a *App) dropTab(tab jsonObject, x, y float64, at *dropTarget) {
	if at != nil {
		// The tab's fields and where it was let go, side by side, as
		// receive-detached-tab's listener in src/app/tabs/useTabDragMerge.ts
		// reads them.
		dropped := maps.Clone(tab)
		dropped["x"], dropped["y"], dropped["row"] = mustJSON(x), mustJSON(y), mustJSON(at.row)
		_ = a.emitTo(at.label, "receive-detached-tab", dropped)
		return
	}
	if err := a.openTabWindow(mustJSON(tab), &[2]float64{x - 100, max(y-14, 0)}); err != nil {
		log.Printf("textus: dropped tab: %v", err)
	}
}

// openTabWindow opens a small window for a tab moved out of its own, at
// at (screen points) or where windows open; the window takes the tab as
// it opens (take_detached_tab).
func (a *App) openTabWindow(tab jsontext.Value, at *[2]float64) error {
	label := a.nextWindowLabel()
	a.mu.Lock()
	if a.detachedTabs == nil {
		a.detachedTabs = map[string]jsontext.Value{}
	}
	a.detachedTabs[label] = tab
	a.mu.Unlock()
	err := a.buildEditorWindow(label, at, true)
	if err != nil {
		a.mu.Lock()
		delete(a.detachedTabs, label)
		a.mu.Unlock()
	}
	return err
}

// trackWindowDrag tells window label where the pointer is until its button
// is let go: a window moved by its title bar hears no end of the move.
func (a *App) trackWindowDrag(label string) {
	defer func() {
		a.drags.mu.Lock()
		delete(a.drags.tracking, label)
		a.drags.mu.Unlock()
	}()
	for {
		// The window goes mid-drag when it turns into a floating tab:
		// that drag carries on from there.
		w := a.window(label)
		if w == nil {
			return
		}
		x, y, down := pointer()
		_ = w.emit("window-drag-tick", map[string]any{"x": x, "y": y, "down": down})
		if !down {
			return
		}
		time.Sleep(trackTick)
	}
}

func init() {
	// Where every editor window is, so a single-tab window dragged by its
	// title bar can tell it entered another's tab row.
	command("list_window_bounds", func(c *Call, _ none) ([]windowBounds, error) {
		return c.app.listWindowBounds(), nil
	})
	command("start_floating_tab_drag", func(c *Call, a struct {
		// The tab and its live content (DetachedTab_Deserialize in
		// src/platform/bindings/app.ts): a JSON object.
		Tab           jsonObject `json:"tab"`
		Title         string     `json:"title"`
		Dirty         bool       `json:"dirty"`
		DestroySource bool       `json:"destroySource"`
		RowHeight     float64    `json:"rowHeight"`
		Look          string     `json:"look"`
	}) (nothing, error) {
		if !pointerTracked {
			return nil, errors.New("floating tab drags need global cursor tracking")
		}
		if a.Tab == nil {
			return nil, errors.New("no tab to drag")
		}
		if c.app.drags.active.Swap(true) {
			return nil, errors.New("a floating tab drag is already active")
		}
		// Once a tab is in flight its source window is gone: the document
		// is the drag's.
		if a.DestroySource && c.window != nil {
			c.window.destroy()
		}
		go c.app.floatTab(a.Tab, a.Title, a.Dirty, a.RowHeight, a.Look)
		return nil, nil
	})
	// The window's first move of a drag: the pointer is followed until the
	// button is let go. False when there is nothing to follow.
	command("start_window_drag_tracking", func(c *Call, _ none) (bool, error) {
		if !pointerTracked || c.window == nil || !leftButtonDown() {
			return false, nil
		}
		c.app.drags.mu.Lock()
		defer c.app.drags.mu.Unlock()
		if c.app.drags.tracking[c.window.label] {
			return true, nil
		}
		if c.app.drags.tracking == nil {
			c.app.drags.tracking = map[string]bool{}
		}
		c.app.drags.tracking[c.window.label] = true
		go c.app.trackWindowDrag(c.window.label)
		return true, nil
	})
}
