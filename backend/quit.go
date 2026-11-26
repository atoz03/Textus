package backend

import (
	"slices"

	"github.com/egoist/mygo"
)

// How the app ends. Every quit - the menu's, the tray's, the `textus`
// command's, the Dock's, a signal's - goes through quit (session.go): each
// window is asked to close, so an unsaved document gets its prompt, and
// the app exits once the last one is gone. Windows the user never sees
// (plugins' background host, a floating view, a PDF being laid out) never
// keep it running.

// endProcess ends the process: MyGo's quit, or its relaunch once an update
// is installed. Tests replace it.
var endProcess = func(relaunch bool) {
	if relaunch {
		mygo.App.Relaunch()
		return
	}
	mygo.App.Quit()
}

// exit ends the app once its windows are gone, or starts the version an
// update installed: once, until MyGo cancels the quit. The tray icon goes
// first, while the event loop still runs: Windows keeps the icon of a
// process that ended in the notification area until a pointer passes it.
func (a *App) exit() {
	if !a.exiting.CompareAndSwap(false, true) {
		return
	}
	// Off the main thread, which taking the icon down waits for.
	go func() {
		a.removeTray()
		endProcess(relaunchAfterQuit.Load())
	}()
}

// takeOverQuit makes a quit MyGo starts itself - the Dock's or the app
// switcher's Quit on macOS, SIGINT or SIGTERM - the app's own. MyGo's
// closes the windows itself, and a window whose page asks first refuses
// it, which cancels the quit and leaves the app running once the pages
// have closed their windows. Not the quit exit asks for, and not the
// session's end, which nothing can stop: its windows close unasked, kept
// to reopen as a quit keeps them.
func (a *App) takeOverQuit(e *mygo.QuitEvent) {
	switch {
	case a.exiting.Load():
	case e.SessionEnding:
		if !a.quitting.Load() {
			a.rememberOpenWindows()
			a.beginQuit()
		}
	default:
		e.PreventDefault()
		if !a.quitting.Load() {
			go a.quit()
		}
	}
}

// quitCanceled: MyGo's quit was cancelled after exit asked for it, so the
// app runs on, with its tray if background mode is on.
func (a *App) quitCanceled() {
	if a.exiting.CompareAndSwap(true, false) && a.readFlag(backgroundSetting) {
		go a.applyBackground(changeStartup)
	}
}

// removeTray takes the tray icon down as the app ends.
func (a *App) removeTray() {
	a.bg.mu.Lock()
	defer a.bg.mu.Unlock()
	a.bg.status = a.syncTray(false)
}

// removeTrayAtExit takes down an icon left by a quit that never went
// through exit (the session's end), as MyGo finishes. Skipped while a
// change to the tray holds the lock: it may be waiting for the main
// thread this runs on, and the icon goes with the process anyway.
func (a *App) removeTrayAtExit() {
	if a.bg.mu.TryLock() {
		a.bg.status = a.syncTray(false)
		a.bg.mu.Unlock()
	}
}

// afterClose is what the app does once a window is gone.
type afterClose int

const (
	// runOn: windows are left, or nothing ends the app.
	runOn afterClose = iota
	// endApp: the last window of a quit is gone.
	endApp
	// quitApp: the last window of the user's is gone and nothing keeps the
	// app running: the windows they don't see close with it.
	quitApp
)

// afterWindowGone is what follows window closed going, with left still
// open: an editor and a chat are the user's; the others (isUserWindow)
// never keep the app running.
func afterWindowGone(goos string, background, quitting bool, closed string, left []string) afterClose {
	switch {
	case quitting && len(left) == 0:
		return endApp
	case quitting, !isUserWindow(closed), keepsRunning(goos, background),
		slices.ContainsFunc(left, isUserWindow):
		return runOn
	}
	return quitApp
}

// quitAsked says whether `textus commands` asks to quit, which needs no
// window: one opened to take it would only close again.
func quitAsked(args []string) bool {
	return slices.Equal(args, []string{"run", "quit"})
}
