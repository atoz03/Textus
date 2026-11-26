package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"textus/backend/programs"
)

const (
	// protocolVersion is the version asked for; a server answers with its
	// own, and every version since 2024-11-05 has the calls used here.
	protocolVersion = "2025-06-18"
	// StartTimeout is how long a server gets to start and answer
	// initialize: npx may first download the package.
	StartTimeout = 90 * time.Second
	// callTimeout is how long one tools/list or other short call may take.
	callTimeout = 30 * time.Second
	// ToolTimeout is how long one tool may run.
	ToolTimeout = 120 * time.Second
	// maxAnswer is the longest answer a web server's call may give.
	maxAnswer = 16 * 1024 * 1024
)

// Conn is a connected, initialized server.
type Conn struct {
	// Server is the name and version it gave.
	Server string
	next   atomic.Uint64
	stdio  *stdio
	web    *web
}

// refused is what a server answered a call with: its error's message.
type refused struct{ message string }

func (e *refused) Error() string { return e.message }

// tookTooLong is a call that had no answer in its time.
type tookTooLong struct {
	method string
	time   time.Duration
}

func (e *tookTooLong) Error() string {
	return fmt.Sprintf("%s took longer than %d s", e.method, int(e.time.Seconds()))
}

// message is one JSON-RPC message Textus sends.
type message struct {
	JSONRPC string  `json:"jsonrpc"`
	ID      *uint64 `json:"id,omitzero"`
	Method  string  `json:"method,omitzero"`
	Params  any     `json:"params,omitzero"`
}

// Open starts or reaches the server at e, and initializes it. client is
// what a web server is reached with; version is the app's.
func Open(ctx context.Context, e Endpoint, client *http.Client, version string) (*Conn, error) {
	c := &Conn{}
	if e.URL != "" {
		c.web = &web{client: client, url: e.URL, headers: e.Headers}
	} else {
		p, err := programs.Start(e.Program, e.Args, e.Env, "")
		if err != nil {
			return nil, err
		}
		c.stdio = startStdio(p)
	}
	init, err := c.request(ctx, "initialize", map[string]any{
		"protocolVersion": protocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "Textus", "version": version},
	}, StartTimeout)
	if err == nil {
		err = c.notify(ctx, "notifications/initialized")
	}
	if err != nil {
		var late *tookTooLong
		var said *refused
		switch {
		case errors.As(err, &late):
			err = c.failure("it didn't answer within 90 seconds")
		case errors.As(err, &said):
			err = c.failure(said.message)
		}
		c.Close()
		return nil, err
	}
	var info struct {
		ServerInfo struct {
			Name    jsontext.Value `json:"name"`
			Version jsontext.Value `json:"version"`
		} `json:"serverInfo"`
	}
	_ = json.Unmarshal(init, &info)
	name := text(info.ServerInfo.Name)
	if name == "" && info.ServerInfo.Name.Kind() != '"' {
		name = "MCP server"
	}
	c.Server = strings.TrimSpace(name + " " + text(info.ServerInfo.Version))
	return c, nil
}

// text is a JSON string's text, "" for anything else.
func text(v jsontext.Value) string {
	var s string
	if v.Kind() != '"' || json.Unmarshal(v, &s) != nil {
		return ""
	}
	return s
}

// fields is a JSON object's members, none for anything else.
func fields(v jsontext.Value) map[string]jsontext.Value {
	var m map[string]jsontext.Value
	if v.Kind() != '{' || json.Unmarshal(v, &m) != nil {
		return nil
	}
	return m
}

// items is a JSON array's items, none for anything else.
func items(v jsontext.Value) []jsontext.Value {
	var a []jsontext.Value
	if v.Kind() != '[' || json.Unmarshal(v, &a) != nil {
		return nil
	}
	return a
}

// ListTools is every tool the server offers, page by page: at most 20
// pages, so a server whose pages never end can't hold it.
func (c *Conn) ListTools(ctx context.Context) ([]Tool, error) {
	tools := []Tool{}
	cursor := ""
	seen := map[string]bool{}
	for range 20 {
		params := map[string]any{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		raw, err := c.request(ctx, "tools/list", params, callTimeout)
		if err != nil {
			return nil, err
		}
		page := fields(raw)
		for _, item := range items(page["tools"]) {
			tool := fields(item)
			if tool["name"].Kind() != '"' {
				continue
			}
			schema := tool["inputSchema"]
			if schema.Kind() != '{' {
				schema = jsontext.Value(`{"type":"object","properties":{}}`)
			}
			tools = append(tools, Tool{Name: text(tool["name"]), Description: text(tool["description"]), InputSchema: schema})
		}
		next := page["nextCursor"]
		// A cursor met before would go round again.
		if next.Kind() != '"' || len(tools) >= 500 || seen[text(next)] {
			break
		}
		cursor = text(next)
		seen[cursor] = true
	}
	return tools, nil
}

// CallTool calls a tool: its result as the model reads it (text, plus a
// note per picture or resource) and whether the tool said it failed.
func (c *Conn) CallTool(ctx context.Context, name string, arguments jsontext.Value) (string, bool, error) {
	raw, err := c.request(ctx, "tools/call", map[string]any{"name": name, "arguments": arguments}, ToolTimeout)
	if err != nil {
		return "", false, err
	}
	result := fields(raw)
	var parts []string
	for _, item := range items(result["content"]) {
		part := fields(item)
		switch text(part["type"]) {
		case "text":
			parts = append(parts, text(part["text"]))
		case "image":
			parts = append(parts, "[a picture]")
		case "audio":
			parts = append(parts, "[a sound]")
		case "resource_link", "resource":
			uri := text(part["uri"])
			if part["uri"].Kind() != '"' {
				uri = text(fields(part["resource"])["uri"])
			}
			parts = append(parts, "[resource "+uri+"]")
		}
	}
	if structured, ok := result["structuredContent"]; ok && len(parts) == 0 {
		compact := structured.Clone()
		if compact.Compact() == nil {
			parts = append(parts, string(compact))
		}
	}
	return strings.Join(parts, "\n"), string(result["isError"]) == "true", nil
}

// failure is why the server failed, with what it wrote to stderr.
func (c *Conn) failure(why string) error {
	if c.stdio != nil {
		if tail := strings.TrimSpace(c.stdio.p.Stderr.String()); tail != "" {
			return fmt.Errorf("%s. It said: %s", why, tail)
		}
	}
	return errors.New(why)
}

// stopped is the error for a started server that stopped. Its standard
// error is copied in full only once waiting for the process ends, a moment
// after its output closed, so it waits for that; a program the server
// started can hold the pipe, so not for long.
func (c *Conn) stopped() error {
	select {
	case <-c.stdio.p.Done():
	case <-time.After(2 * time.Second):
	}
	return c.failure("the server stopped")
}

func (c *Conn) request(ctx context.Context, method string, params any, limit time.Duration) (jsontext.Value, error) {
	id := c.next.Add(1)
	m := message{JSONRPC: "2.0", ID: &id, Method: method, Params: params}
	ctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	var result jsontext.Value
	var err error
	if c.web != nil {
		result, err = c.web.post(ctx, m, id)
	} else {
		result, err = c.stdio.request(ctx, c, id, m)
	}
	if err != nil && errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return nil, &tookTooLong{method, limit}
	}
	return result, err
}

func (c *Conn) notify(ctx context.Context, method string) error {
	m := message{JSONRPC: "2.0", Method: method, Params: map[string]any{}}
	if c.web != nil {
		ctx, cancel := context.WithTimeout(ctx, callTimeout)
		defer cancel()
		_, err := c.web.post(ctx, m, 0)
		return err
	}
	if c.stdio.write(m) != nil {
		return c.stopped()
	}
	return nil
}

// Alive is whether a started server's process is still running.
func (c *Conn) Alive() bool {
	if c.stdio == nil {
		return true
	}
	select {
	case <-c.stdio.p.Done():
		return false
	default:
		return true
	}
}

// Done is closed once a started server's process has ended; never for a
// web server.
func (c *Conn) Done() <-chan struct{} {
	if c.stdio == nil {
		return nil
	}
	return c.stdio.p.Done()
}

// Close stops a started server.
func (c *Conn) Close() {
	if c.stdio != nil {
		_ = c.stdio.p.Stdin.Close()
		c.stdio.p.Kill()
	}
}

// reply is what a call was answered.
type reply struct {
	result jsontext.Value
	err    error
}

// stdio is a server started as a child process.
type stdio struct {
	p   *programs.Piped
	wmu sync.Mutex

	mu      sync.Mutex
	waiting map[uint64]chan reply
	ended   bool
	gone    chan struct{}
}

// startStdio reads the answers of the server p.
func startStdio(p *programs.Piped) *stdio {
	s := &stdio{p: p, waiting: map[uint64]chan reply{}, gone: make(chan struct{})}
	go func() {
		lines := bufio.NewReader(p.Stdout)
		for {
			line, err := lines.ReadBytes('\n')
			if line = bytes.TrimSpace(line); len(line) > 0 {
				s.dispatch(line)
			}
			if err != nil {
				break
			}
		}
		// The server is gone: every call still waiting fails.
		s.mu.Lock()
		s.ended = true
		s.waiting = map[uint64]chan reply{}
		s.mu.Unlock()
		close(s.gone)
	}()
	return s
}

// write sends one message as a line.
func (s *stdio) write(m message) error {
	line, err := json.Marshal(m)
	if err != nil {
		return err
	}
	s.wmu.Lock()
	defer s.wmu.Unlock()
	_, err = s.p.Stdin.Write(append(line, '\n'))
	return err
}

func (s *stdio) request(ctx context.Context, c *Conn, id uint64, m message) (jsontext.Value, error) {
	answer := make(chan reply, 1)
	s.mu.Lock()
	if s.ended {
		s.mu.Unlock()
		return nil, c.stopped()
	}
	s.waiting[id] = answer
	s.mu.Unlock()
	forget := func() {
		s.mu.Lock()
		delete(s.waiting, id)
		s.mu.Unlock()
	}
	if s.write(m) != nil {
		forget()
		return nil, c.stopped()
	}
	select {
	case r := <-answer:
		return r.result, r.err
	case <-s.gone:
		select {
		case r := <-answer:
			return r.result, r.err
		default:
			return nil, c.stopped()
		}
	case <-ctx.Done():
		forget()
		return nil, ctx.Err()
	}
}

// dispatch takes a message from the server: an answer goes to its caller;
// a request gets a reply (ping answered, anything else refused); a
// notification needs nothing.
func (s *stdio) dispatch(line []byte) {
	m := fields(jsontext.Value(line))
	if m == nil {
		return
	}
	if m["method"].Kind() == '"' {
		id, ok := m["id"]
		if !ok {
			return
		}
		// The id as the server sent it, whatever its kind.
		reply := map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{}}
		if text(m["method"]) != "ping" {
			delete(reply, "result")
			reply["error"] = map[string]any{"code": -32601, "message": "Not offered by Textus"}
		}
		if line, err := json.Marshal(reply, json.Deterministic(true)); err == nil {
			s.wmu.Lock()
			_, _ = s.p.Stdin.Write(append(line, '\n'))
			s.wmu.Unlock()
		}
		return
	}
	var id uint64
	if json.Unmarshal(m["id"], &id) != nil || m["id"].Kind() != '0' {
		return
	}
	s.mu.Lock()
	answer := s.waiting[id]
	delete(s.waiting, id)
	s.mu.Unlock()
	if answer != nil {
		answer <- answerOf(m)
	}
}

// answerOf is a response's result, or its error's message.
func answerOf(m map[string]jsontext.Value) reply {
	if e, ok := m["error"]; ok && e.Kind() != 'n' {
		said := text(fields(e)["message"])
		if fields(e)["message"].Kind() != '"' {
			said = "the server refused"
		}
		return reply{err: &refused{said}}
	}
	result, ok := m["result"]
	if !ok {
		result = jsontext.Value("null")
	}
	return reply{result: result}
}
