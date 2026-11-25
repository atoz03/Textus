package backend

import (
	"os"
	"path/filepath"

	"github.com/egoist/mygo"

	"textus/backend/plugins"
)

// The plugin commands Settings › Plugins calls: the switches, installing,
// loading a developer folder, removing, the scripts folder, export for
// publishing, and a plugin's secrets.

// secretsFile is where plugins' secrets are kept, in the app's config
// folder, one owner-only file apart from settings and storage.
const secretsFile = "plugin_secrets.json"

func (a *App) secretsPath() string { return filepath.Join(a.dirs.Config, secretsFile) }

type pluginSecrets = map[string]map[string]string

// forgetPluginSecrets drops a plugin's secrets: uninstalled, or replaced
// by other code under its id.
func (a *App) forgetPluginSecrets(id string) error {
	a.plug.secretsMu.Lock()
	defer a.plug.secretsMu.Unlock()
	secrets, err := readJSONStore[pluginSecrets](a.secretsPath())
	if err != nil {
		return err
	}
	if _, ok := secrets[id]; !ok {
		return nil
	}
	delete(secrets, id)
	return writeSecretJSON(a.secretsPath(), secrets)
}

func init() {
	command("plugins_state", func(c *Call, _ none) (plugins.State, error) {
		return c.app.pluginState(), nil
	})
	// Settings › Plugins › Plugins, the switch for all of them.
	command("plugins_set_all", func(c *Call, a struct {
		On bool `json:"on"`
	}) (nothing, error) {
		return nil, c.app.updatePlugins(func(s *plugins.State) { s.AllOn = a.On })
	})
	// Turns a plugin on or off with the permissions the user granted it,
	// only what its manifest asks for, whoever calls. A developer-mode
	// folder is kept.
	command("plugin_set", func(c *Call, a struct {
		ID      string   `json:"id"`
		Enabled bool     `json:"enabled"`
		Granted []string `json:"granted"`
	}) (nothing, error) {
		if err := plugins.RequireID(a.ID); err != nil {
			return nil, err
		}
		var declared []string
		for _, p := range c.app.installedPlugins() {
			if p.ID == a.ID {
				declared = plugins.DeclaredPermissions(p.Manifest)
			}
		}
		return nil, c.app.updatePlugins(func(s *plugins.State) {
			e := s.Entry(a.ID)
			e.Enabled = a.Enabled
			e.Granted = plugins.OnlyDeclared(a.Granted, declared)
		})
	})
	command("plugins_installed", func(c *Call, _ none) ([]plugins.Installed, error) {
		return c.app.installedPlugins(), nil
	})
	command("plugin_install", func(c *Call, a struct {
		Path string `json:"path"`
	}) (plugins.Installed, error) {
		return c.app.installPlugin(a.Path, fromFile)
	})
	command("plugin_install_files", func(c *Call, a struct {
		Files []plugins.File `json:"files"`
	}) (plugins.Installed, error) {
		return c.app.installPluginFiles(a.Files)
	})
	command("plugin_load_dev", func(c *Call, a struct {
		Path string `json:"path"`
	}) (plugins.Installed, error) {
		return c.app.loadDevPlugin(a.Path)
	})
	command("plugin_uninstall", func(c *Call, a struct {
		ID string `json:"id"`
	}) (nothing, error) {
		return nil, c.app.uninstallPlugin(a.ID)
	})
	// The picker for Install from file… (.textusplugins or zips, several
	// at once) or, with folder, Load a plugin folder….
	command("plugin_pick_package", func(c *Call, a struct {
		Folder     bool   `json:"folder"`
		FilterName string `json:"filterName"`
	}) (*[]string, error) {
		opts := mygo.OpenDialogOptions{Parent: c.parent(), Directory: a.Folder}
		if !a.Folder {
			opts.Multiple = true
			opts.Filters = []mygo.FileFilter{{Name: a.FilterName, Extensions: []string{"textusplugin", "zip"}}}
		}
		paths, err := mygo.Dialog.Open(opts)
		if err != nil || len(paths) == 0 {
			return nil, err
		}
		return &paths, nil
	})
	// Settings › Plugins › Open Scripts Folder: made if it isn't there.
	command("plugin_open_scripts_dir", func(c *Call, _ none) (nothing, error) {
		dir := c.app.scriptsDir()
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
		return nil, mygo.Shell.OpenPath(dir)
	})
	// When a developer's plugin folder last changed (newest file, in ms,
	// ignoring node_modules and .git), so the window reloads it. Nil for
	// other plugins; a script's folder for a script.
	command("plugin_dev_stamp", func(c *Call, a struct {
		ID string `json:"id"`
	}) (*float64, error) {
		if err := plugins.RequireID(a.ID); err != nil {
			return nil, err
		}
		if e := c.app.pluginState().Plugins[a.ID]; e != nil && e.DevPath != "" {
			return plugins.NewestChange(e.DevPath, 0), nil
		}
		if c.app.hasScript(a.ID) {
			return plugins.NewestChange(c.app.scriptsDir(), 0), nil
		}
		return nil, nil
	})
	// Export for publishing: plugin id laid out in folder as publisher's.
	command("plugin_export", func(c *Call, a struct {
		ID        string `json:"id"`
		Publisher string `json:"publisher"`
		Folder    string `json:"folder"`
	}) (string, error) {
		source, err := c.app.pluginHome(c.app.pluginState(), a.ID)
		if err != nil {
			return "", err
		}
		return plugins.ExportTo(source, a.Publisher, a.Folder)
	})
	// One of the plugin's own secrets; it needs no permission, and reaches
	// no other plugin's.
	command("plugin_secret_get", func(c *Call, a struct {
		ID  string `json:"id"`
		Key string `json:"key"`
	}) (*string, error) {
		if err := c.app.pluginState().Running(a.ID); err != nil {
			return nil, err
		}
		secrets, err := readJSONStore[pluginSecrets](c.app.secretsPath())
		if err != nil {
			return nil, err
		}
		if v, ok := secrets[a.ID][a.Key]; ok {
			return &v, nil
		}
		return nil, nil
	})
	// Keeps a secret of the plugin's; nil forgets it.
	command("plugin_secret_set", func(c *Call, a struct {
		ID    string  `json:"id"`
		Key   string  `json:"key"`
		Value *string `json:"value"`
	}) (nothing, error) {
		if err := c.app.pluginState().Running(a.ID); err != nil {
			return nil, err
		}
		if a.Key == "" || len(a.Key) > 200 {
			return nil, errorf("A secret's key is 1 to 200 characters.")
		}
		c.app.plug.secretsMu.Lock()
		defer c.app.plug.secretsMu.Unlock()
		secrets, err := readJSONStore[pluginSecrets](c.app.secretsPath())
		if err != nil {
			return nil, err
		}
		if secrets == nil {
			secrets = pluginSecrets{}
		}
		if a.Value != nil {
			if secrets[a.ID] == nil {
				secrets[a.ID] = map[string]string{}
			}
			secrets[a.ID][a.Key] = *a.Value
		} else if own := secrets[a.ID]; own != nil {
			delete(own, a.Key)
			if len(own) == 0 {
				delete(secrets, a.ID)
			}
		}
		return nil, writeSecretJSON(c.app.secretsPath(), secrets)
	})
}
