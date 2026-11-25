package paper

import (
	"context"
	"errors"
	"sync"

	"textus/backend/ai"
	"textus/backend/ai/web"
)

// Write drafts paper id from its saved outline: every section written and
// kept beside the paper, the whole reviewed and the sections it faults
// revised once - nothing is proposed before the review, so no proposal is
// replaced after it - then finished and sent whole as a ReadyEvent.
// checkClaims checks every cited sentence against its notes before the
// review's revision (the user's choice; slower). pictures begins the
// paper's pictures; nil leaves them out.
func Write(ctx context.Context, s *Session, id string, checkClaims bool, pictures *ai.ImageRun) error {
	files, err := Load(s.Root, id)
	if err != nil {
		return err
	}
	if files.Outline == nil {
		return errors.New("This paper has no outline yet.")
	}
	outline := *files.Outline
	paper := &files.Paper
	// Each section's diagrams and charts are drawn by the window it is
	// written for before it is kept.
	draw := ByWindow(s.Send)
	budget := NewSharedBudget(paper.Budget.Renewed())
	spent := &keepSpent{root: s.Root, id: paper.ID, budget: budget, images: paper.Budget.Limits.Images}
	defer spent.finish()
	metered := s.metered(budget)
	agent := Guided{Inner: metered, Guidance: s.Guidance(paper.Topic)}
	entries := make([]SourceEntry, len(files.Dossier.Sources))
	for i, source := range files.Dossier.Sources {
		entries[i] = SourceEntry{N: source.N, Entry: Entry(source)}
	}
	s.send(SourcesEvent{Heading: ReferencesHeading(paper.Language, paper.Genre), Sources: entries})
	s.send(PhaseEvent{Phase: Drafting})
	sections, err := draftAll(ctx, s, agent, &files, &outline, draw)
	if err != nil {
		return err
	}
	// Reviewed before a stop: its kept sections are the revised ones.
	if paper.Stage != StageReviewed {
		s.send(PhaseEvent{Phase: Reviewing})
		// A review that fails costs the paper nothing: its sections passed
		// their own checks.
		issues := reviewAll(ctx, agent, paper, &outline, &files.Dossier, sections, checkClaims)
		revising := func(i int) { s.send(SectionEvent{Index: uint32(i), Status: SectionRevising}) }
		for _, i := range revise(ctx, agent, paper, &outline, &files.Dossier, sections, issues, revising, s.send, draw) {
			if err := SaveSection(s.Root, paper.ID, i, sections[i]); err != nil {
				return err
			}
			s.send(SectionTextEvent{Index: uint32(i), Text: sections[i]})
		}
		if metered.Spent() {
			return errors.New(StepsSpent)
		}
		reviewed := files
		reviewed.Paper.Stage = StageReviewed
		if err := Save(s.Root, reviewed); err != nil {
			return err
		}
	}
	s.send(PhaseEvent{Phase: Finishing})
	s.send(ReadyEvent{Markdown: finishPaper(ctx, agent, paper, &outline, &files.Dossier, sections, pictures, draw)})
	paper.Stage = StageDone
	paper.Budget = spent.spent()
	if err := Save(s.Root, files); err != nil {
		return err
	}
	spent.done = true
	s.send(PhaseEvent{Phase: Done})
	return nil
}

// draftAll is every section of files' paper written: those an earlier run
// of this draft wrote as they were, the rest by agent, each kept beside
// the paper and shown on the card as it is done.
func draftAll(ctx context.Context, s *Session, agent Model, files *Files, outline *Outline, draw Draw) ([]string, error) {
	id := files.Paper.ID
	written := map[int]string{}
	for i := range outline.Sections {
		text, ok, err := LoadSection(s.Root, id, i)
		if err != nil {
			return nil, err
		}
		if ok {
			written[i] = text
		}
	}
	started := func(i int) { s.send(SectionEvent{Index: uint32(i), Status: SectionDrafting}) }
	kept := func(i int, markdown string) error {
		if err := SaveSection(s.Root, id, i, markdown); err != nil {
			return err
		}
		// The stream's last words may have been held back.
		s.send(SectionTextEvent{Index: uint32(i), Text: markdown})
		s.send(SectionEvent{Index: uint32(i), Status: SectionDrafted})
		return nil
	}
	return Draft(ctx, agent, &files.Paper, outline, &files.Dossier, written, started, kept, s.send, draw)
}

// finishPaper is the reviewed sections made a paper: their web pictures
// found, their pictures begun, every figure drawn and numbered, the
// citations numbered as the paper first cites them, and the opening and
// the reference list around them.
func finishPaper(ctx context.Context, agent Model, paper *Paper, outline *Outline, dossier *Dossier, sections []string, pictures *ai.ImageRun, draw Draw) string {
	// Web pictures first: one Commons has nothing for becomes a picture to
	// generate, begun with the planned ones.
	sections = ResolveWebPictures(ctx, sections, FindOnCommons)
	for i, section := range sections {
		// Pictures begun before the figures are numbered, so one that can't
		// be made leaves no caption behind.
		if pictures != nil {
			section, _ = ai.PlacePictures(pictures, section)
		} else {
			section = WithoutPictures(section)
		}
		// A figure no check saw drawn is drawn now, and taken out if it
		// can't be: the paper never shows a diagram or chart as its code.
		sections[i] = drawable(ctx, draw, section)
	}
	NumberFigures(sections, paper.Language)
	// Numbered first, so only the sources cited are looked up.
	numbered := NumberCitations(sections, dossier)
	plain := make([]string, len(sections))
	for i, section := range sections {
		plain[i] = PlainCitations(section)
	}
	// The opening reads the text, not the reference list: it is written
	// while the sources' titles are looked up.
	var wg sync.WaitGroup
	wg.Go(func() { TitleSources(ctx, &numbered, LookUpTitle(web.DOITitle, web.PageTitle)) })
	opening := Opening(ctx, agent, paper, outline, plain)
	wg.Wait()
	references, _ := References(sections, &numbered, ReferencesHeading(paper.Language, paper.Genre))
	return Assemble(opening, plain, references)
}
