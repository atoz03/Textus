package backend

import (
	"context"
	"errors"
	"sync"

	"textus/backend/ai"
)

// The AI requests running, by the id the frontend gave each: the chat's
// stop button cancels one (ai_cancel), and a message the user sends into a
// reply still running goes to its inbox (ai_steer), which the agent loop
// reads between steps.

// cancelled is what a stopped request fails with: the frontend's
// AI_CANCELLED (src/platform/ipc-ai-types.ts), which it doesn't show as a
// failure.
const cancelled = "cancelled"

var running = struct {
	sync.Mutex
	stops   map[string]*context.CancelFunc
	inboxes map[string]*[]string
}{stops: map[string]*context.CancelFunc{}, inboxes: map[string]*[]string{}}

// cancellable runs work until it finishes or id is cancelled. Cancelling
// answers at once and cancels work's context, so what it is waiting on -
// a model's stream, a page - stops too.
func cancellable[T any](ctx context.Context, id string, work func(ctx context.Context) (T, error)) (T, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := &cancel
	running.Lock()
	running.stops[id] = stop
	running.Unlock()
	// However it ends: a stale entry would let a reused id cancel a later
	// request. Only its own, should the id have been reused meanwhile.
	defer func() {
		running.Lock()
		if running.stops[id] == stop {
			delete(running.stops, id)
		}
		running.Unlock()
	}()
	type answer struct {
		value T
		err   error
	}
	done := make(chan answer, 1)
	go func() {
		value, err := work(ctx)
		done <- answer{value, err}
	}()
	select {
	case a := <-done:
		return a.value, a.err
	case <-ctx.Done():
		var zero T
		return zero, errors.New(cancelled)
	}
}

// cancelRequest stops the request id, if it is running.
func cancelRequest(id string) {
	running.Lock()
	stop := running.stops[id]
	delete(running.stops, id)
	running.Unlock()
	if stop != nil {
		(*stop)()
	}
}

// inbox is a running request's messages from the user, open while it runs.
type inbox struct {
	id       string
	messages *[]string
}

// openInbox opens the request id's inbox; close it however the request
// ends.
func openInbox(id string) inbox {
	box := &[]string{}
	running.Lock()
	running.inboxes[id] = box
	running.Unlock()
	return inbox{id, box}
}

// take is the messages sent in since it was last read, oldest first.
func (b inbox) take() []string {
	running.Lock()
	defer running.Unlock()
	messages := *b.messages
	*b.messages = nil
	return messages
}

func (b inbox) close() {
	running.Lock()
	if running.inboxes[b.id] == b.messages {
		delete(running.inboxes, b.id)
	}
	running.Unlock()
}

// steer puts message into the running request id's inbox; false when none
// of that id is running, and the frontend sends it as a message of its own.
func steer(id, message string) bool {
	running.Lock()
	defer running.Unlock()
	box, ok := running.inboxes[id]
	if ok {
		*box = append(*box, message)
	}
	return ok
}

func init() {
	command("ai_cancel", func(c *Call, a struct {
		RequestID string `json:"requestId"`
	}) (nothing, error) {
		cancelRequest(a.RequestID)
		// The pictures it asked for and are still being made stop too.
		ai.StopPictures(a.RequestID)
		return nil, nil
	})
	command("ai_steer", func(c *Call, a struct {
		RequestID string `json:"requestId"`
		Message   string `json:"message"`
	}) (bool, error) {
		return steer(a.RequestID, a.Message), nil
	})
}
