package plugins

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"slices"
	"strings"
	"sync"
)

// The checks on textus-plugin.json that decide what a plugin may ask for:
// its id and version, who made it, the permissions it names, where it
// runs. Every way a plugin is installed or loaded passes through here, so
// a window that skipped its own check still can't install a manifest
// asking for a permission Textus doesn't have.
//
// Mirrors parseManifest in src/plugins/store/manifest.ts; change both.
// What a plugin adds to the interface (contributes) is left to the window,
// which checks it before drawing any of it.

// lists are the permissions without a host (network:<host> is checked
// apart) and the platforms: one list with the frontend's
// (src/plugins/store/permissions.json).
var lists struct {
	once      sync.Once
	Plain     []string `json:"plain"`
	Platforms []string `json:"platforms"`
}

// LoadPermissions reads src/plugins/store/permissions.json, which the main
// package embeds: before any manifest is checked.
func LoadPermissions(raw []byte) error {
	var err error
	lists.once.Do(func() { err = json.Unmarshal(raw, &lists) })
	return err
}

var requiredSettings = []string{"runInBackground"}

// IsPermission is whether value is a permission a plugin may name: one of
// the plain ones, `commands:` a plugin, `hidden:` a hidden file to read, or
// `network:` and `*`, a host, `*.` a domain, each maybe with `:port`.
func IsPermission(value string) bool {
	if slices.Contains(lists.Plain, value) {
		return true
	}
	if plugin, ok := strings.CutPrefix(value, "commands:"); ok {
		return RequireID(plugin) == nil
	}
	if path, ok := strings.CutPrefix(value, "hidden:"); ok {
		return HiddenFilePath(path)
	}
	host, ok := strings.CutPrefix(value, "network:")
	if !ok {
		return false
	}
	if host == "*" {
		return true
	}
	host = strings.TrimPrefix(host, "*.")
	name, port, hasPort := strings.Cut(host, ":")
	if name == "" {
		return false
	}
	for _, c := range name {
		if !(isASCIIAlnum(c) || c == '.' || c == '-') {
			return false
		}
	}
	if !hasPort {
		return true
	}
	return len(port) >= 1 && len(port) <= 5 && allDigits(port)
}

func isASCIIAlnum(c rune) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}

func allDigits(s string) bool {
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// isVersion: `1.0.0`, maybe with a pre-release: `1.0.0-beta.1`.
func isVersion(value string) bool {
	core, pre, hasPre := strings.Cut(value, "-")
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return false
	}
	for _, p := range parts {
		if p == "" || !allDigits(p) {
			return false
		}
	}
	if !hasPre {
		return true
	}
	if pre == "" {
		return false
	}
	for _, c := range pre {
		if !(isASCIIAlnum(c) || c == '.' || c == '-') {
			return false
		}
	}
	return true
}

type object = map[string]jsontext.Value

func asObject(v jsontext.Value, what string) (object, error) {
	var o object
	if v.Kind() != '{' || json.Unmarshal(v, &o) != nil {
		return nil, fmt.Errorf("%s must be an object.", what)
	}
	return o, nil
}

// asList is a list, or none at all.
func asList(v jsontext.Value, what string) ([]jsontext.Value, error) {
	if v == nil {
		return nil, nil
	}
	var items []jsontext.Value
	if v.Kind() != '[' || json.Unmarshal(v, &items) != nil {
		return nil, fmt.Errorf("%s must be a list.", what)
	}
	return items, nil
}

// asText is a string with something in it.
func asText(v jsontext.Value, what string) (string, error) {
	var s string
	if v == nil || v.Kind() != '"' || json.Unmarshal(v, &s) != nil || strings.TrimSpace(s) == "" {
		return "", fmt.Errorf("%s must be a string.", what)
	}
	return s, nil
}

// optionalText is asText, but it may be left out.
func optionalText(v jsontext.Value, what string) (string, bool, error) {
	if v == nil {
		return "", false, nil
	}
	s, err := asText(v, what)
	return s, err == nil, err
}

// stringOf is v as a string, false for anything else.
func stringOf(v jsontext.Value) (string, bool) {
	var s string
	if v.Kind() != '"' || json.Unmarshal(v, &s) != nil {
		return "", false
	}
	return s, true
}

// DeclaredPermissions are the permissions manifest names that Textus has;
// none when it can't be read.
func DeclaredPermissions(manifest string) []string {
	var m struct {
		Permissions []jsontext.Value `json:"permissions"`
	}
	if json.Unmarshal([]byte(manifest), &m) != nil {
		return nil
	}
	var out []string
	for _, p := range m.Permissions {
		if s, ok := stringOf(p); ok && IsPermission(s) {
			out = append(out, s)
		}
	}
	return out
}

// Check checks manifest (as written) in full; the error says what is
// wrong, in English, for the plugin's author.
func Check(manifest string) error {
	var top jsontext.Value
	if err := json.Unmarshal([]byte(manifest), &top); err != nil {
		return fmt.Errorf("textus-plugin.json is not JSON: %v", err)
	}
	m, err := asObject(top, "textus-plugin.json")
	if err != nil {
		return err
	}
	id, err := asText(m["id"], "id")
	if err != nil {
		return err
	}
	if RequireID(id) != nil {
		return fmt.Errorf("id \"%s\" must be publisher.name, lowercase.", id)
	}
	version, err := asText(m["version"], "version")
	if err != nil {
		return err
	}
	if !isVersion(version) {
		return fmt.Errorf("version \"%s\" must be like 1.0.0.", version)
	}
	for _, field := range []string{"name", "publisher"} {
		if _, err := asText(m[field], field); err != nil {
			return err
		}
	}
	for _, field := range []string{"author", "description", "icon", "license", "repository"} {
		if _, _, err := optionalText(m[field], field); err != nil {
			return err
		}
	}
	engines := object{}
	if m["engines"] != nil {
		if engines, err = asObject(m["engines"], "engines"); err != nil {
			return err
		}
	}
	if _, err := asText(engines["textus"], "engines.textus"); err != nil {
		return err
	}
	permissions, err := asList(m["permissions"], "permissions")
	if err != nil {
		return err
	}
	for _, p := range permissions {
		if s, ok := stringOf(p); !ok || !IsPermission(s) {
			return fmt.Errorf("No permission %s.", compact(p))
		}
	}
	if err := checkPlacement(m); err != nil {
		return err
	}
	if err := checkMain(m["main"]); err != nil {
		return err
	}
	// Where the store lists it; one this Textus doesn't know reads as none.
	if _, _, err := optionalText(m["category"], "category"); err != nil {
		return err
	}
	if v := m["needsTextusAi"]; v != nil && v.Kind() != 't' && v.Kind() != 'f' {
		return fmt.Errorf("needsTextusAi is true or false.")
	}
	events, err := asList(m["activationEvents"], "activationEvents")
	if err != nil {
		return err
	}
	for i, e := range events {
		if _, err := asText(e, fmt.Sprintf("activationEvents[%d]", i)); err != nil {
			return err
		}
	}
	if l10n := m["l10n"]; l10n != nil {
		tables, err := asObject(l10n, "l10n")
		if err != nil {
			return err
		}
		for lang, table := range tables {
			if _, err := asObject(table, "l10n."+lang); err != nil {
				return err
			}
		}
	}
	if c := m["contributes"]; c != nil {
		contributes, err := asObject(c, "contributes")
		if err != nil {
			return err
		}
		var embeds []jsontext.Value
		_ = json.Unmarshal(contributes["embeds"], &embeds)
		reads := false
		for _, p := range permissions {
			if s, _ := stringOf(p); s == "files.read" || s == "files.write" {
				reads = true
			}
		}
		if len(embeds) > 0 && !reads {
			return fmt.Errorf("contributes.embeds needs files.read.")
		}
	}
	return nil
}

// checkPlacement: background code runs on the desktop only; a plugin that
// only asks to keep Textus running (requires runInBackground) may name
// ios, where it runs without (src/plugins/store/manifest.ts, the same
// rule).
func checkPlacement(m object) error {
	_, background, err := optionalText(m["background"], "background")
	if err != nil {
		return err
	}
	requires, err := asList(m["requires"], "requires")
	if err != nil {
		return err
	}
	for i, r := range requires {
		need, err := asObject(r, fmt.Sprintf("requires[%d]", i))
		if err != nil {
			return err
		}
		setting, err := asText(need["setting"], fmt.Sprintf("requires[%d].setting", i))
		if err != nil {
			return err
		}
		if !slices.Contains(requiredSettings, setting) {
			return fmt.Errorf("requires[%d].setting \"%s\" is not one of %s.", i, setting, strings.Join(requiredSettings, ", "))
		}
		if _, _, err := optionalText(need["reason"], fmt.Sprintf("requires[%d].reason", i)); err != nil {
			return err
		}
	}
	if m["platforms"] == nil {
		return nil
	}
	platforms, err := asList(m["platforms"], "platforms")
	if err != nil {
		return err
	}
	ios := false
	for _, p := range platforms {
		s, ok := stringOf(p)
		if !ok || !slices.Contains(lists.Platforms, s) {
			return fmt.Errorf("No platform %s: desktop or ios.", compact(p))
		}
		ios = ios || s == "ios"
	}
	if background && ios {
		return fmt.Errorf("The background is the desktop's only: platforms can't name ios.")
	}
	return nil
}

// checkMain: one file, or several run in order.
func checkMain(main jsontext.Value) error {
	if main != nil && main.Kind() == '"' {
		_, err := asText(main, "main[0]")
		return err
	}
	files, err := asList(main, "main")
	if err != nil {
		return err
	}
	for i, f := range files {
		if _, err := asText(f, fmt.Sprintf("main[%d]", i)); err != nil {
			return err
		}
	}
	return nil
}

// compact is a JSON value as serde_json's Display writes it: no spaces.
func compact(v jsontext.Value) string {
	out := v.Clone()
	if err := out.Compact(); err != nil {
		return string(v)
	}
	return string(out)
}
