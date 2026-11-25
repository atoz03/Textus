// Package ssh is textus.ssh for plugins (permission ssh, desktop): a
// folder on a server read and written over SFTP, through the user's own
// ssh. A plugin reaches a host only once the user said yes to that plugin
// and host; its links close when it stops. Paths are the server's,
// absolute.
package ssh

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	"textus/backend/programs"
	"textus/backend/sftp"
)

// One SFTP link to a host, through the user's own ssh (its config with
// Include, Match and ProxyJump, its agent, keys and known_hosts) asked for
// the sftp subsystem only: no shell, no command, no forwarding. Nothing is
// answered for the user: an unknown host key or a password ssh would ask
// for ends the link with words saying what to do.

// Why a link failed, as the frontend tells the user
// (src/plugins/host/broker-ssh.ts).
const (
	UnknownHost = "ssh-unknown-host"
	NeedsLogin  = "ssh-needs-login"
)

// connectWait is how long ssh may take to reach the host and start sftp.
const connectWait = 30 * time.Second

// saidKept is how much of what ssh says is kept, to explain a failure.
const saidKept = 16 * 1024

// Link is one plugin's link to a host.
type Link struct {
	Plugin string
	SFTP   *sftp.Client
	ssh    *os.Process
	exited <-chan struct{}
}

// Close ends the link and the ssh behind it.
func (l *Link) Close() {
	_ = l.SFTP.Close()
	_ = l.ssh.Kill()
	<-l.exited
}

// ValidHost is a host as ssh's config or the user names it: never an
// option.
func ValidHost(host string) bool {
	if host == "" || len(host) > 255 || strings.HasPrefix(host, "-") {
		return false
	}
	for _, c := range host {
		if !('a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9' || strings.ContainsRune("._-@:[]%", c)) {
			return false
		}
	}
	return true
}

// Remote is a server path as given: absolute, whole.
func Remote(path string) (string, error) {
	if !strings.HasPrefix(path, "/") || strings.ContainsRune(path, 0) {
		return "", fmt.Errorf("%s isn't a path on the server.", path)
	}
	return path, nil
}

// program is the ssh that is run: the system's, on every desktop.
func program() string {
	if runtime.GOOS == "windows" {
		return "ssh.exe"
	}
	return "ssh"
}

// reason is what ssh said on its way out, as one of the codes above or its
// words.
func reason(stderr string) string {
	said := strings.ToLower(stderr)
	switch {
	case strings.Contains(said, "host key verification failed") || strings.Contains(said, "no matching host key") ||
		strings.Contains(said, "remote host identification has changed"):
		return UnknownHost + ": " + lastLine(stderr)
	case strings.Contains(said, "permission denied") || strings.Contains(said, "too many authentication failures"):
		return NeedsLogin + ": " + lastLine(stderr)
	}
	if line := lastLine(stderr); line != "" {
		return line
	}
	return "The connection closed."
}

func lastLine(text string) string {
	lines := strings.Split(text, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if line := strings.TrimSpace(lines[i]); line != "" {
			return line
		}
	}
	return ""
}

// said keeps the start of what ssh writes to standard error.
type said struct {
	mu   sync.Mutex
	text []byte
}

func (s *said) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.text) < saidKept {
		s.text = append(s.text, p...)
	}
	return len(p), nil
}

func (s *said) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return strings.ToValidUTF8(string(s.text), "�")
}

// Open is a new link to host for plugin. config replaces ~/.ssh/config
// (tests only).
func Open(plugin, host, config string) (*Link, error) {
	if !ValidHost(host) {
		return nil, fmt.Errorf("%s isn't a host name.", host)
	}
	var args []string
	if config != "" {
		args = append(args, "-F", config)
	}
	args = append(args,
		"-o", "BatchMode=yes",
		"-o", "ConnectTimeout=15",
		"-o", "ServerAliveInterval=15",
		"-o", "ClearAllForwardings=yes",
		"-T", "-s", "--", host, "sftp")
	cmd := programs.Command(context.Background(), program(), args...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	// Its output is a pipe of our own, which waiting for ssh leaves open.
	stdout, stdoutEnd, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	cmd.Stdout = stdoutEnd
	words := &said{}
	cmd.Stderr = words
	cmd.WaitDelay = time.Second
	if err := cmd.Start(); err != nil {
		stdout.Close()
		stdoutEnd.Close()
		return nil, fmt.Errorf("ssh couldn't be started: %v", err)
	}
	stdoutEnd.Close()
	exited := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		stdout.Close()
		close(exited)
	}()
	type started struct {
		client *sftp.Client
		err    error
	}
	ready := make(chan started, 1)
	go func() {
		client, err := sftp.New(stdout, stdin)
		ready <- started{client, err}
	}()
	timedOut := false
	select {
	case s := <-ready:
		if s.err == nil {
			return &Link{Plugin: plugin, SFTP: s.client, ssh: cmd.Process, exited: exited}, nil
		}
	case <-time.After(connectWait):
		timedOut = true
	}
	_ = cmd.Process.Kill()
	// Its last words arrive as it exits.
	select {
	case <-exited:
	case <-time.After(2 * time.Second):
	}
	text := words.String()
	if timedOut && text == "" {
		return nil, fmt.Errorf("%s didn't answer.", host)
	}
	return nil, errors.New(reason(text))
}
