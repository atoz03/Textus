//go:build !windows

package cli

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"runtime"
)

// Endpoint is where the app listens: in its own data folder on macOS, the
// session's runtime folder elsewhere.
func Endpoint() (string, bool) {
	if runtime.GOOS == "darwin" {
		home := os.Getenv("HOME")
		if home == "" {
			return "", false
		}
		return filepath.Join(home, "Library", "Application Support", AppID, "cli.sock"), true
	}
	dir := os.Getenv("XDG_RUNTIME_DIR")
	if dir == "" {
		home := os.Getenv("HOME")
		if home == "" {
			return "", false
		}
		dir = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(dir, AppID, "cli.sock"), true
}

// Dial connects to the running app: an error when none listens.
func Dial(path string) (Conn, error) { return net.Dial("unix", path) }

// Listen takes the command's connections at path, in a folder only this
// user reaches; ErrAnswered when another Textus listens there already.
func Listen(path string) (Listener, error) {
	if c, err := net.Dial("unix", path); err == nil {
		c.Close()
		return nil, ErrAnswered
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	_ = os.Chmod(dir, 0o700)
	_ = os.Remove(path)
	l, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		l.Close()
		return nil, err
	}
	return &socketListener{l: l, uid: os.Getuid()}, nil
}

type socketListener struct {
	l   net.Listener
	uid int
}

// Accept is the next connection of this user's: anyone else's is closed.
func (s *socketListener) Accept() (Conn, error) {
	for {
		c, err := s.l.Accept()
		if errors.Is(err, net.ErrClosed) {
			return nil, ErrClosed
		}
		if err != nil {
			return nil, err
		}
		if uid, ok := peerUID(c); ok && uid == s.uid {
			return c, nil
		}
		c.Close()
	}
}

func (s *socketListener) Close() error { return s.l.Close() }
