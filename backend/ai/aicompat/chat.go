package aicompat

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json/jsontext"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
)

// OpenAI-compatible Chat Completions, as the catalog's providers and the
// user's own endpoints serve it: an agent step's request (the conversation
// as chat messages, the tools, each vendor's own fields) and its reply,
// streamed or whole.

// Vendor is which server is behind the dialect, where one's defaults differ
// from OpenAI's enough to matter.
type Vendor uint8

const (
	Generic Vendor = iota
	// Ark is Volcengine Ark (Doubao). Its Seed models think by default, and
	// it caps an answer at 4096 tokens unless max_tokens says otherwise.
	Ark
	// DeepSeek's models think by default, at effort high.
	DeepSeek
	// MiniMax writes its thinking into the answer between <think> tags
	// unless reasoning_split asks for it apart.
	MiniMax
)

// ChatVendor is the server a base URL points at: by host rather than
// catalog id, so a custom endpoint with an Ark URL is Ark too.
func ChatVendor(baseURL string) Vendor {
	switch {
	case strings.Contains(baseURL, ".volces.com"):
		return Ark
	case strings.Contains(baseURL, "api.deepseek.com"):
		return DeepSeek
	case strings.Contains(baseURL, "api.minimaxi.com"), strings.Contains(baseURL, "api.minimax.io"):
		return MiniMax
	}
	return Generic
}

// arkAgentMaxTokens is Ark's answer cap for agent steps.
const arkAgentMaxTokens = 16384

// opencodeSession is one session id for this run of the app: stable, so
// OpenCode's routing and prompt cache hold across a conversation's steps.
var opencodeSession = sync.OnceValue(func() string {
	b := make([]byte, 8)
	rand.Read(b)
	return "textus-" + hex.EncodeToString(b)
})

// WithKey puts key on req as the server at baseURL takes it. Google's APIs
// (Vertex reads a bearer token as OAuth) get x-goog-api-key, except
// Gemini's OpenAI-compatible layer, which wants a bearer like everyone
// else; OpenCode refuses a request without a session id to route it by.
func WithKey(req *http.Request, baseURL, key string) {
	host := ""
	if u, err := url.Parse(baseURL); err == nil {
		host = u.Hostname()
	}
	switch {
	case host == "generativelanguage.googleapis.com":
		req.Header.Set("Authorization", "Bearer "+key)
	case strings.HasSuffix(host, ".googleapis.com"):
		req.Header.Set("x-goog-api-key", key)
	case host == "opencode.ai":
		req.Header.Set("Authorization", "Bearer "+key)
		req.Header.Set("x-opencode-session", opencodeSession())
	default:
		req.Header.Set("Authorization", "Bearer "+key)
	}
}

// chatServer is where a Chat Completions request goes.
type chatServer struct {
	baseURL string
	// key is "" for a server that takes none.
	key    string
	vendor Vendor
}

func (s chatServer) url(path string) string { return strings.TrimRight(s.baseURL, "/") + path }

func (s chatServer) sign(req *http.Request) {
	if s.key != "" {
		WithKey(req, s.baseURL, s.key)
	}
}

// chatBody is one agent step's body; `tools` support varies a lot from
// server to server, so the caller falls back to a plain chat on a hard
// failure.
func chatBody(model, instructions string, history []AgentTurn, tools []ToolSpec, search NativeWebSearch, vendor Vendor, thinking Thinking) map[string]any {
	schemas := make([]any, 0, len(tools)+1)
	for _, tool := range tools {
		schemas = append(schemas, map[string]any{
			"type":     "function",
			"function": map[string]any{"name": tool.Name, "description": tool.Description, "parameters": tool.Parameters},
		})
	}
	switch search {
	case GroqSearch:
		schemas = append(schemas, map[string]any{"type": "browser_search"})
	case OpenRouterSearch:
		schemas = append(schemas, map[string]any{"type": "openrouter:web_search"})
	case ZhipuSearch:
		schemas = append(schemas, map[string]any{"type": "web_search", "web_search": map[string]any{"enable": true, "search_result": true}})
	}
	body := map[string]any{
		"model":       model,
		"messages":    chatMessages(instructions, history, thinking.Replay),
		"tools":       schemas,
		"tool_choice": "auto",
	}
	if search == QwenSearch {
		body["enable_search"] = true
	}
	switch vendor {
	case Ark:
		body["max_tokens"] = arkAgentMaxTokens
	case MiniMax:
		body["reasoning_split"] = true
	}
	thinking.applyChatCompletions(body)
	return body
}

// userWithImages is a user message of image parts, then text.
func userWithImages(images []ImageAttachment, text string) map[string]any {
	var content []any
	for _, image := range images {
		content = append(content, map[string]any{"type": "image_url", "image_url": map[string]any{"url": image.DataURL()}})
	}
	content = append(content, map[string]any{"type": "text", "text": text})
	return map[string]any{"role": "user", "content": content}
}

// chatMessages groups the flat history into chat messages: every call of a
// step in one assistant message's tool_calls, each result its own tool
// message after it. A new step only appends, so servers that cache prompt
// prefixes keep earlier steps cached.
func chatMessages(instructions string, history []AgentTurn, replayReasoning bool) []any {
	messages := []any{map[string]any{"role": "system", "content": instructions}}
	for i := 0; i < len(history); {
		turn := history[i]
		switch turn.Kind {
		case User:
			// Plain-string content unless there are images: a server that
			// predates vision may refuse the parts array.
			if len(turn.Images) == 0 {
				messages = append(messages, map[string]any{"role": "user", "content": turn.Text})
			} else {
				messages = append(messages, userWithImages(turn.Images, turn.Text))
			}
			i++
			continue
		case Assistant:
			messages = append(messages, map[string]any{"role": "assistant", "content": turn.Text})
			i++
			continue
		}
		var (
			calls, results []any
			images         []ImageAttachment
			reasoning      *string
		)
	step:
		for ; i < len(history); i++ {
			t := history[i]
			switch {
			case t.Kind == ToolCalled && len(results) > 0:
				break step // the next step's calls
			case t.Kind == ToolCalled:
				text, extra := keptParts(t.Reasoning)
				if reasoning == nil {
					reasoning = text
				}
				call := map[string]any{"id": t.CallID, "type": "function", "function": map[string]any{"name": t.Name, "arguments": t.Arguments}}
				if extra != nil {
					call["extra_content"] = extra
				}
				calls = append(calls, call)
			case t.Kind == ToolResult:
				results = append(results, map[string]any{"role": "tool", "tool_call_id": t.CallID, "content": t.Output})
				images = append(images, t.Images...)
			default:
				break step
			}
		}
		if len(calls) > 0 {
			message := map[string]any{"role": "assistant", "content": nil, "tool_calls": calls}
			if replayReasoning && reasoning != nil {
				message["reasoning_content"] = *reasoning
			}
			messages = append(messages, message)
		}
		messages = append(messages, results...)
		// A tool message is text only, so the step's images follow its
		// results as one user message.
		if len(images) > 0 {
			messages = append(messages, userWithImages(images, ToolImagesNote(images)))
		}
	}
	return messages
}

const (
	reasoningContent = "reasoning_content"
	extraContent     = "extra_content"
)

// reasoningOf is the reasoning a message or delta carries: DeepSeek, Kimi,
// Qwen and GLM call it reasoning_content; OpenRouter, Groq and Ollama
// reasoning.
func reasoningOf(message any) (string, bool) {
	for _, key := range []string{reasoningContent, "reasoning"} {
		if s, ok := str(at(message, key)); ok {
			return s, true
		}
	}
	return "", false
}

// callExtra is a call's extra_content as its turn keeps it. Gemini 3 sends
// a thought signature there and refuses the next request when a call comes
// back without it.
func callExtra(extra any) jsontext.Value {
	return rawOf(map[string]any{extraContent: extra})
}

// attachReasoning puts a step's reasoning on its first call: the text, or
// beside the call's callExtra.
func attachReasoning(calls []AgentTurn, text string) {
	if text == "" || len(calls) == 0 || calls[0].Kind != ToolCalled {
		return
	}
	if kept, ok := decode(string(calls[0].Reasoning)).(map[string]any); ok {
		kept[reasoningContent] = text
		calls[0].Reasoning = rawOf(kept)
		return
	}
	calls[0].Reasoning = rawOf(text)
}

// keptParts are a kept call's reasoning text and extra_content, from
// either shape attachReasoning and callExtra leave.
func keptParts(kept jsontext.Value) (text *string, extra any) {
	if len(kept) == 0 {
		return nil, nil
	}
	switch v := decode(string(kept)).(type) {
	case string:
		return &v, nil
	case map[string]any:
		if s, ok := v[reasoningContent].(string); ok {
			text = &s
		}
		return text, v[extraContent]
	}
	return nil, nil
}

// parseChatMessage reads choices[0].message as a step, the pages a native
// search cited marked and reported.
func parseChatMessage(message any, report *searchReport, on EventSink) StepResult {
	var calls []AgentTurn
	for _, tc := range list(at(message, "tool_calls")) {
		name, ok := str(at(tc, "function", "name"))
		if !ok {
			continue
		}
		arguments, ok := str(at(tc, "function", "arguments"))
		if !ok {
			arguments = "{}"
		}
		turn := AgentTurn{Kind: ToolCalled, CallID: strAt(tc, "id"), Name: name, Arguments: arguments}
		if extra := at(tc, extraContent); extra != nil {
			turn.Reasoning = callExtra(extra)
		}
		calls = append(calls, turn)
	}
	text := strAt(message, "content")
	if len(calls) == 0 {
		return Reply(report.cite(text, on))
	}
	reasoning, _ := reasoningOf(message)
	attachReasoning(calls, reasoning)
	return CallsWithText(calls, report.cite(text, on))
}

// callIDs are the ids a step's calls go by. Some servers number calls from
// call_0 in every reply or send no id at all, and the app keys a proposal
// by its call's id, so a call keeps its id unless it is taken or missing.
type callIDs struct{ taken map[string]bool }

func callIDsOf(history []AgentTurn) callIDs {
	ids := callIDs{taken: map[string]bool{}}
	for _, turn := range history {
		if turn.Kind == ToolCalled {
			ids.taken[turn.CallID] = true
		}
	}
	return ids
}

// claim is the id the call at index goes by: sent, or one made from it.
func (c *callIDs) claim(sent string, index int) string {
	base := sent
	if base == "" {
		base = fmt.Sprintf("call_%d", index)
	}
	id := base
	for n := 2; c.taken[id]; n++ {
		id = fmt.Sprintf("%s~%d", base, n)
	}
	c.taken[id] = true
	return id
}

// chatStepStreaming is one streamed round trip; the usage comes in a last
// chunk only where asked for (streamUsage) and the server takes the ask.
func chatStepStreaming(ctx context.Context, client *http.Client, server chatServer, model, instructions string, history []AgentTurn, tools []ToolSpec, search NativeWebSearch, thinking Thinking, streamUsage bool, on EventSink) (StepResult, *StepUsage, error) {
	body := chatBody(model, instructions, history, tools, search, server.vendor, thinking)
	body["stream"] = true
	if streamUsage {
		body["stream_options"] = map[string]any{"include_usage": true}
	}
	resp, err := request(ctx, client, server.url("/chat/completions"), body, server.sign)
	if err != nil {
		return StepResult{}, nil, err
	}
	acc := chatStream{ids: callIDsOf(history)}
	if err := readSSE(resp, "Custom endpoint", func(data string) { acc.apply(data, on) }); err != nil {
		return StepResult{}, nil, err
	}
	if acc.err != "" {
		return StepResult{}, nil, fmt.Errorf("%s", acc.err)
	}
	if cut := acc.cutOff(); cut != "" {
		return StepResult{}, nil, fmt.Errorf("%s", cut)
	}
	return acc.result(on), acc.usage, nil
}

// chatStep is one round trip answered whole: what a server that refused
// streaming gets.
func chatStep(ctx context.Context, client *http.Client, server chatServer, model, instructions string, history []AgentTurn, tools []ToolSpec, search NativeWebSearch, thinking Thinking, on EventSink) (StepResult, *StepUsage, error) {
	body := chatBody(model, instructions, history, tools, search, server.vendor, thinking)
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	parsed, err := postJSON(ctx, client, server.url("/chat/completions"), body, server.sign, "Custom endpoint request failed")
	if err != nil {
		return StepResult{}, nil, err
	}
	var message any
	if choices := list(at(parsed, "choices")); len(choices) > 0 {
		message = at(choices[0], "message")
	}
	if m := object(message); m != nil && thinking.thoughtsInText() {
		if text, ok := m["content"].(string); ok {
			thought, answer := splitGeminiThoughts(text)
			m["content"] = answer
			if thought != "" {
				m[reasoningContent] = thought
			}
		}
	}
	var report searchReport
	report.take(message, parsed)
	var usage *StepUsage
	if u, ok := openAIUsage(at(parsed, "usage")); ok {
		usage = &u
	}
	step := parseChatMessage(message, &report, on)
	ids := callIDsOf(history)
	index := 0
	for i := range step.Calls {
		if step.Calls[i].Kind == ToolCalled {
			step.Calls[i].CallID = ids.claim(step.Calls[i].CallID, index)
			index++
		}
	}
	return step, usage, nil
}
