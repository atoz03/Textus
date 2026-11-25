package plugins

import (
	"encoding/json/v2"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"textus/backend/ai"
)

func exportable(t *testing.T, dir string) {
	t.Helper()
	write(t, filepath.Join(dir, Manifest), `{"id":"local.word-count","name":"%name%","publisher":"local","description":"Counts.","version":"0.1.0","l10n":{"en":{"name":"Word Count"}},"contributes":{"commands":[{"command":"local.word-count.count","title":"Count"}]}}`)
	write(t, filepath.Join(dir, "host.js"), `textus.commands.register("local.word-count.count", () => {});`)
	write(t, filepath.Join(dir, "lib/x.js"), "1")
	write(t, filepath.Join(dir, ".DS_Store"), "junk")
}

func readString(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestLaysAPluginOutAsItsPublishers(t *testing.T) {
	tmp := t.TempDir()
	source, out := filepath.Join(tmp, "src"), filepath.Join(tmp, "out")
	exportable(t, source)
	target, err := ExportTo(source, "jane", out)
	if err != nil {
		t.Fatal(err)
	}
	if target != filepath.Join(out, "jane.word-count") {
		t.Errorf("%s", target)
	}
	var manifest struct {
		ID          string `json:"id"`
		Publisher   string `json:"publisher"`
		Contributes struct {
			Commands []struct {
				Command string `json:"command"`
			} `json:"commands"`
		} `json:"contributes"`
	}
	if err := json.Unmarshal([]byte(readString(t, filepath.Join(target, Manifest))), &manifest); err != nil {
		t.Fatal(err)
	}
	// Its own ids follow the new one, or Textus wouldn't load it.
	if manifest.ID != "jane.word-count" || manifest.Publisher != "jane" || manifest.Contributes.Commands[0].Command != "jane.word-count.count" {
		t.Errorf("%+v", manifest)
	}
	if !strings.Contains(readString(t, filepath.Join(target, "host.js")), `"jane.word-count.count"`) {
		t.Error("host.js keeps the old id")
	}
	if !isFile(filepath.Join(target, "lib/x.js")) || Exists(filepath.Join(target, ".DS_Store")) {
		t.Error("the files aren't copied as they should be")
	}
	readme := readString(t, filepath.Join(target, "README.md"))
	if !strings.HasPrefix(readme, "# Word Count\n\nCounts.") || !strings.Contains(readme, "jane.word-count-<version>.zip") {
		t.Errorf("README:\n%s", readme)
	}
	if !strings.Contains(readString(t, filepath.Join(target, "LICENSE")), " jane\n") {
		t.Error("the LICENSE doesn't name the publisher")
	}
	if !isFile(filepath.Join(target, ".github/workflows/release.yml")) || !isFile(filepath.Join(target, ".gitignore")) {
		t.Error("no release workflow or .gitignore")
	}
	// Not over a folder already there.
	if _, err := ExportTo(source, "jane", out); err == nil {
		t.Error("exported over a folder")
	}
}

func TestNamesAPluginWithoutEnglishInALanguageItHas(t *testing.T) {
	tmp := t.TempDir()
	source := filepath.Join(tmp, "src")
	exportable(t, source)
	manifest := strings.NewReplacer(`"l10n":{"en":`, `"l10n":{"zh":`, "Word Count", "字数统计").Replace(readString(t, filepath.Join(source, Manifest)))
	write(t, filepath.Join(source, Manifest), manifest)
	target, err := ExportTo(source, "jane", filepath.Join(tmp, "out"))
	if err != nil {
		t.Fatal(err)
	}
	if readme := readString(t, filepath.Join(target, "README.md")); !strings.HasPrefix(readme, "# 字数统计\n\nCounts.") {
		t.Errorf("README:\n%s", readme)
	}
}

func TestRefusesANameThatCantBeAPublisher(t *testing.T) {
	tmp := t.TempDir()
	source := filepath.Join(tmp, "src")
	exportable(t, source)
	for _, bad := range []string{"", "Jane", "jane doe", "-jane", "jane--doe", "jane.doe"} {
		if _, err := ExportTo(source, bad, tmp); err == nil {
			t.Errorf("%q is taken", bad)
		}
	}
}

func readable(t *testing.T, dir, id, publisher string) Installed {
	t.Helper()
	raw, err := json.Marshal(map[string]string{"id": id, "name": "Tasks", "version": "1.1.0", "publisher": publisher})
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dir, Manifest), string(raw))
	write(t, filepath.Join(dir, "host.js"), "exports.activate = () => {};")
	write(t, filepath.Join(dir, "lib/a.js"), "x")
	write(t, filepath.Join(dir, "icon.png"), string([]byte{0x89, 0x50, 0xff, 0xfe}))
	return Installed{ID: id, Dir: dir, Manifest: string(raw)}
}

func TestTheModelReadsTheListAPluginAndItsFiles(t *testing.T) {
	tmp := t.TempDir()
	all := []Installed{
		readable(t, filepath.Join(tmp, "tasks"), "catvinci.tasks", "catvinci"),
		readable(t, filepath.Join(tmp, "mine"), "local.mine", "local"),
	}
	answer := func(args string) string { return ReadPluginAnswer(all, args) }
	listed := answer("{}")
	for _, want := range []string{"- catvinci.tasks: Tasks 1.1.0, by catvinci - a change is a copy", "- local.mine: Tasks 1.1.0, the user's own"} {
		if !strings.Contains(listed, want) {
			t.Errorf("the list lacks %q:\n%s", want, listed)
		}
	}
	one := answer(`{"id":"catvinci.tasks"}`)
	for _, want := range []string{`"publisher":"catvinci"`, "host.js (28 bytes)", "lib/a.js (1 bytes)"} {
		if !strings.Contains(one, want) {
			t.Errorf("the plugin lacks %q:\n%s", want, one)
		}
	}
	if file := answer(`{"id":"catvinci.tasks","path":"host.js"}`); !strings.HasSuffix(file, "exports.activate = () => {};") {
		t.Errorf("the file:\n%s", file)
	}
	// Not text, not its own, not there.
	if got := answer(`{"id":"catvinci.tasks","path":"icon.png"}`); !strings.Contains(got, "isn't text") {
		t.Errorf("a picture: %s", got)
	}
	if got := answer(`{"id":"catvinci.tasks","path":"../mine/host.js"}`); !strings.Contains(got, "isn't a file of this plugin") {
		t.Errorf("another plugin's: %s", got)
	}
	// Hidden, as the listing leaves it out.
	write(t, filepath.Join(tmp, "tasks/.env"), "TOKEN=1")
	if got := answer(`{"id":"catvinci.tasks","path":".env"}`); !strings.Contains(got, "isn't a file of this plugin") {
		t.Errorf("a hidden file: %s", got)
	}
	if got := answer(`{"id":"x.y"}`); !strings.HasPrefix(got, "No plugin `x.y`") {
		t.Errorf("none: %s", got)
	}
}

func TestAModuleIsServedAsJavaScript(t *testing.T) {
	for rel, want := range map[string]string{
		"lib/plantuml.js":       "text/javascript",
		"lib/wasm/qcms_bg.wasm": "application/wasm",
		"lib/cmaps/GB-H.bcmap":  "application/octet-stream",
	} {
		if got := Mime(rel); got != want {
			t.Errorf("%s: %s", rel, got)
		}
	}
}

const wordCountScript = "// ==TextusScript==\n" +
	"// @name        Word Count\n" +
	"// @description Counts words.\n" +
	"// @permission  editor.read\n" +
	"// @permission  files.read\n" +
	"// @command     count Count Words\n" +
	"// ==/TextusScript==\n" +
	"exports.activate = () => {};\n"

func TestAHeaderMakesAManifest(t *testing.T) {
	id, manifest, ok := ScriptManifest(filepath.FromSlash("/s/Word Count.js"), wordCountScript)
	if !ok || id != "local.word-count" {
		t.Fatalf("%q %v", id, ok)
	}
	var m struct {
		Name        string   `json:"name"`
		Publisher   string   `json:"publisher"`
		Main        string   `json:"main"`
		Permissions []string `json:"permissions"`
		Contributes struct {
			Commands []map[string]string `json:"commands"`
		} `json:"contributes"`
	}
	if err := json.Unmarshal([]byte(manifest), &m); err != nil {
		t.Fatal(err)
	}
	if m.Name != "Word Count" || m.Publisher != "local" || m.Main != "Word Count.js" || !slices.Equal(m.Permissions, []string{"editor.read", "files.read"}) {
		t.Errorf("%+v", m)
	}
	if len(m.Contributes.Commands) != 1 || m.Contributes.Commands[0]["command"] != "local.word-count.count" || m.Contributes.Commands[0]["title"] != "Count Words" {
		t.Errorf("commands %v", m.Contributes.Commands)
	}
}

func TestAnIDCanBeGivenAndMustBeGood(t *testing.T) {
	text := strings.Replace(wordCountScript, "@name", "@id jane.counter\n// @name", 1)
	if id, _, ok := ScriptManifest("/s/x.js", text); !ok || id != "jane.counter" {
		t.Errorf("%q %v", id, ok)
	}
	bad := strings.Replace(wordCountScript, "@name", "@id Not An Id\n// @name", 1)
	if _, _, ok := ScriptManifest("/s/x.js", bad); ok {
		t.Error("a bad id is taken")
	}
}

func TestAFileWithNoHeaderIsNoScript(t *testing.T) {
	if _, _, ok := ScriptManifest("/s/x.js", "exports.activate = () => {};"); ok {
		t.Error("taken for a script")
	}
}

func TestTellsLinksFromFiles(t *testing.T) {
	for arg, want := range map[string]bool{
		"textus://catvinci.zotero/item?key=AB12": true,
		"TEXTUS://x.y/":                          true,
		"textus:/x":                              false,
		"/home/me/textus://x.md":                 false,
		"notes.md":                               false,
		"tex":                                    false,
	} {
		if IsTextusURI(arg) != want {
			t.Errorf("%s: %v", arg, !want)
		}
	}
}

func TestANotificationsClickIsToldFromAPluginsLink(t *testing.T) {
	if id, ok := NotificationID("textus://notification/0af3"); !ok || id != "0af3" {
		t.Errorf("%q %v", id, ok)
	}
	for _, other := range []string{"textus://notification/", "textus://notification/../x", "textus://catvinci.zotero/item"} {
		if _, ok := NotificationID(other); ok {
			t.Errorf("%s is a notification's", other)
		}
	}
}

func TestAProfileKeepsTheMovedSkillsItHadOn(t *testing.T) {
	moved := ai.MovedToMarket
	if got := MovedSkillsHadOn(moved, nil); len(got) != len(moved) {
		t.Errorf("no list: %d", len(got))
	}
	on := MovedSkillsHadOn(moved, []any{"email", "proofread", "headline"})
	if slices.Contains(on, "email") || slices.Contains(on, "headline") || !slices.Contains(on, "describe") || len(on) != len(moved)-2 {
		t.Errorf("%q", on)
	}
}

func TestAProfileThatSawToTheFirstMoveStillGetsTheLaterOne(t *testing.T) {
	moved := ai.MovedToMarket
	first := MovedSkillsSeenTo(moved, true)
	if len(first) != firstMoved || slices.Contains(first, "translate") {
		t.Errorf("%q", first)
	}
	if got := MovedSkillsSeenTo(moved, nil); len(got) != 0 {
		t.Errorf("unset: %q", got)
	}
	var all any
	if err := json.Unmarshal(mustMarshal(t, moved), &all); err != nil {
		t.Fatal(err)
	}
	if got := MovedSkillsSeenTo(moved, all); len(got) != len(moved) {
		t.Errorf("all: %d", len(got))
	}
}

func mustMarshal(t *testing.T, v any) []byte {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestATokenNamesTheBytes(t *testing.T) {
	if Token([]byte("a")) != Token([]byte("a")) || Token([]byte("a")) == Token([]byte("b")) || len(Token(nil)) != 64 {
		t.Error("Token")
	}
}

func TestOnlyAFileAtTheSettingsFoldersTopIsNamed(t *testing.T) {
	for name, want := range map[string]bool{
		"bookmarks.json":                                  true,
		"plugins/x/data.json":                             false,
		"plugins/obsidian-citation-plugin/data.json":      true,
		"plugins/obsidian-citation-plugin/../x/data.json": false,
		"../.env": false,
		".hidden": false,
		"":        false,
	} {
		if SettingsFileName(name) != want {
			t.Errorf("%q: %v", name, !want)
		}
	}
}
