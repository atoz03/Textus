package backend

import (
	_ "embed"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf16"
)

//go:embed boot.js
var bootJS string

// maxBootText is the most text the boot snapshot carries: a theme's file,
// or a tab moved to the window.
const maxBootText = 2 * 1024 * 1024

// bootScript is the script every window runs before the page's own: the
// settings snapshot the frontend reads at once (window.__BOOT__), and the
// window's first background colour.
func (a *App) bootScript(label string) (script, background string) {
	if a.stores == nil {
		return "", ""
	}
	settings, rev := a.stores.settings.snapshot()
	uiState, _ := a.stores.uiState.snapshot()
	var settingsObj, uiObj jsonObject
	_ = json.Unmarshal(settings, &settingsObj)
	_ = json.Unmarshal(uiState, &uiObj)
	themeCSS := a.userThemeCSS(settingsObj)
	var bg any
	if color, ok := windowBackground(settingsObj, uiObj, themeCSS, a.shared.ContentThemes); ok {
		bg = color
		background = color
	} else {
		// Following the system: the window's colour follows it too.
		background = "light-dark(#ffffff, #1c1c1e)"
	}
	boot := map[string]any{
		"settings":    settings,
		"rev":         rev,
		"uiState":     uiState,
		"newProfile":  a.stores.settings.missingAtLoad,
		"label":       label,
		"windows10":   false,
		"restoring":   a.windowRestores(label),
		"detachedTab": a.bootDetachedTab(label),
		"background":  bg,
		"tables":      jsontext.Value(a.shared.AppearanceTables),
	}
	raw, err := json.Marshal(boot)
	if err != nil {
		return "", background
	}
	css := jsontext.Value("null")
	if themeCSS != nil {
		css = mustJSON(themeCSS)
	}
	return fmt.Sprintf("%s\n;window.__textusBoot(%s, %s);", bootJS, raw, css), background
}

// themeCSS is a user theme's files.
type themeCSS struct {
	Light string  `json:"light"`
	Dark  *string `json:"dark"`
}

// userThemeCSS is the CSS of the user theme the settings choose, nil for a
// built-in one.
func (a *App) userThemeCSS(settings jsonObject) *themeCSS {
	var id string
	if json.Unmarshal(settings["themeId"], &id) != nil || !safeThemeID(id) {
		return nil
	}
	var themes []struct {
		ID      string `json:"id"`
		HasDark bool   `json:"hasDark"`
	}
	_ = json.Unmarshal(settings["userThemes"], &themes)
	hasDark, found := false, false
	for _, t := range themes {
		if t.ID == id {
			hasDark, found = t.HasDark, true
			break
		}
	}
	if !found {
		return nil
	}
	dir := filepath.Join(a.dirs.Config, "themes", id)
	read := func(name string) *string {
		path := filepath.Join(dir, name)
		info, err := os.Stat(path)
		if err != nil || info.Size() > maxBootText {
			return nil
		}
		text, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		s := string(text)
		return &s
	}
	light := read("light.css")
	if light == nil {
		return nil
	}
	out := &themeCSS{Light: *light}
	if hasDark {
		out.Dark = read("dark.css")
	}
	return out
}

func safeThemeID(id string) bool {
	if id == "" {
		return false
	}
	for _, c := range id {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

// windowBackground is the page colour of the theme the settings choose, as
// "#rrggbb", for the window's first paint. False where it follows the
// system, whose choice is not known here.
func windowBackground(settings, uiState jsonObject, css *themeCSS, contentThemes string) (string, bool) {
	var theme string
	if raw, ok := settings["theme"]; ok {
		if json.Unmarshal(raw, &theme) != nil {
			return "", false
		}
	}
	var dark bool
	switch theme {
	case "dark":
		dark = true
	case "light":
	default:
		return "", false
	}
	var id string
	_ = json.Unmarshal(settings["themeId"], &id)
	if c, ok := builtinBackground(contentThemes, id, dark); ok {
		return c, true
	}
	if c, ok := importedBackground(id, uiState, css, dark); ok {
		return c, true
	}
	if dark {
		return "#1c1c1e", true
	}
	return "#ffffff", true
}

func builtinBackground(contentThemes, theme string, dark bool) (string, bool) {
	if theme == "" {
		return "", false
	}
	for _, c := range theme {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9') {
			return "", false
		}
	}
	rule := "\n[data-content-theme=\"" + theme + "\"] {"
	if dark {
		rule = ":root[data-theme=\"dark\"][data-content-theme=\"" + theme + "\"] {"
	}
	value, ok := declared(contentThemes, rule, "--doc-bg:")
	if !ok {
		return "", false
	}
	return cssColor(value)
}

func importedBackground(theme string, uiState jsonObject, css *themeCSS, dark bool) (string, bool) {
	if css == nil {
		return "", false
	}
	source := css.Light
	if dark && css.Dark != nil {
		source = *css.Dark
	}
	var chrome struct {
		ID           string  `json:"id"`
		Dark         bool    `json:"dark"`
		SourceLength float64 `json:"sourceLength"`
		CSS          string  `json:"css"`
	}
	if json.Unmarshal(uiState["themeChrome"], &chrome) != nil {
		return "", false
	}
	// A JavaScript string's length, as the frontend measured it.
	length := len(utf16.Encode([]rune(source)))
	if chrome.ID != theme || chrome.Dark != dark || int(chrome.SourceLength) != length {
		return "", false
	}
	value, ok := declared(chrome.CSS, ":root {", "--ui-bg:")
	if !ok {
		return "", false
	}
	return cssColor(strings.TrimSpace(strings.TrimSuffix(value, "!important")))
}

// declared is the value of property in the first rule opening with rule.
func declared(css, rule, property string) (string, bool) {
	i := strings.Index(css, rule)
	if i < 0 {
		return "", false
	}
	body := css[i+len(rule):]
	end := strings.Index(body, "}")
	if end < 0 {
		return "", false
	}
	body = body[:end]
	j := strings.Index(body, property)
	if j < 0 {
		return "", false
	}
	value, _, _ := strings.Cut(body[j+len(property):], ";")
	return strings.TrimSpace(value), true
}

// cssColor reads "#rgb", "#rrggbb" or "rgb(r, g, b)" as "#rrggbb".
func cssColor(value string) (string, bool) {
	var rgb [3]uint64
	if hex, ok := strings.CutPrefix(value, "#"); ok {
		switch len(hex) {
		case 3:
			hex = string([]byte{hex[0], hex[0], hex[1], hex[1], hex[2], hex[2]})
		case 6:
		default:
			return "", false
		}
		for i := range 3 {
			n, err := strconv.ParseUint(hex[i*2:i*2+2], 16, 8)
			if err != nil {
				return "", false
			}
			rgb[i] = n
		}
	} else {
		inner, ok := strings.CutPrefix(value, "rgb(")
		if !ok {
			return "", false
		}
		inner, ok = strings.CutSuffix(inner, ")")
		if !ok {
			return "", false
		}
		parts := strings.Split(inner, ",")
		if len(parts) != 3 {
			return "", false
		}
		for i, p := range parts {
			n, err := strconv.ParseUint(strings.TrimSpace(p), 10, 8)
			if err != nil {
				return "", false
			}
			rgb[i] = n
		}
	}
	return fmt.Sprintf("#%02x%02x%02x", rgb[0], rgb[1], rgb[2]), true
}

// bootDetachedTab is the tab moved to the window, unless more text than
// the boot carries: take_detached_tab gives it then.
func (a *App) bootDetachedTab(label string) jsontext.Value {
	a.mu.Lock()
	tab, ok := a.detachedTabs[label]
	a.mu.Unlock()
	if !ok || len(tab) > maxBootText {
		return jsontext.Value("null")
	}
	return tab
}
