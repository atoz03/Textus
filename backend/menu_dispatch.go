package backend

import (
	"runtime"
	"strings"
	"time"

	"github.com/egoist/mygo"
)

// What a menu id runs, from the native bar, a window's own menu or a
// popup menu, and the menu commands the frontend calls.

// emitOrOpenWindow runs a menu command that needs a window: in the focused
// one, or in a new one opened for it.
func (a *App) emitOrOpenWindow(id, origin string) {
	if label := a.focusedEditorWindow(origin); label != "" {
		_ = a.emitTo(label, "menu-"+id, nil)
		return
	}
	a.mu.Lock()
	a.pendingMenuEvent = &id
	a.mu.Unlock()
	_ = a.openNewWindow()
}

// dispatchMenu runs a menu command, from the native bar or a window's own
// menu (origin, that window's label).
func (a *App) dispatchMenu(id, origin string) {
	focused := a.focusedEditorWindow(origin)
	emitFocused := func(event string, payload any) {
		if focused != "" {
			_ = a.emitTo(focused, event, payload)
		}
	}
	withPayload := [][2]string{
		{pluginCommandPfx, "menu-plugin-command"},
		{exportPandocPrefix, "menu-export-pandoc"},
		{pluginExportPrefix, "menu-export-plugin"},
		{insertBlockPrefix, "menu-insert-block"},
		{toggleMarkPrefix, "menu-toggle-mark"},
	}
	for _, p := range withPayload {
		if rest, ok := strings.CutPrefix(id, p[0]); ok {
			emitFocused(p[1], rest)
			return
		}
	}
	switch {
	case id == "new-file":
		// New File: a tab in the focused window; a window
		// when there is none.
		if focused != "" {
			emitFocused("menu-new-file", nil)
		} else {
			_ = a.openNewWindow()
		}
	case id == managePluginsID:
		a.emitOrOpenWindow(managePluginsID, origin)
	case id == recentClearID:
		a.clearRecents()
	case strings.HasPrefix(id, recentFolderPrefix):
		path := strings.TrimPrefix(id, recentFolderPrefix)
		if focused != "" {
			emitFocused("menu-open-recent-folder", path)
		} else {
			a.openFolderWindow(path)
		}
	case strings.HasPrefix(id, recentPrefix):
		path := strings.TrimPrefix(id, recentPrefix)
		if focused != "" {
			emitFocused("menu-open-recent", path)
		} else {
			a.queuePathsToOpen([]string{path})
		}
	case id == "quit":
		a.quit()
	case id == "close-window":
		// close(), not destroy(), so the page's unsaved-tab prompt runs.
		if w := a.windowOf(mygo.FocusedWindow()); w != nil {
			w.win.Close()
		} else if origin != "" {
			if w := a.window(origin); w != nil {
				w.win.Close()
			}
		}
	case id == "new-window":
		_ = a.openNewWindow()
	case id == "help-community" || id == "help-tutorials" || id == "help-sponsor":
		// A window knows the UI's language; with none open, the site picks.
		if focused != "" {
			emitFocused("menu-"+id, nil)
		} else {
			switch id {
			case "help-community":
				openCommunity(nil)
			case "help-sponsor":
				openSponsor(nil)
			default:
				openTutorials(nil, nil)
			}
		}
	case strings.HasPrefix(id, helpDocPrefix):
		doc := strings.TrimPrefix(id, helpDocPrefix)
		if focused != "" {
			emitFocused("menu-open-help", doc)
		} else {
			a.mu.Lock()
			a.pendingShowHelp = &doc
			a.mu.Unlock()
			_ = a.openNewWindow()
		}
	case id == toggleTextusAIID:
		emitFocused("menu-toggle-textus-ai", nil)
	default:
		item, ok := a.menu.findItem(id)
		if !ok {
			item, ok = a.menu.findOwn(id)
		}
		if !ok {
			return
		}
		name := item.ID
		if name == "" {
			name = item.Predefined
		}
		if item.OrOpen {
			a.emitOrOpenWindow(name, origin)
		} else {
			emitFocused("menu-"+name, nil)
		}
	}
}

// popupMenu shows a menu of labels ("" a separator) at the pointer over
// the window and returns the index chosen.
func popupMenu(w *appWindow, labels []string) *int {
	chosen := make(chan int, 1)
	items := make([]*mygo.MenuItem, 0, len(labels))
	for i, label := range labels {
		if label == "" {
			items = append(items, mygo.Separator())
			continue
		}
		items = append(items, &mygo.MenuItem{Label: label, Click: func(*mygo.MenuItem, *mygo.Window) {
			select {
			case chosen <- i:
			default:
			}
		}})
	}
	mygo.NewMenu(items).Popup(w.win)
	// A click is handled on the main thread, at the latest just after the
	// menu closes.
	mygo.RunOnMain(func() {})
	select {
	case i := <-chosen:
		return &i
	case <-time.After(100 * time.Millisecond):
		return nil
	}
}

func init() {
	command("set_menu_labels", func(c *Call, a struct {
		Labels map[string]string `json:"labels"`
	}) (nothing, error) {
		m := c.app.menu
		m.mu.Lock()
		same := len(m.labels) == len(a.Labels)
		if same {
			for k, v := range a.Labels {
				if m.labels[k] != v {
					same = false
					break
				}
			}
		}
		if a.Labels != nil {
			m.labels = a.Labels
		}
		m.mu.Unlock()
		if !same {
			c.app.rebuildMenu()
		}
		return nil, nil
	})
	command("set_menu_checks", func(c *Call, a struct {
		Checks map[string]bool `json:"checks"`
	}) (nothing, error) {
		m := c.app.menu
		m.mu.Lock()
		if a.Checks != nil {
			m.checks = a.Checks
		}
		m.mu.Unlock()
		if runtime.GOOS == "darwin" {
			if bar := mygo.App.Menu(); bar != nil {
				for id, on := range a.Checks {
					if item := bar.ItemByID(id); item != nil {
						item.SetChecked(on)
					}
				}
			}
		}
		return nil, nil
	})
	command("set_menu_accelerators", func(c *Call, a struct {
		Accelerators map[string]string `json:"accelerators"`
	}) (nothing, error) {
		m := c.app.menu
		m.mu.Lock()
		same := len(m.accelerators) == len(a.Accelerators)
		if same {
			for k, v := range a.Accelerators {
				if m.accelerators[k] != v {
					same = false
					break
				}
			}
		}
		if a.Accelerators != nil {
			m.accelerators = a.Accelerators
		}
		m.mu.Unlock()
		if !same {
			c.app.rebuildMenu()
		}
		return nil, nil
	})
	command("trigger_menu_item", func(c *Call, a struct {
		ID string `json:"id"`
	}) (nothing, error) {
		c.app.dispatchMenu(a.ID, c.Label())
		return nil, nil
	})
	command("list_recent_files", func(c *Call, _ none) ([]string, error) {
		return stillThere(c.app.recentList("recentFiles")), nil
	})
	command("list_recent_folders", func(c *Call, _ none) ([]string, error) {
		return stillThere(c.app.recentList("recentFolders")), nil
	})
	command("add_recent_file", func(c *Call, a struct {
		Path string `json:"path"`
	}) (nothing, error) {
		c.app.writeRecentList("recentFiles", pushFront(c.app.recentList("recentFiles"), a.Path, maxRecentFiles))
		c.app.rebuildMenu()
		return nil, nil
	})
	command("add_recent_folder", func(c *Call, a struct {
		Path string `json:"path"`
	}) (nothing, error) {
		c.app.writeRecentList("recentFolders", pushFront(c.app.recentList("recentFolders"), a.Path, maxRecentFolders))
		c.app.rebuildMenu()
		return nil, nil
	})
	command("remove_recent", func(c *Call, a struct {
		Path string `json:"path"`
	}) (nothing, error) {
		for _, name := range []string{"recentFiles", "recentFolders"} {
			c.app.writeRecentList(name, removeString(c.app.recentList(name), a.Path))
		}
		c.app.rebuildMenu()
		return nil, nil
	})
	// The menu a small window shows at the pointer (native-menu.ts): the
	// index of the label chosen, null when none was.
	command("popup_menu", func(c *Call, a struct {
		Labels []string `json:"labels"`
	}) (*int, error) {
		if c.window == nil {
			return nil, nil
		}
		return popupMenu(c.window, a.Labels), nil
	})
}
