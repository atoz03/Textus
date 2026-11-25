package paper

import (
	"cmp"
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"unicode/utf8"

	"textus/backend/ai"
	"textus/backend/ai/aicompat"
	"textus/backend/ai/web"
)

// A paper's research: the topic split into questions, then a helper per
// question searching the web and the literature and keeping what it finds
// as notes with add_note, with the tools every mode shares (the files, the
// reference library) beside the web's. The helpers share one web.Plan, so
// a source has one number for the whole paper and the paper's budget is
// spent by all of them together.

// Folders reads the folders the researchers work in as the file tools do:
// the backend's, so no path climbs out of them.
type Folders interface {
	// Readable are the files under dir the file tools read, by their paths
	// from it.
	Readable(dir string, images bool) []string
	// Resolve is the file at path in folder - where it is on disk and its
	// path from folder - or false.
	Resolve(folder, path string) (abs, rel string, ok bool)
	// Text is the text of the file at path in folder, or false.
	Text(folder, path string) (string, bool)
}

// Kit is what the researchers work with besides the web and add_note: the
// tools every mode shares - the files, the reference library - and what
// they work on.
type Kit struct {
	Tools *ai.ToolRegistry
	Ctx   *ai.ToolContext
	// Root is the project folder and Folder the paper's folder, whose
	// attached/ holds the files the user attached; "" for none.
	Root, Folder string
	// SeesImages: the model sees images, so the attached pictures count.
	SeesImages bool
	// Files reads the folders; nil for none.
	Files Folders
}

// SearchReferencesTool is the reference library's search.
const SearchReferencesTool = "search_references"

// attached are the attached files, by the paths read_file reads them by.
func (k *Kit) attached() []string {
	if k.Folder == "" || k.Files == nil {
		return nil
	}
	var out []string
	for _, rel := range k.Files.Readable(filepath.Join(k.Folder, Attached), k.SeesImages) {
		out = append(out, Attached+"/"+rel)
	}
	return out
}

// prompt is what a researcher is told about the user's own material.
func (k *Kit) prompt() string {
	return materialPrompt(k.attached(), k.Tools.Offers(SearchReferencesTool))
}

// materialPrompt is what a researcher is told about the user's own
// material: the files attached, and their reference library if library.
func materialPrompt(attached []string, library bool) string {
	var out strings.Builder
	if len(attached) > 0 {
		out.WriteString(" " + fill("research.material.attached", kv{"files", strings.Join(attached, "; ")}))
	}
	if library {
		out.WriteString(" " + prompt("research.material.library"))
	}
	return out.String()
}

// planQuestions are the questions that cover paper's topic, from one
// tool-less step.
func planQuestions(ctx context.Context, model Model, paper *Paper, kit *Kit, watch aicompat.EventSink) ([]string, error) {
	profile := paper.Genre.Profile()
	message := "Topic: " + paper.Topic
	for _, path := range kit.attached() {
		// Enough of each to plan by; the helpers read them whole.
		text, _ := kit.Files.Text(kit.Folder, path)
		message += fmt.Sprintf("\n\nThe user attached %s:\n%s", path, firstRunes(text, planGlimpse))
	}
	reply, err := ask(ctx, model, planInstructions(paper), message, watch)
	if err != nil {
		return nil, err
	}
	questions := parseQuestions(reply)
	questions = questions[:min(len(questions), profile.Scale.Questions.most)]
	if len(questions) < min(2, profile.Scale.Questions.least) {
		return nil, errors.New("The research plan came back empty - try again.")
	}
	return questions, nil
}

// planInstructions is what the research plan's step is told.
func planInstructions(paper *Paper) string {
	profile := paper.Genre.Profile()
	return fill("research.plan", kv{"genre", profile.Name}, kv{"chars", paper.TargetChars}, kv{"language", paper.Language},
		kv{"least", profile.Scale.Questions.least}, kv{"most", profile.Scale.Questions.most}, kv{"angles", profile.ResearchAngles})
}

// parseQuestions are the questions in a reply: its JSON array of strings,
// or failing that its list lines ("1. …", "- …").
func parseQuestions(reply string) []string {
	var questions []string
	if !ai.JSONArray(reply, &questions) {
		questions = nil
		for _, l := range lines(reply) {
			l = strings.TrimLeft(strings.TrimSpace(l), "0123456789-*.) ")
			if utf8.RuneCountInString(l) > 8 {
				questions = append(questions, l)
			}
		}
	}
	var out []string
	for _, q := range questions {
		if q = strings.TrimSpace(q); q != "" && !slices.Contains(out, q) {
			out = append(out, q)
		}
	}
	return out[:min(len(out), maxQuestions)]
}

// Answered is a question's notes once its helper finished; nil while it is
// open or its helper failed, so going on asks it again.
type Answered []Note

// MarshalJSON keeps an open question apart from one answered with no
// notes.
func (a Answered) MarshalJSON() ([]byte, error) {
	if a == nil {
		return []byte("null"), nil
	}
	return json.Marshal([]Note(a))
}

// UnmarshalJSON reads an answered question, even one with no notes, as
// answered.
func (a *Answered) UnmarshalJSON(raw []byte) error {
	if string(raw) == "null" {
		*a = nil
		return nil
	}
	notes := []Note{}
	if err := json.Unmarshal(raw, &notes); err != nil {
		return err
	}
	*a = notes
	return nil
}

// ResearchProgress is the research so far, kept as `research.json` while
// it is under way: the planned questions, each finished helper's notes,
// and the sources those notes cite, numbered as the notes cite them.
type ResearchProgress struct {
	Questions []string             `json:"questions"`
	Answered  []Answered           `json:"answered"`
	Sources   []aicompat.WebSource `json:"sources"`
}

// clone is a copy of p whose lists are its own.
func (p ResearchProgress) clone() ResearchProgress {
	p.Questions = slices.Clone(p.Questions)
	p.Answered = slices.Clone(p.Answered)
	p.Sources = slices.Clone(p.Sources)
	return p
}

// Progress is what research goes on from, and how it keeps what it has as
// it goes.
type Progress struct {
	Kept ResearchProgress
	Keep func(ResearchProgress)
}

// NoProgress is nothing kept before and nothing kept now.
func NoProgress() Progress { return Progress{Keep: func(ResearchProgress) {}} }

// seedSources gives the kept sources their numbers in plan's book, and the
// kept notes citing them those: a book begun afresh numbers from 1 again.
func seedSources(plan *web.Plan, progress *ResearchProgress) {
	kept := progress.Sources
	progress.Sources = nil
	slices.SortStableFunc(kept, func(a, b aicompat.WebSource) int { return cmp.Compare(a.N, b.N) })
	numbers := map[uint32]uint32{}
	for _, s := range kept {
		numbers[s.N] = plan.AddSource(s.URL, s.Title)
	}
	for _, notes := range progress.Answered {
		for i := range notes {
			var renumbered []uint32
			for _, n := range notes[i].Sources {
				if m, ok := numbers[n]; ok {
					renumbered = append(renumbered, m)
				}
			}
			notes[i].Sources = renumbered
		}
	}
}

// cited are the sources in plan's book that notes cite.
func cited(plan *web.Plan, notes []Note) []aicompat.WebSource {
	wanted := map[uint32]bool{}
	for _, n := range notes {
		for _, s := range n.Sources {
			wanted[s] = true
		}
	}
	var out []aicompat.WebSource
	for _, s := range plan.Sources() {
		if wanted[s.N] {
			out = append(out, s)
		}
	}
	return out
}

// allNotes are the answered questions' notes, in question order.
func allNotes(answered []Answered) []Note {
	var out []Note
	for _, notes := range answered {
		out = append(out, notes...)
	}
	return out
}

// Research is the paper researched: its questions planned, then answered
// by helpers at once. A helper that fails costs its question, not the
// paper; the paper fails only when no helper kept a note. Each finished
// question is kept with progress as it ends, and research going on from a
// kept one asks only the questions still open.
func Research(ctx context.Context, model Model, paper *Paper, plan *web.Plan, budget *SharedBudget, send Send, kit *Kit, progress Progress) (Dossier, error) {
	kept, keep := progress.Kept, progress.Keep
	if len(kept.Questions) == 0 {
		send(PhaseEvent{Planning})
		thinking := newThinkingTail(send)
		questions, err := planQuestions(ctx, model, paper, kit, func(e aicompat.ProviderEvent) {
			if e.Kind == aicompat.EventThinking || e.Kind == aicompat.EventText {
				thinking.push(e.Text)
			}
		})
		if err != nil {
			return Dossier{}, err
		}
		kept = ResearchProgress{Questions: questions}
	}
	for len(kept.Answered) < len(kept.Questions) {
		kept.Answered = append(kept.Answered, nil)
	}
	kept.Answered = kept.Answered[:len(kept.Questions)]
	seedSources(plan, &kept)
	keep(kept.clone())
	var open []int
	for i := range kept.Questions {
		if kept.Answered[i] == nil {
			open = append(open, i)
		}
	}
	// What is left, split among the questions still open.
	share := shareOf(budget.Snapshot().Remaining(), len(open))
	send(QuestionsEvent{slices.Clone(kept.Questions)})
	send(PhaseEvent{Researching})
	for i, notes := range kept.Answered {
		if notes != nil {
			send(QuestionDoneEvent{Question: uint32(i), Notes: uint32(len(notes))})
		}
	}
	questions := slices.Clone(kept.Questions)
	var mu, writing sync.Mutex
	failed := make([]error, len(questions))
	slots := make(chan struct{}, researchersAtOnce)
	var wg sync.WaitGroup
	for _, i := range open {
		if ctx.Err() != nil {
			break
		}
		slots <- struct{}{}
		wg.Go(func() {
			defer func() { <-slots }()
			notes, err := researchQuestion(ctx, model, paper, plan, budget, kit, uint32(i), questions[i], share, send)
			if ctx.Err() != nil {
				// Stopped: nothing more is told, nor kept.
				return
			}
			send(budgetEvent(budget))
			send(QuestionDoneEvent{Question: uint32(i), Notes: uint32(len(notes)), Failed: err != nil})
			if err != nil {
				failed[i] = err
				return
			}
			mu.Lock()
			kept.Answered[i] = append(Answered{}, notes...)
			kept.Sources = cited(plan, allNotes(kept.Answered))
			mu.Unlock()
			// Kept as it ends, outside the lock; one write at a time, each
			// of the latest, so none undoes another.
			writing.Lock()
			defer writing.Unlock()
			mu.Lock()
			now := kept.clone()
			mu.Unlock()
			keep(now)
		})
	}
	wg.Wait()
	// Stopped: what finished is kept, and nothing else is said.
	if err := ctx.Err(); err != nil {
		return Dossier{}, err
	}
	for i, err := range failed {
		if err != nil {
			send(ErrorEvent{Message: fmt.Sprintf("%s: %v", questions[i], err)})
		}
	}
	dossier := Dossier{Notes: allNotes(kept.Answered)}
	if len(dossier.Notes) == 0 {
		// Nothing to go on from: going on plans the research again.
		keep(ResearchProgress{})
		return Dossier{}, errors.New("The research found nothing to write from - check the search service in Settings, or try a broader topic.")
	}
	for i := range dossier.Notes {
		dossier.Notes[i].ID = fmt.Sprintf("n%d", i+1)
	}
	// Only what the notes cite: everything a search showed would bury them.
	for _, s := range cited(plan, dossier.Notes) {
		s.URL = linkSafe(s.URL)
		dossier.Sources = append(dossier.Sources, s)
	}
	return dossier, nil
}

// share is one question's share of the paper's searches, page reads and
// paper lookups: an even split, so the helpers that run first can't leave
// the last questions nothing to research with.
type share [3]int

func shareOf(limits BudgetCounts, questions int) share {
	each := func(limit uint32) int {
		q := max(questions, 1)
		return max((int(limit)+q-1)/q, 1)
	}
	return share{each(limits.Searches), each(limits.Fetches), each(limits.PaperLookups)}
}

// usedUp reports whether spent has used every kind of the share: nothing
// is left to search, read or look up with.
func (s share) usedUp(spent [3]int) bool {
	for i := range s {
		if spent[i] < s[i] {
			return false
		}
	}
	return true
}

// spend counts a call against the share, in spent; why it is refused once
// its kind's share is used. Other tools are not counted.
func (s share) spend(tool string, spent *[3]int) string {
	var kind int
	var what string
	switch tool {
	case web.WebSearchTool:
		kind, what = 0, "web searches"
	case web.WebFetchTool:
		kind, what = 1, "page reads"
	case web.SearchPapersTool:
		kind, what = 2, "paper searches"
	default:
		return ""
	}
	if spent[kind] >= s[kind] {
		return fmt.Sprintf("That's this question's share of %s (%d) - the other questions need the rest. Keep notes from what you have read, or use another kind of search.", what, s[kind])
	}
	spent[kind]++
	return ""
}

// isSearchPage reports whether address is a search engine's results or a
// search API's answer: a list of other pages, which a note cites instead
// of what they list.
func isSearchPage(address string) bool {
	u, err := url.Parse(address)
	if err != nil || u.Scheme == "" {
		return false
	}
	host := strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
	path := u.EscapedPath()
	engine := slices.Contains([]string{"duckduckgo.com", "html.duckduckgo.com", "lite.duckduckgo.com", "search.yahoo.com", "yandex.com", "search.brave.com"}, host) ||
		(strings.HasPrefix(host, "google.") || host == "bing.com" || host == "cn.bing.com") && strings.HasPrefix(path, "/search") ||
		host == "baidu.com" && path == "/s" ||
		host == "scholar.google.com"
	api := slices.Contains([]string{"api.crossref.org", "api.openalex.org", "api.semanticscholar.org", "export.arxiv.org"}, host) ||
		(strings.HasSuffix(host, "wikipedia.org") || strings.HasSuffix(host, "wikimedia.org")) && strings.HasSuffix(path, "/api.php")
	return engine || api
}
