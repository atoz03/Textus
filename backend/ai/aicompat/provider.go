package aicompat

import (
	"context"
	"net/http"
	"strconv"
	"strings"
)

// One agent step against whichever provider a request goes to: the
// dialect picked, the step's thinking planned, and the provider's own web
// search switched on in that dialect's shape.

// EndpointKind is where an agent step goes.
type EndpointKind uint8

const (
	// CodexEndpoint is a ChatGPT sign-in: the ChatGPT backend's Responses
	// API.
	CodexEndpoint EndpointKind = iota
	// OpenAIKeyEndpoint is the public Responses API, with an API key.
	OpenAIKeyEndpoint
	// AnthropicEndpoint is Claude's Messages API, by sign-in or API key.
	AnthropicEndpoint
	// ChatEndpoint is an OpenAI-compatible Chat Completions server: a
	// catalog provider, or the user's own endpoint.
	ChatEndpoint
)

// Endpoint is where an agent step goes, with the credentials it needs.
type Endpoint struct {
	Kind EndpointKind
	// AccessToken, AccountID and Originator are a ChatGPT sign-in's: its
	// token, its account, and the originator header the backend expects.
	AccessToken, AccountID, Originator string
	// APIKey is OpenAI's, or a Chat Completions server's ("" for none).
	APIKey string
	// Anthropic is Claude's auth.
	Anthropic AnthropicAuth
	// BaseURL is a Chat Completions server's, Vendor which server it is.
	BaseURL string
	Vendor  Vendor
	// StreamUsage: see ProviderCaps.StreamUsage.
	StreamUsage bool
}

// AgentProvider is the provider, model and settings one agent request runs
// on, built once per request; Step is what the loop calls each step.
type AgentProvider struct {
	// ID is the catalog id: the capability table's key for thinking and
	// search.
	ID, Model string
	Endpoint  Endpoint
	// Thinking is how hard the model thinks, planned per step.
	Thinking ThinkingLevel
	// NativeSearch is the provider's own web search, nil when the request
	// doesn't search with it.
	NativeSearch *NativeSearch
	// Client sends the requests. It has no total timeout - a step streams
	// for as long as the model writes - and the stream's own watchdog
	// gives up on one that went silent.
	Client *http.Client
}

// FallsBackToPlainChat reports whether a request that fails on this
// provider is answered again without tools. Not every OpenAI-compatible
// server implements tools properly; the first-party APIs do, and their
// errors are real ones.
func (p AgentProvider) FallsBackToPlainChat() bool { return p.Endpoint.Kind == ChatEndpoint }

// Step is one round trip: the model's answer to history, streamed through
// on, and the step's token usage where the provider reports it (nil
// where not). Thinking parameters the provider refuses are dropped and the
// step sent again.
func (p AgentProvider) Step(ctx context.Context, instructions string, history []AgentTurn, tools []ToolSpec, on EventSink) (StepResult, *StepUsage, error) {
	type answer struct {
		step  StepResult
		usage *StepUsage
	}
	a, err := withFallback(p.ID, p.Model, Plan(p.ID, p.Model, p.Thinking), func(thinking Thinking) (answer, error) {
		step, usage, err := p.stepOnce(ctx, instructions, history, tools, thinking, on)
		return answer{step, usage}, err
	})
	return a.step, a.usage, err
}

func (p AgentProvider) stepOnce(ctx context.Context, instructions string, history []AgentTurn, tools []ToolSpec, thinking Thinking, on EventSink) (StepResult, *StepUsage, error) {
	e := p.Endpoint
	searches := p.NativeSearch != nil
	switch e.Kind {
	case CodexEndpoint:
		sign := func(req *http.Request) {
			req.Header.Set("Authorization", "Bearer "+e.AccessToken)
			req.Header.Set("chatgpt-account-id", e.AccountID)
			req.Header.Set("originator", e.Originator)
			req.Header.Set("OpenAI-Beta", "responses=experimental")
		}
		return responsesStep(ctx, p.Client, CodexResponsesURL, sign, "Codex", p.Model, instructions, history, tools, searches, thinking, on)
	case OpenAIKeyEndpoint:
		sign := func(req *http.Request) { req.Header.Set("Authorization", "Bearer "+e.APIKey) }
		return responsesStep(ctx, p.Client, OpenAIResponsesURL, sign, "OpenAI", p.Model, instructions, history, tools, searches, thinking, on)
	case AnthropicEndpoint:
		step, usage, err := anthropicStep(ctx, p.Client, e.Anthropic, instructions, history, tools, searches, thinking, p.Model, on)
		return step, &usage, err
	}
	server := chatServer{baseURL: e.BaseURL, key: e.APIKey, vendor: e.Vendor}
	// xAI searches only on its /responses.
	if p.NativeSearch != nil && p.NativeSearch.Kind == CompatibleResponsesSearch {
		return compatibleResponsesStep(ctx, p.Client, server, p.Model, instructions, history, tools, thinking, on)
	}
	native := NoWebSearch
	if p.NativeSearch != nil && p.NativeSearch.Kind == ChatSearch {
		native = p.NativeSearch.Chat
	}
	// Streaming support varies across compatible servers even more than
	// tools do, so a step the server refused as asked is sent once more
	// without streaming (and without thinking parameters, which some take
	// only when streaming). Anything else - a key, a limit, a cut-off
	// reply - is said as it is, not paid for twice.
	step, usage, err := chatStepStreaming(ctx, p.Client, server, p.Model, instructions, history, tools, native, thinking, e.StreamUsage, on)
	if err == nil || !streamingRefused(err) {
		return step, usage, err
	}
	return chatStep(ctx, p.Client, server, p.Model, instructions, history, tools, native, thinking.WithoutParameters(), on)
}

// streamingRefused reports whether a streamed step's error reads as the
// server refusing the request as made (a 4xx other than a key, a
// permission, a rate or a balance), which a request without streaming may
// get past.
func streamingRefused(err error) bool {
	if IsThinkingRefusal(err) {
		return false
	}
	text := err.Error()
	_, rest, ok := strings.Cut(text, "failed (")
	if !ok || len(rest) < 3 {
		return false
	}
	status, convErr := strconv.Atoi(rest[:3])
	if convErr != nil || status < 400 || status > 499 {
		return false
	}
	switch ErrorKind(text) {
	case "auth", "rateLimit", "balance":
		return false
	}
	return true
}
