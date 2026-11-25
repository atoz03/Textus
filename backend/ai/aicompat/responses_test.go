package aicompat

import (
	"slices"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestCitationMarkersAreTakenOutOfTextAndArguments(t *testing.T) {
	delimited := "返回选项及概率。" + string(citeOpen) + "cite" + string(rune(0xE202)) + "turn3view0" + string(citeClose)
	for text, want := range map[string]string{
		delimited: "返回选项及概率。",
		"中文输入应优先测试多语言版。citeturn2search0turn3view2\n\nNext": "中文输入应优先测试多语言版。\n\nNext",
		"It was found citeturn1view0. Then":                "It was found. Then",
		"We cite turnout data.":                            "We cite turnout data.",
	} {
		if got := stripCitationMarkers(text); got != want {
			t.Errorf("%q: %q", text, got)
		}
	}
	step := parseResponsesOutput([]any{
		map[string]any{"type": "function_call", "call_id": "c1", "name": "propose_edit", "arguments": `{"action":"append","text":"Jev 的接口。citeturn3view0"}`},
		map[string]any{"type": "function_call", "call_id": "c2", "name": "propose_edit", "arguments": `{"action":"replace","anchor":"概率。citeturn3view0","text":"概率。"}`},
	}, Ignore)
	if args := step.Calls[0].Arguments; !strings.Contains(args, "Jev 的接口。") || strings.Contains(args, "cite") {
		t.Errorf("text: %s", args)
	}
	// An anchor quotes the document as it is, markers and all.
	if args := step.Calls[1].Arguments; !strings.Contains(args, "概率。citeturn3view0") {
		t.Errorf("anchor: %s", args)
	}
}

func TestEveryTurnKindBecomesItsInputItems(t *testing.T) {
	shown := result("c", "Here.")
	shown.Images = []ImageAttachment{{Name: "a.png", Mime: "image/png", DataBase64: "AAAA"}}
	body := responsesBody("gpt-5.5", "rules", []AgentTurn{
		UserTurn("look", shown.Images...), assistant("ok"), call("c", "view_image", "{}"), shown,
	}, []ToolSpec{{Name: "view_image", Description: "d", Parameters: map[string]any{"type": "object"}}}, true, Thinking{}, true)
	want := `[{"content":[{"image_url":"data:image/png;base64,AAAA","type":"input_image"},{"text":"look","type":"input_text"}],"role":"user","type":"message"},` +
		`{"content":[{"text":"ok","type":"output_text"}],"role":"assistant","type":"message"},` +
		`{"arguments":"{}","call_id":"c","name":"view_image","type":"function_call"},` +
		`{"call_id":"c","output":"Here.","type":"function_call_output"},` +
		`{"content":[{"image_url":"data:image/png;base64,AAAA","type":"input_image"},{"text":"(Opened by the tool call above, not sent by the user: a.png)","type":"input_text"}],"role":"user","type":"message"}]`
	if got := compact(body["input"]); got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
	if got := compact(body["tools"]); got != `[{"description":"d","name":"view_image","parameters":{"type":"object"},"type":"function"},{"type":"web_search"}]` {
		t.Errorf("tools %s", got)
	}
	if compact(body["text"]) != `{"verbosity":"low"}` || body["store"] != false || body["parallel_tool_calls"] != true {
		t.Errorf("body %s", compact(body))
	}
	if old := responsesBody("gpt-4.1", "r", nil, nil, false, Thinking{}, false); old["text"] != nil || compact(old["input"]) != "[]" {
		t.Errorf("an older model: %s", compact(old))
	}
}

func responsesCited(output []any) (string, [][2]string) {
	var pages [][2]string
	step := parseResponsesOutput(output, func(e ProviderEvent) { pages = append(pages, [2]string{e.URL, e.Title}) })
	text := ReplaceCitationMarkers(step.Text, func(k int) (string, bool) { return "[" + string(rune('0'+k)) + "]", true })
	return text, pages
}

func TestOpenAIInlineCitationLinksBecomeNumberedMarkers(t *testing.T) {
	text := "Spring tides peak at full moon ([noaa.gov](https://noaa.gov/tides)). Neap tides are weakest ([例子](https://例.example/x))."
	span := func(needle string) (int, int) {
		start := utf8.RuneCountInString(text[:strings.Index(text, needle)])
		return start, start + utf8.RuneCountInString(needle)
	}
	a0, a1 := span("[noaa.gov](https://noaa.gov/tides)")
	b0, b1 := span("([例子](https://例.example/x))")
	got, pages := responsesCited([]any{map[string]any{"type": "message", "content": []any{map[string]any{
		"type": "output_text", "text": text,
		"annotations": []any{
			map[string]any{"type": "url_citation", "url": "https://noaa.gov/tides", "title": "Tides", "start_index": float64(a0), "end_index": float64(a1)},
			map[string]any{"type": "url_citation", "url": "https://例.example/x", "title": "例子", "start_index": float64(b0), "end_index": float64(b1)},
		},
	}}}})
	if got != "Spring tides peak at full moon [1]. Neap tides are weakest [2]." || pages[0] != [2]string{"https://noaa.gov/tides", "Tides"} {
		t.Errorf("%q %v", got, pages)
	}
}

func TestXAINumbersAreNotTitlesAndQwenSourcesAreReported(t *testing.T) {
	text := "It rose 3% [[1]](https://x.example/a)."
	start := strings.Index(text, "[[")
	got, pages := responsesCited([]any{
		map[string]any{"type": "web_search_call", "action": map[string]any{"type": "search", "sources": []any{map[string]any{"url": "https://q.example/"}}}},
		map[string]any{"type": "message", "content": []any{map[string]any{
			"type": "output_text", "text": text,
			"annotations": []any{map[string]any{"type": "url_citation", "url": "https://x.example/a", "title": "1", "start_index": float64(start), "end_index": float64(len(text) - 1)}},
		}}},
	})
	if got != "It rose 3% [2]." || !slices.Equal(pages, [][2]string{{"https://q.example/", ""}, {"https://x.example/a", ""}}) {
		t.Errorf("%q %v", got, pages)
	}
}

func TestOutputKeepsTheTextAfterItsCallsAndJoinsParts(t *testing.T) {
	step := parseResponsesOutput([]any{
		map[string]any{"type": "reasoning"},
		map[string]any{"type": "function_call", "call_id": "c1", "name": "propose_edit", "arguments": `{"text":"hi"}`},
		map[string]any{"type": "message", "content": []any{map[string]any{"type": "output_text", "text": "Added a greeting."}}},
	}, Ignore)
	if len(step.Calls) != 2 || step.Calls[0].CallID != "c1" || step.Calls[1].Text != "Added a greeting." {
		t.Errorf("%+v", step)
	}
	done := parseResponsesOutput([]any{map[string]any{"type": "message", "content": []any{map[string]any{"text": "part one"}, map[string]any{"text": "part two"}}}}, Ignore)
	if !done.Done || done.Text != "part one\npart two" {
		t.Errorf("%+v", done)
	}
}
