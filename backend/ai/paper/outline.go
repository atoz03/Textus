package paper

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"textus/backend/ai/aicompat"
)

// A paper's outline, made from its dossier: a thesis, and per section its
// key points, the notes it draws on, its length and its figures. The model
// hands it over with submit_outline, which checks it and sends back what is
// wrong until it holds; the user then edits and approves it.

const submitOutlineTool = "submit_outline"

// shape is the outline a paper of a given length is to have: how many
// sections, and how many figures in all and from the web.
type shape struct {
	minSections, maxSections int
	// figureEvery: at least one figure for every this many sections; 0 for
	// none.
	figureEvery int
	maxFigures  int
	// maxWeb are the web pictures: each needs a match on Commons, and too
	// many crowd out the figures that carry the paper's own data.
	maxWeb int
	// images are the generated pictures the budget still allows.
	images uint32
}

// shapeOf is the shape of a piece of scale and targetChars characters:
// about one section per its CharsPerSection - fewer, longer sections read
// as an argument; many short ones as a list.
func shapeOf(scale Scale, targetChars, images uint32) shape {
	per := uint64(scale.CharsPerSection)
	n := int((min(uint64(targetChars)+per/2, 1<<32-1)) / per)
	maxSections := min(max(n, scale.Sections.least), scale.Sections.most)
	s := shape{
		minSections: max(maxSections-sectionsSlack, scale.Sections.least),
		maxSections: maxSections,
		figureEvery: scale.FigureEvery,
	}
	if scale.FigureEvery > 0 {
		s.maxFigures = maxSections + figuresBeyondSections
		s.maxWeb = (maxSections + sectionsPerWebPicture - 1) / sectionsPerWebPicture
		s.images = images
	}
	return s
}

func (s shape) minFigures(sections int) int {
	if s.figureEvery == 0 {
		return 0
	}
	return (sections + s.figureEvery - 1) / s.figureEvery
}

// targetUnits is the paper's length in the unit its sections' TargetWords
// use: its characters for Chinese, Japanese and Korean, else about a sixth
// of them in words.
func targetUnits(paper *Paper) uint32 {
	if CountsCharacters(paper.Language) {
		return paper.TargetChars
	}
	return paper.TargetChars / 6
}

// debugList is items as Rust's {:?} writes a list of strings.
func debugList(items []string) string {
	quoted := make([]string, len(items))
	for i, item := range items {
		quoted[i] = debugQuoted(item)
	}
	return "[" + strings.Join(quoted, ", ") + "]"
}

// outlineProblems is what is wrong with outline for this paper, every
// problem at once, or nothing, for a paper of target units shaped as s
// says.
func outlineProblems(outline *Outline, dossier *Dossier, target uint32, s shape) []string {
	var out []string
	if strings.TrimSpace(outline.Thesis) == "" {
		out = append(out, "`thesis` is empty: state what the paper argues or explains in one or two sentences.")
	}
	n := len(outline.Sections)
	if n < s.minSections || n > s.maxSections {
		out = append(out, fmt.Sprintf("%d sections: a paper this long has %d-%d, each long enough to develop its point.", n, s.minSections, s.maxSections))
	}
	known := func(id string) bool {
		for _, note := range dossier.Notes {
			if note.ID == id {
				return true
			}
		}
		return false
	}
	var figures []FigurePlan
	for i, section := range outline.Sections {
		name := fmt.Sprintf("Section %d (\"%s\")", i+1, strings.TrimSpace(section.Title))
		if strings.TrimSpace(section.Title) == "" {
			out = append(out, fmt.Sprintf("Section %d has no title.", i+1))
		}
		if all(section.KeyPoints, func(p string) bool { return strings.TrimSpace(p) == "" }) {
			out = append(out, name+" has no key points.")
		}
		if section.TargetWords == 0 {
			out = append(out, name+" has no length (`targetWords`).")
		}
		var unknown []string
		for _, id := range section.Evidence {
			if !known(id) {
				unknown = append(unknown, id)
			}
		}
		if len(unknown) > 0 {
			out = append(out, fmt.Sprintf("%s draws on notes that don't exist: %s. Use the ids in the dossier.", name, debugList(unknown)))
		}
		for _, figure := range section.Figures {
			if strings.TrimSpace(figure.Spec) == "" {
				out = append(out, name+" has a figure with no `spec`.")
			}
		}
		figures = append(figures, section.Figures...)
	}
	if len(figures) < s.minFigures(n) {
		out = append(out, fmt.Sprintf("%d figures for %d sections: plan at least %d - diagrams, charts of the dossier's data, pictures.", len(figures), n, s.minFigures(n)))
	}
	if len(figures) > s.maxFigures {
		out = append(out, fmt.Sprintf("%d figures: at most %d for a piece this long - keep the ones that carry the most.", len(figures), s.maxFigures))
	}
	web, generated := 0, uint32(0)
	for _, f := range figures {
		switch f.Kind {
		case WebPicture:
			web++
		case Generated:
			generated++
		}
	}
	if web > s.maxWeb {
		out = append(out, fmt.Sprintf("%d web pictures: at most %d - make the rest diagrams or charts of the notes' data.", web, s.maxWeb))
	}
	if generated > s.images {
		out = append(out, fmt.Sprintf("%d generated pictures, but only %d can be made: use Mermaid diagrams, charts or web pictures for the rest.", generated, s.images))
	}
	total := uint32(0)
	for _, section := range outline.Sections {
		total += section.TargetWords
	}
	ratio := float64(total) / float64(max(target, 1))
	if n > 0 && (ratio < outlineLength[0] || ratio > outlineLength[1]) {
		out = append(out, fmt.Sprintf("The sections' lengths add up to %d; the paper is about %d.", total, target))
	}
	return out
}

// dossierText is the dossier as the outline's model reads it: one note a
// line.
func dossierText(dossier *Dossier) string {
	out := make([]string, len(dossier.Notes))
	for i, note := range dossier.Notes {
		var sources strings.Builder
		for _, n := range note.Sources {
			fmt.Fprintf(&sources, "[%d]", n)
		}
		var leads []string
		for _, l := range note.Leads {
			kind := "Image"
			if l.Kind == DataLead {
				kind = "Data"
			}
			leads = append(leads, strings.ToLower(kind+" lead: "+l.Description))
		}
		line := fmt.Sprintf("%s - %s %s Evidence: %s", note.ID, note.Claim, sources.String(), note.Evidence)
		if len(leads) > 0 {
			line += " (" + strings.Join(leads, "; ") + ")"
		}
		out[i] = line
	}
	return strings.Join(out, "\n")
}

func submitOutlineSpec() aicompat.ToolSpec {
	strs := map[string]any{"type": "array", "items": map[string]any{"type": "string"}}
	return aicompat.ToolSpec{
		Name:        submitOutlineTool,
		Description: "Hand over the paper's outline. It is checked: if anything is wrong you are told what, and call this again with the whole outline corrected.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"thesis": map[string]any{"type": "string", "description": "What the paper argues or explains, in one or two sentences"},
				"sections": map[string]any{
					"type": "array",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"title":       map[string]any{"type": "string"},
							"keyPoints":   strs,
							"evidence":    map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "The ids of the dossier notes it draws on, like \"n3\""},
							"targetWords": map[string]any{"type": "integer", "description": "Its length"},
							"figures": map[string]any{
								"type": "array",
								"items": map[string]any{
									"type": "object",
									"properties": map[string]any{
										"kind": map[string]any{"type": "string", "enum": []any{"generated", "mermaid", "chart", "web"}},
										"spec": map[string]any{"type": "string"},
									},
									"required": []any{"kind", "spec"},
								},
							},
						},
						"required": []any{"title", "keyPoints", "evidence", "targetWords", "figures"},
					},
				},
			},
			"required": []any{"thesis", "sections"},
		},
	}
}

// outlineInstructions is what the outline's step is told, for an outline
// shaped as s.
func outlineInstructions(paper *Paper, s shape) string {
	profile := paper.Genre.Profile()
	unit := "words"
	if CountsCharacters(paper.Language) {
		unit = "characters"
	}
	figures := prompt("outline.no-figures")
	if s.figureEvery > 0 {
		every := fmt.Sprint(s.figureEvery)
		switch s.figureEvery {
		case 2:
			every = "two"
		case 3:
			every = "three"
		}
		figures = fill("outline.figures", kv{"every", every}, kv{"max_figures", s.maxFigures}, kv{"max_web", s.maxWeb}, kv{"images", s.images})
	}
	return fill("outline.instructions", kv{"genre", profile.Name}, kv{"topic", paper.Topic}, kv{"language", paper.Language},
		kv{"target", targetUnits(paper)}, kv{"unit", unit}, kv{"thesis", profile.Thesis}, kv{"least", s.minSections},
		kv{"most", s.maxSections}, kv{"order", profile.OutlineOrder}, kv{"figures", figures})
}

// readOutline is submit_outline's arguments as an outline, every field
// there as the tool asks; or why it can't be read.
func readOutline(arguments string) (Outline, error) {
	var outline Outline
	if err := json.Unmarshal([]byte(arguments), &outline); err != nil {
		return Outline{}, err
	}
	var raw struct {
		Thesis   *string `json:"thesis"`
		Sections *[]struct {
			Title       *string `json:"title"`
			KeyPoints   *any    `json:"keyPoints"`
			Evidence    *any    `json:"evidence"`
			TargetWords *any    `json:"targetWords"`
			Figures     *[]struct {
				Kind *any `json:"kind"`
				Spec *any `json:"spec"`
			} `json:"figures"`
		} `json:"sections"`
	}
	_ = json.Unmarshal([]byte(arguments), &raw)
	missing := func(field string) (Outline, error) { return Outline{}, fmt.Errorf("missing field `%s`", field) }
	if raw.Thesis == nil {
		return missing("thesis")
	}
	if raw.Sections == nil {
		return missing("sections")
	}
	for _, s := range *raw.Sections {
		switch {
		case s.Title == nil:
			return missing("title")
		case s.KeyPoints == nil:
			return missing("keyPoints")
		case s.Evidence == nil:
			return missing("evidence")
		case s.TargetWords == nil:
			return missing("targetWords")
		case s.Figures == nil:
			return missing("figures")
		}
		for _, f := range *s.Figures {
			if f.Kind == nil {
				return missing("kind")
			}
			if f.Spec == nil {
				return missing("spec")
			}
		}
	}
	return outline, nil
}

// MakeOutline is the outline for paper from its dossier.
func MakeOutline(ctx context.Context, model Model, paper *Paper, dossier *Dossier, send Send) (Outline, error) {
	s := shapeOf(paper.Genre.Profile().Scale, paper.TargetChars, paper.Budget.Remaining().Images)
	watch := newOutlineWatch(send)
	t := task{
		instructions: outlineInstructions(paper, s),
		message:      fmt.Sprintf("Topic: %s\n\nThe research notes:\n%s", paper.Topic, dossierText(dossier)),
		tool:         submitOutlineSpec(),
		steps:        outlineSteps,
		watch:        watch.push,
	}
	kept, _, err := handOver(ctx, model, t, func(arguments string) checked[Outline] {
		outline, err := readOutline(arguments)
		if err != nil {
			return refused[Outline](fmt.Sprintf("The outline couldn't be read: %v. Send it again.", err))
		}
		if wrong := outlineProblems(&outline, dossier, targetUnits(paper), s); len(wrong) > 0 {
			return notKept[Outline]("outline", wrong)
		}
		return keptAs(outline)
	})
	if err != nil {
		return Outline{}, err
	}
	if kept == nil {
		return Outline{}, errors.New("No outline that holds came back - try again.")
	}
	return *kept, nil
}

// AsEdited is the user's edit of an outline, made safe to draft from:
// blank key points and notes the dossier doesn't have dropped. Refused
// without a section, or with a section without a title.
func AsEdited(outline Outline, dossier Dossier) (Outline, error) {
	if len(outline.Sections) == 0 {
		return Outline{}, errors.New("The outline needs at least one section.")
	}
	// The caller's outline stays as it was.
	outline.Sections = slices.Clone(outline.Sections)
	for i := range outline.Sections {
		section := &outline.Sections[i]
		section.Title = strings.TrimSpace(section.Title)
		if section.Title == "" {
			return Outline{}, errors.New("Every section needs a title.")
		}
		section.KeyPoints = keep(section.KeyPoints, func(p string) bool { return strings.TrimSpace(p) != "" })
		section.Evidence = keep(section.Evidence, func(id string) bool {
			for _, n := range dossier.Notes {
				if n.ID == id {
					return true
				}
			}
			return false
		})
		section.Figures = keep(section.Figures, func(f FigurePlan) bool { return strings.TrimSpace(f.Spec) != "" })
	}
	return outline, nil
}

// keep is items with only those ok keeps, in order.
func keep[T any](items []T, ok func(T) bool) []T {
	out := items[:0:0]
	for _, item := range items {
		if ok(item) {
			out = append(out, item)
		}
	}
	return out
}

// draftEvery is the least time between two OutlineDraftEvents.
const draftEvery = 250 * time.Millisecond

// outlineWatch watches the outliner's stream: its thinking sent as
// ThinkingEvents, and the outline it is writing as OutlineDraftEvents -
// read from submit_outline's arguments as they stream, a few times a
// second at most.
type outlineWatch struct {
	send     Send
	thinking *thinkingTailOf
	mu       sync.Mutex
	state    draftState
}

type draftState struct {
	attempt uint32
	args    string
	// scanned is where scanning args picks up: everything before it is
	// read.
	scanned int
	thesis  *string
	titles  []string
	// sentThesis and sentTitles are what was last sent.
	sentThesis *string
	sentTitles int
	last       lastSent
}

func newOutlineWatch(send Send) *outlineWatch {
	return &outlineWatch{send: send, thinking: newThinkingTail(send)}
}

func (w *outlineWatch) push(e aicompat.ProviderEvent) {
	switch e.Kind {
	case aicompat.EventThinking, aicompat.EventText:
		w.thinking.push(e.Text)
		return
	case aicompat.EventToolStart, aicompat.EventToolArgs:
	default:
		return
	}
	w.mu.Lock()
	s := &w.state
	if e.Kind == aicompat.EventToolStart {
		*s = draftState{attempt: s.attempt + 1}
	} else {
		s.args += e.Text
	}
	s.scan()
	changed := !sameText(s.sentThesis, s.thesis) || s.sentTitles != len(s.titles)
	if !changed || !s.last.due(draftEvery) {
		w.mu.Unlock()
		return
	}
	s.last.now()
	s.sentThesis, s.sentTitles = s.thesis, len(s.titles)
	event := OutlineDraftEvent{Attempt: s.attempt, Thesis: s.thesis, Titles: append([]string{}, s.titles...)}
	w.mu.Unlock()
	w.send(event)
}

func sameText(a, b *string) bool { return a == nil && b == nil || a != nil && b != nil && *a == *b }

// scan reads the whole "thesis" and "title" strings that arrived since the
// last scan. Each fragment is scanned once, from the last key on, so a
// long outline costs no more than its length.
func (s *draftState) scan() {
	for {
		rest := s.args[s.scanned:]
		key, at := "", -1
		for _, k := range []string{`"thesis"`, `"title"`} {
			if i := strings.Index(rest, k); i >= 0 && (at < 0 || i < at) {
				key, at = k, i
			}
		}
		if at < 0 {
			// Keep the tail that may be a key cut in half.
			s.scanned = max(len(s.args)-8, 0)
			for s.scanned > 0 && !utf8.RuneStart(s.args[s.scanned]) {
				s.scanned--
			}
			return
		}
		start := s.scanned + at
		found := readAfterKey(s.args[start+len(key):])
		var end int
		switch {
		// The value hasn't all arrived: wait for more from the key.
		case found.pending || !found.other && found.end < 0:
			s.scanned = start
			return
		case found.other:
			end = found.at
		default:
			if key == `"thesis"` {
				s.thesis = nil
				if strings.TrimSpace(found.text) != "" {
					thesis := found.text
					s.thesis = &thesis
				}
			} else {
				s.titles = append(s.titles, found.text)
			}
			end = found.end
		}
		s.scanned = start + len(key) + end
	}
}
