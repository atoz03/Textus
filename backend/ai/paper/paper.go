// Package paper is the long-form paper agent: a topic researched,
// outlined, approved by the user, then drafted section by section
// (docs/design/ai-paper.md). This file holds what the stages share - the
// paper, its research dossier and outline - in the shapes
// src/platform/bindings/ai-paper.ts names.
package paper

import (
	"encoding/json/v2"

	"textus/backend/ai/aicompat"
)

// Paper is one paper being written, kept as `papers/<id>/paper.json`
// (Paper in src/platform/bindings/ai-paper.ts).
type Paper struct {
	ID    string `json:"id"`
	Topic string `json:"topic"`
	// Genre is what kind of piece it is; a paper kept before genres is a
	// paper.
	Genre Genre `json:"genre"`
	// Language is the language it is written in, a BCP 47 tag ("zh",
	// "en").
	Language string `json:"language"`
	// TargetChars is how long the finished text should be, in characters.
	TargetChars uint32 `json:"targetChars"`
	// Budget is what its research may still spend; kept so a resumed paper
	// can't start over with a full budget.
	Budget ResearchBudget `json:"budget"`
	// Stage is how far it has come, so a paper stopped - or the app quit -
	// goes on from there.
	Stage Stage `json:"stage"`
	// SubHelpers: a question's researcher may hand parts of it to helpers
	// of its own - the user's choice, set again on every run.
	SubHelpers bool `json:"subHelpers"`
}

// UnmarshalJSON reads a kept paper, one kept before genres and stages as
// a paper being researched.
func (p *Paper) UnmarshalJSON(raw []byte) error {
	type plain Paper
	read := plain{Genre: GenrePaper, Stage: StageResearching}
	if err := json.Unmarshal(raw, &read); err != nil {
		return err
	}
	*p = Paper(read)
	return nil
}

// Stage is how far a paper has come (PaperStage in ai-paper.ts).
type Stage string

const (
	// StageResearching: begun; its research is kept question by question
	// in research.json, and as the dossier once it is whole.
	StageResearching Stage = "researching"
	// StageOutlined: researched and outlined, waiting for the outline to
	// be approved.
	StageOutlined Stage = "outlined"
	// StageDrafting: its outline approved, being drafted, its sections kept
	// as written.
	StageDrafting Stage = "drafting"
	// StageReviewed: drafted, reviewed and revised - only finishing is
	// left, so going on doesn't review and revise it again.
	StageReviewed Stage = "reviewed"
	// StageDone: written into its document.
	StageDone Stage = "done"
)

// UnmarshalText refuses a stage there is none of.
func (s *Stage) UnmarshalText(text []byte) error {
	return oneOf(s, text, StageResearching, StageOutlined, StageDrafting, StageReviewed, StageDone)
}

// Dossier is what the research helpers found, kept as `dossier.json`.
type Dossier struct {
	Notes []Note `json:"notes"`
	// Sources are the sources the notes cite, numbered once for the whole
	// paper: what Note.Sources and the text's [n] point at, and the
	// reference list.
	Sources []aicompat.WebSource `json:"sources"`
}

// Note is one finding: a claim and what supports it.
type Note struct {
	// ID is "n1", "n2", ...: what an outline section's Evidence names.
	ID string `json:"id"`
	// Question is the research question it answers.
	Question string `json:"question"`
	Claim    string `json:"claim"`
	Evidence string `json:"evidence"`
	// Quote is words taken verbatim from a source, if any.
	Quote *string `json:"quote"`
	// Sources are the sources' numbers in the paper's source book.
	Sources []uint32 `json:"sources"`
	Leads   []Lead   `json:"leads"`
	// Grounding is how much of its sources the note was checked against.
	Grounding Grounding `json:"grounding"`
	// Excerpt is the passage of a source the note rests on, in the
	// source's words.
	Excerpt *string `json:"excerpt,omitzero"`
	// Unmatched are figures the note gives that its sources didn't, kept
	// because its researcher sent it again unchanged.
	Unmatched []string `json:"unmatched,omitempty"`
}

// UnmarshalJSON reads a note kept before its checks as unchecked.
func (n *Note) UnmarshalJSON(raw []byte) error {
	type plain Note
	read := plain{Grounding: Unchecked}
	if err := json.Unmarshal(raw, &read); err != nil {
		return err
	}
	*n = Note(read)
	return nil
}

// Grounding is how much of its sources a note was checked against.
type Grounding string

const (
	// Unchecked: none of them was shown to the researcher (a page the
	// provider's own search cited, an address from a plugin), or the note
	// is older than the checks.
	Unchecked Grounding = "unchecked"
	// Unread: cited by an address nothing showed it - no search, read or
	// the provider's own search (a plugin's or a tool's, or one made up).
	Unread Grounding = "unread"
	// SnippetGrounded: search snippets or abstracts only.
	SnippetGrounded Grounding = "snippet"
	// ReadGrounded: a page read in full, or a file.
	ReadGrounded Grounding = "read"
)

// UnmarshalText refuses a grounding there is none of.
func (g *Grounding) UnmarshalText(text []byte) error {
	return oneOf(g, text, Unchecked, Unread, SnippetGrounded, ReadGrounded)
}

// Lead is a picture or a data set a note's source has that a figure could
// use.
type Lead struct {
	Kind        LeadKind `json:"kind"`
	Description string   `json:"description"`
	URL         *string  `json:"url"`
}

// LeadKind is what a lead is.
type LeadKind string

const (
	ImageLead LeadKind = "image"
	DataLead  LeadKind = "data"
)

// UnmarshalText refuses a lead there is none of.
func (k *LeadKind) UnmarshalText(text []byte) error {
	return oneOf(k, text, ImageLead, DataLead)
}

// Outline is the plan the user approves before drafting, kept as
// `outline.json` (Outline in ai-paper.ts).
type Outline struct {
	Thesis   string           `json:"thesis"`
	Sections []OutlineSection `json:"sections"`
}

// OutlineSection is one section of the outline.
type OutlineSection struct {
	Title     string   `json:"title"`
	KeyPoints []string `json:"keyPoints"`
	// Evidence are the dossier notes it draws on, by id.
	Evidence    []string     `json:"evidence"`
	TargetWords uint32       `json:"targetWords"`
	Figures     []FigurePlan `json:"figures"`
}

// FigurePlan is a figure the section will have, and how it is made.
type FigurePlan struct {
	Kind FigureKind `json:"kind"`
	// Spec is what to draw: a picture prompt, the diagram or chart to make
	// from which notes, or the picture to look for.
	Spec string `json:"spec"`
}

// FigureKind is how a figure is made.
type FigureKind string

const (
	// Generated: a picture made by the image service (`textus-picture:`).
	Generated FigureKind = "generated"
	Mermaid   FigureKind = "mermaid"
	// Chart: a ```chart block from the dossier's data.
	Chart FigureKind = "chart"
	// WebPicture: a picture found on the web, with its source and licence.
	WebPicture FigureKind = "web"
)

// UnmarshalText refuses a figure kind there is none of.
func (k *FigureKind) UnmarshalText(text []byte) error {
	return oneOf(k, text, Generated, Mermaid, Chart, WebPicture)
}

// oneOf sets *v to text when it is one of values, as serde reads an enum.
func oneOf[T ~string](v *T, text []byte, values ...T) error {
	for _, value := range values {
		if string(text) == string(value) {
			*v = value
			return nil
		}
	}
	return &unknownVariant{string(text)}
}

type unknownVariant struct{ text string }

func (e *unknownVariant) Error() string { return "unknown variant `" + e.text + "`" }
