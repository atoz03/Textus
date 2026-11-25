package backend

import (
	"embed"
	"encoding/json/v2"
	"fmt"
	"log"
	"runtime"
	"slices"
	"strings"

	"github.com/egoist/mygo"
)

// The tray icon of background mode (background.go), made when the setting
// turns on and removed when it turns off. Its menu: Open Textus, the
// plugins' commands, Quit Textus.

const (
	// trayID names the icon across launches. There is only ever one.
	trayID = "textus"
	// Menu ids, "tray:<item>".
	trayMenuPrefix   = "tray:"
	trayOpenApp      = "open"
	trayQuit         = "quit"
	trayPluginPrefix = "plugin:" // "tray:plugin:<command>": a plugin's command
)

// trayIcons are the icons made by scripts/make-tray-icons.sh.
//
//go:embed icons/tray/*.png
var trayIcons embed.FS

// windowsTraySizes are the sizes of icons/tray/windows-<n>.png.
var windowsTraySizes = []int{16, 20, 24, 32, 40, 48}

// windowsTraySize is the smallest bitmap at least wanted pixels wide, or
// the largest: scaling down blurs less than scaling up.
func windowsTraySize(wanted int) int {
	for _, size := range windowsTraySizes {
		if size >= wanted {
			return size
		}
	}
	return windowsTraySizes[len(windowsTraySizes)-1]
}

// trayIconFor is the icon for goos, and whether it is a template image:
// macOS paints that in the menu bar's own colour, light or dark. Windows
// gets the bitmap at the size the notification area draws on this
// display (smallIcon pixels), so it doesn't scale one into a blur.
func trayIconFor(goos string, smallIcon int) ([]byte, bool) {
	name, template := "linux.png", false
	switch goos {
	case "darwin":
		name, template = "macos-template.png", true
	case "windows":
		name = fmt.Sprintf("windows-%d.png", windowsTraySize(smallIcon))
	}
	png, err := trayIcons.ReadFile("icons/tray/" + name)
	if err != nil {
		panic(err) // embedded above
	}
	return png, template
}

// trayTexts are the menu's labels, Open Textus and Quit Textus, by UI
// language: the backend's own, as the tray can be up before any window
// has sent the page's. Each matches the locale files (trayOpenTextus and
// menuQuit in src/i18n/locales/; a test holds them to it).
var trayTexts = map[string][2]string{
	"en":      {"Open Textus", "Quit Textus"},
	"zh":      {"打开 Textus", "退出 Textus"},
	"zh-Hant": {"打開 Textus", "結束 Textus"},
	"ja":      {"Textus を開く", "Textusを終了"},
	"ko":      {"Textus 열기", "Textus 종료"},
	"es":      {"Abrir Textus", "Salir de Textus"},
	"fr":      {"Ouvrir Textus", "Quitter Textus"},
	"de":      {"Textus öffnen", "Textus beenden"},
	"ru":      {"Открыть Textus", "Завершить Textus"},
	"pt":      {"Abrir o Textus", "Encerrar Textus"},
}

// trayLabels are the labels in language, English for one Textus doesn't
// speak.
func trayLabels(language string) (open, quit string) {
	texts, ok := trayTexts[language]
	if !ok {
		texts = trayTexts["en"]
	}
	return texts[0], texts[1]
}

// trayMenuLabel is label as goos's menus show it: Windows reads a lone
// ampersand as the mark of a mnemonic.
func trayMenuLabel(goos, label string) string {
	if goos == "windows" {
		return strings.ReplaceAll(label, "&", "&&")
	}
	return label
}

// trayMenuItem is one entry of the tray's menu: its id ("" for a
// separator) and label.
type trayMenuItem struct{ id, label string }

// trayMenuItems is the menu: Open Textus, the plugins' commands between
// separators, Quit Textus.
func trayMenuItems(language string, plugins []trayPluginItem) []trayMenuItem {
	open, quit := trayLabels(language)
	items := []trayMenuItem{{trayMenuPrefix + trayOpenApp, open}}
	if len(plugins) > 0 {
		items = append(items, trayMenuItem{})
		for _, p := range plugins {
			items = append(items, trayMenuItem{trayMenuPrefix + trayPluginPrefix + p.Command, p.Label})
		}
	}
	return append(items, trayMenuItem{}, trayMenuItem{trayMenuPrefix + trayQuit, quit})
}

// uiLanguage is the language the UI is set to.
func (a *App) uiLanguage() string {
	var language string
	if raw := a.readSetting("language"); raw != nil {
		_ = json.Unmarshal(raw, &language)
	}
	return language
}

// trayMenu is the tray's menu as it is now.
func (a *App) trayMenu() *mygo.Menu {
	a.plug.mu.Lock()
	plugins := slices.Clone(a.plug.trayItems)
	a.plug.mu.Unlock()
	var items []*mygo.MenuItem
	for _, it := range trayMenuItems(a.uiLanguage(), plugins) {
		if it.id == "" {
			items = append(items, mygo.Separator())
			continue
		}
		id := strings.TrimPrefix(it.id, trayMenuPrefix)
		items = append(items, &mygo.MenuItem{
			ID: it.id, Label: trayMenuLabel(runtime.GOOS, it.label),
			Click: func(*mygo.MenuItem, *mygo.Window) { go a.trayClicked(id) },
		})
	}
	return mygo.NewMenu(items)
}

// trayClicked runs a tray menu item (its id without the prefix).
func (a *App) trayClicked(item string) {
	if command, ok := strings.CutPrefix(item, trayPluginPrefix); ok {
		w := a.trayPluginCommand(command)
		a.reopenWithoutWindows()
		if w != nil {
			_ = w.emit("menu-plugin-command", command)
		}
		return
	}
	switch item {
	case trayOpenApp:
		a.reopenWithoutWindows()
	case trayQuit:
		a.quit()
	}
}

// trayPluginCommand is the editor window to run a plugin's command from
// the tray, the one focused last; with none open, the command waits for
// the window that opens for it (take_pending_plugin_command), and nil.
func (a *App) trayPluginCommand(command string) *appWindow {
	if w := a.lastEditor(); w != nil {
		return w
	}
	a.plug.mu.Lock()
	a.plug.pendingCommand = &command
	a.plug.mu.Unlock()
	return nil
}

// syncTray makes, relabels or removes the tray as on says, and answers
// what it is now. The caller holds a.bg.mu.
func (a *App) syncTray(on bool) string {
	if !on {
		if a.bg.tray != nil {
			if a.bg.offClick != nil {
				a.bg.offClick()
				a.bg.offClick = nil
			}
			a.bg.tray.Destroy()
			a.bg.tray = nil
		}
		return trayOff
	}
	if a.bg.tray != nil {
		// Already up: the language or the plugins may have changed.
		a.bg.tray.SetMenu(a.trayMenu())
		return trayOn
	}
	icon, template := trayIconFor(runtime.GOOS, smallIconSize())
	tray, err := mygo.NewTray(mygo.TrayOptions{
		ID: trayID, Icon: icon, IconIsTemplate: template, ToolTip: "Textus", Menu: a.trayMenu(),
	})
	if err != nil {
		log.Printf("textus: background: no tray icon: %v", err)
		return trayUnavailable
	}
	a.bg.tray = tray
	// A click opens Textus where the menu has a click of its own to wait
	// for: Windows' left click (the right one opens the menu), Linux's
	// activation. macOS opens the menu on any click.
	if runtime.GOOS != "darwin" {
		a.bg.offClick = tray.OnClick(func() { go a.reopenWithoutWindows() })
	}
	return trayOn
}

// rebuildTray gives the tray, while there is one, its menu as it is now.
func (a *App) rebuildTray() {
	a.bg.mu.Lock()
	defer a.bg.mu.Unlock()
	if a.bg.tray != nil {
		a.bg.tray.SetMenu(a.trayMenu())
	}
}
