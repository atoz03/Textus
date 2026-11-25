package backend

import (
	"log"
	"runtime"
	"sync"

	"github.com/egoist/mygo"
)

// Background mode: with it on, closing the last window leaves Textus
// running with a tray icon (tray.go). Off by default; desktop only. A
// windowless process keeps no webview, and the tray exists only while the
// setting is on.

const (
	// backgroundSetting is the setting that turns background mode on.
	backgroundSetting = "runInBackground"
	// eventBackgroundStatus tells every window the tray changed. Payload:
	// backgroundStatus.
	eventBackgroundStatus = "background-status"
)

// The tray's states, for Settings.
const (
	trayOff = "off"
	trayOn  = "on"
	// trayUnavailable: no tray can be shown (Linux without
	// libayatana-appindicator); the process still stays.
	trayUnavailable = "unavailable"
)

// backgroundStatus is the tray, for Settings (Status in
// src/platform/bindings/app.ts).
type backgroundStatus struct {
	// Tray is "off", "on", or "unavailable".
	Tray string `json:"tray"`
}

// backgroundChange is what asks the tray to follow the settings.
type backgroundChange int

const (
	// changeStartup: the app started with background mode on.
	changeStartup backgroundChange = iota
	// changeSettings: the setting itself changed.
	changeSettings
	// changeLanguage: the UI language changed, which the tray menu's
	// labels follow.
	changeLanguage
)

// backgroundState is the tray while there is one, and what Settings is
// told of it.
type backgroundState struct {
	mu     sync.Mutex
	tray   *mygo.Tray
	status string
	// offClick stops the tray's click from reopening a window.
	offClick func()
}

// keepsRunning says whether closing the last window leaves the process
// running: always on macOS (the Dock), elsewhere in background mode.
func keepsRunning(goos string, background bool) bool {
	return goos == "darwin" || background
}

// endsWhenTurnedOff says whether background mode just turned off leaves
// the process with nothing to do: no window of the user's, and not macOS,
// which keeps running windowless anyway.
func endsWhenTurnedOff(goos string, userWindowsOpen bool) bool {
	return goos != "darwin" && !userWindowsOpen
}

// lastWindowClosed: MyGo's last window is gone. A quit exits on its own
// (windowGone); otherwise the app stays as keepsRunning says, or quits.
func (a *App) lastWindowClosed() {
	if a.quitting.Load() || keepsRunning(runtime.GOOS, a.readFlag(backgroundSetting)) {
		return
	}
	a.exit()
}

// userWindowsOpen says whether a window of the user's is open: an editor
// or a chat window, not one Textus keeps out of sight.
func (a *App) userWindowsOpen() bool {
	for _, w := range a.allWindows() {
		if isUserWindow(w.label) {
			return true
		}
	}
	return false
}

// applyBackground brings the tray in line with the settings and tells
// every window how that went. Turned off with no window of the user's
// open, the app quits (but on macOS).
func (a *App) applyBackground(change backgroundChange) {
	on := a.readFlag(backgroundSetting)
	a.bg.mu.Lock()
	if change == changeLanguage && a.bg.tray == nil {
		a.bg.mu.Unlock()
		return
	}
	status := a.syncTray(on)
	a.bg.status = status
	a.bg.mu.Unlock()
	a.emitAll(eventBackgroundStatus, backgroundStatus{Tray: status})
	if !on && change == changeSettings && endsWhenTurnedOff(runtime.GOOS, a.userWindowsOpen()) {
		log.Printf("textus: background mode is off and no window is open; quitting")
		a.quit()
	}
}

// backgroundChanged follows a settings patch: background mode itself, or
// the UI language the tray's menu says.
func (a *App) backgroundChanged(patch jsonObject) {
	if _, ok := patch[backgroundSetting]; ok {
		a.applyBackground(changeSettings)
	} else if _, ok := patch["language"]; ok {
		a.applyBackground(changeLanguage)
	}
}

// currentBackgroundStatus is the tray now.
func (a *App) currentBackgroundStatus() backgroundStatus {
	a.bg.mu.Lock()
	defer a.bg.mu.Unlock()
	if a.bg.status == "" {
		return backgroundStatus{Tray: trayOff}
	}
	return backgroundStatus{Tray: a.bg.status}
}

func init() {
	// The tray now, for Settings as it opens.
	command("background_status", func(c *Call, _ none) (backgroundStatus, error) {
		return c.app.currentBackgroundStatus(), nil
	})
}
