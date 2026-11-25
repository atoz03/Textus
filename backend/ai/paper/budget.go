package paper

import (
	"math"
	"sync"

	"textus/backend/ai/web"
)

// What one paper's research may spend, in all its helpers together - in
// place of chat's five searches and five page reads per request.

// ModelSteps are the model calls a paper may make: no limit - Research
// mode runs until the paper is done or the user pauses it. Still counted,
// in Used.
const ModelSteps uint32 = math.MaxUint32

// StepsSpent is a paper's error when its model calls are spent; the card
// says so.
const StepsSpent = "PAPER_STEPS_SPENT"

// BudgetCounts is a count of each thing research spends (BudgetCounts in
// src/platform/bindings/ai-paper.ts).
type BudgetCounts struct {
	Searches     uint32 `json:"searches"`
	Fetches      uint32 `json:"fetches"`
	PaperLookups uint32 `json:"paperLookups"`
	Images       uint32 `json:"images"`
	// ModelSteps are calls to the writing model, in every stage together.
	ModelSteps uint32 `json:"modelSteps"`
}

// BudgetKind is one thing research spends.
type BudgetKind int

const (
	Search BudgetKind = iota
	Fetch
	PaperLookup
	// Image is a picture made by the image service.
	Image
	// ModelStep is one call to the writing model.
	ModelStep
)

func (c *BudgetCounts) of(kind BudgetKind) *uint32 {
	switch kind {
	case Search:
		return &c.Searches
	case Fetch:
		return &c.Fetches
	case PaperLookup:
		return &c.PaperLookups
	case Image:
		return &c.Images
	}
	return &c.ModelSteps
}

// ResearchBudget is a paper's limits and what it has used of them.
type ResearchBudget struct {
	Limits BudgetCounts `json:"limits"`
	Used   BudgetCounts `json:"used"`
}

// NewBudget is limits with nothing used.
func NewBudget(limits BudgetCounts) ResearchBudget { return ResearchBudget{Limits: limits} }

// PaperBudget is enough for 6-10 research questions on a 5,000-10,000
// character paper.
func PaperBudget() ResearchBudget { return GenreBudget(GenrePaper) }

// GenreBudget is what genre's research may spend.
func GenreBudget(genre Genre) ResearchBudget {
	b := genre.Profile().Scale.Budget
	return NewBudget(BudgetCounts{Searches: b[0], Fetches: b[1], PaperLookups: b[2], Images: b[3], ModelSteps: ModelSteps})
}

// Renewed: a paper gone on with has no model call limit, a paper kept
// under an older, limited budget too.
func (b ResearchBudget) Renewed() ResearchBudget {
	b.Limits.ModelSteps = ModelSteps
	return b
}

// Take counts one use of kind; false, and nothing counted, when none is
// left.
func (b *ResearchBudget) Take(kind BudgetKind) bool {
	limit, used := *b.Limits.of(kind), b.Used.of(kind)
	if *used >= limit {
		return false
	}
	*used++
	return true
}

// Remaining is what is left of each kind.
func (b ResearchBudget) Remaining() BudgetCounts {
	var left BudgetCounts
	for kind := Search; kind <= ModelStep; kind++ {
		limit, used := *b.Limits.of(kind), *b.Used.of(kind)
		if limit > used {
			*left.of(kind) = limit - used
		}
	}
	return left
}

// SharedBudget is a budget the research helpers running at once all count
// against; a web plan counts its searches and reads against it.
type SharedBudget struct {
	mu     sync.Mutex
	budget ResearchBudget
}

// NewSharedBudget shares budget.
func NewSharedBudget(budget ResearchBudget) *SharedBudget { return &SharedBudget{budget: budget} }

// TakeKind counts one use of kind; false when none is left.
func (s *SharedBudget) TakeKind(kind BudgetKind) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.budget.Take(kind)
}

// SetLimits makes its limits limits, the model steps aside: a new
// piece's, once its genre is chosen.
func (s *SharedBudget) SetLimits(limits BudgetCounts) {
	s.mu.Lock()
	defer s.mu.Unlock()
	limits.ModelSteps = s.budget.Limits.ModelSteps
	s.budget.Limits = limits
}

// Snapshot is what it stands at now, to keep with the paper or show the
// user.
func (s *SharedBudget) Snapshot() ResearchBudget {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.budget
}

func webKind(kind web.Use) BudgetKind {
	switch kind {
	case web.Searches:
		return Search
	case web.Fetches:
		return Fetch
	}
	return PaperLookup
}

// Take is web.Budget's: one search, read or paper lookup.
func (s *SharedBudget) Take(kind web.Use) bool { return s.TakeKind(webKind(kind)) }

// Limit is web.Budget's: how many of kind it allows in all.
func (s *SharedBudget) Limit(kind web.Use) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return int(*s.budget.Limits.of(webKind(kind)))
}

// keepSpent writes what research has spent into the kept paper when it
// ends short - stopped, failed - so going on doesn't begin again with a
// full budget: its finish runs deferred, unless done.
type keepSpent struct {
	root, id string
	budget   *SharedBudget
	// images is the paper's own picture limit, not this run's image
	// model's.
	images uint32
	done   bool
}

// spent is the budget as kept with the paper.
func (k *keepSpent) spent() ResearchBudget {
	spent := k.budget.Snapshot()
	spent.Limits.Images = k.images
	return spent
}

// finish keeps what was spent, unless the run ended as it should.
func (k *keepSpent) finish() {
	if k.done {
		return
	}
	if files, err := Load(k.root, k.id); err == nil {
		files.Paper.Budget = k.spent()
		_ = Save(k.root, files)
	}
}
