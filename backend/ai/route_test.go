package ai

import (
	"errors"
	"testing"
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
