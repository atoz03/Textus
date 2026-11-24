package backend

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/egoist/mygo"
)

type filter struct {
	name string
	exts []string
}

func (c *Call) parent() *mygo.Window {
	if c.window == nil {
		return nil
	}
	return c.window.win
}

// pickFile asks for one file; nil when the user cancels.
func pickFile(c *Call, filters []filter) (*string, error) {
	opts := mygo.OpenDialogOptions{Parent: c.parent()}
	for _, f := range filters {
		opts.Filters = append(opts.Filters, mygo.FileFilter{Name: f.name, Extensions: f.exts})
	}
	paths, err := mygo.Dialog.Open(opts)
	if err != nil || len(paths) == 0 {
		return nil, err
	}
	return &paths[0], nil
}

// pickFolder asks for one folder; nil when the user cancels.
func pickFolder(c *Call) (*string, error) {
	paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: c.parent(), Directory: true, CreateDirectories: true})
	if err != nil || len(paths) == 0 {
		return nil, err
	}
	return &paths[0], nil
}

// saveFile asks where to save, named name in dir; the path gets ext when the
// user typed none.
func saveFile(c *Call, name string, dir *string, f filter) (*string, error) {
	opts := mygo.SaveDialogOptions{
		Parent:            c.parent(),
		DefaultPath:       name,
		CreateDirectories: true,
		Filters:           []mygo.FileFilter{{Name: f.name, Extensions: f.exts}},
	}
	if dir != nil {
		if info, err := os.Stat(*dir); err == nil && info.IsDir() {
			opts.DefaultPath = filepath.Join(*dir, name)
		}
	}
	path, err := mygo.Dialog.Save(opts)
	if err != nil || path == "" {
		return nil, err
	}
	if len(f.exts) > 0 {
		path = withDefaultExtension(path, f.exts[0])
	}
	return &path, nil
}

func withDefaultExtension(path, ext string) string {
	if filepath.Ext(path) != "" {
		return path
	}
	return path + "." + ext
}

func or(s *string, fallback string) string {
	if s == nil {
		return fallback
	}
	return *s
}

func init() {
	command("open_file_dialog", func(c *Call, a struct {
		TextFilter *string `json:"textFilter"`
		WordFilter *string `json:"wordFilter"`
		AllFilter  *string `json:"allFilter"`
	}) (*string, error) {
		var filters []filter
		// macOS lists every file its own way; elsewhere, the kinds Textus opens.
		if runtime.GOOS != "darwin" {
			filters = []filter{
				{"Markdown", []string{"md", "markdown", "mdx"}},
				{or(a.TextFilter, "Text files"), []string{"txt", "text"}},
				{or(a.WordFilter, "Word documents"), []string{"docx"}},
				{or(a.AllFilter, "All files"), []string{"*"}},
			}
		}
		return pickFile(c, filters)
	})
	command("open_css_file_dialog", func(c *Call, _ none) (*string, error) {
		return pickFile(c, []filter{{"CSS", []string{"css"}}})
	})
	command("open_settings_file_dialog", func(c *Call, _ none) (*string, error) {
		return pickFile(c, []filter{{"JSON", []string{"json"}}})
	})
	command("save_settings_file_dialog", func(c *Call, a struct {
		DefaultName string `json:"defaultName"`
	}) (*string, error) {
		return saveFile(c, a.DefaultName, nil, filter{"JSON", []string{"json"}})
	})
	command("open_image_file_dialog", func(c *Call, a struct {
		FilterName string   `json:"filterName"`
		Extensions []string `json:"extensions"`
	}) (*string, error) {
		return pickFile(c, []filter{{a.FilterName, a.Extensions}})
	})
	command("save_file_dialog", func(c *Call, a struct {
		DefaultName string  `json:"defaultName"`
		DefaultDir  *string `json:"defaultDir"`
	}) (*string, error) {
		name := strings.TrimSpace(a.DefaultName)
		if name == "" {
			name = "Untitled.md"
		}
		return saveFile(c, name, a.DefaultDir, filter{"Markdown", []string{"md"}})
	})
	command("export_save_dialog", func(c *Call, a struct {
		DefaultName string `json:"defaultName"`
		FilterName  string `json:"filterName"`
		Ext         string `json:"ext"`
	}) (*string, error) {
		return saveFile(c, a.DefaultName, nil, filter{a.FilterName, []string{a.Ext}})
	})
	command("pick_open_folder", func(c *Call, _ none) (*string, error) {
		picked, err := pickFolder(c)
		if err != nil || picked == nil {
			return nil, err
		}
		if isTooBroad(*picked) {
			return nil, codeError("tooBroad")
		}
		return picked, nil
	})
}
