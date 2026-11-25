//go:build darwin && !ios

package backend

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

const cliBinPath = "/usr/local/bin/textus"

// bundledExe is this binary when it runs from an .app; "" for a dev build,
// which has none.
func bundledExe() string {
	if _, ok := macBundle(); !ok {
		return ""
	}
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	return exe
}

func cliScriptContent() string { return macScript(bundledExe(), "Textus") }

func writeCLIScript() error {
	if err := os.WriteFile(cliBinPath, []byte(cliScriptContent()), 0o755); err != nil {
		return err
	}
	return os.Chmod(cliBinPath, 0o755)
}

// cliCommandStatus: a dev build takes any script as current, so it never
// swaps an installed app's script for its own.
func cliCommandStatus() cliStatus {
	return cliStatusOf(readCLIScript(cliBinPath), func(s string) bool {
		return bundledExe() == "" || s == cliScriptContent()
	})
}

// quietCLIInstall installs only when /usr/local/bin is the user's to
// write, rather than ask for a password at startup.
func quietCLIInstall() {
	if cliCommandStatus() != cliInstalled {
		_ = writeCLIScript()
	}
}

func installCLICommand() error {
	if writeCLIScript() == nil {
		return nil
	}
	// A /usr/local/bin of root's: the script staged in a fresh owner-only
	// temporary file, which an administrator's shell copies, so AppleScript
	// only quotes paths.
	tmp, err := os.CreateTemp("", "textus-cli-install-")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	_, err = tmp.WriteString(cliScriptContent())
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	shell := fmt.Sprintf("mkdir -p /usr/local/bin && cp '%s' '%s' && chmod 755 '%s'", tmp.Name(), cliBinPath, cliBinPath)
	escaped := strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(shell)
	script := fmt.Sprintf(`do shell script "%s" with administrator privileges with prompt "Textus wants to install the 'textus' command in /usr/local/bin."`, escaped)
	var stderr bytes.Buffer
	cmd := exec.Command("osascript", "-e", script)
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		why := strings.TrimSpace(stderr.String())
		switch {
		case strings.Contains(why, "User canceled"):
			return errors.New("Cancelled.")
		case why != "":
			return errors.New(why)
		}
		return err
	}
	return nil
}
