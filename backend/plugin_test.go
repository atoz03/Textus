package backend

import (
	"encoding/json/v2"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"textus/backend/ai"
	"textus/backend/plugins"
)

// toTestTrash moves what goes to the Trash into a folder of the test's,
// as the Trash takes it: nothing there is nothing to move.
func toTestTrash(t *testing.T) {
	t.Helper()
	trash := t.TempDir()
	old := moveToTrash
	moveToTrash = func(path string) error {
		if _, err := os.Lstat(path); err != nil {
			return nil
		}
		return os.Rename(path, filepath.Join(trash, fmt.Sprintf("%d-%s", time.Now().UnixNano(), filepath.Base(path))))
	}
	t.Cleanup(func() { moveToTrash = old })
}

// pluginApp is an app with folders of its own and the permission list
// main.go embeds.
func pluginApp(t *testing.T) *App {
	t.Helper()
	toTestTrash(t)
	raw, err := os.ReadFile("../src/plugins/store/permissions.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := plugins.LoadPermissions(raw); err != nil {
		t.Fatal(err)
	}
	return &App{dirs: Dirs{Config: t.TempDir(), Data: t.TempDir(), Cache: t.TempDir()}}
}

// A folder of notes, a plain-text one and a hidden one.
func notesFolder(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeText(t, filepath.Join(root, "a.md"), "# A\n\nSee [[B]].\n- [ ] Call Ann\n")
	writeText(t, filepath.Join(root, "sub/b.markdown"), "B, calling back.\n")
	writeText(t, filepath.Join(root, "notes.txt"), "call me")
	writeText(t, filepath.Join(root, ".hidden/c.md"), "call")
	return root
}

func TestDocumentsAreTheFoldersMarkdownWithTheirText(t *testing.T) {
	found := documentsIn(notesFolder(t))
	var rels []string
	for _, d := range found.Documents {
		rels = append(rels, d.Rel)
	}
	if !slices.Equal(rels, []string{"sub/b.markdown", "a.md"}) || found.Truncated {
		t.Fatalf("%q %v", rels, found.Truncated)
	}
	if !strings.Contains(found.Documents[1].Text, "[[B]]") {
		t.Errorf("%q", found.Documents[1].Text)
	}
}

func TestASearchFindsLinesIgnoringCaseAndStopsAtItsLimit(t *testing.T) {
	root := notesFolder(t)
	far := time.Now().Add(searchTime)
	found := searchIn(root, "CALL", 10, far)
	type at struct {
		rel  string
		line uint32
	}
	var lines []at
	for _, h := range found.Hits {
		lines = append(lines, at{h.Rel, h.Line})
	}
	// A plain-text note is searched as it opens: a document.
	for _, want := range []at{{"notes.txt", 1}, {"a.md", 4}, {"sub/b.markdown", 1}} {
		if !slices.Contains(lines, want) {
			t.Errorf("no hit at %v: %v", want, lines)
		}
	}
	if found.Truncated || len(found.Hits) < 2 || found.Hits[1].Text != "- [ ] Call Ann" {
		t.Errorf("%+v", found)
	}
	if one := searchIn(root, "call", 1, far); len(one.Hits) != 1 || !one.Truncated {
		t.Errorf("limit 1: %+v", one)
	}
	if none := searchIn(root, "  ", 10, far); len(none.Hits) != 0 {
		t.Errorf("blank: %+v", none)
	}
}

func TestASkillInstallsOnceAndNeverOverOneThere(t *testing.T) {
	dir := t.TempDir()
	text := "---\nname: press-release\ndescription: Writes a press release.\n---\n\nWrite it as a press release."
	path, err := installSkillIn(dir, "press-release", text)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
	if _, err := installSkillIn(dir, "press-release", text); !hasCode(err, skillExists) {
		t.Errorf("again: %v", err)
	}
	if _, err := installSkillIn(dir, "empty", "---\nname: empty\n---\n"); err == nil {
		t.Error("an empty skill is installed")
	}
}

func TestANewerCatalogSkillUpdatesItsOlderSelfButNotTheUsersOwn(t *testing.T) {
	dir := t.TempDir()
	at := func(v string) string {
		return "---\nname: tone\ndescription: Tone.\nmetadata:\n  textus-version: \"" + v + "\"\n---\n\nTone " + v + "."
	}
	path, err := installSkillIn(dir, "tone", at("1.0.0"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := installSkillIn(dir, "tone", at("1.0.0")); err == nil {
		t.Error("the same version is installed again")
	}
	if got, err := installSkillIn(dir, "tone", at("1.1.0")); err != nil || got != path {
		t.Errorf("the update: %q %v", got, err)
	}
	if raw, _ := os.ReadFile(path); !strings.Contains(string(raw), "Tone 1.1.0.") {
		t.Errorf("not updated: %s", raw)
	}
	// One the user wrote, with no version, is never replaced.
	if _, err := installSkillIn(dir, "mine", "---\nname: mine\ndescription: Mine.\n---\n\nMine."); err != nil {
		t.Fatal(err)
	}
	offered := "---\nname: mine\ndescription: Mine.\nmetadata:\n  textus-version: \"2.0.0\"\n---\n\nTheirs."
	if _, err := installSkillIn(dir, "mine", offered); err == nil {
		t.Error("the user's own skill is replaced")
	}
	// An older one offered is no update either.
	if _, err := installSkillIn(dir, "tone", at("1.0.5")); err == nil {
		t.Error("an older version is installed")
	}
}

// A connection that drops halfway through is tried once more, as on a
// weak network (#580).
func TestADownloadCutOffHalfwayIsTriedAgain(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		for i := range 2 {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			buf := make([]byte, 1024)
			_, _ = conn.Read(buf)
			body := "whole body"
			if i == 0 {
				body = "half"
			}
			_, _ = io.WriteString(conn, "HTTP/1.1 200 OK\r\nContent-Length: 10\r\nConnection: close\r\n\r\n"+body)
			conn.Close()
		}
	}()
	client := releaseClient(nil)
	data, err := downloadWith(t.Context(), client, "http://"+listener.Addr().String()+"/p.zip", "p")
	if err != nil || string(data) != "whole body" {
		t.Errorf("%q %v", data, err)
	}
}

func TestANewProfileHasNoMovedSkillsToBringOverAndAnOldOneDoes(t *testing.T) {
	dir := t.TempDir()
	stores := loadStores(Dirs{Config: dir, Data: dir})
	all := string(mustJSON(ai.MovedToMarket))
	if got, _ := stores.settings.get(movedSkillsKey); string(got) != all {
		t.Errorf("a new profile: %s", got)
	}
	// Written out with the first change, as any setting.
	stores.settings.patch(jsonObject{"theme": mustJSON("dark")})
	if err := stores.settings.flush(); err != nil {
		t.Fatal(err)
	}
	var file struct {
		Settings map[string]any `json:"settings"`
	}
	raw, _ := os.ReadFile(filepath.Join(dir, fileName("settings")))
	if err := json.Unmarshal(raw, &file); err != nil || file.Settings[movedSkillsKey] == nil {
		t.Errorf("written: %s %v", raw, err)
	}
	// A settings file from before the move: its skills still to install.
	old := t.TempDir()
	writeText(t, filepath.Join(old, fileName("settings")), `{"schemaVersion": 1, "settings": {"theme": "dark"}}`)
	if got, ok := loadStores(Dirs{Config: old, Data: old}).settings.get(movedSkillsKey); ok {
		t.Errorf("an old profile: %s", got)
	}
}

// A plugin package with a manifest asking for files.read and a network
// host.
func pluginPackage(t *testing.T, id string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), id)
	writeText(t, filepath.Join(dir, plugins.Manifest), `{"id":"`+id+`","version":"1.0.0","name":"X","publisher":"a",
		"engines":{"textus":"^1.3.0"},"permissions":["files.read","project.data","network:api.example.com"]}`)
	writeText(t, filepath.Join(dir, "host.js"), "exports.activate = () => {};")
	return dir
}

func TestAPluginIsInstalledOffAndUninstalledWithWhatItKept(t *testing.T) {
	a := pluginApp(t)
	got, err := run(a, "plugin_install", `{"path": `+string(mustJSON(pluginPackage(t, "a.b")))+`}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, `"id":"a.b"`) {
		t.Errorf("installed: %s", got)
	}
	if listed, _ := run(a, "plugins_installed", `{}`); !strings.Contains(listed, `"id":"a.b"`) {
		t.Errorf("listed: %s", listed)
	}
	// Off until the user turns it on, and then only with what it asks for.
	if _, err := run(a, "plugin_store_set", `{"id":"a.b","scope":"global","root":null,"key":"k","value":1}`); err == nil {
		t.Error("a plugin that is off keeps data")
	}
	if _, err := run(a, "plugin_set", `{"id":"a.b","enabled":true,"granted":["files.read","files.write","network:api.example.com"]}`); err != nil {
		t.Fatal(err)
	}
	if granted := a.pluginState().Granted("a.b"); !slices.Equal(granted, []string{"files.read", "network:api.example.com"}) {
		t.Errorf("granted %q", granted)
	}
	if _, err := run(a, "plugin_store_set", `{"id":"a.b","scope":"global","root":null,"key":"k","value":{"n":1}}`); err != nil {
		t.Fatal(err)
	}
	if v, _ := run(a, "plugin_store_get", `{"id":"a.b","scope":"global","root":null,"key":"k"}`); v != `{"n":1}` {
		t.Errorf("kept: %s", v)
	}
	if v, _ := run(a, "plugin_store_get", `{"id":"a.b","scope":"global","root":null,"key":"none"}`); v != `null` {
		t.Errorf("nothing kept: %s", v)
	}
	// The project's place needs project.data, which it wasn't granted.
	if _, err := run(a, "plugin_store_keys", `{"id":"a.b","scope":"project","root":null}`); err == nil || !strings.Contains(err.Error(), "project.data") {
		t.Errorf("project place: %v", err)
	}
	if _, err := run(a, "plugin_secret_set", `{"id":"a.b","key":"token","value":"s3"}`); err != nil {
		t.Fatal(err)
	}
	if v, _ := run(a, "plugin_secret_get", `{"id":"a.b","key":"token"}`); v != `"s3"` {
		t.Errorf("secret: %s", v)
	}
	if info, err := os.Stat(a.secretsPath()); err != nil || !ownerOnly(info) {
		t.Errorf("the secrets file: %v %v", info, err)
	}
	if _, err := run(a, "plugin_uninstall", `{"id":"a.b"}`); err != nil {
		t.Fatal(err)
	}
	if plugins.Exists(filepath.Join(a.pluginsRoot(), "a.b")) || plugins.Exists(plugins.GlobalPlace(a.globalAgentPath(), "a.b")) {
		t.Error("its files or storage are left")
	}
	if _, ok := a.pluginState().Plugins["a.b"]; ok {
		t.Error("its entry is left")
	}
	if secrets, _ := readJSONStore[pluginSecrets](a.secretsPath()); len(secrets) != 0 {
		t.Errorf("its secrets are left: %v", secrets)
	}
}

func TestAPackageInstalledOverAPluginStartsOffWithoutItsSecrets(t *testing.T) {
	a := pluginApp(t)
	pkg := pluginPackage(t, "a.b")
	if _, err := a.installPlugin(pkg, fromFile); err != nil {
		t.Fatal(err)
	}
	if err := a.updatePlugins(func(s *plugins.State) {
		e := s.Entry("a.b")
		e.Enabled, e.Granted = true, []string{"files.read"}
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := run(a, "plugin_secret_set", `{"id":"a.b","key":"token","value":"s3"}`); err != nil {
		t.Fatal(err)
	}
	if _, err := a.installPlugin(pkg, fromFile); err != nil {
		t.Fatal(err)
	}
	e := a.pluginState().Plugins["a.b"]
	if e == nil || e.Enabled || len(e.Granted) != 0 {
		t.Errorf("entry %+v", e)
	}
	if secrets, _ := readJSONStore[pluginSecrets](a.secretsPath()); len(secrets) != 0 {
		t.Errorf("secrets kept for other code: %v", secrets)
	}
}

func TestAPluginReadsNoneOfTheAppsOwnFiles(t *testing.T) {
	a := pluginApp(t)
	if err := a.updatePlugins(func(s *plugins.State) {
		e := s.Entry("a.b")
		e.Enabled, e.Granted = true, []string{"files.read"}
	}); err != nil {
		t.Fatal(err)
	}
	writeText(t, filepath.Join(a.dirs.Config, "notes/a.md"), "# A")
	if _, err := run(a, "plugin_fs_list", `{"id":"a.b","root":`+string(mustJSON(a.dirs.Config))+`,"path":"notes"}`); err == nil {
		t.Error("the app's config folder is listed")
	}
	if _, err := run(a, "plugin_metadata_all", `{"id":"a.b","root":`+string(mustJSON(a.dirs.Data))+`,"after":null}`); err == nil {
		t.Error("the app's data folder is indexed")
	}
	root := notesFolder(t)
	got, err := run(a, "plugin_metadata_all", `{"id":"a.b","root":`+string(mustJSON(root))+`,"after":null}`)
	if err != nil || !strings.Contains(got, `"rel":"a.md"`) || strings.Contains(got, "c.md") {
		t.Errorf("%s %v", got, err)
	}
	one, err := run(a, "plugin_metadata_get", `{"id":"a.b","root":`+string(mustJSON(root))+`,"path":"a.md"}`)
	if err != nil || !strings.Contains(one, `"rel":"a.md"`) || !strings.Contains(one, `"blocks":[`) {
		t.Errorf("%s %v", one, err)
	}
	if _, err := run(a, "plugin_metadata_all", `{"id":"c.d","root":`+string(mustJSON(root))+`,"after":null}`); err == nil {
		t.Error("a plugin without files.read reads the folder")
	}
}

// otherSpellings are the spellings of dir that name it on this machine
// besides its own: through a link, in upper case where the file system
// ignores case (macOS, Windows), through a Mac's data volume.
func otherSpellings(t *testing.T, dir string) []string {
	t.Helper()
	dir = resolvedPath(dir)
	var out []string
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(dir, link); err == nil {
		out = append(out, link)
	} else {
		t.Logf("no links here: %v", err)
	}
	for _, p := range []string{strings.ToUpper(dir), "/System/Volumes/Data" + dir} {
		if info, err := os.Stat(p); err == nil && info.IsDir() {
			out = append(out, p)
		}
	}
	return out
}

func TestAPluginReadsNoneOfTheAppsOwnFilesUnderAnotherSpelling(t *testing.T) {
	a := pluginApp(t)
	if err := a.updatePlugins(func(s *plugins.State) {
		s.AllOn = true
		e := s.Entry("a.b")
		e.Enabled, e.Granted = true, []string{"files.read"}
	}); err != nil {
		t.Fatal(err)
	}
	config := a.dirs.Config
	for _, name := range []string{"plugin_secrets.json", "settings.json"} {
		writeText(t, filepath.Join(config, name), `{"key":"s3cret"}`)
	}
	writeText(t, filepath.Join(a.dirs.Data, "notes", "a.md"), "# A")
	refused := func(what string, err error) {
		t.Helper()
		if err == nil || !strings.Contains(err.Error(), "app's own files") {
			t.Errorf("%s: %v", what, err)
		}
	}
	parent, name := filepath.Dir(resolvedPath(config)), filepath.Base(resolvedPath(config))
	for _, root := range otherSpellings(t, parent) {
		for _, file := range []string{"plugin_secrets.json", "settings.json"} {
			_, err := run(a, "plugin_fs_read", `{"id":"a.b","root":`+string(mustJSON(root))+`,"path":`+string(mustJSON(name+"/"+file))+`}`)
			refused(root+": "+file, err)
			_, err = run(a, "plugin_read_resource", `{"id":"a.b","path":`+string(mustJSON(filepath.Join(root, name, file)))+`}`)
			refused(root+": the resource "+file, err)
		}
		_, err := run(a, "plugin_fs_list", `{"id":"a.b","root":`+string(mustJSON(root))+`,"path":`+string(mustJSON(name))+`}`)
		refused(root+": the listing", err)
	}
	for _, root := range append(otherSpellings(t, a.dirs.Data), otherSpellings(t, config)...) {
		_, err := run(a, "plugin_metadata_all", `{"id":"a.b","root":`+string(mustJSON(root))+`,"after":null}`)
		refused(root+": the index", err)
		_, err = run(a, "plugin_fs_search", `{"id":"a.b","root":`+string(mustJSON(root))+`,"query":"a"}`)
		refused(root+": the search", err)
	}
}
