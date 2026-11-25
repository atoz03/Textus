package ai

import (
	"encoding/json/v2"
	"sync"
	"time"

	"textus/backend/ai/aicompat"
)

// What a request sends the frontend as it runs: the reply's text and
// thinking, its tool calls as they stream, and each finished turn.

// StreamEvent is one live fragment of an in-flight ai_agent_message
// (StreamEvent in src/platform/bindings/ai-agent.ts). Display only: the
// command still resolves with the complete turn list, so a dropped event
// costs liveness, never correctness.
type StreamEvent struct {
	// Type is "delta" (a chunk of the reply), "toolStart" (a tool call
	// opened; its arguments follow as "toolArgsDelta"), "turn" (a completed
	// intermediate turn as it lands), "retry" (the step being streamed came
	// back garbled and is asked for again: drop what it streamed),
	// "thinking" (a fragment of the model's reasoning, or "" when the
	// provider only says that it is thinking), "forgot" (the conversation's
	// oldest exchanges no longer fit and were left out) or "picture" (a
	// picture written into a propose_edit's text is being made).
	Type   string
	Text   string
	CallID string
	Name   string
	Delta  string
	Turn   aicompat.AgentTurn
	// Line, ID, Width and Height are a picture's: its line in the text
	// stands for picture ID from now on.
	Line, ID      string
	Width, Height uint32
}

func (e StreamEvent) MarshalJSON() ([]byte, error) {
	type text struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	switch e.Type {
	case "delta", "thinking":
		return json.Marshal(text{e.Type, e.Text})
	case "toolStart":
		return json.Marshal(struct {
			Type   string `json:"type"`
			CallID string `json:"callId"`
			Name   string `json:"name"`
		}{e.Type, e.CallID, e.Name})
	case "toolArgsDelta":
		return json.Marshal(struct {
			Type   string `json:"type"`
			CallID string `json:"callId"`
			Delta  string `json:"delta"`
		}{e.Type, e.CallID, e.Delta})
	case "turn":
		return json.Marshal(struct {
			Type string             `json:"type"`
			Turn aicompat.AgentTurn `json:"turn"`
		}{e.Type, e.Turn})
	case "picture":
		return json.Marshal(struct {
			Type   string `json:"type"`
			CallID string `json:"callId"`
			Line   string `json:"line"`
			ID     string `json:"id"`
			Width  uint32 `json:"width"`
			Height uint32 `json:"height"`
		}{e.Type, e.CallID, e.Line, e.ID, e.Width, e.Height})
	}
	return json.Marshal(struct {
		Type string `json:"type"`
	}{e.Type})
}

// EventOf is a provider's fragment as the frontend shows it; false for a
// citation, which goes to the request's sources instead.
func EventOf(e aicompat.ProviderEvent) (StreamEvent, bool) {
	switch e.Kind {
	case aicompat.EventText:
		return StreamEvent{Type: "delta", Text: e.Text}, true
	case aicompat.EventToolStart:
		return StreamEvent{Type: "toolStart", CallID: e.CallID, Name: e.Name}, true
	case aicompat.EventToolArgs:
		return StreamEvent{Type: "toolArgsDelta", CallID: e.CallID, Delta: e.Text}, true
	case aicompat.EventThinking:
		return StreamEvent{Type: "thinking", Text: e.Text}, true
	}
	return StreamEvent{}, false
}

// pace is how often merged fragments go to the window: tokens come far
// faster than a frame, and each send is a script the page runs.
const pace = 30 * time.Millisecond

// Paced merges a run of text, thinking or one call's argument fragments
// and sends them at most every pace; any other event sends what waits
// first, so the order holds. Flush sends what still waits.
type Paced struct {
	send    func(StreamEvent)
	mu      sync.Mutex
	waiting *StreamEvent
	last    time.Time
}

// NewPaced is a paced sink in front of send.
func NewPaced(send func(StreamEvent)) *Paced { return &Paced{send: send} }

// merged adds next to waiting when both are fragments of one stream.
func merged(waiting *StreamEvent, next StreamEvent) bool {
	switch {
	case waiting.Type != next.Type:
		return false
	case next.Type == "delta" || next.Type == "thinking":
		waiting.Text += next.Text
		return true
	case next.Type == "toolArgsDelta" && waiting.CallID == next.CallID:
		waiting.Delta += next.Delta
		return true
	}
	return false
}

// Send takes one event. What it sends goes out under the lock: the
// stream's fragments and the loop's turns come from different goroutines,
// and the window must see them in order.
func (p *Paced) Send(e StreamEvent) {
	fragment := e.Type == "delta" || e.Type == "thinking" || e.Type == "toolArgsDelta"
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.waiting == nil || !merged(p.waiting, e) {
		if p.waiting != nil {
			p.send(*p.waiting)
			p.waiting = nil
		}
		if fragment {
			p.waiting = &e
		} else {
			p.send(e)
		}
	}
	if p.last.IsZero() || time.Since(p.last) >= pace {
		if p.waiting != nil {
			p.send(*p.waiting)
			p.waiting = nil
		}
		p.last = time.Now()
	}
}

// Flush sends what still waits.
func (p *Paced) Flush() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.waiting != nil {
		p.send(*p.waiting)
		p.waiting = nil
	}
}
