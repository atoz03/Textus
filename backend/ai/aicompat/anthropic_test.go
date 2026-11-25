package aicompat

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

func user(text string) AgentTurn      { return UserTurn(text) }
func assistant(text string) AgentTurn { return AssistantTurn(text) }
func result(id, output string) AgentTurn {
	return AgentTurn{Kind: ToolResult, CallID: id, Output: output}
}

func TestClaudeMessagesSkipEmptyAssistantTurns(t *testing.T) {
	got := compact(anthropicMessages([]AgentTurn{user("hi"), assistant("hello"), assistant(" "), user("again")}))
	want := `[{"content":"hi","role":"user"},{"content":"hello","role":"assistant"},{"content":"again","role":"user"}]`
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestEachStepsCallsAndResultsFormOnePair(t *testing.T) {
	messages := anthropicMessages([]AgentTurn{
		user("go"),
		call("a", "read_document", `{}`), call("b", "search_document", `{"query":"x"}`),
		result("a", "text"), result("b", "found"),
		call("c", "propose_edit", `not json`), result("c", "refused"),
		assistant("done"),
	})
	var roles []string
	for _, m := range messages {
		roles = append(roles, strAt(m, "role"))
	}
	if !slices.Equal(roles, []string{"user", "assistant", "user", "assistant", "user", "assistant"}) {
		t.Fatalf("%v", roles)
	}
	if got := compact(at(messages[1], "content")); got != `[{"id":"a","input":{},"name":"read_document","type":"tool_use"},{"id":"b","input":{"query":"x"},"name":"search_document","type":"tool_use"}]` {
		t.Errorf("calls: %s", got)
	}
	if got := compact(at(messages[4], "content")); got != `[{"content":"refused","tool_use_id":"c","type":"tool_result"}]` {
		t.Errorf("results: %s", got)
	}
	if got := compact(at(messages[3], "content")); !strings.Contains(got, `"input":{}`) {
		t.Errorf("arguments that don't parse: %s", got)
	}
}

func TestAToolsImagesRideInsideItsResult(t *testing.T) {
	turn := result("v", "Here it is.")
	turn.Images = []ImageAttachment{{Name: "map.png", Mime: "image/png", DataBase64: "AAAA"}}
	got := compact(anthropicMessages([]AgentTurn{call("v", "view_image", "{}"), turn})[1])
	want := `{"content":[{"content":[{"text":"Here it is.","type":"text"},{"source":{"data":"AAAA","media_type":"image/png","type":"base64"},"type":"image"}],"tool_use_id":"v","type":"tool_result"}],"role":"user"}`
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

// breakpoints are where a body marks the cache: "system/i", "messages/i".
func breakpoints(body map[string]any) []string {
	var out []string
	for i, block := range list(body["system"]) {
		if at(block, "cache_control") != nil {
			out = append(out, fmt.Sprintf("system/%d", i))
		}
	}
	for i, message := range list(body["messages"]) {
		for _, block := range list(at(message, "content")) {
			if at(block, "cache_control") != nil {
				out = append(out, fmt.Sprintf("messages/%d", i))
			}
		}
	}
	return out
}

func claudeBody(history ...AgentTurn) map[string]any {
	return anthropicBody(AnthropicAuth{APIKey: "k"}, "rules", history, nil, false, Thinking{}, "m")
}

func TestTheRequestMarksTheSystemPromptAndTheHistoryForCaching(t *testing.T) {
	// First message: only the system prompt; the automatic one covers the
	// rest.
	first := claudeBody(user("doc + q1"))
	if got := breakpoints(first); !slices.Equal(got, []string{"system/0"}) || strAt(first, "cache_control", "type") != "ephemeral" {
		t.Errorf("first: %v", got)
	}
	// Second: the end of the history too - not the message being sent,
	// which carries the document and is never sent again as it is.
	if got := breakpoints(claudeBody(user("q1"), assistant("a1"), user("doc + q2"))); !slices.Equal(got, []string{"system/0", "messages/1"}) {
		t.Errorf("second: %v", got)
	}
	// Third: also the end of the history before the newest exchange.
	third := claudeBody(user("q1"), assistant("a1"), user("q2"), call("1", "a", "{}"), result("1", "found"), assistant("a2"), user("doc + q3"))
	if got := breakpoints(third); !slices.Equal(got, []string{"system/0", "messages/1", "messages/5"}) {
		t.Errorf("third: %v", got)
	}
	if _, ok := at(list(third["messages"])[6], "content").(string); !ok {
		t.Error("the message being sent changed shape")
	}
	// A sign-in's identity block comes first; the marker stays on the last
	// block, so it still covers the whole system prompt.
	oauth := anthropicBody(AnthropicAuth{OAuthToken: "t"}, "rules", []AgentTurn{user("q")}, nil, false, Thinking{}, "m")
	if got := breakpoints(oauth); !slices.Equal(got, []string{"system/1"}) || strAt(list(oauth["system"])[0], "text") != claudeCodeIdentity {
		t.Errorf("oauth: %v", got)
	}
}

func TestEveryRequestStartsWithWhatTheOneBeforeItCached(t *testing.T) {
	// The prompt without its markers: what the cache compares.
	prompt := func(body map[string]any) []string {
		var out []string
		for _, m := range list(body["messages"]) {
			raw := compact(m)
			raw = strings.ReplaceAll(raw, `,"cache_control":{"type":"ephemeral"}`, "")
			raw = strings.ReplaceAll(raw, `"cache_control":{"type":"ephemeral"},`, "")
			out = append(out, raw)
		}
		return out
	}
	steps := [][]AgentTurn{
		{call("r1", "a", "{}"), call("r2", "b", "{}"), result("r1", "x"), result("r2", "y")},
		{call("e", "c", "{}"), result("e", "Edit proposed")},
	}
	turns := []AgentTurn{user("doc + q1")}
	requests := []map[string]any{claudeBody(turns...)}
	for _, step := range steps {
		turns = append(turns, step...)
		requests = append(requests, claudeBody(turns...))
	}
	for i := 1; i < len(requests); i++ {
		before, after := prompt(requests[i-1]), prompt(requests[i])
		if !slices.Equal(after[:len(before)], before) {
			t.Errorf("step %d doesn't start with the step before it", i)
		}
	}
	history := append([]AgentTurn{user("q1")}, steps[0]...)
	history = append(append(history, steps[1]...), assistant("a1"))
	second := claudeBody(append(slices.Clone(history), user("doc' + q2"))...)
	history = append(history, user("q2"), assistant("a2"))
	third := claudeBody(append(slices.Clone(history), user("doc'' + q3"))...)
	if got := breakpoints(second); !slices.Equal(got, []string{"system/0", "messages/5"}) {
		t.Errorf("second: %v", got)
	}
	if got := breakpoints(third); !slices.Equal(got, []string{"system/0", "messages/5", "messages/7"}) {
		t.Errorf("third: %v", got)
	}
	if !slices.Equal(prompt(third)[:6], prompt(second)[:6]) {
		t.Error("the third exchange doesn't start with what the second cached")
	}
}

func TestAStepsThinkingRidesOnItsFirstCallAndLeadsItsMessageBack(t *testing.T) {
	content := []any{
		map[string]any{"type": "thinking", "thinking": "Check first.", "signature": "sig"},
		map[string]any{"type": "tool_use", "id": "c1", "name": "propose_edit", "input": map[string]any{"action": "append", "text": "hi"}},
		map[string]any{"type": "text", "text": "Added a greeting."},
	}
	step := parseAnthropicContent(content, Ignore)
	if step.Done || len(step.Calls) != 2 || step.Calls[1].Kind != Assistant || step.Calls[1].Text != "Added a greeting." {
		t.Fatalf("%+v", step)
	}
	if got := step.Calls[0].Arguments; got != `{"action":"append","text":"hi"}` {
		t.Errorf("arguments %s", got)
	}
	replay := anthropicMessages(append([]AgentTurn{user("go")}, step.Calls[0], result("c1", "ok")))
	blocks := list(at(replay[1], "content"))
	if len(blocks) != 2 || strAt(decode(compact(blocks[0])), "signature") != "sig" || strAt(blocks[1], "type") != "tool_use" {
		t.Errorf("replayed %s", compact(replay[1]))
	}
}

func TestCitedTextBlocksJoinIntoSentencesWithTheirPagesMarked(t *testing.T) {
	var pages []string
	step := parseAnthropicContent([]any{
		map[string]any{"type": "server_tool_use", "id": "s", "name": "web_search"},
		map[string]any{"type": "web_search_tool_result", "tool_use_id": "s"},
		map[string]any{"type": "text", "text": "Tides peak"},
		map[string]any{"type": "text", "text": " at full moon", "citations": []any{map[string]any{"url": "https://noaa.gov/", "title": "NOAA"}}},
		map[string]any{"type": "text", "text": "."},
	}, func(e ProviderEvent) {
		if e.Kind == EventCitation {
			pages = append(pages, e.URL+" "+e.Title)
		}
	})
	if want := "Tides peak at full moon" + CitationMarker(1) + "."; step.Text != want {
		t.Errorf("%q", step.Text)
	}
	if !slices.Equal(pages, []string{"https://noaa.gov/ NOAA"}) {
		t.Errorf("%v", pages)
	}
}

func feed(s *anthropicStream, on EventSink, events ...string) {
	for _, e := range events {
		s.apply(e, on)
	}
}

func TestTheStreamRebuildsTextThinkingAndToolUseBlocks(t *testing.T) {
	var s anthropicStream
	var seen []string
	feed(&s, func(e ProviderEvent) { seen = append(seen, fmt.Sprint(e.Kind, ":", e.Text, e.CallID)) },
		`{"type":"message_start","message":{"usage":{"input_tokens":10,"cache_read_input_tokens":90}}}`,
		`{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"Plan."}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"s1"}}`,
		`{"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}`,
		`{"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"On it."}}`,
		`{"type":"content_block_start","index":2,"content_block":{"type":"tool_use","id":"t1","name":"propose_edit"}}`,
		`{"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"{\"text\":"}}`,
		`{"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"\"hi\"}"}}`,
		`{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":7}}`,
	)
	if got := compact(s.content()); got != `[{"signature":"s1","thinking":"Plan.","type":"thinking"},{"text":"On it.","type":"text"},{"id":"t1","input":{"text":"hi"},"name":"propose_edit","type":"tool_use"}]` {
		t.Errorf("content %s", got)
	}
	if s.usage != (StepUsage{InputTokens: 10, CacheReadInputTokens: 90, OutputTokens: 7}) {
		t.Errorf("usage %+v", s.usage)
	}
	want := []string{"3:", "3:Plan.", "0:On it.", "1:t1", "2:{\"text\":t1", "2:\"hi\"}t1"}
	if !slices.Equal(seen, want) {
		t.Errorf("events %q", seen)
	}
	if s.cutOff(16384) != "" {
		t.Error("a whole call refused")
	}
}

func TestTheStreamReportsErrorsAndKeepsServerBlocksForAPausedTurn(t *testing.T) {
	var s anthropicStream
	feed(&s, Ignore,
		`{"type":"content_block_start","index":0,"content_block":{"type":"server_tool_use","id":"s","name":"web_search"}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"query\":\"tides\"}"}}`,
		`{"type":"content_block_start","index":1,"content_block":{"type":"web_search_tool_result","tool_use_id":"s","content":[]}}`,
		`{"type":"message_delta","delta":{"stop_reason":"pause_turn"}}`,
	)
	if got := compact(s.content()); got != `[{"id":"s","input":{"query":"tides"},"name":"web_search","type":"server_tool_use"},{"content":[],"tool_use_id":"s","type":"web_search_tool_result"}]` {
		t.Errorf("content %s", got)
	}
	var failed anthropicStream
	feed(&failed, Ignore, `{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`)
	if failed.err != `Claude request failed: {"message":"Overloaded","type":"overloaded_error"}` {
		t.Errorf("%q", failed.err)
	}
}

func TestAToolCallCutOffByTheLimitOrTheConnectionIsRefused(t *testing.T) {
	start := `{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"t","name":"propose_edit"}}`
	half := `{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"text\":\"half"}}`
	var limit anthropicStream
	feed(&limit, Ignore, start, half, `{"type":"message_delta","delta":{"stop_reason":"max_tokens"}}`)
	if got := limit.cutOff(16384); !strings.Contains(got, "16384-token output limit") {
		t.Errorf("limit: %q", got)
	}
	var dropped anthropicStream
	feed(&dropped, Ignore, start, half)
	if got := dropped.cutOff(16384); !strings.Contains(got, "connection ended") {
		t.Errorf("connection: %q", got)
	}
}
