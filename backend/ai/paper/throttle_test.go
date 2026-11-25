package paper

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"textus/backend/ai/aicompat"
)

// slow takes a while each step, counting how many are in flight; it fails
// with each of errs first, in turn.
type slow struct {
	now, most, calls atomic.Int64
	mu               sync.Mutex
	errs             []string
}

func (s *slow) Step(ctx context.Context, _ string, _ []aicompat.AgentTurn, _ []aicompat.ToolSpec, _ aicompat.EventSink) (aicompat.StepResult, error) {
	s.calls.Add(1)
	now := s.now.Add(1)
	for {
		most := s.most.Load()
		if now <= most || s.most.CompareAndSwap(most, now) {
			break
		}
	}
	timer := time.NewTimer(30 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-ctx.Done():
		s.now.Add(-1)
		return aicompat.StepResult{}, ctx.Err()
	}
	s.now.Add(-1)
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.errs) > 0 {
		err := s.errs[0]
		s.errs = s.errs[1:]
		return aicompat.StepResult{}, errors.New(err)
	}
	return aicompat.Reply("ok"), nil
}

func (s *slow) AtOnce() int { return AtOnce }

// together runs n steps of model at once, and their errors.
func together(t *testing.T, model Model, n int) []error {
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() {
			_, errs[i] = model.Step(t.Context(), "", nil, nil, nil)
		})
	}
	wg.Wait()
	return errs
}

// waits are the waits a test's retries asked for, which take no time.
func waits(t *testing.T) *[]time.Duration {
	var mu sync.Mutex
	var asked []time.Duration
	before := sleep
	sleep = func(ctx context.Context, d time.Duration) error {
		mu.Lock()
		asked = append(asked, d)
		mu.Unlock()
		return ctx.Err()
	}
	t.Cleanup(func() { sleep = before })
	return &asked
}

func TestNoMoreStepsThanItsSlotsRunAtOnce(t *testing.T) {
	s := &slow{}
	for _, err := range together(t, NewThrottled(s, 3), 10) {
		if err != nil {
			t.Fatal(err)
		}
	}
	if s.most.Load() != 3 {
		t.Fatal(s.most.Load())
	}
}

func TestABusyStepWaitsAndGoesAgain(t *testing.T) {
	asked := waits(t)
	s := &slow{errs: []string{
		"Claude request failed (429 Too Many Requests): rate limited",
		`{"type":"overloaded_error"}`,
	}}
	if _, err := NewThrottled(s, 2).Step(t.Context(), "", nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	// Two waits, 2 s and 4 s at the least.
	if s.calls.Load() != 3 || len(*asked) != 2 || (*asked)[0] < 2*time.Second || (*asked)[1] < 4*time.Second {
		t.Fatal(s.calls.Load(), *asked)
	}
}

func TestAStepWrongInItselfIsNotTriedAgain(t *testing.T) {
	s := &slow{errs: []string{"request failed (400): bad tool schema"}}
	if _, err := NewThrottled(s, 2).Step(t.Context(), "", nil, nil, nil); err == nil || s.calls.Load() != 1 {
		t.Fatal(err, s.calls.Load())
	}
}

func TestAServiceBusyEveryTimeGivesUpAfterItsTries(t *testing.T) {
	s := &slow{}
	for range 10 {
		s.errs = append(s.errs, "request failed (429): slow down")
	}
	_, err := NewThrottled(s, 1).Step(t.Context(), "", nil, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "429") || s.calls.Load() != tries {
		t.Fatal(err, s.calls.Load())
	}
}

func TestAStoppedStepFreesItsSlot(t *testing.T) {
	throttled := NewThrottled(&slow{}, 1)
	// Stopped mid-call, as ai_cancel stops a paper.
	stopped, cancel := context.WithTimeout(t.Context(), 10*time.Millisecond)
	defer cancel()
	if _, err := throttled.Step(stopped, "", nil, nil, nil); err == nil {
		t.Fatal("not stopped")
	}
	next, cancelNext := context.WithTimeout(t.Context(), time.Second)
	defer cancelNext()
	if _, err := throttled.Step(next, "", nil, nil, nil); err != nil {
		t.Fatal(err)
	}
}

func TestABusyAnswerAsksForOneFewerAtOnce(t *testing.T) {
	s := &slow{errs: []string{"request failed (429): slow down"}}
	throttled := NewThrottled(s, 3)
	if _, err := throttled.Step(t.Context(), "", nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if throttled.AtOnce() != 2 {
		t.Fatal(throttled.AtOnce())
	}
	together(t, throttled, 6)
	if s.most.Load() != 2 {
		t.Fatal(s.most.Load())
	}
}

func TestABusyAnswerWithEverySlotTakenStillAsksForFewer(t *testing.T) {
	s := &slow{errs: []string{"request failed (429): slow down"}}
	throttled := NewThrottled(s, 3)
	together(t, throttled, 3)
	if throttled.AtOnce() != 2 {
		t.Fatal(throttled.AtOnce())
	}
	s.most.Store(0)
	together(t, throttled, 6)
	if s.most.Load() != 2 {
		t.Fatal(s.most.Load())
	}
}

// cut streams some text, then the service says it is busy.
type cut struct{ calls atomic.Int64 }

func (c *cut) Step(_ context.Context, _ string, _ []aicompat.AgentTurn, _ []aicompat.ToolSpec, watch aicompat.EventSink) (aicompat.StepResult, error) {
	c.calls.Add(1)
	if watch != nil {
		watch(aicompat.ProviderEvent{Kind: aicompat.EventText, Text: "Half a"})
	}
	return aicompat.StepResult{}, errors.New("stream error: overloaded")
}

func (*cut) AtOnce() int { return AtOnce }

func TestAStepCutOffMidAnswerIsNotTriedAgain(t *testing.T) {
	c := &cut{}
	var shown string
	watch := func(e aicompat.ProviderEvent) {
		if e.Kind == aicompat.EventText {
			shown += e.Text
		}
	}
	if _, err := NewThrottled(c, 2).Step(t.Context(), "", nil, nil, watch); err == nil {
		t.Fatal("no error")
	}
	if c.calls.Load() != 1 || shown != "Half a" {
		t.Fatal(c.calls.Load(), shown)
	}
}

func TestBusyIsReadFromTheStatusOrTheBody(t *testing.T) {
	for text, busy := range map[string]bool{
		"OpenAI request failed (429 Too Many Requests): {}":             true,
		"Claude request failed (529 <unknown status code>): overloaded": true,
		"stream error: rate_limit_error":                                true,
		"Claude request failed (401 Unauthorized): bad key":             false,
		"Claude request failed (400 Bad Request): too long":             false,
		"request failed (400): see docs (429 is our limit)":             false,
	} {
		if isBusy(errors.New(text)) != busy {
			t.Error(text)
		}
	}
}

func TestWaitsDoubleWithALittleSpread(t *testing.T) {
	if wait(1, 0) != 2*time.Second || wait(3, 0) != 8*time.Second || wait(1, 999) >= 2500*time.Millisecond {
		t.Fatal(wait(1, 0), wait(3, 0), wait(1, 999))
	}
}
