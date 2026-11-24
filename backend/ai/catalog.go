// Package ai is Textus AI's side of the backend that knows providers: the
// catalog, how each one takes a key, and the requests that differ by
// provider. The commands that use it are backend/ai_*.go.
package ai

import (
	_ "embed"
	"encoding/json/v2"
	"fmt"
	"strings"
)

// Provider is one supported AI service, as data. catalog.json is
// PROVIDER_CATALOG of src/platform/bindings/ai.ts, which a test keeps the
// same.
type Provider struct {
	// ID matches Settings.aiProvider and the provider every ai_* command
	// dispatches on.
	ID    string `json:"id"`
	Label string `json:"label"`
	// Dialect is the wire format: "openai-responses", "anthropic-messages"
	// or "openai-chat-completions".
	Dialect string `json:"dialect"`
	// Auth is the sign-in methods in preference order: "oauth", "api_key",
	// "custom" (the user's own endpoint).
	Auth []string `json:"auth"`
	// BaseURL is a chat-completions provider's fixed endpoint; none where
	// the dialect implies it or the user gives it.
	BaseURL           *string  `json:"baseUrl"`
	DefaultModel      *string  `json:"defaultModel"`
	AgentDefaultModel *string  `json:"agentDefaultModel"`
	KeyOptional       bool     `json:"keyOptional"`
	OAuthUnofficial   bool     `json:"oauthUnofficial"`
	SupportsVision    bool     `json:"supportsVision"`
	KnownModels       []string `json:"knownModels"`
}

//go:embed catalog.json
var catalogJSON []byte

// Catalog is every provider, in the order Settings lists them.
var Catalog = func() []Provider {
	var list []Provider
	if err := json.Unmarshal(catalogJSON, &list); err != nil {
		panic(fmt.Sprintf("ai/catalog.json: %v", err))
	}
	return list
}()

// Find is the catalog's entry for a provider id; a custom endpoint's id
// ("custom:…") finds the custom entry.
func Find(id string) (Provider, bool) {
	if IsCustomID(id) {
		id = firstCustomID
	}
	for _, p := range Catalog {
		if p.ID == id {
			return p, true
		}
	}
	return Provider{}, false
}

// NotConfigured is what a request to a provider that isn't set up fails
// with.
const NotConfigured = "This provider isn't set up yet - configure it in Settings > Textus AI."

// Vendor is which server is behind the chat-completions dialect, where one
// differs from OpenAI's enough to matter.
type Vendor uint8

const (
	Generic Vendor = iota
	// Ark is Volcengine Ark (Doubao), whose Seed models think by default.
	Ark
	// DeepSeek's models think by default, at effort high.
	DeepSeek
	// MiniMax writes its thinking into the answer unless asked not to.
	MiniMax
)

// ChatVendor is the server a chat-completions base URL points at: by host
// rather than catalog id, so a custom endpoint with an Ark URL is Ark too.
func ChatVendor(baseURL string) Vendor {
	switch {
	case strings.Contains(baseURL, ".volces.com"):
		return Ark
	case strings.Contains(baseURL, "api.deepseek.com"):
		return DeepSeek
	case strings.Contains(baseURL, "api.minimaxi.com"), strings.Contains(baseURL, "api.minimax.io"):
		return MiniMax
	}
	return Generic
}
