package aicompat

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	raw, err := os.ReadFile("../../../src/ai/ai-error-kinds.json")
	if err == nil {
		err = LoadErrorKinds(raw)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "ai-error-kinds.json:", err)
		os.Exit(1)
	}
	os.Exit(m.Run())
}

func TestErrorsReadAsTheFrontendReadsThem(t *testing.T) {
	for text, want := range map[string]string{
		"request failed (429): slow down":                         "rateLimit",
		"request failed (429): You exceeded your current quota":   "balance",
		"request failed (401): bad key":                           "auth",
		"request failed (403): not allowed":                       "auth",
		"ChatGPT: usage limit reached":                            "rateLimit",
		"request failed (503): busy":                              "server",
		"tools are not supported":                                 "",
		"error sending request: dial tcp: connection refused":     "network",
		"Custom endpoint request failed (400 Bad Request): nope":  "",
		"Claude request failed (401 Unauthorized): invalid x-api": "auth",
	} {
		if got := ErrorKind(text); got != want {
			t.Errorf("%s: %q, want %q", text, got, want)
		}
	}
}

func TestOnlyARequestRefusedAsMadeIsTriedAgainUnstreamed(t *testing.T) {
	for text, want := range map[string]bool{
		"Custom endpoint request failed (400 Bad Request): stream not supported": true,
		"Custom endpoint request failed (422): bad field":                        true,
		"Custom endpoint request failed (401): bad key":                          false,
		"Custom endpoint request failed (403): not allowed":                      false,
		"Custom endpoint request failed (429): slow down":                        false,
		"Custom endpoint request failed (402): payment required":                 false,
		"Custom endpoint request failed (500): oops":                             false,
		"Custom endpoint request failed (400): reasoning_effort is unsupported":  false,
		"The model hit its output limit in the middle of an edit.":               false,
		"The connection ended in the middle of the reply. Try again.":            false,
	} {
		if got := streamingRefused(errors.New(text)); got != want {
			t.Errorf("%s: %v", text, got)
		}
	}
	chat := AgentProvider{Endpoint: Endpoint{Kind: ChatEndpoint}}
	if !chat.FallsBackToPlainChat() || (AgentProvider{Endpoint: Endpoint{Kind: AnthropicEndpoint}}).FallsBackToPlainChat() ||
		(AgentProvider{Endpoint: Endpoint{Kind: OpenAIKeyEndpoint}}).FallsBackToPlainChat() {
		t.Error("only a compatible server falls back to plain chat")
	}
}

// recorded is one request a test server took.
type recorded struct {
	header http.Header
	path   string
	body   map[string]any
}

// server answers each request with the next of answers (a status and a
// body, an SSE stream when it starts with "data:"), recording it.
func server(t *testing.T, answers ...[2]string) (*httptest.Server, func() []recorded) {
	t.Helper()
	var mu sync.Mutex
	var seen []recorded
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		json.Unmarshal(raw, &body)
		mu.Lock()
		n := len(seen)
		seen = append(seen, recorded{r.Header.Clone(), r.URL.Path, body})
		mu.Unlock()
		if n >= len(answers) {
			http.Error(w, "no more answers", http.StatusTeapot)
			return
		}
		status := 0
		fmt.Sscan(answers[n][0], &status)
		if strings.HasPrefix(answers[n][1], "data:") {
			w.Header().Set("Content-Type", "text/event-stream")
		}
		w.WriteHeader(status)
		io.WriteString(w, answers[n][1])
	}))
	t.Cleanup(s.Close)
	return s, func() []recorded {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(seen)
	}
}

func sse(events ...string) string {
	var b strings.Builder
	for _, e := range events {
		b.WriteString("data: " + e + "\n\n")
	}
	return b.String()
}

func pointAt(t *testing.T, target *string, url string) {
	old := *target
	*target = url
	t.Cleanup(func() { *target = old })
}

func TestClaudeIsSignedAndAPausedTurnIsContinued(t *testing.T) {
	paused := sse(
		`{"type":"message_start","message":{"usage":{"input_tokens":5}}}`,
		`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Searching."}}`,
		`{"type":"message_delta","delta":{"stop_reason":"pause_turn"},"usage":{"output_tokens":3}}`,
		`{"type":"message_stop"}`,
	)
	finished := sse(
		`{"type":"message_start","message":{"usage":{"input_tokens":7}}}`,
		`{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"t1","name":"read_document"}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{}"}}`,
		`{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":4}}`,
		`{"type":"message_stop"}`,
	)
	s, seen := server(t, [2]string{"200", paused}, [2]string{"200", finished})
	pointAt(t, &AnthropicMessagesURL, s.URL+"/v1/messages")
	p := AgentProvider{ID: "anthropic", Model: "claude-sonnet-5-5", Endpoint: Endpoint{Kind: AnthropicEndpoint, Anthropic: AnthropicAuth{APIKey: "sk-ant"}}, Thinking: Auto, NativeSearch: &NativeSearch{Kind: AnthropicSearch}, Client: s.Client()}
	step, usage, err := p.Step(context.Background(), "rules", []AgentTurn{user("q")}, nil, Ignore)
	if err != nil {
		t.Fatal(err)
	}
	if len(step.Calls) != 2 || step.Calls[0].Name != "read_document" || step.Calls[1].Text != "Searching." {
		t.Errorf("%+v", step)
	}
	if usage == nil || *usage != (StepUsage{InputTokens: 12, OutputTokens: 7}) {
		t.Errorf("usage %+v", usage)
	}
	requests := seen()
	if len(requests) != 2 || requests[0].header.Get("x-api-key") != "sk-ant" || requests[0].header.Get("anthropic-version") != "2023-06-01" {
		t.Fatalf("%d requests: %v", len(requests), requests[0].header)
	}
	if tools := compact(requests[0].body["tools"]); !strings.Contains(tools, "web_search_20250305") {
		t.Errorf("no search: %s", tools)
	}
	messages := list(requests[1].body["messages"])
	if last := compact(messages[len(messages)-1]); last != `{"content":[{"text":"Searching.","type":"text"}],"role":"assistant"}` {
		t.Errorf("not continued: %s", last)
	}
}

func TestTheOpenAIKeyAndTheChatGPTSignInStreamTheResponsesAPI(t *testing.T) {
	stream := sse(
		`{"type":"response.output_item.added","item":{"type":"function_call","id":"fc_1","call_id":"call_1","name":"propose_edit"}}`,
		`{"type":"response.function_call_arguments.delta","item_id":"fc_1","delta":"{\"text\":"}`,
		`{"type":"response.function_call_arguments.delta","item_id":"fc_1","delta":"\"hi\"}"}`,
		`{"type":"response.output_item.done","item":{"type":"function_call","id":"fc_1","call_id":"call_1","name":"propose_edit","arguments":"{\"text\":\"hi\"}"}}`,
		`{"type":"response.completed","response":{"usage":{"input_tokens":20,"input_tokens_details":{"cached_tokens":16},"output_tokens":5}}}`,
	)
	s, seen := server(t, [2]string{"200", stream}, [2]string{"200", stream})
	pointAt(t, &OpenAIResponsesURL, s.URL+"/v1/responses")
	pointAt(t, &CodexResponsesURL, s.URL+"/codex/responses")
	var args []string
	on := func(e ProviderEvent) {
		if e.Kind == EventToolArgs && e.CallID == "call_1" {
			args = append(args, e.Text)
		}
	}
	key := AgentProvider{ID: "openai", Model: "gpt-6.1-sol", Endpoint: Endpoint{Kind: OpenAIKeyEndpoint, APIKey: "sk"}, Thinking: Deep, Client: s.Client()}
	step, usage, err := key.Step(context.Background(), "rules", []AgentTurn{user("q")}, nil, on)
	if err != nil {
		t.Fatal(err)
	}
	if len(step.Calls) != 1 || step.Calls[0].Arguments != `{"text":"hi"}` || strings.Join(args, "") != `{"text":"hi"}` {
		t.Errorf("%+v %q", step, args)
	}
	if usage == nil || *usage != (StepUsage{InputTokens: 4, CacheReadInputTokens: 16, OutputTokens: 5}) {
		t.Errorf("usage %+v", usage)
	}
	signIn := AgentProvider{ID: "openai", Model: "gpt-6-luna", Endpoint: Endpoint{Kind: CodexEndpoint, AccessToken: "tok", AccountID: "acct", Originator: "textus"}, Client: s.Client()}
	if _, _, err := signIn.Step(context.Background(), "rules", []AgentTurn{user("q")}, nil, Ignore); err != nil {
		t.Fatal(err)
	}
	requests := seen()
	if requests[0].path != "/v1/responses" || requests[0].header.Get("Authorization") != "Bearer sk" || compact(requests[0].body["reasoning"]) != `{"effort":"high","summary":"auto"}` {
		t.Errorf("key request: %s %v %s", requests[0].path, requests[0].header, compact(requests[0].body))
	}
	h := requests[1].header
	if requests[1].path != "/codex/responses" || h.Get("Authorization") != "Bearer tok" || h.Get("chatgpt-account-id") != "acct" || h.Get("originator") != "textus" || h.Get("OpenAI-Beta") != "responses=experimental" {
		t.Errorf("sign-in request: %s %v", requests[1].path, h)
	}
}

func TestAServerThatRefusesStreamingIsAskedAgainWhole(t *testing.T) {
	whole := `{"choices":[{"message":{"content":"","tool_calls":[{"id":"call_0","type":"function","function":{"name":"read_document","arguments":"{}"}}]}}],"usage":{"prompt_tokens":9,"completion_tokens":1}}`
	s, seen := server(t, [2]string{"400", `{"error":"stream not supported"}`}, [2]string{"200", whole})
	p := AgentProvider{ID: "custom", Model: "local", Endpoint: Endpoint{Kind: ChatEndpoint, BaseURL: s.URL + "/v1/", APIKey: "k"}, Thinking: Deep, Client: s.Client()}
	history := []AgentTurn{user("q0"), call("call_0", "read_document", "{}"), result("call_0", "x"), assistant("a"), user("q")}
	step, usage, err := p.Step(context.Background(), "rules", history, nil, Ignore)
	if err != nil {
		t.Fatal(err)
	}
	// The id the server reused is made the step's own.
	if len(step.Calls) != 1 || step.Calls[0].CallID != "call_0~2" || usage == nil || usage.InputTokens != 9 {
		t.Errorf("%+v %+v", step, usage)
	}
	requests := seen()
	if len(requests) != 2 || requests[0].path != "/v1/chat/completions" || requests[0].body["stream"] != true || requests[0].body["reasoning_effort"] != "high" {
		t.Fatalf("first: %+v", requests)
	}
	if _, streamed := requests[1].body["stream"]; streamed || requests[1].body["reasoning_effort"] != nil || requests[1].header.Get("Authorization") != "Bearer k" {
		t.Errorf("second: %s", compact(requests[1].body))
	}
}

func TestABadKeyIsSaidAsItIsNotPaidForTwice(t *testing.T) {
	s, seen := server(t, [2]string{"401", `{"error":"invalid api key"}`})
	p := AgentProvider{ID: "custom", Model: "local", Endpoint: Endpoint{Kind: ChatEndpoint, BaseURL: s.URL}, Client: s.Client()}
	_, _, err := p.Step(context.Background(), "rules", []AgentTurn{user("q")}, nil, Ignore)
	if err == nil || !strings.HasPrefix(err.Error(), "Custom endpoint request failed (401 Unauthorized): ") || len(seen()) != 1 {
		t.Errorf("%v after %d requests", err, len(seen()))
	}
	if seen()[0].header.Get("Authorization") != "" {
		t.Error("a server without a key got one")
	}
}

func TestXAISearchesThroughItsResponsesEndpoint(t *testing.T) {
	answer := `{"output":[{"type":"message","content":[{"type":"output_text","text":"Found."}]}],"usage":{"input_tokens":3,"output_tokens":1}}`
	s, seen := server(t, [2]string{"200", answer})
	p := AgentProvider{ID: "xai", Model: "grok-4.7", Endpoint: Endpoint{Kind: ChatEndpoint, BaseURL: s.URL + "/v1", APIKey: "xk"}, NativeSearch: &NativeSearch{Kind: CompatibleResponsesSearch}, Client: s.Client()}
	step, _, err := p.Step(context.Background(), "rules", []AgentTurn{user("q")}, nil, Ignore)
	if err != nil || !step.Done || step.Text != "Found." {
		t.Fatalf("%+v %v", step, err)
	}
	r := seen()[0]
	if r.path != "/v1/responses" || r.body["stream"] != false || !strings.Contains(compact(r.body["tools"]), `{"type":"web_search"}`) || r.header.Get("Authorization") != "Bearer xk" {
		t.Errorf("%s %s", r.path, compact(r.body))
	}
}

func TestAStreamThatGoesSilentIsGivenUpOn(t *testing.T) {
	old := streamReadTimeout
	streamReadTimeout = 200 * time.Millisecond
	t.Cleanup(func() { streamReadTimeout = old })
	release := make(chan struct{})
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Hel\"}}]}\n\n")
		w.(http.Flusher).Flush()
		<-release
	}))
	t.Cleanup(func() { close(release); s.Close() })
	p := AgentProvider{ID: "custom", Model: "m", Endpoint: Endpoint{Kind: ChatEndpoint, BaseURL: s.URL}, Client: s.Client()}
	done := make(chan error, 1)
	go func() {
		_, _, err := p.Step(context.Background(), "r", []AgentTurn{user("q")}, nil, Ignore)
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "stopped responding") {
			t.Errorf("%v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("a silent stream held the request")
	}
}

func TestEventsSplitAcrossReadsAndAServerThatKeepsTheConnectionOpen(t *testing.T) {
	release := make(chan struct{})
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, part := range []string{"data:{\"choices\":[{\"delta\":{\"con", "tent\":\"Hi\"}}]}\r\n\r\n: keep-alive\n\n", "data: [DONE]\n\n"} {
			io.WriteString(w, part)
			w.(http.Flusher).Flush()
			time.Sleep(10 * time.Millisecond)
		}
		<-release
	}))
	t.Cleanup(func() { close(release); s.Close() })
	p := AgentProvider{ID: "custom", Model: "m", Endpoint: Endpoint{Kind: ChatEndpoint, BaseURL: s.URL}, Client: s.Client()}
	step, _, err := p.Step(context.Background(), "r", []AgentTurn{user("q")}, nil, Ignore)
	if err != nil || step.Text != "Hi" {
		t.Errorf("%+v %v", step, err)
	}
}

func TestACancelledRequestSaysSo(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Read whole, so the server watches the connection and sees it go.
		io.ReadAll(r.Body)
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
	}))
	t.Cleanup(s.Close)
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)
	p := AgentProvider{ID: "custom", Model: "m", Endpoint: Endpoint{Kind: ChatEndpoint, BaseURL: s.URL}, Client: s.Client()}
	if _, _, err := p.Step(ctx, "r", []AgentTurn{user("q")}, nil, Ignore); !errors.Is(err, context.Canceled) {
		t.Errorf("%v", err)
	}
}
