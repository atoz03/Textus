package aicompat

import (
	"strings"
	"unicode"
)

// A streamed Chat Completions reply put together chunk by chunk: its text,
// reasoning and tool calls, each call kept apart by its own id.

// streamCall is one call under construction: id and name come first, the
// JSON arguments in pieces after, all addressed by array index rather than
// id.
type streamCall struct {
	// id is the id the server sent, then (once started) the one it goes by.
	id, name string
	args     strings.Builder
	// started: ToolStart was reported, once both id and name arrived.
	started bool
	// extra is what the server sent beside the call for it to have back
	// (extra_content: Gemini's thought signature).
	extra any
}

// chatStream rebuilds the whole choices[0].message from the chunks,
// reporting fragments as they arrive.
type chatStream struct {
	text, reasoning strings.Builder
	calls           []*streamCall
	err             string
	search          searchReport
	// usage is from the chunk that carries it - the last, with
	// include_usage.
	usage *StepUsage
	ids   callIDs
	// finishReason is why the reply ended, from its last chunk: "length"
	// when the output limit cut it.
	finishReason string
}

func (s *chatStream) apply(data string, on EventSink) {
	if s.err != "" || strings.TrimSpace(data) == "[DONE]" {
		return
	}
	chunk, ok := decode(data).(map[string]any)
	if !ok {
		return
	}
	// A failure mid-stream arrives as an error object in the data: the
	// status was 200 by the time it happened.
	if e := chunk["error"]; e != nil {
		s.err = "Custom endpoint request failed: " + compact(e)
		return
	}
	if usage, ok := openAIUsage(chunk["usage"]); ok {
		s.usage = &usage
	}
	var choice any
	if choices := list(chunk["choices"]); len(choices) > 0 {
		choice = choices[0]
	}
	if reason, ok := str(at(choice, "finish_reason")); ok {
		s.finishReason = reason
	}
	delta := at(choice, "delta")
	s.search.take(delta, chunk)
	if delta == nil {
		return
	}
	if thought, ok := reasoningOf(delta); ok && thought != "" {
		s.reasoning.WriteString(thought)
		on(ProviderEvent{Kind: EventThinking, Text: thought})
	}
	if text, ok := str(at(delta, "content")); ok && text != "" {
		// Gemini's thoughts come as text, marked.
		if isGeminiThought(delta) {
			s.reasoning.WriteString(text)
			on(ProviderEvent{Kind: EventThinking, Text: text})
		} else {
			s.text.WriteString(text)
			on(ProviderEvent{Kind: EventText, Text: text})
		}
	}
	for _, tc := range list(at(delta, "tool_calls")) {
		index := max(len(s.calls)-1, 0)
		if n, ok := u64(at(tc, "index")); ok {
			if n >= maxBlocks {
				continue
			}
			index = int(n)
		}
		for len(s.calls) <= index {
			s.calls = append(s.calls, &streamCall{})
		}
		call := s.calls[index]
		if extra := at(tc, extraContent); extra != nil {
			call.extra = extra
		}
		if id, ok := str(at(tc, "id")); ok && !call.started {
			call.id = id
		}
		if name, ok := str(at(tc, "function", "name")); ok && name != "" {
			call.name = name
		}
		if !call.started && call.name != "" {
			call.started = true
			call.id = s.ids.claim(call.id, index)
			on(ProviderEvent{Kind: EventToolStart, CallID: call.id, Name: call.name})
		}
		if part, ok := str(at(tc, "function", "arguments")); ok && part != "" {
			call.args.WriteString(part)
			if call.started {
				on(ProviderEvent{Kind: EventToolArgs, CallID: call.id, Text: part})
			}
		}
	}
}

func (s *chatStream) result(on EventSink) StepResult {
	var calls []AgentTurn
	for _, c := range s.calls {
		if !c.started {
			continue
		}
		arguments := c.args.String()
		if arguments == "" {
			arguments = "{}"
		}
		turn := AgentTurn{Kind: ToolCalled, CallID: c.id, Name: c.name, Arguments: arguments}
		if c.extra != nil {
			turn.Reasoning = callExtra(c.extra)
		}
		calls = append(calls, turn)
	}
	text := s.search.cite(s.text.String(), on)
	if len(calls) == 0 {
		return Reply(text)
	}
	attachReasoning(calls, s.reasoning.String())
	return CallsWithText(calls, text)
}

// cutOff is why the reply can't be used, if it was cut inside a tool call
// - by the output limit, or by the connection ending first: its half-JSON
// arguments would reach the tool as {} and send the model round in
// circles. "" when it can.
func (s *chatStream) cutOff() string {
	if len(s.calls) == 0 {
		return ""
	}
	if s.finishReason == "length" {
		return "The model hit its output limit in the middle of an edit. Try asking for a smaller change at a time."
	}
	args := strings.TrimSpace(s.calls[len(s.calls)-1].args.String())
	if s.finishReason == "" && args != "" && !validJSON(args) {
		return "The connection ended in the middle of the reply. Try again."
	}
	return ""
}

// Gemini's OpenAI-compatible layer, asked for its thoughts, sends them as
// the reply's text: marked per chunk when streamed, between <think> tags
// when whole. Split out, they go where other providers' reasoning goes.

// isGeminiThought reports whether a streamed delta's text is a thought.
func isGeminiThought(delta any) bool {
	thought, _ := at(delta, extraContent, "google", "thought").(bool)
	return thought
}

// splitGeminiThoughts is a whole reply's text as its thought and answer.
func splitGeminiThoughts(text string) (thought, answer string) {
	for _, tags := range [][2]string{{"<think>", "</think>"}, {"<thought>", "</thought>"}} {
		rest, ok := strings.CutPrefix(strings.TrimLeftFunc(text, unicode.IsSpace), tags[0])
		if !ok {
			continue
		}
		if end := strings.Index(rest, tags[1]); end >= 0 {
			return strings.TrimSpace(rest[:end]), strings.TrimLeftFunc(rest[end+len(tags[1]):], unicode.IsSpace)
		}
	}
	return "", text
}
