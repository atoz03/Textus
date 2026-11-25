package cli

import (
	"net"
	"syscall"
)

// peerUID is the user on the other end of a Unix socket.
func peerUID(c net.Conn) (int, bool) {
	unix, ok := c.(*net.UnixConn)
	if !ok {
		return 0, false
	}
	raw, err := unix.SyscallConn()
	if err != nil {
		return 0, false
	}
	var cred *syscall.Ucred
	var credErr error
	if err := raw.Control(func(fd uintptr) {
		cred, credErr = syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
	}); err != nil || credErr != nil {
		return 0, false
	}
	return int(cred.Uid), true
}
