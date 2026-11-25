package paper

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"textus/backend/ai/aicompat"
)

// Sections written at once: each kept as it is done, whatever the order,
// the paper handed back in the outline's; and what that saves. The clock
// is a bubble's, so a writer's wait costs no real time and the times are
// what the waits add up to.

// timed writes section "S<i>" after delays[i], handing it over whole; a
// section whose delay is negative fails.
type timed struct {
	delays []time.Duration
	atOnce int
}

func (m *timed) Step(_ context.Context, _ string, turns []aicompat.AgentTurn, _ []aicompat.ToolSpec, _ aicompat.EventSink) (aicompat.StepResult, error) {
	_, rest, _ := strings.Cut(turns[0].Text, `Your section: "S`)
	digits, _, _ := strings.Cut(rest, `"`)
	i, err := strconv.Atoi(digits)
	if err != nil {
		panic("no section in " + turns[0].Text)
	}
	if turns[len(turns)-1].Kind != aicompat.User {
		return aicompat.Reply("Written."), nil
	}
	if m.delays[i] < 0 {
		time.Sleep(50 * time.Millisecond)
		return aicompat.StepResult{}, errors.New("Claude request failed (400): no")
	}
	time.Sleep(m.delays[i])
	return submitting(fmt.Sprintf("s%d", i), sectionText(97, aChart)), nil
}

func (m *timed) AtOnce() int { return m.atOnce }

// timedOutline is an outline of n sections titled S0, S1, ….
func timedOutline(n int) Outline {
	plan := outlineOf(n, 100)
	for i := range plan.Sections {
		plan.Sections[i].Title = fmt.Sprintf("S%d", i)
	}
	return plan
}

func ms(delays ...int) []time.Duration {
	out := make([]time.Duration, len(delays))
	for i, d := range delays {
		out[i] = time.Duration(d) * time.Millisecond
	}
	return out
}

// drafted drafts plan, answering the sections and the order they were
// kept in.
func drafted(ctx context.Context, writer Model, plan *Outline) ([]string, []int, error) {
	paper, dossier := english(), draftingDossier()
	var mu sync.Mutex
	var kept []int
	sections, err := Draft(ctx, writer, &paper, plan, &dossier, nil, func(int) {}, func(i int, _ string) error {
		mu.Lock()
		kept = append(kept, i)
		mu.Unlock()
		return nil
	}, ignore, nil)
	return sections, kept, err
}

func TestSectionsAreKeptAsTheyAreDoneAndHandedBackInOrder(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		plan := timedOutline(4)
		sections, kept, err := drafted(t.Context(), &timed{ms(500, 100, 300, 100), 3}, &plan)
		// The slow first one keeps none of the others waiting, nor unsaved.
		if err != nil || !slices.Equal(kept, []int{1, 3, 2, 0}) {
			t.Fatal(kept, err)
		}
		var headings []string
		for _, s := range sections {
			headings = append(headings, firstLine(s))
		}
		if want := []string{"## S0", "## S1", "## S2", "## S3"}; !slices.Equal(headings, want) {
			t.Fatal(headings)
		}
	})
}

func TestAFailedSectionLetsThoseUnderWayBeKeptAndStartsNoMore(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		delays := ms(0, 200, 200, 200, 200)
		delays[0] = -1
		plan := timedOutline(5)
		_, kept, err := drafted(t.Context(), &timed{delays, 3}, &plan)
		if err == nil || !strings.Contains(err.Error(), "(400)") {
			t.Fatal(err)
		}
		// 1 and 2 were under way, and end at the same moment; 3 would have
		// begun once 0 failed.
		slices.Sort(kept)
		if !slices.Equal(kept, []int{1, 2}) {
			t.Fatal(kept)
		}
	})
}

// draftedAsBefore drafts plan as the paper writer once did: three at a
// time, each handed on only once every one before it was - a slow section
// held the slots of those done after it. Answers how many were written.
func draftedAsBefore(ctx context.Context, writer Model, plan *Outline) int {
	paper, dossier := english(), draftingDossier()
	n := len(plan.Sections)
	done := make([]chan error, n)
	written := 0
	handOn := func(i int) {
		if <-done[i] == nil {
			written++
		}
	}
	for i := range n {
		// The fourth starts only once the first is handed on, and so on.
		if i >= 3 {
			handOn(i - 3)
		}
		done[i] = make(chan error, 1)
		go func() {
			_, err := writeSection(ctx, writer, &paper, plan, i, &dossier, nil, ignore, nil)
			done[i] <- err
		}()
	}
	for i := max(n-3, 0); i < n; i++ {
		handOn(i)
	}
	return written
}

func TestDraftingEightSectionsAtOnceTakesLessThanThreeInTurn(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		// What a writer takes, in seconds: as a real run's sections vary.
		delays := ms(90_000, 30_000, 40_000, 20_000, 50_000, 30_000, 20_000, 40_000)
		plan := timedOutline(8)
		start := time.Now()
		if n := draftedAsBefore(t.Context(), &timed{delays, 3}, &plan); n != 8 {
			t.Fatal(n)
		}
		before := time.Since(start)
		var after []time.Duration
		for _, atOnce := range []int{3, 4, 6} {
			start := time.Now()
			sections, _, err := drafted(t.Context(), &timed{delays, atOnce}, &plan)
			if err != nil || len(sections) != 8 {
				t.Fatal(len(sections), err)
			}
			after = append(after, time.Since(start))
		}
		t.Logf("drafting 8 sections (90/30/40/20/50/30/20/40 s each): before, 3 in turn: %v", before)
		for i, atOnce := range []int{3, 4, 6} {
			t.Logf("  after, %d at once: %v", atOnce, after[i])
		}
		if after[0] >= before {
			t.Error("unordered at 3 doesn't beat ordered at 3")
		}
		if after[2]*2 > before {
			t.Error("6 at once takes more than half the time")
		}
	})
}
