package plugins

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Tools plugins give Textus AI: each window reports its plugins' tools, a
// request offers the asking window's, and a call goes back there. Like MCP
// tools, calls run one at a time, in order.

// ToolResultChars is the most of an answer the model is shown.
const ToolResultChars = 20_000

// AITool is one tool, as the window describes it from the plugin's
// manifest (PluginAiTool in src/platform/bindings/plugins.ts).
type AITool struct {
	Plugin string `json:"plugin"`
	// Name is the plugin's name for it.
	Name        string `json:"name"`
	Description string `json:"description"`
	// Parameters is a JSON schema of its arguments.
	Parameters jsontext.Value `json:"parameters"`
}

// ToolCallEvent is a call of a plugin's tool, sent to its window as
// "plugin-ai-tool-call".
type ToolCallEvent struct {
	CallID    string         `json:"callId"`
	Plugin    string         `json:"plugin"`
	Name      string         `json:"name"`
	Arguments jsontext.Value `json:"arguments"`
}

// ShortName is a plugin's name in a tool's name and in its `textus`
// command: the last part of its id.
func ShortName(plugin string) string {
	return plugin[strings.LastIndexByte(plugin, '.')+1:]
}

// ModelName is the model's name for a plugin's tool: `plugin_` and its
// name, in the letters a tool name may have, at most 64.
func ModelName(plugin, name string) string {
	var b strings.Builder
	for _, c := range "plugin_" + ShortName(plugin) + "_" + name {
		if c < utf8.RuneSelf && (c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			b.WriteRune(c)
		} else {
			b.WriteByte('_')
		}
	}
	out := b.String()
	if len(out) > 64 {
		out = out[:64]
	}
	return out
}

// NamedTool is a tool with the model's name for it.
type NamedTool struct {
	Name string
	Tool AITool
}

// WindowTools are the tools offered, each with the model's name for it,
// kept distinct.
func WindowTools(offered []AITool) []NamedTool {
	var out []NamedTool
	taken := map[string]bool{}
	for _, t := range offered {
		name := ModelName(t.Plugin, t.Name)
		for taken[name] {
			name += "_"
		}
		taken[name] = true
		out = append(out, NamedTool{name, t})
	}
	return out
}

func wordChar(c rune) bool { return unicode.IsLetter(c) || unicode.IsNumber(c) || c == '_' }

// ModelNamesIn is text with the plugins' own names for their tools -
// "list_reminders" in a description - as the model is given them. Only
// names with a `_`: a plain word ("search") is more often just the word.
func ModelNamesIn(text string, names [][2]string) string {
	out := text
	for _, pair := range names {
		own, model := pair[0], pair[1]
		if own == "" || !strings.Contains(own, "_") {
			continue
		}
		at := 0
		for {
			found := strings.Index(out[at:], own)
			if found < 0 {
				break
			}
			start, end := at+found, at+found+len(own)
			before, _ := utf8.DecodeLastRuneInString(out[:start])
			after, _ := utf8.DecodeRuneInString(out[end:])
			if start > 0 && wordChar(before) || end < len(out) && wordChar(after) {
				at = end
				continue
			}
			out = out[:start] + model + out[end:]
			at = start + len(model)
		}
	}
	return out
}

// Trimmed is a tool's answer as the model is shown it: something, and no
// more than ToolResultChars.
func Trimmed(text string) string { return OutsideToolResult(text, ToolResultChars) }

// OutsideToolResult is an outside tool's answer (a plugin's, an MCP
// server's) as the model is shown it: something, and no more than most
// characters.
func OutsideToolResult(text string, most int) string {
	if strings.TrimSpace(text) == "" {
		return "(The tool returned nothing.)"
	}
	n := 0
	for i := range text {
		if n == most {
			return text[:i] + "\n[cut off: the rest was too long]"
		}
		n++
	}
	return text
}

// ToolDescription is what the model is told of a plugin's tool.
func ToolDescription(t AITool) string {
	return strings.TrimSpace(t.Description) + "\n\n(A tool of the plugin " + t.Plugin + ", which the user installed.)"
}

// ToolParameters is a plugin tool's schema as the model is given it: an
// object, with properties.
func ToolParameters(raw jsontext.Value) map[string]any {
	var schema map[string]any
	if err := json.Unmarshal(raw, &schema); err != nil || schema == nil {
		schema = map[string]any{"type": "object"}
	}
	if _, ok := schema["properties"]; !ok {
		schema["properties"] = map[string]any{}
	}
	return schema
}
