package backend

import (
	"context"
	"os"
	"strings"
	"syscall"
	"unsafe"

	"github.com/egoist/mygo"
)

var (
	procRegCreateKeyExW   = syscall.NewLazyDLL("advapi32.dll").NewProc("RegCreateKeyExW")
	procRegSetValueExW    = syscall.NewLazyDLL("advapi32.dll").NewProc("RegSetValueExW")
	procAssocQueryStringW = syscall.NewLazyDLL("shlwapi.dll").NewProc("AssocQueryStringW")
	procSHChangeNotify    = syscall.NewLazyDLL("shell32.dll").NewProc("SHChangeNotify")
)

const (
	assocStrExecutable = 2
	shcneAssocChanged  = 0x08000000
)

// mdHandler is the program Explorer opens a .md file with, the user's
// choice included.
func mdHandler() (string, bool) {
	ext, _ := syscall.UTF16PtrFromString(".md")
	buf := make([]uint16, 1024)
	size := uint32(len(buf))
	r, _, _ := procAssocQueryStringW.Call(0, assocStrExecutable, uintptr(unsafe.Pointer(ext)), 0,
		uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)))
	if r != 0 {
		return "", false
	}
	return syscall.UTF16ToString(buf), true
}

func editorStatus(context.Context) defaultEditorStatus {
	exe, err := os.Executable()
	handler, ok := mdHandler()
	return defaultEditorStatus{
		Supported:        true,
		IsDefault:        err == nil && ok && strings.EqualFold(handler, exe),
		ChooseInSettings: true,
	}
}

// setRegistryValue writes v under HKEY_CURRENT_USER, making its key.
func setRegistryValue(v registryValue) error {
	path, err := syscall.UTF16PtrFromString(v.key)
	if err != nil {
		return err
	}
	var key syscall.Handle
	r, _, _ := procRegCreateKeyExW.Call(uintptr(syscall.HKEY_CURRENT_USER), uintptr(unsafe.Pointer(path)), 0, 0, 0,
		syscall.KEY_WRITE, 0, uintptr(unsafe.Pointer(&key)), 0)
	if r != 0 {
		return syscall.Errno(r)
	}
	defer syscall.RegCloseKey(key)
	name, err := syscall.UTF16PtrFromString(v.name)
	if err != nil {
		return err
	}
	if v.empty {
		r, _, _ = procRegSetValueExW.Call(uintptr(key), uintptr(unsafe.Pointer(name)), 0, syscall.REG_NONE, 0, 0)
	} else {
		data, err := syscall.UTF16FromString(v.text)
		if err != nil {
			return err
		}
		r, _, _ = procRegSetValueExW.Call(uintptr(key), uintptr(unsafe.Pointer(name)), 0, syscall.REG_SZ,
			uintptr(unsafe.Pointer(&data[0])), uintptr(len(data)*2))
	}
	if r != 0 {
		return syscall.Errno(r)
	}
	return nil
}

// makeDefaultEditor lists Textus for Markdown files and opens Default apps
// at its entry, where the user chooses.
func makeDefaultEditor(context.Context) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	for _, v := range windowsRegistryValues(exe) {
		if err := setRegistryValue(v); err != nil {
			return err
		}
	}
	// Explorer caches file types: this makes "Open with" read them again.
	procSHChangeNotify.Call(shcneAssocChanged, 0, 0, 0)
	return mygo.Shell.OpenExternal(defaultAppsSettings)
}
