package programs

import (
	"os/exec"
	"syscall"
)

// createNoWindow keeps a console program from opening a window.
const createNoWindow = 0x0800_0000

func quiet(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNoWindow}
}
