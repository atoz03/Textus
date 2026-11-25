package backend

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"log"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"unicode/utf8"

	"textus/backend/ai"
)

// HistoryKind names a history the frontend keeps: "chat" or "clipboard".
type HistoryKind string

const (
	historyChat      HistoryKind = "chat"
	historyClipboard HistoryKind = "clipboard"
)

const maxClipboardText = 50_000

func (k HistoryKind) stem() string {
	if k == historyChat {
		return "chat-history"
	}
	return "clipboard-history"
}

func (k HistoryKind) max() int {
	if k == historyChat {
		return maxConversations
	}
	return 30
}

// entryFields are the fields of a history entry the backend reads.
type entryFields struct {
	ID        *string          `json:"id"`
	Text      *string          `json:"text"`
	DocPath   *string          `json:"docPath"`
	Title     jsontext.Value   `json:"title"`
	UpdatedAt *float64         `json:"updatedAt"`
	At        *float64         `json:"at"`
	Turns     []jsontext.Value `json:"turns"`
}

func fieldsOf(entry jsontext.Value) entryFields {
	var f entryFields
	_ = json.Unmarshal(entry, &f, json.RejectUnknownMembers(false))
	return f
}

// keyOf is what makes two entries the same: a conversation's id, or a
// clipboard text itself (copying again moves it to the top).
func (k HistoryKind) keyOf(entry jsontext.Value) (string, bool) {
	f := fieldsOf(entry)
	if k == historyChat {
		if f.ID == nil {
			return "", false
		}
		return *f.ID, true
	}
	if f.Text == nil {
		return "", false
	}
	return *f.Text, true
}

func (k HistoryKind) valid(entry jsontext.Value) bool {
	f := fieldsOf(entry)
	if k == historyChat {
		return f.ID != nil && safeID(*f.ID) && f.UpdatedAt != nil && f.Turns != nil
	}
	return f.Text != nil && utf8.RuneCountInString(*f.Text) <= maxClipboardText && f.At != nil
}

// tidy orders and caps a list: conversations most recently active first
// (stable, so an upserted entry leads ties). It returns the overflow.
func (k HistoryKind) tidy(list []jsontext.Value) (kept, dropped []jsontext.Value) {
	if k == historyChat {
		updated := func(e jsontext.Value) float64 {
			if f := fieldsOf(e); f.UpdatedAt != nil {
				return *f.UpdatedAt
			}
			return 0
		}
		sort.SliceStable(list, func(i, j int) bool { return updated(list[i]) > updated(list[j]) })
	}
	if len(list) <= k.max() {
		return list, nil
	}
	return list[:k.max()], slices.Clone(list[k.max():])
}

// Stores are the app's JSON files: the settings, the UI state and the
// histories.
type Stores struct {
	settings  *Store
	uiState   *Store
	clipboard *Store
	chat      *ChatHistory
}

func loadStores(dirs Dirs) *Stores {
	s := &Stores{
		settings:  loadStore(filepath.Join(dirs.Config, fileName("settings")), "settings", false, true),
		uiState:   loadStore(filepath.Join(dirs.Data, fileName("ui-state")), "state", false, true),
		clipboard: loadStore(filepath.Join(dirs.Data, fileName(historyClipboard.stem())), "entries", true, false),
		chat:      loadChatHistory(dirs.Data),
	}
	if s.settings.missingAtLoad {
		s.seedNewProfile()
	}
	s.carryKeptApart(dirs.Config)
	return s
}

// seedNewProfile marks a new profile as having no moved skills to bring
// over; an older settings file lacks the mark and gets them installed
// (installMovedSkills). In memory until the first write.
func (s *Stores) seedNewProfile() {
	s.settings.mu.Lock()
	defer s.settings.mu.Unlock()
	mergeTopLevel(s.settings.object, jsonObject{movedSkillsKey: mustJSON(ai.MovedToMarket)})
}

// carryKeptApart moves what older versions kept in files of their own (the
// last window's size and place, the recent files and folders) into the UI
// state, once: the old files go only once the UI state holds them on disk.
func (s *Stores) carryKeptApart(configDir string) {
	patch := jsonObject{}
	var old []string
	prefs := filepath.Join(configDir, fileName("prefs"))
	if raw, err := os.ReadFile(prefs); err == nil {
		var kept jsonObject
		if json.Unmarshal(raw, &kept) == nil {
			for _, pair := range [][2]string{
				{"editor_window_size", "editorWindowSize"},
				{"editor_window_position", "editorWindowPosition"},
			} {
				if v, ok := kept[pair[0]]; ok {
					if _, has := s.uiState.get(pair[1]); !has {
						patch[pair[1]] = v
					}
				}
			}
		} else {
			moveAside(prefs)
		}
		old = append(old, prefs)
	}
	for _, pair := range [][2]string{{"recent_files", "recentFiles"}, {"recent_folders", "recentFolders"}} {
		path := filepath.Join(configDir, fileName(pair[0]))
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var list []string
		if json.Unmarshal(raw, &list) == nil {
			if _, has := s.uiState.get(pair[1]); !has {
				patch[pair[1]] = mustJSON(list)
			}
		} else {
			moveAside(path)
		}
		old = append(old, path)
	}
	if len(old) == 0 || s.uiState.unreadable {
		return
	}
	if len(patch) > 0 {
		s.uiState.patch(patch)
		if err := s.uiState.flush(); err != nil {
			log.Printf("textus: cannot write %s: %v", s.uiState.path, err)
			return
		}
	}
	for _, path := range old {
		_ = os.Remove(path)
	}
}

func (s *Stores) flushAll() {
	for _, store := range []*Store{s.settings, s.uiState, s.clipboard} {
		if err := store.flush(); err != nil {
			log.Printf("textus: cannot write %s: %v", store.path, err)
		}
	}
	s.chat.flush()
}

// upsertEntry puts entry at the top of a list store, in place of the one
// with its key, and caps the list.
func (s *Store) upsertEntry(kind HistoryKind, entry jsontext.Value) uint64 {
	key, hasKey := kind.keyOf(entry)
	return s.update(func(s *Store) {
		list := s.items[:0:0]
		list = append(list, entry)
		for _, e := range s.items {
			if k, ok := kind.keyOf(e); ok == hasKey && k == key {
				continue
			}
			list = append(list, e)
		}
		s.items, _ = kind.tidy(list)
	})
}

func (s *Store) removeEntry(kind HistoryKind, key string) uint64 {
	return s.update(func(s *Store) {
		s.items = slices.DeleteFunc(s.items, func(e jsontext.Value) bool {
			k, ok := kind.keyOf(e)
			return ok && k == key
		})
	})
}

func (s *Store) clearEntries() uint64 {
	return s.update(func(s *Store) { s.items = []jsontext.Value{} })
}
