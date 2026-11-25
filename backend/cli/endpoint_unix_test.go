//go:build !windows

package cli

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestTheSocketIsThisUsersAlone(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app", "cli.sock")
	if len(path) > 100 {
		t.Skipf("%s is too long a path for a socket", path)
	}
	l, err := Listen(path)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	for at, want := range map[string]os.FileMode{path: 0o600, filepath.Dir(path): 0o700} {
		if info, err := os.Stat(at); err != nil || info.Mode().Perm() != want {
			t.Errorf("%s: %v %v", at, info.Mode(), err)
		}
	}
	go func() {
		c, err := l.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		_ = Serve(c, func(r Request) Answer { return OK("hi " + r.Command) })
	}()
	c, err := Dial(path)
	if err != nil {
		t.Fatal(err)
	}
	var got string
	code, err := Exchange(c, Request{Command: "x"}, func(r Reply) { got = r.Text })
	c.Close()
	if err != nil || code != 0 || got != "hi x" {
		t.Errorf("%d %q %v", code, got, err)
	}
	// A second Textus finds the first one answering, and leaves it be.
	if _, err := Listen(path); !errors.Is(err, ErrAnswered) {
		t.Errorf("a second listener: %v", err)
	}
	l.Close()
	if _, err := l.Accept(); !errors.Is(err, ErrClosed) {
		t.Errorf("accepted after closing: %v", err)
	}
}
