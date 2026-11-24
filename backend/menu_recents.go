package backend

import (
	"encoding/json/v2"
	"os"
	"strings"
)

// The Open Recent lists behind the menu, kept in the app store.

func (a *App) recentList(name string) []string {
	if a.stores == nil {
		return []string{}
	}
	raw, ok := a.stores.uiState.get(name)
	var list []string
	if !ok || json.Unmarshal(raw, &list) != nil {
		return []string{}
	}
	return list
}

func (a *App) writeRecentList(name string, list []string) {
	if list == nil {
		list = []string{}
	}
	a.stores.uiState.patch(jsonObject{name: mustJSON(list)})
}

func pushFront(list []string, path string, max int) []string {
	out := []string{path}
	for _, p := range list {
		if p != path {
			out = append(out, p)
		}
	}
	if len(out) > max {
		out = out[:max]
	}
	return out
}

func stillThere(list []string) []string {
	out := []string{}
	for _, p := range list {
		if _, err := os.Stat(p); err == nil {
			out = append(out, p)
		}
	}
	return out
}

func (a *App) clearRecents() {
	a.writeRecentList("recentFiles", nil)
	a.writeRecentList("recentFolders", nil)
	a.rebuildMenu()
}

func abbreviateHome(path string) string {
	home := os.Getenv("HOME")
	if home == "" {
		home = os.Getenv("USERPROFILE")
	}
	if home == "" {
		return path
	}
	home = strings.TrimRight(home, `/\`)
	rest, ok := strings.CutPrefix(path, home)
	if !ok || (rest != "" && rest[0] != '/' && rest[0] != '\\') {
		return path
	}
	return "~" + rest
}

func folderMenuText(path string) string {
	sep := "/"
	if strings.Contains(path, `\`) && !strings.Contains(path, "/") {
		sep = `\`
	}
	return strings.TrimRight(abbreviateHome(path), `/\`) + sep
}
