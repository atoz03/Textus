package backend

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"log"

	"github.com/egoist/mygo"
)

// HostEvent is how every event reaches a page: the Tauri event's name and
// payload in one envelope, which src/platform/native/event.ts hands to the
// listeners of that name.
type HostEvent struct {
	Event   string         `json:"event"`
	Payload jsontext.Value `json:"payload"`
}

// hostEvent is the one MyGo event the backend sends.
var hostEvent = mygo.NewEvent[HostEvent]("textus:event")

// Names of the window's own events (Tauri's tauri://… events).
const (
	eventCloseRequested = "native://close-requested"
	eventFocus          = "native://focus"
	eventBlur           = "native://blur"
	eventResized        = "native://resized"
	eventMoved          = "native://moved"
	eventDragDrop       = "native://drag-drop"
)

func encodePayload(payload any) (jsontext.Value, error) {
	if raw, ok := payload.(jsontext.Value); ok {
		if len(raw) == 0 {
			return jsontext.Value("null"), nil
		}
		return raw, nil
	}
	return json.Marshal(payload)
}

// emitTo sends event to the window labelled label.
func (a *App) emitTo(label, event string, payload any) error {
	w := a.window(label)
	if w == nil {
		return fmt.Errorf("no window %s", label)
	}
	return w.emit(event, payload)
}

// emitAll sends event to every window.
func (a *App) emitAll(event string, payload any) {
	for _, w := range a.allWindows() {
		_ = w.emit(event, payload)
	}
}

func (w *appWindow) emit(event string, payload any) error {
	raw, err := encodePayload(payload)
	if err != nil {
		log.Printf("textus: cannot encode %s: %v", event, err)
		return err
	}
	if w.win == nil || w.win.IsDestroyed() {
		return nil
	}
	return hostEvent.Emit(w.win, HostEvent{Event: event, Payload: raw})
}
