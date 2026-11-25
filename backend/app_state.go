package backend

import (
	"sync"
	"time"

	"github.com/egoist/mygo"
)

// Whether a Textus window is in front, open behind others, or none is
// open, for plugins' textus.app.state. Moving from one window to another
// is a blur then a focus: the state is read once both have landed.

const (
	// eventAppState tells every window the state changed. Payload: the
	// state (AppState in src/platform/bindings/schedule.ts).
	eventAppState  = "app-state"
	appStateSettle = 100 * time.Millisecond
)

// The states.
const (
	// stateActive: a Textus window has the focus.
	stateActive = "active"
	// stateInactive: windows are open, none focused.
	stateInactive = "inactive"
	// stateBackground: no window is open (macOS with every window closed).
	stateBackground = "background"
)

type appStateWatch struct {
	mu   sync.Mutex
	last string
}

// isUserWindow is a window of the user's: not one Textus keeps out of
// sight (the background host, a PDF being laid out, the dragged tab) or a
// plugin's floating view.
func isUserWindow(label string) bool {
	kind := kindOf(label)
	return kind == kindEditor || kind == kindChat
}

// stateOf is the state from each user window's focus.
func stateOf(focused []bool) string {
	if len(focused) == 0 {
		return stateBackground
	}
	for _, f := range focused {
		if f {
			return stateActive
		}
	}
	return stateInactive
}

// currentAppState looks at the windows now.
func (a *App) currentAppState() string {
	front := a.windowOf(mygo.FocusedWindow())
	var focused []bool
	for _, w := range a.allWindows() {
		if !isUserWindow(w.label) || w.win.IsDestroyed() || !w.win.IsVisible() {
			continue
		}
		focused = append(focused, w == front)
	}
	return stateOf(focused)
}

// appStateChanged: a window gained or lost focus, opened or closed. Once
// the state has settled, the windows are told if it changed, and a click
// that couldn't come back is stood in for as Textus comes to the front.
func (a *App) appStateChanged() {
	time.AfterFunc(appStateSettle, func() {
		now := a.currentAppState()
		a.appState.mu.Lock()
		before := a.appState.last
		a.appState.last = now
		a.appState.mu.Unlock()
		if before == now {
			return
		}
		if now == stateActive {
			a.notificationActivated()
		}
		a.emitAll(eventAppState, now)
	})
}

func init() {
	command("app_state", func(c *Call, _ none) (string, error) {
		return c.app.currentAppState(), nil
	})
}
