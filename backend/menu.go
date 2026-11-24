package backend

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"log"
	"runtime"
	"strings"
	"sync"
)

// The menu bar, from src/registry/menu/menu.json. Only macOS shows the
// system's: on Linux and Windows the app draws its own (src/app/menu) and
// sends what it runs through trigger_menu_item.

const (
	recentPrefix       = "recent:"
	recentFolderPrefix = "recent-folder:"
	recentClearID      = "recent-clear"
	recentNoneID       = "recent-none"
	exportPandocPrefix = "export-pandoc:"
	insertBlockPrefix  = "insert-block:"
	toggleMarkPrefix   = "toggle-mark:"
	helpDocPrefix      = "help-doc:"
	pluginCommandPfx   = "plugin-command:"
	pluginExportPrefix = "plugin-export:"
	managePluginsID    = "manage-plugins"
	noPluginCommandsID = "plugin-commands-none"
	toggleTextusAIID   = "toggle-textus-ai"
	maxRecentFiles     = 10
	maxRecentFolders   = 5
)

// notYetInMenu are the items of what this build can't do yet, left off
// the bar: src/platform/capabilities.ts leaves them out of the drawn menu.
var notYetInMenu = []string{"export-pdf", "check-updates"}

// menuPlatform is how menu.json names this platform.
var menuPlatform = map[string]string{"darwin": "mac", "windows": "windows"}[runtime.GOOS]

func init() {
	if menuPlatform == "" {
		menuPlatform = "linux"
	}
}

type menuNode struct {
	// A submenu.
	Menu     string           `json:"menu"`
	RawItems []jsontext.Value `json:"items"`
	Items    []menuNode       `json:"-"`
	// A part made from another list.
	Section string `json:"section"`
	// A separator on some platforms.
	Sep bool `json:"sep"`
	// An item.
	ID         string   `json:"id"`
	Predefined string   `json:"predefined"`
	En         string   `json:"en"`
	Suffix     string   `json:"suffix"`
	Keys       string   `json:"keys"`
	KeysOff    []string `json:"keysOff"`
	On         []string `json:"on"`
	MacOwn     bool     `json:"macOwn"`
	Check      bool     `json:"check"`
	OrOpen     bool     `json:"orOpen"`
	// "-", a separator everywhere.
	line bool
}

func (n menuNode) isMenu() bool    { return n.RawItems != nil || n.Items != nil }
func (n menuNode) isSection() bool { return n.Section != "" }
func (n menuNode) isSep() bool     { return n.Sep }

func parseMenuNodes(raw []jsontext.Value) []menuNode {
	out := make([]menuNode, 0, len(raw))
	for _, v := range raw {
		if v.Kind() == '"' {
			out = append(out, menuNode{line: true})
			continue
		}
		var n menuNode
		if err := json.Unmarshal(v, &n); err != nil {
			log.Printf("textus: menu.json: %v", err)
			continue
		}
		if n.RawItems != nil {
			n.Items = parseMenuNodes(n.RawItems)
			n.RawItems = nil
			if n.Items == nil {
				n.Items = []menuNode{}
			}
		}
		out = append(out, n)
	}
	return out
}

func here(on []string) bool {
	return len(on) == 0 || containsString(on, menuPlatform)
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// labelKey is the key an item's label is sent under: its id, or
// "predefined:<name>".
func (n menuNode) labelKey() string {
	if n.ID != "" {
		return n.ID
	}
	if n.Predefined != "" {
		return "predefined:" + n.Predefined
	}
	return ""
}

// accelerator is the item's keys as the menu reads them ("mod+shift+s" is
// "CmdOrCtrl+Shift+s"), unless this platform goes without.
func (n menuNode) accelerator() string {
	if n.Keys == "" || containsString(n.KeysOff, menuPlatform) {
		return ""
	}
	return accelerator(n.Keys)
}

func accelerator(combo string) string {
	parts := strings.Split(combo, "+")
	for i, p := range parts {
		switch p {
		case "mod":
			parts[i] = "CmdOrCtrl"
		case "ctrl":
			parts[i] = "Ctrl"
		case "alt":
			parts[i] = "Alt"
		case "shift":
			parts[i] = "Shift"
		case "meta":
			parts[i] = "Super"
		}
	}
	return strings.Join(parts, "+")
}

func named(text string) string { return strings.ReplaceAll(text, "{name}", "Textus") }

type pandocFormat struct {
	Format string `json:"format"`
	Ext    string `json:"ext"`
	Menu   string `json:"menu"`
}

// menuState is the table and what the pages last sent of the native bar.
type menuState struct {
	bar    []menuNode
	texts  map[string]string
	pandoc []pandocFormat

	mu           sync.Mutex
	labels       map[string]string
	checks       map[string]bool
	accelerators map[string]string
	pending      bool // a rebuild is on its way
}

func newMenuState(table, formats []byte) *menuState {
	m := &menuState{texts: map[string]string{}, labels: map[string]string{}, checks: map[string]bool{}, accelerators: map[string]string{}}
	var t struct {
		Bar   []jsontext.Value `json:"bar"`
		Texts []struct {
			Key string `json:"key"`
			En  string `json:"en"`
		} `json:"texts"`
	}
	if err := json.Unmarshal(table, &t); err != nil {
		log.Printf("textus: menu.json: %v", err)
	}
	m.bar = parseMenuNodes(t.Bar)
	for _, text := range t.Texts {
		m.texts[text.Key] = text.En
	}
	if err := json.Unmarshal(formats, &m.pandoc); err != nil {
		log.Printf("textus: export-formats.json: %v", err)
	}
	return m
}

func (m *menuState) findItem(id string) (menuNode, bool) {
	var walk func(nodes []menuNode) (menuNode, bool)
	walk = func(nodes []menuNode) (menuNode, bool) {
		for _, n := range nodes {
			switch {
			case n.isMenu():
				if found, ok := walk(n.Items); ok {
					return found, true
				}
			case n.isSection(), n.isSep(), n.line:
			case n.ID == id:
				return n, true
			}
		}
		return menuNode{}, false
	}
	for _, menu := range m.bar {
		if found, ok := walk(menu.Items); ok {
			return found, true
		}
	}
	return menuNode{}, false
}

// findOwn is a predefined item macOS runs as the app's own, by name.
func (m *menuState) findOwn(name string) (menuNode, bool) {
	if menuPlatform != "mac" {
		return menuNode{}, false
	}
	var walk func(nodes []menuNode) (menuNode, bool)
	walk = func(nodes []menuNode) (menuNode, bool) {
		for _, n := range nodes {
			if n.isMenu() {
				if found, ok := walk(n.Items); ok {
					return found, true
				}
			} else if !n.isSection() && !n.isSep() && !n.line && n.MacOwn && n.Predefined == name {
				return n, true
			}
		}
		return menuNode{}, false
	}
	for _, menu := range m.bar {
		if found, ok := walk(menu.Items); ok {
			return found, true
		}
	}
	return menuNode{}, false
}
