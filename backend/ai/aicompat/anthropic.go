package aicompat

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"net/http"
	"slices"
	"strings"
)

// Claude's Messages API: an agent step's request (the conversation as
// content blocks, its cache breakpoints, the tools) and the reply read
// back, streamed.

// AnthropicMessagesURL is where Messages requests go; tests point it
// elsewhere.
var AnthropicMessagesURL = "https://api.anthropic.com/v1/messages"

const (
	// claudeCodeIdentity opens the system prompt of a request made with a
	// Claude sign-in: its tokens are taken only from Claude Code.
	claudeCodeIdentity = "You are Claude Code, Anthropic's official CLI for Claude."
	// anthropicAgentMaxTokens is room for a rewritten section inside one
	// propose_edit call; Deep thinking spends from the same limit, so it
	// gets twice the room.
	anthropicAgentMaxTokens = 16384
	anthropicDeepMaxTokens  = 32768
	// maxPauseContinuations is how many times a turn Claude paused
	// (pause_turn: a long run of server searches) is continued before what
	// it has so far is taken as the step.
	maxPauseContinuations = 3
)

// AnthropicAuth is how a request to Anthropic is signed: an API key, or a
// Claude sign-in's token. A sign-in is taken only with Claude Code's
// identity (its headers, and the system prompt's first block); an API key
// must not claim it.
type AnthropicAuth struct {
	APIKey, OAuthToken string
}

// Sign puts the auth and API version on req.
func (a AnthropicAuth) Sign(req *http.Request) {
	req.Header.Set("anthropic-version", "2023-06-01")
	if a.OAuthToken != "" {
		req.Header.Set("Authorization", "Bearer "+a.OAuthToken)
		req.Header.Set("anthropic-beta", "claude-code-20250219,oauth-2025-04-20")
		req.Header.Set("anthropic-dangerous-direct-browser-access", "true")
		req.Header.Set("User-Agent", "claude-cli/1.0.0")
		req.Header.Set("x-app", "cli")
		return
	}
	req.Header.Set("x-api-key", a.APIKey)
}

// AnthropicSystem is a request's system blocks for instructions, the last
// a cache breakpoint: they come first, unchanged from request to request.
func AnthropicSystem(auth AnthropicAuth, instructions string) []any {
	var blocks []any
	if auth.OAuthToken != "" {
		blocks = append(blocks, map[string]any{"type": "text", "text": claudeCodeIdentity})
	}
	return append(blocks, map[string]any{"type": "text", "text": instructions, "cache_control": ephemeral()})
}

func ephemeral() map[string]any { return map[string]any{"type": "ephemeral"} }

// anthropicStep is one streamed round trip with the whole history and the
// tools, and its token usage. A turn with server searches can stop part
// way at pause_turn; it is continued with what Claude sent so far as the
// last assistant message.
func anthropicStep(ctx context.Context, client *http.Client, auth AnthropicAuth, instructions string, history []AgentTurn, tools []ToolSpec, webSearch bool, thinking Thinking, model string, on EventSink) (StepResult, StepUsage, error) {
	var content []any
	var usage StepUsage
	for round := 0; round <= maxPauseContinuations; round++ {
		body := anthropicBody(auth, instructions, history, tools, webSearch, thinking, model)
		if len(content) > 0 {
			body["messages"] = append(body["messages"].([]any), map[string]any{"role": "assistant", "content": slices.Clone(content)})
		}
		maxTokens, _ := body["max_tokens"].(int)
		resp, err := request(ctx, client, AnthropicMessagesURL, body, auth.Sign)
		if err != nil {
			return StepResult{}, usage, err
		}
		var acc anthropicStream
		if err := readSSE(resp, "Claude", func(data string) { acc.apply(data, on) }); err != nil {
			return StepResult{}, usage, err
		}
		if acc.err != "" {
			return StepResult{}, usage, errors.New(acc.err)
		}
		if cut := acc.cutOff(maxTokens); cut != "" {
			return StepResult{}, usage, errors.New(cut)
		}
		usage.Add(acc.usage)
		content = append(content, acc.content()...)
		if acc.stopReason != "pause_turn" {
			break
		}
	}
	return parseAnthropicContent(content, on), usage, nil
}

// anthropicBody is one agent step's body, with three explicit cache
// breakpoints plus the automatic one (the API's limit of four): the last
// system block, the two in the history, and the top-level one that follows
// the request's end so each tool step reads the step before it from cache.
func anthropicBody(auth AnthropicAuth, instructions string, history []AgentTurn, tools []ToolSpec, webSearch bool, thinking Thinking, model string) map[string]any {
	schemas := make([]any, 0, len(tools)+1)
	for _, tool := range tools {
		// eager_input_streaming: the arguments stream as Claude writes them,
		// not each value whole. They may then be invalid JSON; a call that
		// doesn't parse is refused like any other.
		schemas = append(schemas, map[string]any{
			"name": tool.Name, "description": tool.Description,
			"input_schema": tool.Parameters, "eager_input_streaming": true,
		})
	}
	// Claude runs this server tool inside the request. Newer versions route
	// the search through code execution by default; this one searches
	// directly, on every model.
	if webSearch {
		schemas = append(schemas, map[string]any{"type": "web_search_20250305", "name": "web_search", "max_uses": 5})
	}
	maxTokens := anthropicAgentMaxTokens
	if thinking.Show {
		maxTokens = anthropicDeepMaxTokens
	}
	body := map[string]any{
		"model":         model,
		"max_tokens":    maxTokens,
		"stream":        true,
		"system":        AnthropicSystem(auth, instructions),
		"messages":      anthropicCachedMessages(history),
		"tools":         schemas,
		"cache_control": ephemeral(),
	}
	thinking.applyAnthropic(body)
	return body
}

func imageBlock(image ImageAttachment) map[string]any {
	return map[string]any{
		"type":   "image",
		"source": map[string]any{"type": "base64", "media_type": image.Mime, "data": image.DataBase64},
	}
}

// toolInput is a call's arguments as the object Claude sent back: {} for
// arguments that aren't one.
func toolInput(arguments string) any {
	if _, ok := decode(arguments).(map[string]any); ok {
		return jsontext.Value(strings.TrimSpace(arguments))
	}
	return map[string]any{}
}

// anthropicMessages groups the flat history into Messages API turns. Roles
// must alternate, so a step's calls form one assistant message of tool_use
// blocks and its results one user message of tool_result blocks.
func anthropicMessages(history []AgentTurn) []any {
	var messages []any
	for i := 0; i < len(history); {
		turn := history[i]
		switch turn.Kind {
		case User:
			// A text-only turn keeps the plain-string content, so the common
			// request stays as it was before images.
			if len(turn.Images) == 0 {
				messages = append(messages, map[string]any{"role": "user", "content": turn.Text})
			} else {
				var content []any
				for _, image := range turn.Images {
					content = append(content, imageBlock(image))
				}
				content = append(content, map[string]any{"type": "text", "text": turn.Text})
				messages = append(messages, map[string]any{"role": "user", "content": content})
			}
			i++
			continue
		case Assistant:
			// Claude refuses an empty assistant message on replay, which
			// would fail every later send; the API merges the neighbours.
			if strings.TrimSpace(turn.Text) != "" {
				messages = append(messages, map[string]any{"role": "assistant", "content": turn.Text})
			}
			i++
			continue
		}
		var uses, results []any
	step:
		for ; i < len(history); i++ {
			t := history[i]
			switch {
			case t.Kind == ToolCalled && len(results) > 0:
				break step // the next step's calls
			case t.Kind == ToolCalled:
				// The step's thinking blocks, ahead of its first tool_use:
				// thinking with tools needs them back, signatures untouched.
				if len(uses) == 0 && len(t.Reasoning) > 0 {
					var blocks []jsontext.Value
					if json.Unmarshal(t.Reasoning, &blocks) == nil {
						for _, b := range blocks {
							uses = append(uses, b)
						}
					}
				}
				uses = append(uses, map[string]any{"type": "tool_use", "id": t.CallID, "name": t.Name, "input": toolInput(t.Arguments)})
			case t.Kind == ToolResult:
				// A tool_result takes image blocks next to its text, so a
				// tool's images stay in its result.
				var content any = t.Output
				if len(t.Images) > 0 {
					blocks := []any{map[string]any{"type": "text", "text": t.Output}}
					for _, image := range t.Images {
						blocks = append(blocks, imageBlock(image))
					}
					content = blocks
				}
				results = append(results, map[string]any{"type": "tool_result", "tool_use_id": t.CallID, "content": content})
			default:
				break step
			}
		}
		if len(uses) > 0 {
			messages = append(messages, map[string]any{"role": "assistant", "content": uses})
		}
		if len(results) > 0 {
			messages = append(messages, map[string]any{"role": "user", "content": results})
		}
	}
	return messages
}

// markCacheBreakpoint makes the last content block of message a cache
// breakpoint. A plain-string content becomes the one text block it stands
// for, so the cache entry still matches once the marker moves on.
func markCacheBreakpoint(message any) {
	m, ok := message.(map[string]any)
	if !ok {
		return
	}
	if text, ok := m["content"].(string); ok {
		m["content"] = []any{map[string]any{"type": "text", "text": text}}
	}
	if blocks, ok := m["content"].([]any); ok && len(blocks) > 0 {
		if last, ok := blocks[len(blocks)-1].(map[string]any); ok {
			last["cache_control"] = ephemeral()
		}
	}
}

// anthropicCachedMessages is anthropicMessages with the conversation's two
// cache breakpoints. The message being sent carries the document and
// history keeps it without, so only the history is shared by later
// requests: the first breakpoint goes on its last block, the second on the
// last block before the newest exchange, where the previous request put
// its first, so that entry is read back however many blocks the exchange
// added.
func anthropicCachedMessages(history []AgentTurn) []any {
	isUser := func(t AgentTurn) bool { return t.Kind == User }
	current := lastIndexFunc(history, isUser)
	if current < 0 {
		return anthropicMessages(history)
	}
	newest := max(lastIndexFunc(history[:current], isUser), 0)
	// Each slice starts at a user turn, which no message spans, so they
	// render as they would together.
	messages := anthropicMessages(history[:newest])
	if len(messages) > 0 {
		markCacheBreakpoint(messages[len(messages)-1])
	}
	messages = append(messages, anthropicMessages(history[newest:current])...)
	if len(messages) > 0 {
		markCacheBreakpoint(messages[len(messages)-1])
	}
	return append(messages, anthropicMessages(history[current:])...)
}

func lastIndexFunc[T any](s []T, f func(T) bool) int {
	for i := len(s) - 1; i >= 0; i-- {
		if f(s[i]) {
			return i
		}
	}
	return -1
}

// parseAnthropicContent reads a reply's content blocks as a step. Text
// blocks join as they stand, since a cited reply comes as many short
// blocks; text after a server block starts a new paragraph. A tool step's
// thinking blocks ride on its first call, verbatim.
func parseAnthropicContent(content []any, on EventSink) StepResult {
	var (
		calls      []AgentTurn
		text       strings.Builder
		afterOther bool
		citations  stepCitations
		thinking   []any
	)
	for _, block := range content {
		switch strAt(block, "type") {
		case "tool_use":
			input := at(block, "input")
			if input == nil {
				input = map[string]any{}
			}
			calls = append(calls, AgentTurn{Kind: ToolCalled, CallID: strAt(block, "id"), Name: strAt(block, "name"), Arguments: compact(input)})
		case "thinking", "redacted_thinking":
			thinking = append(thinking, block)
		case "text":
			if afterOther && strings.TrimSpace(text.String()) != "" {
				text.WriteString("\n\n")
			}
			afterOther = false
			text.WriteString(strAt(block, "text"))
			for _, citation := range list(at(block, "citations")) {
				url, ok := str(at(citation, "url"))
				if !ok {
					continue
				}
				marker := citations.cite(url, strAt(citation, "title"), on)
				if !strings.HasSuffix(text.String(), marker) {
					text.WriteString(marker)
				}
			}
		default:
			afterOther = true
		}
	}
	if len(calls) == 0 {
		return Reply(text.String())
	}
	if len(thinking) > 0 {
		calls[0].Reasoning = rawOf(thinking)
	}
	return CallsWithText(calls, text.String())
}
