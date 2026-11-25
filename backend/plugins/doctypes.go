package plugins

import (
	"encoding/json/v2"
	"slices"
	"strings"
	"sync"
)

// The file types plugins that are on open as documents
// (contributes.documentTypes): Markdown in another dress, so search, the
// AI's file list and the folder's metadata read them as Markdown too.

var documentTypes struct {
	sync.RWMutex
	list []string
}

// IsDocumentType is whether ext (lowercase, no dot) is a plugin's document
// type now.
func IsDocumentType(ext string) bool {
	if ext == "" {
		return false
	}
	documentTypes.RLock()
	defer documentTypes.RUnlock()
	return slices.Contains(documentTypes.list, ext)
}

// DocumentTypes are the plugins' document types now, in name order.
func DocumentTypes() []string {
	documentTypes.RLock()
	defer documentTypes.RUnlock()
	return slices.Clone(documentTypes.list)
}

// SetDocumentTypes keeps the document types of the plugins that are on,
// read from their manifests: at startup and after any plugin is turned on
// or off, installed or removed.
func SetDocumentTypes(manifests []string) {
	var found []string
	for _, m := range manifests {
		found = append(found, ExtensionsIn(m)...)
	}
	slices.Sort(found)
	found = slices.Compact(found)
	documentTypes.Lock()
	documentTypes.list = found
	documentTypes.Unlock()
}

// ExtensionsIn are the extensions a manifest's contributes.documentTypes
// names, lowercase and without their dot.
func ExtensionsIn(manifest string) []string {
	var m struct {
		Contributes struct {
			DocumentTypes []struct {
				Extensions []any `json:"extensions"`
			} `json:"documentTypes"`
		} `json:"contributes"`
	}
	if json.Unmarshal([]byte(manifest), &m) != nil {
		return nil
	}
	var out []string
	for _, t := range m.Contributes.DocumentTypes {
		for _, e := range t.Extensions {
			if s, ok := e.(string); ok {
				if ext := strings.ToLower(strings.TrimLeft(s, ".")); ext != "" {
					out = append(out, ext)
				}
			}
		}
	}
	return out
}
