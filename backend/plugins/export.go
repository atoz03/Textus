package plugins

import (
	_ "embed"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Export for publishing: a plugin you made, copied into a folder ready to
// be a GitHub repository, its id under your name (`local.word-count` ->
// `jane.word-count`), with a README, MIT LICENSE, .gitignore and release
// workflow.

var (
	//go:embed template/README.md
	readmeTemplate string
	//go:embed template/LICENSE
	licenseTemplate string
	//go:embed template/gitignore
	gitignoreTemplate string
	//go:embed template/release.yml
	releaseTemplate string
)

// validPublisher: a publisher's name, as in a plugin id - lowercase
// letters, digits and single dashes.
func validPublisher(name string) bool {
	if name == "" {
		return false
	}
	for _, part := range strings.Split(name, "-") {
		if part == "" {
			return false
		}
		for _, c := range part {
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9') {
				return false
			}
		}
	}
	return true
}

// copyFiles copies every file of the plugin under from to to.
func copyFiles(from, to string) error {
	if err := os.MkdirAll(to, 0o755); err != nil {
		return err
	}
	entries, err := os.ReadDir(from)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if NotPluginFile(e.Name()) {
			continue
		}
		source, target := filepath.Join(from, e.Name()), filepath.Join(to, e.Name())
		switch {
		case e.IsDir():
			if err := copyFiles(source, target); err != nil {
				return err
			}
		case e.Type().IsRegular():
			if err := copyFile(source, target); err != nil {
				return err
			}
		}
	}
	return nil
}

// renameID renames the plugin's own ids (its commands, views, settings),
// which all start with its id, in every text file: Textus refuses a
// manifest whose ones don't.
func renameID(dir, from, to string) error {
	text := []string{"json", "js", "mjs", "html", "css", "md", "txt"}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		path := filepath.Join(dir, e.Name())
		if info, err := os.Stat(path); err == nil && info.IsDir() {
			if err := renameID(path, from, to); err != nil {
				return err
			}
			continue
		}
		if !slices.Contains(text, strings.TrimPrefix(filepath.Ext(path), ".")) {
			continue
		}
		raw, err := os.ReadFile(path)
		if err != nil || !strings.Contains(string(raw), from) {
			continue
		}
		if err := os.WriteFile(path, []byte(strings.ReplaceAll(string(raw), from, to)), 0o644); err != nil {
			return err
		}
	}
	return nil
}

// writeNew writes text at path unless the plugin brought its own.
func writeNew(path, text string) error {
	if Exists(path) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(text), 0o644)
}

// ExportTo lays out the plugin at source for publishing in folder as
// publisher's, and answers the new folder's path.
func ExportTo(source, publisher, folder string) (string, error) {
	if !validPublisher(publisher) {
		return "", fmt.Errorf("\"%s\" can't be a publisher's name: use lowercase letters, digits and dashes.", publisher)
	}
	raw, err := os.ReadFile(filepath.Join(source, Manifest))
	if err != nil {
		return "", fmt.Errorf("The plugin has no textus-plugin.json: %v", err)
	}
	var manifest map[string]any
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return "", err
	}
	id, _ := manifest["id"].(string)
	_, name, ok := strings.Cut(id, ".")
	if !ok {
		return "", fmt.Errorf("%s is not a plugin id.", id)
	}
	newID := publisher + "." + name
	target := filepath.Join(folder, newID)
	if Exists(target) {
		return "", fmt.Errorf("%s is already there.", target)
	}
	if err := copyFiles(source, target); err != nil {
		return "", err
	}
	if newID != id {
		if err := renameID(target, id, newID); err != nil {
			return "", err
		}
		renamed, err := os.ReadFile(filepath.Join(target, Manifest))
		if err != nil {
			return "", err
		}
		if err := json.Unmarshal(renamed, &manifest); err != nil {
			return "", err
		}
	}
	manifest["id"] = newID
	manifest["publisher"] = publisher
	pretty, err := json.Marshal(manifest, json.Deterministic(true), jsontext.WithIndent("  "))
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(target, Manifest), append(pretty, '\n'), 0o644); err != nil {
		return "", err
	}
	// A "%key%" value is a string of its l10n: English, else any language.
	field := func(key string) string {
		value, _ := manifest[key].(string)
		k, ok := strings.CutPrefix(value, "%")
		if k, ok2 := strings.CutSuffix(k, "%"); ok && ok2 {
			tables, _ := manifest["l10n"].(map[string]any)
			english, _ := tables["en"].(map[string]any)
			if s, ok := english[k].(string); ok {
				return s
			}
			langs := make([]string, 0, len(tables))
			for lang := range tables {
				langs = append(langs, lang)
			}
			slices.Sort(langs)
			for _, lang := range langs {
				if table, ok := tables[lang].(map[string]any); ok {
					if s, ok := table[k].(string); ok {
						return s
					}
				}
			}
		}
		return value
	}
	readme := strings.NewReplacer("{name}", field("name"), "{description}", field("description"), "{id}", newID).Replace(readmeTemplate)
	license := strings.NewReplacer("{year}", strconv.Itoa(time.Now().Year()), "{publisher}", publisher).Replace(licenseTemplate)
	for _, f := range [][2]string{
		{"README.md", readme}, {"LICENSE", license}, {".gitignore", gitignoreTemplate},
		{filepath.Join(".github", "workflows", "release.yml"), releaseTemplate},
	} {
		if err := writeNew(filepath.Join(target, f[0]), f[1]); err != nil {
			return "", err
		}
	}
	return target, nil
}
