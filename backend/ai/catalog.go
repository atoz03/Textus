// Package ai is Textus AI's side of the backend that knows providers: the
// catalog, how each one takes a key, and the requests that differ by
// provider. The commands that use it are backend/ai_*.go.
package ai

import (
	_ "embed"
	"encoding/json/v2"
	"fmt"

	"textus/backend/ai/aicompat"
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

// paperAtOnce is how many model calls a paper's run makes at once, by
// provider: what the service's usual rate limits bear, or a local
// server's slots. Kept out of catalog.json, which is the frontend's.
var paperAtOnce = map[string]int{
	"openai": 6, "anthropic": 4, "google": 6, "deepseek": 6, "xai": 4, "mistral": 3,
	"groq": 3, "openrouter": 4, "opencode-go": 4, "moonshot": 3, "zhipu": 3, "qwen": 4,
	"doubao": 4, "minimax": 4, "minimax-intl": 4, "ollama": 2, "custom": 3,
}

// PaperAtOnce is how many model calls a paper's run makes at once with
// this provider.
func (p Provider) PaperAtOnce() int {
	if n, ok := paperAtOnce[p.ID]; ok {
		return n
	}
	return 3
}

// NotConfigured is what a request to a provider that isn't set up fails
// with.
const NotConfigured = "This provider isn't set up yet - configure it in Settings > Textus AI."

// Vendor is which server is behind the chat-completions dialect, where one
// differs from OpenAI's enough to matter (aicompat's).
type Vendor = aicompat.Vendor

const (
	Generic = aicompat.Generic
	// Ark is Volcengine Ark (Doubao), whose Seed models think by default.
	Ark = aicompat.Ark
	// DeepSeek's models think by default, at effort high.
	DeepSeek = aicompat.DeepSeek
	// MiniMax writes its thinking into the answer unless asked not to.
	MiniMax = aicompat.MiniMax
)

// ChatVendor is the server a chat-completions base URL points at: by host
// rather than catalog id, so a custom endpoint with an Ark URL is Ark too.
func ChatVendor(baseURL string) Vendor { return aicompat.ChatVendor(baseURL) }
