package backend

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
)

// Snapshot is a store's value and its revision.
type Snapshot struct {
	Value jsontext.Value `json:"value"`
	Rev   uint64         `json:"rev"`
}

type settingsChanged struct {
	Rev   uint64     `json:"rev"`
	Patch jsonObject `json:"patch"`
	From  string     `json:"from"`
}

type historyOp struct {
	Type  string         `json:"type"`
	Entry jsontext.Value `json:"entry,omitzero"`
	Key   *string        `json:"key,omitzero"`
}

type historyChanged struct {
	Kind HistoryKind `json:"kind"`
	Op   historyOp   `json:"op"`
	From string      `json:"from"`
}

// themeChanged gives the windows the page colour of the theme now chosen.
func (a *App) themeChanged() {
	settings, _ := a.stores.settings.snapshot()
	uiState, _ := a.stores.uiState.snapshot()
	var s, u jsonObject
	_ = json.Unmarshal(settings, &s)
	_ = json.Unmarshal(uiState, &u)
	color, ok := windowBackground(s, u, a.userThemeCSS(s), a.shared.ContentThemes)
	if !ok {
		color = "light-dark(#ffffff, #1c1c1e)"
	}
	for _, w := range a.allWindows() {
		switch kindOf(w.label) {
		case kindDragPill, kindBackground, kindPdfExport:
			continue
		}
		_ = w.win.SetBackgroundColor(color)
	}
}

// patchSettingsFromBackend changes settings for every window, as one of
// them would.
func (a *App) patchSettingsFromBackend(patch jsonObject) uint64 {
	rev := a.stores.settings.patch(patch)
	a.emitAll("settings-changed", settingsChanged{Rev: rev, Patch: patch, From: ""})
	return rev
}

func validKind(kind HistoryKind) error {
	if kind != historyChat && kind != historyClipboard {
		return fmt.Errorf("unknown history %q", kind)
	}
	return nil
}

func init() {
	command("settings_get", func(c *Call, _ none) (Snapshot, error) {
		v, rev := c.app.stores.settings.snapshot()
		return Snapshot{v, rev}, nil
	})
	command("settings_patch", func(c *Call, a struct {
		Patch jsonObject `json:"patch"`
	}) (uint64, error) {
		rev := c.app.stores.settings.patch(a.Patch)
		_, theme := a.Patch["theme"]
		_, themeID := a.Patch["themeId"]
		if theme || themeID {
			c.app.themeChanged()
		}
		c.app.emitAll("settings-changed", settingsChanged{Rev: rev, Patch: a.Patch, From: c.Label()})
		return rev, nil
	})
	command("settings_fill_defaults", func(c *Call, a struct {
		Defaults jsonObject `json:"defaults"`
	}) (Snapshot, error) {
		if added, rev := c.app.stores.settings.fillMissing(a.Defaults); added != nil {
			c.app.emitAll("settings-changed", settingsChanged{Rev: rev, Patch: added, From: c.Label()})
		}
		v, rev := c.app.stores.settings.snapshot()
		return Snapshot{v, rev}, nil
	})
	command("ui_state_get", func(c *Call, _ none) (jsontext.Value, error) {
		v, _ := c.app.stores.uiState.snapshot()
		return v, nil
	})
	command("ui_state_patch", func(c *Call, a struct {
		Patch jsonObject `json:"patch"`
	}) (nothing, error) {
		c.app.stores.uiState.patch(a.Patch)
		if _, ok := a.Patch["themeChrome"]; ok {
			c.app.themeChanged()
		}
		return nil, nil
	})
	command("history_list", func(c *Call, a struct {
		Kind HistoryKind `json:"kind"`
	}) (jsontext.Value, error) {
		if err := validKind(a.Kind); err != nil {
			return nil, err
		}
		if a.Kind == historyChat {
			return c.app.stores.chat.list(), nil
		}
		v, _ := c.app.stores.clipboard.snapshot()
		return v, nil
	})
	command("history_upsert", func(c *Call, a struct {
		Kind  HistoryKind    `json:"kind"`
		Entry jsontext.Value `json:"entry"`
	}) (nothing, error) {
		if err := validKind(a.Kind); err != nil {
			return nil, err
		}
		if !a.Kind.valid(a.Entry) {
			return nil, fmt.Errorf("invalid %s history entry", a.Kind)
		}
		entry := a.Entry
		if a.Kind == historyChat {
			entry = c.app.stores.chat.save(a.Entry)
		} else {
			c.app.stores.clipboard.upsertEntry(a.Kind, a.Entry)
		}
		c.app.emitAll("history-changed", historyChanged{Kind: a.Kind, Op: historyOp{Type: "upsert", Entry: entry}, From: c.Label()})
		return nil, nil
	})
	command("history_remove", func(c *Call, a struct {
		Kind HistoryKind `json:"kind"`
		Key  string      `json:"key"`
	}) (nothing, error) {
		if err := validKind(a.Kind); err != nil {
			return nil, err
		}
		if a.Kind == historyChat {
			c.app.stores.chat.remove(a.Key)
		} else {
			c.app.stores.clipboard.removeEntry(a.Kind, a.Key)
		}
		c.app.emitAll("history-changed", historyChanged{Kind: a.Kind, Op: historyOp{Type: "remove", Key: &a.Key}, From: c.Label()})
		return nil, nil
	})
	command("history_clear", func(c *Call, a struct {
		Kind HistoryKind `json:"kind"`
	}) (nothing, error) {
		if err := validKind(a.Kind); err != nil {
			return nil, err
		}
		if a.Kind == historyChat {
			c.app.stores.chat.clear()
		} else {
			c.app.stores.clipboard.clearEntries()
		}
		c.app.emitAll("history-changed", historyChanged{Kind: a.Kind, Op: historyOp{Type: "clear"}, From: c.Label()})
		return nil, nil
	})
	command("chat_history_get", func(c *Call, a struct {
		ID string `json:"id"`
	}) (jsontext.Value, error) {
		return c.app.stores.chat.get(a.ID), nil
	})
	command("chat_history_search", func(c *Call, a struct {
		Query string `json:"query"`
	}) ([]jsontext.Value, error) {
		return c.app.stores.chat.search(a.Query, 50), nil
	})
	command("chat_history_archived", func(c *Call, _ none) (int, error) {
		return c.app.stores.chat.archived(), nil
	})
	command("chat_history_move_document", func(c *Call, a struct {
		From string `json:"from"`
		To   string `json:"to"`
	}) (nothing, error) {
		c.app.moveConversations(a.From, a.To)
		return nil, nil
	})
}

// moveConversations takes a document's conversations along with it.
func (a *App) moveConversations(from, to string) {
	if a.stores.chat.moveDocument(from, to) {
		a.emitAll("history-changed", historyChanged{Kind: historyChat, Op: historyOp{Type: "reload"}, From: ""})
	}
}
