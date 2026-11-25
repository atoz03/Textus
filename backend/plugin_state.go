package backend

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"textus/backend/plugins"
)

// Plugins: which are installed, which are on and what each was granted.
// The calls their sandboxed code makes through the app window are each
// checked again here, so a window bug can't grant more than the user did.

// pluginRuntime is what the plugin commands keep while the app runs.
type pluginRuntime struct {
	// stateMu, aiMu and secretsMu make each read-change-write of
	// state.json, ai.json and plugin_secrets.json one step.
	stateMu, aiMu, secretsMu sync.Mutex
	metadata                 plugins.Index

	mu sync.Mutex
	// offered are the AI tools each window's plugins offer, by its label.
	offered map[string][]plugins.AITool
	// waiting are the tool calls sent to a window, by call id.
	waiting map[string]chan toolAnswer
	// uris are textus:// links not yet taken by a window, oldest first.
	uris []string
	// fetched and fetchedSkills are the catalog as last fetched, for an
	// install to take its entry from.
	fetched       []plugins.CatalogEntry
	fetchedSkills []plugins.SkillEntry
	// menuItems, exports and trayItems are what the frontend last sent of
	// the Plugins menu, File › Export's formats and the tray's commands.
	menuItems []pluginMenuItem
	exports   []pluginExportItem
	trayItems []trayPluginItem
	// pendingCommand is a tray command asked for while no window was open.
	pendingCommand *string
}

// pluginsRoot is plugins/ in the app's data folder.
func (a *App) pluginsRoot() string { return filepath.Join(a.dirs.Data, "plugins") }

// pluginDataDir is plugin-data/<id>/ in the app's data folder.
func (a *App) pluginDataDir(id string) (string, error) {
	if err := plugins.RequireID(id); err != nil {
		return "", err
	}
	return filepath.Join(a.dirs.Data, "plugin-data", id), nil
}

// scriptsDir is scripts/ in the global agent folder.
func (a *App) scriptsDir() string { return filepath.Join(a.globalAgentPath(), "scripts") }

func (a *App) pluginStatePath() string { return filepath.Join(a.pluginsRoot(), "state.json") }

// pluginState is plugins/state.json; everything off without one, and a
// file that doesn't parse set aside.
func (a *App) pluginState() plugins.State {
	raw, err := os.ReadFile(a.pluginStatePath())
	if err != nil {
		return plugins.NewState()
	}
	s, err := plugins.ParseState(raw)
	if err != nil {
		log.Printf("textus: %s is not valid (%v); moving it aside", a.pluginStatePath(), err)
		moveAside(a.pluginStatePath())
		return plugins.NewState()
	}
	return s
}

// writePrettyJSON writes v to path, indented, keys in order.
func writePrettyJSON(path string, v any) error {
	raw, err := json.Marshal(v, json.Deterministic(true), jsontext.WithIndent("  "))
	if err != nil {
		return err
	}
	return writeFileAtomic(path, raw)
}

// updatePlugins reads the state, changes it and writes it back, as one
// step. Then what the plugins that are on open as documents is read again,
// a plugin switched on again has its paused jobs looked at, and every
// window reloads its plugins, so one turned off anywhere stops everywhere.
func (a *App) updatePlugins(change func(s *plugins.State)) error {
	a.plug.stateMu.Lock()
	s := a.pluginState()
	change(&s)
	err := writePrettyJSON(a.pluginStatePath(), s)
	a.plug.stateMu.Unlock()
	if err != nil {
		return err
	}
	a.refreshDocumentTypes()
	a.pluginsChanged()
	a.emitAll("plugins-changed", nil)
	return nil
}

// pluginsChanged is what follows a change of the plugins besides the
// windows' reload: the schedule's jobs looked at again.
func (a *App) pluginsChanged() {
	a.rearmSchedule()
}

// refreshDocumentTypes reads the plugins' document types again from the
// plugins that are on: at startup and after any is turned on or off,
// installed or removed.
func (a *App) refreshDocumentTypes() {
	s := a.pluginState()
	var manifests []string
	for _, p := range a.installedPlugins() {
		if s.Runs(p.ID) {
			manifests = append(manifests, p.Manifest)
		}
	}
	plugins.SetDocumentTypes(manifests)
}

// scripts are the scripts in the scripts folder that have a header, by id.
func (a *App) scripts() []plugins.Installed {
	dir := a.scriptsDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []plugins.Installed
	for _, e := range entries {
		if filepath.Ext(e.Name()) != ".js" {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		id, manifest, ok := plugins.ScriptManifest(e.Name(), string(raw))
		if ok && !slices.ContainsFunc(out, func(p plugins.Installed) bool { return p.ID == id }) {
			out = append(out, plugins.Installed{ID: id, Dir: dir, Manifest: manifest, Dev: true})
		}
	}
	return out
}

// scriptFile is the script whose header gives it id.
func (a *App) scriptFile(id string) (string, bool) {
	dir := a.scriptsDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", false
	}
	for _, e := range entries {
		path := filepath.Join(dir, e.Name())
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		if got, _, ok := plugins.ScriptManifest(e.Name(), string(raw)); ok && got == id {
			return path, true
		}
	}
	return "", false
}

// installedPlugins is every installed plugin: the developer folders in
// state.json, plugins/, and the scripts, by id. Unreadable manifests are
// left out; a developer folder wins.
func (a *App) installedPlugins() []plugins.Installed {
	out := []plugins.Installed{}
	has := func(id string) bool {
		return slices.ContainsFunc(out, func(p plugins.Installed) bool { return p.ID == id })
	}
	for id, entry := range a.pluginState().Plugins {
		if entry.DevPath == "" {
			continue
		}
		// Checked again each time: its author may have changed it.
		if got, text, err := plugins.ReadCheckedManifest(entry.DevPath); err == nil && got == id {
			out = append(out, plugins.Installed{ID: id, Dir: entry.DevPath, Manifest: text, Dev: true})
		}
	}
	root := a.pluginsRoot()
	entries, _ := os.ReadDir(root)
	for _, e := range entries {
		dir := filepath.Join(root, e.Name())
		if info, err := os.Stat(dir); err != nil || !info.IsDir() || plugins.RequireID(e.Name()) != nil || has(e.Name()) {
			continue
		}
		if got, text, err := plugins.ReadManifest(dir); err == nil && got == e.Name() {
			out = append(out, plugins.Installed{ID: got, Dir: dir, Manifest: text})
		}
	}
	for _, script := range a.scripts() {
		if !has(script.ID) {
			out = append(out, script)
		}
	}
	slices.SortFunc(out, func(x, y plugins.Installed) int { return strings.Compare(x.ID, y.ID) })
	return out
}

// pluginHome is where a plugin's own files are: its developer folder,
// plugins/<id>/, or the scripts folder for a script.
func (a *App) pluginHome(s plugins.State, id string) (string, error) {
	if err := plugins.RequireID(id); err != nil {
		return "", err
	}
	if e := s.Plugins[id]; e != nil && e.DevPath != "" {
		return e.DevPath, nil
	}
	installed := filepath.Join(a.pluginsRoot(), id)
	if info, err := os.Stat(installed); (err != nil || !info.IsDir()) && a.hasScript(id) {
		return a.scriptsDir(), nil
	}
	return installed, nil
}

func (a *App) hasScript(id string) bool {
	_, ok := a.scriptFile(id)
	return ok
}

// pluginOrigin is where a package came from: the catalog's is the plugin
// it says it is.
type pluginOrigin int

const (
	fromFile pluginOrigin = iota
	fromCatalog
)

// stagingName is a name for a folder made beside where a plugin goes.
func stagingName(kind string) string { return fmt.Sprintf(".%s-%d", kind, time.Now().UnixNano()) }

// installPlugin copies a plugin folder or unpacks a .textusplugin into
// plugins/<id>/, replacing an earlier version. Installed now, it is no
// longer a developer folder and not Official until the catalog says so. A
// file is code from anywhere under the same id: off, asked again for what
// it may do, and given no secret kept for the code before it. New programs
// to run are asked for again, an update or not.
func (a *App) installPlugin(source string, origin pluginOrigin) (plugins.Installed, error) {
	root := a.pluginsRoot()
	if err := os.MkdirAll(root, 0o755); err != nil {
		return plugins.Installed{}, err
	}
	// Unpacked beside where it goes, then renamed into place, so a failed
	// install leaves the earlier version.
	staging := filepath.Join(root, stagingName("installing"))
	defer os.RemoveAll(staging)
	if info, err := os.Stat(source); err == nil && info.IsDir() {
		var total int64
		if err := plugins.CopyDir(source, staging, &total); err != nil {
			return plugins.Installed{}, err
		}
	} else if err := plugins.Unzip(source, staging); err != nil {
		return plugins.Installed{}, err
	}
	// A zip may hold the plugin's folder rather than its files.
	dir, err := plugins.PluginDirIn(staging)
	if err != nil {
		return plugins.Installed{}, err
	}
	id, text, err := plugins.ReadCheckedManifest(dir)
	if err != nil {
		return plugins.Installed{}, err
	}
	target := filepath.Join(root, id)
	before, hadBefore := "", false
	if _, old, err := plugins.ReadManifest(target); err == nil {
		before, hadBefore = plugins.AgentCommands(old), true
	}
	if err := plugins.SwapIn(dir, target, staging+".old"); err != nil {
		return plugins.Installed{}, err
	}
	agentsChanged := hadBefore && before != plugins.AgentCommands(text)
	had := false
	err = a.updatePlugins(func(s *plugins.State) {
		e := s.Plugins[id]
		if e == nil {
			return
		}
		had = true
		if origin == fromFile {
			e.Enabled = false
			e.Granted = []string{}
		} else if agentsChanged {
			e.Granted = slices.DeleteFunc(e.Granted, func(g string) bool { return g == "agents" })
		}
		e.DevPath = ""
		e.Official = false
	})
	if err != nil {
		return plugins.Installed{}, err
	}
	if origin == fromFile && had {
		if err := a.forgetPluginSecrets(id); err != nil {
			return plugins.Installed{}, err
		}
	}
	return plugins.Installed{ID: id, Dir: target, Manifest: text}, nil
}

// installPluginFiles writes files Textus AI wrote into a folder of their
// own, then installs it as a package is installed.
func (a *App) installPluginFiles(files []plugins.File) (plugins.Installed, error) {
	if err := plugins.CheckWritten(files); err != nil {
		return plugins.Installed{}, err
	}
	root := a.pluginsRoot()
	if err := os.MkdirAll(root, 0o755); err != nil {
		return plugins.Installed{}, err
	}
	folder := filepath.Join(root, stagingName("written"))
	defer os.RemoveAll(folder)
	if err := plugins.LayOut(folder, files); err != nil {
		return plugins.Installed{}, err
	}
	return a.installPlugin(folder, fromFile)
}

// uninstallPlugin removes a plugin: its entry, its jobs, its storage and,
// unless loaded from a developer's folder, its files. What may be the
// user's goes to the Trash.
func (a *App) uninstallPlugin(id string) error {
	if err := plugins.RequireID(id); err != nil {
		return err
	}
	if err := a.updatePlugins(func(s *plugins.State) { delete(s.Plugins, id) }); err != nil {
		return err
	}
	a.forgetPluginJobs(id)
	target := filepath.Join(a.pluginsRoot(), id)
	if info, err := os.Stat(target); err == nil && info.IsDir() {
		if err := os.RemoveAll(target); err != nil {
			return err
		}
	}
	data, err := a.pluginDataDir(id)
	if err != nil {
		return err
	}
	if err := moveToTrash(data); err != nil {
		return err
	}
	global := a.globalAgentPath()
	for _, place := range []string{plugins.GlobalPlace(global, id), plugins.LooseDir(global, id)} {
		if info, err := os.Stat(place); err == nil && info.IsDir() {
			if err := moveToTrash(place); err != nil {
				return err
			}
		}
	}
	// A script's file goes to the Trash, or it would be found again.
	if file, ok := a.scriptFile(id); ok {
		if err := moveToTrash(file); err != nil {
			return err
		}
	}
	return a.forgetPluginSecrets(id)
}

// loadDevPlugin is Load a plugin folder…: the plugin runs from where it is,
// so changes show on reload. It starts off, like any new plugin - over one
// already here as well: other code under its id gets none of its
// permissions or secrets.
func (a *App) loadDevPlugin(path string) (plugins.Installed, error) {
	id, text, err := plugins.ReadCheckedManifest(path)
	if err != nil {
		return plugins.Installed{}, err
	}
	otherCode := false
	err = a.updatePlugins(func(s *plugins.State) {
		e := s.Entry(id)
		if e.DevPath != path {
			otherCode = true
			*e = plugins.Entry{Granted: []string{}, DevPath: path}
		}
	})
	if err != nil {
		return plugins.Installed{}, err
	}
	if otherCode {
		if err := a.forgetPluginSecrets(id); err != nil {
			return plugins.Installed{}, err
		}
	}
	return plugins.Installed{ID: id, Dir: path, Manifest: text, Dev: true}, nil
}
