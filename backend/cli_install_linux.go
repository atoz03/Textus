package backend

import (
	"errors"
	"os"
	"path/filepath"
)

// cliPackaged is where a package that ships the binary itself as the
// command puts it: no script is needed then.
const cliPackaged = "/usr/bin/textus"

func cliScriptPath() (string, bool) {
	home := os.Getenv("HOME")
	if home == "" {
		return "", false
	}
	return filepath.Join(home, ".local", "bin", "textus"), true
}

func cliPackagedHere() bool {
	if os.Getenv("APPIMAGE") != "" {
		return false
	}
	_, err := os.Stat(cliPackaged)
	return err == nil
}

func cliCommandStatus() cliStatus {
	if cliPackagedHere() {
		return cliInstalled
	}
	app, err := selfPath()
	path, ok := cliScriptPath()
	if err != nil || !ok {
		return cliMissing
	}
	// A dev build (no AppImage) takes any script as current, so it never
	// swaps an installed app's script for its own.
	dev := devBuild && os.Getenv("APPIMAGE") == ""
	return cliStatusOf(readCLIScript(path), func(s string) bool { return dev || s == linuxScript(app) })
}

func quietCLIInstall() {
	if cliCommandStatus() != cliInstalled {
		_ = installCLICommand()
	}
}

func installCLICommand() error {
	if cliPackagedHere() {
		return nil
	}
	app, err := selfPath()
	if err != nil {
		return errors.New("No app path.")
	}
	path, ok := cliScriptPath()
	if !ok {
		return errors.New("No home folder.")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	// MyGo's install.sh links the command to the app's binary: the script
	// takes the link's place, as a write through it would overwrite the
	// binary.
	if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
		if err := os.Remove(path); err != nil {
			return err
		}
	}
	if err := os.WriteFile(path, []byte(linuxScript(app)), 0o755); err != nil {
		return err
	}
	return os.Chmod(path, 0o755)
}
