//go:build !windows

package backend

import (
	"os/exec"
	"runtime"
)

// detachedCommand starts program in a session of its own (Linux), so
// closing the terminal leaves the window open.
func detachedCommand(program string, args []string) *exec.Cmd {
	if runtime.GOOS == "linux" {
		if setsid, err := exec.LookPath("setsid"); err == nil {
			return exec.Command(setsid, append([]string{"-f", program}, args...)...)
		}
	}
	return exec.Command(program, args...)
}

// attachConsole: a terminal's output is the process's own here.
func attachConsole() {}
