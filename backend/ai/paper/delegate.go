package paper

import (
	"fmt"
	"strings"

	"textus/backend/ai/aicompat"
)

// delegate_task: a question's researcher hands a narrower part of its
// question - a case, a data set, a dispute that needs searches of its own -
// to a helper of its own. The helper keeps its notes with the question's,
// spends the question's share of the budget, and hands nothing on; what it
// kept comes back as the call's answer, so the researcher doesn't note it
// again.

const delegateTool = "delegate_task"

// delegateSpec is what delegate_task looks like to the model.
func delegateSpec() aicompat.ToolSpec {
	return aicompat.ToolSpec{
		Name:        delegateTool,
		Description: fmt.Sprintf("Hand a narrower part of your question to a helper who researches it while you wait: a case, a data set, a dispute or a period that needs searches of its own. The helper keeps its notes with yours and spends your question's share of searches; its answer lists what it kept, so don't note that again. At most %d per question - use it for a part you can't cover in passing, not for your whole question.", maxHanded),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"question": map[string]any{"type": "string", "description": "The part to research, as one specific question"},
				"context":  map[string]any{"type": "string", "description": "What you already know that the helper shouldn't look for again, if anything"},
			},
			"required": []any{"question"},
		},
	}
}

// handed is what a delegate_task call asks for.
type handed struct {
	question string
	// context is what is known already, "" for nothing.
	context string
}

// readHanded is a delegate_task call's question, or why it can't be handed
// over. parent is the researcher's own question, which isn't handed back.
func readHanded(arguments, parent string) (handed, string) {
	args, err := parseValue(arguments)
	if err != nil {
		return handed{}, err.Error()
	}
	text := func(key string) string {
		s, _ := args[key].(string)
		return strings.TrimSpace(s)
	}
	question := text("question")
	if question == "" {
		return handed{}, "Pass `question`: the part to research."
	}
	if asciiFold(question) == asciiFold(strings.TrimSpace(parent)) {
		return handed{}, "That's your whole question: hand over a narrower part of it, or research it yourself."
	}
	return handed{question: question, context: text("context")}, ""
}

// asciiFold is s with its ASCII letters lowercased, and nothing else: how
// Rust's eq_ignore_ascii_case compares.
func asciiFold(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 'a' - 'A'
		}
	}
	return string(b)
}

// brief is the helper's first message: its question, and what is known
// already.
func brief(h handed) string {
	if h.context == "" {
		return "Your question: " + h.question
	}
	return fmt.Sprintf("Your question: %s\n\nKnown already - don't look for it again: %s", h.question, h.context)
}

// answer is what the researcher is told the helper kept: its claims and
// their sources' numbers, so they are cited as they are and not noted
// again.
func answer(question string, kept []Note) string {
	if len(kept) == 0 {
		return fmt.Sprintf("The helper found nothing to keep on \"%s\": cover it yourself if it matters, or leave it.", question)
	}
	plural := "s"
	if len(kept) == 1 {
		plural = ""
	}
	out := fmt.Sprintf("The helper kept %d note%s on \"%s\" with yours - don't note these again:", len(kept), plural, question)
	for _, note := range kept {
		var sources strings.Builder
		for _, n := range note.Sources {
			fmt.Fprintf(&sources, "[%d]", n)
		}
		out += fmt.Sprintf("\n- %s %s", clip(note.Claim), sources.String())
	}
	return out
}
