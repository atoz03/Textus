package paper

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"textus/backend/ai/aicompat"
)

// How many model calls a paper makes at once, and what it does when the
// service says it is busy. Every stage steps through one Throttled, so
// research helpers, writers and the reviewer together never have more
// calls in flight than the provider bears; a call refused for its rate or
// an overload waits and goes again, holding its slot while it waits, so
// the paper slows down instead of piling on.

// tries are the tries of one call before its error stands.
const tries = 5

// firstWait is the first wait; each one after is twice the last.
const firstWait = 2 * time.Second

// isBusy reports whether err reads as the service being busy rather than
// the request being wrong: a rate limit or an overload, or the server
// failing.
func isBusy(err error) bool {
	kind := aicompat.ErrorKind(err.Error())
	return kind == "rateLimit" || kind == "server"
}

// wait is the wait before try n (1 for the first retry): doubling, with up
// to a quarter more so calls refused together don't all come back
// together.
func wait(n int, spread uint64) time.Duration {
	base := firstWait * time.Duration(1<<min(max(n-1, 0), 5))
	return base + time.Duration(float64(base)*float64(spread%1000)/4000)
}

// sleep waits d, or until ctx ends. Tests count the waits instead.
var sleep = func(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// slots are a counting semaphore whose slots can be given up for good.
type slots struct {
	mu      sync.Mutex
	free    int
	waiting []chan struct{}
}

// acquire takes a slot, waiting for one; an error when ctx ends first.
func (s *slots) acquire(ctx context.Context) error {
	s.mu.Lock()
	if s.free > 0 && len(s.waiting) == 0 {
		s.free--
		s.mu.Unlock()
		return nil
	}
	ready := make(chan struct{})
	s.waiting = append(s.waiting, ready)
	s.mu.Unlock()
	select {
	case <-ready:
		return nil
	case <-ctx.Done():
		s.mu.Lock()
		for i, w := range s.waiting {
			if w == ready {
				s.waiting = append(s.waiting[:i], s.waiting[i+1:]...)
				s.mu.Unlock()
				return ctx.Err()
			}
		}
		s.mu.Unlock()
		// Handed a slot as it stopped: passed on.
		s.release()
		return ctx.Err()
	}
}

// release gives a slot back: to the first waiting, else to the free ones.
func (s *slots) release() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.waiting) > 0 {
		ready := s.waiting[0]
		s.waiting = s.waiting[1:]
		close(ready)
		return
	}
	s.free++
}

// forgetFree gives up one free slot for good; false when none is free.
func (s *slots) forgetFree() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.free == 0 {
		return false
	}
	s.free--
	return true
}

// Throttled is a model with at most AtOnce steps in flight, a busy step
// tried again. Each busy answer gives up a slot for good (one is always
// kept): a provider that refuses this many at once is asked for fewer.
type Throttled struct {
	inner  Model
	slots  slots
	atOnce atomic.Int64
	// toDrop are slots given up while every one was busy: the next to come
	// back goes for good instead.
	toDrop atomic.Int64
}

// NewThrottled is inner with at most atOnce steps in flight (one at the
// least).
func NewThrottled(inner Model, atOnce int) *Throttled {
	atOnce = max(atOnce, 1)
	t := &Throttled{inner: inner, slots: slots{free: atOnce}}
	t.atOnce.Store(int64(atOnce))
	return t
}

// done gives a step's slot back, unless one is owed.
func (t *Throttled) done() {
	for {
		owed := t.toDrop.Load()
		if owed == 0 {
			t.slots.release()
			return
		}
		if t.toDrop.CompareAndSwap(owed, owed-1) {
			return
		}
	}
}

// askFewer gives up one slot, keeping one: a free one now, else the next
// back.
func (t *Throttled) askFewer() {
	for {
		n := t.atOnce.Load()
		if n <= 1 {
			return
		}
		if t.atOnce.CompareAndSwap(n, n-1) {
			break
		}
	}
	if !t.slots.forgetFree() {
		// Every slot is busy: given up as the first comes back.
		t.toDrop.Add(1)
	}
}

func (t *Throttled) Step(ctx context.Context, instructions string, turns []aicompat.AgentTurn, tools []aicompat.ToolSpec, watch aicompat.EventSink) (aicompat.StepResult, error) {
	if err := t.slots.acquire(ctx); err != nil {
		return aicompat.StepResult{}, err
	}
	// Given back however the step ends: a stopped paper frees its slot.
	defer t.done()
	// A step that streamed some of its answer isn't tried again: the paper
	// has shown it already.
	var streamed atomic.Bool
	seen := watch
	if watch != nil {
		seen = func(e aicompat.ProviderEvent) {
			streamed.Store(true)
			watch(e)
		}
	}
	for tried := 1; ; tried++ {
		result, err := t.inner.Step(ctx, instructions, turns, tools, seen)
		if err == nil || tried >= tries || !isBusy(err) || streamed.Load() || ctx.Err() != nil {
			return result, err
		}
		t.askFewer()
		if err := sleep(ctx, wait(tried, uint64(time.Now().Nanosecond()))); err != nil {
			return aicompat.StepResult{}, err
		}
	}
}

func (t *Throttled) AtOnce() int { return int(t.atOnce.Load()) }
