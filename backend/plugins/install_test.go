package plugins

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAWrittenPluginIsTheUsersOwn(t *testing.T) {
	if id, err := WrittenID(`{"id":"local.word-count"}`); err != nil || id != "local.word-count" {
		t.Errorf("the user's own: %q %v", id, err)
	}
	if _, err := WrittenID(`{"id":"catvinci.tasks"}`); err == nil || !strings.Contains(err.Error(), "someone else's") {
		t.Errorf("someone else's: %v", err)
	}
	if _, err := WrittenID(`{}`); err == nil {
		t.Error("no id is taken")
	}
}

func TestAWrittenPluginFileStaysInsideThePlugin(t *testing.T) {
	for path, want := range map[string]bool{
		"host.js": true, "lib/view.js": true,
		"../host.js": false, "/etc/passwd": false, ".hidden": false, "": false, `a\b.js`: false,
	} {
		if PlainRelative(path) != want {
			t.Errorf("%q: %v", path, !want)
		}
	}
}

func writeManifest(t *testing.T, dir, id string) {
	t.Helper()
	write(t, filepath.Join(dir, Manifest), `{"id":"`+id+`","version":"1.0.0","name":"X"}`)
}

func TestAManifestNeedsAGoodIDAndAVersion(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, "a.b")
	if id, _, err := ReadManifest(dir); err != nil || id != "a.b" {
		t.Errorf("a good one: %q %v", id, err)
	}
	writeManifest(t, dir, "../evil")
	if _, _, err := ReadManifest(dir); err == nil {
		t.Error("a bad id is taken")
	}
	write(t, filepath.Join(dir, Manifest), `{"id":"a.b","version":" "}`)
	if _, _, err := ReadManifest(dir); err == nil {
		t.Error("no version is taken")
	}
}

func TestInstallingChecksTheWholeManifest(t *testing.T) {
	dir := t.TempDir()
	good := `{"id":"a.b","version":"1.0.0","name":"X","publisher":"a",
        "engines":{"textus":"^1.3.0"},"permissions":["files.read"]}`
	write(t, filepath.Join(dir, Manifest), good)
	if _, _, err := ReadCheckedManifest(dir); err != nil {
		t.Errorf("a good one: %v", err)
	}
	write(t, filepath.Join(dir, Manifest), strings.Replace(good, "files.read", "shell", 1))
	// Listed, so Settings can say what is wrong, but never installed.
	if _, _, err := ReadManifest(dir); err != nil {
		t.Errorf("not listed: %v", err)
	}
	if _, _, err := ReadCheckedManifest(dir); err == nil || !strings.Contains(err.Error(), "No permission") {
		t.Errorf("installed: %v", err)
	}
}

func TestAPackageMayHoldThePluginOrItsFolder(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, "a.b")
	if got, err := PluginDirIn(dir); err != nil || got != dir {
		t.Errorf("the plugin: %q %v", got, err)
	}
	outer := t.TempDir()
	writeManifest(t, filepath.Join(outer, "inner"), "a.b")
	if got, err := PluginDirIn(outer); err != nil || got != filepath.Join(outer, "inner") {
		t.Errorf("its folder: %q %v", got, err)
	}
	if _, err := PluginDirIn(t.TempDir()); err == nil {
		t.Error("an empty package is taken")
	}
}

func zipOf(t *testing.T, path string, files [][2]string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	z := zip.NewWriter(f)
	for _, file := range files {
		w, err := z.Create(file[0])
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(file[1])); err != nil {
			t.Fatal(err)
		}
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestAZipNamingAFileOutsideItselfIsRefused(t *testing.T) {
	tmp := t.TempDir()
	file := filepath.Join(tmp, "bad.textusplugin")
	zipOf(t, file, [][2]string{{"../escape.txt", "x"}})
	if Unzip(file, filepath.Join(tmp, "out")) == nil {
		t.Error("unpacked")
	}
	if Exists(filepath.Join(tmp, "escape.txt")) {
		t.Error("a file escaped")
	}
}

func TestAZipUnpacksItsFiles(t *testing.T) {
	tmp := t.TempDir()
	file := filepath.Join(tmp, "ok.textusplugin")
	zipOf(t, file, [][2]string{{Manifest, `{"id":"a.b","version":"1.0.0"}`}, {"dist/host.js", "1"}})
	out := filepath.Join(tmp, "out")
	if err := Unzip(file, out); err != nil {
		t.Fatal(err)
	}
	if !isFile(filepath.Join(out, "dist/host.js")) {
		t.Error("dist/host.js isn't there")
	}
	if got, err := PluginDirIn(out); err != nil || got != out {
		t.Errorf("%q %v", got, err)
	}
}

func TestAnUpdateThatCannotMoveInKeepsTheVersionThere(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "p")
	writeManifest(t, target, "p")
	aside := filepath.Join(root, ".p.old")
	if SwapIn(filepath.Join(root, "missing"), target, aside) == nil {
		t.Error("nothing moved in")
	}
	if !isFile(filepath.Join(target, Manifest)) || Exists(aside) {
		t.Error("the version there is gone")
	}
	fresh := filepath.Join(root, "new")
	writeManifest(t, fresh, "p")
	write(t, filepath.Join(fresh, "added"), "x")
	if err := SwapIn(fresh, target, aside); err != nil {
		t.Fatal(err)
	}
	if !isFile(filepath.Join(target, "added")) || Exists(aside) || Exists(fresh) {
		t.Error("the update isn't in place")
	}
}

func TestACheckoutsSecretsAndPackagesAreNotInstalled(t *testing.T) {
	from, to := t.TempDir(), t.TempDir()
	write(t, filepath.Join(from, "main.js"), "x")
	write(t, filepath.Join(from, ".env"), "TOKEN=1")
	write(t, filepath.Join(from, "node_modules/a.js"), "x")
	var total int64
	if err := CopyDir(from, filepath.Join(to, "p"), &total); err != nil {
		t.Fatal(err)
	}
	if !Exists(filepath.Join(to, "p/main.js")) || Exists(filepath.Join(to, "p/.env")) || Exists(filepath.Join(to, "p/node_modules")) {
		t.Error("a checkout's secrets or packages were copied")
	}
}
