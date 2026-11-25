package paper

import (
	"context"
	"errors"

	"textus/backend/ai/aicompat"
	"textus/backend/ai/web"
)

// A paper's two runs, as the app starts them: Run researches and outlines
// it and stops for the user to approve the outline; Write drafts it from
// the approved outline to the finished text. Each goes on from what a run
// before it kept, and keeps what it spends of the paper's budget however
// it ends.

// Session is what a run takes from the app: where the paper is kept, the
// provider's agent, what the user asks of their writing, and the card the
// run reports to.
type Session struct {
	// Root is the folder the paper is kept under (RootIn's).
	Root string
	// Agent is the provider's agent, and AtOnce how many of its calls a run
	// makes at once.
	Agent  Model
	AtOnce int
	// Guidance is what the user asks of all the writing done for them -
	// their instructions, memories, style and library - for a piece on
	// topic.
	Guidance func(topic string) string
	// Send sends an event to the card; false once its window is gone.
	Send func(Event) bool
}

// send is Send for an event whose delivery changes nothing.
func (s *Session) send(e Event) { s.Send(e) }

// metered is the agent with at most AtOnce calls in flight, each spent
// from budget.
func (s *Session) metered(budget *SharedBudget) Metered {
	return Metered{Inner: NewThrottled(s.Agent, s.AtOnce), Budget: budget}
}

// Request is a paper to research and outline, and what the research may
// use (PaperRequest in src/platform/bindings/ai-paper.ts, with what the
// app makes of its AgentRequest).
type Request struct {
	// ID is the paper to go on with, or the id a new one is kept under; ""
	// for a new id.
	ID    string
	Topic string
	// Genre is the kind of piece to write; nil for the model to choose
	// from the topic.
	Genre *Genre
	// Language is the language it is written in, a BCP 47 tag.
	Language string
	// TargetChars is its length in characters; nil for the genre's, or the
	// model's choice.
	TargetChars *uint32
	// SubHelpers: research hands parts of a question to helpers of its
	// own.
	SubHelpers bool
	// Conversation is the scratch folder of the conversation whose
	// attached files the research reads, copied into the paper's folder
	// so going on needs none; "" for none.
	Conversation string
	// Pictures is how many pictures one send of the image model may make,
	// 0 without one: the outline plans no more than can be drawn.
	Pictures uint32
	// Native is the model's own web search and Service the search service
	// Settings chose; nil for none.
	Native  *aicompat.NativeSearch
	Service *web.Service
	// Kit is the researchers' kit, over the paper's folder of attached
	// files ("" for none).
	Kit func(folder string) *Kit
}

// Run researches and outlines the paper r asks for, kept under s.Root; a
// paper begun before goes on from what it kept - its research, its
// outline, what its research may still spend. It answers the paper, its
// outline sent to the card to approve.
func Run(ctx context.Context, s *Session, r Request) (Paper, error) {
	var kept *Files
	if r.ID != "" {
		if files, err := Load(s.Root, r.ID); err == nil {
			kept = &files
		}
	}
	// As many pictures as one send may make, the paper's own limit aside.
	limits := PaperBudget()
	if kept != nil {
		limits = kept.Paper.Budget.Renewed()
	}
	images := limits.Limits.Images
	limits.Limits.Images = min(images, r.Pictures)
	budget := NewSharedBudget(limits)
	plan := web.NewPlan(true, r.Native, r.Service, nil).WithBudget(budget).KeepingSeenText().WithContext(ctx)
	s.send(WebSearchEvent{Available: r.Native != nil || r.Service != nil})
	agent := s.metered(budget)
	var files Files
	if kept != nil {
		files = *kept
	} else {
		files, images = begin(ctx, s, r, agent, budget)
	}
	// Kept at once, so a paper stopped while researching is found and
	// begun again.
	if err := Save(s.Root, files); err != nil {
		return Paper{}, err
	}
	folder, err := KeepAttachments(s.Root, files.Paper.ID, r.Conversation)
	if err != nil {
		return Paper{}, err
	}
	spent := &keepSpent{root: s.Root, id: files.Paper.ID, budget: budget, images: images}
	defer spent.finish()
	if len(files.Dossier.Notes) == 0 {
		if err := research(ctx, s, r.Kit(folder), plan, agent, &files, spent); err != nil {
			return Paper{}, err
		}
	}
	outline := files.Outline
	if outline == nil {
		s.send(PhaseEvent{Phase: Outlining})
		// Outlined, like everything written, as the user asks of their
		// writing; researched plainly.
		guided := Guided{Inner: agent, Guidance: s.Guidance(files.Paper.Topic)}
		made, err := MakeOutline(ctx, guided, &files.Paper, &files.Dossier, s.send)
		if err != nil {
			return Paper{}, err
		}
		outline = &made
	}
	files.Outline = outline
	if files.Paper.Stage == StageResearching {
		files.Paper.Stage = StageOutlined
	}
	files.Paper.Budget = spent.spent()
	if err := Save(s.Root, files); err != nil {
		return Paper{}, err
	}
	spent.done = true
	s.send(OutlineReadyEvent{Outline: *outline})
	s.send(PhaseEvent{Phase: AwaitingOutline})
	return files.Paper, nil
}

// begin is a new paper's files: its genre and length as r asks or as the
// model chooses, and its budget the genre's - with the paper's own picture
// limit, which it answers too.
func begin(ctx context.Context, s *Session, r Request, agent Model, budget *SharedBudget) (Files, uint32) {
	var genre Genre
	var target uint32
	if r.Genre != nil {
		genre, target = *r.Genre, r.Genre.Length(r.TargetChars)
	} else {
		genre, target = Choose(ctx, agent, r.Topic, r.TargetChars)
	}
	limits := GenreBudget(genre).Limits
	images := limits.Images
	limits.Images = min(images, r.Pictures)
	budget.SetLimits(limits)
	s.send(GenreEvent{Genre: genre, TargetChars: target, Form: genre.Form()})
	id := r.ID
	if id == "" {
		id = NewID()
	}
	return Files{Paper: Paper{
		ID: id, Topic: r.Topic, Genre: genre, Language: r.Language, TargetChars: target,
		Budget: budget.Snapshot(), Stage: StageResearching, SubHelpers: r.SubHelpers,
	}}, images
}

// research researches files' paper into its dossier, kept with what it
// spent. What a run stopped mid-research kept - its questions, the notes
// of those it finished - is gone on from.
func research(ctx context.Context, s *Session, kit *Kit, plan *web.Plan, agent Metered, files *Files, spent *keepSpent) error {
	id := files.Paper.ID
	progress := Progress{
		Kept: LoadResearch(s.Root, id),
		// Best kept: a write that fails costs only going on from it.
		Keep: func(p ResearchProgress) { _ = SaveResearch(s.Root, id, p) },
	}
	dossier, err := Research(ctx, agent, &files.Paper, plan, agent.Budget, s.send, kit, progress)
	if err != nil {
		return err
	}
	// Questions cut short would leave it thin: going on asks them again,
	// the finished ones kept.
	if agent.Spent() {
		return errors.New(StepsSpent)
	}
	files.Dossier = dossier
	files.Paper.Budget = spent.spent()
	// Kept before the outline, so a failed outline doesn't cost the
	// research.
	if err := Save(s.Root, *files); err != nil {
		return err
	}
	// Whole now, in the dossier; one left behind is never read again.
	_ = ClearResearch(s.Root, id)
	return nil
}
