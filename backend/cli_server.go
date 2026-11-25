package backend

import (
	"cmp"
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"log"
	"maps"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"textus/backend/ai"
	"textus/backend/cli"
	"textus/backend/plugins"
)

// The running app's side of `textus`: a listener started as the app is
// ready, which answers plugins' tools and skills itself and hands the rest
// (commands, plugins, themes, export) to a window, which answers with
// cli_respond.

const (
	// cliWindowAnswersWithin is how long a window may take: turning a
	// plugin on waits for the user to grant its permissions.
	cliWindowAnswersWithin = 600 * time.Second
	// cliWindowReadyWithin is how long a window has to open and start
	// listening.
	cliWindowReadyWithin = 30 * time.Second
	// cliExportStartsWithin is how long an export has to reach its save
	// dialog.
	cliExportStartsWithin = 30 * time.Second
)

// cliBridge is what the app keeps of the `textus` requests.
type cliBridge struct {
	mu sync.Mutex
	// listening are the windows whose page takes requests, by label.
	listening []string
	// waiting are the requests handed to a window, by id.
	waiting map[string]cliWait
	// exportTarget is where a `textus export` writes, waiting for its save
	// dialog; exportTaken once the dialog took it.
	exportTarget *string
	exportTaken  bool
	// exporting lets one `textus export` run at a time: the target is one
	// slot.
	exporting sync.Mutex
}

// cliWait is a request a window has: closing answer ends the wait.
type cliWait struct {
	window string
	answer chan cli.Answer
}

// cliRequestEvent is a request for a window, sent as "cli-request".
// Mirrors CliRequest in src/app/cli-requests.ts.
type cliRequestEvent struct {
	ID      string   `json:"id"`
	Command string   `json:"command"`
	Args    []string `json:"args"`
	Cwd     string   `json:"cwd"`
	JSON    bool     `json:"json"`
}

// cliCalls numbers the requests and tool calls the command makes.
var cliCalls atomic.Uint64

func nextCLICall() string { return "cli-" + strconv.FormatUint(cliCalls.Add(1)-1, 10) }

// listenForCLI answers the `textus` command while the app runs, unless
// another Textus (a dev build beside an installed one) answers it already.
func (a *App) listenForCLI() {
	path, ok := cli.Endpoint()
	if !ok {
		return
	}
	listener, err := cli.Listen(path)
	if err != nil {
		if !errors.Is(err, cli.ErrAnswered) {
			log.Printf("textus: the textus command can't reach this app: %v", err)
		}
		return
	}
	go a.serveCLI(listener)
}

// serveCLI answers each connection to listener until it closes.
func (a *App) serveCLI(listener cli.Listener) {
	for {
		conn, err := listener.Accept()
		if errors.Is(err, cli.ErrClosed) {
			return
		}
		if err != nil {
			time.Sleep(100 * time.Millisecond)
			continue
		}
		go func() {
			defer conn.Close()
			_ = cli.Serve(conn, func(r cli.Request) cli.Answer {
				return a.handleCLI(context.Background(), r)
			})
		}()
	}
}

func (a *App) handleCLI(ctx context.Context, r cli.Request) cli.Answer {
	if r.Command == cli.ListTools {
		return cli.OK(toolLines(a.cliTools()))
	}
	if word, ok := strings.CutPrefix(r.Command, cli.PluginPrefix); ok {
		return a.cliPluginTool(ctx, word, r.Args)
	}
	switch r.Command {
	case "skill":
		return a.cliSkills(r)
	case "export":
		return a.cliExport(r)
	case "commands", "plugin", "theme":
		return a.inWindow(r)
	}
	return cli.Fail(fmt.Sprintf("'%s' is not a textus command.", r.Command), 2)
}

// offeredTool is a plugin's tool and the window whose plugin runs it.
type offeredTool struct {
	window string
	tool   plugins.AITool
}

// cliTools are each plugin tool once, though every window offers its
// plugins' tools.
func (a *App) cliTools() []offeredTool {
	a.plug.mu.Lock()
	var all []offeredTool
	for _, window := range slices.Sorted(maps.Keys(a.plug.offered)) {
		for _, t := range a.plug.offered[window] {
			all = append(all, offeredTool{window, t})
		}
	}
	a.plug.mu.Unlock()
	seen := map[[2]string]bool{}
	var tools []offeredTool
	for _, t := range all {
		key := [2]string{t.tool.Plugin, t.tool.Name}
		if !seen[key] && a.window(t.window) != nil {
			seen[key] = true
			tools = append(tools, t)
		}
	}
	slices.SortStableFunc(tools, func(x, y offeredTool) int {
		return cmp.Or(cmp.Compare(x.tool.Plugin, y.tool.Plugin), cmp.Compare(x.tool.Name, y.tool.Name))
	})
	return tools
}

// summaryLine is text's first line.
func summaryLine(text string) string {
	line, _, _ := strings.Cut(text, "\n")
	return strings.TrimSuffix(line, "\r")
}

// toolLines are `textus help`'s plugin lines: `  word-count count  Counts
// words.`
func toolLines(tools []offeredTool) string {
	names := make([]string, len(tools))
	width := 0
	for i, t := range tools {
		names[i] = plugins.ShortName(t.tool.Plugin) + " " + t.tool.Name
		width = max(width, len(names[i]))
	}
	var b strings.Builder
	for i, t := range tools {
		fmt.Fprintf(&b, "  %-*s  %s\n", width, names[i], summaryLine(t.tool.Description))
	}
	return b.String()
}

// cliPluginTool runs `textus <plugin> <tool> [options]`.
func (a *App) cliPluginTool(ctx context.Context, word string, args []string) cli.Answer {
	var tools []offeredTool
	for _, t := range a.cliTools() {
		if plugins.ShortName(t.tool.Plugin) == word {
			tools = append(tools, t)
		}
	}
	if len(tools) == 0 {
		return cli.Fail(fmt.Sprintf("'%s' is not a textus command or a plugin's. See 'textus help'.", word), 2)
	}
	help := slices.ContainsFunc(args, func(s string) bool { return s == "--help" || s == "-h" })
	at := -1
	if len(args) > 0 {
		at = slices.IndexFunc(tools, func(t offeredTool) bool { return t.tool.Name == args[0] })
	}
	if at < 0 {
		list := fmt.Sprintf("Usage: textus %s <tool>\n\nTools:\n%s", word, toolLines(tools))
		if help {
			return cli.OK(list)
		}
		return cli.Fail(list, 2)
	}
	found := tools[at]
	t := found.tool
	if help {
		return cli.OK(cli.ToolUsage(t.Plugin, word, t.Name, t.Description, t.Parameters))
	}
	arguments, err := cli.Arguments(t.Parameters, args[1:])
	if err != nil {
		return cli.Fail(fmt.Sprintf("%s See 'textus %s %s --help'.", err.Error(), word, t.Name), 2)
	}
	text, err := a.callWindowTool(ctx, found.window, t, nextCLICall(), arguments)
	if err != nil {
		return cli.Fail(err.Error(), 1)
	}
	return cli.OK(text)
}

// listedSkill is a skill as `textus skill list --json` prints it.
type listedSkill struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Off         bool   `json:"off"`
}

// cliSkills is `textus skill list`: the skills Textus AI has, the global
// folder's included.
func (a *App) cliSkills(r cli.Request) cli.Answer {
	if len(r.Args) != 1 || r.Args[0] != "list" {
		return cli.Fail("Usage: textus skill list", 2)
	}
	skills := ai.ListedSkills(a.workspace("", "").Skills)
	if r.JSON {
		list := make([]listedSkill, len(skills))
		for i, s := range skills {
			list[i] = listedSkill{s.Name, s.Description, s.Off}
		}
		out, err := json.Marshal(list)
		if err != nil {
			return cli.Fail(err.Error(), 1)
		}
		return cli.OK(string(out))
	}
	width := 0
	for _, s := range skills {
		width = max(width, len(s.Name))
	}
	var b strings.Builder
	for _, s := range skills {
		off := ""
		if s.Off {
			off = " (off)"
		}
		fmt.Fprintf(&b, "%-*s  %s%s\n", width, s.Name, summaryLine(s.Description), off)
	}
	return cli.OK(b.String())
}

// inWindow hands the request to a window and waits for its answer.
func (a *App) inWindow(r cli.Request) cli.Answer {
	window, ok := a.listeningWindow()
	if !ok {
		return cli.Fail("No Textus window could take the command.", 1)
	}
	id := nextCLICall()
	answer := make(chan cli.Answer, 1)
	a.cli.mu.Lock()
	if a.cli.waiting == nil {
		a.cli.waiting = map[string]cliWait{}
	}
	a.cli.waiting[id] = cliWait{window: window, answer: answer}
	a.cli.mu.Unlock()
	forget := func() {
		a.cli.mu.Lock()
		delete(a.cli.waiting, id)
		a.cli.mu.Unlock()
	}
	event := cliRequestEvent{ID: id, Command: r.Command, Args: r.Args, Cwd: r.Cwd, JSON: r.JSON}
	if err := a.emitTo(window, "cli-request", event); err != nil {
		forget()
		return cli.Fail("The Textus window closed.", 1)
	}
	timer := time.NewTimer(cliWindowAnswersWithin)
	defer timer.Stop()
	select {
	case got, ok := <-answer:
		if !ok {
			return cli.Fail("The Textus window closed.", 1)
		}
		return got
	case <-timer.C:
		forget()
		return cli.Fail("Textus didn't answer in time.", 1)
	}
}

// listeningWindow is the focused window that takes requests, else any;
// with none open (macOS keeps running windowless) a new one, once it
// listens.
func (a *App) listeningWindow() (string, bool) {
	deadline := time.Now().Add(cliWindowReadyWithin)
	opened := false
	for {
		a.cli.mu.Lock()
		listening := slices.Clone(a.cli.listening)
		a.cli.mu.Unlock()
		listening = slices.DeleteFunc(listening, func(label string) bool { return a.window(label) == nil })
		if focused := a.focusedEditorWindow(""); slices.Contains(listening, focused) {
			return focused, true
		}
		if len(listening) > 0 {
			return listening[0], true
		}
		if !opened && a.lastEditor() == nil {
			if err := a.openNewWindow(); err != nil {
				log.Printf("textus: cannot open a window: %v", err)
			}
			opened = true
		}
		if time.Now().After(deadline) {
			return "", false
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// cliWindowGone: window label takes no more requests, and those it had
// end now rather than at the time limit.
func (a *App) cliWindowGone(label string) {
	a.cli.mu.Lock()
	defer a.cli.mu.Unlock()
	a.cli.listening = slices.DeleteFunc(a.cli.listening, func(l string) bool { return l == label })
	for id, wait := range a.cli.waiting {
		if wait.window == label {
			delete(a.cli.waiting, id)
			close(wait.answer)
		}
	}
}

func init() {
	// A window's page starts (on) or stops taking `textus` requests.
	command("cli_listening", func(c *Call, a struct {
		On bool `json:"on"`
	}) (nothing, error) {
		label := c.Label()
		if label == "" {
			return nil, nil
		}
		c.app.cli.mu.Lock()
		defer c.app.cli.mu.Unlock()
		c.app.cli.listening = slices.DeleteFunc(c.app.cli.listening, func(l string) bool { return l == label })
		if a.On {
			c.app.cli.listening = append(c.app.cli.listening, label)
		}
		return nil, nil
	})
	// A window's answer to request id: only the window it went to answers.
	command("cli_respond", func(c *Call, a struct {
		ID   string `json:"id"`
		Out  string `json:"out"`
		Err  string `json:"err"`
		Exit int    `json:"exit"`
	}) (nothing, error) {
		c.app.cli.mu.Lock()
		wait, ok := c.app.cli.waiting[a.ID]
		ok = ok && wait.window == c.Label()
		if ok {
			delete(c.app.cli.waiting, a.ID)
		}
		c.app.cli.mu.Unlock()
		if ok {
			wait.answer <- cli.Answer{Out: a.Out, Err: a.Err, Exit: a.Exit}
		}
		return nil, nil
	})
}
