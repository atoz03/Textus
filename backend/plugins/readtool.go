package plugins

import (
	"encoding/json/v2"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"
)

// read_plugin: Create mode's look at the installed plugins (the list, a
// manifest and files, a file's text), so a plugin is changed from its own
// code. Read-only.

const (
	// ReadPluginToolName is the tool's name.
	ReadPluginToolName = "read_plugin"
	// ReadPluginDescription is what the model is told of it.
	ReadPluginDescription = "Read the plugins the user has installed, to change one from its own code. No `id`: the list. An `id`: its manifest and its files. An `id` and a `path` (as the list gives it): that file's text."
	// fileChars is the most of one file the model is shown, in characters.
	fileChars = 60_000
	// listedFiles is the most files a plugin's list shows.
	listedFiles = 200
)

// ReadPluginParameters is the tool's schema.
func ReadPluginParameters() map[string]any {
	text := func(what string) map[string]any { return map[string]any{"type": "string", "description": what} }
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id":   text("The plugin's id, e.g. catvinci.tasks."),
			"path": text("A file of the plugin, relative to its folder, e.g. host.js."),
		},
	}
}

// ReadPluginAnswer is the answer to one call over the plugins installed.
func ReadPluginAnswer(installed []Installed, arguments string) string {
	var args struct {
		ID   string `json:"id"`
		Path string `json:"path"`
	}
	_ = json.Unmarshal([]byte(arguments), &args)
	id, path := strings.TrimSpace(args.ID), strings.TrimSpace(args.Path)
	if id == "" {
		return listPlugins(installed)
	}
	i := slices.IndexFunc(installed, func(p Installed) bool { return p.ID == id })
	if i < 0 {
		return fmt.Sprintf("No plugin `%s` is installed.\n\n%s", id, listPlugins(installed))
	}
	plugin := installed[i]
	if path == "" {
		return fmt.Sprintf("Plugin `%s` (%s). Its manifest, textus-plugin.json:\n%s\n\nIts files:\n%s",
			id, describePlugin(plugin), strings.TrimSpace(plugin.Manifest), strings.Join(filesOf(plugin.Dir), "\n"))
	}
	text, err := readPluginFile(plugin.Dir, path)
	if err != nil {
		return fmt.Sprintf("%v Its files:\n%s", err, strings.Join(filesOf(plugin.Dir), "\n"))
	}
	return fmt.Sprintf("`%s`'s %s:\n%s", id, path, text)
}

// describePlugin is "name 1.2.0 by publisher, official": what the manifest
// says of it.
func describePlugin(p Installed) string {
	var m map[string]any
	_ = json.Unmarshal([]byte(p.Manifest), &m)
	field := func(k string) string {
		s, _ := m[k].(string)
		return s
	}
	publisher := field("publisher")
	whose := "by " + publisher + " - a change is a copy of the user's own, as the plugin skill says"
	if publisher == "local" {
		whose = "the user's own, made in Create mode - change it in place, keeping its id"
	}
	return fmt.Sprintf("%s %s, %s", field("name"), field("version"), whose)
}

func listPlugins(installed []Installed) string {
	if len(installed) == 0 {
		return "No plugin is installed."
	}
	lines := make([]string, len(installed))
	for i, p := range installed {
		lines[i] = fmt.Sprintf("- %s: %s", p.ID, describePlugin(p))
	}
	return "The plugins installed:\n" + strings.Join(lines, "\n")
}

// filesOf are a plugin's files, relative to dir, with sizes - not a
// checkout's packages.
func filesOf(dir string) []string {
	var out []string
	var walk func(at string)
	walk = func(at string) {
		entries, err := os.ReadDir(at)
		if err != nil {
			return
		}
		for _, e := range entries {
			if NotPluginFile(e.Name()) {
				continue
			}
			path := filepath.Join(at, e.Name())
			if info, err := os.Stat(path); err == nil && info.IsDir() {
				walk(path)
			} else if rel, ok := within(path, dir); ok {
				var size int64
				if info, err := e.Info(); err == nil {
					size = info.Size()
				}
				out = append(out, fmt.Sprintf("%s (%d bytes)", filepath.ToSlash(rel), size))
			}
		}
	}
	walk(dir)
	slices.Sort(out)
	if len(out) > listedFiles {
		more := len(out) - listedFiles
		out = append(out[:listedFiles], fmt.Sprintf("(and %d more)", more))
	}
	return out
}

// readPluginFile is a file's text, when rel is a text file inside dir.
func readPluginFile(dir, rel string) (string, error) {
	rel, _, _ = strings.Cut(rel, " (")
	notOurs := fmt.Errorf("`%s` isn't a file of this plugin.", rel)
	// As the listing leaves them out: a .env is no file of the plugin.
	if HiddenPart(rel) {
		return "", notOurs
	}
	root, err1 := filepath.EvalSymlinks(dir)
	file, err2 := filepath.EvalSymlinks(filepath.Join(dir, rel))
	if err1 != nil || err2 != nil {
		return "", notOurs
	}
	if _, ok := within(file, root); !ok || !isFile(file) {
		return "", notOurs
	}
	raw, err := os.ReadFile(file)
	if err != nil {
		return "", err
	}
	if !utf8.Valid(raw) {
		return "", fmt.Errorf("`%s` isn't text.", rel)
	}
	text := string(raw)
	n := 0
	for i := range text {
		if n == fileChars {
			return fmt.Sprintf("%s\n… (cut at %d characters)", text[:i], fileChars), nil
		}
		n++
	}
	return text, nil
}
