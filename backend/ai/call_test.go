package ai

import (
	"context"
	"encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

// sent is what one request carried, as the server saw it.
type sent struct {
	host, path string
	header     http.Header
	body       map[string]any
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// serve answers every request, whatever host it is for, from one test
// server, with status and answer; got is what each request carried.
func serve(t *testing.T, status int, answer string) (client *http.Client, got *[]sent) {
	got = &[]sent{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.UnmarshalRead(r.Body, &body)
		*got = append(*got, sent{r.Host, r.URL.Path, r.Header.Clone(), body})
		w.WriteHeader(status)
		w.Write([]byte(answer))
	}))
	t.Cleanup(server.Close)
	target, _ := url.Parse(server.URL)
	return &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		r = r.Clone(r.Context())
		r.URL.Scheme, r.URL.Host = target.Scheme, target.Host
		return http.DefaultTransport.RoundTrip(r)
	})}, got
}

// at is the value at a path of keys and indexes in a request's body.
func at(v any, path ...any) any {
	for _, step := range path {
		switch s := step.(type) {
		case string:
			m, _ := v.(map[string]any)
			v = m[s]
		case int:
			l, _ := v.([]any)
			if s >= len(l) {
				return nil
			}
			v = l[s]
		}
	}
	return v
}

func route(provider, model string, key *string) Route {
	p, _ := Find(provider)
	return Route{Provider: p, Model: model, Key: key}
}

func TestOpenAIIsAskedForItsQuickestAnswer(t *testing.T) {
	client, got := serve(t, 200, `{"output":[{"type":"reasoning","content":[]},{"type":"message","content":[{"type":"output_text","text":"Hello"}]}]}`)
	text, err := Complete(context.Background(), client, route("openai", "", ptr("sk-o")), "Be brief.", "Hi")
	if err != nil || text != "Hello" {
		t.Fatal(text, err)
	}
	r := (*got)[0]
	if r.host != "api.openai.com" || r.path != "/v1/responses" || r.header.Get("Authorization") != "Bearer sk-o" {
		t.Errorf("%s %s %v", r.host, r.path, r.header)
	}
	if at(r.body, "model") != OpenAICompletionModel || at(r.body, "store") != false || at(r.body, "instructions") != "Be brief." ||
		at(r.body, "input", 0, "content", 0, "text") != "Hi" || at(r.body, "reasoning", "effort") != "none" || at(r.body, "text") != nil {
		t.Errorf("%v", r.body)
	}
	// Verbosity for GPT-5, the lowest effort each GPT-6 takes.
	for model, want := range map[string][2]any{"gpt-5.2": {"low", nil}, "gpt-6-sol": {nil, "low"}, "gpt-4.1": {nil, nil}} {
		Complete(context.Background(), client, route("openai", model, ptr("sk-o")), "", "")
		body := (*got)[len(*got)-1].body
		if at(body, "model") != model || at(body, "text", "verbosity") != want[0] || at(body, "reasoning", "effort") != want[1] {
			t.Errorf("%s: %v", model, body)
		}
	}
}

func TestClaudeIsAskedWithItsInstructionsCached(t *testing.T) {
	client, got := serve(t, 200, `{"content":[{"type":"thinking","thinking":"Hmm."},{"type":"text","text":"Hi there"}]}`)
	text, err := Complete(context.Background(), client, route("anthropic", "", ptr("sk-ant")), "Be brief.", "Hi")
	if err != nil || text != "Hi there" {
		t.Fatal(text, err)
	}
	r := (*got)[0]
	if r.host != "api.anthropic.com" || r.path != "/v1/messages" || r.header.Get("x-api-key") != "sk-ant" || r.header.Get("anthropic-version") == "" {
		t.Errorf("%s %s %v", r.host, r.path, r.header)
	}
	if at(r.body, "model") != AnthropicCompletionModel || at(r.body, "thinking", "type") != "disabled" ||
		at(r.body, "system", 0, "text") != "Be brief." || at(r.body, "system", 0, "cache_control", "type") != "ephemeral" ||
		at(r.body, "messages", 0, "content") != "Hi" || at(r.body, "max_tokens") != float64(4096) {
		t.Errorf("%v", r.body)
	}
	// A model the writer picked keeps its own thinking.
	Complete(context.Background(), client, route("anthropic", "claude-opus-5-5", ptr("sk-ant")), "", "")
	if body := (*got)[1].body; at(body, "model") != "claude-opus-5-5" || at(body, "thinking") != nil {
		t.Errorf("%v", body)
	}
}

func TestEachChatServerGetsItsOwnSwitchForThinking(t *testing.T) {
	client, got := serve(t, 200, `{"choices":[{"message":{"role":"assistant","content":"Yo"}}]}`)
	for _, c := range []struct {
		base, path string
		key        *string
		field      []any
		want       any
	}{
		{"https://ark.cn-beijing.volces.com/api/v3", "/api/v3/chat/completions", ptr("sk"), []any{"reasoning_effort"}, "minimal"},
		{"https://api.minimax.io/v1", "/v1/chat/completions", ptr("sk"), []any{"reasoning_split"}, true},
		{"https://api.deepseek.com/v1/", "/v1/chat/completions", ptr("sk"), []any{"thinking", "type"}, "disabled"},
		{"http://localhost:11434/v1", "/v1/chat/completions", nil, []any{"reasoning_effort"}, nil},
	} {
		r := route("custom", "m", c.key)
		r.BaseURL = c.base
		text, err := Complete(context.Background(), client, r, "Be brief.", "Hi")
		s := (*got)[len(*got)-1]
		if err != nil || text != "Yo" || s.path != c.path || at(s.body, c.field...) != c.want ||
			at(s.body, "messages", 0, "content") != "Be brief." || at(s.body, "messages", 1, "role") != "user" {
			t.Errorf("%s: %q %v %s %v", c.base, text, err, s.path, s.body)
		}
		if (s.header.Get("Authorization") != "") != (c.key != nil) {
			t.Errorf("%s: %v", c.base, s.header)
		}
	}
}

func TestAFailedRequestSaysItsStatusAndAnswer(t *testing.T) {
	client, _ := serve(t, http.StatusTooManyRequests, `{"error":"slow down"}`)
	r := route("custom", "m", nil)
	r.BaseURL = "https://relay.example/v1"
	if _, err := Complete(context.Background(), client, r, "", ""); err == nil ||
		err.Error() != `Custom endpoint request failed (429 Too Many Requests): {"error":"slow down"}` {
		t.Errorf("%v", err)
	}
	if _, err := Complete(context.Background(), client, route("anthropic", "", ptr("k")), "", ""); err == nil ||
		err.Error() != `Claude request failed (429 Too Many Requests): {"error":"slow down"}` {
		t.Errorf("%v", err)
	}
	// An answer with no text is no text.
	client, _ = serve(t, 200, `{"choices":[{"message":{"content":null}}]}`)
	if text, err := Complete(context.Background(), client, r, "", ""); err != nil || text != "" {
		t.Errorf("%q %v", text, err)
	}
}
