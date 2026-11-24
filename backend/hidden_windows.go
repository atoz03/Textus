//go:build windows

package backend

import (
	"io/fs"
	"syscall"
)

// markedHidden: a file Windows marks hidden or system (desktop.ini).
func markedHidden(info fs.FileInfo) bool {
	if attrs, ok := info.Sys().(*syscall.Win32FileAttributeData); ok {
		return attrs.FileAttributes&(syscall.FILE_ATTRIBUTE_HIDDEN|syscall.FILE_ATTRIBUTE_SYSTEM) != 0
	}
	return false
}
