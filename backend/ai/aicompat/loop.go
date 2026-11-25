package aicompat

import (
	"fmt"
	"strings"
	"sync"
)

// The agent loop: a step, its tool calls run, their results sent back,
// until the model replies or the steps run out.

// OutOfSteps is the reply of an exchange that ran out of steps; the model
// reads it when asked to go on. Keep in step with OUT_OF_STEPS in
// out-of-steps.ts.
const OutOfSteps = "(Stopped at the step limit before finishing. Ask me to continue, or narrow the request.)"

// LoopEvent is what the loop reports while it runs: an intermediate turn
// (a ToolCall or its ToolResult, or a steered message) as it lands, or
// Retry - the step that just streamed came back garbled and is asked for
// again, so whatever it streamed is void.
type LoopEvent struct {
	Turn  *AgentTurn
	Retry bool
}

// Loop is one exchange's tool-calling loop.
type Loop struct {
	MaxSteps int
	// Step asks the model for the next step given the turns so far.
	Step func(turns []AgentTurn) (StepResult, error)
	// Execute runs one call.
	Execute func(call ToolCall) ToolOutput
	// HowToRun says how a tool's calls run.
	HowToRun func(name string) ToolRun
	// OnEvent gets each ToolCall and ToolResult as it lands, and Retry; the
	// final Assistant turn is not reported, as its text already streamed.
	OnEvent func(LoopEvent)
	// Steered are the messages the user sent in while the step ran, asked
	// after each step's tool results; each goes on as a user turn. Left
	// unread when the model answers first: the caller sends those on their
	// own. Nil for none.
	Steered func() []AgentTurn
}

// Run runs the loop from history and the user's turn until the model
// replies or the steps run out, and answers the exchange's new turns.
//
// A step whose calls are all settled and that came with text ends the
// exchange, the text as the reply. A step's turns are recorded as all its
// calls, then all their results, so a ToolCall right after a ToolResult
// always starts a new step; the dialects group calls by that.
func (l Loop) Run(history []AgentTurn, user AgentTurn) ([]AgentTurn, error) {
	turns := append(append([]AgentTurn{}, history...), user)
	added := []AgentTurn{user}
	report := func(e LoopEvent) {
		if l.OnEvent != nil {
			l.OnEvent(e)
		}
	}
	retried := false
	for index := range l.MaxSteps {
		result, err := l.Step(append([]AgentTurn{}, turns...))
		if err != nil {
			return nil, err
		}
		// A garbled step is asked for again once per request: usually a
		// sampling accident, but text that really holds U+FFFD mustn't cost
		// a retry every step.
		if !retried && garbled(result) {
			retried = true
			report(LoopEvent{Retry: true})
			if result, err = l.Step(append([]AgentTurn{}, turns...)); err != nil {
				return nil, err
			}
		}
		if result.Done {
			return append(added, AssistantTurn(result.Text)), nil
		}
		reply := ""
		var calls []AgentTurn
		for _, turn := range result.Calls {
			switch {
			case turn.Kind == ToolCalled:
				calls = append(calls, turn)
			case turn.Kind == Assistant && reply == "" && strings.TrimSpace(turn.Text) != "":
				reply = turn.Text
			}
		}
		amends := false
		for _, call := range calls {
			amends = amends || l.HowToRun(call.Name) == Amending
		}
		if !amends {
			for i := range calls {
				report(LoopEvent{Turn: &calls[i]})
			}
		}
		outputs := l.runCalls(calls)
		for i := range calls {
			if amended := outputs[i].CallArguments; amended != nil {
				calls[i].Arguments = *amended
			}
			if amends {
				report(LoopEvent{Turn: &calls[i]})
			}
			turns = append(turns, calls[i])
			added = append(added, calls[i])
		}
		remaining := l.MaxSteps - index - 1
		settled := len(calls) > 0 && !(len(calls) == 1 && outputs[0].SettlesWithOthers)
		for _, o := range outputs {
			settled = settled && o.Settled
		}
		note := budgetNote(remaining)
		for i, call := range calls {
			result := AgentTurn{Kind: ToolResult, CallID: call.CallID, Output: outputs[i].Text, Images: outputs[i].Images}
			report(LoopEvent{Turn: &result})
			added = append(added, result)
			// The note goes on the step's last result, only as first sent,
			// so earlier turns (and their prompt cache) stay as they were.
			if note != "" && i == len(calls)-1 {
				result.Output += "\n\n" + note
			}
			turns = append(turns, result)
		}
		if reply != "" && settled {
			return append(added, AssistantTurn(reply)), nil
		}
		if l.Steered != nil {
			for _, turn := range l.Steered() {
				report(LoopEvent{Turn: &turn})
				turns = append(turns, turn)
				added = append(added, turn)
			}
		}
	}
	// Out of steps while the model still wanted tools: without a reply the
	// UI would show the previous exchange's answer as this one's.
	return append(added, AssistantTurn(OutOfSteps)), nil
}

// runCalls runs one step's calls and answers their outputs in call order:
// a run of Concurrent calls together (bounded), every other call on its
// own once the calls before it are done.
func (l Loop) runCalls(calls []AgentTurn) []ToolOutput {
	outputs := make([]ToolOutput, len(calls))
	for i := 0; i < len(calls); {
		if l.HowToRun(calls[i].Name) != Concurrent {
			outputs[i] = l.Execute(calls[i].Call())
			i++
			continue
		}
		end := i
		for end < len(calls) && l.HowToRun(calls[end].Name) == Concurrent {
			end++
		}
		var wg sync.WaitGroup
		slots := make(chan struct{}, MaxConcurrentTools)
		for j := i; j < end; j++ {
			slots <- struct{}{}
			wg.Go(func() {
				defer func() { <-slots }()
				outputs[j] = l.Execute(calls[j].Call())
			})
		}
		wg.Wait()
		i = end
	}
	return outputs
}

// garbled reports whether a step came back with U+FFFD in its text or a
// call's arguments: some servers decode byte-level tokens one at a time
// and send a replacement character for each byte of a split character
// (escaped, from servers that write ASCII-only JSON).
func garbled(result StepResult) bool {
	broken := func(s string) bool {
		return strings.ContainsRune(s, '\uFFFD') || strings.Contains(s, `\ufffd`) || strings.Contains(s, `\uFFFD`)
	}
	if result.Done {
		return broken(result.Text)
	}
	for _, turn := range result.Calls {
		if turn.Kind == ToolCalled && broken(turn.Arguments) || turn.Kind == Assistant && broken(turn.Text) {
			return true
		}
	}
	return false
}

// budgetNote is what the model is told after a step with remaining steps
// still to come, when they are few, so it delivers instead of running out
// mid-task; "" otherwise.
func budgetNote(remaining int) string {
	if remaining < 1 || remaining > 2 {
		return ""
	}
	steps := "1 step"
	if remaining == 2 {
		steps = "2 steps"
	}
	return fmt.Sprintf("[Only %s left in this request: stop gathering or checking and deliver your result now - send any edit proposals, then give your final reply, saying what is done and what is left if you didn't get through it all.]", steps)
}
