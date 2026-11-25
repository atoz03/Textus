package paper

// The numbers the paper's stages are tuned by, in one place. A genre's
// own sizes - its questions, sections and research budget - are in its
// Scale (genre.go); these hold for every genre.

// Research.
const (
	// researchersAtOnce are the helpers researching at once.
	researchersAtOnce = 4
	// helperSteps are a helper's steps: a few searches and reads, and its
	// notes.
	helperSteps = 10
	// maxHanded are the parts of its question one researcher may hand to
	// helpers of its own, when the user lets it.
	maxHanded = 2
	// subSteps are a part's helper's steps: fewer than a question's, for a
	// narrower one.
	subSteps = 6
	// maxQuestions are the questions a research plan may give at most,
	// whatever the genre.
	maxQuestions = 10
	// planGlimpse is the characters of each attached file the research
	// plan sees; the helpers read them whole.
	planGlimpse = 1_500
)

// Outline.
const (
	// outlineSteps are the tries at an outline that holds.
	outlineSteps = 5
	// sectionsSlack is the fewest sections an outline may have below its
	// most: a few fewer, longer sections are fine, a list of short ones
	// isn't.
	sectionsSlack = 2
	// figuresBeyondSections are the figures an outline may plan beyond one
	// a section.
	figuresBeyondSections = 1
	// sectionsPerWebPicture: web pictures, at most one for every this many
	// sections - each needs a match on Commons, and too many crowd out the
	// figures of the paper's own data.
	sectionsPerWebPicture = 3
)

// outlineLength is how far the outline's section lengths may add up from
// the paper's, as fractions of it.
var outlineLength = [2]float64{0.5, 1.6}

// Drafting.
const (
	// writerSteps are a writer's tries at a section that holds.
	writerSteps = 5
)

// How far a section's length may be from its target, as fractions of it:
// within pass it holds; within soft it is kept if nothing else is wrong (a
// writer's last try); beyond, it is sent back.
var (
	pass = [2]float64{0.7, 1.2}
	soft = [2]float64{0.5, 1.4}
)

// Review.
const (
	// reviewSteps are the tries at a review report that reads.
	reviewSteps = 3
	// maxPointsPerSection are the points kept against one section: the
	// most important, as the reviewer ordered them.
	maxPointsPerSection = 3
)

// Figures.
const (
	// commonsTries are the Commons search results tried for one web picture
	// before it is left out.
	commonsTries = 3
)
