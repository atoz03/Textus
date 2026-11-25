package backend

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo"

	"textus/backend/plugins"
	"textus/backend/schedule"
)

// Plugins' system notifications, and their click: Textus comes up with the
// file the notification names open at its line. Where a click can't come
// back (a Linux notification server without actions, a macOS build that
// can't show them) the next time Textus becomes active within a minute
// stands in for it.

const (
	notificationClicksFile = "notification-clicks.json"
	// keptTargets and keptTargetsMS: how many clicked-to-be targets are
	// kept, and for how long - a click can come after Textus restarted.
	keptTargets   = 50
	keptTargetsMS = 7 * 24 * 3_600_000
	// clickFallbackMS is how long "the next activation" stands in for a
	// click that can't come.
	clickFallbackMS = 60_000
	// eventNotificationOpen tells an editor window a notification was
	// clicked: it takes what to open with notify_take_open.
	eventNotificationOpen = "notification-open"
)

// notificationTarget is what a click on notification ID opens.
type notificationTarget struct {
	ID    string                    `json:"id"`
	Open  schedule.NotificationOpen `json:"open"`
	Shown int64                     `json:"shown"`
}

// notifications is what clicks open, and the open waiting for a window.
type notifications struct {
	mu       sync.Mutex
	targets  []notificationTarget
	loaded   bool
	pending  *schedule.NotificationOpen
	fallback *notificationTarget
}

func (a *App) notificationClicksFile() string {
	return filepath.Join(a.pluginsRoot(), notificationClicksFile)
}

// loadTargets reads the targets from disk the first time; the caller holds
// a.notes.mu.
func (a *App) loadTargets() {
	if a.notes.loaded {
		return
	}
	a.notes.targets, _ = readJSONStore[[]notificationTarget](a.notificationClicksFile())
	a.notes.loaded = true
}

// newNotificationID is an id no one can guess, so a textus:// link from a
// web page can't name one.
func newNotificationID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// resolveNotificationOpen is the file a notification opens, made absolute
// inside root: a plugin names a file of the folder it was granted, nothing
// else.
func resolveNotificationOpen(open schedule.NotificationOpen, root *string) (schedule.NotificationOpen, error) {
	if root == nil {
		return schedule.NotificationOpen{}, errors.New("A notification opens a file of the open folder; none is open.")
	}
	path, err := plugins.Inside(*root, open.Path)
	if err != nil {
		return schedule.NotificationOpen{}, err
	}
	return schedule.NotificationOpen{Path: path, Line: open.Line}, nil
}

// keepTarget remembers what a click on id opens, dropping the old ones.
func (a *App) keepTarget(id string, open schedule.NotificationOpen, now int64) {
	a.notes.mu.Lock()
	defer a.notes.mu.Unlock()
	a.loadTargets()
	kept := a.notes.targets[:0:0]
	for _, t := range a.notes.targets {
		if now-t.Shown < keptTargetsMS {
			kept = append(kept, t)
		}
	}
	kept = append(kept, notificationTarget{ID: id, Open: open, Shown: now})
	if len(kept) > keptTargets {
		kept = kept[len(kept)-keptTargets:]
	}
	a.notes.targets = kept
	if err := writePrettyJSON(a.notificationClicksFile(), kept); err != nil {
		log.Printf("textus: notifications: %v", err)
	}
}

// showNotification shows a plugin's notification, its grants checked
// again: notifications, and files.read for a file to open.
func (a *App) showNotification(ctx context.Context, plugin string, spec schedule.NotificationSpec, root *string) error {
	s := a.pluginState()
	if !s.Allows(plugin, "notifications") {
		return plugins.Refused(plugin, "notifications")
	}
	var open *schedule.NotificationOpen
	if spec.Open != nil {
		if !s.Allows(plugin, "files.read") {
			return plugins.Refused(plugin, "files.read")
		}
		resolved, err := resolveNotificationOpen(*spec.Open, root)
		if err != nil {
			return err
		}
		open = &resolved
	}
	id := newNotificationID()
	if open != nil {
		a.keepTarget(id, *open, nowMS())
	}
	clicksReturn, err := showSystemNotification(ctx, id, strings.TrimSpace(spec.Title), spec.Body)
	if err != nil {
		return err
	}
	if !clicksReturn && open != nil {
		a.notes.mu.Lock()
		a.notes.fallback = &notificationTarget{Open: *open, Shown: nowMS()}
		a.notes.mu.Unlock()
	}
	return nil
}

// showSystemNotification shows a notification; whether its click comes
// back to OnNotificationClick.
func showSystemNotification(ctx context.Context, id, title, body string) (bool, error) {
	if !mygo.NotificationsSupported() {
		switch runtime.GOOS {
		case "darwin":
			// A build outside an app bundle has no Notification Center.
			log.Printf("notification (not shown: an unbundled build): %s - %s", title, body)
			return false, nil
		case "linux":
			return false, errors.New("No notification server on the session bus.")
		}
		return false, errors.New("Notifications can't be shown here.")
	}
	n := mygo.NewNotification(mygo.NotificationOptions{ID: id, Title: title, Body: body})
	if err := n.ShowContext(ctx); err != nil {
		if errors.Is(err, mygo.ErrNotificationsDenied) {
			return false, errors.New("Textus isn't allowed to show notifications: turn them on in System Settings › Notifications.")
		}
		return false, err
	}
	return serverHasActions(), nil
}

// serverHasActions is whether the Linux notification server shows the
// action a click comes back through; elsewhere a click always does. Asked
// once; a server that can't be asked is taken to have them.
var serverHasActions = sync.OnceValue(func() bool {
	if runtime.GOOS != "linux" {
		return true
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "gdbus", "call", "--session",
		"--dest", "org.freedesktop.Notifications",
		"--object-path", "/org/freedesktop/Notifications",
		"--method", "org.freedesktop.Notifications.GetCapabilities").Output()
	if err != nil {
		return true
	}
	return strings.Contains(string(out), "'actions'")
})

// notificationClicked runs a click on notification id: the file it opens,
// or Textus brought up.
func (a *App) notificationClicked(id string) {
	a.notes.mu.Lock()
	a.loadTargets()
	var open *schedule.NotificationOpen
	for _, t := range a.notes.targets {
		if t.ID == id {
			o := t.Open
			open = &o
		}
	}
	a.notes.mu.Unlock()
	switch {
	case open != nil:
		a.openNotificationTarget(*open)
	case a.stores != nil:
		a.reopenWithoutWindows()
	}
}

// notificationActivated: Textus came to the front, a click that couldn't
// come back, shortly after.
func (a *App) notificationActivated() {
	a.notes.mu.Lock()
	waiting := a.notes.fallback
	a.notes.fallback = nil
	a.notes.mu.Unlock()
	if waiting != nil && nowMS()-waiting.Shown < clickFallbackMS {
		a.openNotificationTarget(waiting.Open)
	}
}

// openNotificationTarget brings Textus up with open waiting for a window:
// the front editor window is told, or one opens and takes it as it mounts.
func (a *App) openNotificationTarget(open schedule.NotificationOpen) {
	a.notes.mu.Lock()
	a.notes.pending = &open
	a.notes.mu.Unlock()
	// Before the app is ready, the first window takes it as it opens.
	if a.stores == nil {
		return
	}
	if w := a.window(a.focusedEditorWindow("")); w != nil {
		if w.win.IsMinimized() {
			w.win.Restore()
		}
		w.win.Show()
		w.win.Focus()
		_ = w.emit(eventNotificationOpen, nil)
		return
	}
	a.reopenWithoutWindows()
}

func (a *App) takeNotificationOpen() *schedule.NotificationOpen {
	a.notes.mu.Lock()
	defer a.notes.mu.Unlock()
	open := a.notes.pending
	a.notes.pending = nil
	return open
}
