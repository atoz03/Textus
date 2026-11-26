package backend

import (
	"os"
	"path/filepath"
	"testing"
)

// MyGo's install.sh links ~/.local/bin/textus to the app's binary. The
// script takes the link's place: written through the link, it would
// overwrite the binary it leads to.
func TestTheScriptReplacesALinkToTheAppAndLeavesTheAppAlone(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("APPIMAGE", "")
	if cliPackagedHere() {
		t.Skip("/usr/bin/textus is installed here: no script is written")
	}
	app := filepath.Join(home, ".local", "textus.app", "textus")
	binary := "\x7fELF the installed app"
	if err := os.MkdirAll(filepath.Dir(app), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(app, []byte(binary), 0o755); err != nil {
		t.Fatal(err)
	}
	link, _ := cliScriptPath()
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(app, link); err != nil {
		t.Fatal(err)
	}

	if err := installCLICommand(); err != nil {
		t.Fatal(err)
	}

	if got, err := os.ReadFile(app); err != nil || string(got) != binary {
		t.Errorf("the installed app was overwritten: %q %v", got, err)
	}
	info, err := os.Lstat(link)
	if err != nil || !info.Mode().IsRegular() {
		t.Fatalf("the command is not a file of its own: %v %v", info, err)
	}
	exe, _ := selfPath()
	if got, _ := os.ReadFile(link); string(got) != linuxScript(exe) {
		t.Errorf("script: %q", got)
	}
	if cliCommandStatus() != cliInstalled {
		t.Error("not installed afterwards")
	}
}
