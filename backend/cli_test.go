package backend

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"textus/backend/cli"
)

// fakeApp is an app that isn't running, or one that answers every request
// the same way.
type fakeApp struct {
	running *cli.Answer
	sent    []sentRequest
	opened  [][]string
}

type sentRequest struct {
	request cli.Request
	launch  bool
}

func (f *fakeApp) send(request cli.Request, launch bool, o *cliIO) (int, error, bool) {
	f.sent = append(f.sent, sentRequest{request, launch})
	if f.running == nil {
		return 0, nil, false
	}
	o.text(f.running.Out)
	return f.running.Exit, nil, true
}

func (f *fakeApp) open(args []string) error {
	f.opened = append(f.opened, args)
	return nil
}

// runIn runs a command line: its exit code, stdout and stderr.
func runIn(cwd string, app *fakeApp, line ...string) (int, string, string) {
	var out, err strings.Builder
	code := runCLI(line, cwd, &cliIO{out: &out, err: &err}, app, "1.2.3")
	return code, out.String(), err.String()
}

func runLine(line ...string) (int, string, string) { return runIn("/", &fakeApp{}, line...) }

func TestPathsAndNothingOpenTheAppAndWordsAreCommands(t *testing.T) {
	none := func(string) bool { return false }
	for _, gui := range [][]string{nil, {"notes.md"}, {"/a/b"}, {"textus://command/x"}, {"-psn_0_1"}} {
		if classifiedCLI(gui, none) {
			t.Errorf("%q is a command", gui)
		}
	}
	for _, command := range [][]string{{"search", "x"}, {"--help"}, {"-V"}, {"serch"}} {
		if !classifiedCLI(command, none) {
			t.Errorf("%q opens the app", command)
		}
	}
	// A folder named like a word still opens; a command wins over a file
	// of its name.
	there := func(string) bool { return true }
	if classifiedCLI([]string{"Notes"}, there) || !classifiedCLI([]string{"list"}, there) {
		t.Error("a word that names a file")
	}
}

func TestHelpListsEveryCommandButTheOnesKeptForLater(t *testing.T) {
	code, out, _ := runLine("help")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	for _, c := range cli.Commands {
		line := "\n  " + c.Name + strings.Repeat(" ", 9-len(c.Name)) + " "
		if strings.Contains(out, line) != (c.Runs != cli.NotYet) {
			t.Errorf("%s in help", c.Name)
		}
	}
	if !strings.Contains(out, "Shown while Textus is running") {
		t.Error(out)
	}
	if _, again, _ := runLine("--help"); again != out {
		t.Error("--help isn't help")
	}
}

func TestHelpListsTheRunningAppsPluginCommands(t *testing.T) {
	app := &fakeApp{running: &cli.Answer{Out: "  word-count count  Counts words.\n"}}
	_, out, _ := runIn("/", app, "help")
	if !strings.Contains(out, "Plugin commands:\n  word-count count  Counts words.\n") {
		t.Error(out)
	}
	if app.sent[0].request.Command != cli.ListTools || app.sent[0].launch {
		t.Errorf("sent %+v: help never starts the app", app.sent[0])
	}
}

func TestACommandsHelpIsItsUsage(t *testing.T) {
	code, out, _ := runLine("help", "search")
	if code != 0 || !strings.HasPrefix(out, "Usage: textus search <query> [<folder>]") {
		t.Fatalf("%d %s", code, out)
	}
	for _, line := range [][]string{{"search", "--help"}, {"search", "-h"}} {
		if _, got, _ := runLine(line...); got != out {
			t.Errorf("%q: %s", line, got)
		}
	}
}

func TestVersionIsPrintedThreeWays(t *testing.T) {
	for _, flag := range []string{"version", "--version", "-V"} {
		code, out, err := runLine(flag)
		if code != 0 || out != "Textus 1.2.3\n" || err != "" {
			t.Errorf("%s: %d %q %q", flag, code, out, err)
		}
	}
}

func TestAnUnknownCommandIsAUsageErrorOnStderr(t *testing.T) {
	code, out, err := runLine("serch", "x")
	if code != 2 || out != "" || !strings.Contains(err, "'serch' is not a textus command") {
		t.Errorf("%d %q %q", code, out, err)
	}
}

func TestAPluginWordGoesToTheRunningAppWithoutStartingIt(t *testing.T) {
	app := &fakeApp{running: &cli.Answer{Out: "12 words"}}
	code, out, _ := runIn("/w", app, "word-count", "count", "--text", "a b")
	if code != 0 || out != "12 words\n" {
		t.Fatalf("%d %q", code, out)
	}
	sent := app.sent[0]
	if sent.request.Command != "plugin:word-count" || !slices.Equal(sent.request.Args, []string{"count", "--text", "a b"}) || sent.launch {
		t.Errorf("sent %+v", sent)
	}
	// The help's own form: an object after --json is the tool's arguments.
	runIn("/w", app, "word-count", "count", "--json", `{"text":"a"}`)
	if args := app.sent[1].request.Args; !slices.Equal(args, []string{"count", "--json", `{"text":"a"}`}) {
		t.Errorf("args %q", args)
	}
}

func TestAppCommandsStartTheAppAndCarryJSONAndTheFolder(t *testing.T) {
	app := &fakeApp{running: &cli.Answer{Out: "[]"}}
	if code, _, _ := runIn("/w", app, "theme", "list", "--json"); code != 0 {
		t.Fatalf("exit %d", code)
	}
	sent := app.sent[0]
	want := cli.Request{Command: "theme", Args: []string{"list"}, Cwd: "/w", JSON: true}
	if sent.request.Command != want.Command || !slices.Equal(sent.request.Args, want.Args) || sent.request.Cwd != want.Cwd || !sent.request.JSON || !sent.launch {
		t.Errorf("sent %+v", sent)
	}
}

func TestAnAppCommandWithTheAppNotThereSaysSo(t *testing.T) {
	code, _, err := runIn("/w", &fakeApp{}, "theme", "list")
	if code != 1 || !strings.Contains(err, "Textus isn't running.") {
		t.Errorf("%d %q", code, err)
	}
}

func TestOpenHandsAbsolutePathsToTheApp(t *testing.T) {
	app := &fakeApp{}
	if code, _, _ := runIn(filepath.FromSlash("/w/x"), app, "open", "./a.md", "../b.md", "/c.md", "textus://open?x"); code != 0 {
		t.Fatalf("exit %d", code)
	}
	want := []string{filepath.FromSlash("/w/x/a.md"), filepath.FromSlash("/w/b.md"), filepath.FromSlash("/c.md"), "textus://open?x"}
	if !slices.Equal(app.opened[0], want) {
		t.Errorf("opened %q", app.opened[0])
	}
}

func TestAIIsKeptForLater(t *testing.T) {
	code, _, err := runLine("ai", "hello")
	if code != 1 || !strings.Contains(err, "not available yet") {
		t.Errorf("%d %q", code, err)
	}
}

func TestFilesAreCreatedMovedListedSearchedAndRead(t *testing.T) {
	dir := t.TempDir()
	app := &fakeApp{}
	at := func(line ...string) (int, string) {
		code, out, _ := runIn(dir, app, line...)
		return code, out
	}
	if code, out := at("new", "Plan", "--title", "The plan"); code != 0 || !strings.HasSuffix(strings.TrimSpace(out), "Plan.md") {
		t.Fatalf("new: %d %q", code, out)
	}
	if code, _ := at("new", "Plan.md"); code != 1 {
		t.Error("new replaced a file")
	}
	if err := os.Mkdir(filepath.Join(dir, "done"), 0o755); err != nil {
		t.Fatal(err)
	}
	if code, _ := at("move", "Plan.md", "done"); code != 0 {
		t.Fatal("move")
	}
	writeText(t, filepath.Join(dir, "Plan.md"), "x")
	if code, _ := at("move", "Plan.md", "done"); code != 1 {
		t.Error("move replaced a file")
	}
	for _, c := range []struct {
		line []string
		want string
	}{
		{[]string{"list"}, "done/Plan.md\nPlan.md\n"},
		{[]string{"list", "--json"}, `["done/Plan.md","Plan.md"]` + "\n"},
		{[]string{"search", "THE PLAN"}, "done/Plan.md:1: # The plan\n"},
		{[]string{"search", "plan", "--json"}, `[{"line":1,"path":"done/Plan.md","text":"# The plan"}]` + "\n"},
		{[]string{"read", "done/Plan.md"}, "# The plan\n"},
	} {
		if code, out := at(c.line...); code != 0 || out != c.want {
			t.Errorf("%q: %d %q", c.line, code, out)
		}
	}
	if code, _ := at("search", "nowhere"); code != 1 {
		t.Error("a search with no match succeeded")
	}
	// A built-in command's --json is the output form, whatever follows it.
	if _, out := at("search", "--json", "{plan}"); !strings.HasPrefix(out, "[") {
		t.Errorf("search --json {plan}: %q", out)
	}
	writeText(t, filepath.Join(dir, "page.html"), "<h1>Hi</h1><p>there</p>")
	if _, out := at("read", "page.html"); out != "# Hi\n\nthere\n" {
		t.Errorf("read page.html: %q", out)
	}
}

// Help and version answer without the app: a budget far above what they
// take, but far below anything that started a window.
func TestHelpAndVersionAnswerInMilliseconds(t *testing.T) {
	started := time.Now()
	for range 100 {
		runLine("help")
		runLine("version")
	}
	if each := time.Since(started) / 200; each > 5*time.Millisecond {
		t.Errorf("%v a command", each)
	}
}

func TestARepliesTextReachesTheTerminalOnceWithOneLineBreak(t *testing.T) {
	var out strings.Builder
	o := &cliIO{out: &out, err: &strings.Builder{}}
	o.text("a\n")
	o.text("b")
	if out.String() != "a\nb\n" {
		t.Errorf("%q", out.String())
	}
}
