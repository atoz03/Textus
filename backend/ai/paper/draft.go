package paper

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"

	"textus/backend/ai/aicompat"
)

// A paper drafted from its approved outline: a writer per section, as many
// at a time as the model takes, each with the thesis, what the other
// sections cover, and its own notes and their sources. A writer hands its
// section over with submit_section, which checks its length, its citations
// and its figures and sends back what is wrong. Each section is kept as
// soon as it is written; sections are handed on in the outline's order.

const submitSectionTool = "submit_section"

// revision is a section being revised: its earlier draft and the
// reviewer's points against it.
type revision struct {
	earlier string
	points  []string
}

// sectionSources are the sources section's notes cite, by number and
// address.
func sectionSources(section *OutlineSection, dossier *Dossier) []numbered {
	var out []numbered
	for _, n := range sectionNotes(section, dossier) {
		for _, number := range n.Sources {
			if s, ok := dossier.source(number); ok {
				out = append(out, numbered{s.N, s.URL})
			}
		}
	}
	return out
}

// writeSection is section index written: its Markdown under its heading.
// A revision is given the earlier draft and the points against it; draw
// draws the section's figures as the document would, nil to check them by
// their JSON and syntax alone.
func writeSection(ctx context.Context, model Model, paper *Paper, outline *Outline, index int, dossier *Dossier, rev *revision, send Send, draw Draw) (string, error) {
	watch := newSectionWatch(uint32(index), send)
	section := &outline.Sections[index]
	sources := sectionSources(section, dossier)
	var notes []Note
	for _, n := range sectionNotes(section, dossier) {
		notes = append(notes, *n)
	}
	given := figuresBySource(notes)
	var mu sync.Mutex
	// soft is the latest section handed over whose problems were all soft:
	// kept at the end if no try holds. last is the latest handed over at
	// all: kept, repaired, if no try holds or is only soft - a paper with
	// one weaker section beats one that stops there.
	var soft, last string
	check := func(arguments string) checked[string] {
		args, _ := parseValue(arguments)
		markdown := strings.TrimSpace(str(args, "markdown"))
		wrong := problems(markdown, section, sources, paper.Language)
		wrong = append(wrong, longPosts(markdown, paper)...)
		// Drawn even when other things are wrong, so the writer fixes all
		// at once.
		wrong = append(wrong, undrawn(ctx, draw, markdown)...)
		wrong = append(wrong, unsupportedFigures(markdown, given)...)
		mu.Lock()
		defer mu.Unlock()
		if markdown != "" {
			last = markdown
		}
		if len(wrong) == 0 {
			return keptAs(markdown)
		}
		if markdown != "" && allSoft(wrong) {
			soft = markdown
		}
		return notKept[string]("section", problemTexts(wrong))
	}
	message := writerMessage(paper, outline, index, dossier)
	if rev != nil {
		message = revisionMessage(paper, outline, index, dossier, rev.earlier, rev.points)
	}
	t := task{instructions: writerInstructions(paper), message: message, tool: submitSectionSpec(), steps: writerSteps, watch: watch.push}
	kept, turns, err := handOver(ctx, model, t, check)
	if err != nil {
		return "", err
	}
	mu.Lock()
	defer mu.Unlock()
	var body string
	switch {
	case kept != nil:
		body = *kept
	case soft != "":
		body = soft
	default:
		if replied, ok := repliedSection(turns, section, sources, paper.Language); ok {
			body = replied
			break
		}
		// A revision that never holds keeps the draft it would replace.
		text := last
		if text == "" {
			text = replyBody(turns)
		}
		if rev != nil || text == "" {
			return "", fmt.Errorf("\"%s\" wasn't written in a form that holds.", section.Title)
		}
		body = repaired(text, sources)
	}
	body = uprightTimelines(body)
	// A genre of one section is all under its title: no heading of its own.
	if !paper.Genre.Form().Headings {
		return body, nil
	}
	return fmt.Sprintf("## %s\n\n%s", section.Title, body), nil
}

// postAddress is a citation's address in a post, which isn't read: its
// link shows as [n].
var postAddress = regexp.MustCompile(`\]\([^)` + space + `]*\)`)

// longPosts are the posts of a thread too long for one post: its genre's
// most characters, half that in Chinese, Japanese and Korean, whose
// characters count double. Soft, as a length is.
func longPosts(markdown string, paper *Paper) []Problem {
	most := paper.Genre.Profile().Scale.PostChars
	if most == 0 {
		return nil
	}
	if CountsCharacters(paper.Language) {
		most /= 2
	}
	var out []Problem
	for i, post := range strings.Split(markdown, "\n---") {
		n := uint32(runeCount(postAddress.ReplaceAllLiteralString(strings.TrimSpace(post), "]")))
		if n > most {
			out = append(out, softly(fmt.Sprintf("It is too long in post %d: %d characters, against at most %d - split it or cut it.", i+1, n, most)))
		}
	}
	return out
}

// repliedSection is a writer's section answered as its reply instead of
// handed over (some models do): the reply, under the same checks - kept
// if nothing is wrong with it but its length or a missing figure. Its own
// heading, if it wrote one, is left off.
func repliedSection(turns []aicompat.AgentTurn, section *OutlineSection, sources []numbered, language string) (string, bool) {
	body := replyBody(turns)
	if body == "" {
		return "", false
	}
	return body, allSoft(problems(body, section, sources, language))
}

// replyBody is the writer's last reply, without the heading it may have
// given it; "" when it wrote nothing.
func replyBody(turns []aicompat.AgentTurn) string {
	reply := ""
	for i := len(turns) - 1; i >= 0; i-- {
		if turns[i].Kind == aicompat.Assistant {
			reply = strings.TrimSpace(turns[i].Text)
			break
		}
	}
	// The loop's own words when the steps ran out are no section.
	if reply == aicompat.OutOfSteps {
		return ""
	}
	if first, rest, ok := strings.Cut(reply, "\n"); ok && strings.HasPrefix(strings.TrimLeftFunc(first, unicode.IsSpace), "#") {
		return strings.TrimSpace(rest)
	}
	return reply
}

// repaired is a section whose writer never got it to hold, made safe to
// keep: each citation linked to its source's address, or left out when it
// names no source of the section, and each chart that can't be drawn taken
// out. Its length and its figures stay as written.
func repaired(markdown string, sources []numbered) string {
	address := func(n string) (string, bool) {
		number, err := strconv.ParseUint(n, 10, 32)
		if err != nil {
			return "", false
		}
		for _, s := range sources {
			if uint64(s.n) == number {
				return s.url, true
			}
		}
		return "", false
	}
	linked := citedLink.ReplaceAllStringFunc(markdown, func(c string) string {
		n := citedLink.FindStringSubmatch(c)[1]
		if url, ok := address(n); ok {
			return fmt.Sprintf("[%s](%s)", n, url)
		}
		return ""
	})
	var out, chart []string
	inChart, inFence := false, false
	for _, line := range strings.Split(linked, "\n") {
		trimmed := strings.TrimLeftFunc(line, unicode.IsSpace)
		fence := strings.HasPrefix(trimmed, "```")
		if inChart {
			chart = append(chart, line)
			if fence {
				if chartHolds(strings.Join(chart[1:len(chart)-1], "\n")) == "" {
					out = append(out, chart...)
				}
				chart, inChart = nil, false
			}
			continue
		}
		if fence && !inFence && strings.TrimSpace(strings.TrimLeft(trimmed, "`")) == "chart" {
			chart, inChart = []string{line}, true
			continue
		}
		if fence {
			inFence = !inFence
		}
		if inFence || fence {
			out = append(out, line)
			continue
		}
		out = append(out, bareNumber.ReplaceAllStringFunc(line, func(c string) string {
			m := bareNumber.FindStringSubmatch(c)
			if url, ok := address(m[1]); ok {
				return fmt.Sprintf("[%s](%s)%s", m[1], url, m[2])
			}
			return m[2]
		}))
	}
	return strings.TrimSpace(closeGaps(strings.Join(out, "\n")))
}

// Draft is every section of outline written, as many at a time as the
// model takes. Each goes to finished as soon as it is written - kept, so a
// stop costs no section done - and the whole comes back in the outline's
// order. One written has already (by a run that stopped) is finished as it
// is. started hears of each as its writer begins. A section that fails
// stops any more from starting; those under way are finished, then it
// fails.
func Draft(ctx context.Context, model Model, paper *Paper, outline *Outline, dossier *Dossier, written map[int]string, started func(int), finished func(int, string) error, send Send, draw Draw) ([]string, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	type result struct {
		i       int
		text    string
		err     error
		skipped bool
	}
	n := len(outline.Sections)
	results := make(chan result)
	var failed atomic.Bool
	go func() {
		slots := make(chan struct{}, max(model.AtOnce(), 1))
		var wg sync.WaitGroup
		for i := range n {
			slots <- struct{}{}
			wg.Go(func() {
				defer func() { <-slots }()
				if text, ok := written[i]; ok {
					results <- result{i: i, text: text}
					return
				}
				if failed.Load() || ctx.Err() != nil {
					results <- result{i: i, skipped: true}
					return
				}
				started(i)
				text, err := writeSection(ctx, model, paper, outline, i, dossier, nil, send, draw)
				if err != nil {
					failed.Store(true)
				}
				results <- result{i: i, text: text, err: err}
			})
		}
		wg.Wait()
		close(results)
	}()
	done := make([]string, n)
	first, firstErr := n, error(nil)
	for r := range results {
		switch {
		case r.skipped:
		case r.err == nil:
			if err := finished(r.i, r.text); err != nil {
				cancel()
				for range results {
				}
				return nil, err
			}
			done[r.i] = r.text
		case r.i < first:
			first, firstErr = r.i, r.err
		}
	}
	if firstErr != nil {
		return nil, firstErr
	}
	return done, nil
}

// sectionEvery is the least time between two SectionTextEvents for one
// section.
const sectionEvery = 300 * time.Millisecond

// sectionWatch watches a writer's stream and sends what it has written so
// far, whole, as SectionTextEvent - the document shows it as it comes: the
// markdown of the submit_section call it is making, or its reply when it
// writes the section there instead.
type sectionWatch struct {
	index uint32
	send  Send
	mu    sync.Mutex
	args  string
	reply string
	sent  int
	last  lastSent
}

func newSectionWatch(index uint32, send Send) *sectionWatch {
	return &sectionWatch{index: index, send: send}
}

func (w *sectionWatch) push(e aicompat.ProviderEvent) {
	w.mu.Lock()
	switch e.Kind {
	case aicompat.EventText:
		w.reply += e.Text
	case aicompat.EventToolStart:
		w.args, w.reply, w.sent = "", "", 0
	case aicompat.EventToolArgs:
		w.args += e.Text
	default:
		w.mu.Unlock()
		return
	}
	if !w.last.due(sectionEvery) {
		w.mu.Unlock()
		return
	}
	// Decoded only when due: a few times a second, not per token.
	text := strings.TrimSpace(w.reply)
	if w.args != "" {
		text = stringValue(w.args, `"markdown"`)
	}
	if len(text) == w.sent || strings.TrimSpace(text) == "" {
		w.mu.Unlock()
		return
	}
	w.sent = len(text)
	w.last.now()
	w.mu.Unlock()
	w.send(SectionTextEvent{Index: w.index, Text: text})
}
