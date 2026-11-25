//go:build windows

package backend

import "syscall"

// The pointer's button on Windows, wherever the pointer is.

const pointerTracked = true

var procGetAsyncKeyState = syscall.NewLazyDLL("user32.dll").NewProc("GetAsyncKeyState")

// leftButtonDown: the high bit of VK_LBUTTON's state is set while it is
// down.
func leftButtonDown() bool {
	r, _, _ := procGetAsyncKeyState.Call(0x01)
	return r&0x8000 != 0
}
