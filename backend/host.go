package backend

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"log"
	"runtime/debug"

	"github.com/egoist/mygo"
)

// Host is the one service the frontend calls (src/platform/native/core.ts).
// Its pages keep the command contract they had under Tauri: a command name
// and its arguments by name (src/platform/bindings), answered with a value
// or an error string.
type Host struct{ app *App }

// Invoke runs one command and returns its result as JSON.
func (h *Host) Invoke(ctx context.Context, command string, args jsontext.Value) (jsontext.Value, error) {
	return h.app.dispatch(ctx, command, args, nil)
}

// Stream runs a command that sends values as it works (a Tauri Channel
// argument), through channel, and returns its result.
func (h *Host) Stream(ctx context.Context, command string, args jsontext.Value, channel *mygo.Channel[jsontext.Value]) (jsontext.Value, error) {
	return h.app.dispatch(ctx, command, args, channel)
}

// Emit sends an event from a page to the window labelled target ("*": every
// window), as Tauri's emitTo did.
func (h *Host) Emit(ctx context.Context, target, event string, payload jsontext.Value) error {
	if target == "*" {
		h.app.emitAll(event, payload)
		return nil
	}
	return h.app.emitTo(target, event, payload)
}

// Call is one command, with what its handler may need of the caller.
type Call struct {
	ctx    context.Context
	app    *App
	window *appWindow // the calling window; nil outside a window (tests)
	args   jsontext.Value
	stream *mygo.Channel[jsontext.Value]
	name   string
}

// Label is the calling window's label, "" outside a window.
func (c *Call) Label() string {
	if c.window == nil {
		return ""
	}
	return c.window.label
}

// Send streams v to the page, for a command that takes a channel.
func (c *Call) Send(v any) error {
	if c.stream == nil {
		return fmt.Errorf("%s: no channel to send on", c.name)
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return c.stream.Send(raw)
}

type handler func(c *Call) (any, error)

// commands maps each command name to its handler.
var commands = map[string]handler{}

// command registers a command whose arguments decode into A, by name.
func command[A any, R any](name string, fn func(c *Call, a A) (R, error)) {
	if _, dup := commands[name]; dup {
		panic("textus: command registered twice: " + name)
	}
	commands[name] = func(c *Call) (any, error) {
		var a A
		if len(c.args) > 0 && c.args.Kind() != 'n' {
			if err := json.Unmarshal(c.args, &a); err != nil {
				return nil, fmt.Errorf("%s: invalid arguments: %v", c.name, err)
			}
		}
		return fn(c, a)
	}
}

// none is the arguments of a command that takes none.
type none struct{}

// nothing is the result of a command that returns nothing (null).
type nothing = *struct{}

func (a *App) dispatch(ctx context.Context, name string, args jsontext.Value, stream *mygo.Channel[jsontext.Value]) (result jsontext.Value, err error) {
	h, ok := commands[name]
	if !ok {
		return nil, fmt.Errorf("unknown command %s", name)
	}
	c := &Call{ctx: ctx, app: a, args: args, stream: stream, name: name}
	if ctx != nil {
		c.window = a.windowOf(mygo.CallerWindow(ctx))
	}
	defer func() {
		if r := recover(); r != nil {
			log.Printf("textus: %s panicked: %v\n%s", name, r, debug.Stack())
			err = fmt.Errorf("%s failed: %v", name, r)
		}
	}()
	v, err := h(c)
	if err != nil {
		return nil, err
	}
	if raw, ok := v.(jsontext.Value); ok {
		if len(raw) == 0 {
			return jsontext.Value("null"), nil
		}
		return raw, nil
	}
	return json.Marshal(v)
}
