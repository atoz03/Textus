package backend

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"testing"

	"textus/backend/ai"
)

// run calls a command as the frontend does, with its arguments as JSON.
func run(a *App, name, args string) (string, error) {
	v, err := commands[name](&Call{ctx: context.Background(), app: a, args: jsontext.Value(args), name: name})
	if err != nil {
		return "", err
	}
	raw, err := json.Marshal(v)
	return string(raw), err
}

func TestTheWritersOwnEndpointNamesTheDocument(t *testing.T) {
	asked := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked++
		if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer sk-relay" {
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		w.Write([]byte(`{"choices":[{"message":{"content":"Title: 《旅行计划》。\n1. Plan day two\n2. plan day two"}}]}`))
	}))
	defer server.Close()
	a := &App{dirs: Dirs{Config: t.TempDir()}, defaults: map[string]bool{"textusAi": true}}
	key := "sk-relay"
	a.changeEndpoints(func([]ai.CustomEndpoint) []ai.CustomEndpoint {
		return []ai.CustomEndpoint{{ID: "custom:relay", BaseURL: server.URL + "/v1", APIKey: &key, Model: "m"}}
	})
	if got, err := run(a, "suggest_doc_title", `{"provider":"custom:relay","model":null,"excerpt":"去京都的计划"}`); err != nil || got != `"旅行计划"` {
		t.Error(got, err)
	}
	if got, err := run(a, "suggest_next_message", `{"provider":"custom:relay","model":"m2","exchange":{"messages":["Plan a trip"],"reply":"Here is day one."}}`); err != nil ||
		got != `["Title: 《旅行计划》。","Plan day two"]` {
		t.Error(got, err)
	}
	// Nothing to go on: no request.
	asked = 0
	for name, args := range map[string]string{
		"suggest_doc_title":    `{"provider":"custom:relay","model":null,"excerpt":"  "}`,
		"suggest_next_message": `{"provider":"custom:relay","model":null,"exchange":{"messages":[],"reply":"Done."}}`,
	} {
		if got, err := run(a, name, args); err != nil || (got != "null" && got != "[]") {
			t.Error(name, got, err)
		}
	}
	if asked != 0 {
		t.Error("asked with nothing to go on")
	}
	// A provider not set up says so.
	if _, err := run(a, "suggest_doc_title", `{"provider":"anthropic","model":null,"excerpt":"text"}`); err == nil || err.Error() != ai.NotConfigured {
		t.Error(err)
	}
}
