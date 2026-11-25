//go:build windows

package backend

import "syscall"

var procGetSystemMetrics = syscall.NewLazyDLL("user32.dll").NewProc("GetSystemMetrics")

// smallIconSize is the width the notification area draws its icons at on
// this display (SM_CXSMICON).
func smallIconSize() int {
	n, _, _ := procGetSystemMetrics.Call(49) // SM_CXSMICON
	return int(n)
}
