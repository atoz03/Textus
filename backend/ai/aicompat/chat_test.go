package aicompat

import (
	"slices"
	"strings"
	"testing"
)

func TestChatMessagesStartWithTheSystemAndGroupEachStep(t *testing.T) {
	images := []ImageAttachment{{Name: "a.png", Mime: "image/png", DataBase64: "AAAA"}}
	shown := result("b", "Here.")
	shown.Images = images
	got := compact(chatMessages("rules", []AgentTurn{
		user("hi"), UserTurn("look", images...),
		call("a", "read_document", "{}"), call("b", "view_image", "{}"), result("a", "text"), shown,
		assistant("done"),
	}, false))
	want := `[{"content":"rules","role":"system"},{"content":"hi","role":"user"},` +
		`{"content":[{"image_url":{"url":"data:image/png;base64,AAAA"},"type":"image_url"},{"text":"look","type":"text"}],"role":"user"},` +
		`{"content":null,"role":"assistant","tool_calls":[{"function":{"arguments":"{}","name":"read_document"},"id":"a","type":"function"},{"function":{"arguments":"{}","name":"view_image"},"id":"b","type":"function"}]},` +
		`{"content":"text","role":"tool","tool_call_id":"a"},{"content":"Here.","role":"tool","tool_call_id":"b"},` +
		`{"content":[{"image_url":{"url":"data:image/png;base64,AAAA"},"type":"image_url"},{"text":"(Opened by the tool call above, not sent by the user: a.png)","type":"text"}],"role":"user"},` +
		`{"content":"done","role":"assistant"}]`
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestAStepsReasoningGoesBackOnlyWhereTheProviderWantsIt(t *testing.T) {
	first := call("a", "read_document", "{}")
	first.Reasoning = rawOf("Read it first.")
	history := []AgentTurn{user("go"), first, result("a", "text")}
	if got := compact(chatMessages("r", history, true)[2]); !strings.Contains(got, `"reasoning_content":"Read it first."`) {
		t.Errorf("replayed: %s", got)
	}
	if got := compact(chatMessages("r", history, false)[2]); strings.Contains(got, "reasoning_content") {
		t.Errorf("not wanted: %s", got)
	}
}

func TestAWholeReplyPrefersToolCallsAndKeepsTheirReasoningAndExtra(t *testing.T) {
	message := decode(`{"content":"Let me look.","reasoning_content":"Think.","tool_calls":[
		{"id":"c1","function":{"name":"read_document","arguments":"{}"},"extra_content":{"google":{"thought_signature":"sig"}}},
		{"id":"c2","function":{"name":"search_document"}}]}`)
	step := parseChatMessage(message, &searchReport{}, Ignore)
	if len(step.Calls) != 3 || step.Calls[1].Arguments != "{}" || step.Calls[2].Text != "Let me look." {
		t.Fatalf("%+v", step)
	}
	if got := string(step.Calls[0].Reasoning); got != `{"extra_content":{"google":{"thought_signature":"sig"}},"reasoning_content":"Think."}` {
		t.Errorf("kept %s", got)
	}
	// Both go back with the call.
	back := compact(chatMessages("r", []AgentTurn{user("go"), step.Calls[0], result("c1", "x")}, true)[2])
	if !strings.Contains(back, `"extra_content":{"google":{"thought_signature":"sig"}}`) || !strings.Contains(back, `"reasoning_content":"Think."`) {
		t.Errorf("sent back %s", back)
	}
	if done := parseChatMessage(decode(`{"content":"Just text."}`), &searchReport{}, Ignore); !done.Done || done.Text != "Just text." {
		t.Errorf("%+v", done)
	}
}

func TestAReusedOrMissingCallIDGetsOneOfItsOwn(t *testing.T) {
	ids := callIDsOf([]AgentTurn{call("call_0", "a", "{}")})
	got := []string{ids.claim("call_0", 0), ids.claim("", 1), ids.claim("x", 2), ids.claim("call_0", 3)}
	if want := []string{"call_0~2", "call_1", "x", "call_0~3"}; !slices.Equal(got, want) {
		t.Errorf("%v", got)
	}
}

func feedChat(s *chatStream, on EventSink, chunks ...string) {
	for _, c := range chunks {
		s.apply(c, on)
	}
}

func TestTheChatStreamAssemblesTextReasoningAndIndexedCalls(t *testing.T) {
	s := chatStream{ids: callIDsOf(nil)}
	var thinking, text []string
	feedChat(&s, func(e ProviderEvent) {
		switch e.Kind {
		case EventThinking:
			thinking = append(thinking, e.Text)
		case EventText:
			text = append(text, e.Text)
		}
	},
		`{"choices":[{"delta":{"reasoning_content":"Hm."}}]}`,
		`{"choices":[{"delta":{"content":"Sure"}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_0","function":{"name":"propose_edit","arguments":"{\"te"}}]}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":1,"id":"","function":{"name":"read_document","arguments":""}}]}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"xt\":1}"}}]}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`{"choices":[],"usage":{"prompt_tokens":10,"completion_tokens":2}}`,
		`[DONE]`,
	)
	if s.cutOff() != "" {
		t.Error("a whole reply refused")
	}
	step := s.result(Ignore)
	if len(step.Calls) != 3 || step.Calls[0].Arguments != `{"text":1}` || step.Calls[1].CallID != "call_1" || step.Calls[1].Arguments != "{}" || step.Calls[2].Text != "Sure" {
		t.Fatalf("%+v", step)
	}
	if string(step.Calls[0].Reasoning) != `"Hm."` {
		t.Errorf("reasoning %s", step.Calls[0].Reasoning)
	}
	if s.usage == nil || s.usage.InputTokens != 10 || s.usage.OutputTokens != 2 {
		t.Errorf("usage %+v", s.usage)
	}
	if !slices.Equal(thinking, []string{"Hm."}) || !slices.Equal(text, []string{"Sure"}) {
		t.Errorf("%v %v", thinking, text)
	}
}

func TestAChatCallCutOffIsRefusedNotRun(t *testing.T) {
	half := `{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c","function":{"name":"propose_edit","arguments":"{\"text\":\"ha"}}]}}]}`
	limit := chatStream{ids: callIDsOf(nil)}
	feedChat(&limit, Ignore, half, `{"choices":[{"delta":{},"finish_reason":"length"}]}`)
	if !strings.Contains(limit.cutOff(), "output limit") {
		t.Errorf("limit: %q", limit.cutOff())
	}
	dropped := chatStream{ids: callIDsOf(nil)}
	feedChat(&dropped, Ignore, half)
	if !strings.Contains(dropped.cutOff(), "connection ended") {
		t.Errorf("connection: %q", dropped.cutOff())
	}
	failed := chatStream{ids: callIDsOf(nil)}
	feedChat(&failed, Ignore, `{"error":{"message":"overloaded"}}`)
	if failed.err != `Custom endpoint request failed: {"message":"overloaded"}` {
		t.Errorf("%q", failed.err)
	}
}

func TestEachNativeSearchHasItsShapeAndEachVendorItsFields(t *testing.T) {
	tools := []ToolSpec{{Name: "read_document", Description: "d", Parameters: map[string]any{"type": "object"}}}
	shape := func(search NativeWebSearch, vendor Vendor) map[string]any {
		return chatBody("m", "r", []AgentTurn{user("q")}, tools, search, vendor, Thinking{})
	}
	last := func(body map[string]any) string { l := list(body["tools"]); return compact(l[len(l)-1]) }
	if got := last(shape(GroqSearch, Generic)); got != `{"type":"browser_search"}` {
		t.Errorf("groq %s", got)
	}
	if got := last(shape(OpenRouterSearch, Generic)); got != `{"type":"openrouter:web_search"}` {
		t.Errorf("openrouter %s", got)
	}
	if got := last(shape(ZhipuSearch, Generic)); got != `{"type":"web_search","web_search":{"enable":true,"search_result":true}}` {
		t.Errorf("zhipu %s", got)
	}
	if qwen := shape(QwenSearch, Generic); qwen["enable_search"] != true || len(list(qwen["tools"])) != 1 {
		t.Errorf("qwen %s", compact(qwen))
	}
	if ark := shape(NoWebSearch, Ark); ark["max_tokens"] != arkAgentMaxTokens {
		t.Errorf("ark %s", compact(ark))
	}
	if minimax := shape(NoWebSearch, MiniMax); minimax["reasoning_split"] != true {
		t.Errorf("minimax %s", compact(minimax))
	}
	if body := shape(NoWebSearch, Generic); compact(body["tool_choice"]) != `"auto"` || body["max_tokens"] != nil {
		t.Errorf("generic %s", compact(body))
	}
}

func cited(r *searchReport, text string) (string, []string) {
	var pages []string
	out := r.cite(text, func(e ProviderEvent) { pages = append(pages, e.URL) })
	return ReplaceCitationMarkers(out, func(k int) (string, bool) { return "[" + string(rune('0'+k)) + "]", true }), pages
}

func TestOpenRouterAnnotationsMarkTheirLinksOnceHoweverOftenStreamed(t *testing.T) {
	text := "Tides ([noaa](https://noaa.gov/))."
	annotation := `{"annotations":[{"type":"url_citation","url_citation":{"url":"https://noaa.gov/","title":"NOAA","start_index":7,"end_index":32}}]}`
	var r searchReport
	r.take(decode(annotation), nil)
	r.take(decode(annotation), nil)
	got, pages := cited(&r, text)
	if got != "Tides [1]." || !slices.Equal(pages, []string{"https://noaa.gov/"}) {
		t.Errorf("%q %v", got, pages)
	}
}

func TestZhipuRefsBecomeMarkersByTheirResults(t *testing.T) {
	var r searchReport
	r.take(nil, decode(`{"web_search":[{"refer":"ref_1","link":"https://a.example/","title":"A"},{"refer":"ref_2","link":"https://b.example/","title":"B"}]}`))
	got, pages := cited(&r, "Fact[ref_2]. Other【来源：ref_1】. Not [a link].")
	if got != "Fact[1]. Other[2]. Not [a link]." || !slices.Equal(pages, []string{"https://b.example/", "https://a.example/"}) {
		t.Errorf("%q %v", got, pages)
	}
	var unmarked searchReport
	unmarked.take(nil, decode(`{"web_search":[{"refer":"ref_1","link":"https://a.example/"}]}`))
	if got, pages := cited(&unmarked, "No marks."); got != "No marks." || len(pages) != 1 {
		t.Errorf("%q %v", got, pages)
	}
}

func TestGroqBrowserMarksGoAndItsPagesAreReported(t *testing.T) {
	var r searchReport
	r.take(decode(`{"executed_tools":[{"search_results":{"results":[{"url":"https://g.example/","title":"G"}]}}]}`), nil)
	got, pages := cited(&r, "It rose【2†L6-L10】. Kept【note】.")
	if got != "It rose. Kept【note】." || !slices.Equal(pages, []string{"https://g.example/"}) {
		t.Errorf("%q %v", got, pages)
	}
}

func TestGeminiThoughtsComeApartFromTheAnswer(t *testing.T) {
	if !isGeminiThought(decode(`{"content":"Weigh both.","extra_content":{"google":{"thought":true}}}`)) ||
		isGeminiThought(decode(`{"content":"The second."}`)) ||
		isGeminiThought(decode(`{"content":"","extra_content":{"google":{"thought_signature":"s"}}}`)) {
		t.Error("a streamed thought misread")
	}
	for text, want := range map[string][2]string{
		"<think>\nWeigh both.\n</think>\n\nThe second.": {"Weigh both.", "The second."},
		"a <think> b":         {"", "a <think> b"},
		"<think>never closed": {"", "<think>never closed"},
	} {
		if thought, answer := splitGeminiThoughts(text); thought != want[0] || answer != want[1] {
			t.Errorf("%q: %q %q", text, thought, answer)
		}
	}
	s := chatStream{ids: callIDsOf(nil)}
	var thinking []string
	feedChat(&s, func(e ProviderEvent) {
		if e.Kind == EventThinking {
			thinking = append(thinking, e.Text)
		}
	}, `{"choices":[{"delta":{"content":"Weigh.","extra_content":{"google":{"thought":true}}}}]}`, `{"choices":[{"delta":{"content":"Answer."}}]}`)
	if step := s.result(Ignore); step.Text != "Answer." || !slices.Equal(thinking, []string{"Weigh."}) {
		t.Errorf("%+v %v", step, thinking)
	}
}
