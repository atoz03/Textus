package ai

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json/v2"
	"strings"
)

// The user's own OpenAI-compatible endpoints (relays, proxies, services the
// catalog doesn't list), as kept in custom_endpoint.json, and the rules for
// changing them. Each is a provider of its own, by id.

const (
	// firstCustomID is the id the one endpoint before the list had, and so
	// still the id of the endpoint migrated from it: settings naming it hold.
	firstCustomID = "custom"
	// customIDPrefix starts every other endpoint's id.
	customIDPrefix = "custom:"
	// EndpointsFile is the file, in the app's config folder.
	EndpointsFile = "custom_endpoint.json"
)

// IsCustomID is whether a provider id names one of the user's endpoints
// (src/ai/custom-endpoints.ts isCustomId).
func IsCustomID(id string) bool {
	return id == firstCustomID || strings.HasPrefix(id, customIDPrefix)
}

// NewCustomID is a fresh provider id for a new endpoint.
func NewCustomID() string {
	b := make([]byte, 4)
	rand.Read(b)
	return customIDPrefix + hex.EncodeToString(b)
}

// CustomEndpoint is one saved endpoint, as the file keeps it.
type CustomEndpoint struct {
	// ID is its provider id, as Settings.aiProvider names it.
	ID string `json:"id"`
	// Name is what the user called it; empty shows as "Custom endpoint".
	Name    string  `json:"name"`
	BaseURL string  `json:"base_url"`
	APIKey  *string `json:"api_key"`
	Model   string  `json:"model"`
}

// ParseEndpoints reads the file in either shape it has had: the list, or
// the single endpoint before it, whose key and model carry over. ok is
// false for a file that is neither.
func ParseEndpoints(raw []byte) (endpoints []CustomEndpoint, ok bool) {
	var list struct {
		Endpoints *[]CustomEndpoint `json:"endpoints"`
	}
	if json.Unmarshal(raw, &list) == nil && list.Endpoints != nil {
		return *list.Endpoints, true
	}
	var legacy struct {
		BaseURL *string `json:"base_url"`
		APIKey  *string `json:"api_key"`
		Model   *string `json:"model"`
	}
	if json.Unmarshal(raw, &legacy) != nil || legacy.BaseURL == nil || legacy.Model == nil {
		return nil, false
	}
	return []CustomEndpoint{{ID: firstCustomID, BaseURL: *legacy.BaseURL, APIKey: legacy.APIKey, Model: *legacy.Model}}, true
}

// EndpointsJSON is the file's text for endpoints.
func EndpointsJSON(endpoints []CustomEndpoint) ([]byte, error) {
	if endpoints == nil {
		endpoints = []CustomEndpoint{}
	}
	return json.Marshal(map[string][]CustomEndpoint{"endpoints": endpoints})
}

// EndpointEdit is what the settings page asks to save; ID "" is a new
// endpoint.
type EndpointEdit struct {
	ID, Name, BaseURL, Model string
	APIKey                   *string
}

// SaveEndpoint is endpoints with edit saved over its endpoint, or added as
// a new one with an id from fresh; and its id. An unknown id is added
// rather than lost.
func SaveEndpoint(endpoints []CustomEndpoint, edit EndpointEdit, fresh func() string) ([]CustomEndpoint, string) {
	at := -1
	for i, e := range endpoints {
		if edit.ID != "" && e.ID == edit.ID {
			at = i
		}
	}
	var stored *CustomEndpoint
	if at >= 0 {
		stored = &endpoints[at]
	}
	id := edit.ID
	if id == "" {
		id = fresh()
	}
	endpoint := CustomEndpoint{
		ID:      id,
		Name:    strings.TrimSpace(edit.Name),
		BaseURL: strings.TrimSpace(edit.BaseURL),
		APIKey:  ResolveEndpointKey(stored, edit.BaseURL, edit.APIKey),
		Model:   strings.TrimSpace(edit.Model),
	}
	out := append([]CustomEndpoint(nil), endpoints...)
	if at >= 0 {
		out[at] = endpoint
	} else {
		out = append(out, endpoint)
	}
	return out, id
}

// ResolveEndpointKey is the key typed, else the endpoint's stored one, but
// only for the URL it was saved with, so a new host never gets the old key.
func ResolveEndpointKey(stored *CustomEndpoint, baseURL string, typed *string) *string {
	if typed != nil && strings.TrimSpace(*typed) != "" {
		return typed
	}
	if stored != nil && sameEndpoint(stored.BaseURL, baseURL) {
		return stored.APIKey
	}
	return nil
}

func sameEndpoint(a, b string) bool {
	return strings.TrimRight(strings.TrimSpace(a), "/") == strings.TrimRight(strings.TrimSpace(b), "/")
}
