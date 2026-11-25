package ai

import (
	"fmt"
	"strings"
)

// AgentMode is the conversation's mode, picked in the composer: it decides
// what propose_edit may do, which skills apply and what the prompt says
// (src/platform/bindings/ai-skills.ts AgentMode). Ordered as the modes
// sort in a skill's list.
type AgentMode uint8

const (
	// ModeReview reads, answers and comments: no propose_edit.
	ModeReview AgentMode = iota
	// ModeWrite adds text and changes the text that is there: every
	// propose_edit action. The old Revise mode reads as this one.
	ModeWrite
	// ModeCreate makes things for the app itself - a theme, a plugin, a
	// skill - as files in the reply, which the chat offers to install: no
	// propose_edit, no comments.
	ModeCreate
)

// ModeFromWord is the mode a skill file's `textus-modes` word names.
func ModeFromWord(word string) (AgentMode, bool) {
	switch strings.ToLower(strings.TrimSpace(word)) {
	case "write", "revise":
		return ModeWrite, true
	case "review":
		return ModeReview, true
	case "create":
		return ModeCreate, true
	}
	return 0, false
}

func (m AgentMode) String() string {
	switch m {
	case ModeReview:
		return "review"
	case ModeCreate:
		return "create"
	}
	return "write"
}

func (m AgentMode) MarshalText() ([]byte, error) { return []byte(m.String()), nil }

func (m *AgentMode) UnmarshalText(text []byte) error {
	switch string(text) {
	case "review", "write", "revise", "create":
		*m, _ = ModeFromWord(string(text))
		return nil
	}
	return fmt.Errorf("unknown agent mode %q", text)
}
