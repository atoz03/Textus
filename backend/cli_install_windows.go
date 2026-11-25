package backend

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"unsafe"
)

var procSendMessageTimeoutW = syscall.NewLazyDLL("user32.dll").NewProc("SendMessageTimeoutW")

const (
	hwndBroadcast   = 0xffff
	wmSettingChange = 0x001A
	smtoAbortIfHung = 0x0002
)

// cliPaths are the app's file name and the bin folder beside it.
func cliPaths() (string, string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", "", err
	}
	return filepath.Base(exe), filepath.Join(filepath.Dir(exe), "bin"), nil
}

// userPath is the user's PATH as it is stored, and its type: nil when
// there is none; an error when it can't be read whole, and then nothing
// writes it.
func userPath() (*string, uint32, error) {
	sub, err := syscall.UTF16PtrFromString("Environment")
	if err != nil {
		return nil, 0, err
	}
	var key syscall.Handle
	if err := syscall.RegOpenKeyEx(syscall.HKEY_CURRENT_USER, sub, 0, syscall.KEY_READ, &key); err != nil {
		if err == syscall.ERROR_FILE_NOT_FOUND {
			return nil, 0, nil
		}
		return nil, 0, err
	}
	defer syscall.RegCloseKey(key)
	name, err := syscall.UTF16PtrFromString("Path")
	if err != nil {
		return nil, 0, err
	}
	var kind, size uint32
	for {
		buf := make([]byte, size+2)
		got := uint32(len(buf))
		err := syscall.RegQueryValueEx(key, name, nil, &kind, &buf[0], &got)
		switch {
		case err == syscall.ERROR_FILE_NOT_FOUND:
			return nil, 0, nil
		case err == syscall.ERROR_MORE_DATA:
			size = got
			continue
		case err != nil:
			return nil, 0, err
		case kind != syscall.REG_SZ && kind != syscall.REG_EXPAND_SZ:
			return nil, 0, errors.New("PATH is not text.")
		}
		text, ok := registryText(buf[:got])
		if !ok {
			return nil, 0, errors.New("PATH is not text.")
		}
		return &text, kind, nil
	}
}

// setUserPath writes the user's PATH as text of kind.
func setUserPath(text string, kind uint32) error {
	sub, err := syscall.UTF16PtrFromString("Environment")
	if err != nil {
		return err
	}
	var key syscall.Handle
	r, _, _ := procRegCreateKeyExW.Call(uintptr(syscall.HKEY_CURRENT_USER), uintptr(unsafe.Pointer(sub)), 0, 0, 0,
		syscall.KEY_WRITE, 0, uintptr(unsafe.Pointer(&key)), 0)
	if r != 0 {
		return syscall.Errno(r)
	}
	defer syscall.RegCloseKey(key)
	name, err := syscall.UTF16PtrFromString("Path")
	if err != nil {
		return err
	}
	data, err := syscall.UTF16FromString(text)
	if err != nil {
		return err
	}
	r, _, _ = procRegSetValueExW.Call(uintptr(key), uintptr(unsafe.Pointer(name)), 0, uintptr(kind),
		uintptr(unsafe.Pointer(&data[0])), uintptr(len(data)*2))
	if r != 0 {
		return syscall.Errno(r)
	}
	return nil
}

func cliCommandStatus() cliStatus {
	name, bin, err := cliPaths()
	if err != nil {
		return cliMissing
	}
	onPath := func() bool {
		path, _, err := userPath()
		return err == nil && path != nil && pathHas(*path, bin)
	}
	return cliStatusOf(readCLIScript(filepath.Join(bin, "textus.cmd")), func(s string) bool {
		return s == windowsScript(name) && onPath()
	})
}

// quietCLIInstall keeps the script current, at startup. The PATH is the
// user's own: it is changed only when they ask (Settings › General).
func quietCLIInstall() {
	name, bin, err := cliPaths()
	if err != nil {
		return
	}
	script := filepath.Join(bin, "textus.cmd")
	if _, err := os.Stat(script); err == nil {
		_ = os.WriteFile(script, []byte(windowsScript(name)), 0o644)
	}
}

func installCLICommand() error {
	name, bin, err := cliPaths()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(bin, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(bin, "textus.cmd"), []byte(windowsScript(name)), 0o644); err != nil {
		return err
	}
	// Not read whole: left as it is.
	old, kind, err := userPath()
	if err != nil {
		return err
	}
	if old != nil && pathHas(*old, bin) {
		return nil
	}
	next, ok := nextPath(old, bin)
	if !ok {
		return errors.New("PATH left unchanged.")
	}
	// What it was, beside the script, before anything is written.
	if old != nil {
		if err := os.WriteFile(filepath.Join(bin, "PATH-before-textus.txt"), []byte(*old), 0o644); err != nil {
			return err
		}
	} else {
		kind = syscall.REG_EXPAND_SZ
	}
	if err := setUserPath(next, kind); err != nil {
		return err
	}
	// Read back: anything but what was meant, and the old one goes back.
	if now, _, err := userPath(); err != nil || now == nil || *now != next {
		if old != nil {
			_ = setUserPath(*old, kind)
		}
		return errors.New("PATH could not be checked; put back as it was.")
	}
	// New terminals opened from Explorer read PATH again on this; a window
	// that doesn't answer could hold it for seconds.
	go announceEnvironment()
	return nil
}

func announceEnvironment() {
	env, err := syscall.UTF16PtrFromString("Environment")
	if err != nil {
		return
	}
	procSendMessageTimeoutW.Call(hwndBroadcast, wmSettingChange, 0, uintptr(unsafe.Pointer(env)), smtoAbortIfHung, 5000, 0)
}
