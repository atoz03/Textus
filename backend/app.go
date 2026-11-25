// Package backend is the desktop side of Textus on MyGo: its windows, its
// files and settings, and the commands its frontend calls through Host.
package backend

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"

	"github.com/egoist/mygo"

	"textus/backend/ai/aicompat"
	"textus/backend/plugins"
)

// Shared are the files the frontend and the backend both read, embedded by
// the main package (Go embeds only files below the package's folder).
type Shared struct {
	Version          string
	BackendDefaults  []byte // src/prefs/backend-defaults.json
	AppearanceTables []byte // src/prefs/appearance-tables.json
	ContentThemes    string // src/theme/content-themes.css
	MenuTable        []byte // src/registry/menu/menu.json
	ErrorTable       []byte // src/i18n/errors.json
	ExportFormats    []byte // src/registry/export-formats.json
	ImageTypes       []byte // src/editor/images/image-types.json
	ErrorKinds       []byte // src/ai/ai-error-kinds.json
	Permissions      []byte // src/plugins/store/permissions.json
}

// App is the running app: its state shared by every command.
type App struct {
	shared   Shared
	dirs     Dirs
	stores   *Stores
	defaults map[string]bool

	mu             sync.Mutex
	windows        map[string]*appWindow
	recentFocus    []string
	sessions       map[string]WindowSession
	pending        []WindowSession
	pendingFolders [][2]string
	launchOpened   []string
	launchDone     bool

	pendingShowHelp  *string
	pendingMenuEvent *string
	argvPaths        map[string]int            // documents of command lines, not yet echoed as open-file events
	detachedTabs     map[string]jsontext.Value // a tab moved out, by its new window

	windowCounter   atomic.Uint32
	uiReady         atomic.Bool
	quitting        atomic.Bool
	openingAtLaunch atomic.Bool

	menu   *menuState
	drafts *draftStore
	chats  chatWindows
	plug   pluginRuntime
}

func newApp(shared Shared, dirs Dirs) *App {
	a := &App{
		shared:   shared,
		dirs:     dirs,
		windows:  map[string]*appWindow{},
		sessions: map[string]WindowSession{},
	}
	if err := json.Unmarshal(shared.BackendDefaults, &a.defaults); err != nil {
		log.Printf("textus: backend-defaults.json: %v", err)
	}
	appVersion = shared.Version
	loadErrorTable(shared.ErrorTable)
	loadImageTypes(shared.ImageTypes)
	if len(shared.ErrorKinds) > 0 {
		if err := aicompat.LoadErrorKinds(shared.ErrorKinds); err != nil {
			log.Printf("textus: ai-error-kinds.json: %v", err)
		}
	}
	if len(shared.Permissions) > 0 {
		if err := plugins.LoadPermissions(shared.Permissions); err != nil {
			log.Printf("textus: permissions.json: %v", err)
		}
	}
	plugins.Coded = codeError
	a.menu = newMenuState(shared.MenuTable, shared.ExportFormats)
	return a
}

// load reads the app's files: kept out of newApp, which `mygo generate`
// also runs, so that generating the client never touches a profile.
func (a *App) load() {
	a.stores = loadStores(a.dirs)
	a.drafts = loadDrafts(a.dirs)
	a.proxyChanged()
	// The document types plugins add, before a window lists a folder.
	a.refreshDocumentTypes()
}

// Run starts Textus and returns when it quits.
func Run(shared Shared) error {
	devBuild = mygo.IsDev()
	app := mygo.App
	app.SetName("Textus")
	if shared.Version != "" {
		app.SetVersion(shared.Version)
	}
	dirs, err := defaultDirs()
	if err != nil {
		return err
	}
	a := newApp(shared, dirs)
	mygo.Bind(&Host{app: a})
	registerSchemes(a)
	// Before Run, to receive the link Textus was started with.
	a.listenForURIs()

	// Windows and Linux start a new process per opened file: the first one
	// takes the others' documents. Not in development, so a dev build runs
	// beside an installed Textus.
	if !mygo.IsDev() && runtime.GOOS != "darwin" && os.Getenv("MYGO_GENERATE") == "" {
		if !app.RequestSingleInstanceLock() {
			return nil
		}
		app.OnSecondInstance(func(args []string, cwd string) {
			paths := pathsFromArgv(args, cwd)
			a.noteArgvPaths(paths)
			switch {
			case len(paths) > 0:
				a.queuePathsToOpen(paths)
			case a.lastEditor() == nil:
				a.reopenWithoutWindows()
			default:
				if err := a.openNewWindow(); err != nil {
					log.Printf("textus: cannot open a window: %v", err)
				}
			}
		})
	}
	// macOS hands documents over as open-file events. MyGo also passes the
	// documents of a command line on as such events, after the command line
	// itself was read here: those are already open.
	app.OnOpenFile(func(path string) {
		if a.takeArgvPath(path) {
			return
		}
		a.openedFromSystem([]string{path})
	})
	// Each window draws its own menu bar but on macOS, whose bar is set as
	// the app is ready.
	if runtime.GOOS != "darwin" {
		app.SetMenu(nil)
	}

	app.WhenReady(func() {
		a.load()
		go a.installMovedSkills(context.Background())
		a.installMenu()
		cwd, _ := os.Getwd()
		paths, windows := a.launchOpens(os.Args, cwd)
		if err := a.buildMainWindow(); err != nil {
			log.Printf("textus: cannot open the main window: %v", err)
		}
		switch {
		case len(paths) > 0:
			a.queuePathsToOpen(paths)
		case len(windows) > 0:
			a.queueWindowsToOpen(windows)
		}
	})
	if runtime.GOOS == "darwin" {
		// Closing the last window leaves the app running, as Mac apps do;
		// the Dock icon brings a window back.
		app.OnWindowAllClosed(func() {})
		app.OnActivate(func(visible bool) {
			if !visible && a.stores != nil {
				a.reopenWithoutWindows()
			}
		})
	}
	app.OnQuit(func() {
		if a.stores != nil {
			a.stores.flushAll()
		}
		if a.drafts != nil {
			a.drafts.flush()
		}
	})
	return app.Run()
}

// exit ends the app once its windows are gone, or starts the version an
// update installed.
func (a *App) exit() {
	if relaunchAfterQuit.Load() {
		mygo.App.Relaunch()
		return
	}
	mygo.App.Quit()
}

// readFlag is a boolean setting the backend acts on, or its default.
func (a *App) readFlag(key string) bool {
	if a.stores != nil {
		if raw, ok := a.stores.settings.get(key); ok {
			var v bool
			if json.Unmarshal(raw, &v) == nil {
				return v
			}
		}
	}
	return a.defaults[key]
}

// readSetting is one setting as JSON, nil when unset.
func (a *App) readSetting(key string) jsontext.Value {
	if a.stores == nil {
		return nil
	}
	v, _ := a.stores.settings.get(key)
	return v
}

func (a *App) keptPair(key string) (float64, float64, bool) {
	if a.stores == nil {
		return 0, 0, false
	}
	raw, ok := a.stores.uiState.get(key)
	if !ok {
		return 0, 0, false
	}
	var pair []float64
	if json.Unmarshal(raw, &pair) != nil || len(pair) < 2 {
		return 0, 0, false
	}
	return pair[0], pair[1], true
}

// editorWindowSize is the size the last editor window closed at; a corrupt
// or hand-edited value must not open an unusable window.
func (a *App) editorWindowSize() (float64, float64, bool) {
	w, h, ok := a.keptPair("editorWindowSize")
	return w, h, ok && w >= 320 && h >= 240
}

func (a *App) editorWindowPosition() (float64, float64, bool) {
	return a.keptPair("editorWindowPosition")
}

// rememberEditorWindow keeps a closing editor window's size and place for
// the next one, unless it is maximized, full screen or minimized.
func (a *App) rememberEditorWindow(w *appWindow) {
	win := w.win
	if a.stores == nil || win.IsMaximized() || win.IsFullScreen() || win.IsMinimized() {
		return
	}
	width, height := win.ContentSize()
	patch := jsonObject{"editorWindowSize": mustJSON([]int{width, height})}
	if !isWayland() {
		x, y := win.Position()
		patch["editorWindowPosition"] = mustJSON([]int{x, y})
	}
	a.stores.uiState.patch(patch)
}

// noteArgvPaths keeps the documents a command line named, which MyGo hands
// over again as open-file events.
func (a *App) noteArgvPaths(paths []string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.argvPaths == nil {
		a.argvPaths = map[string]int{}
	}
	for _, p := range paths {
		a.argvPaths[filepath.Clean(p)]++
	}
}

// takeArgvPath says whether an open-file event repeats a command line's
// document.
func (a *App) takeArgvPath(path string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	key := filepath.Clean(path)
	if a.argvPaths[key] == 0 {
		return false
	}
	a.argvPaths[key]--
	return true
}
