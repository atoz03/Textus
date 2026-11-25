package backend

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"textus/backend/cli"
	"textus/backend/plugins"
)

const wordCountSchema = `{"type":"object","properties":{"text":{"type":"string"},"limit":{"type":"integer"},"exact":{"type":"boolean"}},"required":["text"]}`

// callAs runs command name as window w's page calls it.
func callAs(a *App, w *appWindow, name string, args any) (any, error) {
	raw, err := json.Marshal(args)
	if err != nil {
		return nil, err
	}
	return commands[name](&Call{ctx: context.Background(), app: a, window: w, args: raw, name: name})
}

// waitForCall is the id of the first call that waits on a window's answer.
func waitForCall(t *testing.T, waiting func() []string) string {
	t.Helper()
	for range 500 {
		if ids := waiting(); len(ids) > 0 {
			return ids[0]
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Error("nothing was sent to the window")
	return ""
}

func cliWaiting(a *App) func() []string {
	return func() []string {
		a.cli.mu.Lock()
		defer a.cli.mu.Unlock()
		var ids []string
		for id := range a.cli.waiting {
			ids = append(ids, id)
		}
		return ids
	}
}

func TestAPluginsToolsAreItsCommands(t *testing.T) {
	a := pluginApp(t)
	a.windows = map[string]*appWindow{"main": {label: "main"}, "window-2": {label: "window-2"}}
	tool := plugins.AITool{Plugin: "catvinci.word-count", Name: "count", Description: "Counts words.\nAll of them.", Parameters: jsontext.Value(wordCountSchema)}
	a.plug.offered = map[string][]plugins.AITool{"main": {tool}, "window-2": {tool}, "window-9": {{Plugin: "x.gone", Name: "z"}}}
	// Each tool once, though two windows offer it; none of a window that
	// closed.
	if got := toolLines(a.cliTools()); got != "  word-count count  Counts words.\n" {
		t.Errorf("lines: %q", got)
	}
	ctx := context.Background()
	for _, c := range []struct {
		args       []string
		exit       int
		startsWith string
	}{
		{nil, 2, "Usage: textus word-count <tool>\n\nTools:\n  word-count count"},
		{[]string{"--help"}, 0, "Usage: textus word-count <tool>"},
		{[]string{"count", "--help"}, 0, "Usage: textus word-count count [--exact <boolean>] [--limit <integer>] --text <string>"},
		{[]string{"count", "--limit", "1"}, 2, "Missing: --text. See 'textus word-count count --help'."},
	} {
		got := a.handleCLI(ctx, cli.Request{Command: cli.PluginPrefix + "word-count", Args: c.args})
		if got.Exit != c.exit || !strings.HasPrefix(got.Out+got.Err, c.startsWith) {
			t.Errorf("%q: %+v", c.args, got)
		}
	}
	for command, want := range map[string]string{
		cli.PluginPrefix + "gone": "'gone' is not a textus command or a plugin's.",
		"serch":                   "'serch' is not a textus command.",
	} {
		if got := a.handleCLI(ctx, cli.Request{Command: command}); got.Exit != 2 || !strings.HasPrefix(got.Err, want) {
			t.Errorf("%s: %+v", command, got)
		}
	}
}

func TestAToolsAnswerReachesTheTerminal(t *testing.T) {
	a := pluginApp(t)
	a.windows = map[string]*appWindow{"main": {label: "main"}}
	tool := plugins.AITool{Plugin: "catvinci.word-count", Name: "count", Parameters: jsontext.Value(wordCountSchema)}
	a.plug.offered = map[string][]plugins.AITool{"main": {tool}}
	go func() {
		id := waitForCall(t, func() []string {
			a.plug.mu.Lock()
			defer a.plug.mu.Unlock()
			var ids []string
			for id := range a.plug.waiting {
				ids = append(ids, id)
			}
			return ids
		})
		_, _ = run(a, "plugin_ai_tool_result", `{"callId":"`+id+`","text":"2 words","error":null}`)
	}()
	got := a.handleCLI(context.Background(), cli.Request{Command: "plugin:word-count", Args: []string{"count", "--text", "a b"}})
	if got != cli.OK("2 words") {
		t.Errorf("%+v", got)
	}
}

func TestACommandForAWindowWaitsForThatWindowsAnswer(t *testing.T) {
	a := pluginApp(t)
	main, other := &appWindow{label: "main"}, &appWindow{label: "window-2"}
	a.windows = map[string]*appWindow{"main": main, "window-2": other}
	if _, err := callAs(a, main, "cli_listening", map[string]bool{"on": true}); err != nil {
		t.Fatal(err)
	}
	go func() {
		id := waitForCall(t, cliWaiting(a))
		// Another window's answer is not this request's.
		_, _ = callAs(a, other, "cli_respond", map[string]any{"id": id, "out": "no", "err": "", "exit": 0})
		_, _ = callAs(a, main, "cli_respond", map[string]any{"id": id, "out": "dark\n", "err": "", "exit": 0})
	}()
	if got := a.handleCLI(context.Background(), cli.Request{Command: "theme", Args: []string{"list"}}); got != cli.OK("dark\n") {
		t.Errorf("%+v", got)
	}
	// A window that closes ends the request it had at once.
	go func() {
		waitForCall(t, cliWaiting(a))
		a.cliWindowGone("main")
	}()
	if got := a.handleCLI(context.Background(), cli.Request{Command: "plugin", Args: []string{"list"}}); got.Exit != 1 || got.Err != "The Textus window closed." {
		t.Errorf("%+v", got)
	}
	a.cli.mu.Lock()
	defer a.cli.mu.Unlock()
	if len(a.cli.listening) != 0 || len(a.cli.waiting) != 0 {
		t.Errorf("left: %v %v", a.cli.listening, a.cli.waiting)
	}
}

func TestSkillListPrintsTheSkills(t *testing.T) {
	noTrash(t)
	a := &App{dirs: Dirs{Config: t.TempDir()}, defaults: map[string]bool{"textusAi": true}}
	if _, err := run(a, "save_written_skill", `{"text":"---\nname: weekly\ndescription: Plans the week.\n---\nWrite it.\n","modes":["write"],"confirmed":false}`); err != nil {
		t.Fatal(err)
	}
	if _, err := run(a, "set_skill_off", `{"name":"weekly","source":"global","off":true}`); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if got := a.handleCLI(ctx, cli.Request{Command: "skill"}); got.Exit != 2 || got.Err != "Usage: textus skill list" {
		t.Errorf("no list: %+v", got)
	}
	text := a.handleCLI(ctx, cli.Request{Command: "skill", Args: []string{"list"}})
	if text.Exit != 0 || !strings.Contains(text.Out, "weekly") || !strings.Contains(text.Out, "  Plans the week. (off)\n") {
		t.Errorf("list: %+v", text)
	}
	listed := a.handleCLI(ctx, cli.Request{Command: "skill", Args: []string{"list"}, JSON: true})
	var skills []listedSkill
	if err := json.Unmarshal([]byte(listed.Out), &skills); err != nil {
		t.Fatalf("%s: %v", listed.Out, err)
	}
	found := false
	for _, s := range skills {
		found = found || s == listedSkill{Name: "weekly", Description: "Plans the week.", Off: true}
	}
	if !found {
		t.Errorf("json: %s", listed.Out)
	}
}

func exportFormats(t *testing.T) []pandocFormat {
	t.Helper()
	raw, err := os.ReadFile("../src/registry/export-formats.json")
	if err != nil {
		t.Fatal(err)
	}
	var formats []pandocFormat
	if err := json.Unmarshal(raw, &formats); err != nil {
		t.Fatal(err)
	}
	return formats
}

func TestEveryExportFormatHasItsExtension(t *testing.T) {
	formats := exportFormats(t)
	if len(formats) < 9 {
		t.Errorf("%d formats", len(formats))
	}
	for format, want := range map[string]string{"latex": "tex", "pdf": "pdf", "html": "html"} {
		if ext, ok := exportExtension(formats, format); !ok || ext != want {
			t.Errorf("%s: %s", format, ext)
		}
	}
	if _, ok := exportExtension(formats, "mobi"); ok {
		t.Error("mobi")
	}
	for path, want := range map[string]string{"/x/a.md": "/x/a.pdf", "/x/a": "/x/a.pdf", "/x/a.tar.gz": "/x/a.tar.pdf", "/x/.md": "/x/.md.pdf"} {
		if got := withExtension(filepath.FromSlash(path), "pdf"); got != filepath.FromSlash(want) {
			t.Errorf("%s: %s", path, got)
		}
	}
}

func TestAnExportIsCheckedBeforeTheAppIsAsked(t *testing.T) {
	dir := t.TempDir()
	writeText(t, filepath.Join(dir, "a.md"), "# A")
	formats := exportFormats(t)
	request := func(args ...string) cli.Request { return cli.Request{Command: "export", Args: args, Cwd: dir} }
	job, err := exportJob(request("./a.md", "--to", "latex"), formats)
	if err != nil || job.file != filepath.Join(dir, "a.md") || job.out != filepath.Join(dir, "a.tex") || job.format != "latex" {
		t.Errorf("%+v %v", job, err)
	}
	if job, err := exportJob(request("a.md", "--to", "html", "--out", "out/b.html"), formats); err != nil || job.out != filepath.Join(dir, "out", "b.html") {
		t.Errorf("--out: %+v %v", job, err)
	}
	for _, args := range [][]string{{"a.md", "--to", "doc"}, {"a.md"}, {"b.md", "--to", "pdf"}, {"a.md", "b.md", "--to", "pdf"}, {"a.md", "--to"}} {
		if _, err := exportJob(request(args...), formats); err == nil {
			t.Errorf("%q passed", args)
		}
	}
	writeText(t, filepath.Join(dir, "a.pdf"), "")
	if _, err := exportJob(request("a.md", "--to", "pdf"), formats); err == nil || !strings.Contains(err.Error(), "already there") {
		t.Errorf("replaced: %v", err)
	}
	// Checked before any window is asked.
	a := pluginApp(t)
	if got := a.handleCLI(context.Background(), request("a.md", "--to", "pdf")); got.Exit != 2 {
		t.Errorf("%+v", got)
	}
}

func TestATerminalsExportSavesWhereItSaidOnce(t *testing.T) {
	a := pluginApp(t)
	target := filepath.Join(t.TempDir(), "a.pdf")
	a.setCLIExportTarget(&target)
	got, err := run(a, "export_save_dialog", `{"defaultName":"a.pdf","filterName":"PDF","ext":"pdf"}`)
	if want, _ := json.Marshal(target); err != nil || got != string(want) {
		t.Errorf("%s %v", got, err)
	}
	if _, again := a.takeCLIExportTarget(); again || !a.cliExportTaken() {
		t.Error("the target was taken twice")
	}
	// The terminal prints where it went: no file manager window.
	args, _ := json.Marshal(map[string]string{"path": target})
	if got, err := run(a, "reveal_in_dir", string(args)); err != nil || got != "null" {
		t.Errorf("%s %v", got, err)
	}
	a.setCLIExportTarget(nil)
	if a.isCLIExportTarget(target) || a.cliExportTaken() {
		t.Error("the target outlived its export")
	}
}

func TestAnExportIsDoneOnceItsFileStopsGrowing(t *testing.T) {
	a := pluginApp(t)
	out := filepath.Join(t.TempDir(), "a.pdf")
	writeText(t, out, "%PDF")
	if !a.cliWritten(out) {
		t.Error("a written file wasn't")
	}
}

func TestAppCLICommandsWithNoWindow(t *testing.T) {
	a := pluginApp(t)
	for name, args := range map[string]string{
		"cli_listening": `{"on":true}`,
		"cli_respond":   `{"id":"cli-0","out":"","err":"","exit":0}`,
	} {
		if got, err := run(a, name, args); err != nil || got != "null" {
			t.Errorf("%s: %s %v", name, got, err)
		}
	}
	if len(a.cli.listening) != 0 {
		t.Errorf("listening: %v", a.cli.listening)
	}
	if got, err := run(a, "cli_command_status", `{}`); err != nil || !strings.Contains(`"installed" "outdated" "missing"`, got) {
		t.Errorf("status: %s %v", got, err)
	}
}

func TestTheTerminalReachesTheRunningApp(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the pipe is named for the user: a Textus running here would answer")
	}
	dir := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", dir)
	t.Setenv("HOME", dir)
	path, _ := cli.Endpoint()
	if len(path) > 100 {
		t.Skipf("%s is too long a path for a socket", path)
	}
	a := pluginApp(t)
	a.windows = map[string]*appWindow{"main": {label: "main"}}
	a.plug.offered = map[string][]plugins.AITool{"main": {{Plugin: "catvinci.word-count", Name: "count", Description: "Counts words.", Parameters: jsontext.Value(wordCountSchema)}}}
	listener, err := cli.Listen(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	go a.serveCLI(listener)
	line := func(args ...string) (int, string, string) {
		var out, errOut strings.Builder
		code := runCLI(args, dir, &cliIO{out: &out, err: &errOut}, runningApp{}, "1.2.3")
		return code, out.String(), errOut.String()
	}
	if code, out, _ := line("help"); code != 0 || !strings.Contains(out, "Plugin commands:\n  word-count count  Counts words.\n\n") {
		t.Errorf("help: %d %s", code, out)
	}
	if code, out, errOut := line("word-count", "count"); code != 2 || out != "" || errOut != "Missing: --text. See 'textus word-count count --help'.\n" {
		t.Errorf("a tool's usage error: %d %q %q", code, out, errOut)
	}
	if code, out, _ := line("word-count", "count", "--help"); code != 0 || !strings.HasPrefix(out, "Usage: textus word-count count") {
		t.Errorf("a tool's help: %d %q", code, out)
	}
}
