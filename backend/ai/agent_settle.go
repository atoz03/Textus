package ai

import (
	"slices"

	"textus/backend/ai/aicompat"
	"textus/backend/ai/rules"
)

// A step's result made ready for the loop: its lists split into one call
// per item, edits before comments, what block ids name filled in, and the
// document's conventions applied to proposed text.

// PostfixStep makes each propose_edit `text` in a step follow the
// conventions the document already follows, before the call reaches the
// tool, history and frontend, so the user sees the fixed text. An edit,
// comment or picture naming its block gets what the block says filled in,
// its ids kept for the frontend to place it by. It answers the rules
// applied.
func PostfixStep(result aicompat.StepResult, document *Document, conv *rules.Conventions) (aicompat.StepResult, []string) {
	if result.Done {
		return result, nil
	}
	var applied []string
	calls := slices.Clone(result.Calls)
	for i, t := range calls {
		if t.Kind != aicompat.ToolCalled {
			continue
		}
		switch t.Name {
		case ProposeEditToolName:
			arguments := ResolveEditArguments(document, t.Arguments)
			if fixed, rulesApplied, ok := rules.ProposalArguments(arguments, document.Text, conv); ok {
				applied = append(applied, rulesApplied...)
				arguments = fixed
			}
			calls[i].Arguments = arguments
		case AddCommentToolName, GenerateImageToolName:
			calls[i].Arguments = ResolveTargetArguments(document, t.Arguments)
		}
	}
	result.Calls = calls
	return result, applied
}

// SettleStep is a step's result as the loop takes it: lists split, edits
// before comments, PostfixStep applied. It answers the rules applied, for
// the eval to record.
func SettleStep(result aicompat.StepResult, document *Document, conv *rules.Conventions) (aicompat.StepResult, []string) {
	return PostfixStep(editsBeforeComments(SplitCalls(result)), document, conv)
}

// editsBeforeComments has a step's edits run before its comments, so a
// comment knows whether the request has proposed a change. Everything else
// keeps its place.
func editsBeforeComments(result aicompat.StepResult) aicompat.StepResult {
	if result.Done {
		return result
	}
	named := func(t aicompat.AgentTurn, tool string) bool { return t.Kind == aicompat.ToolCalled && t.Name == tool }
	lastEdit := -1
	for i, t := range result.Calls {
		if named(t, ProposeEditToolName) {
			lastEdit = i
		}
	}
	if lastEdit < 0 {
		return result
	}
	var early, rest []aicompat.AgentTurn
	for i, t := range result.Calls {
		if i < lastEdit && named(t, AddCommentToolName) {
			early = append(early, t)
		} else {
			rest = append(rest, t)
		}
	}
	// The comments go right after the last edit, which moved up by as many.
	at := lastEdit + 1 - len(early)
	result.Calls = slices.Concat(rest[:at], early, rest[at:])
	return result
}
