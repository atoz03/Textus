package plugins

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"path/filepath"
	"strings"
	"unicode"
)

// Scripts: a plugin in one .js file in the global scripts/ folder, its
// manifest in a header comment:
//
//	// ==TextusScript==
//	// @name        Word Count
//	// @command     count Count Words
//	// ==/TextusScript==
//
// Its id is `local.<file name>` unless @id gives one.

const (
	scriptStart = "==TextusScript=="
	scriptEnd   = "==/TextusScript=="
)

type field struct{ key, value string }

// header is the `// @key value` lines between the markers, in order.
func header(text string) []field {
	var out []field
	inside := false
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSuffix(line, "\r")
		rest, ok := strings.CutPrefix(strings.TrimSpace(line), "//")
		if !ok {
			if inside {
				break
			}
			continue
		}
		rest = strings.TrimSpace(rest)
		switch {
		case rest == scriptStart:
			inside = true
		case rest == scriptEnd:
			return out
		case inside:
			if f, ok := strings.CutPrefix(rest, "@"); ok {
				key, value := f, ""
				if i := strings.IndexFunc(f, unicode.IsSpace); i >= 0 {
					key, value = f[:i], strings.TrimSpace(f[i:])
				}
				out = append(out, field{key, value})
			}
		}
	}
	return out
}

// idPart is a file name as the part of an id after `local.`.
func idPart(stem string) string {
	var b strings.Builder
	for _, c := range strings.ToLower(stem) {
		if c >= 'a' && c <= 'z' || c >= '0' && c <= '9' {
			b.WriteRune(c)
		} else {
			b.WriteByte('-')
		}
	}
	part := b.String()
	for strings.Contains(part, "--") {
		part = strings.ReplaceAll(part, "--", "-")
	}
	return strings.Trim(part, "-")
}

// ScriptManifest is the manifest a script's header makes, and its id;
// false for a file with no header, or an id that can't be one.
func ScriptManifest(file, text string) (id, manifest string, ok bool) {
	fields := header(text)
	if len(fields) == 0 {
		return "", "", false
	}
	name := filepath.Base(file)
	stem := strings.TrimSuffix(name, filepath.Ext(name))
	one := func(key, fallback string) string {
		for _, f := range fields {
			if f.key == key {
				return f.value
			}
		}
		return fallback
	}
	all := func(key string) []string {
		out := []string{}
		for _, f := range fields {
			if f.key == key {
				out = append(out, f.value)
			}
		}
		return out
	}
	id = one("id", "local."+idPart(stem))
	if RequireID(id) != nil {
		return "", "", false
	}
	publisher, _, _ := strings.Cut(id, ".")
	type command struct {
		Command string `json:"command"`
		Title   string `json:"title"`
	}
	commands := []command{}
	for _, line := range all("command") {
		i := strings.IndexFunc(line, unicode.IsSpace)
		if i < 0 {
			continue
		}
		commands = append(commands, command{id + "." + line[:i], strings.TrimSpace(line[i:])})
	}
	// Keys in name order, as the manifest always came out.
	m := map[string]any{
		"id": id, "name": one("name", stem), "version": one("version", "0.1.0"), "publisher": publisher,
		"description": one("description", ""), "engines": map[string]any{"textus": "^1.3.0"}, "main": name,
		"permissions": all("permission"), "activationEvents": []string{"onStartupFinished"},
		"contributes": map[string]any{"commands": commands},
	}
	raw, err := json.Marshal(m, json.Deterministic(true), jsontext.WithIndent("  "))
	if err != nil {
		return "", "", false
	}
	return id, string(raw), true
}
