//go:build !windows

package programs

import "os/exec"

func quiet(*exec.Cmd) {}
