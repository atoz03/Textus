// Package programs starts the programs Textus runs for the user - git, MCP
// servers, external agents - from the PATH a terminal would have, with no
// console window flashing up on Windows.
package programs

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"
)

// stderrKept is how much of a program's standard error is kept, to explain
// a failure.
const stderrKept = 4_000

var loginPath = sync.OnceValue(func() string {
	var dirs []string
	add := func(list string) {
		for _, dir := range filepath.SplitList(list) {
			if dir != "" && !slices.Contains(dirs, dir) {
				dirs = append(dirs, dir)
			}
		}
	}
	add(shellPath())
	add(os.Getenv("PATH"))
	home := os.Getenv("HOME")
	if home == "" {
		home = os.Getenv("USERPROFILE")
	}
	if home != "" {
		for _, sub := range []string{".local/bin", ".cargo/bin", ".bun/bin", ".deno/bin", ".volta/bin"} {
			add(filepath.Join(home, filepath.FromSlash(sub)))
		}
	}
	if runtime.GOOS != "windows" {
		add("/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin")
	}
	return strings.Join(dirs, string(filepath.ListSeparator))
})

// LoginPath is the PATH a terminal would have, with the usual tool folders
// after it: an app opened from the Dock lacks a terminal's PATH, so the
// login shell is asked once. The first call can take seconds.
func LoginPath() string { return loginPath() }

// shellPath is what the login shell says PATH is, within a few seconds; ""
// on Windows or when it doesn't say.
func shellPath() string {
	if runtime.GOOS == "windows" {
		return ""
	}
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/zsh"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, shell, "-ilc", `printf '\n%s\n' "$PATH"`)
	// A profile's children may hold the pipe: let go of it at the timeout.
	cmd.WaitDelay = 500 * time.Millisecond
	out, _ := cmd.Output()
	if ctx.Err() != nil {
		return ""
	}
	// The last line with a path: a profile may print greetings before it.
	lines := strings.Split(string(out), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.Contains(lines[i], "/") {
			return strings.TrimRight(lines[i], "\r")
		}
	}
	return ""
}

// given is whether program names a file by its path rather than a program
// to look for.
func given(program string) bool {
	if runtime.GOOS == "windows" {
		return strings.ContainsAny(program, `/\`)
	}
	return strings.Contains(program, "/")
}

func isFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

// Find is program as a file to run: a path as given, or the first match on
// path, with Windows's extensions - a runnable file first: Node puts an
// extensionless npx shell script beside npx.cmd, which Windows can't
// start.
func Find(program, path string) (string, bool) {
	if given(program) {
		return program, isFile(program)
	}
	extensions := []string{""}
	if runtime.GOOS == "windows" {
		extensions = []string{".exe", ".cmd", ".bat", ""}
	}
	for _, dir := range filepath.SplitList(path) {
		if dir == "" {
			continue
		}
		for _, ext := range extensions {
			if candidate := filepath.Join(dir, program+ext); isFile(candidate) {
				return candidate, true
			}
		}
	}
	return "", false
}

// Command is program run with args, quiet: no console window on Windows.
func Command(ctx context.Context, program string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, program, args...)
	quiet(cmd)
	return cmd
}

// Tail keeps the last few thousand characters a program writes to standard
// error, line by line, to explain a failure.
type Tail struct {
	mu      sync.Mutex
	text    string
	partial []byte
}

// Write takes what the program wrote.
func (t *Tail) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.partial = append(t.partial, p...)
	for {
		i := bytes.IndexByte(t.partial, '\n')
		if i < 0 {
			break
		}
		t.add(t.partial[:i])
		t.partial = t.partial[i+1:]
	}
	t.partial = slices.Clone(t.partial)
	return len(p), nil
}

func (t *Tail) add(line []byte) {
	t.text += strings.ToValidUTF8(strings.TrimSuffix(string(line), "\r"), "�") + "\n"
	if len(t.text) > stderrKept {
		cut := len(t.text) - stderrKept
		for cut < len(t.text) && t.text[cut]&0xC0 == 0x80 {
			cut++
		}
		t.text = t.text[cut:]
	}
}

// ended keeps a last line that had no line break.
func (t *Tail) ended() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.partial) > 0 {
		t.add(t.partial)
		t.partial = nil
	}
}

// String is what was kept.
func (t *Tail) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.text
}

// Piped is a program started with its three pipes, as MCP servers and
// external agents both run.
type Piped struct {
	Cmd   *exec.Cmd
	Stdin io.WriteCloser
	// Stdout is read to its end whenever the program ends.
	Stdout io.ReadCloser
	// Stderr is the tail of what it wrote to standard error, filled as it
	// runs.
	Stderr *Tail
	waited chan struct{}
	err    error
}

// Wait waits for the program to end, for every caller.
func (p *Piped) Wait() error {
	<-p.waited
	return p.err
}

// Done is closed when the program has ended.
func (p *Piped) Done() <-chan struct{} { return p.waited }

// Kill ends the program.
func (p *Piped) Kill() {
	if p.Cmd.Process != nil {
		_ = p.Cmd.Process.Kill()
	}
}

// ExitCode is the program's exit code once it ended; nil while it runs or
// when a signal ended it.
func (p *Piped) ExitCode() *int {
	select {
	case <-p.waited:
	default:
		return nil
	}
	if p.Cmd.ProcessState == nil {
		return nil
	}
	code := p.Cmd.ProcessState.ExitCode()
	if code < 0 {
		return nil
	}
	return &code
}

// Start starts program, found on the login PATH, in cwd when that is a
// folder, with env added to the app's own environment.
func Start(program string, args []string, env [][2]string, cwd string) (*Piped, error) {
	path := LoginPath()
	resolved, ok := Find(program, path)
	if !ok {
		return nil, fmt.Errorf("`%s` isn't installed, or isn't on the PATH Textus sees (%s)", program, path)
	}
	cmd := exec.Command(resolved, args...)
	quiet(cmd)
	// Node (most agents and servers are) trusts the system's certificates
	// too, as the app does: a company's own root among them.
	cmd.Env = append(os.Environ(), "PATH="+path, "NODE_USE_SYSTEM_CA=1")
	for _, kv := range env {
		cmd.Env = append(cmd.Env, kv[0]+"="+kv[1])
	}
	if info, err := os.Stat(cwd); cwd != "" && err == nil && info.IsDir() {
		cmd.Dir = cwd
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	// Its standard output is a pipe of our own, which waiting for the
	// program doesn't close: read to its end, nothing it said is lost.
	stdout, stdoutEnd, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	cmd.Stdout = stdoutEnd
	tail := &Tail{}
	cmd.Stderr = tail
	// What it started may hold standard error after it ends.
	cmd.WaitDelay = time.Second
	if err := cmd.Start(); err != nil {
		stdout.Close()
		stdoutEnd.Close()
		return nil, fmt.Errorf("couldn't start `%s`: %v", program, err)
	}
	stdoutEnd.Close()
	p := &Piped{Cmd: cmd, Stdin: stdin, Stdout: stdout, Stderr: tail, waited: make(chan struct{})}
	go func() {
		p.err = cmd.Wait()
		tail.ended()
		close(p.waited)
	}()
	return p, nil
}
