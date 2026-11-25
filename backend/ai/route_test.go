package ai

import (
	"errors"
	"testing"

	"textus/backend/ai/aicompat"
)

func TestARouteHasWhatItsServerNeedsOrIsNotSetUp(t *testing.T) {
	keys := map[string]string{"openai": "sk-o", "deepseek": "sk-d"}
	creds := Credentials{
		Key: func(id string) (string, error) { return keys[id], nil },
		Endpoint: func(id string) (CustomEndpoint, bool) {
			if id == "custom:relay" {
				return CustomEndpoint{ID: id, BaseURL: "https://relay.example/v1", APIKey: ptr("sk-r"), Model: "relay-m"}, true
			}
			if id == "custom" {
				return CustomEndpoint{ID: id, BaseURL: "http://localhost:8080/v1", APIKey: ptr(" "), Model: "local-m"}, true
			}
			return CustomEndpoint{}, false
		},
	}
	key := func(r Route) string {
		if r.Key == nil {
			return "<none>"
		}
		return *r.Key
	}
	for _, c := range []struct {
		provider, model          string
		base, wantKey, wantModel string
	}{
		{"openai", "", "", "sk-o", ""},
		{"deepseek", "", "https://api.deepseek.com/v1", "sk-d", "deepseek-flash"},
		{"deepseek", " deepseek-pro ", "https://api.deepseek.com/v1", "sk-d", "deepseek-pro"},
		{"ollama", "llama4", "http://localhost:11434/v1", "<none>", "llama4"},
		{"custom:relay", "", "https://relay.example/v1", "sk-r", "relay-m"},
		{"custom:relay", "other-m", "https://relay.example/v1", "sk-r", "other-m"},
		{"custom", "", "http://localhost:8080/v1", "<none>", "local-m"},
	} {
		r, err := creds.Route(c.provider, c.model)
		if err != nil || r.BaseURL != c.base || key(r) != c.wantKey || r.Model != c.wantModel {
			t.Errorf("%s %q: %+v %s %v", c.provider, c.model, r, key(r), err)
		}
	}
	for _, provider := range []string{"anthropic", "google", "custom:gone"} {
		if _, err := creds.Route(provider, ""); err == nil || err.Error() != NotConfigured {
			t.Errorf("%s: %v", provider, err)
		}
	}
	// A local server needs no key, but a model.
	if _, err := creds.Route("ollama", ""); err == nil || err.Error() != NotConfigured {
		t.Errorf("ollama: %v", err)
	}
	if _, err := creds.Route("nope", ""); err == nil || err.Error() != "unknown provider: nope" {
		t.Error(err)
	}
	unreadable := Credentials{Key: func(string) (string, error) { return "", errors.New("keys unreadable") }}
	if _, err := unreadable.Route("openai", ""); err == nil || err.Error() != "keys unreadable" {
		t.Error(err)
	}
}

func TestAnAgentRequestRunsOnTheAgentDefaultUnlessAModelIsAsked(t *testing.T) {
	keys := map[string]string{"openai": "sk-o", "anthropic": "sk-a", "deepseek": "sk-d"}
	creds := Credentials{
		Key: func(id string) (string, error) { return keys[id], nil },
		Endpoint: func(id string) (CustomEndpoint, bool) {
			return CustomEndpoint{ID: id, BaseURL: "https://relay.example/v1", APIKey: ptr("sk-r"), Model: "relay-m"}, id == "custom:relay"
		},
	}
	for _, c := range []struct {
		provider, model string
		kind            aicompat.EndpointKind
		wantModel, key  string
	}{
		{"openai", "", aicompat.OpenAIKeyEndpoint, "gpt-6.1-sol", "sk-o"},
		{"openai", "gpt-6-luna", aicompat.OpenAIKeyEndpoint, "gpt-6-luna", "sk-o"},
		{"anthropic", "", aicompat.AnthropicEndpoint, "claude-sonnet-5-5", "sk-a"},
		{"deepseek", "", aicompat.ChatEndpoint, "deepseek-v4-pro", "sk-d"},
		{"ollama", "llama4", aicompat.ChatEndpoint, "llama4", ""},
		{"custom:relay", "", aicompat.ChatEndpoint, "relay-m", "sk-r"},
	} {
		p, ok, err := creds.AgentProvider(c.provider, c.model, aicompat.Deep, nil, nil)
		key := p.Endpoint.APIKey
		if c.kind == aicompat.AnthropicEndpoint {
			key = p.Endpoint.Anthropic.APIKey
		}
		if err != nil || !ok || p.Endpoint.Kind != c.kind || p.Model != c.wantModel || key != c.key || p.Thinking != aicompat.Deep {
			t.Errorf("%s %q: %+v %v", c.provider, c.model, p, err)
		}
	}
	if p, _, _ := creds.AgentProvider("deepseek", "", aicompat.Auto, nil, nil); p.Endpoint.Vendor != aicompat.DeepSeek || !p.Endpoint.StreamUsage || !p.FallsBackToPlainChat() {
		t.Errorf("%+v", p.Endpoint)
	}
	for _, provider := range []string{"google", "ollama", "custom:gone"} {
		if _, _, err := creds.AgentProvider(provider, "", aicompat.Auto, nil, nil); err == nil || err.Error() != NotConfigured {
			t.Errorf("%s: %v", provider, err)
		}
	}
	if CanThinkDeeper("openai", "") != aicompat.CanThinkDeeper("openai", "gpt-6.1-sol") {
		t.Error("the agent default isn't the model looked up")
	}
}
