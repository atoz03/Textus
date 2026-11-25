package ai

import (
	"cmp"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"textus/backend/ai/aicompat"
)

// Originator names the app to OpenAI on a ChatGPT sign-in's requests.
const Originator = "textus"

// Credentials look up what a route needs: a provider's stored key ("" for
// none), a custom endpoint by its own id, and the ChatGPT ("openai") or
// Claude ("anthropic") sign-in, valid for a request - false when not
// signed in. SignIn may be nil: no sign-in.
type Credentials struct {
	Key      func(provider string) (string, error)
	Endpoint func(id string) (CustomEndpoint, bool)
	SignIn   func(provider string) (aicompat.SignIn, bool, error)
}

// signInNames name the sign-ins in their errors.
var signInNames = map[string]string{"openai": "ChatGPT", "anthropic": "Claude"}

// Route resolves provider (a catalog id, or a custom endpoint's own id)
// and model ("" for the provider's default) to where one request goes. A
// ChatGPT or Claude sign-in is preferred to a key when both are set up:
// it's the richer entitlement, and what a user who did both probably
// wants.
func (c Credentials) Route(provider, model string) (Route, error) {
	entry, ok := Find(provider)
	if !ok {
		return Route{}, fmt.Errorf("unknown provider: %s", provider)
	}
	r := Route{Provider: entry, Model: strings.TrimSpace(model)}
	switch entry.Dialect {
	case "openai-responses", "anthropic-messages":
		if c.SignIn != nil {
			signIn, ok, err := c.SignIn(entry.ID)
			// Signed in but its refresh failed: said as it is, not quietly
			// swapped for a pasted key (another account) or "not set up".
			if err != nil {
				return Route{}, fmt.Errorf("%s sign-in: %v", signInNames[entry.ID], err)
			}
			if ok {
				r.SignIn = &signIn
				return r, nil
			}
		}
		key, err := c.Key(entry.ID)
		if err != nil {
			return Route{}, err
		}
		if key == "" {
			return Route{}, errors.New(NotConfigured)
		}
		r.Key = &key
		return r, nil
	case "openai-chat-completions":
		return c.chatRoute(provider, entry, r)
	}
	return Route{}, fmt.Errorf("unhandled dialect: %s", entry.Dialect)
}

// chatRoute is a chat-completions provider's route: the catalog's server
// with the stored key, or the user's own endpoint, found by its id.
func (c Credentials) chatRoute(provider string, entry Provider, r Route) (Route, error) {
	defaultModel := ""
	if entry.BaseURL == nil {
		e, ok := c.Endpoint(provider)
		if !ok {
			return Route{}, errors.New(NotConfigured)
		}
		r.BaseURL, defaultModel = e.BaseURL, e.Model
		if e.APIKey != nil && strings.TrimSpace(*e.APIKey) != "" {
			r.Key = e.APIKey
		}
	} else {
		key, err := c.Key(entry.ID)
		if err != nil {
			return Route{}, err
		}
		if key == "" && !entry.KeyOptional {
			return Route{}, errors.New(NotConfigured)
		}
		if key != "" {
			r.Key = &key
		}
		r.BaseURL = *entry.BaseURL
		if entry.DefaultModel != nil {
			defaultModel = *entry.DefaultModel
		}
	}
	if r.Model == "" {
		r.Model = defaultModel
	}
	if r.Model == "" {
		return Route{}, errors.New(NotConfigured)
	}
	return r, nil
}

// AgentProvider is the provider, credentials and model an agent request
// runs on (route::agent_provider); false for a dialect without tool
// calling, which the chat answers in one plain completion. model "" is the
// provider's agent default; native is the provider's own web search, nil
// for none. client sends the steps: it has no total timeout.
func (c Credentials) AgentProvider(provider, model string, thinking aicompat.ThinkingLevel, native *aicompat.NativeSearch, client *http.Client) (aicompat.AgentProvider, bool, error) {
	entry, ok := Find(provider)
	if !ok {
		return aicompat.AgentProvider{}, false, fmt.Errorf("unknown provider: %s", provider)
	}
	requested := strings.TrimSpace(model)
	agentDefault := ""
	if entry.AgentDefaultModel != nil {
		agentDefault = *entry.AgentDefaultModel
	}
	model = cmp.Or(requested, agentDefault)
	var endpoint aicompat.Endpoint
	switch entry.Dialect {
	case "openai-responses", "anthropic-messages":
		r, err := c.Route(provider, model)
		if err != nil {
			return aicompat.AgentProvider{}, false, err
		}
		switch {
		case entry.Dialect == "anthropic-messages":
			auth := aicompat.AnthropicAuth{APIKey: r.key()}
			if r.SignIn != nil {
				auth = aicompat.AnthropicAuth{OAuthToken: r.SignIn.Access}
			}
			endpoint = aicompat.Endpoint{Kind: aicompat.AnthropicEndpoint, Anthropic: auth}
			model = cmp.Or(model, AnthropicCompletionModel)
		case r.SignIn != nil:
			endpoint = aicompat.Endpoint{Kind: aicompat.CodexEndpoint, AccessToken: r.SignIn.Access, AccountID: r.SignIn.AccountID, Originator: Originator}
			// Clamped, not just defaulted: a model saved while the API key
			// was in use is a hard 400 here.
			model = aicompat.CodexUsableModel(requested, cmp.Or(agentDefault, aicompat.CodexCompletionModel))
		default:
			endpoint = aicompat.Endpoint{Kind: aicompat.OpenAIKeyEndpoint, APIKey: r.key()}
			model = cmp.Or(model, OpenAICompletionModel)
		}
	case "openai-chat-completions":
		// The model asked, else the agent default, else the server's own.
		r, err := c.Route(provider, model)
		if err != nil {
			return aicompat.AgentProvider{}, false, err
		}
		model = r.Model
		endpoint = aicompat.Endpoint{
			Kind: aicompat.ChatEndpoint, APIKey: r.key(), BaseURL: r.BaseURL,
			Vendor: ChatVendor(r.BaseURL), StreamUsage: aicompat.Provider(entry.ID).StreamUsage,
		}
	default:
		return aicompat.AgentProvider{}, false, nil
	}
	return aicompat.AgentProvider{
		ID: entry.ID, Model: model, Endpoint: endpoint, Thinking: thinking, NativeSearch: native, Client: client,
	}, true, nil
}

// CanThinkDeeper reports whether the chat composer offers Deep thinking
// for provider and its Agent model ("" is the provider's agent default).
// With no model to look up - a custom endpoint's comes from its settings -
// the table answers for one it doesn't know.
func CanThinkDeeper(provider, model string) bool {
	if model == "" {
		if entry, ok := Find(provider); ok && entry.AgentDefaultModel != nil {
			model = *entry.AgentDefaultModel
		}
	}
	return aicompat.CanThinkDeeper(provider, model)
}
