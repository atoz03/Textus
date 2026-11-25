package plugins

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Small rules of the plugin commands: textus:// links, the files their
// frames load, an Obsidian vault's settings, a read's version, a
// developer folder's last change, and the skills that moved to the market.

// URIScheme is the scheme Textus answers to: textus://<plugin id>/<path>.
const URIScheme = "textus"

// IsTextusURI is whether a launch argument is one of these links, not a
// file to open.
func IsTextusURI(arg string) bool {
	prefix := URIScheme + "://"
	return len(arg) >= len(prefix) && strings.EqualFold(arg[:len(prefix)], prefix)
}

// NotificationID is the id of `textus://notification/<id>`, a
// notification's click rather than a plugin's link; false for any other.
func NotificationID(u string) (string, bool) {
	if !IsTextusURI(u) {
		return "", false
	}
	id, ok := strings.CutPrefix(u[len(URIScheme)+3:], "notification/")
	if !ok || id == "" {
		return "", false
	}
	for _, c := range id {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
			return "", false
		}
	}
	return id, true
}

// Mime is the type a plugin's file is served as.
func Mime(rel string) string {
	ext := ""
	if i := strings.LastIndexByte(rel, '.'); i >= 0 {
		ext = strings.ToLower(rel[i+1:])
	}
	switch ext {
	case "js", "mjs":
		return "text/javascript"
	case "css":
		return "text/css"
	case "html":
		return "text/html"
	case "json":
		return "application/json"
	case "svg":
		return "image/svg+xml"
	case "png":
		return "image/png"
	case "jpg", "jpeg":
		return "image/jpeg"
	case "gif":
		return "image/gif"
	case "webp":
		return "image/webp"
	case "woff":
		return "font/woff"
	case "woff2":
		return "font/woff2"
	case "ttf":
		return "font/ttf"
	case "wasm":
		return "application/wasm"
	}
	return "application/octet-stream"
}

// sharedPluginSettings are Obsidian plugins whose settings hold folders
// and templates, never a token, so a plugin doing the same job may follow
// them.
var sharedPluginSettings = []string{"obsidian-zotero-desktop-connector", "obsidian-citation-plugin"}

// SettingsFileName is whether name is a file at the top of an Obsidian
// vault's settings folder (no folders, nothing hidden), or the data.json
// of a plugin whose settings are shared.
func SettingsFileName(name string) bool {
	if rest, ok := strings.CutPrefix(name, "plugins/"); ok {
		if id, ok := strings.CutSuffix(rest, "/data.json"); ok {
			return slices.Contains(sharedPluginSettings, id)
		}
	}
	if name == "" || strings.HasPrefix(name, ".") {
		return false
	}
	for _, c := range name {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-' || c == '.') {
			return false
		}
	}
	return true
}

// Token names a version of a file's bytes: what a read for a change hands
// over and its write gives back.
func Token(data []byte) string { return HexSHA256(data) }

// NewestChange is when a developer's plugin folder last changed: its
// newest file, in ms, leaving out what is no part of a plugin and its
// build's output (a rebuild is no change of the source). Nil for none.
func NewestChange(dir string, depth int) *float64 {
	if depth > 6 {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var newest *float64
	for _, e := range entries {
		if NotPluginFile(e.Name()) || e.Name() == "dist" {
			continue
		}
		// A link is the link's own change, never followed.
		info, err := e.Info()
		if err != nil {
			continue
		}
		var here *float64
		if info.IsDir() {
			here = NewestChange(filepath.Join(dir, e.Name()), depth+1)
		} else {
			here = ModifiedMs(info)
		}
		if here != nil && (newest == nil || *here > *newest) {
			newest = here
		}
	}
	return newest
}

// firstMoved is how many skills moved in the first move, which a profile
// marked `true` (before the mark was a list) has seen to.
const firstMoved = 12

// MovedSkillsSeenTo are the moved skills a profile has seen to, by what
// its skillsMovedToMarket setting holds: true, or a list of names.
func MovedSkillsSeenTo(moved []string, mark any) []string {
	switch m := mark.(type) {
	case bool:
		if m {
			return slices.Clone(moved[:min(firstMoved, len(moved))])
		}
	case []any:
		var out []string
		for _, name := range moved {
			if slices.Contains(m, any(name)) {
				out = append(out, name)
			}
		}
		return out
	}
	return nil
}

// MovedSkillsHadOn are the moved skills a profile had on, given its list
// of turned-off built-ins: all of them without a list, else those not in
// it.
func MovedSkillsHadOn(moved []string, disabled any) []string {
	off, _ := disabled.([]any)
	var out []string
	for _, name := range moved {
		if !slices.Contains(off, any(name)) {
			out = append(out, name)
		}
	}
	return out
}
