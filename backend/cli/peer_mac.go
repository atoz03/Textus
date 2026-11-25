//go:build darwin

package cli

import (
	"net"
	"sync"

	"github.com/ebitengine/purego"
)

// getpeereid from the system's C library, loaded the first time; nil
// where it can't be.
var getpeereid = sync.OnceValue(func() func(fd int32, uid, gid *uint32) int32 {
	lib, err := purego.Dlopen("/usr/lib/libSystem.B.dylib", purego.RTLD_LAZY|purego.RTLD_GLOBAL)
	if err != nil {
		return nil
	}
	var fn func(fd int32, uid, gid *uint32) int32
	purego.RegisterLibFunc(&fn, lib, "getpeereid")
	return fn
})

// peerUID is the user on the other end of a Unix socket.
func peerUID(c net.Conn) (int, bool) {
	unix, ok := c.(*net.UnixConn)
	fn := getpeereid()
	if !ok || fn == nil {
		return 0, false
	}
	raw, err := unix.SyscallConn()
	if err != nil {
		return 0, false
	}
	var uid, gid uint32
	var status int32 = -1
	if err := raw.Control(func(fd uintptr) { status = fn(int32(fd), &uid, &gid) }); err != nil || status != 0 {
		return 0, false
	}
	return int(uid), true
}
