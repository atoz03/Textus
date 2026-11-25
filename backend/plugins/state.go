package plugins

import (
	"encoding/json/v2"
	"slices"
)

// plugins/state.json: the switch for every plugin, and per plugin whether
// it is on, its granted permissions and any developer folder. Only
// Settings writes it; a plugin can't reach the commands that do.

// State is plugins/state.json (PluginsState in
// src/platform/bindings/plugins.ts).
type State struct {
	// AllOn is Settings › Plugins › Plugins: off stops every plugin at once.
	AllOn   bool              `json:"allOn"`
	Plugins map[string]*Entry `json:"plugins"`
}

// Entry is one plugin's line in State (PluginEntry in
// src/platform/bindings/plugins.ts).
type Entry struct {
	Enabled bool `json:"enabled"`
	// Granted are permissions as the manifest names them: "files.read",
	// "network:api.zotero.org".
	Granted []string `json:"granted"`
	// DevPath is the folder of a plugin loaded in developer mode.
	DevPath string `json:"devPath,omitempty"`
	// Official: installed from the catalog as CatVinci's own, which
	// Settings marks. Never set by the window.
	Official bool `json:"official,omitempty"`
}

// NewState is the state with no file yet: every plugin off, the switch on.
func NewState() State { return State{AllOn: true, Plugins: map[string]*Entry{}} }

// ParseState reads state.json; a field left out keeps its default.
func ParseState(raw []byte) (State, error) {
	var s struct {
		AllOn   *bool             `json:"allOn"`
		Plugins map[string]*Entry `json:"plugins"`
	}
	if err := json.Unmarshal(raw, &s); err != nil {
		return NewState(), err
	}
	out := NewState()
	if s.AllOn != nil {
		out.AllOn = *s.AllOn
	}
	for id, e := range s.Plugins {
		if e == nil {
			e = &Entry{}
		}
		if e.Granted == nil {
			e.Granted = []string{}
		}
		out.Plugins[id] = e
	}
	return out, nil
}

// Entry is plugin id's entry, made when it has none.
func (s *State) Entry(id string) *Entry {
	if s.Plugins == nil {
		s.Plugins = map[string]*Entry{}
	}
	e := s.Plugins[id]
	if e == nil {
		e = &Entry{Granted: []string{}}
		s.Plugins[id] = e
	}
	return e
}

// Allows is whether id is on and was granted permission - what every
// privileged call checks first. Writing files includes reading them, as
// the window's own check (api-permissions.ts grants) has it.
func (s State) Allows(id, permission string) bool {
	e := s.Plugins[id]
	if !s.AllOn || e == nil || !e.Enabled {
		return false
	}
	for _, g := range e.Granted {
		if g == permission || permission == "files.read" && g == "files.write" {
			return true
		}
	}
	return false
}

// Runs is whether id is on at all.
func (s State) Runs(id string) bool {
	e := s.Plugins[id]
	return s.AllOn && e != nil && e.Enabled
}

// Granted is what id was granted, nil for a plugin with no entry.
func (s State) Granted(id string) []string {
	if e := s.Plugins[id]; e != nil {
		return e.Granted
	}
	return nil
}

// Running is nil when id is a plugin that is on, else why it can't call.
func (s State) Running(id string) error {
	if err := RequireID(id); err != nil {
		return err
	}
	if !s.Runs(id) {
		return TurnedOff(id)
	}
	return nil
}

// OnlyDeclared keeps of granted what the manifest asks for: only that can
// be granted, whoever calls.
func OnlyDeclared(granted, declared []string) []string {
	out := []string{}
	for _, g := range granted {
		if slices.Contains(declared, g) {
			out = append(out, g)
		}
	}
	return out
}
