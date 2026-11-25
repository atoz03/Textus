package paper

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"strconv"
)

// What a paper's run tells the frontend as it goes, over its channel: the
// progress card's phase, each section's state, the budget spent, and the
// outline when it waits for the user.

// Event is one update from ai_paper_run (PaperEvent in
// src/platform/bindings/ai-paper.ts): one of the *Event types, sent as its
// fields with its type (Wire). Display only: what is kept is on disk.
type Event interface{ EventType() string }

// Send takes a run's events.
type Send func(Event)

// Wire is e as the frontend reads it: its fields, with its type first.
func Wire(e Event) jsontext.Value {
	body, err := json.Marshal(e, json.Deterministic(true))
	if err != nil || len(body) < 2 || body[0] != '{' {
		body = []byte("{}")
	}
	head := `{"type":` + strconv.Quote(e.EventType())
	if len(body) > 2 {
		head += ","
	}
	return jsontext.Value(head + string(body[1:]))
}

// PhaseEvent: the card's phase.
type PhaseEvent struct {
	Phase Phase `json:"phase"`
}

// SectionEvent: section Index of the outline is now Status.
type SectionEvent struct {
	Index  uint32        `json:"index"`
	Status SectionStatus `json:"status"`
}

// BudgetEvent: what research has spent and has left.
type BudgetEvent struct {
	Used      BudgetCounts `json:"used"`
	Remaining BudgetCounts `json:"remaining"`
}

// ReadyEvent: the paper is written - its title and abstract, its sections
// and its reference list, as one text to propose.
type ReadyEvent struct {
	Markdown string `json:"markdown"`
}

// DrawFiguresEvent: draw these diagram and chart blocks as the document
// would, and say with ai_paper_figures_drawn CallID which can't be drawn.
type DrawFiguresEvent struct {
	CallID string        `json:"callId"`
	Blocks []FigureBlock `json:"blocks"`
}

// OutlineReadyEvent: the outline is ready for the user to edit and
// approve.
type OutlineReadyEvent struct {
	Outline Outline `json:"outline"`
}

// ErrorEvent: a stage failed; Section when it was one section's.
type ErrorEvent struct {
	Message string  `json:"message"`
	Section *uint32 `json:"section"`
}

// ThinkingEvent: what the planner is thinking or writing, as it comes -
// the tail of it so far, sent a few times a second at most.
type ThinkingEvent struct {
	Text string `json:"text"`
}

// WebSearchEvent: whether research can search the web - false when the
// model has no search of its own and Settings names no search service, so
// only the scholarly indexes and the user's library are searched.
type WebSearchEvent struct {
	Available bool `json:"available"`
}

// GenreEvent: the kind of piece the request was found to call for, and
// its length in characters - sent once, before research, for a new piece.
type GenreEvent struct {
	Genre       Genre     `json:"genre"`
	TargetChars uint32    `json:"targetChars"`
	Form        GenreForm `json:"form"`
}

// QuestionsEvent: the research questions the topic was split into, in the
// order the helpers take them.
type QuestionsEvent struct {
	Questions []string `json:"questions"`
}

// ActivityEvent: a research helper did something - the tool question
// Question (its index) called, and its arguments as JSON, which the card
// words as the chat does a tool call.
type ActivityEvent struct {
	Question  uint32 `json:"question"`
	Tool      string `json:"tool"`
	Arguments string `json:"arguments"`
}

// OutlineDraftEvent: the outline as the outliner writes it - its thesis
// once whole, and the titles of the sections given so far. Attempt counts
// its tries; a try after the first follows one that was sent back.
type OutlineDraftEvent struct {
	Attempt uint32   `json:"attempt"`
	Thesis  *string  `json:"thesis"`
	Titles  []string `json:"titles"`
}

// SectionTextEvent: what section Index's writer has written so far, as it
// writes it - the whole of it, sent a few times a second at most. A try
// that starts over starts the text over. Sent once more with the section
// as kept when it is drafted or revised.
type SectionTextEvent struct {
	Index uint32 `json:"index"`
	Text  string `json:"text"`
}

// SourcesEvent: the sources the sections may cite, each as the reference
// list gives it after its number, and the list's heading - sent as
// drafting starts, so the document numbers its citations and lists them
// while the sections are written.
type SourcesEvent struct {
	Heading string        `json:"heading"`
	Sources []SourceEntry `json:"sources"`
}

// QuestionDoneEvent: question Question's helper finished, keeping Notes
// notes; Failed when it stopped on an error.
type QuestionDoneEvent struct {
	Question uint32 `json:"question"`
	Notes    uint32 `json:"notes"`
	Failed   bool   `json:"failed"`
}

func (PhaseEvent) EventType() string        { return "phase" }
func (SectionEvent) EventType() string      { return "section" }
func (BudgetEvent) EventType() string       { return "budget" }
func (ReadyEvent) EventType() string        { return "ready" }
func (DrawFiguresEvent) EventType() string  { return "drawFigures" }
func (OutlineReadyEvent) EventType() string { return "outlineReady" }
func (ErrorEvent) EventType() string        { return "error" }
func (ThinkingEvent) EventType() string     { return "thinking" }
func (WebSearchEvent) EventType() string    { return "webSearch" }
func (GenreEvent) EventType() string        { return "genre" }
func (QuestionsEvent) EventType() string    { return "questions" }
func (ActivityEvent) EventType() string     { return "activity" }
func (OutlineDraftEvent) EventType() string { return "outlineDraft" }
func (SectionTextEvent) EventType() string  { return "sectionText" }
func (SourcesEvent) EventType() string      { return "sources" }
func (QuestionDoneEvent) EventType() string { return "questionDone" }

// SourceEntry is a source of the paper: its number in the research's
// numbering, and its entry in the reference list (SourceEntry in
// ai-paper.ts).
type SourceEntry struct {
	N     uint32 `json:"n"`
	Entry string `json:"entry"`
}

// Phase is the card's phase (PaperPhase in ai-paper.ts).
type Phase string

const (
	Planning    Phase = "planning"
	Researching Phase = "researching"
	Outlining   Phase = "outlining"
	// AwaitingOutline: waiting for the user to approve the outline.
	AwaitingOutline Phase = "awaitingOutline"
	Drafting        Phase = "drafting"
	Reviewing       Phase = "reviewing"
	Finishing       Phase = "finishing"
	Done            Phase = "done"
)

// SectionStatus is where a section stands (SectionStatus in
// ai-paper.ts).
type SectionStatus string

const (
	SectionDrafting SectionStatus = "drafting"
	SectionDrafted  SectionStatus = "drafted"
	SectionRevising SectionStatus = "revising"
)

// budgetEvent is what budget stands at, for the card.
func budgetEvent(budget *SharedBudget) BudgetEvent {
	spent := budget.Snapshot()
	return BudgetEvent{Used: spent.Used, Remaining: spent.Remaining()}
}

// FigureBlock is one diagram or chart block to draw (FigureBlock in
// src/platform/bindings/ai-paper.ts).
type FigureBlock struct {
	// Language is "mermaid" or "chart".
	Language string `json:"language"`
	Code     string `json:"code"`
}
