// Package mcp is the client half of the Model Context Protocol: MCP
// servers, programs or web services that give Textus AI more tools. One
// connection is JSON-RPC 2.0 over a child process's stdio (a message a
// line) or Streamable HTTP: only initialize, tools/list, tools/call, and
// an answer to ping. The servers themselves come from settings.json's
// mcpServers (backend/mcp_hub.go).
package mcp

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"encoding/json/jsontext"
)

// ServersKey is the settings.json field listing the servers.
const ServersKey = "mcpServers"

// Tool is one tool a server offers (McpTool in
// src/platform/bindings/plugins.ts).
type Tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema jsontext.Value `json:"inputSchema"`
}

// Endpoint is how to reach a server: a command to start (Program, Args,
// Env), or a URL (URL, Headers). Env and Headers are sorted by name.
type Endpoint struct {
	Program string
	Args    []string
	Env     [][2]string
	URL     string
	Headers [][2]string
}

// Equal is whether e and o reach the same server the same way.
func (e Endpoint) Equal(o Endpoint) bool {
	return e.Program == o.Program && slices.Equal(e.Args, o.Args) && slices.Equal(e.Env, o.Env) &&
		e.URL == o.URL && slices.Equal(e.Headers, o.Headers)
}

// Config is one server as Settings keeps it.
type Config struct {
	ID      string            `json:"id"`
	Name    string            `json:"name"`
	Enabled bool              `json:"enabled"`
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
}

func pairs(m map[string]string) [][2]string {
	out := make([][2]string, 0, len(m))
	for k, v := range m {
		out = append(out, [2]string{k, v})
	}
	slices.SortFunc(out, func(a, b [2]string) int {
		return cmp.Or(strings.Compare(a[0], b[0]), strings.Compare(a[1], b[1]))
	})
	return out
}

// Endpoint is how the server is reached: its URL when it has one, else its
// command; false with neither.
func (c Config) Endpoint() (Endpoint, bool) {
	if url := strings.TrimSpace(c.URL); url != "" {
		return Endpoint{URL: url, Headers: pairs(c.Headers)}, true
	}
	if program := strings.TrimSpace(c.Command); program != "" {
		args := c.Args
		if args == nil {
			args = []string{}
		}
		return Endpoint{Program: program, Args: args, Env: pairs(c.Env)}, true
	}
	return Endpoint{}, false
}

// Label is what the server is called: its name, or its id.
func (c Config) Label() string {
	if name := strings.TrimSpace(c.Name); name != "" {
		return name
	}
	return c.ID
}

// ToolName is a tool's name as the model sees it: mcp_<server>_<tool>, in
// the letters, digits, _ and - every provider takes, at most 64 long.
func ToolName(server, tool string) string {
	clean := func(s string) string {
		return strings.Trim(strings.Map(func(r rune) rune {
			switch {
			case 'a' <= r && r <= 'z' || '0' <= r && r <= '9' || r == '-':
				return r
			case 'A' <= r && r <= 'Z':
				return r + 'a' - 'A'
			}
			return '_'
		}, s), "_")
	}
	var parts []string
	for _, p := range []string{"mcp", clean(server), clean(tool)} {
		if p != "" {
			parts = append(parts, p)
		}
	}
	name := strings.Join(parts, "_")
	if len(name) > 64 {
		name = name[:64]
	}
	return name
}

// UniqueName is name, or with _2, _3… where another tool already has it,
// still at most 64 long.
func UniqueName(name string, taken []string) string {
	if !slices.Contains(taken, name) {
		return name
	}
	for n := 2; ; n++ {
		suffix := fmt.Sprintf("_%d", n)
		base := name
		if len(base) > 64-len(suffix) {
			base = base[:64-len(suffix)]
		}
		if candidate := base + suffix; !slices.Contains(taken, candidate) {
			return candidate
		}
	}
}

// Description is what the model is told of a server's tool.
func Description(server string, t Tool) string {
	if strings.TrimSpace(t.Description) == "" {
		return fmt.Sprintf("%s (a tool of the MCP server %s, which the user connected).", t.Name, server)
	}
	return fmt.Sprintf("%s\n\n(A tool of the MCP server %s, which the user connected.)", strings.TrimSpace(t.Description), server)
}
