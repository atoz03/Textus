package backend

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/egoist/mygo"
)

// Themes the user imported or wrote, kept as CSS in the config folder.

func themeVariantFile(variant string) (string, error) {
	switch variant {
	case "light":
		return "light.css", nil
	case "dark":
		return "dark.css", nil
	}
	return "", errorf("invalid theme variant: %s", variant)
}

func (a *App) themesDir() string { return filepath.Join(a.dirs.Config, "themes") }

func init() {
	command("save_theme_css", func(c *Call, a struct {
		ID      string `json:"id"`
		Variant string `json:"variant"`
		CSS     string `json:"css"`
	}) (nothing, error) {
		if !safeThemeID(a.ID) {
			return nil, errors.New("invalid theme id")
		}
		name, err := themeVariantFile(a.Variant)
		if err != nil {
			return nil, err
		}
		return nil, writeFileAtomic(filepath.Join(c.app.themesDir(), a.ID, name), []byte(a.CSS))
	})
	command("load_theme_css", func(c *Call, a struct {
		ID      string `json:"id"`
		Variant string `json:"variant"`
	}) (*string, error) {
		if !safeThemeID(a.ID) {
			return nil, errors.New("invalid theme id")
		}
		name, err := themeVariantFile(a.Variant)
		if err != nil {
			return nil, err
		}
		raw, err := os.ReadFile(filepath.Join(c.app.themesDir(), a.ID, name))
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		text := string(raw)
		return &text, nil
	})
	command("delete_theme", func(c *Call, a struct {
		ID string `json:"id"`
	}) (nothing, error) {
		if !safeThemeID(a.ID) {
			return nil, errors.New("invalid theme id")
		}
		dir := filepath.Join(c.app.themesDir(), a.ID)
		if _, err := os.Stat(dir); errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		// A theme the user imported or wrote: to the Trash.
		return nil, mygo.Shell.TrashItem(dir)
	})
}
