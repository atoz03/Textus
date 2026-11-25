package cli

import (
	"os"
	"strings"
	"sync"
	"syscall"
	"unicode"
	"unsafe"
)

// Endpoint is where the app listens: a pipe named for the user.
func Endpoint() (string, bool) {
	safe := strings.Map(func(c rune) rune {
		if unicode.IsLetter(c) || unicode.IsDigit(c) {
			return c
		}
		return '_'
	}, os.Getenv("USERNAME"))
	return `\\.\pipe\textus-cli-` + safe, true
}

// Dial connects to the running app: an error when none listens.
func Dial(name string) (Conn, error) { return os.OpenFile(name, os.O_RDWR, 0) }

var (
	procCreateNamedPipeW    = syscall.NewLazyDLL("kernel32.dll").NewProc("CreateNamedPipeW")
	procConnectNamedPipe    = syscall.NewLazyDLL("kernel32.dll").NewProc("ConnectNamedPipe")
	procDisconnectNamedPipe = syscall.NewLazyDLL("kernel32.dll").NewProc("DisconnectNamedPipe")
)

const (
	pipeAccessDuplex          = 0x3
	fileFlagFirstPipeInstance = 0x00080000
	pipeRejectRemoteClients   = 0x8
	pipeUnlimitedInstances    = 255
	errorPipeConnected        = 535
	pipeBuffer                = 64 << 10
)

// createPipe makes an instance of the pipe; the first fails when another
// Textus has the pipe already. Remote clients are turned away, and the
// pipe's default security leaves writing to this user.
func createPipe(name string, first bool) (syscall.Handle, error) {
	path, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return syscall.InvalidHandle, err
	}
	mode := uintptr(pipeAccessDuplex)
	if first {
		mode |= fileFlagFirstPipeInstance
	}
	h, _, callErr := procCreateNamedPipeW.Call(uintptr(unsafe.Pointer(path)), mode, pipeRejectRemoteClients,
		pipeUnlimitedInstances, pipeBuffer, pipeBuffer, 0, 0)
	if syscall.Handle(h) == syscall.InvalidHandle {
		return syscall.InvalidHandle, callErr
	}
	return syscall.Handle(h), nil
}

// Listen takes the command's connections on the pipe; ErrAnswered when
// another Textus has it.
func Listen(name string) (Listener, error) {
	h, err := createPipe(name, true)
	if err != nil {
		return nil, ErrAnswered
	}
	return &pipeListener{name: name, next: h}, nil
}

type pipeListener struct {
	mu   sync.Mutex
	name string
	next syscall.Handle
}

// Accept waits for a client on the waiting instance, then makes the next.
func (l *pipeListener) Accept() (Conn, error) {
	l.mu.Lock()
	h := l.next
	l.mu.Unlock()
	if h == syscall.InvalidHandle {
		return nil, ErrClosed
	}
	r, _, err := procConnectNamedPipe.Call(uintptr(h), 0)
	connected := r != 0 || err == syscall.Errno(errorPipeConnected)
	next, nextErr := createPipe(l.name, false)
	l.mu.Lock()
	l.next = next
	l.mu.Unlock()
	if !connected {
		syscall.CloseHandle(h)
		if nextErr != nil {
			return nil, nextErr
		}
		return nil, err
	}
	return &pipeConn{File: os.NewFile(uintptr(h), l.name), h: h}, nil
}

func (l *pipeListener) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.next == syscall.InvalidHandle {
		return nil
	}
	err := syscall.CloseHandle(l.next)
	l.next = syscall.InvalidHandle
	return err
}

// pipeConn is a connected instance: closed once the client has read all.
type pipeConn struct {
	*os.File
	h syscall.Handle
}

func (c *pipeConn) Close() error {
	_ = c.File.Sync()
	procDisconnectNamedPipe.Call(uintptr(c.h))
	return c.File.Close()
}
