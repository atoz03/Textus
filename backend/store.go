package backend

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"io/fs"
	"log"
	"os"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"
)

// schemaVersion marks the store files this build writes.
const schemaVersion = 1

// flushDelay is how long a change waits before its store is written, so a
// burst of changes is one write.
var flushDelay = 250 * time.Millisecond

// jsonObject is a JSON object whose values are kept as they were written.
type jsonObject = map[string]jsontext.Value

// Store is one JSON file of the app's own: {"schemaVersion": 1, "<key>": payload}.
// The payload is an object (settings, the UI state) or a list (histories).
// Changes are kept in memory at once and written a moment later, whole.
type Store struct {
	path   string
	key    string
	pretty bool
	list   bool // the payload is a list, else an object

	mu     sync.Mutex
	object jsonObject
	items  []jsontext.Value
	rev    uint64
	schema uint64
	dirty  bool

	flushScheduled atomic.Bool
	// missingAtLoad: there was no file; for settings.json, a new profile.
	missingAtLoad bool
	// unreadable: the file is there but could not be read: it probably holds
	// the user's data, so nothing is written this session.
	unreadable bool
}

// loadStore reads path, or starts empty when it is missing. A file that
// does not parse is moved aside, and the store starts empty.
func loadStore(path, key string, list, pretty bool) *Store {
	s := &Store{path: path, key: key, list: list, pretty: pretty, rev: 1, schema: schemaVersion}
	s.reset()
	raw, err := os.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		s.missingAtLoad = true
	case err != nil:
		log.Printf("textus: cannot read %s: %v; using defaults for this session and leaving the file alone", path, err)
		s.unreadable = true
	case !utf8.Valid(raw) || !s.parse(raw):
		moveAside(path)
	}
	return s
}

func (s *Store) reset() {
	if s.list {
		s.items = []jsontext.Value{}
	} else {
		s.object = jsonObject{}
	}
}

// parse takes the payload out of a file's text; false when it is not this
// store's file.
func (s *Store) parse(raw []byte) bool {
	var file map[string]jsontext.Value
	if err := json.Unmarshal(raw, &file); err != nil {
		return false
	}
	payload, ok := file[s.key]
	if !ok {
		return false
	}
	if s.list {
		var items []jsontext.Value
		if payload.Kind() != '[' || json.Unmarshal(payload, &items) != nil {
			return false
		}
		s.items = items
	} else {
		var object jsonObject
		if payload.Kind() != '{' || json.Unmarshal(payload, &object) != nil {
			return false
		}
		s.object = object
	}
	// A file without a marker was written by hand: read as this version.
	var schema uint64
	if v, ok := file["schemaVersion"]; ok && json.Unmarshal(v, &schema) == nil {
		s.schema = schema
	}
	return true
}

// snapshot is the payload as JSON, and its revision.
func (s *Store) snapshot() (jsontext.Value, uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.encodePayload(), s.rev
}

func (s *Store) encodePayload() jsontext.Value {
	var v any = s.object
	if s.list {
		v = s.items
	}
	out, err := json.Marshal(v, json.Deterministic(true))
	if err != nil {
		return jsontext.Value("null")
	}
	return out
}

// get returns one field of an object store.
func (s *Store) get(key string) (jsontext.Value, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.object[key]
	return v, ok
}

// update changes the payload under the lock, bumps the revision and
// schedules a write. It returns the new revision.
func (s *Store) update(change func(s *Store)) uint64 {
	s.mu.Lock()
	change(s)
	s.rev++
	s.dirty = true
	rev := s.rev
	s.mu.Unlock()
	s.scheduleFlush()
	return rev
}

func (s *Store) scheduleFlush() {
	if s.unreadable || s.flushScheduled.Swap(true) {
		return
	}
	go func() {
		time.Sleep(flushDelay)
		// Cleared before writing: a change landing mid-write schedules
		// another write instead of being left unwritten.
		s.flushScheduled.Store(false)
		if err := s.flush(); err != nil {
			log.Printf("textus: cannot write %s: %v", s.path, err)
		}
	}()
}

// flush writes the file now if anything changed, never over an unreadable one.
func (s *Store) flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.dirty || s.unreadable {
		return nil
	}
	if err := s.write(); err != nil {
		return err
	}
	s.dirty = false
	return nil
}

func (s *Store) write() error {
	file := jsonObject{
		"schemaVersion": mustJSON(s.schema),
		s.key:           s.encodePayload(),
	}
	opts := []json.Options{json.Deterministic(true)}
	if s.pretty {
		opts = append(opts, jsontext.WithIndent("  "))
	}
	text, err := json.Marshal(file, opts...)
	if err != nil {
		return err
	}
	return writeFileAtomic(s.path, text)
}

// discard drops a change still waiting to be written, so the caller can
// move or delete the file without a write bringing it back.
func (s *Store) discard() {
	s.mu.Lock()
	s.dirty = false
	s.mu.Unlock()
}

// patch merges top-level fields: each replaces that field whole, and null
// removes it, so two windows can change different settings at once.
func (s *Store) patch(patch jsonObject) uint64 {
	return s.update(func(s *Store) { mergeTopLevel(s.object, patch) })
}

// fillMissing sets the fields of defaults the store lacks, under one lock.
// It returns what it added and the new revision, or nil when nothing was
// missing.
func (s *Store) fillMissing(defaults jsonObject) (jsonObject, uint64) {
	s.mu.Lock()
	missing := jsonObject{}
	for key, value := range defaults {
		if value.Kind() == 'n' {
			continue
		}
		if _, ok := s.object[key]; !ok {
			missing[key] = value
		}
	}
	if len(missing) == 0 {
		s.mu.Unlock()
		return nil, 0
	}
	mergeTopLevel(s.object, missing)
	s.rev++
	s.dirty = true
	rev := s.rev
	s.mu.Unlock()
	s.scheduleFlush()
	return missing, rev
}

func mergeTopLevel(object jsonObject, patch jsonObject) {
	for key, value := range patch {
		if len(value) == 0 || value.Kind() == 'n' {
			delete(object, key)
		} else {
			object[key] = value
		}
	}
}

// mustJSON encodes a value that always encodes.
func mustJSON(v any) jsontext.Value {
	out, err := json.Marshal(v, json.Deterministic(true))
	if err != nil {
		panic(err)
	}
	return out
}

// readStoreFile is the payload of a store file, or nil.
func readStoreFile(path, key string, list bool) jsontext.Value {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	s := &Store{key: key, list: list}
	s.reset()
	if !s.parse(raw) {
		return nil
	}
	return s.encodePayload()
}
