package backend

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
)

const (
	maxConversations = 500
	chatIndex        = "index.json"
	chatTrash        = ".trash"
	// keptFiles is how many conversation files stay loaded at once.
	keptFiles = 4
)

// safeID reports whether a conversation id may name its file.
func safeID(id string) bool {
	if id == "" || len(id) > 128 || id == "index" {
		return false
	}
	for _, c := range id {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

// ChatHistory keeps each conversation in a file of its own, and a summary of
// every one in index.json, most recent first.
type ChatHistory struct {
	dir   string
	index *Store

	mu    sync.Mutex // held through every whole operation
	files map[string]*Store
}

func loadChatHistory(dataDir string) *ChatHistory {
	dir := filepath.Join(dataDir, dirName(historyChat.stem()))
	h := &ChatHistory{
		dir:   dir,
		index: loadStore(filepath.Join(dir, chatIndex), "entries", true, false),
		files: map[string]*Store{},
	}
	h.rebuildLostIndex()
	return h
}

func summary(entry jsontext.Value) jsontext.Value {
	f := fieldsOf(entry)
	title := f.Title
	if title == nil {
		title = jsontext.Value(`""`)
	}
	var doc any
	if f.DocPath != nil {
		doc = *f.DocPath
	}
	var updated any
	if f.UpdatedAt != nil {
		updated = *f.UpdatedAt
	}
	return mustJSON(map[string]any{
		"id":        f.ID,
		"docPath":   doc,
		"title":     title,
		"updatedAt": updated,
		"turnCount": len(f.Turns),
	})
}

func (h *ChatHistory) list() jsontext.Value {
	v, _ := h.index.snapshot()
	return v
}

func (h *ChatHistory) ids() []string {
	h.index.mu.Lock()
	defer h.index.mu.Unlock()
	var ids []string
	for _, e := range h.index.items {
		if f := fieldsOf(e); f.ID != nil {
			ids = append(ids, *f.ID)
		}
	}
	return ids
}

// get is one whole conversation, turns included, or nil when it is gone.
func (h *ChatHistory) get(id string) jsontext.Value {
	if !safeID(id) {
		return nil
	}
	h.mu.Lock()
	store := h.files[id]
	h.mu.Unlock()
	var entry jsontext.Value
	if store != nil {
		entry, _ = store.snapshot()
	} else {
		entry = readStoreFile(filepath.Join(h.dir, id+".json"), "entry", false)
	}
	if entry == nil || fieldsOf(entry).Turns == nil {
		return nil
	}
	return entry
}

// fileStore is the store of conversation id's file. A few are kept loaded;
// past that, the others are written and let go.
func (h *ChatHistory) fileStore(id string) *Store {
	if _, ok := h.files[id]; !ok && len(h.files) >= keptFiles {
		for other, store := range h.files {
			if err := store.flush(); err != nil {
				log.Printf("textus: cannot write %s.json: %v", other, err)
				continue
			}
			delete(h.files, other)
		}
	}
	store, ok := h.files[id]
	if !ok {
		store = loadStore(filepath.Join(h.dir, id+".json"), "entry", false, false)
		h.files[id] = store
	}
	return store
}

// save writes one conversation and its line of the index, moving the oldest
// past the cap to the trash. It returns the summary other windows are told.
func (h *ChatHistory) save(entry jsontext.Value) jsontext.Value {
	h.mu.Lock()
	defer h.mu.Unlock()
	f := fieldsOf(entry)
	if f.ID == nil {
		return jsontext.Value("null")
	}
	id := *f.ID
	line := summary(entry)
	var whole jsonObject
	_ = json.Unmarshal(entry, &whole)
	h.fileStore(id).update(func(s *Store) { s.object = whole })
	var dropped []jsontext.Value
	h.index.update(func(s *Store) {
		list := []jsontext.Value{line}
		for _, e := range s.items {
			if g := fieldsOf(e); g.ID != nil && *g.ID == id {
				continue
			}
			list = append(list, e)
		}
		s.items, dropped = historyChat.tidy(list)
	})
	h.trash(dropped)
	return line
}

func (h *ChatHistory) remove(id string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.index.removeEntry(historyChat, id)
	if !safeID(id) {
		return
	}
	if store, ok := h.files[id]; ok {
		store.discard()
		delete(h.files, id)
	}
	removeFile(filepath.Join(h.dir, id+".json"))
}

// clear deletes every conversation file, in the folder and the trash, by
// name; nothing else is touched.
func (h *ChatHistory) clear() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for id, store := range h.files {
		store.discard()
		delete(h.files, id)
	}
	h.index.clearEntries()
	trash := filepath.Join(h.dir, chatTrash)
	for _, dir := range []string{h.dir, trash} {
		for _, name := range conversationFiles(dir) {
			removeFile(filepath.Join(dir, name))
		}
	}
	_ = os.Remove(trash) // only if empty: never a recursive delete
	if err := h.index.flush(); err != nil {
		log.Printf("textus: cannot write %s: %v", h.index.path, err)
	}
}

// moveDocument takes a document's conversations along when it moves, or
// those of every document under a moved folder. It reports whether any did.
func (h *ChatHistory) moveDocument(from, to string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	type move struct{ id, next string }
	var moves []move
	h.index.update(func(s *Store) {
		for i, e := range s.items {
			var line jsonObject
			if json.Unmarshal(e, &line) != nil {
				continue
			}
			var doc, id string
			if json.Unmarshal(line["docPath"], &doc) != nil || json.Unmarshal(line["id"], &id) != nil {
				continue
			}
			next, ok := movedPath(doc, from, to)
			if !ok {
				continue
			}
			line["docPath"] = mustJSON(next)
			s.items[i] = mustJSON(line)
			moves = append(moves, move{id, next})
		}
	})
	for _, m := range moves {
		if !safeID(m.id) {
			continue
		}
		store := h.fileStore(m.id)
		if v, ok := store.get("turns"); !ok || v == nil {
			delete(h.files, m.id) // gone: nothing to move, no stub made
			continue
		}
		store.update(func(s *Store) { s.object["docPath"] = mustJSON(m.next) })
	}
	return len(moves) > 0
}

// archived is how many conversations the trash holds.
func (h *ChatHistory) archived() int {
	return len(conversationFiles(filepath.Join(h.dir, chatTrash)))
}

func (h *ChatHistory) flush() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, store := range h.files {
		if err := store.flush(); err != nil {
			log.Printf("textus: cannot write %s: %v", store.path, err)
		}
	}
	if err := h.index.flush(); err != nil {
		log.Printf("textus: cannot write %s: %v", h.index.path, err)
	}
}

// trash moves the conversations trimmed off the index into .trash/.
func (h *ChatHistory) trash(dropped []jsontext.Value) {
	if len(dropped) == 0 {
		return
	}
	trash := filepath.Join(h.dir, chatTrash)
	if err := os.MkdirAll(trash, 0o755); err != nil {
		log.Printf("textus: cannot create %s: %v", trash, err)
		return
	}
	for _, e := range dropped {
		f := fieldsOf(e)
		if f.ID == nil || !safeID(*f.ID) {
			continue
		}
		id := *f.ID
		if store, ok := h.files[id]; ok {
			if err := store.flush(); err != nil {
				log.Printf("textus: cannot write %s.json: %v", id, err)
			}
			store.discard()
			delete(h.files, id)
		}
		name := id + ".json"
		if err := os.Rename(filepath.Join(h.dir, name), filepath.Join(trash, name)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			log.Printf("textus: cannot move %s to the trash: %v", name, err)
		}
	}
}

// rebuildLostIndex lists again the conversation files of an index that is
// missing, so they do not vanish from the list.
func (h *ChatHistory) rebuildLostIndex() {
	h.index.mu.Lock()
	listed := len(h.index.items) > 0
	h.index.mu.Unlock()
	if listed || h.index.unreadable {
		return
	}
	var lines []jsontext.Value
	for _, name := range conversationFiles(h.dir) {
		entry := readStoreFile(filepath.Join(h.dir, name), "entry", false)
		if entry != nil && historyChat.valid(entry) {
			lines = append(lines, summary(entry))
		}
	}
	if len(lines) == 0 {
		return
	}
	lines, _ = historyChat.tidy(lines)
	h.index.update(func(s *Store) { s.items = lines })
}

// search returns the conversations with every word of query in one of
// their messages or replies, newest first, at most limit.
func (h *ChatHistory) search(query string, limit int) []jsontext.Value {
	terms := strings.Fields(strings.ToLower(query))
	hits := []jsontext.Value{}
	if len(terms) == 0 {
		return hits
	}
	for _, id := range h.ids() {
		entry := h.get(id)
		if entry == nil {
			continue
		}
		if hit := searchEntry(entry, terms); hit != nil {
			hits = append(hits, hit)
			if len(hits) >= limit {
				break
			}
		}
	}
	return hits
}

func searchEntry(entry jsontext.Value, terms []string) jsontext.Value {
	f := fieldsOf(entry)
	for _, raw := range f.Turns {
		var turn struct {
			Kind string  `json:"kind"`
			Text *string `json:"text"`
		}
		if json.Unmarshal(raw, &turn) != nil || turn.Text == nil {
			continue
		}
		role := map[string]string{"User": "user", "Assistant": "assistant"}[turn.Kind]
		if role == "" {
			continue
		}
		lower := strings.ToLower(*turn.Text)
		if !slices.ContainsFunc(terms, func(t string) bool { return !strings.Contains(lower, t) }) {
			title := f.Title
			if title == nil {
				title = jsontext.Value(`""`)
			}
			return mustJSON(map[string]any{
				"id":        f.ID,
				"docPath":   f.DocPath,
				"title":     title,
				"updatedAt": f.UpdatedAt,
				"role":      role,
				"snippet":   excerptAround(*turn.Text, lower, terms[0]),
			})
		}
	}
	return nil
}

func conversationFiles(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if !e.Type().IsRegular() {
			continue
		}
		if id, ok := strings.CutSuffix(e.Name(), ".json"); ok && safeID(id) {
			names = append(names, e.Name())
		}
	}
	return names
}

func removeFile(path string) {
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		log.Printf("textus: cannot delete %s: %v", path, err)
	}
}
