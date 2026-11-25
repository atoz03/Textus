//go:build !linux && !darwin && !windows

package cli

import (
	"net"
	"os"
)

// peerUID: where the system can't say, the socket's own folder (this
// user's alone) is the check.
func peerUID(net.Conn) (int, bool) { return os.Getuid(), true }
