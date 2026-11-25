package backend

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"strconv"
	"sync/atomic"
	"time"

	"textus/backend/ai"
	"textus/backend/ai/aicompat"
	"textus/backend/plugins"
)

// Tools plugins give Textus AI: each window reports its plugins' tools, a
// request offers the asking window's, and a call goes back there to run.
// The app's own tools that run in the window (Create mode's plugin check,
// what the folder's index knows) are called the same way, as the owner
// "textus".

// pluginToolWait is how long a plugin has to answer a call.
const pluginToolWait = 60 * time.Second

// toolAnswer is what a window answered a tool call: its text, or why it
// couldn't.
type toolAnswer struct {
	text string
	err  *string
}

// editorWindowOf is the editor window a request's tools run in: a
// detached chat's is the window it serves.
func (a *App) editorWindowOf(asking string) string {
	if editor, ok := a.chats.editorFor(asking); ok {
		return editor
	}
	return asking
}

// offeredTools are the tools window's plugins offer now.
func (a *App) offeredTools(window string) []plugins.AITool {
	a.plug.mu.Lock()
	defer a.plug.mu.Unlock()
	return a.plug.offered[window]
}

var toolCalls atomic.Uint64

// callWindowTool runs tool in window: its answer, or why there is none.
func (a *App) callWindowTool(ctx context.Context, window string, tool plugins.AITool, callID string, arguments jsontext.Value) (string, error) {
	if callID == "" {
		callID = "textus-call-" + strconv.FormatUint(toolCalls.Add(1), 10)
	}
	answered := make(chan toolAnswer, 1)
	a.plug.mu.Lock()
	if a.plug.waiting == nil {
		a.plug.waiting = map[string]chan toolAnswer{}
	}
	a.plug.waiting[callID] = answered
	a.plug.mu.Unlock()
	forget := func() {
		a.plug.mu.Lock()
		delete(a.plug.waiting, callID)
		a.plug.mu.Unlock()
	}
	event := plugins.ToolCallEvent{CallID: callID, Plugin: tool.Plugin, Name: tool.Name, Arguments: arguments}
	if err := a.emitTo(window, "plugin-ai-tool-call", event); err != nil {
		forget()
		return "", errors.New("The plugin's window is gone, so the tool couldn't run.")
	}
	timer := time.NewTimer(pluginToolWait)
	defer timer.Stop()
	select {
	case answer := <-answered:
		if answer.err != nil {
			return "", fmt.Errorf("The plugin's tool failed: %s", *answer.err)
		}
		return answer.text, nil
	case <-timer.C:
		forget()
		return "", errors.New("The plugin didn't answer in time.")
	case <-ctx.Done():
		forget()
		return "", ctx.Err()
	}
}

// toolArguments are a call's arguments as an object, {} when they aren't
// one; root, when given, goes along as __root.
func toolArguments(raw, root string) jsontext.Value {
	var args map[string]any
	if json.Unmarshal([]byte(raw), &args) != nil || args == nil {
		args = map[string]any{}
	}
	if root != "" {
		args["__root"] = root
	}
	out, err := json.Marshal(args, json.Deterministic(true))
	if err != nil {
		return jsontext.Value("{}")
	}
	return out
}

// windowTool is a tool answered by window, as the model is offered it.
func (a *App) windowTool(ctx context.Context, window string, spec aicompat.ToolSpec, access ai.Access, modes []ai.AgentMode, tool plugins.AITool, root string) ai.Tool {
	return ai.Tool{Spec: spec, Access: access, Modes: modes, Run: func(_ *ai.ToolContext, call aicompat.ToolCall) aicompat.ToolOutput {
		text, err := a.callWindowTool(ctx, window, tool, call.CallID, toolArguments(call.Arguments, root))
		if err != nil {
			text = err.Error()
		}
		return aicompat.Text(plugins.Trimmed(text))
	}}
}

// pluginToolsFor are the tools the asking window's plugins offer, for one
// request, and Create mode's check of a plugin it wrote. A detached chat
// asks for the editor window it serves.
func (a *App) pluginToolsFor(ctx context.Context, asking string) []ai.Tool {
	if asking == "" {
		return nil
	}
	window := a.editorWindowOf(asking)
	named := plugins.WindowTools(a.offeredTools(window))
	names := make([][2]string, len(named))
	for i, n := range named {
		names[i] = [2]string{n.Tool.Name, n.Name}
	}
	var tools []ai.Tool
	for _, n := range named {
		t := n.Tool
		t.Description = plugins.ModelNamesIn(t.Description, names)
		spec := aicompat.ToolSpec{Name: n.Name, Description: plugins.ToolDescription(t), Parameters: plugins.ToolParameters(t.Parameters)}
		tools = append(tools, a.windowTool(ctx, window, spec, ai.Proposes, ai.EveryMode, t, ""))
	}
	return append(tools, a.checkPluginTool(ctx, window))
}

// appWindowTool is one of the app's own tools the window answers.
func (a *App) appWindowTool(ctx context.Context, window, name, description string, parameters map[string]any, modes []ai.AgentMode, root string) ai.Tool {
	spec := aicompat.ToolSpec{Name: name, Description: description, Parameters: parameters}
	return a.windowTool(ctx, window, spec, ai.ReadOnly, modes, plugins.AITool{Plugin: appToolsOwner, Name: name}, root)
}

// checkPluginTool is Create mode's check of a plugin it wrote, by the
// rules install reads it with (check-written-plugin.ts).
func (a *App) checkPluginTool(ctx context.Context, window string) ai.Tool {
	return a.appWindowTool(ctx, window, "check_plugin",
		"Checks a plugin you wrote before you give it to the user, as Textus will read it at install: the manifest, the `local.` id, the files `main` names, the permissions the code needs, commands and views declared but never registered, menu conditions Textus never sets. Pass every file of the plugin. Fix what it reports and check again until it reports nothing.",
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"files": map[string]any{
					"type":        "array",
					"description": "Every file of the plugin: textus-plugin.json, host.js and the rest.",
					"items": map[string]any{
						"type":       "object",
						"properties": map[string]any{"path": map[string]any{"type": "string"}, "text": map[string]any{"type": "string"}},
						"required":   []any{"path", "text"},
					},
				},
			},
			"required": []any{"files"},
		}, []ai.AgentMode{ai.ModeCreate}, "")
}

// folderTools are what the AI's folder's notes hold, read from the index
// plugins read: offered with a folder for the AI, which goes along with
// each call as the folder it works in.
func (a *App) folderTools(ctx context.Context, asking, root string) []ai.Tool {
	if asking == "" || root == "" {
		return nil
	}
	window := a.editorWindowOf(asking)
	text := func(what string) map[string]any { return map[string]any{"type": "string", "description": what} }
	object := func(properties map[string]any, required ...any) map[string]any {
		schema := map[string]any{"type": "object", "properties": properties}
		if len(required) > 0 {
			schema["required"] = required
		}
		return schema
	}
	return []ai.Tool{
		a.appWindowTool(ctx, window, "list_tasks",
			"Lists the Markdown tasks (- [ ] ...) in the open folder's notes - open ones unless asked otherwise - each with its document path, line (from 1), due date and text. Filters by status, document, tag, due date and words.",
			object(map[string]any{
				"status":     map[string]any{"type": "string", "enum": []any{"open", "done", "all"}, "description": "Which tasks: open (the default), done or all."},
				"path":       text("Only this document's tasks: its path in the folder."),
				"tag":        text("Only tasks with this #tag."),
				"due_before": text("Only tasks due on or before this date, YYYY-MM-DD."),
				"query":      text("Only tasks whose text holds these words."),
				"limit":      map[string]any{"type": "integer", "description": "At most this many; 100 by default."},
			}), ai.EveryMode, root),
		a.appWindowTool(ctx, window, "backlinks",
			"Lists the notes that link to a note, each with the line (from 1) and the link's text: what refers to it across the folder.",
			object(map[string]any{"path": text("The note's path in the folder, as find_files gives it.")}, "path"), ai.EveryMode, root),
		a.appWindowTool(ctx, window, "list_tags",
			"Lists the #tags used in the open folder's notes (in the text and the front matter), the most used first, with how many notes use each.",
			object(map[string]any{}), ai.EveryMode, root),
		a.appWindowTool(ctx, window, "notes_with_tag",
			"Lists the notes that carry a #tag (nested tags under it too, as #project finds #project/alpha), with their titles.",
			object(map[string]any{"tag": text("The tag, with or without #.")}, "tag"), ai.EveryMode, root),
	}
}

// readPluginTool is Create mode's look at the plugins installed when the
// request starts, to change one from its own code.
func (a *App) readPluginTool() ai.Tool {
	installed := a.installedPlugins()
	spec := aicompat.ToolSpec{Name: plugins.ReadPluginToolName, Description: plugins.ReadPluginDescription, Parameters: plugins.ReadPluginParameters()}
	return ai.Tool{Spec: spec, Access: ai.ReadOnly, Modes: []ai.AgentMode{ai.ModeCreate}, Text: func(_ *ai.ToolContext, arguments string) string {
		return plugins.ReadPluginAnswer(installed, arguments)
	}}
}

// installedTools are the tools of the MCP servers the user turned on, and
// of the asking window's plugins the user gave ai.tools: what every mode
// and the paper's researchers offer.
func (a *App) installedTools(ctx context.Context, asking string) []ai.Tool {
	return append(a.mcpTools(ctx), a.pluginToolsFor(ctx, asking)...)
}

func init() {
	// The tools a window's plugins offer now; none when it closes or its
	// plugins are off.
	command("plugin_ai_tools_set", func(c *Call, a struct {
		Tools []plugins.AITool `json:"tools"`
	}) (nothing, error) {
		c.app.plug.mu.Lock()
		defer c.app.plug.mu.Unlock()
		if c.app.plug.offered == nil {
			c.app.plug.offered = map[string][]plugins.AITool{}
		}
		if len(a.Tools) == 0 {
			delete(c.app.plug.offered, c.Label())
		} else {
			c.app.plug.offered[c.Label()] = a.Tools
		}
		return nil, nil
	})
	// What a plugin's tool answered (text), or why it couldn't (error).
	command("plugin_ai_tool_result", func(c *Call, a struct {
		CallID string  `json:"callId"`
		Text   *string `json:"text"`
		Error  *string `json:"error"`
	}) (nothing, error) {
		c.app.plug.mu.Lock()
		answered := c.app.plug.waiting[a.CallID]
		delete(c.app.plug.waiting, a.CallID)
		c.app.plug.mu.Unlock()
		if answered != nil {
			answered <- toolAnswer{text: deref(a.Text), err: a.Error}
		}
		return nil, nil
	})
}
