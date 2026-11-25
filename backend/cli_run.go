package backend

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"textus/backend/cli"
)

// The `textus` terminal command, run by the app's own binary before MyGo,
// a window or a plugin starts: a command runs here and the process exits;
// paths and a bare `textus` go on to the app as they always did.

// cliScriptEnv is set by the installed `textus` script, so the binary knows
// a terminal started it rather than the system.
const cliScriptEnv = "TEXTUS_CLI"

// classifiedCLI is whether a command line is a command: one of the
// table's, a help or version flag, or a bare word that names no file here.
// Anything else is what a file manager or the old script passed, and opens
// in the app.
func classifiedCLI(args []string, exists func(string) bool) bool {
	if len(args) == 0 {
		return false
	}
	first := args[0]
	switch first {
	case "-h", "--help", "-V", "--version":
		return true
	}
	if _, ok := cli.Find(first); ok {
		return true
	}
	return first != "" && !strings.HasPrefix(first, "-") && !strings.ContainsAny(first, `/\.:`) && !exists(first)
}

// resolveAgainst is arg against cwd with . and .. folded, so ./a.md names
// the same file, and the same open tab, as its full path.
func resolveAgainst(cwd, arg string) string {
	if filepath.IsAbs(arg) {
		return filepath.Clean(arg)
	}
	return filepath.Join(cwd, arg)
}

// cliIO is where a command's output goes.
type cliIO struct {
	out, err io.Writer
}

func (o *cliIO) line(text string) { fmt.Fprintln(o.out, text) }

// text is text with one line break at its end.
func (o *cliIO) text(text string) {
	fmt.Fprint(o.out, text)
	if !strings.HasSuffix(text, "\n") {
		fmt.Fprintln(o.out)
	}
}

func (o *cliIO) warn(text string) { fmt.Fprintln(o.err, "textus: "+text) }

// cliCtx is what a command runs against.
type cliCtx struct {
	cwd  string
	json bool
}

// cliApp is the running app, as a command reaches it.
type cliApp interface {
	// send sends a request, starting the app first when launch; running
	// is false when it isn't running and wasn't to be started.
	send(request cli.Request, launch bool, o *cliIO) (code int, err error, running bool)
	// open opens paths in it, or brings it to the front.
	open(args []string) error
}

// RunCLI runs the command line when it is a command, and says so with its
// exit code; false to start the app as usual.
func RunCLI(shared Shared) (int, bool) {
	// `mygo generate` runs the app to read its commands.
	if os.Getenv("MYGO_GENERATE") != "" {
		return 0, false
	}
	args := os.Args[1:]
	cwd, _ := os.Getwd()
	fromScript := os.Getenv(cliScriptEnv) != ""
	exists := func(a string) bool {
		_, err := os.Stat(filepath.Join(cwd, a))
		return err == nil
	}
	if !classifiedCLI(args, exists) {
		if !fromScript {
			return 0, false
		}
		if err := launchApp(absoluteArgs(args, cwd)); err != nil {
			fmt.Fprintln(os.Stderr, "textus: "+err.Error())
			return 1, true
		}
		return 0, true
	}
	attachConsole()
	loadErrorTable(shared.ErrorTable)
	out := bufio.NewWriter(os.Stdout)
	code := runCLI(args, cwd, &cliIO{out: out, err: os.Stderr}, runningApp{}, shared.Version)
	_ = out.Flush()
	return code, true
}

// takeOption takes `--name value` or `--name=value` out of args.
func takeOption(args *[]string, name string) (*string, error) {
	at := slices.IndexFunc(*args, func(a string) bool { return a == name || strings.HasPrefix(a, name+"=") })
	if at < 0 {
		return nil, nil
	}
	arg := (*args)[at]
	*args = slices.Delete(*args, at, at+1)
	if value, ok := strings.CutPrefix(arg, name+"="); ok {
		return &value, nil
	}
	if at < len(*args) {
		value := (*args)[at]
		*args = slices.Delete(*args, at, at+1)
		return &value, nil
	}
	return nil, fmt.Errorf("%s needs a value.", name)
}

func takeFlag(args *[]string, names ...string) bool {
	before := len(*args)
	*args = slices.DeleteFunc(*args, func(a string) bool { return slices.Contains(names, a) })
	return len(*args) != before
}

// takeOutputJSON is whether --json asks for JSON output, taken out of args
// - but not, for a plugin's tool, its `--json '{...}'` arguments.
func takeOutputJSON(args *[]string, plugin bool) bool {
	found := false
	for i := 0; i < len(*args); {
		object := plugin && i+1 < len(*args) && strings.HasPrefix((*args)[i+1], "{")
		if (*args)[i] == "--json" && !object {
			*args = slices.Delete(*args, i, i+1)
			found = true
		} else {
			i++
		}
	}
	return found
}

// runCLI runs one command line (without the program's name): the exit
// code.
func runCLI(args []string, cwd string, o *cliIO, app cliApp, version string) int {
	if len(args) == 0 {
		return 0
	}
	first, rest := args[0], slices.Clone(args[1:])
	switch first {
	case "-h", "--help":
		return cliHelp(firstOf(rest), o, app, version)
	case "-V", "--version":
		o.line(cli.VersionLine(version))
		return 0
	}
	helpAsked := takeFlag(&rest, "-h", "--help")
	command, known := cli.Find(first)
	ctx := cliCtx{cwd: cwd, json: takeOutputJSON(&rest, !known)}
	if !known {
		return pluginCommand(first, rest, ctx, helpAsked, o, app)
	}
	if helpAsked {
		o.text(cli.Usage(command))
		return 0
	}
	var code int
	var err error
	switch {
	case command.Name == "help":
		return cliHelp(firstOf(rest), o, app, version)
	case command.Name == "version":
		o.line(cli.VersionLine(version))
		return 0
	case command.Runs == cli.NotYet:
		err = fmt.Errorf("'textus %s' is not available yet.", command.Name)
	case command.Runs == cli.Open:
		err = app.open(absoluteArgs(rest, cwd))
	case command.Runs == cli.App:
		code, err = toApp(command.Name, rest, ctx, true, o, app)
	default:
		code, err = runHere(command.Name, ctx, rest, o)
	}
	if err != nil {
		o.warn(err.Error())
		return 1
	}
	return code
}

func firstOf(args []string) *string {
	if len(args) == 0 {
		return nil
	}
	return &args[0]
}

func toApp(name string, args []string, ctx cliCtx, launch bool, o *cliIO, app cliApp) (int, error) {
	request := cli.Request{Command: name, Args: args, Cwd: ctx.cwd, JSON: ctx.json}
	code, err, running := app.send(request, launch, o)
	if !running {
		return 0, fmt.Errorf("Textus isn't running.")
	}
	return code, err
}

// pluginCommand runs a word that is no command of the table's: a plugin's
// tool while the app runs, else a mistake.
func pluginCommand(word string, args []string, ctx cliCtx, helpAsked bool, o *cliIO, app cliApp) int {
	if helpAsked {
		args = append(args, "--help")
	}
	request := cli.Request{Command: cli.PluginPrefix + word, Args: args, Cwd: ctx.cwd, JSON: ctx.json}
	code, err, running := app.send(request, false, o)
	switch {
	case !running:
		o.warn(fmt.Sprintf("'%s' is not a textus command. See 'textus help'. (Plugins' commands work while Textus is running.)", word))
		return 2
	case err != nil:
		o.warn(err.Error())
		return 1
	}
	return code
}

// cliHelp is `textus help`, or one command's usage.
func cliHelp(topic *string, o *cliIO, app cliApp, version string) int {
	if topic != nil {
		if command, ok := cli.Find(*topic); ok && command.Runs != cli.NotYet {
			o.text(cli.Usage(command))
			return 0
		}
		return pluginCommand(*topic, nil, cliCtx{}, true, o, app)
	}
	var plugins strings.Builder
	code, err, running := app.send(cli.Request{Command: cli.ListTools}, false, &cliIO{out: &plugins, err: io.Discard})
	listed := strings.TrimRight(plugins.String(), " \t\r\n")
	switch {
	case !running:
		listed = "  (Shown while Textus is running: start it with 'textus'.)"
	case err != nil || code != 0:
		listed = "  (Textus didn't say which plugins add commands.)"
	case listed == "":
		listed = "  (None: no plugin that is on adds one.)"
	}
	o.text(cli.Overview(version, listed))
	return 0
}
