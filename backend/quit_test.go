package backend

import (
	"context"
	"encoding/json/v2"
	"os"
	"runtime"
	"testing"
	"time"

	"github.com/egoist/mygo"

	"textus/backend/cli"
)

// endings stands in for MyGo's quit: each end of the process, true for a
// relaunch, arrives on the channel.
func endings(t *testing.T) <-chan bool {
	t.Helper()
	ended := make(chan bool, 4)
	was := endProcess
	endProcess = func(relaunch bool) { ended <- relaunch }
	t.Cleanup(func() { endProcess = was })
	return ended
}

func waitForEnd(t *testing.T, ended <-chan bool) bool {
	t.Helper()
	select {
	case relaunch := <-ended:
		return relaunch
	case <-time.After(5 * time.Second):
		t.Fatal("the app never ended")
		return false
	}
}

func noEnd(t *testing.T, ended <-chan bool) {
	t.Helper()
	select {
	case <-ended:
		t.Fatal("the app ended")
	case <-time.After(100 * time.Millisecond):
	}
}

func TestTheLastWindowOfAQuitEndsTheApp(t *testing.T) {
	if got := afterWindowGone("linux", false, true, "main", nil); got != endApp {
		t.Errorf("last window of a quit: %v", got)
	}
	if got := afterWindowGone("darwin", true, true, "window-2", []string{"main"}); got != runOn {
		t.Errorf("a window of a quit with others left: %v", got)
	}
}

func TestWindowsTheUserNeverSeesDontKeepTheAppRunning(t *testing.T) {
	hidden := []string{"background-host", "floating-a_b", "pdf-export-1", "drag-pill"}
	for _, goos := range []string{"windows", "linux"} {
		for _, closed := range []string{"main", "window-3", "chat-4"} {
			if got := afterWindowGone(goos, false, false, closed, hidden); got != quitApp {
				t.Errorf("%s, %s closed beside hidden windows: %v, want quit", goos, closed, got)
			}
		}
		if got := afterWindowGone(goos, false, false, "main", nil); got != quitApp {
			t.Errorf("%s, the last window closed: %v", goos, got)
		}
	}
}

func TestTheAppRunsOnWhileSomethingKeepsIt(t *testing.T) {
	for _, c := range []struct {
		name       string
		goos       string
		background bool
		closed     string
		left       []string
	}{
		{"a window of the user's left", "linux", false, "main", []string{"window-2"}},
		{"a chat left", "windows", false, "main", []string{"chat-2", "background-host"}},
		{"background mode", "windows", true, "main", []string{"background-host"}},
		{"macOS keeps running windowless", "darwin", false, "main", nil},
		// The background host closing after its run in background mode, or
		// a floating view: never the user's last window.
		{"a hidden window closed", "linux", false, "background-host", nil},
		{"a floating view closed", "windows", false, "floating-x", []string{"main"}},
	} {
		if got := afterWindowGone(c.goos, c.background, false, c.closed, c.left); got != runOn {
			t.Errorf("%s: %v, want run on", c.name, got)
		}
	}
}

func TestClosingTheLastWindowEndsTheAppWithoutBackgroundMode(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("a Mac app keeps running windowless (TestTheAppRunsOnWhileSomethingKeepsIt)")
	}
	ended := endings(t)
	main := &appWindow{label: "main"}
	a := &App{windows: map[string]*appWindow{"main": main}}
	a.windowGone(main)
	if waitForEnd(t, ended) {
		t.Error("relaunched")
	}
	if !a.quitting.Load() {
		t.Error("ended without quitting: the session would shrink")
	}
}

func TestAQuitWithNoWindowEndsTheAppOnce(t *testing.T) {
	ended := endings(t)
	a := &App{windows: map[string]*appWindow{}}
	a.quit()
	a.quit()
	a.exit()
	if waitForEnd(t, ended) {
		t.Error("relaunched")
	}
	noEnd(t, ended)
}

func TestAQuitAfterAnUpdateRelaunches(t *testing.T) {
	ended := endings(t)
	relaunchAfterQuit.Store(true)
	t.Cleanup(func() { relaunchAfterQuit.Store(false) })
	a := &App{windows: map[string]*appWindow{}}
	a.quit()
	if !waitForEnd(t, ended) {
		t.Error("quit instead of relaunching")
	}
}

func TestTheTrayGoesBeforeTheAppEnds(t *testing.T) {
	a := &App{windows: map[string]*appWindow{}}
	a.bg.status = trayOn
	tray := make(chan string, 1)
	was := endProcess
	endProcess = func(bool) { tray <- a.currentBackgroundStatus().Tray }
	t.Cleanup(func() { endProcess = was })
	a.exit()
	select {
	case status := <-tray:
		if status != trayOff {
			t.Errorf("the tray was %q as the app ended", status)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the app never ended")
	}
}

func TestTheTrayLeftAtExitIsNeverWaitedFor(t *testing.T) {
	a := &App{}
	a.bg.status = trayOn
	// A change to the tray holds the lock, maybe waiting for the main
	// thread: the exit goes on without it.
	a.bg.mu.Lock()
	done := make(chan struct{})
	go func() {
		a.removeTrayAtExit()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("waited for the tray's lock at exit")
	}
	a.bg.mu.Unlock()
	if a.bg.status != trayOn {
		t.Errorf("status %q", a.bg.status)
	}
	a.removeTrayAtExit()
	if a.bg.status != trayOff {
		t.Errorf("the icon stayed: %q", a.bg.status)
	}
}

func TestAQuitMyGoStartsRunsAsTheAppsOwn(t *testing.T) {
	ended := endings(t)
	a := &App{windows: map[string]*appWindow{}}
	e := &mygo.QuitEvent{}
	a.takeOverQuit(e)
	if !e.DefaultPrevented() {
		t.Fatal("MyGo's quit went on: a window that asks first would cancel it")
	}
	waitForEnd(t, ended)
	// exit's own quit is the one MyGo carries out.
	again := &mygo.QuitEvent{}
	if a.takeOverQuit(again); again.DefaultPrevented() {
		t.Error("the quit exit asked for was stopped")
	}
}

func TestAQuitMyGoStartsDuringTheAppsOwnWaitsForIt(t *testing.T) {
	ended := endings(t)
	a := &App{windows: map[string]*appWindow{}}
	a.quitting.Store(true)
	e := &mygo.QuitEvent{}
	a.takeOverQuit(e)
	if !e.DefaultPrevented() {
		t.Error("MyGo's quit went on while the windows were asked")
	}
	noEnd(t, ended)
}

func TestTheSessionsEndKeepsTheWindowsToReopen(t *testing.T) {
	ended := endings(t)
	a := &App{
		dirs:    Dirs{Config: t.TempDir()},
		windows: map[string]*appWindow{},
		sessions: map[string]WindowSession{
			"main":     {Paths: []string{"/notes/a.md"}},
			"window-2": {Paths: []string{"/notes/b.md"}},
		},
	}
	e := &mygo.QuitEvent{SessionEnding: true}
	a.takeOverQuit(e)
	if e.DefaultPrevented() {
		t.Error("tried to stop the session's end")
	}
	// The system closes the windows one by one.
	a.forgetWindowSession("main")
	raw, err := os.ReadFile(a.sessionFile())
	if err != nil {
		t.Fatal(err)
	}
	var kept []WindowSession
	if err := json.Unmarshal(raw, &kept); err != nil {
		t.Fatal(err)
	}
	if len(kept) != 2 {
		t.Errorf("kept %d windows to reopen, want 2: %s", len(kept), raw)
	}
	noEnd(t, ended)
}

func TestAQuitCancelledAfterExitLetsTheNextOneThrough(t *testing.T) {
	ended := endings(t)
	a := &App{windows: map[string]*appWindow{}}
	a.exit()
	waitForEnd(t, ended)
	a.quitCanceled()
	a.exit()
	waitForEnd(t, ended)
}

func TestTextusCommandsRunQuitQuitsWithoutAWindow(t *testing.T) {
	for _, c := range []struct {
		args []string
		want bool
	}{
		{[]string{"run", "quit"}, true},
		{[]string{"run", "quit", "now"}, false},
		{[]string{"run", "new-window"}, false},
		{[]string{"list"}, false},
		{nil, false},
	} {
		if got := quitAsked(c.args); got != c.want {
			t.Errorf("%q: %v", c.args, got)
		}
	}
	ended := endings(t)
	a := &App{windows: map[string]*appWindow{}}
	got := a.handleCLI(context.Background(), cli.Request{Command: "commands", Args: []string{"run", "quit"}})
	if got != cli.OK("") {
		t.Errorf("answer %+v", got)
	}
	waitForEnd(t, ended)
	if len(a.windows) != 0 {
		t.Error("a window opened to take the quit")
	}
}

func TestNoBackgroundHostOpensWhileTheAppQuits(t *testing.T) {
	a := &App{windows: map[string]*appWindow{}}
	a.quitting.Store(true)
	a.openBackgroundHost()
	if a.window(backgroundHostLabel) != nil {
		t.Error("the background host opened during a quit")
	}
}

// closings stands in for closing a window unasked: MyGo then reports it
// closed (windowGone), and its label arrives on the channel.
func closings(t *testing.T, a *App) <-chan string {
	t.Helper()
	closed := make(chan string, 4)
	was := closeUnasked
	closeUnasked = func(w *appWindow) {
		closed <- w.label
		a.windowGone(w)
	}
	t.Cleanup(func() { closeUnasked = was })
	return closed
}

func noClosing(t *testing.T, closed <-chan string) {
	t.Helper()
	select {
	case label := <-closed:
		t.Errorf("%s closed unasked", label)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestAQuitEndsTheAppOnceThePageItAskedIsGone(t *testing.T) {
	// `timeout` sends SIGTERM to the app, then to its process group: the
	// quit asks the page, whose process the same signal ended.
	ended := endings(t)
	main := &appWindow{label: "main"}
	main.closeListened.Store(true)
	a := &App{windows: map[string]*appWindow{"main": main}}
	closed := closings(t, a)
	a.quitting.Store(true)
	a.pageGone(main, "crashed")
	if waitForEnd(t, ended) {
		t.Error("relaunched")
	}
	if label := <-closed; label != "main" {
		t.Errorf("closed %s", label)
	}
	if main.closeListened.Load() {
		t.Error("a close would still go to the page that is gone")
	}
}

func TestAWindowWhosePageIsGoneClosesUnasked(t *testing.T) {
	ended := endings(t)
	main := &appWindow{label: "main"}
	main.closeListened.Store(true)
	a := &App{windows: map[string]*appWindow{"main": main}}
	closed := closings(t, a)
	a.pageGone(main, "terminated")
	if main.closeListened.Load() {
		t.Error("the next close would go to the page that is gone")
	}
	// Outside a quit the window stays until it is closed.
	noClosing(t, closed)
	noEnd(t, ended)
}

func TestAPageThatHangsIsStillAsked(t *testing.T) {
	ended := endings(t)
	main := &appWindow{label: "main"}
	main.closeListened.Store(true)
	a := &App{windows: map[string]*appWindow{"main": main}}
	closed := closings(t, a)
	a.quitting.Store(true)
	a.pageGone(main, pageUnresponsive)
	if !main.closeListened.Load() {
		t.Error("a page that may answer yet is no longer asked")
	}
	noClosing(t, closed)
	noEnd(t, ended)
}
