package backend

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestMacAndWindowsSpellAPathInOneCase(t *testing.T) {
	for _, c := range []struct{ goos, path, want string }{
		{"darwin", "/Users/Ann/Library/Application Support", "/users/ann/library/application support"},
		{"darwin", "/System/Volumes/Data/Users/Ann/.ssh", "/users/ann/.ssh"},
		{"darwin", "/SYSTEM/volumes/DATA/private/var/folders", "/private/var/folders"},
		{"darwin", "/System/Volumes/Data", "/"},
		{"darwin", "/System/Volumes/Database/x", "/system/volumes/database/x"},
		{"windows", `C:\Users\Ann\AppData\Roaming`, `c:\users\ann\appdata\roaming`},
		{"linux", "/System/Volumes/Data/Home/Ann", "/System/Volumes/Data/Home/Ann"},
	} {
		if got := spelledFor(c.goos, c.path); got != c.want {
			t.Errorf("%s %s: %s, want %s", c.goos, c.path, got, c.want)
		}
	}
}

// linkTo is a new link to dir, or a skip where links can't be made.
func linkTo(t *testing.T, dir string) string {
	t.Helper()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(dir, link); err != nil {
		t.Skipf("no links here: %v", err)
	}
	return link
}

func TestAFolderReachedThroughALinkIsTheSamePlace(t *testing.T) {
	real := t.TempDir()
	config := filepath.Join(real, "config")
	writeFile(t, filepath.Join(config, "settings.json"), "{}")
	link := linkTo(t, real)
	// /var and /private/var on a Mac: one spelled each way.
	if !inAnyDir(filepath.Join(link, "config", "settings.json"), config) {
		t.Error("a file through the link is outside the folder")
	}
	if !inAnyDir(filepath.Join(real, "config", "new.json"), filepath.Join(link, "config")) {
		t.Error("a new file is outside the folder named through the link")
	}
	if !samePath(filepath.Join(link, "config"), config) {
		t.Error("the folder through the link is another folder")
	}
	if inAnyDir(filepath.Join(real, "configs", "a.md"), config) || inAnyDir(real, config) {
		t.Error("a folder beside it or holding it is inside it")
	}
	if inAnyDir(config, "") || samePath(config, "") {
		t.Error("no folder holds everything")
	}
}

// caseBlind is whether the file system under dir ignores case, as macOS's
// and Windows's do by default.
func caseBlind(t *testing.T, dir string) bool {
	t.Helper()
	probe := filepath.Join(dir, "Probe")
	writeFile(t, probe, "")
	_, err := os.Stat(filepath.Join(dir, "PROBE"))
	return err == nil
}

func TestAnotherCaseIsTheSamePlaceWhereTheFileSystemIgnoresIt(t *testing.T) {
	root := resolvedPath(t.TempDir())
	if !caseBlind(t, root) {
		t.Skip("this file system tells case apart")
	}
	config := filepath.Join(root, "Config")
	writeFile(t, filepath.Join(config, "plugin_secrets.json"), "{}")
	for _, p := range []string{
		filepath.Join(root, "CONFIG", "plugin_secrets.json"),
		filepath.Join(strings.ToUpper(root), "config", "Plugin_Secrets.json"),
		filepath.Join(strings.ToLower(root), "cOnFiG"),
	} {
		if !inAnyDir(p, config) {
			t.Errorf("%s is outside %s", p, config)
		}
	}
	if !samePath(filepath.Join(root, "CONFIG"), config) {
		t.Error("the folder in another case is another folder")
	}
}

func TestAMacsDataVolumeIsTheSamePlace(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS only")
	}
	config := filepath.Join(resolvedPath(t.TempDir()), "config")
	writeFile(t, filepath.Join(config, "settings.json"), "{}")
	firm := "/System/Volumes/Data" + config
	if _, err := os.Stat(firm); err != nil {
		t.Skipf("no data volume here: %v", err)
	}
	if !inAnyDir(filepath.Join(firm, "settings.json"), config) || !samePath(firm, config) {
		t.Errorf("%s is another folder than %s", firm, config)
	}
}
