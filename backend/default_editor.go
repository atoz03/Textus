package backend

import (
	"path/filepath"
	"strings"
)

// Textus made the app that opens .md files, as far as each system allows:
// Windows only lets the user choose, so Textus registers itself and opens
// Default apps; macOS sets LaunchServices; Linux runs xdg-mime (not from an
// AppImage). Each system's part is default_editor_<os>.go.

// defaultEditorStatus is what Settings shows for the row. Mirrors
// DefaultEditorStatus in src/platform/bindings/app.ts.
type defaultEditorStatus struct {
	// Supported is false where this installation can't be made the
	// default (an AppImage).
	Supported bool `json:"supported"`
	IsDefault bool `json:"isDefault"`
	// ChooseInSettings: the user makes the choice in the system's own
	// settings, which set_default_editor opens (Windows); elsewhere it is
	// made outright.
	ChooseInSettings bool `json:"chooseInSettings"`
}

// The registry values that list Textus for Markdown files, all under
// HKEY_CURRENT_USER, built without touching the registry.
const (
	// progID is Textus's own ProgID: not "Markdown", which other editors'
	// installers also write. Keep in step with mygo.json's file
	// associations.
	progID = "Textus.Markdown"
	// registeredApp is the name Default apps knows Textus by.
	registeredApp       = "Textus"
	appCapabilitiesKey  = `Software\Textus\Capabilities`
	registeredAppsKey   = `Software\RegisteredApplications`
	defaultAppsSettings = "ms-settings:defaultapps?registeredAppUser=" + registeredApp
)

var markdownExtensions = []string{".md", ".markdown"}

// registryValue is a value to write: text (REG_SZ), or with empty set an
// entry whose name is the whole point (REG_NONE, no bytes). Name "" is the
// key's default value.
type registryValue struct {
	key, name, text string
	empty           bool
}

// windowsRegistryValues are every value, for Textus installed at exe. The
// extensions' own default values are left to the installer.
func windowsRegistryValues(exe string) []registryValue {
	exeName := exe[strings.LastIndexByte(exe, '\\')+1:]
	dir := ""
	if i := strings.LastIndexByte(exe, '\\'); i >= 0 {
		dir = exe[:i]
	}
	open := `"` + exe + `" "%1"`
	prog := `Software\Classes\` + progID
	app := `Software\Classes\Applications\` + exeName
	text := func(key, name, text string) registryValue { return registryValue{key: key, name: name, text: text} }
	values := []registryValue{
		text(prog, "", "Markdown document"),
		// A document's own icon, installed beside the app.
		text(prog+`\DefaultIcon`, "", `"`+dir+`\doc.ico"`),
		text(prog+`\shell\open\command`, "", open),
		text(app, "FriendlyAppName", "Textus"),
		text(app+`\shell\open\command`, "", open),
		text(appCapabilitiesKey, "ApplicationName", "Textus"),
		text(appCapabilitiesKey, "ApplicationDescription", "Integrated Writing Environment"),
		text(registeredAppsKey, registeredApp, appCapabilitiesKey),
	}
	for _, ext := range markdownExtensions {
		// What keeps Textus under "Open with" whoever holds the default.
		values = append(values,
			registryValue{key: `Software\Classes\` + ext + `\OpenWithProgids`, name: progID, empty: true},
			text(app+`\SupportedTypes`, ext, ""),
			text(appCapabilitiesKey+`\FileAssociations`, ext, progID))
	}
	return values
}

// markdownUTI is Markdown's type as macOS and most editors declare it.
const markdownUTI = "net.daringfireball.markdown"

// contentTypes are the types to take over on macOS: what .md and .markdown
// resolve to here (a dyn. type when nothing declares Markdown), then
// Markdown's own, each once; the first sets the status.
func contentTypes(resolved []string) []string {
	var types []string
	for _, uti := range append(resolved, markdownUTI) {
		seen := false
		for _, t := range types {
			seen = seen || strings.EqualFold(t, uti)
		}
		if !seen {
			types = append(types, uti)
		}
	}
	return types
}

// desktopIDs are the desktop entries Textus is installed under on Linux:
// the packages name it after the product, the AUR package after the
// binary.
var desktopIDs = []string{"Textus.desktop", "textus.desktop"}

// markdownMIMETypes are the MIME types the desktop entry claims.
var markdownMIMETypes = []string{"text/markdown", "text/x-markdown"}

// installedDesktopID is the first of desktopIDs found in one of the
// applications folders, in their search order.
func installedDesktopID(applications []string, exists func(string) bool) (string, bool) {
	for _, dir := range applications {
		for _, id := range desktopIDs {
			if exists(filepath.Join(dir, id)) {
				return id, true
			}
		}
	}
	return "", false
}

// xdgMimeDefaultArgs are xdg-mime's arguments to make desktopID the default
// for Markdown.
func xdgMimeDefaultArgs(desktopID string) []string {
	return append([]string{"default", desktopID}, markdownMIMETypes...)
}

func init() {
	command("default_editor_status", func(c *Call, _ none) (defaultEditorStatus, error) {
		return editorStatus(c.ctx), nil
	})
	command("set_default_editor", func(c *Call, _ none) (nothing, error) {
		return nil, makeDefaultEditor(c.ctx)
	})
}
