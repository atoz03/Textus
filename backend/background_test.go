package backend

import (
	"bytes"
	"encoding/binary"
	"encoding/json/jsontext"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

func TestClosingTheLastWindowKeepsTheProcessOnMacOSOrInBackgroundMode(t *testing.T) {
	for _, c := range []struct {
		goos       string
		background bool
		keeps      bool
	}{
		{"darwin", false, true},
		{"darwin", true, true},
		{"windows", true, true},
		{"linux", true, true},
		{"windows", false, false},
		{"linux", false, false},
	} {
		if got := keepsRunning(c.goos, c.background); got != c.keeps {
			t.Errorf("%s, background %v: keeps running %v, want %v", c.goos, c.background, got, c.keeps)
		}
	}
}

func TestTurningBackgroundModeOffEndsAWindowlessProcessExceptOnMacOS(t *testing.T) {
	if !endsWhenTurnedOff("windows", false) || !endsWhenTurnedOff("linux", false) {
		t.Error("a windowless process stayed on Windows or Linux")
	}
	if endsWhenTurnedOff("darwin", false) {
		t.Error("macOS ended its windowless process")
	}
	for _, goos := range []string{"darwin", "windows", "linux"} {
		if endsWhenTurnedOff(goos, true) {
			t.Errorf("%s ended with a window open", goos)
		}
	}
}

func TestSettingsLearnTheTrayIsOffUntilItIsMade(t *testing.T) {
	a := &App{}
	raw, err := a.dispatch(nil, "background_status", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `{"tray":"off"}` {
		t.Errorf("background_status = %s", raw)
	}
	a.bg.status = trayUnavailable
	if raw, _ := a.dispatch(nil, "background_status", nil, nil); string(raw) != `{"tray":"unavailable"}` {
		t.Errorf("background_status = %s", raw)
	}
}

func TestALanguageChangeWithoutATrayLeavesItAlone(t *testing.T) {
	a := &App{}
	// No tray is up: nothing is made, nothing is told.
	a.backgroundChanged(jsonObject{"language": jsontext.Value(`"de"`)})
	if a.bg.status != "" || a.bg.tray != nil {
		t.Errorf("a language change made the tray: %q", a.bg.status)
	}
	// Neither setting: nothing at all.
	a.backgroundChanged(jsonObject{"theme": jsontext.Value(`"dark"`)})
	if a.bg.status != "" {
		t.Errorf("an unrelated setting touched the tray: %q", a.bg.status)
	}
}

// localeString is `key: "text"` (or `text` with ${APP_NAME} or {name}) in
// a locale file, as the interface shows it.
func localeString(t *testing.T, source, key string) string {
	t.Helper()
	m := regexp.MustCompile(`(?m)^\s*` + key + `:\s*["` + "`" + `](.*)["` + "`" + `],?\s*$`).FindStringSubmatch(source)
	if m == nil {
		t.Fatalf("no %s", key)
	}
	return strings.NewReplacer("${APP_NAME}", "Textus", "{name}", "Textus").Replace(m[1])
}

func TestTheTrayMenuSaysWhatTheInterfaceSaysInEveryLanguage(t *testing.T) {
	files, err := filepath.Glob("../src/i18n/locales/*.ts")
	if err != nil || len(files) == 0 {
		t.Fatalf("no locale files: %v", err)
	}
	for _, file := range files {
		language := strings.TrimSuffix(filepath.Base(file), ".ts")
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := trayTexts[language]; !ok {
			t.Errorf("no tray labels in %s", language)
			continue
		}
		open, quit := trayLabels(language)
		if want := localeString(t, string(raw), "trayOpenTextus"); open != want {
			t.Errorf("%s: Open is %q, the interface says %q", language, open, want)
		}
		if want := localeString(t, string(raw), "menuQuit"); quit != want {
			t.Errorf("%s: Quit is %q, the interface says %q", language, quit, want)
		}
	}
}

func TestAnUnknownLanguageGetsEnglishInTheTray(t *testing.T) {
	if open, quit := trayLabels("xx"); open != "Open Textus" || quit != "Quit Textus" {
		t.Errorf("%q %q", open, quit)
	}
	if open, _ := trayLabels(""); open != "Open Textus" {
		t.Errorf("%q", open)
	}
}

func TestTheTrayMenuListsThePluginsCommandsBetweenOpenAndQuit(t *testing.T) {
	ids := func(items []trayMenuItem) []string {
		var out []string
		for _, it := range items {
			out = append(out, it.id)
		}
		return out
	}
	if got := ids(trayMenuItems("en", nil)); !slices.Equal(got, []string{"tray:open", "", "tray:quit"}) {
		t.Errorf("without plugins: %q", got)
	}
	items := trayMenuItems("fr", []trayPluginItem{{Command: "a.b:sync", Label: "Sync & Save"}})
	if got := ids(items); !slices.Equal(got, []string{"tray:open", "", "tray:plugin:a.b:sync", "", "tray:quit"}) {
		t.Errorf("with a plugin: %q", got)
	}
	if items[0].label != "Ouvrir Textus" || items[2].label != "Sync & Save" || items[4].label != "Quitter Textus" {
		t.Errorf("labels: %+v", items)
	}
}

func TestOnlyWindowsReadsAnAmpersandAsAMnemonic(t *testing.T) {
	if got := trayMenuLabel("windows", "Sync & Save"); got != "Sync && Save" {
		t.Errorf("windows: %q", got)
	}
	for _, goos := range []string{"darwin", "linux"} {
		if got := trayMenuLabel(goos, "Sync & Save"); got != "Sync & Save" {
			t.Errorf("%s: %q", goos, got)
		}
	}
}

func TestATrayCommandWithNoWindowOpenWaitsForTheWindowThatOpensForIt(t *testing.T) {
	a := &App{windows: map[string]*appWindow{}}
	if w := a.trayPluginCommand("a.b:sync"); w != nil {
		t.Fatalf("ran in %s with no window open", w.label)
	}
	raw, err := a.dispatch(nil, "take_pending_plugin_command", nil, nil)
	if err != nil || string(raw) != `"a.b:sync"` {
		t.Fatalf("pending = %s, %v", raw, err)
	}
	// Taken once.
	if raw, _ := a.dispatch(nil, "take_pending_plugin_command", nil, nil); string(raw) != "null" {
		t.Errorf("taken twice: %s", raw)
	}
	// With an editor window open, it runs there and nothing waits.
	a.windows["window-2"] = &appWindow{label: "window-2"}
	if w := a.trayPluginCommand("a.b:sync"); w == nil || w.label != "window-2" {
		t.Errorf("ran in %v", w)
	}
	if raw, _ := a.dispatch(nil, "take_pending_plugin_command", nil, nil); string(raw) != "null" {
		t.Errorf("waited with a window open: %s", raw)
	}
}

func TestTheTrayKeepsThePluginsCommandsTheWindowSends(t *testing.T) {
	a := &App{}
	args := `{"items":[{"command":"a.b:sync","label":"Sync"}]}`
	if _, err := a.dispatch(nil, "set_tray_plugin_items", []byte(args), nil); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(a.plug.trayItems, []trayPluginItem{{Command: "a.b:sync", Label: "Sync"}}) {
		t.Errorf("kept %+v", a.plug.trayItems)
	}
}

func TestWindowsGetsTheBitmapItsDisplayScaleAsksFor(t *testing.T) {
	for wanted, size := range map[int]int{16: 16, 20: 20, 24: 24, 18: 20, 0: 16, 64: 48} {
		if got := windowsTraySize(wanted); got != size {
			t.Errorf("%d px: got the %d px bitmap, want %d", wanted, got, size)
		}
	}
}

// pngHeader is a PNG's width and height and whether it has an alpha
// channel (colour type 6, RGBA), from its header.
func pngHeader(t *testing.T, png []byte) (int, int, bool) {
	t.Helper()
	if len(png) < 26 || !bytes.Equal(png[1:4], []byte("PNG")) {
		t.Fatal("not a PNG")
	}
	return int(binary.BigEndian.Uint32(png[16:20])), int(binary.BigEndian.Uint32(png[20:24])), png[25] == 6
}

func TestEveryTrayIconIsThereAtItsSizeWithTransparency(t *testing.T) {
	for _, size := range windowsTraySizes {
		png, template := trayIconFor("windows", size)
		w, h, alpha := pngHeader(t, png)
		if w != size || h != size || !alpha || template {
			t.Errorf("windows %d: %dx%d alpha %v template %v", size, w, h, alpha, template)
		}
	}
	png, template := trayIconFor("darwin", 0)
	if w, h, alpha := pngHeader(t, png); fmt.Sprint(w, h, alpha, template) != "36 36 true true" {
		t.Errorf("macOS: %dx%d alpha %v template %v", w, h, alpha, template)
	}
	png, template = trayIconFor("linux", 0)
	if w, h, alpha := pngHeader(t, png); fmt.Sprint(w, h, alpha, template) != "64 64 true false" {
		t.Errorf("Linux: %dx%d alpha %v template %v", w, h, alpha, template)
	}
}
