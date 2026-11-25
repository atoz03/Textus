package paper

import (
	"math"
	"sync"
)

// What kind of piece is written - a paper, a review, a report - and how
// that changes each stage: what the research looks for, how the outline is
// ordered, the writers' voice, what the review reads for and what opens
// the piece. A genre is data, a Profile - its words in prompts/genres/,
// its sizes here; the stages put its parts into their prompts and keep
// their own checks.

// Genre is the kind of piece (Genre in src/platform/bindings/ai-paper.ts).
type Genre string

const (
	// GenrePaper: a researched paper with a thesis, an abstract and
	// numbered references.
	GenrePaper Genre = "paper"
	// GenreReview: a literature review - what the research on a question
	// has found, strand by strand, and what it leaves open.
	GenreReview Genre = "review"
	// GenreReport: a long read for the general reader, as a news
	// organisation writes one: a headline, a standfirst, and its sources
	// named in the text.
	GenreReport Genre = "report"
	// GenreAnalysis: an analysis for decision-makers - of a market, an
	// industry or a policy: an executive summary, the findings first,
	// recommendations.
	GenreAnalysis Genre = "analysis"
	// GenreArticle: a feature for a newsletter or a WeChat official
	// account - a few thousand characters, crossheads, a headline and a
	// standfirst.
	GenreArticle Genre = "article"
	// GenreThread: a thread of numbered posts for X (Twitter) or Weibo,
	// each short enough for one post.
	GenreThread Genre = "thread"
	// GenreBrief: a short news story of a few hundred characters - the
	// news first, for quick, automated reporting.
	GenreBrief Genre = "brief"
)

// Genres are every genre, in the order the choosing step is told them.
var Genres = []Genre{GenrePaper, GenreReview, GenreReport, GenreAnalysis, GenreArticle, GenreThread, GenreBrief}

// UnmarshalText refuses a genre there is none of.
func (g *Genre) UnmarshalText(text []byte) error {
	return oneOf(g, text, Genres...)
}

// GenreForm is what the app does differently for a genre (GenreForm in
// src/platform/bindings/ai-paper.ts).
type GenreForm struct {
	// Checkpoint: the user approves its outline; false for a piece written
	// straight on.
	Checkpoint bool `json:"checkpoint"`
	// Headings: its sections go under headings of their own; false for a
	// piece of one section, all under its title.
	Headings bool `json:"headings"`
}

// Frame is how a piece is set around its sections.
type Frame int

const (
	// Academic: an abstract labelled as one, and a list of references.
	Academic Frame = iota
	// News: a standfirst in bold with no label, and a list of sources.
	News
	// Headline: only a headline, and a list of sources.
	Headline
	// Executive: an executive summary labelled as one, and a list of
	// sources.
	Executive
)

// span is a least and a most.
type span[T any] struct{ least, most T }

// Scale is how big a genre's piece is and how it is made.
type Scale struct {
	// Questions are its research questions, at least and at most.
	Questions span[int]
	// Budget is what its research may spend: searches, page reads,
	// scholarly lookups and generated pictures.
	Budget [4]uint32
	// Sections, at least and at most, and about how many characters each.
	Sections        span[int]
	CharsPerSection uint32
	// FigureEvery: one figure at least for every this many sections; 0 for
	// a piece with no figures.
	FigureEvery int
	// Checkpoint: the user approves its outline before it is written.
	Checkpoint bool
	// PostChars is a post's most characters when the piece is a thread of
	// posts; 0 otherwise.
	PostChars uint32
}

// long is a paper's and a long read's scale.
var long = Scale{
	Questions:       span[int]{4, 10},
	Budget:          [4]uint32{40, 60, 30, 6},
	Sections:        span[int]{3, 12},
	CharsPerSection: 800,
	FigureEvery:     2,
	Checkpoint:      true,
}

// Profile is what a genre puts into the stages' prompts. Its words are in
// prompts/genres/<name>.md, one part a heading; its sizes are here.
type Profile struct {
	// Name is what the piece is called in the prompts, and its file's name.
	Name string
	// When a request calls for it, as the step choosing a genre reads it.
	When string
	// Chars is its length in characters, at least and at most.
	Chars span[uint32]
	// ResearchAngles is what its research questions cover between them.
	ResearchAngles string
	// ResearchSources is what a research helper reads and keeps, and which
	// sources it prefers.
	ResearchSources string
	// Thesis is what the outline's thesis is.
	Thesis string
	// OutlineOrder is the order its sections go in.
	OutlineOrder string
	// Voice is how a section is written: its reader, its voice, what it
	// makes of the notes.
	Voice string
	// Cite is how its text marks where a fact comes from.
	Cite string
	// ReviewFor is what the review reads the whole for.
	ReviewFor string
	// Opening is what opens the piece, its title included; the step
	// replies with {"title", "abstract"}.
	Opening string
	Frame   Frame
	Scale   Scale
}

// sizes are a genre's name, length, frame and scale: what isn't words.
type sizes struct {
	name  string
	chars span[uint32]
	frame Frame
	scale Scale
}

func with(scale Scale, change func(*Scale)) Scale {
	change(&scale)
	return scale
}

var genreSizes = map[Genre]sizes{
	GenrePaper:  {"paper", span[uint32]{5000, 10000}, Academic, long},
	GenreReview: {"review", span[uint32]{5000, 10000}, Academic, with(long, func(s *Scale) { s.Budget, s.FigureEvery = [4]uint32{30, 50, 50, 4}, 3 })},
	GenreReport: {"report", span[uint32]{5000, 10000}, News, long},
	GenreAnalysis: {"analysis", span[uint32]{5000, 10000}, Executive, with(long, func(s *Scale) {
		s.Budget = [4]uint32{50, 60, 10, 4}
	})},
	GenreArticle: {"article", span[uint32]{1500, 4000}, News, Scale{
		Questions: span[int]{3, 5}, Budget: [4]uint32{15, 20, 5, 3}, Sections: span[int]{2, 6},
		CharsPerSection: 600, FigureEvery: 3,
	}},
	GenreThread: {"thread", span[uint32]{600, 2500}, Headline, Scale{
		Questions: span[int]{2, 4}, Budget: [4]uint32{10, 12, 0, 0}, Sections: span[int]{1, 1},
		CharsPerSection: math.MaxUint32, PostChars: 280,
	}},
	GenreBrief: {"brief", span[uint32]{300, 1500}, Headline, Scale{
		Questions: span[int]{1, 3}, Budget: [4]uint32{8, 10, 0, 0}, Sections: span[int]{1, 1},
		CharsPerSection: math.MaxUint32,
	}},
}

// profile is these sizes' profile, with their words from their file.
func (s sizes) profile() *Profile {
	part := func(part string) string { return prompt("genre." + s.name + "." + part) }
	return &Profile{
		Name:            s.name,
		When:            part("when"),
		Chars:           s.chars,
		ResearchAngles:  part("research_angles"),
		ResearchSources: part("research_sources"),
		Thesis:          part("thesis"),
		OutlineOrder:    part("outline_order"),
		Voice:           part("voice"),
		Cite:            part("cite"),
		ReviewFor:       part("review_for"),
		Opening:         part("opening"),
		Frame:           s.frame,
		Scale:           s.scale,
	}
}

var profiles = sync.OnceValue(func() map[Genre]*Profile {
	all := map[Genre]*Profile{}
	for genre, s := range genreSizes {
		all[genre] = s.profile()
	}
	return all
})

// Profile is the genre's profile; a genre not set is a paper.
func (g Genre) Profile() *Profile {
	if p, ok := profiles()[g]; ok {
		return p
	}
	return profiles()[GenrePaper]
}

// Form is what the app does differently for the genre.
func (g Genre) Form() GenreForm {
	scale := g.Profile().Scale
	return GenreForm{Checkpoint: scale.Checkpoint, Headings: scale.Sections.most > 1}
}

// Length is the genre's length: chars brought within its lengths, or with
// none asked for (nil), their middle.
func (g Genre) Length(chars *uint32) uint32 {
	c := g.Profile().Chars
	if chars == nil {
		return (c.least + c.most) / 2
	}
	return g.Fit(*chars)
}

// Fit is chars brought within the genre's lengths.
func (g Genre) Fit(chars uint32) uint32 {
	c := g.Profile().Chars
	return min(max(chars, c.least), c.most)
}
