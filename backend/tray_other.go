//go:build !windows

package backend

// smallIconSize matters on Windows only (tray_windows.go).
func smallIconSize() int { return 0 }
