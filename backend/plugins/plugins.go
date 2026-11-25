// Package plugins is what the backend checks for plugins, apart from the
// app: their ids and manifests, which are on and what each was granted,
// the paths, hosts and storage a plugin may reach, the catalog and its
// versions, and what a folder's documents hold for them. Each check is
// made again here, so a window bug can't grant more than the user did.
// The commands that use it are the backend's (backend/plugin_*.go).
package plugins

import (
	"errors"
	"fmt"
	"strings"
)

// Manifest is the manifest's file name in a plugin's folder.
const Manifest = "textus-plugin.json"

// Coded is the error the frontend knows by its code (src/i18n/errors.json):
// the backend sets it to its own, which spells the English out.
var Coded = func(code string) error { return errors.New(code) }

// RequireID checks that id is `publisher.name`, lowercase letters, digits
// and dashes: safe as a folder name on every system, never `..` or a
// separator.
func RequireID(id string) error {
	valid := func(part string) bool {
		if part == "" || len(part) > 64 || strings.HasPrefix(part, "-") {
			return false
		}
		for _, c := range part {
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
				return false
			}
		}
		return true
	}
	publisher, name, ok := strings.Cut(id, ".")
	if !ok || !valid(publisher) || !valid(name) {
		return fmt.Errorf("\"%s\" is not a plugin id (publisher.name).", id)
	}
	return nil
}

// Refused is the error of a call plugin id makes without permission.
func Refused(id, permission string) error {
	return fmt.Errorf("%s was not granted %s.", id, permission)
}

// TurnedOff is the error of a call a plugin that is off makes.
func TurnedOff(id string) error { return fmt.Errorf("%s is turned off.", id) }
