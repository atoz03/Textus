package paper

import (
	"context"
	"errors"
	"strings"

	"textus/backend/ai"
	"textus/backend/ai/aicompat"
)

// How every stage talks to a model: one step at a time, its stream going
// to a watcher. Research's stages step through Model, so each runs on a
// scripted model in tests.

// Model is one step of a model. Watch, when not nil, gets what the step
// streams as it comes; a model that doesn't stream just steps.
type Model interface {
	Step(ctx context.Context, instructions string, turns []aicompat.AgentTurn, tools []aicompat.ToolSpec, watch aicompat.EventSink) (aicompat.StepResult, error)
	// AtOnce is how many steps it takes at once: what a stage starts
	// together.
	AtOnce() int
}

// AtOnce is the steps at once of a model without a cap of its own.
const AtOnce = 3

// Provider is a provider's agent as a Model, taking atOnce steps at once.
type Provider struct {
	Agent aicompat.AgentProvider
	// Calls is how many steps it takes at once; 0 for AtOnce.
	Calls int
}

func (p Provider) Step(ctx context.Context, instructions string, turns []aicompat.AgentTurn, tools []aicompat.ToolSpec, watch aicompat.EventSink) (aicompat.StepResult, error) {
	// A helper's text and thinking aren't shown: its activity is.
	if watch == nil {
		watch = aicompat.Ignore
	}
	result, _, err := p.Agent.Step(ctx, instructions, turns, tools, watch)
	return result, err
}

func (p Provider) AtOnce() int {
	if p.Calls > 0 {
		return p.Calls
	}
	return AtOnce
}

// Guided is a model whose every step is told what the user asks of all
// the writing done for them - their writing instructions, memories, style
// profile and library (ai.WritingGuidance) - after the stage's own
// instructions.
type Guided struct {
	Inner    Model
	Guidance string
}

func (g Guided) Step(ctx context.Context, instructions string, turns []aicompat.AgentTurn, tools []aicompat.ToolSpec, watch aicompat.EventSink) (aicompat.StepResult, error) {
	if g.Guidance != "" {
		instructions += "\n\n" + g.Guidance
	}
	return g.Inner.Step(ctx, instructions, turns, tools, watch)
}

func (g Guided) AtOnce() int { return g.Inner.AtOnce() }

// Metered is a model whose every step counts against the paper's budget:
// with none left, a step fails with StepsSpent and the paper stops to ask.
type Metered struct {
	Inner  Model
	Budget *SharedBudget
}

func (m Metered) Step(ctx context.Context, instructions string, turns []aicompat.AgentTurn, tools []aicompat.ToolSpec, watch aicompat.EventSink) (aicompat.StepResult, error) {
	if !m.Budget.TakeKind(ModelStep) {
		return aicompat.StepResult{}, errors.New(StepsSpent)
	}
	return m.Inner.Step(ctx, instructions, turns, tools, watch)
}

func (m Metered) AtOnce() int { return m.Inner.AtOnce() }

// Spent reports whether the budget ran out on this run.
func (m Metered) Spent() bool { return m.Budget.Snapshot().Remaining().ModelSteps == 0 }

// ask is one tool-less step, as it streams to watch: the model's reply to
// message, or "" when it calls a tool anyway.
func ask(ctx context.Context, model Model, instructions, message string, watch aicompat.EventSink) (string, error) {
	result, err := model.Step(ctx, instructions, []aicompat.AgentTurn{aicompat.UserTurn(message)}, nil, watch)
	if err != nil || !result.Done {
		return "", err
	}
	return result.Text, nil
}

// detailChars is the longest an activity's detail is sent: the card shows
// one line.
const detailChars = 120

// clip is text on one line, at most detailChars long.
func clip(text string) string {
	return ai.Clip(strings.Join(strings.Fields(text), " "), detailChars-1)
}
