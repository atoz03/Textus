package git

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"textus/backend/programs"
)

// The user's own git, run for a plugin's call: found on the login PATH,
// told to ask for nothing, its risky settings off (safety.go), and stopped
// when it takes too long.

// How long a call may take: reading is quick; a commit runs the writer's
// hooks and signing.
const (
	readTime   = 30 * time.Second
	commitTime = 180 * time.Second
)

var (
	gitMu    sync.Mutex
	gitFound string
)

// program is the git to run: on the login PATH; on macOS /usr/bin/git only
// with the developer tools there, as it otherwise asks to install them.
// Found once; looked for again while there is none (installed since).
func program() (string, bool) {
	gitMu.Lock()
	defer gitMu.Unlock()
	if gitFound == "" {
		gitFound = findGit()
	}
	return gitFound, gitFound != ""
}

func findGit() string {
	found, ok := programs.Find("git", programs.LoginPath())
	if !ok {
		return ""
	}
	if runtime.GOOS == "darwin" && found == "/usr/bin/git" {
		if exec.Command("/usr/bin/xcode-select", "-p").Run() != nil {
			return ""
		}
	}
	return found
}

// run is git in dir, never asking for anything: its output, or what it said
// went wrong.
func run(dir string, args []string, input *string) ([]byte, error) {
	return runWithin(dir, args, input, readTime)
}

// runWithin is run stopped after limit: a hook waiting for an answer, a
// prompt that never shows.
func runWithin(dir string, args []string, input *string, limit time.Duration) ([]byte, error) {
	git, ok := program()
	if !ok {
		return nil, errors.New("Git isn't installed on this computer.")
	}
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()
	full := append([]string{"-c", "core.quotepath=off", "-c", "color.ui=false"}, quiet...)
	cmd := programs.Command(ctx, git, append(full, args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_PAGER=cat", "GIT_OPTIONAL_LOCKS=0")
	if input != nil {
		cmd.Stdin = strings.NewReader(*input)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	// What a stopped git started (a hook, an alias's shell) may hold its
	// pipes: they are let go of shortly after.
	cmd.WaitDelay = 500 * time.Millisecond
	err := cmd.Run()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return nil, errors.New("Git took too long and was stopped.")
	}
	if err != nil {
		var exited *exec.ExitError
		if !errors.As(err, &exited) {
			return nil, err
		}
		said := strings.TrimSpace(lossy(stderr.Bytes()))
		if said == "" {
			said = "git failed."
		}
		return nil, errors.New(said)
	}
	return stdout.Bytes(), nil
}

// lossy is text git wrote, what isn't UTF-8 in it replaced.
func lossy(b []byte) string { return strings.ToValidUTF8(string(b), "�") }

// joined is path as git names it from dir: a relative path under dir, an
// absolute one as it is.
func joined(dir, path string) string {
	path = filepath.FromSlash(path)
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(dir, path)
}
