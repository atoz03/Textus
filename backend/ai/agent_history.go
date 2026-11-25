package ai

import (
	"fmt"
	"unicode/utf8"

	"textus/backend/ai/aicompat"
)

// The conversation sent with a request, kept under its budget: old tool
// output shortened first, the oldest turns dropped after.

const (
	// oldToolOutputLimit: tool output from an earlier exchange longer than
	// this is cut to oldToolOutputKeep before it is replayed; re-sending
	// old reads made long conversations slow.
	oldToolOutputLimit = 1_500
	oldToolOutputKeep  = 400
	// historyBudget: past this much replayed history (char units), whole
	// exchanges are dropped oldest first, so a long session forgets its
	// start instead of failing.
	historyBudget = 150_000
	// historyDropStep: exchanges are dropped in blocks of at least this
	// many characters, counted from the start, so the history's first
	// message (and the prompt cache) stays put until the conversation grows
	// by another block.
	historyDropStep = historyBudget / 3
)

// MemoryTurn is the name a memory chip's turns go by (MEMORY_TURN in
// src/platform/bindings/ai-agent.ts).
const MemoryTurn = "remember"

// PaperTurn is the name a paper card's turns go by (PAPER_TURN in
// src/platform/bindings/ai-agent.ts).
const PaperTurn = "write_paper"

// units is a text's size in the char units every budget meant to
// approximate tokens counts in (CJK as 3).
func units(text string) int {
	n := 0
	for _, c := range text {
		n += CharUnits(c)
	}
	return n
}

// turnChars is a turn's size in char units.
func turnChars(t aicompat.AgentTurn) int {
	switch t.Kind {
	case aicompat.ToolCalled:
		return units(t.Arguments)
	case aicompat.ToolResult:
		return units(t.Output)
	}
	return units(t.Text)
}

// CompactTelling is CompactHistory, and whether the conversation's start
// was left out: the writer hears the model no longer sees it.
func CompactTelling(history []aicompat.AgentTurn) ([]aicompat.AgentTurn, bool) {
	asked := func(turns []aicompat.AgentTurn) int {
		n := 0
		for _, t := range turns {
			if t.Kind == aicompat.User {
				n++
			}
		}
		return n
	}
	before := asked(history)
	history = CompactHistory(history)
	return history, asked(history) < before
}

// WithoutMemoryTurns is history without the memory chips' turns (a
// remember call and its result): the model has no remember tool, and what
// is remembered comes with every message. Nor a paper's card turns: the
// paper's own proposal is sent.
func WithoutMemoryTurns(history []aicompat.AgentTurn) []aicompat.AgentTurn {
	chips := map[string]bool{}
	for _, t := range history {
		if t.Kind == aicompat.ToolCalled && (t.Name == MemoryTurn || t.Name == PaperTurn) {
			chips[t.CallID] = true
		}
	}
	out := make([]aicompat.AgentTurn, 0, len(history))
	for _, t := range history {
		if (t.Kind == aicompat.ToolCalled || t.Kind == aicompat.ToolResult) && chips[t.CallID] {
			continue
		}
		out = append(out, t)
	}
	return out
}

// CompactHistory shrinks the replayed conversation to what a writing agent
// still needs; propose_edit results are never cut. Both cuts come out the
// same on every send, so the prompt cache can cover the whole history.
func CompactHistory(history []aicompat.AgentTurn) []aicompat.AgentTurn {
	turns := WithoutMemoryTurns(history)
	names := map[string]string{}
	for _, t := range turns {
		if t.Kind == aicompat.ToolCalled {
			names[t.CallID] = t.Name
		}
	}
	total := 0
	for i, t := range turns {
		if t.Kind == aicompat.ToolResult && names[t.CallID] != ProposeEditToolName {
			if n := utf8.RuneCountInString(t.Output); n > oldToolOutputLimit {
				t.Output = fmt.Sprintf("%s\n[... %d more characters of this earlier result omitted to save space - call the tool again if you need them]",
					charPrefix(t.Output, oldToolOutputKeep), n-oldToolOutputKeep)
				turns[i] = t
			}
		}
		total += turnChars(turns[i])
	}
	if total <= historyBudget {
		return turns
	}
	// Cut only where an exchange starts (at a user turn): dropping whole
	// exchanges keeps every tool call paired with its result. Each start
	// with the characters before it.
	type start struct{ at, before int }
	var starts []start
	before := 0
	for i, t := range turns {
		if i > 0 && t.Kind == aicompat.User {
			starts = append(starts, start{i, before})
		}
		before += turnChars(t)
	}
	if len(starts) == 0 {
		return turns
	}
	// The fewest exchanges that must go: up to the first start that leaves
	// the rest within budget, else all but the newest, as large as it is.
	needed := starts[len(starts)-1]
	for _, s := range starts {
		if total-s.before <= historyBudget {
			needed = s
			break
		}
	}
	// Rounded up to the end of its block. An exchange bigger than a block
	// (a long attachment) would leave a fraction of the budget, so then just
	// what must go.
	cut := needed.at
	blockStart := 0
	for _, s := range starts {
		if s.before-blockStart < historyDropStep {
			continue
		}
		blockStart = s.before
		if s.at >= needed.at {
			if s.before-needed.before <= 2*historyDropStep {
				cut = s.at
			}
			break
		}
	}
	return turns[cut:]
}

// ForHistory is a turn as the frontend keeps it: a call without the step's
// reasoning (for this exchange's own requests: no provider wants it back
// in a later one, and it never shows), a tool result without the images it
// showed the model, which would grow the saved history by megabytes.
func ForHistory(t aicompat.AgentTurn) aicompat.AgentTurn {
	switch {
	case t.Kind == aicompat.ToolCalled:
		t.Reasoning = nil
	case t.Kind == aicompat.ToolResult && len(t.Images) > 0:
		t.Output += "\n[The image was shown for that request only - call read_file again to look at it.]"
		t.Images = nil
	}
	return t
}
