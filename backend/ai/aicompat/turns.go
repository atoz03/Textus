// Package aicompat is the agent's side of the providers: a conversation
// kept provider-agnostic, the tool-calling loop, what each provider and
// model can be asked to do, and one streamed step in each dialect - the
// Responses API (OpenAI, the ChatGPT sign-in), Anthropic Messages, and
// OpenAI-compatible Chat Completions.
package aicompat

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"strings"
)

// ImageAttachment is an image riding along with a user turn, or one a tool
// showed the model (ImageAttachment in src/platform/bindings/ai-agent.ts).
type ImageAttachment struct {
	Name       string `json:"name"`
	Mime       string `json:"mime"`
	DataBase64 string `json:"dataBase64"`
}

// DataURL is `data:image/png;base64,...`, the form both OpenAI dialects
// want.
func (i ImageAttachment) DataURL() string { return "data:" + i.Mime + ";base64," + i.DataBase64 }

// WebSource is one web page a reply cites as `[n]` (WebSource in
// src/platform/bindings/ai-agent.ts).
type WebSource struct {
	N     uint32 `json:"n"`
	URL   string `json:"url"`
	Title string `json:"title"`
}

// TurnKind is which entry an AgentTurn is.
type TurnKind string

const (
	User       TurnKind = "User"
	Assistant  TurnKind = "Assistant"
	ToolCalled TurnKind = "ToolCall"
	ToolResult TurnKind = "ToolResult"
)

// AgentTurn is one entry in an agent conversation, kept provider-agnostic
// (AgentTurn in src/platform/bindings/ai-agent.ts). Which fields it has
// depends on its Kind.
type AgentTurn struct {
	Kind TurnKind
	// Text is a User or Assistant turn's.
	Text string
	// Images are a User turn's attachments, or what a tool showed the model
	// (a ToolResult's, for the request in flight only).
	Images []ImageAttachment
	// Sources are the web pages an Assistant turn cites as [n].
	Sources []WebSource
	// CallID ties a ToolResult to its ToolCall.
	CallID    string
	Name      string
	Arguments string
	// Reasoning is the model's reasoning for the step a ToolCall opens, in
	// the provider's wire shape, on the step's first call only; nil for
	// none. Thinking with tools needs it sent back.
	Reasoning jsontext.Value
	// Output is a ToolResult's text.
	Output string
}

// UserTurn is a message from the user.
func UserTurn(text string, images ...ImageAttachment) AgentTurn {
	return AgentTurn{Kind: User, Text: text, Images: images}
}

// AssistantTurn is a reply that cites no web sources.
func AssistantTurn(text string) AgentTurn { return AgentTurn{Kind: Assistant, Text: text} }

// Call is a ToolCall turn as the loop's executor takes it.
func (t AgentTurn) Call() ToolCall {
	return ToolCall{CallID: t.CallID, Name: t.Name, Arguments: t.Arguments}
}

type userWire struct {
	Kind   TurnKind          `json:"kind"`
	Text   string            `json:"text"`
	Images []ImageAttachment `json:"images"`
}

type assistantWire struct {
	Kind    TurnKind    `json:"kind"`
	Text    string      `json:"text"`
	Sources []WebSource `json:"sources,omitempty"`
}

type callWire struct {
	Kind      TurnKind       `json:"kind"`
	CallID    string         `json:"call_id"`
	Name      string         `json:"name"`
	Arguments string         `json:"arguments"`
	Reasoning jsontext.Value `json:"reasoning,omitzero"`
}

type resultWire struct {
	Kind   TurnKind          `json:"kind"`
	CallID string            `json:"call_id"`
	Output string            `json:"output"`
	Images []ImageAttachment `json:"images,omitempty"`
}

// MarshalJSON writes the turn with its kind's fields only.
func (t AgentTurn) MarshalJSON() ([]byte, error) {
	switch t.Kind {
	case User:
		return json.Marshal(userWire{t.Kind, t.Text, t.Images})
	case Assistant:
		return json.Marshal(assistantWire{t.Kind, t.Text, t.Sources})
	case ToolCalled:
		return json.Marshal(callWire{t.Kind, t.CallID, t.Name, t.Arguments, t.Reasoning})
	case ToolResult:
		return json.Marshal(resultWire{t.Kind, t.CallID, t.Output, t.Images})
	}
	return nil, fmt.Errorf("unknown turn kind %q", t.Kind)
}

// UnmarshalJSON reads a turn; fields saved before they existed default.
func (t *AgentTurn) UnmarshalJSON(raw []byte) error {
	var w struct {
		Kind      TurnKind          `json:"kind"`
		Text      string            `json:"text"`
		Images    []ImageAttachment `json:"images"`
		Sources   []WebSource       `json:"sources"`
		CallID    string            `json:"call_id"`
		Name      string            `json:"name"`
		Arguments string            `json:"arguments"`
		Reasoning jsontext.Value    `json:"reasoning"`
		Output    string            `json:"output"`
	}
	if err := json.Unmarshal(raw, &w); err != nil {
		return err
	}
	switch w.Kind {
	case User, Assistant, ToolCalled, ToolResult:
	default:
		return fmt.Errorf("unknown turn kind %q", w.Kind)
	}
	if len(w.Reasoning) == 0 || string(w.Reasoning) == "null" {
		w.Reasoning = nil
	}
	*t = AgentTurn{
		Kind: w.Kind, Text: w.Text, Images: w.Images, Sources: w.Sources, CallID: w.CallID,
		Name: w.Name, Arguments: w.Arguments, Reasoning: w.Reasoning.Clone(), Output: w.Output,
	}
	return nil
}

// ToolOutput is what a tool returns to the loop: text, and any images it
// shows the model.
type ToolOutput struct {
	Text   string
	Images []ImageAttachment
	// CallArguments are the call's arguments as they are to be recorded, for
	// an Amending tool; nil to keep them.
	CallArguments *string
	// Settled: the call needs nothing more from the model. When every call
	// of a step is settled and the model wrote its reply with them, the loop
	// ends.
	Settled bool
	// SettlesWithOthers: settled only beside other calls - a step of this
	// call alone goes on, since a lone one means the model meant to.
	SettlesWithOthers bool
}

// Text is a ToolOutput of text alone.
func Text(text string) ToolOutput { return ToolOutput{Text: text} }

// StepUsage is one step's token accounting: uncached input, input written
// to the cache (Claude only), input read from it, and output.
type StepUsage struct {
	InputTokens, CacheCreationInputTokens, CacheReadInputTokens, OutputTokens uint64
}

// Add adds o.
func (u *StepUsage) Add(o StepUsage) {
	u.InputTokens += o.InputTokens
	u.CacheCreationInputTokens += o.CacheCreationInputTokens
	u.CacheReadInputTokens += o.CacheReadInputTokens
	u.OutputTokens += o.OutputTokens
}

// openAIUsage reads an OpenAI-style `usage` object, Chat Completions or
// Responses; both count cached input inside the input total, named
// differently by each provider. False when there is none.
func openAIUsage(usage any) (StepUsage, bool) {
	n := func(path ...string) (uint64, bool) { return u64(at(usage, path...)) }
	input, ok := n("prompt_tokens")
	if !ok {
		if input, ok = n("input_tokens"); !ok {
			return StepUsage{}, false
		}
	}
	var cached uint64
	for _, path := range [][]string{
		{"prompt_tokens_details", "cached_tokens"}, {"input_tokens_details", "cached_tokens"},
		{"prompt_cache_hit_tokens"}, {"cached_tokens"},
	} {
		if c, ok := n(path...); ok {
			cached = c
			break
		}
	}
	cached = min(cached, input)
	output, ok := n("completion_tokens")
	if !ok {
		output, _ = n("output_tokens")
	}
	return StepUsage{InputTokens: input - cached, CacheReadInputTokens: cached, OutputTokens: output}, true
}

// ToolCall is one call the loop hands its executor.
type ToolCall struct {
	CallID, Name, Arguments string
}

// ToolRun is how the loop runs a tool's calls.
type ToolRun uint8

const (
	// Concurrent: it only reads - a step's calls run at the same time, up
	// to MaxConcurrentTools.
	Concurrent ToolRun = iota
	// InOrder: it proposes or changes something - one call at a time, in
	// the order the model sent them.
	InOrder
	// Amending: in order, and its output gives its call's final arguments
	// (ToolOutput.CallArguments); a step with such a call reports its calls
	// once they have run.
	Amending
)

// MaxConcurrentTools is how many read-only calls of one step run at once.
const MaxConcurrentTools = 4

// ToolImagesNote is the text a user message carries next to images a tool
// showed, where the dialect can't put them in the tool result, so the
// model reads them as the tool's answer.
func ToolImagesNote(images []ImageAttachment) string {
	names := make([]string, len(images))
	for i, image := range images {
		names[i] = image.Name
	}
	return "(Opened by the tool call above, not sent by the user: " + strings.Join(names, ", ") + ")"
}

// ToolSpec is a tool the model may call; Parameters is a JSON schema.
type ToolSpec struct {
	Name, Description string
	Parameters        any
}

// StepResult is what one model turn produced: a reply (Done), or tool
// calls, which may end with an Assistant turn carrying the prose written
// with them - the reply once every call is settled.
type StepResult struct {
	Done  bool
	Text  string
	Calls []AgentTurn
}

// Reply is a step that answered.
func Reply(text string) StepResult { return StepResult{Done: true, Text: text} }

// CallsWithText is a step's tool calls and the text the model wrote with
// them, if any.
func CallsWithText(calls []AgentTurn, text string) StepResult {
	if strings.TrimSpace(text) != "" {
		calls = append(calls, AssistantTurn(text))
	}
	return StepResult{Calls: calls}
}

// EventKind is what a ProviderEvent reports.
type EventKind uint8

const (
	// EventText is a chunk of the assistant's prose.
	EventText EventKind = iota
	// EventToolStart: the model opened a tool call (CallID, Name).
	EventToolStart
	// EventToolArgs is a raw fragment (Text) of a call's JSON arguments:
	// partial JSON.
	EventToolArgs
	// EventThinking is a fragment of the model's readable reasoning, or ""
	// where the provider only says that it thinks.
	EventThinking
	// EventCitation is a page the provider's own search cited (URL, Title),
	// once per page and step in the order first cited.
	EventCitation
)

// ProviderEvent is a live fragment surfaced while a step's response is
// still being read. Purely additive: the step still returns the complete
// StepResult.
type ProviderEvent struct {
	Kind         EventKind
	Text         string
	CallID, Name string
	URL, Title   string
}

// EventSink takes a step's ProviderEvents.
type EventSink func(ProviderEvent)

// Ignore is the EventSink that drops them.
func Ignore(ProviderEvent) {}
