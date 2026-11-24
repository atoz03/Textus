package backend

import (
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo"
)

// The native menu bar, built from the menu table: macOS only.

var predefinedRoles = map[string]mygo.MenuRole{
	"about":              mygo.RoleAbout,
	"hide":               mygo.RoleHide,
	"hide-others":        mygo.RoleHideOthers,
	"show-all":           mygo.RoleUnhide,
	"undo":               mygo.RoleUndo,
	"redo":               mygo.RoleRedo,
	"cut":                mygo.RoleCut,
	"copy":               mygo.RoleCopy,
	"paste":              mygo.RolePaste,
	"select-all":         mygo.RoleSelectAll,
	"fullscreen":         mygo.RoleToggleFullScreen,
	"minimize":           mygo.RoleMinimize,
	"maximize":           mygo.RoleZoom,
	"bring-all-to-front": mygo.RoleFront,
}

type menuBuilder struct {
	a            *App
	m            *menuState
	labels       map[string]string
	checks       map[string]bool
	accelerators map[string]string
	recentFiles  []string
	recentDirs   []string
}

func (b *menuBuilder) text(key, english string) string {
	if l, ok := b.labels[key]; ok && l != "" {
		return l
	}
	return named(english)
}

func (b *menuBuilder) click(id string) func(*mygo.MenuItem, *mygo.Window) {
	return func(*mygo.MenuItem, *mygo.Window) { go b.a.dispatchMenu(id, "") }
}

func (b *menuBuilder) submenu(title string, nodes []menuNode) *mygo.MenuItem {
	children := []*mygo.MenuItem{}
	for _, n := range nodes {
		switch {
		case n.line:
			children = append(children, mygo.Separator())
		case n.isSep():
			if here(n.On) {
				children = append(children, mygo.Separator())
			}
		case n.isMenu():
			children = append(children, b.submenu(b.text("menu:"+n.Menu, n.En), n.Items))
		case n.isSection():
			children = append(children, b.section(n.Section)...)
		case here(n.On):
			children = append(children, b.item(n))
		}
	}
	return &mygo.MenuItem{Label: title, Type: mygo.MenuItemSubmenu, Submenu: children}
}

func (b *menuBuilder) item(n menuNode) *mygo.MenuItem {
	text := b.text(n.labelKey(), n.En)
	if n.Suffix != "" && !strings.HasSuffix(text, n.Suffix) {
		text += n.Suffix
	}
	if n.Predefined != "" && !(n.MacOwn && menuPlatform == "mac") {
		if role, ok := predefinedRoles[n.Predefined]; ok {
			return &mygo.MenuItem{Label: text, Role: role, Accelerator: n.accelerator()}
		}
		return mygo.Separator()
	}
	// A macOS-own item (undo, redo reach WebKit's history, never the
	// editor's) runs in the focused window under its name.
	id := n.ID
	if id == "" {
		id = n.Predefined
	}
	item := &mygo.MenuItem{ID: id, Label: text, Accelerator: n.accelerator(), Click: b.click(id)}
	if n.Check {
		item.Type = mygo.MenuItemCheckbox
		item.Checked = b.checks[id]
	}
	return item
}

func (b *menuBuilder) section(name string) []*mygo.MenuItem {
	var out []*mygo.MenuItem
	switch name {
	case "headings":
		for level := 1; level <= 6; level++ {
			id := insertBlockPrefix + "h" + strconv.Itoa(level)
			english := strings.ReplaceAll(b.m.texts["heading"], "{n}", strconv.Itoa(level))
			out = append(out, &mygo.MenuItem{ID: id, Label: b.text(id, english), Accelerator: b.accelerators[id], Click: b.click(id)})
		}
	case "pandoc":
		for _, f := range b.m.pandoc {
			id := exportPandocPrefix + f.Format
			out = append(out, &mygo.MenuItem{ID: id, Label: b.text(id, f.Menu), Click: b.click(id)})
		}
	case "recent":
		if len(b.recentFiles) == 0 && len(b.recentDirs) == 0 {
			return []*mygo.MenuItem{{ID: recentNoneID, Label: b.text(recentNoneID, b.m.texts[recentNoneID]), Disabled: true}}
		}
		for _, p := range b.recentDirs {
			id := recentFolderPrefix + p
			out = append(out, &mygo.MenuItem{ID: id, Label: folderMenuText(p), Click: b.click(id)})
		}
		if len(b.recentDirs) > 0 && len(b.recentFiles) > 0 {
			out = append(out, mygo.Separator())
		}
		for _, p := range b.recentFiles {
			id := recentPrefix + p
			out = append(out, &mygo.MenuItem{ID: id, Label: abbreviateHome(p), Click: b.click(id)})
		}
		out = append(out, mygo.Separator(), &mygo.MenuItem{ID: recentClearID, Label: b.text(recentClearID, b.m.texts[recentClearID]), Click: b.click(recentClearID)})
	case "plugins":
		// This build runs no plugins: none has a command to show.
		out = append(out,
			&mygo.MenuItem{ID: noPluginCommandsID, Label: b.text(noPluginCommandsID, b.m.texts[noPluginCommandsID]), Disabled: true},
			mygo.Separator(),
			&mygo.MenuItem{ID: managePluginsID, Label: b.text(managePluginsID, b.m.texts[managePluginsID]), Click: b.click(managePluginsID)},
		)
	}
	return out
}

func (a *App) buildNativeMenu() *mygo.Menu {
	m := a.menu
	m.mu.Lock()
	b := &menuBuilder{a: a, m: m, labels: copyMap(m.labels), checks: copyMap(m.checks), accelerators: copyMap(m.accelerators)}
	m.mu.Unlock()
	b.recentFiles = stillThere(a.recentList("recentFiles"))
	b.recentDirs = stillThere(a.recentList("recentFolders"))
	var bar []*mygo.MenuItem
	for _, menu := range m.bar {
		// The app menu is titled by the app's name on every platform.
		title := named(menu.En)
		if menu.Menu != "app" {
			title = b.text("menu:"+menu.Menu, menu.En)
		}
		bar = append(bar, b.submenu(title, menu.Items))
	}
	return mygo.NewMenu(bar)
}

func copyMap[K comparable, V any](m map[K]V) map[K]V {
	out := make(map[K]V, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// installMenu sets the menu bar: the app's on macOS, none elsewhere, where
// each window draws its own.
func (a *App) installMenu() {
	if runtime.GOOS != "darwin" {
		mygo.App.SetMenu(nil)
		return
	}
	mygo.App.SetMenu(a.buildNativeMenu())
}

// rebuildMenu builds the bar again after what it shows changed; changes
// that come together make one rebuild.
func (a *App) rebuildMenu() {
	if runtime.GOOS != "darwin" || a.menu == nil {
		return
	}
	m := a.menu
	m.mu.Lock()
	if m.pending {
		m.mu.Unlock()
		return
	}
	m.pending = true
	m.mu.Unlock()
	time.AfterFunc(30*time.Millisecond, func() {
		m.mu.Lock()
		m.pending = false
		m.mu.Unlock()
		mygo.App.SetMenu(a.buildNativeMenu())
	})
}
