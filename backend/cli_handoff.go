package backend

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"textus/backend/cli"
	"textus/backend/plugins"
)

// Handing the terminal's paths to the app window the way the old scripts
// did - `open -a` on macOS, a detached process elsewhere - so the terminal
// gets its prompt back at once; and reaching the running app.

// absoluteArgs are the arguments that name files made absolute against
// cwd; flags and textus:// links stay as they are.
func absoluteArgs(args []string, cwd string) []string {
	out := make([]string, len(args))
	for i, a := range args {
		if strings.HasPrefix(a, "-") || plugins.IsTextusURI(a) {
			out[i] = a
		} else {
			out[i] = resolveAgainst(cwd, a)
		}
	}
	return out
}

// macBundle is the .app holding this binary (…/Textus.app/Contents/MacOS/
// Textus); false for a build outside one.
func macBundle() (string, bool) {
	exe, err := os.Executable()
	if err != nil {
		return "", false
	}
	app := filepath.Dir(filepath.Dir(filepath.Dir(exe)))
	return app, filepath.Ext(app) == ".app"
}

// selfPath is the program to start again: the AppImage (this binary lives
// in its mount, which goes away when this process ends), else this binary.
func selfPath() (string, error) {
	if image := os.Getenv("APPIMAGE"); image != "" {
		return image, nil
	}
	return os.Executable()
}

// launchApp starts the app, or wakes the running one, with args, without
// waiting for it.
func launchApp(args []string) error {
	var cmd *exec.Cmd
	if runtime.GOOS == "darwin" {
		app := "Textus"
		if bundle, ok := macBundle(); ok {
			app = bundle
		}
		cmd = exec.Command("open", append([]string{"-a", app}, args...)...)
	} else {
		self, err := selfPath()
		if err != nil {
			return err
		}
		cmd = detachedCommand(self, args)
	}
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, cliScriptEnv+"=") {
			env = append(env, kv)
		}
	}
	cmd.Env = env
	if runtime.GOOS == "darwin" {
		if err := cmd.Run(); err != nil {
			var exit *exec.ExitError
			if errors.As(err, &exit) {
				return errors.New("Textus couldn't open that.")
			}
			return fmt.Errorf("Textus couldn't be started: %v", err)
		}
		return nil
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("Textus couldn't be started: %v", err)
	}
	return cmd.Process.Release()
}

// runningApp is the app as the terminal reaches it.
type runningApp struct{}

func (runningApp) send(request cli.Request, launch bool, o *cliIO) (int, error, bool) {
	var conn cli.Conn
	if launch {
		c, err := connectOrLaunch()
		if err != nil {
			return 0, err, true
		}
		conn = c
	} else {
		c, ok := dialApp()
		if !ok {
			return 0, nil, false
		}
		conn = c
	}
	defer conn.Close()
	code, err := cli.Exchange(conn, request, func(r cli.Reply) {
		if r.Kind == "out" {
			o.text(r.Text)
			return
		}
		fmt.Fprint(o.err, r.Text)
		if !strings.HasSuffix(r.Text, "\n") {
			fmt.Fprintln(o.err)
		}
	})
	return code, err, true
}

func (runningApp) open(args []string) error { return launchApp(args) }

// dialApp is a connection to the running app; false when none listens.
func dialApp() (cli.Conn, bool) {
	path, ok := cli.Endpoint()
	if !ok {
		return nil, false
	}
	c, err := cli.Dial(path)
	return c, err == nil
}

// connectOrLaunch connects to the app, starting it and waiting for it to
// listen when it isn't running.
func connectOrLaunch() (cli.Conn, error) {
	if c, ok := dialApp(); ok {
		return c, nil
	}
	if err := launchApp(nil); err != nil {
		return nil, err
	}
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
		if c, ok := dialApp(); ok {
			return c, nil
		}
	}
	return nil, errors.New("Textus was started but didn't answer within 30 seconds.")
}
