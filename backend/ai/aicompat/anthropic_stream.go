package aicompat

import (
	"fmt"
	"strings"
)

// A streamed Messages reply put together event by event: its text,
// thinking and tool calls, block by block.

type anthropicBlockKind uint8

const (
	ignoredBlock anthropicBlockKind = iota
	textBlock
	// thinkingBlock is readable (or, by default, empty) thinking and the
	// signature that lets it be sent back.
	thinkingBlock
	// redactedBlock is thinking Claude returned encrypted, sent back as it
	// came.
	redactedBlock
	toolUseBlock
	// otherBlock is any other block - a server_tool_use (web search) and
	// its result - kept whole, as the API sent it: a paused turn is
	// continued by sending them back unchanged.
	otherBlock
)

type anthropicBlock struct {
	kind anthropicBlockKind
	// text is a text or thinking block's.
	text      strings.Builder
	signature strings.Builder
	// citations are where a web search result backs the text.
	citations []any
	// block is a redacted or other block as it came.
	block    map[string]any
	id, name string
	// args are input_json_delta fragments, parsed once at the end.
	args strings.Builder
}

// anthropicStream rebuilds the whole reply's content from its events,
// reporting fragments as they arrive.
type anthropicStream struct {
	blocks     []*anthropicBlock
	err        string
	stopReason string
	usage      StepUsage
}

// maxBlocks bounds the block index a stream is believed about.
const maxBlocks = 4096

func (s *anthropicStream) at(index int) *anthropicBlock {
	for len(s.blocks) <= index {
		s.blocks = append(s.blocks, &anthropicBlock{})
	}
	return s.blocks[index]
}

func (s *anthropicStream) set(index int, b *anthropicBlock) {
	s.at(index)
	s.blocks[index] = b
}

func (s *anthropicStream) apply(data string, on EventSink) {
	if s.err != "" {
		return
	}
	event, ok := decode(data).(map[string]any)
	if !ok {
		return
	}
	n, _ := u64(event["index"])
	if n >= maxBlocks {
		return
	}
	index := int(n)
	switch strAt(event, "type") {
	case "content_block_start":
		block := object(event["content_block"])
		if block == nil {
			return
		}
		switch strAt(block, "type") {
		case "text":
			s.set(index, &anthropicBlock{kind: textBlock})
		case "thinking":
			on(ProviderEvent{Kind: EventThinking})
			b := &anthropicBlock{kind: thinkingBlock}
			b.text.WriteString(strAt(block, "thinking"))
			b.signature.WriteString(strAt(block, "signature"))
			s.set(index, b)
		case "redacted_thinking":
			on(ProviderEvent{Kind: EventThinking})
			s.set(index, &anthropicBlock{kind: redactedBlock, block: block})
		case "tool_use":
			id, name := strAt(block, "id"), strAt(block, "name")
			on(ProviderEvent{Kind: EventToolStart, CallID: id, Name: name})
			s.set(index, &anthropicBlock{kind: toolUseBlock, id: id, name: name})
		default:
			s.set(index, &anthropicBlock{kind: otherBlock, block: block})
		}
	case "content_block_delta":
		delta := object(event["delta"])
		if delta == nil {
			return
		}
		b := s.at(index)
		switch strAt(delta, "type") {
		case "text_delta":
			if text, ok := str(delta["text"]); ok && b.kind == textBlock {
				b.text.WriteString(text)
				on(ProviderEvent{Kind: EventText, Text: text})
			}
		case "citations_delta":
			if citation := delta["citation"]; citation != nil && b.kind == textBlock {
				b.citations = append(b.citations, citation)
			}
		case "thinking_delta":
			if part, ok := str(delta["thinking"]); ok && b.kind == thinkingBlock {
				b.text.WriteString(part)
				on(ProviderEvent{Kind: EventThinking, Text: part})
			}
		case "signature_delta":
			if part, ok := str(delta["signature"]); ok && b.kind == thinkingBlock {
				b.signature.WriteString(part)
			}
		case "input_json_delta":
			part, ok := str(delta["partial_json"])
			if !ok {
				return
			}
			switch b.kind {
			case toolUseBlock:
				b.args.WriteString(part)
				on(ProviderEvent{Kind: EventToolArgs, CallID: b.id, Text: part})
			case otherBlock:
				b.args.WriteString(part)
			}
		}
	case "message_start":
		if usage := at(event, "message", "usage"); usage != nil {
			n := func(key string) uint64 { v, _ := u64(at(usage, key)); return v }
			s.usage.InputTokens = n("input_tokens")
			s.usage.CacheCreationInputTokens = n("cache_creation_input_tokens")
			s.usage.CacheReadInputTokens = n("cache_read_input_tokens")
		}
	case "message_delta":
		if out, ok := u64(at(event, "usage", "output_tokens")); ok {
			s.usage.OutputTokens = out
		}
		if reason, ok := str(at(event, "delta", "stop_reason")); ok {
			s.stopReason = reason
		}
	case "error":
		var e any = event
		if inner := event["error"]; inner != nil {
			e = inner
		}
		s.err = "Claude request failed: " + compact(e)
	}
}

// cutOff is why the reply can't be used, if it was cut inside a tool call
// - by the output limit, or by the connection ending first: its half-JSON
// arguments would reach the tool as {} and send the model round in
// circles. "" when it can.
func (s *anthropicStream) cutOff(maxTokens int) string {
	if len(s.blocks) == 0 || s.blocks[len(s.blocks)-1].kind != toolUseBlock {
		return ""
	}
	if s.stopReason == "max_tokens" {
		return fmt.Sprintf("Claude hit the %d-token output limit in the middle of an edit. Try asking for a smaller change at a time.", maxTokens)
	}
	args := strings.TrimSpace(s.blocks[len(s.blocks)-1].args.String())
	if s.stopReason == "" && args != "" && !validJSON(args) {
		return "The connection ended in the middle of the reply. Try again."
	}
	return ""
}

// parsedInput is streamed input JSON as a value, {} where it doesn't
// parse.
func parsedInput(args string) any {
	if v := decode(args); v != nil {
		return v
	}
	return map[string]any{}
}

// content is the reply's content blocks, as the API would have sent them
// whole.
func (s *anthropicStream) content() []any {
	var out []any
	for _, b := range s.blocks {
		switch b.kind {
		case textBlock:
			block := map[string]any{"type": "text", "text": b.text.String()}
			if len(b.citations) > 0 {
				block["citations"] = b.citations
			}
			out = append(out, block)
		case thinkingBlock:
			out = append(out, map[string]any{"type": "thinking", "thinking": b.text.String(), "signature": b.signature.String()})
		case redactedBlock:
			out = append(out, b.block)
		case toolUseBlock:
			out = append(out, map[string]any{"type": "tool_use", "id": b.id, "name": b.name, "input": parsedInput(b.args.String())})
		case otherBlock:
			if b.args.Len() > 0 {
				b.block["input"] = parsedInput(b.args.String())
			}
			out = append(out, b.block)
		}
	}
	return out
}
