package backend

import (
	"fmt"
	"os"
	"slices"
	"strings"
	"unicode/utf16"
)

// Installs the `textus` terminal command: a script that runs the app's own
// binary marked as started from a terminal (cliScriptEnv), which answers
// commands itself and hands paths to the app window at once.
//   - macOS: /usr/local/bin/textus runs the binary inside the .app, or
//     `open -a Textus` once that is gone.
//   - Windows: bin\textus.cmd beside the app, that folder on the user's
//     PATH.
//   - Linux: ~/.local/bin/textus runs the AppImage or the installed binary,
//     in place of the link to the binary that MyGo's install.sh makes; a
//     package that puts the binary itself at /usr/bin/textus needs none.
//
// Startup installs it quietly where no password is needed; Settings ›
// General retries. On Windows startup only keeps the script current: the
// user's PATH is changed only from Settings, and never unless it was read
// whole.

// cliStatus is what Settings shows: outdated is a script an older Textus
// wrote, which only opens the app. Mirrors CliStatus in
// src/platform/bindings/app.ts.
type cliStatus string

const (
	cliInstalled cliStatus = "installed"
	cliOutdated  cliStatus = "outdated"
	cliMissing   cliStatus = "missing"
)

// cliStatusOf is the status from the script found, if any, and whether it
// is this app's.
func cliStatusOf(script *string, current func(string) bool) cliStatus {
	switch {
	case script == nil:
		return cliMissing
	case current(*script):
		return cliInstalled
	}
	return cliOutdated
}

// readCLIScript is the script at path, nil when there is none.
func readCLIScript(path string) *string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	text := string(raw)
	return &text
}

// macScript is /usr/local/bin/textus: the binary at exe, falling back to
// `open -a` (the whole script before commands, and still a dev build's,
// whose exe is "").
func macScript(exe, appName string) string {
	open := fmt.Sprintf("exec open -a \"%s\" \"$@\"\n", appName)
	if exe == "" {
		return "#!/bin/bash\n" + open
	}
	quoted := strings.ReplaceAll(exe, "'", `'\''`)
	return fmt.Sprintf("#!/bin/sh\nif [ -x '%s' ]; then\n  %s=1 exec '%s' \"$@\"\nfi\n%s", quoted, cliScriptEnv, quoted, open)
}

// windowsScript is textus.cmd: it waits for the binary, which prints and
// returns its exit code, or hands paths to the window and returns at once.
// %~dp0.. keeps it working when the install folder moves.
func windowsScript(exeName string) string {
	return fmt.Sprintf("@echo off\r\nsetlocal\r\nset \"%s=1\"\r\n\"%%~dp0..\\%s\" %%*\r\n", cliScriptEnv, exeName)
}

// linuxScript is ~/.local/bin/textus: the AppImage or the binary, which
// detaches the window from the terminal itself.
func linuxScript(app string) string {
	quoted := strings.ReplaceAll(app, "'", `'\''`)
	return fmt.Sprintf("#!/bin/sh\n%s=1 exec '%s' \"$@\"\n", cliScriptEnv, quoted)
}

// pathHas is whether a ;-separated PATH holds dir, compared as Windows
// does.
func pathHas(path, dir string) bool {
	norm := func(s string) string { return strings.ToLower(strings.TrimRight(strings.TrimSpace(s), `\`)) }
	for _, p := range strings.Split(path, ";") {
		if norm(p) == norm(dir) {
			return true
		}
	}
	return false
}

func pathWith(path, dir string) string {
	rest := strings.TrimRight(path, ";")
	if rest == "" {
		return dir
	}
	return rest + ";" + dir
}

// nextPath is the PATH to write: old (nil when the user has none) with dir
// added at its end, or false when it can't be shown to keep every entry of
// old - the one check between a read and a write that could lose the
// user's PATH.
func nextPath(old *string, dir string) (string, bool) {
	before := ""
	if old != nil {
		before = *old
	}
	if strings.TrimSpace(dir) == "" || strings.Contains(dir, ";") {
		return "", false
	}
	next := pathWith(before, dir)
	kept := strings.TrimRight(before, ";")
	if !strings.HasPrefix(next, kept) || len(next) <= len(kept) {
		return "", false
	}
	return next, true
}

// registryText is a registry value's UTF-16 data as text: false for data
// that isn't text throughout (a NUL before its end, a lone surrogate),
// which can't be written back as it was.
func registryText(data []byte) (string, bool) {
	if len(data)%2 != 0 {
		return "", false
	}
	units := make([]uint16, len(data)/2)
	for i := range units {
		units[i] = uint16(data[2*i]) | uint16(data[2*i+1])<<8
	}
	for len(units) > 0 && units[len(units)-1] == 0 {
		units = units[:len(units)-1]
	}
	if slices.Contains(units, 0) {
		return "", false
	}
	text := string(utf16.Decode(units))
	if !slices.Equal(utf16.Encode([]rune(text)), units) {
		return "", false
	}
	return text, true
}

// installCLIQuietly installs the command at startup where it needs no
// password, or keeps it current. A development build installs nothing by
// itself: its binary is a build folder's, not the app's.
func (a *App) installCLIQuietly() {
	if devBuild {
		return
	}
	quietCLIInstall()
}

func init() {
	command("cli_command_status", func(c *Call, _ none) (cliStatus, error) {
		return cliCommandStatus(), nil
	})
	command("install_cli_command", func(c *Call, _ none) (nothing, error) {
		return nil, installCLICommand()
	})
}
