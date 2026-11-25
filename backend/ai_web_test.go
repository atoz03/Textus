package backend

import (
	"context"
	"encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"textus/backend/ai"
	"textus/backend/ai/aicompat"
)

func TestTheWebSearchButtonSaysWhoSearches(t *testing.T) {
	a := agentApp(t, nil)
	for _, c := range [][2]string{
		{`{"provider":"openai","model":null,"searchService":null,"searxngUrl":null}`, `{"native":true,"service":null}`},
		{`{"provider":"deepseek","model":null,"searchService":"auto","searxngUrl":null}`, `{"native":false,"service":null}`},
		{`{"provider":"custom","model":null,"searchService":"searxng","searxngUrl":"https://search.example"}`, `{"native":false,"service":"searxng"}`},
	} {
		if got, err := run(a, "web_search_status", c[0]); err != nil || got != c[1] {
			t.Fatal(c[0], got, err)
		}
	}
	a.changeKeys(func(keys map[string]string) bool { keys["moonshot"] = "sk-m"; return true })
	if got, _ := run(a, "web_search_status", `{"provider":"deepseek","model":null,"searchService":null,"searxngUrl":null}`); got != `{"native":false,"service":"kimi"}` {
		t.Fatal(got)
	}
}

// toolNamesOf is the tools a Chat Completions request offered.
func toolNamesOf(body map[string]any) []string {
	var names []string
	tools, _ := body["tools"].([]any)
	for _, tool := range tools {
		names = append(names, tool.(map[string]any)["function"].(map[string]any)["name"].(string))
	}
	return names
}

func TestASearchedReplyListsThePagesItCites(t *testing.T) {
	var mu sync.Mutex
	var bodies []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/search" {
			if r.URL.Query().Get("format") != "json" {
				http.Error(w, "not json", http.StatusForbidden)
				return
			}
			w.Write([]byte(`{"results":[{"title":"Tides","url":"https://sea.example/tides","content":"Twice a day."}]}`))
			return
		}
		var body map[string]any
		json.UnmarshalRead(r.Body, &body)
		mu.Lock()
		bodies = append(bodies, body)
		n := len(bodies)
		mu.Unlock()
		if n == 1 {
			sse(w, `{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c1","type":"function","function":{"name":"web_search","arguments":"{\"queries\":[\"agent web test tides\"]}"}}]}}]}`,
				`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`)
			return
		}
		sse(w, `{"choices":[{"delta":{"content":"Twice a day [2]."}}]}`, `{"choices":[{"delta":{},"finish_reason":"stop"}]}`)
	}))
	defer server.Close()
	a := agentApp(t, server)
	choice, searxng := "searxng", server.URL
	history := []aicompat.AgentTurn{aicompat.UserTurn("Earlier"), {Kind: aicompat.Assistant, Text: "Old [1].", Sources: []aicompat.WebSource{{N: 1, URL: "https://old.example/", Title: "Old"}}}}
	turns, err := a.agentMessage(context.Background(), messageRequest{
		Provider: "custom", Blocks: []ai.SentBlock{{ID: "b1", Markdown: "A short draft."}}, History: history,
		Message: "How often do tides turn?", Mode: ai.ModeWrite, RequestID: "web1", WebSearch: true,
		SearchService: &choice, SearxngURL: &searxng,
	}, func(ai.StreamEvent) {})
	if err != nil {
		t.Fatal(err)
	}
	if names := toolNamesOf(bodies[0]); !slices.Contains(names, "web_search") || !slices.Contains(names, "web_fetch") || !slices.Contains(names, "search_papers") {
		t.Fatal(names)
	}
	raw, _ := json.Marshal(bodies[0]["messages"])
	// Told how to search and cite; the earlier reply shown with its sources.
	if !strings.Contains(string(raw), "queries go to SearXNG") || !strings.Contains(string(raw), `Old [1].\n\nSources:\n[1] Old - https://old.example/`) {
		t.Fatal(string(raw))
	}
	// Numbered on from the earlier reply's sources.
	result := turns[2]
	if result.Kind != aicompat.ToolResult || !strings.Contains(result.Output, "[2] Tides\nhttps://sea.example/tides") {
		t.Fatalf("%+v", result)
	}
	reply := turns[len(turns)-1]
	if reply.Text != "Twice a day [2]." || len(reply.Sources) != 1 || reply.Sources[0] != (aicompat.WebSource{N: 2, URL: "https://sea.example/tides", Title: "Tides"}) {
		t.Fatalf("%+v", reply)
	}
}

func TestWithSearchOffNoWebToolIsOffered(t *testing.T) {
	var body map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.UnmarshalRead(r.Body, &body)
		sse(w, `{"choices":[{"delta":{"content":"Hi."}}]}`, `{"choices":[{"delta":{},"finish_reason":"stop"}]}`)
	}))
	defer server.Close()
	a := agentApp(t, server)
	if _, err := a.agentMessage(context.Background(), messageRequest{
		Provider: "custom", Blocks: []ai.SentBlock{{ID: "b1", Markdown: "A short draft."}}, Message: "Hi", Mode: ai.ModeWrite, RequestID: "web2",
	}, func(ai.StreamEvent) {}); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(body["messages"])
	if names := toolNamesOf(body); slices.Contains(names, "web_fetch") || strings.Contains(string(raw), "---web---") {
		t.Fatal(names)
	}
}
