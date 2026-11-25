package backend

import (
	"bufio"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"textus/backend/plugins"
	"textus/backend/programs"
)

// External AI agents a plugin names (contributes.agents): Claude Code,
// Codex and the like, started as a child process the window talks the
// Agent Client Protocol to, a JSON message a line. Only a running plugin
// granted agents starts one, and only the command its manifest names; the
// window never passes a command.
//
// agent_start answers when the agent ends: its channel says first that it
// started, with the handle agent_send and agent_stop take, then what it
// says, then that it ended (AgentOutput in
// src/platform/bindings/plugins.ts, agentsIpc in
// src/platform/ipc-plugins.ts). The window closing ends the call, and the
// agent with it.

const (
	// agentGather is how long lines are gathered before they go to the
	// window together.
	agentGather = 24 * time.Millisecond
	// slimOver is how long a line may be before it may carry a whole file
	// or command output.
	slimOver = 4096
	// stepText is how much of a step's text the window keeps, as its step
	// output shows.
	stepText = 4000
	// stepDetail is how much of each kept input field the window gets.
	stepDetail = 1000
)

// stepDetailKeys are what of a tool's raw input (a whole written file, an
// edit's old and new text) says what the step does, as its permission
// prompt shows.
var stepDetailKeys = []string{"command", "cmd", "file_path", "path", "url", "query"}

// externalAgent is an agent running.
type externalAgent struct {
	p   *programs.Piped
	wmu sync.Mutex
}

// externalAgents are the agents running in this app, by handle.
type externalAgents struct {
	mu      sync.Mutex
	next    uint64
	running map[uint64]*externalAgent
}

// agentLaunch is how a manifest says to start one of its agents.
type agentLaunch struct {
	command string
	args    []string
	env     [][2]string
}

// launchOf is agent of manifest, its {pluginDir} filled with home.
func launchOf(manifest jsontext.Value, agent, home string) (agentLaunch, error) {
	var m struct {
		Contributes struct {
			Agents []map[string]jsontext.Value `json:"agents"`
		} `json:"contributes"`
	}
	_ = json.Unmarshal(manifest, &m)
	str := func(v jsontext.Value) (string, bool) {
		var s string
		return s, v.Kind() == '"' && json.Unmarshal(v, &s) == nil
	}
	fill := func(s string) string { return strings.ReplaceAll(s, "{pluginDir}", home) }
	for _, entry := range m.Contributes.Agents {
		if id, ok := str(entry["id"]); !ok || id != agent {
			continue
		}
		command, ok := str(entry["command"])
		if !ok || strings.TrimSpace(command) == "" {
			return agentLaunch{}, errors.New("An agent needs a command.")
		}
		launch := agentLaunch{command: fill(command), args: []string{}}
		var args []jsontext.Value
		if entry["args"].Kind() == '[' && json.Unmarshal(entry["args"], &args) == nil {
			for _, a := range args {
				if s, ok := str(a); ok {
					launch.args = append(launch.args, fill(s))
				}
			}
		}
		var env map[string]jsontext.Value
		if entry["env"].Kind() == '{' && json.Unmarshal(entry["env"], &env) == nil {
			for k, v := range env {
				if s, ok := str(v); ok {
					launch.env = append(launch.env, [2]string{k, fill(s)})
				}
			}
		}
		return launch, nil
	}
	return agentLaunch{}, errors.New("No agent " + agent + ".")
}

// cut is text at most n characters long.
func cut(text string, n int) string {
	i := 0
	for at := range text {
		if i == n {
			return text[:at]
		}
		i++
	}
	return text
}

// stepDetailOf is input cut down to the fields that say what a step does,
// each short; nil for none.
func stepDetailOf(input jsontext.Value) map[string]any {
	var fields map[string]any
	if input.Kind() != '{' || json.Unmarshal(input, &fields) != nil {
		return nil
	}
	kept := map[string]any{}
	for _, key := range stepDetailKeys {
		if v, ok := fields[key]; ok {
			if s, isText := v.(string); isText {
				v = cut(s, stepDetail)
			}
			kept[key] = v
		}
	}
	if len(kept) == 0 {
		return nil
	}
	return kept
}

// slim is line without what the window never reads of a step: its raw
// fields, diff and terminal blocks, and text past what a step shows.
func slim(line string) string {
	if len(line) <= slimOver {
		return line
	}
	var message map[string]any
	if json.Unmarshal([]byte(line), &message) != nil || message["method"] != "session/update" {
		return line
	}
	params, _ := message["params"].(map[string]any)
	update, _ := params["update"].(map[string]any)
	if kind := update["sessionUpdate"]; kind != "tool_call" && kind != "tool_call_update" {
		return line
	}
	// The tool's raw output, which the window never reads (a whole file
	// read).
	delete(update, "rawOutput")
	if raw, ok := update["rawInput"]; ok {
		delete(update, "rawInput")
		if detail := stepDetailOf(mustJSON(raw)); detail != nil {
			update["rawInput"] = detail
		}
	}
	if blocks, ok := update["content"].([]any); ok {
		kept := []any{}
		for _, b := range blocks {
			block, _ := b.(map[string]any)
			if block == nil || block["type"] != "content" {
				continue
			}
			if content, ok := block["content"].(map[string]any); ok {
				if text, ok := content["text"].(string); ok {
					content["text"] = cut(text, stepText)
				}
			}
			kept = append(kept, block)
		}
		update["content"] = kept
	}
	out, err := json.Marshal(message, json.Deterministic(true))
	if err != nil {
		return line
	}
	return string(out)
}

// agentOutput is one message on agent_start's channel: {type: "started",
// handle}, {type: "lines", lines} or {type: "exit", code, stderr}.
type agentOutput = map[string]any

// relayAgent sends what p says, gathered, until its output ends; then,
// once ended has forgotten it, that it ended.
func relayAgent(p *programs.Piped, send func(agentOutput) error, ended func()) {
	lines := make(chan string)
	go func() {
		defer close(lines)
		r := bufio.NewReader(p.Stdout)
		for {
			line, err := r.ReadString('\n')
			if line != "" {
				lines <- strings.ToValidUTF8(strings.TrimRight(line, "\r\n"), "�")
			}
			if err != nil {
				return
			}
		}
	}()
	for open := true; open; {
		first, ok := <-lines
		if !ok {
			break
		}
		batch := []string{first}
		until := time.NewTimer(agentGather)
	gather:
		for {
			select {
			case line, ok := <-lines:
				if !ok {
					open = false
					break gather
				}
				batch = append(batch, line)
			case <-until.C:
				break gather
			}
		}
		until.Stop()
		kept := []string{}
		for _, line := range batch {
			if strings.TrimSpace(line) != "" {
				kept = append(kept, slim(line))
			}
		}
		if len(kept) > 0 {
			_ = send(agentOutput{"type": "lines", "lines": kept})
		}
	}
	// Its code, once it has exited: closing its output is its last act.
	select {
	case <-p.Done():
	case <-time.After(2 * time.Second):
	}
	ended()
	_ = send(agentOutput{"type": "exit", "code": p.ExitCode(), "stderr": p.Stderr.String()})
}

// agent is the running agent handle.
func (a *App) agent(handle uint64) *externalAgent {
	r := &a.plug.agents
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.running[handle]
}

// stopAgent ends agent handle, if it is still running.
func (a *App) stopAgent(handle uint64) {
	r := &a.plug.agents
	r.mu.Lock()
	run := r.running[handle]
	delete(r.running, handle)
	r.mu.Unlock()
	if run != nil {
		run.p.Kill()
	}
}

// stopAgents ends every agent: a child process outlives the app that
// started it.
func (a *App) stopAgents() {
	r := &a.plug.agents
	r.mu.Lock()
	handles := make([]uint64, 0, len(r.running))
	for h := range r.running {
		handles = append(handles, h)
	}
	r.mu.Unlock()
	for _, h := range handles {
		a.stopAgent(h)
	}
}

func init() {
	// Starts agent of plugin in cwd: see the top of this file.
	command("agent_start", func(c *Call, args struct {
		Plugin string  `json:"plugin"`
		Agent  string  `json:"agent"`
		Cwd    *string `json:"cwd"`
	}) (uint64, error) {
		if err := c.app.ensureAIOn(); err != nil {
			return 0, err
		}
		s := c.app.pluginState()
		if !s.Allows(args.Plugin, "agents") {
			return 0, plugins.Refused(args.Plugin, "agents")
		}
		home, err := c.app.pluginHome(s, args.Plugin)
		if err != nil {
			return 0, err
		}
		manifest, err := os.ReadFile(filepath.Join(home, plugins.Manifest))
		if err != nil {
			return 0, err
		}
		if !jsontext.Value(manifest).IsValid() {
			return 0, errors.New("The plugin's manifest isn't JSON.")
		}
		launch, err := launchOf(manifest, args.Agent, home)
		if err != nil {
			return 0, err
		}
		p, err := programs.Start(launch.command, launch.args, launch.env, deref(args.Cwd))
		if err != nil {
			return 0, err
		}
		r := &c.app.plug.agents
		r.mu.Lock()
		if r.running == nil {
			r.running = map[uint64]*externalAgent{}
		}
		r.next++
		handle := r.next
		r.running[handle] = &externalAgent{p: p}
		r.mu.Unlock()
		// The window gone, or the page done with the call: the agent ends.
		go func() {
			select {
			case <-c.ctx.Done():
				c.app.stopAgent(handle)
			case <-p.Done():
			}
		}()
		send := func(out agentOutput) error { return c.Send(out) }
		if err := send(agentOutput{"type": "started", "handle": handle}); err != nil {
			c.app.stopAgent(handle)
		}
		relayAgent(p, send, func() { c.app.stopAgent(handle) })
		return handle, nil
	})
	// Writes one protocol message to agent handle.
	command("agent_send", func(c *Call, a struct {
		Handle uint64 `json:"handle"`
		Line   string `json:"line"`
	}) (nothing, error) {
		run := c.app.agent(a.Handle)
		if run == nil {
			return nil, errors.New("The agent has stopped.")
		}
		run.wmu.Lock()
		defer run.wmu.Unlock()
		if _, err := io.WriteString(run.p.Stdin, strings.ReplaceAll(a.Line, "\n", " ")+"\n"); err != nil {
			return nil, errors.New("The agent has stopped.")
		}
		return nil, nil
	})
	// Ends agent handle, if it is still running.
	command("agent_stop", func(c *Call, a struct {
		Handle uint64 `json:"handle"`
	}) (nothing, error) {
		c.app.stopAgent(a.Handle)
		return nil, nil
	})
}
