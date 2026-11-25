package paper

import (
	"context"
	"strings"
	"sync"

	"textus/backend/ai/aicompat"
)

// How a stage hands its work over: the model works until it calls the
// stage's one tool - submit_outline, submit_section, report_issues - whose
// check keeps what holds and sends back what is wrong, for another try.
// One loop for every stage, so a stage says only what it asks for and
// what holds.

// checked is what a stage's check makes of a hand-over: kept, with its
// value, or refused, with what the model is told to try again.
type checked[T any] struct {
	value T
	kept  bool
	why   string
}

func keptAs[T any](value T) checked[T] { return checked[T]{value: value, kept: true} }

func refused[T any](why string) checked[T] { return checked[T]{why: why} }

// notKept sends a hand-over back with every problem at once, for the whole
// what again.
func notKept[T any](what string, wrong []string) checked[T] {
	return refused[T]("Not kept - fix these and send the whole " + what + " again:\n- " + strings.Join(wrong, "\n- "))
}

// task is a stage's task: what the model is told, the tool it hands its
// work over with, and how many steps it has to make it hold.
type task struct {
	instructions, message string
	tool                  aicompat.ToolSpec
	steps                 int
	// watch is where the steps' stream goes; nil steps unwatched.
	watch aicompat.EventSink
}

// keptReply is what the model is told when its hand-over holds.
const keptReply = "Kept. Reply with one short line."

// handOver runs t until a hand-over holds or its steps run out: what was
// kept (nil for nothing), and the turns - for a stage that reads the
// model's reply when nothing held. check may wait on something, such as a
// section's figures drawn by the window.
func handOver[T any](ctx context.Context, model Model, t task, check func(arguments string) checked[T]) (*T, []aicompat.AgentTurn, error) {
	var mu sync.Mutex
	var kept *T
	specs := []aicompat.ToolSpec{t.tool}
	turns, err := aicompat.Loop{
		MaxSteps: t.steps,
		Step: func(turns []aicompat.AgentTurn) (aicompat.StepResult, error) {
			return model.Step(ctx, t.instructions, turns, specs, t.watch)
		},
		Execute: func(call aicompat.ToolCall) aicompat.ToolOutput {
			c := check(call.Arguments)
			if !c.kept {
				return aicompat.Text(c.why)
			}
			mu.Lock()
			kept = &c.value
			mu.Unlock()
			return aicompat.ToolOutput{Text: keptReply, Settled: true}
		},
		HowToRun: func(string) aicompat.ToolRun { return aicompat.InOrder },
	}.Run(nil, aicompat.UserTurn(t.message))
	if err != nil {
		return nil, nil, err
	}
	return kept, turns, nil
}
