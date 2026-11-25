package backend

import (
	"log"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"slices"
	"strings"

	"github.com/egoist/mygo"

	"textus/backend/plugins"
)

// What the plugins that are on put in the app's own places: the Plugins
// menu, File › Export's formats and the tray's commands, as the window
// last sent them; textus:// links for them; and their files by URL.

// pluginMenuItem is one command in the Plugins menu, as the frontend sends
// it (PluginMenuItem in src/platform/bindings/app.ts).
type pluginMenuItem struct {
	Command string `json:"command"`
	Label   string `json:"label"`
	// Group is the plugin's name: two or more of its commands go in a
	// submenu of it. "" (or null) for none.
	Group string `json:"group"`
}

// pluginExportItem is one format a plugin adds to File › Export
// (PluginExportItem in src/platform/bindings/app.ts).
type pluginExportItem struct {
	Key   string `json:"key"`
	Label string `json:"label"`
}

// trayPluginItem is one plugin command in the tray's menu
// (TrayPluginItem in src/platform/bindings/app.ts).
type trayPluginItem struct {
	Command string `json:"command"`
	Label   string `json:"label"`
}

// pluginGroup is a plugin's name and its commands in the Plugins menu.
type pluginGroup struct {
	name  string
	items []pluginMenuItem
}

// byPlugin is the items by plugin, in name order.
func byPlugin(items []pluginMenuItem) []pluginGroup {
	var groups []pluginGroup
	for _, item := range items {
		key := item.Group
		if key == "" {
			key = item.Label
		}
		at := slices.IndexFunc(groups, func(g pluginGroup) bool { return g.name == key })
		if at < 0 {
			groups = append(groups, pluginGroup{name: key})
			at = len(groups) - 1
		}
		groups[at].items = append(groups[at].items, item)
	}
	slices.SortStableFunc(groups, func(x, y pluginGroup) int {
		return strings.Compare(strings.ToLower(x.name), strings.ToLower(y.name))
	})
	return groups
}

// pluginMenuItems is the Plugins menu's section: the plugins' commands,
// a plugin with several in a submenu of its own, then Manage Plugins….
func (b *menuBuilder) pluginMenuItems() []*mygo.MenuItem {
	b.a.plug.mu.Lock()
	items := slices.Clone(b.a.plug.menuItems)
	b.a.plug.mu.Unlock()
	var out []*mygo.MenuItem
	if len(items) == 0 {
		out = append(out, &mygo.MenuItem{ID: noPluginCommandsID, Label: b.text(noPluginCommandsID, b.m.texts[noPluginCommandsID]), Disabled: true})
	}
	command := func(item pluginMenuItem) *mygo.MenuItem {
		id := pluginCommandPfx + item.Command
		return &mygo.MenuItem{ID: id, Label: item.Label, Click: b.click(id)}
	}
	for _, group := range byPlugin(items) {
		if len(group.items) == 1 {
			out = append(out, command(group.items[0]))
			continue
		}
		var inner []*mygo.MenuItem
		for _, item := range group.items {
			inner = append(inner, command(item))
		}
		out = append(out, &mygo.MenuItem{Label: group.name, Type: mygo.MenuItemSubmenu, Submenu: inner})
	}
	return append(out, mygo.Separator(),
		&mygo.MenuItem{ID: managePluginsID, Label: b.text(managePluginsID, b.m.texts[managePluginsID]), Click: b.click(managePluginsID)})
}

// pluginExportMenuItems are the formats plugins add to File › Export,
// after pandoc's.
func (b *menuBuilder) pluginExportMenuItems() []*mygo.MenuItem {
	b.a.plug.mu.Lock()
	formats := slices.Clone(b.a.plug.exports)
	b.a.plug.mu.Unlock()
	var out []*mygo.MenuItem
	for _, f := range formats {
		id := pluginExportPrefix + f.Key
		out = append(out, &mygo.MenuItem{ID: id, Label: f.Label, Click: b.click(id)})
	}
	return out
}

// keepPluginItems keeps what the window sent in slot; false when nothing
// changed.
func keepPluginItems[T comparable](a *App, slot *[]T, items []T) bool {
	a.plug.mu.Lock()
	defer a.plug.mu.Unlock()
	if slices.Equal(*slot, items) {
		return false
	}
	*slot = slices.Clone(items)
	return true
}

// deliverURI queues a textus:// link for the windows and tells them one
// came; a notification's click goes to the notifications instead.
func (a *App) deliverURI(u string) {
	if !plugins.IsTextusURI(u) {
		return
	}
	if id, ok := plugins.NotificationID(u); ok {
		a.notificationClicked(id)
		return
	}
	a.plug.mu.Lock()
	a.plug.uris = append(a.plug.uris, u)
	a.plug.mu.Unlock()
	a.emitAll("plugin-uri", nil)
}

// listenForURIs takes the textus:// links the system hands Textus, the one
// it was started with too. Linux (an AppImage has no installer) and a
// Windows development build register the scheme as they run; macOS's
// comes with the app (urlSchemes in mygo.json).
func (a *App) listenForURIs() {
	if os.Getenv("MYGO_GENERATE") != "" {
		return
	}
	if runtime.GOOS == "linux" || runtime.GOOS == "windows" && mygo.IsDev() {
		if err := mygo.App.RegisterURLScheme(plugins.URIScheme); err != nil {
			log.Printf("textus: cannot register %s://: %v", plugins.URIScheme, err)
		}
	}
	mygo.App.OnOpenURL(a.deliverURI)
}

// servePluginFile answers textus-plugin://localhost/<id>/<path>: a
// plugin's own files, by URL, for its sandboxed frames. Inlined as data:
// URLs instead, a large module (PlantUML's 3 MB engine) took WebKit
// gigabytes to load.
func (a *App) servePluginFile(w http.ResponseWriter, r *http.Request) {
	// Never to a site in a tab: what plugins there are is the user's.
	if a.fromWebPage(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	// The frames' origin is opaque: a module script is only run from a
	// response that lets any origin have it.
	w.Header().Set("Access-Control-Allow-Origin", "*")
	data, mime, err := a.pluginFileAt(r.URL.EscapedPath())
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", mime)
	_, _ = w.Write(data)
}

// pluginFileAt is the file a textus-plugin:// path (/<id>/<rel>, percent
// encoded) names, inside that plugin's own folder, and its type.
func (a *App) pluginFileAt(escaped string) ([]byte, string, error) {
	path, err := url.PathUnescape(strings.TrimLeft(escaped, "/"))
	if err != nil {
		return nil, "", err
	}
	id, rel, ok := strings.Cut(path, "/")
	if !ok {
		return nil, "", errorf("No file named.")
	}
	// A folder in development holds its .env and .git too.
	if plugins.HiddenPart(rel) {
		return nil, "", errorf("Hidden files aren't served.")
	}
	home, err := a.pluginHome(a.pluginState(), id)
	if err != nil {
		return nil, "", err
	}
	file, err := plugins.Inside(home, rel)
	if err != nil {
		return nil, "", err
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, "", err
	}
	return data, plugins.Mime(rel), nil
}

func init() {
	// The commands the plugins that are on put in the Plugins menu,
	// refilled when they change. The native menu bar is macOS's; elsewhere
	// the window draws its own from the same list.
	command("set_plugin_menu", func(c *Call, a struct {
		Items []pluginMenuItem `json:"items"`
	}) (nothing, error) {
		if keepPluginItems(c.app, &c.app.plug.menuItems, a.Items) {
			c.app.rebuildMenu()
		}
		return nil, nil
	})
	// The export formats the plugins that are on add to File › Export.
	command("set_plugin_exports", func(c *Call, a struct {
		Items []pluginExportItem `json:"items"`
	}) (nothing, error) {
		if keepPluginItems(c.app, &c.app.plug.exports, a.Items) {
			c.app.rebuildMenu()
		}
		return nil, nil
	})
	// The plugin commands the tray's menu lists.
	command("set_tray_plugin_items", func(c *Call, a struct {
		Items []trayPluginItem `json:"items"`
	}) (nothing, error) {
		if keepPluginItems(c.app, &c.app.plug.trayItems, a.Items) {
			c.app.rebuildTray()
		}
		return nil, nil
	})
	// The tray command waiting for a window to run it, taken.
	command("take_pending_plugin_command", func(c *Call, _ none) (*string, error) {
		c.app.plug.mu.Lock()
		defer c.app.plug.mu.Unlock()
		pending := c.app.plug.pendingCommand
		c.app.plug.pendingCommand = nil
		return pending, nil
	})
	// Hands the waiting textus:// links to the window that asks, and
	// forgets them.
	command("plugin_take_uris", func(c *Call, _ none) ([]string, error) {
		c.app.plug.mu.Lock()
		defer c.app.plug.mu.Unlock()
		uris := c.app.plug.uris
		c.app.plug.uris = nil
		if uris == nil {
			uris = []string{}
		}
		return uris, nil
	})
}
