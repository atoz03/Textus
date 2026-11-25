package backend

import (
	"os"
	"os/exec"
	"syscall"
)

const (
	detachedProcess       = 0x00000008
	createNewProcessGroup = 0x00000200
	attachParentProcess   = ^uint32(0) // ATTACH_PARENT_PROCESS, (DWORD)-1
)

var procAttachConsole = syscall.NewLazyDLL("kernel32.dll").NewProc("AttachConsole")

// detachedCommand starts program with no console, in a group of its own.
func detachedCommand(program string, args []string) *exec.Cmd {
	cmd := exec.Command(program, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: detachedProcess | createNewProcessGroup}
	return cmd
}

// attachConsole: a release build has no console of its own, so its output
// goes to the terminal that started it, unless that output is redirected.
func attachConsole() {
	if h, err := syscall.GetStdHandle(syscall.STD_OUTPUT_HANDLE); err == nil && h != 0 && h != syscall.InvalidHandle {
		return
	}
	if r, _, _ := procAttachConsole.Call(uintptr(attachParentProcess)); r == 0 {
		return
	}
	if out, err := os.OpenFile("CONOUT$", os.O_WRONLY, 0); err == nil {
		os.Stdout, os.Stderr = out, out
	}
}
