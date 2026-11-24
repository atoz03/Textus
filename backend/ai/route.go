package ai

import (
	"errors"
	"fmt"
	"strings"
)

// Credentials look up what a route needs: a provider's stored key ("" for
// none) and a custom endpoint by its own id.
type Credentials struct {
	Key      func(provider string) (string, error)
	Endpoint func(id string) (CustomEndpoint, bool)
}

// Route resolves provider (a catalog id, or a custom endpoint's own id)
// and model ("" for the provider's default) to where one request goes. A
// ChatGPT or Claude sign-in is not in this build: OpenAI and Anthropic are
// reached by API key.
func (c Credentials) Route(provider, model string) (Route, error) {
	entry, ok := Find(provider)
	if !ok {
		return Route{}, fmt.Errorf("unknown provider: %s", provider)
	}
	r := Route{Provider: entry, Model: strings.TrimSpace(model)}
	switch entry.Dialect {
	case "openai-responses", "anthropic-messages":
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
