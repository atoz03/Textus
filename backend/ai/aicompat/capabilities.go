package aicompat

import (
	_ "embed"
	"encoding/json/v2"
	"strings"
	"sync"
)

// The capability table: what each provider and model can be asked to do
// beyond plain chat, and how each request has to say it. The provider rows
// are written here; each model's reasoning comes from
// model-capabilities.json, which scripts/sync-models.mjs makes.

// ThinkingLevel is how hard the model is asked to think (ThinkingLevel in
// src/platform/bindings/ai-agent.ts). The user sees one button, Deep
// thinking; the other two are picked by the surface.
type ThinkingLevel string

const (
	// Fast is Quick Ask: as little as the model allows.
	Fast ThinkingLevel = "fast"
	// Auto is the panel with Deep thinking off: the provider's own
	// default, so nothing is sent.
	Auto ThinkingLevel = "auto"
	// Deep is Deep thinking on: `high` where the model has levels (never
	// `xhigh` or `max`, which cost several times as much), on where it only
	// switches.
	Deep ThinkingLevel = "deep"
)

// ThinkingFormat is how a provider takes thinking parameters on the wire.
type ThinkingFormat uint8

const (
	// NoThinking: nothing is ever sent, and Deep thinking isn't offered.
	NoThinking ThinkingFormat = iota
	// OpenAIThinking: `reasoning_effort` in Chat Completions,
	// `reasoning.effort` in the Responses API; off as the effort `none`.
	OpenAIThinking
	// GeminiThinking: `extra_body.google.thinking_config`.
	GeminiThinking
	// AnthropicThinking: `thinking` (adaptive, disabled, or a budget on
	// older models) and `output_config.effort`.
	AnthropicThinking
	// ThinkingType: `thinking: {type: "enabled" | "disabled"}`, plus
	// `reasoning_effort` where the model has levels.
	ThinkingType
	// QwenThinking: DashScope's `enable_thinking` and `thinking_budget`.
	QwenThinking
	// OpenRouterThinking: `reasoning: {effort | enabled}`.
	OpenRouterThinking
)

// NativeWebSearch is a vendor's switch for its own web search on an
// otherwise OpenAI-compatible Chat Completions request.
type NativeWebSearch uint8

const (
	NoWebSearch NativeWebSearch = iota
	GroqSearch
	OpenRouterSearch
	QwenSearch
	ZhipuSearch
)

// SearchKind is how a provider's own (server-side) web search is switched
// on. The agent offers its local search tools either way.
type SearchKind uint8

const (
	NoSearch SearchKind = iota
	// ResponsesSearch is the Responses API's `web_search` tool (OpenAI).
	ResponsesSearch
	// AnthropicSearch is Claude's server-side `web_search` tool.
	AnthropicSearch
	// ChatSearch is a switch on a Chat Completions request (NativeSearch.Chat).
	ChatSearch
	// CompatibleResponsesSearch is the provider's OpenAI-compatible
	// `/responses` endpoint with `web_search`, in place of Chat Completions
	// while searching (xAI).
	CompatibleResponsesSearch
	// qwenSearch is Alibaba's: /responses for the newer Qwen generations,
	// `enable_search` for older ones (SearchRoute resolves it).
	qwenSearch
)

// NativeSearch is how a request searches with the provider's own search.
type NativeSearch struct {
	Kind SearchKind
	Chat NativeWebSearch
}

// ImageAPI is how a service is asked for a picture (ImageApi in
// src/platform/bindings/ai-agent.ts).
type ImageAPI string

// ProviderCaps is one provider's row.
type ProviderCaps struct {
	Thinking     ThinkingFormat
	NativeSearch NativeSearch
	// ReplayReasoning (Chat Completions): in thinking mode the provider
	// wants each step's `reasoning_content` back on the assistant message
	// that made the tool calls, for the rest of that exchange.
	ReplayReasoning bool
	// Image is how the same key asks for a picture, "" for none.
	Image ImageAPI
	// StreamUsage (Chat Completions): a streamed request may ask for its
	// token usage.
	StreamUsage bool
	// AutoEffort is the effort Auto asks for where the provider's default
	// thinks far longer than writing needs; "" sends nothing.
	AutoEffort string
	// Roomy: every model the provider serves reads 128k tokens or more.
	Roomy bool
}

// defaultCaps is a provider the table doesn't list - a new catalog entry,
// or "custom": plain OpenAI-compatible Chat Completions.
var defaultCaps = ProviderCaps{Thinking: OpenAIThinking}

var providerRows = map[string]ProviderCaps{
	"openai":    {Roomy: true, Thinking: OpenAIThinking, NativeSearch: NativeSearch{Kind: ResponsesSearch}, StreamUsage: true, Image: "open-ai"},
	"anthropic": {Roomy: true, Thinking: AnthropicThinking, NativeSearch: NativeSearch{Kind: AnthropicSearch}, StreamUsage: true},
	"google":    {Roomy: true, Thinking: GeminiThinking, Image: "gemini", StreamUsage: true},
	"deepseek":  {Roomy: true, Thinking: ThinkingType, ReplayReasoning: true, StreamUsage: true},
	"xai":       {Roomy: true, Thinking: OpenAIThinking, NativeSearch: NativeSearch{Kind: CompatibleResponsesSearch}, StreamUsage: true},
	// Mistral rejects unknown fields, and none of its chat models think.
	"mistral":    {Thinking: NoThinking},
	"groq":       {Thinking: OpenAIThinking, NativeSearch: NativeSearch{Kind: ChatSearch, Chat: GroqSearch}, StreamUsage: true},
	"openrouter": {Thinking: OpenRouterThinking, NativeSearch: NativeSearch{Kind: ChatSearch, Chat: OpenRouterSearch}, StreamUsage: true},
	// OpenCode Go serves DeepSeek, Kimi and GLM models on one endpoint:
	// their thinking switch, and their thoughts handed back with each tool
	// call, which they refuse a turn without.
	"opencode-go": {Thinking: ThinkingType, ReplayReasoning: true},
	"moonshot":    {Thinking: ThinkingType, ReplayReasoning: true, StreamUsage: true},
	"zhipu":       {Thinking: ThinkingType, NativeSearch: NativeSearch{Kind: ChatSearch, Chat: ZhipuSearch}, ReplayReasoning: true, Image: "zhipu"},
	"qwen":        {Thinking: QwenThinking, NativeSearch: NativeSearch{Kind: qwenSearch}, StreamUsage: true, Image: "dash-scope"},
	// MiniMax: M3 switches thinking on and off, M3.1 Flash only takes an
	// effort.
	"minimax":      {Roomy: true, Thinking: ThinkingType, StreamUsage: true, Image: "mini-max"},
	"minimax-intl": {Roomy: true, Thinking: ThinkingType, StreamUsage: true, Image: "mini-max"},
	// Ark takes `reasoning_effort` and maps it onto the models that only
	// switch, where `thinking.type: disabled` can be refused.
	"doubao": {AutoEffort: "minimal", Thinking: OpenAIThinking, ReplayReasoning: true, StreamUsage: true, Image: "seedream"},
	// Ollama's /v1 endpoint takes `reasoning_effort` and maps it onto its
	// own `think`.
	"ollama": {Thinking: OpenAIThinking, StreamUsage: true},
	"custom": defaultCaps,
}

// Provider is the provider's row, or the plain OpenAI-compatible default.
func Provider(id string) ProviderCaps {
	if caps, ok := providerRows[id]; ok {
		return caps
	}
	return defaultCaps
}

// HasRow reports whether the table has a row of its own for id.
func HasRow(id string) bool {
	_, ok := providerRows[id]
	return ok
}

// qwenUsesResponsesSearch: Alibaba searches with newer Qwen generations
// through its Responses API; older qwen-plus/flash models keep the Chat
// Completions `enable_search` switch.
func qwenUsesResponsesSearch(model string) bool {
	for _, prefix := range []string{"qwen3.8-", "qwen3.7-", "qwen3.6-", "qwen3.5-", "qwen3-max"} {
		if strings.HasPrefix(model, prefix) {
			return true
		}
	}
	return false
}

// SearchRoute is how provider's native search is switched on for model.
func SearchRoute(provider, model string) NativeSearch {
	route := Provider(provider).NativeSearch
	if route.Kind != qwenSearch {
		return route
	}
	if qwenUsesResponsesSearch(model) {
		return NativeSearch{Kind: CompatibleResponsesSearch}
	}
	return NativeSearch{Kind: ChatSearch, Chat: QwenSearch}
}

// ReasoningKind is what a model's thinking takes, per models.dev.
type ReasoningKind uint8

const (
	// ReasoningUnknown: not in the table - a custom endpoint, Ollama,
	// OpenRouter's routers, a model newer than the last sync.
	ReasoningUnknown ReasoningKind = iota
	// ReasoningNo: doesn't reason.
	ReasoningNo
	// ReasoningFixed: reasons, and takes no setting.
	ReasoningFixed
	// ReasoningOptions: takes the settings in ModelReasoning.
	ReasoningOptions
)

// ModelReasoning is how one model reasons.
type ModelReasoning struct {
	Kind ReasoningKind
	// Toggle: it can be switched off.
	Toggle bool
	// Efforts are the effort levels it takes, lowest first.
	Efforts []string
	// Budget is the most thinking tokens it takes a budget of; nil for no
	// budget.
	Budget *uint64
}

//go:embed model-capabilities.json
var capabilitiesJSON []byte

var modelTable = sync.OnceValue(func() map[string]map[string]any {
	var file struct {
		Providers map[string]map[string]struct {
			Reasoning any `json:"reasoning"`
		} `json:"providers"`
	}
	table := map[string]map[string]any{}
	if json.Unmarshal(capabilitiesJSON, &file) != nil {
		return table
	}
	for provider, models := range file.Providers {
		table[provider] = map[string]any{}
		for model, entry := range models {
			table[provider][model] = entry.Reasoning
		}
	}
	return table
})

func parseReasoning(v any) ModelReasoning {
	switch v := v.(type) {
	case bool:
		if !v {
			return ModelReasoning{Kind: ReasoningNo}
		}
	case map[string]any:
		r := ModelReasoning{Kind: ReasoningOptions, Toggle: v["toggle"] == true}
		for _, e := range list(v["effort"]) {
			if s, ok := e.(string); ok {
				r.Efforts = append(r.Efforts, s)
			}
		}
		if b, ok := v["budget"]; ok {
			n, ok := u64(b)
			if !ok {
				n = ^uint64(0)
			}
			r.Budget = &n
		}
		return r
	}
	return ModelReasoning{Kind: ReasoningFixed}
}

// ReasoningOf is how model of provider reasons.
func ReasoningOf(provider, model string) ModelReasoning {
	v, ok := modelTable()[provider][model]
	if !ok {
		return ModelReasoning{Kind: ReasoningUnknown}
	}
	return parseReasoning(v)
}

// CanThinkDeeper reports whether Deep thinking would change anything for
// model - what shows the composer's button. Off for a model that doesn't
// think, or thinks one fixed way; on for one the table doesn't know, where
// the parameters are tried and dropped if refused.
func CanThinkDeeper(provider, model string) bool {
	if Provider(provider).Thinking == NoThinking {
		return false
	}
	r := ReasoningOf(provider, model)
	switch r.Kind {
	case ReasoningUnknown:
		return true
	case ReasoningOptions:
		return r.Toggle || r.Budget != nil || len(r.Efforts) > 1
	}
	return false
}
