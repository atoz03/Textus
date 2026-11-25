package backend

import (
	"encoding/json/jsontext"
	"errors"
	"strconv"
	"sync"
)

// Detached chat windows: the Agent conversation popped out of an editor
// window into a window of its own. Unlike Quick Ask it follows whatever
// document that editor shows, so there is at most one per editor window,
// and it closes with it.

const (
	// chatLabelPrefix starts every chat window's label: a chat window has
	// no tab bar or document, and is never taken for an editor window.
	chatLabelPrefix = "chat-"
	// eventChatAdoptHandoff tells one chat window that a fresh handoff
	// waits under its label, to claim. Only sent to one shown again.
	eventChatAdoptHandoff = "chat:adopt-handoff"
	// eventChatWindowsChanged tells every editor window the set of chat
	// windows changed, so none keeps pushing at a closed one
	// (CHAT_WINDOWS_CHANGED in src/platform/bindings/app.ts).
	eventChatWindowsChanged = "chat:windows-changed"
	chatWidth, chatHeight   = 420, 560
	chatMinW, chatMinH      = 320, 240
)

// chatHandoff is everything a detached chat window needs to carry on where
// the editor window's conversation left off (ChatHandoff in
// src/platform/bindings/app.ts). State is opaque: the frontend defines it.
type chatHandoff struct {
	// EditorLabel is the editor window the chat starts with; once running,
	// the chat follows whichever editor last pushed it context.
	EditorLabel string         `json:"editorLabel"`
	State       jsontext.Value `json:"state"`
}

// chatWindows are the chat windows open, each with the editor window it
// serves, and the handoffs waiting for their window's page to claim them.
type chatWindows struct {
	mu      sync.Mutex
	editors map[string]string // chat window → the editor window it serves
	pending map[string]chatHandoff
}

// editorFor is the editor window chat serves: where a menu command focused
// on a chat goes, since the chat has no document.
func (c *chatWindows) editorFor(chat string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	editor, ok := c.editors[chat]
	return editor, ok
}

// serving is the chat window serving editor.
func (c *chatWindows) serving(editor string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for chat, e := range c.editors {
		if e == editor {
			return chat, true
		}
	}
	return "", false
}

func (c *chatWindows) add(chat, editor string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.editors == nil {
		c.editors = map[string]string{}
	}
	c.editors[chat] = editor
}

// park keeps h for chat's page to claim.
func (c *chatWindows) park(chat string, h chatHandoff) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.pending == nil {
		c.pending = map[string]chatHandoff{}
	}
	c.pending[chat] = h
}

// take is the handoff waiting for chat, claimed: nil when none waits.
func (c *chatWindows) take(chat string) *chatHandoff {
	c.mu.Lock()
	defer c.mu.Unlock()
	h, ok := c.pending[chat]
	if !ok {
		return nil
	}
	delete(c.pending, chat)
	return &h
}

// forget drops what involves the closed window label, both ways: a closed
// chat its own entry, a closed editor its chat, which it answers to close.
// changed is whether anything was dropped.
func (c *chatWindows) forget(label string) (orphans []string, changed bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.pending, label)
	for chat, editor := range c.editors {
		switch {
		case chat == label:
			delete(c.editors, chat)
			changed = true
		case editor == label:
			delete(c.editors, chat)
			orphans = append(orphans, chat)
			changed = true
		}
	}
	return orphans, changed
}

// forgetChatWindow keeps the chat windows in step with the closed window
// label: a chat scoped to a closed editor closes with it (it could only
// push at a gone window, and kept the app running).
func (a *App) forgetChatWindow(label string) {
	orphans, changed := a.chats.forget(label)
	if changed {
		a.notifyChatWindowsChanged()
	}
	for _, chat := range orphans {
		if w := a.window(chat); w != nil {
			w.destroy()
		}
	}
}

// notifyChatWindowsChanged tells every editor window, the only ones that
// listen.
func (a *App) notifyChatWindowsChanged() {
	for _, w := range a.allWindows() {
		if isEditorWindow(w.label) {
			_ = w.emit(eventChatWindowsChanged, nil)
		}
	}
}

// detachRequest is detach_chat_window's input (DetachRequest in
// src/platform/bindings/app.ts).
type detachRequest struct {
	State    jsontext.Value `json:"state"`
	Position *[2]*float64   `json:"position"`
	Title    string         `json:"title"`
	Pinned   bool           `json:"pinned"`
}

// detachChat pops the calling editor window's chat out into a window of
// its own, or hands the state to the one already serving it; the chat
// window's label.
func (a *App) detachChat(editor *appWindow, r detachRequest) (string, error) {
	handoff := chatHandoff{EditorLabel: editor.label, State: r.State}
	if chat, ok := a.chats.serving(editor.label); ok {
		if w := a.window(chat); w != nil {
			// Its page won't ask for a handoff again by itself: park the new
			// state and tell it to claim it.
			a.chats.park(chat, handoff)
			_ = w.emit(eventChatAdoptHandoff, nil)
			w.win.Show()
			w.win.Focus()
			return chat, nil
		}
		// Registered but gone: drop it and open a fresh one.
		a.chats.forget(chat)
	}
	label := chatLabelPrefix + strconv.FormatUint(uint64(a.windowCounter.Add(1)), 10)
	a.chats.park(label, handoff)
	a.chats.add(label, editor.label)
	spec := windowSpec{
		label: label, url: "/index.html?view=chat", title: r.Title,
		width: chatWidth, height: chatHeight, minW: chatMinW, minH: chatMinH,
		// Set from the start, so a pinned chat never shows behind the
		// editor before its page mounts.
		alwaysOnTop: r.Pinned,
	}
	if r.Position != nil && r.Position[0] != nil && r.Position[1] != nil {
		spec.at = &[2]float64{*r.Position[0], *r.Position[1]}
	}
	// Wayland places the window and ignores always-on-top: as a child of
	// its editor the chat stays above it.
	if isWayland() {
		spec.parent = editor.win
	}
	if _, err := a.openWindow(spec); err != nil {
		a.chats.forget(label)
		return "", err
	}
	a.notifyChatWindowsChanged()
	return label, nil
}

func init() {
	// Pops this editor window's chat out into a window of its own.
	command("detach_chat_window", func(c *Call, a struct {
		Request detachRequest `json:"request"`
	}) (string, error) {
		if c.window == nil {
			return "", errors.New("detach_chat_window: no window")
		}
		return c.app.detachChat(c.window, a.Request)
	})
	// The chat window serving the calling editor window, if any. Asked on
	// mount and focus, so a reloaded page never trusts a stale label.
	command("current_chat_window", func(c *Call, _ none) (*string, error) {
		if chat, ok := c.app.chats.serving(c.Label()); ok {
			return &chat, nil
		}
		return nil, nil
	})
	// Claimed by the chat window's page as it mounts, and whenever
	// eventChatAdoptHandoff says another waits.
	command("take_chat_handoff", func(c *Call, _ none) (*chatHandoff, error) {
		return c.app.chats.take(c.Label()), nil
	})
}
