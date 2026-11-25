package paper

import (
	"fmt"
	"slices"
	"strings"

	"textus/backend/ai/aicompat"
)

// What a section's writer is told: the paper, its section and notes with
// how far their sources were read, its figures, the submit_section tool,
// and what to change when a draft is sent back.

// figureLine is how a figure of the section is to be written into it.
func figureLine(figure FigurePlan) string {
	name := map[FigureKind]string{
		Mermaid:    "draft.figure.mermaid",
		Chart:      "draft.figure.chart",
		Generated:  "draft.figure.generated",
		WebPicture: "draft.figure.web",
	}[figure.Kind]
	return fmt.Sprintf("- %s - as %s", figure.Spec, prompt(name))
}

// sectionNotes are the dossier's notes section index draws on, in the
// dossier's order.
func sectionNotes(section *OutlineSection, dossier *Dossier) []*Note {
	var out []*Note
	for i := range dossier.Notes {
		if slices.Contains(section.Evidence, dossier.Notes[i].ID) {
			out = append(out, &dossier.Notes[i])
		}
	}
	return out
}

// source is the dossier's source numbered n.
func (d *Dossier) source(n uint32) (aicompat.WebSource, bool) {
	for _, s := range d.Sources {
		if s.N == n {
			return s, true
		}
	}
	return aicompat.WebSource{}, false
}

// writerMessage is the writer's message: its section, the rest of the
// paper in outline, and its notes.
func writerMessage(paper *Paper, outline *Outline, index int, dossier *Dossier) string {
	section := &outline.Sections[index]
	others := make([]string, len(outline.Sections))
	for i, s := range outline.Sections {
		mark := ""
		if i == index {
			mark = "  <- yours"
		}
		others[i] = fmt.Sprintf("%d. %s - %s%s", i+1, s.Title, strings.Join(s.KeyPoints, "; "), mark)
	}
	var notes []string
	for _, n := range sectionNotes(section, dossier) {
		var cite []string
		for _, number := range n.Sources {
			if s, ok := dossier.source(number); ok {
				cite = append(cite, fmt.Sprintf("[%d](%s)", s.N, s.URL))
			}
		}
		quote := ""
		if n.Quote != nil {
			quote = fmt.Sprintf(" Quote: \"%s\"", *n.Quote)
		}
		notes = append(notes, fmt.Sprintf("- %s Evidence: %s%s%s Sources: %s%s", n.Claim, n.Evidence, quote, groundingLine(n), strings.Join(cite, " "), sourcePassage(n)))
	}
	var figures []string
	for _, f := range section.Figures {
		figures = append(figures, figureLine(f))
	}
	listed := func(lines []string) string {
		if len(lines) == 0 {
			return "(none)"
		}
		return strings.Join(lines, "\n")
	}
	return fill("draft.message", kv{"topic", paper.Topic}, kv{"thesis", outline.Thesis}, kv{"sections", strings.Join(others, "\n")},
		kv{"title", section.Title}, kv{"target", section.TargetWords}, kv{"most", most(section.TargetWords)},
		kv{"points", strings.Join(section.KeyPoints, "\n- ")}, kv{"notes", listed(notes)}, kv{"figures", listed(figures)})
}

// revisionMessage is a reviser's message: the writer's message, its
// earlier draft and the reviewer's points against it.
func revisionMessage(paper *Paper, outline *Outline, index int, dossier *Dossier, earlier string, points []string) string {
	return fill("draft.revision", kv{"message", writerMessage(paper, outline, index, dossier)}, kv{"earlier", earlier},
		kv{"points", strings.Join(points, "\n- ")})
}

func submitSectionSpec() aicompat.ToolSpec {
	return aicompat.ToolSpec{
		Name:        submitSectionTool,
		Description: "Hand over your section, whole, as Markdown without its own heading. It is checked - length, citations, figures - and if anything is wrong you are told what; send the whole section again, corrected.",
		Parameters: map[string]any{
			"type":       "object",
			"properties": map[string]any{"markdown": map[string]any{"type": "string"}},
			"required":   []any{"markdown"},
		},
	}
}

// writerInstructions is what a section's writer is told.
func writerInstructions(paper *Paper) string {
	profile := paper.Genre.Profile()
	unit := "words"
	if CountsCharacters(paper.Language) {
		unit = "characters"
	}
	return fill("draft.instructions", kv{"genre", profile.Name}, kv{"language", paper.Language}, kv{"voice", profile.Voice},
		kv{"cite", profile.Cite}, kv{"unit", unit})
}

// groundingLine is how far a note's sources were seen, for its writer: how
// much of them was read, and figures they didn't give.
func groundingLine(note *Note) string {
	var line strings.Builder
	switch note.Grounding {
	case SnippetGrounded:
		line.WriteString(" (Only a search snippet was read: state it no more firmly than the snippet does.)")
	case Unread:
		line.WriteString(" (Its source was never read: say whose claim it is - \"according to …\" - rather than state it as fact.)")
	}
	if len(note.Unmatched) > 0 {
		fmt.Fprintf(&line, " (Not in its sources: %s - leave these out, or say whose figure it is.)", strings.Join(note.Unmatched, ", "))
	}
	return line.String()
}
