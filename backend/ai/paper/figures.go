package paper

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"
)

// Whether a section's diagrams and charts are drawn: the checks read a
// chart's JSON and see that a diagram is there, but only the renderer
// knows whether Mermaid takes a diagram's syntax. So the window the paper
// is written for draws every ```mermaid and ```chart block as its document
// would (src/ai/paper/figure-check.ts), and a block it can't draw is sent
// back to its writer with the renderer's own words.

// drawnWithin is how long the window has to draw a section's figures. Past
// it the section is checked as if no window could draw: by its JSON and
// syntax alone. Tests shorten it.
var drawnWithin = 30 * time.Second

// errorChars are the most of a renderer's error a writer is shown.
const errorChars = 400

// Draw draws blocks: for each, why it can't be drawn, or nil when it can.
// False when nothing could draw them.
type Draw func(ctx context.Context, blocks []FigureBlock) ([]*string, bool)

// figureCalls are the draws waiting for the window's answer, by call id:
// three writers draw at once.
var figureCalls struct {
	sync.Mutex
	next    uint64
	waiting map[string]chan []*string
}

// FiguresDrawn is what the window made of the blocks DrawFiguresEvent
// callID sent: for each, in order, why it couldn't be drawn, or nil.
func FiguresDrawn(callID string, errors []*string) {
	figureCalls.Lock()
	answer, ok := figureCalls.waiting[callID]
	delete(figureCalls.waiting, callID)
	figureCalls.Unlock()
	if ok {
		answer <- errors
	}
}

func forgetCall(callID string) {
	figureCalls.Lock()
	delete(figureCalls.waiting, callID)
	figureCalls.Unlock()
}

// ByWindow is a Draw that asks the window behind send - the paper's
// channel - to draw; send reports whether the window could be told.
func ByWindow(send func(Event) bool) Draw {
	// A window that let one draw time out isn't waited for again.
	var gone atomic.Bool
	return func(ctx context.Context, blocks []FigureBlock) ([]*string, bool) {
		if gone.Load() {
			return nil, false
		}
		answer := make(chan []*string, 1)
		figureCalls.Lock()
		callID := strconv.FormatUint(figureCalls.next, 10)
		figureCalls.next++
		if figureCalls.waiting == nil {
			figureCalls.waiting = map[string]chan []*string{}
		}
		figureCalls.waiting[callID] = answer
		figureCalls.Unlock()
		count := len(blocks)
		if !send(DrawFiguresEvent{CallID: callID, Blocks: blocks}) {
			forgetCall(callID)
			return nil, false
		}
		timer := time.NewTimer(drawnWithin)
		defer timer.Stop()
		select {
		case errors := <-answer:
			return errors, len(errors) == count
		case <-timer.C:
			gone.Store(true)
		case <-ctx.Done():
		}
		forgetCall(callID)
		return nil, false
	}
}

// figureBlocks are markdown's diagram and chart blocks, as the window
// draws them: a chart whose JSON the checks already refuse is left out,
// since its writer hears of it there.
func figureBlocks(markdown string) []FigureBlock {
	var out []FigureBlock
	for _, f := range fences(markdown) {
		language, body := f[0], f[1]
		switch {
		case language == "mermaid" && strings.TrimSpace(body) != "",
			language == "chart" && chartHolds(body) == "":
			out = append(out, FigureBlock{Language: language, Code: strings.TrimRightFunc(body, unicode.IsSpace)})
		}
	}
	return out
}

// undrawn is what is wrong with markdown's figures as draw draws them: one
// problem for each block it couldn't draw. None without a draw, or when it
// couldn't draw at all.
func undrawn(ctx context.Context, draw Draw, markdown string) []Problem {
	blocks := figureBlocks(markdown)
	if draw == nil || len(blocks) == 0 {
		return nil
	}
	errors, ok := draw(ctx, blocks)
	if !ok {
		return nil
	}
	diagrams, charts := 0, 0
	var out []Problem
	for i, block := range blocks {
		n, what := 0, "Diagram"
		if block.Language == "chart" {
			charts++
			n, what = charts, "Chart"
		} else {
			diagrams++
			n = diagrams
		}
		if i < len(errors) && errors[i] != nil {
			out = append(out, hard(figureProblem(what, n, *errors[i])))
		}
	}
	return out
}

// drawable is section with every diagram and chart that can't be drawn
// taken out: the last pass before the figures are numbered, for what no
// writer's check saw - a section kept from a run before this one, kept
// repaired, or kept on its last try. An empty diagram and a chart the
// checks refuse go undrawn; the rest are drawn by draw, and kept as they
// are when it can't draw at all. So no figure reaches the paper as its
// code.
func drawable(ctx context.Context, draw Draw, section string) string {
	lines := strings.Split(section, "\n")
	type found struct {
		open, close int
		// known: whether it is kept without drawing; nil to draw.
		known *bool
		block FigureBlock
	}
	no := false
	var all []found
	var toDraw []FigureBlock
	for _, f := range fenceSpans(section) {
		var known *bool
		switch {
		case f.language == "mermaid" && strings.TrimSpace(f.body) == "",
			f.language == "chart" && chartHolds(f.body) != "":
			known = &no
		case f.language == "mermaid" || f.language == "chart":
		default:
			continue
		}
		block := FigureBlock{Language: f.language, Code: strings.TrimRightFunc(f.body, unicode.IsSpace)}
		all = append(all, found{f.open, f.close, known, block})
		if known == nil {
			toDraw = append(toDraw, block)
		}
	}
	var drawn []*string
	ok := false
	if draw != nil && len(toDraw) > 0 {
		drawn, ok = draw(ctx, toDraw)
	}
	var out []string
	next, asked := 0, 0
	for _, f := range all {
		keep := true
		if f.known != nil {
			keep = *f.known
		} else {
			if ok && asked < len(drawn) {
				keep = drawn[asked] == nil
			}
			asked++
		}
		if !keep {
			out = append(out, lines[next:f.open]...)
			next = f.close + 1
		}
	}
	out = append(out, lines[next:]...)
	return closeGaps(strings.Join(out, "\n"))
}

// closeGaps is text with no more than one blank line in a row.
func closeGaps(text string) string {
	for strings.Contains(text, "\n\n\n") {
		text = strings.ReplaceAll(text, "\n\n\n", "\n\n")
	}
	return text
}

// figureProblem is a block that doesn't draw, as its writer is told: which
// one, and the renderer's words, cut short.
func figureProblem(what string, n int, err string) string {
	err = firstRunes(strings.TrimSpace(err), errorChars)
	if err == "" {
		err = "the renderer drew nothing"
	}
	fix := "fix its Mermaid syntax - quote a label with brackets, parentheses or punctuation as A[\"label (x)\"], or make it simpler"
	if what == "Chart" {
		fix = "fix its JSON"
	}
	return fmt.Sprintf("%s %d doesn't draw (%s): %s.", what, n, err, fix)
}
