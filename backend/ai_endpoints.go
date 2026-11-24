package backend

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"textus/backend/ai"
)

// The user's own OpenAI-compatible endpoints, kept in the config folder
// readable by its owner only, and the provider model lists Settings offers.

const (
	// endpointsChanged is sent to every window after a save or a removal,
	// so each reads the list anew (CHANGED_EVENT in src/ai/custom-endpoints.ts).
	endpointsChanged = "custom-endpoints-changed"
	// textusAIOff is what a request to an AI service is refused with while
	// Textus AI is switched off. The frontend checks too; this is the backstop.
	textusAIOff = "textus-ai-off"
	// modelListTimeout bounds one model listing.
	modelListTimeout = 30 * time.Second
)

// ensureAIOn refuses what would send something to an AI service while
// Textus AI is off.
func (a *App) ensureAIOn() error {
	if !a.readFlag("textusAi") {
		return errors.New(textusAIOff)
	}
	return nil
}

// CustomEndpointStatus is what the settings page is told about one saved
// endpoint: never the key, only whether one is stored
// (src/platform/bindings/auth.ts CustomEndpointStatus).
type CustomEndpointStatus struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	BaseURL   string `json:"base_url"`
	HasAPIKey bool   `json:"has_api_key"`
	Model     string `json:"model"`
}

func (a *App) endpointsPath() string { return filepath.Join(a.dirs.Config, ai.EndpointsFile) }

// endpointsMu makes each change one step: two saves at once would each
// drop the other's endpoint, and its key.
var endpointsMu sync.Mutex

// customEndpoints are the saved endpoints; a file that doesn't parse is
// moved aside, so the endpoints are entered again rather than stuck.
func (a *App) customEndpoints() []ai.CustomEndpoint {
	raw, err := os.ReadFile(a.endpointsPath())
	if err != nil {
		return nil
	}
	endpoints, ok := ai.ParseEndpoints(raw)
	if !ok {
		moveAside(a.endpointsPath())
	}
	return endpoints
}

func (a *App) customEndpoint(id string) (ai.CustomEndpoint, bool) {
	for _, e := range a.customEndpoints() {
		if e.ID == id {
			return e, true
		}
	}
	return ai.CustomEndpoint{}, false
}

// changeEndpoints reads the endpoints, changes them and writes them back
// (removing the file when none is left), as one step.
func (a *App) changeEndpoints(change func([]ai.CustomEndpoint) []ai.CustomEndpoint) error {
	endpointsMu.Lock()
	defer endpointsMu.Unlock()
	endpoints := change(a.customEndpoints())
	path := a.endpointsPath()
	if len(endpoints) == 0 {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	raw, err := ai.EndpointsJSON(endpoints)
	if err != nil {
		return err
	}
	if err := writeFileAtomic(path, raw); err != nil {
		return err
	}
	return os.Chmod(path, 0o600)
}

// agentModels is the live model list for the agent model picker; a custom
// endpoint has fetch_custom_models instead.
func (a *App) agentModels(c *Call, provider string) ([]string, error) {
	entry, ok := ai.Find(provider)
	if !ok {
		return nil, fmt.Errorf("unknown provider: %s", provider)
	}
	client := netClient(modelListTimeout)
	switch {
	case entry.Dialect == "anthropic-messages":
		key, err := a.providerKey(entry.ID)
		if err != nil {
			return nil, err
		}
		if key == "" {
			return nil, errors.New(ai.NotConfigured)
		}
		return ai.ListAnthropicModels(c.ctx, client, ai.AnthropicAuth{APIKey: key})
	// A ChatGPT sign-in's token can't list models: only the API key can.
	case entry.Dialect == "openai-responses":
		key, err := a.providerKey(entry.ID)
		if err != nil {
			return nil, err
		}
		if key == "" {
			return nil, errors.New(ai.NotConfigured)
		}
		return ai.ListModels(c.ctx, client, "https://api.openai.com/v1", &key)
	case entry.Dialect == "openai-chat-completions" && entry.BaseURL != nil:
		key, err := a.providerKey(entry.ID)
		if err != nil {
			return nil, err
		}
		var k *string
		if key != "" {
			k = &key
		}
		return ai.ListModels(c.ctx, client, *entry.BaseURL, k)
	}
	return nil, fmt.Errorf("models can't be listed for provider: %s", provider)
}

func init() {
	command("custom_endpoints_status", func(c *Call, _ none) ([]CustomEndpointStatus, error) {
		out := []CustomEndpointStatus{}
		for _, e := range c.app.customEndpoints() {
			out = append(out, CustomEndpointStatus{
				ID: e.ID, Name: e.Name, BaseURL: e.BaseURL, Model: e.Model,
				HasAPIKey: e.APIKey != nil && *e.APIKey != "",
			})
		}
		return out, nil
	})
	// Saves an endpoint (id null: a new one) and answers its id. A missing
	// key keeps the stored one while the URL stays the same; for another
	// URL the old key is dropped rather than sent to the new host.
	command("set_custom_endpoint", func(c *Call, a struct {
		ID      *string `json:"id"`
		Name    string  `json:"name"`
		BaseURL string  `json:"baseUrl"`
		APIKey  *string `json:"apiKey"`
		Model   string  `json:"model"`
	}) (string, error) {
		edit := ai.EndpointEdit{Name: a.Name, BaseURL: a.BaseURL, APIKey: a.APIKey, Model: a.Model}
		if a.ID != nil {
			edit.ID = *a.ID
		}
		var id string
		err := c.app.changeEndpoints(func(endpoints []ai.CustomEndpoint) []ai.CustomEndpoint {
			endpoints, id = ai.SaveEndpoint(endpoints, edit, ai.NewCustomID)
			return endpoints
		})
		if err != nil {
			return "", err
		}
		c.app.emitAll(endpointsChanged, nil)
		return id, nil
	})
	command("clear_custom_endpoint", func(c *Call, a struct {
		ID string `json:"id"`
	}) (nothing, error) {
		err := c.app.changeEndpoints(func(endpoints []ai.CustomEndpoint) []ai.CustomEndpoint {
			var kept []ai.CustomEndpoint
			for _, e := range endpoints {
				if e.ID != a.ID {
					kept = append(kept, e)
				}
			}
			return kept
		})
		if err == nil {
			c.app.emitAll(endpointsChanged, nil)
		}
		return nil, err
	})
	// The models baseUrl lists, asked with the key typed, or else the stored
	// key of endpoint id, for the URL it was saved with only.
	command("fetch_custom_models", func(c *Call, a struct {
		ID      *string `json:"id"`
		BaseURL string  `json:"baseUrl"`
		APIKey  *string `json:"apiKey"`
	}) ([]string, error) {
		if err := c.app.ensureAIOn(); err != nil {
			return nil, err
		}
		var stored *ai.CustomEndpoint
		if a.ID != nil {
			if e, ok := c.app.customEndpoint(*a.ID); ok {
				stored = &e
			}
		}
		key := ai.ResolveEndpointKey(stored, a.BaseURL, a.APIKey)
		return ai.ListModels(c.ctx, netClient(modelListTimeout), a.BaseURL, key)
	})
	command("fetch_agent_models", func(c *Call, a struct {
		Provider string `json:"provider"`
	}) ([]string, error) {
		if err := c.app.ensureAIOn(); err != nil {
			return nil, err
		}
		return c.app.agentModels(c, a.Provider)
	})
}
