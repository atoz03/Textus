package backend

import (
	"cmp"
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"reflect"
	"slices"
	"sync"
	"time"

	"textus/backend/ai"
	"textus/backend/ai/aicompat"
	"textus/backend/mcp"
	"textus/backend/plugins"
)

// MCP servers (backend/mcp): each one turned on in settings.json's
// mcpServers runs in the background; a request offers the tools of every
// ready one. Their secrets are kept with the providers' keys.

const (
	// mcpWaitForStart is how long a request waits for servers still
	// starting, all of them together.
	mcpWaitForStart = 10 * time.Second
	// mcpResultChars is the most of one tool's answer the model is given.
	mcpResultChars = 30_000
)

// mcpServer is a server started: once ready is closed, its connection and
// tools, or why it couldn't start.
type mcpServer struct {
	endpoint mcp.Endpoint
	name     string
	ready    chan struct{}

	mu      sync.Mutex
	conn    *mcp.Conn
	tools   []mcp.Tool
	err     string
	dropped bool
}

// mcpHub are the servers running, by id.
type mcpHub struct {
	mu      sync.Mutex
	running map[string]*mcpServer
}

func (s *mcpServer) started() bool {
	select {
	case <-s.ready:
		return true
	default:
		return false
	}
}

// stopped is whether it started and then stopped (its process gone).
func (s *mcpServer) stopped() bool {
	return s.started() && s.conn != nil && !s.conn.Alive()
}

// drop stops it, now or as soon as it has started.
func (s *mcpServer) drop() {
	s.mu.Lock()
	s.dropped = true
	conn := s.conn
	s.mu.Unlock()
	if conn != nil {
		conn.Close()
	}
}

// startMCP starts a server in the background.
func startMCP(name string, e mcp.Endpoint) *mcpServer {
	s := &mcpServer{endpoint: e, name: name, ready: make(chan struct{})}
	go func() {
		defer close(s.ready)
		ctx := context.Background()
		conn, err := mcp.Open(ctx, e, netClient(mcp.ToolTimeout), appVersion)
		var tools []mcp.Tool
		if err == nil {
			if tools, err = conn.ListTools(ctx); err != nil {
				conn.Close()
			}
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		if err != nil {
			s.err = err.Error()
			return
		}
		s.conn, s.tools = conn, tools
		if s.dropped {
			conn.Close()
		}
	}()
	return s
}

// mcpKept are the servers' secrets as the key store keeps them.
func (a *App) mcpKept() mcp.Kept {
	text, err := a.providerKey(mcp.StoreID)
	if err != nil {
		return mcp.Kept{}
	}
	return mcp.ParseKept(text)
}

// sealMCP moves the secrets typed into servers (the settings' list) to the
// key store: the list with a mask in their place, and whether it changed.
func (a *App) sealMCP(servers jsontext.Value) (jsontext.Value, bool) {
	was := a.mcpKept()
	kept, sealed, changed := mcp.Seal(was, servers)
	if !reflect.DeepEqual(kept, was) {
		if text, err := json.Marshal(kept, json.Deterministic(true)); err == nil {
			_ = a.changeKeys(func(keys map[string]string) bool {
				keys[mcp.StoreID] = string(text)
				return true
			})
		}
	}
	return sealed, changed
}

// sealSavedMCP moves secrets left in the saved settings (from before, or
// written by another build) to the key store. Run once at startup.
func (a *App) sealSavedMCP() {
	servers := a.readSetting(mcp.ServersKey)
	if servers == nil {
		return
	}
	if sealed, changed := a.sealMCP(servers); changed {
		a.patchSettingsFromBackend(jsonObject{mcp.ServersKey: sealed})
	}
}

// mcpConfigs are the servers in the settings, their secrets as kept.
func (a *App) mcpConfigs() []mcp.Config {
	raw := a.readSetting(mcp.ServersKey)
	var configs []mcp.Config
	if raw == nil || json.Unmarshal(raw, &configs) != nil {
		return nil
	}
	kept := a.mcpKept()
	for i := range configs {
		mcp.Unmask(kept, &configs[i])
	}
	return configs
}

// syncMCP brings the running servers in line with the settings: each one
// on is started (again, if changed or stopped), each one off or gone is
// stopped.
func (a *App) syncMCP() {
	type wanted struct {
		config   mcp.Config
		endpoint mcp.Endpoint
	}
	var want []wanted
	for _, c := range a.mcpConfigs() {
		if e, ok := c.Endpoint(); c.Enabled && ok {
			want = append(want, wanted{c, e})
		}
	}
	h := &a.mcp
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.running == nil {
		h.running = map[string]*mcpServer{}
	}
	for id, s := range h.running {
		if !slices.ContainsFunc(want, func(w wanted) bool {
			return w.config.ID == id && w.endpoint.Equal(s.endpoint) && !s.stopped()
		}) {
			s.drop()
			delete(h.running, id)
		}
	}
	for _, w := range want {
		if s, ok := h.running[w.config.ID]; ok {
			s.name = w.config.Label()
			continue
		}
		h.running[w.config.ID] = startMCP(w.config.Label(), w.endpoint)
	}
}

// stopMCP stops every server: a child process outlives the app that
// started it.
func (a *App) stopMCP() {
	h := &a.mcp
	h.mu.Lock()
	defer h.mu.Unlock()
	for id, s := range h.running {
		s.drop()
		delete(h.running, id)
	}
}

// mcpServerStatus is a server's state as Settings shows it (ServerStatus
// in src/platform/bindings/plugins.ts).
type mcpServerStatus struct {
	ID string `json:"id"`
	// State is "starting", "ready" or "failed".
	State  string     `json:"state"`
	Server string     `json:"server"`
	Tools  []mcp.Tool `json:"tools"`
	Error  *string    `json:"error"`
}

// mcpStatus are the servers that are on, each starting, ready or failed.
func (a *App) mcpStatus() []mcpServerStatus {
	a.syncMCP()
	h := &a.mcp
	h.mu.Lock()
	defer h.mu.Unlock()
	list := []mcpServerStatus{}
	for id, s := range h.running {
		status := mcpServerStatus{ID: id, State: "starting", Tools: []mcp.Tool{}}
		if s.started() {
			if s.conn != nil {
				status.State, status.Server, status.Tools = "ready", s.conn.Server, s.tools
			} else {
				why := s.err
				status.State, status.Error = "failed", &why
			}
		}
		list = append(list, status)
	}
	slices.SortFunc(list, func(x, y mcpServerStatus) int { return cmp.Compare(x.ID, y.ID) })
	return list
}

// mcpTools are the tools of every server ready for a request, waiting a
// little for those still starting.
func (a *App) mcpTools(ctx context.Context) []ai.Tool {
	a.syncMCP()
	type server struct {
		id, name string
		s        *mcpServer
	}
	var servers []server
	a.mcp.mu.Lock()
	for id, s := range a.mcp.running {
		servers = append(servers, server{id, s.name, s})
	}
	a.mcp.mu.Unlock()
	slices.SortFunc(servers, func(x, y server) int { return cmp.Compare(x.id, y.id) })
	// One wait for all the servers still starting, not one each.
	wait, cancel := context.WithTimeout(ctx, mcpWaitForStart)
	defer cancel()
	var tools []ai.Tool
	var taken []string
	for _, sv := range servers {
		select {
		case <-sv.s.ready:
		case <-wait.Done():
			continue
		}
		if sv.s.conn == nil {
			continue
		}
		for _, t := range sv.s.tools {
			name := mcp.UniqueName(mcp.ToolName(sv.name, t.Name), taken)
			taken = append(taken, name)
			tools = append(tools, mcpTool(ctx, sv.name, name, t, sv.s.conn))
		}
	}
	return tools
}

// mcpTool is a server's tool as the model is offered it.
func mcpTool(ctx context.Context, server, name string, t mcp.Tool, conn *mcp.Conn) ai.Tool {
	var parameters map[string]any
	if json.Unmarshal(t.InputSchema, &parameters) != nil || parameters == nil {
		parameters = map[string]any{}
	}
	if _, ok := parameters["properties"]; !ok {
		parameters["properties"] = map[string]any{}
	}
	return ai.Tool{
		Spec: aicompat.ToolSpec{Name: name, Description: mcp.Description(server, t), Parameters: parameters},
		// A server's tool may change things outside Textus: one call at a
		// time, in the order asked.
		Access: ai.Proposes,
		Modes:  ai.EveryMode,
		Run: func(_ *ai.ToolContext, call aicompat.ToolCall) aicompat.ToolOutput {
			arguments := jsontext.Value(call.Arguments)
			if !arguments.IsValid() {
				arguments = jsontext.Value("{}")
			}
			text, failed, err := conn.CallTool(ctx, t.Name, arguments)
			switch {
			case err != nil:
				text = "The MCP server couldn't run it: " + err.Error()
			case failed:
				text = "The tool reported an error: " + text
			}
			return aicompat.Text(plugins.OutsideToolResult(text, mcpResultChars))
		},
	}
}

// mcpTried is what a server offers, tried before it is saved: its name and
// tools (Tried in src/platform/bindings/plugins.ts).
type mcpTried struct {
	Server string     `json:"server"`
	Tools  []mcp.Tool `json:"tools"`
}

func init() {
	command("mcp_status", func(c *Call, _ none) ([]mcpServerStatus, error) {
		return c.app.mcpStatus(), nil
	})
	command("mcp_restart", func(c *Call, a struct {
		ID string `json:"id"`
	}) ([]mcpServerStatus, error) {
		h := &c.app.mcp
		h.mu.Lock()
		if s := h.running[a.ID]; s != nil {
			s.drop()
			delete(h.running, a.ID)
		}
		h.mu.Unlock()
		return c.app.mcpStatus(), nil
	})
	command("mcp_try", func(c *Call, a struct {
		Server jsontext.Value `json:"server"`
	}) (mcpTried, error) {
		var config mcp.Config
		if err := json.Unmarshal(a.Server, &config); err != nil {
			return mcpTried{}, err
		}
		mcp.Unmask(c.app.mcpKept(), &config)
		e, ok := config.Endpoint()
		if !ok {
			return mcpTried{}, errors.New("Give a command or a URL.")
		}
		conn, err := mcp.Open(c.ctx, e, netClient(mcp.ToolTimeout), appVersion)
		if err != nil {
			return mcpTried{}, err
		}
		defer conn.Close()
		tools, err := conn.ListTools(c.ctx)
		if err != nil {
			return mcpTried{}, err
		}
		return mcpTried{Server: conn.Server, Tools: tools}, nil
	})
}
