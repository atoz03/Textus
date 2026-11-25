package aicompat

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"unicode"
	"unicode/utf8"
)

// OpenAI's Responses API, as the public API (by key), the ChatGPT backend
// (by sign-in) and an OpenAI-compatible /responses (xAI, newer Qwen) serve
// it: an agent step's request (the conversation as input items, the tools)
// and the output read back, its citation markers stripped and its cited
// spans numbered.

// The Responses endpoints; tests point them elsewhere.
var (
	// OpenAIResponsesURL is the public API's.
	OpenAIResponsesURL = "https://api.openai.com/v1/responses"
	// CodexResponsesURL is the ChatGPT backend's: a ChatGPT sign-in's token
	// works only there.
	CodexResponsesURL = "https://chatgpt.com/backend-api/codex/responses"
)

func responsesImage(image ImageAttachment) map[string]any {
	return map[string]any{"type": "input_image", "image_url": image.DataURL()}
}

// responsesInputItems are one turn's input items: its own, and after a tool
// result that showed images, a user message carrying them, the one place
// every Responses server is known to read an input_image.
func responsesInputItems(turn AgentTurn) []any {
	switch turn.Kind {
	case User:
		// Images first, so the text reads as a question about the picture
		// above it.
		var content []any
		for _, image := range turn.Images {
			content = append(content, responsesImage(image))
		}
		content = append(content, map[string]any{"type": "input_text", "text": turn.Text})
		return []any{map[string]any{"type": "message", "role": "user", "content": content}}
	case Assistant:
		return []any{map[string]any{
			"type": "message", "role": "assistant",
			"content": []any{map[string]any{"type": "output_text", "text": turn.Text}},
		}}
	case ToolCalled:
		return []any{map[string]any{"type": "function_call", "call_id": turn.CallID, "name": turn.Name, "arguments": turn.Arguments}}
	}
	items := []any{map[string]any{"type": "function_call_output", "call_id": turn.CallID, "output": turn.Output}}
	if len(turn.Images) > 0 {
		var content []any
		for _, image := range turn.Images {
			content = append(content, responsesImage(image))
		}
		content = append(content, map[string]any{"type": "input_text", "text": ToolImagesNote(turn.Images)})
		items = append(items, map[string]any{"type": "message", "role": "user", "content": content})
	}
	return items
}

// responsesBody is one agent step's body: the whole history as input items
// plus the tools. Reasoning items are not sent back: with store: false the
// API needs only the calls and outputs.
func responsesBody(model, instructions string, history []AgentTurn, tools []ToolSpec, webSearch bool, thinking Thinking, streaming bool) map[string]any {
	input := []any{}
	for _, turn := range history {
		input = append(input, responsesInputItems(turn)...)
	}
	schemas := make([]any, 0, len(tools)+1)
	for _, tool := range tools {
		schemas = append(schemas, map[string]any{"type": "function", "name": tool.Name, "description": tool.Description, "parameters": tool.Parameters})
	}
	// OpenAI's own web search runs inside the response: offering it is
	// enough.
	if webSearch {
		schemas = append(schemas, map[string]any{"type": "web_search"})
	}
	body := map[string]any{
		"model":               model,
		"store":               false,
		"stream":              streaming,
		"instructions":        instructions,
		"input":               input,
		"tools":               schemas,
		"tool_choice":         "auto",
		"parallel_tool_calls": true,
	}
	// text.verbosity is a GPT-5-only field; older models refuse it.
	if strings.HasPrefix(model, "gpt-5") {
		body["text"] = map[string]any{"verbosity": "low"}
	}
	thinking.applyResponses(body)
	return body
}

// readResponsesStream reads a streamed reply and collects its finished
// output items (the response.completed event's output is unreliable on
// the ChatGPT backend), reporting fragments as they arrive, and the usage.
func readResponsesStream(resp *http.Response, label string, on EventSink) ([]any, *StepUsage, error) {
	var (
		output  []any
		usage   *StepUsage
		failure string
		// argument deltas carry the item's id, not the call's: the mapping
		// arrives with output_item.added.
		callIDs = map[string]string{}
	)
	err := readSSE(resp, label, func(data string) {
		if failure != "" {
			return
		}
		event, ok := decode(data).(map[string]any)
		if !ok {
			return
		}
		switch strAt(event, "type") {
		case "response.output_item.added":
			item := event["item"]
			switch strAt(item, "type") {
			case "reasoning":
				// The model is thinking, whether or not a summary follows.
				on(ProviderEvent{Kind: EventThinking})
			case "function_call":
				callID := strAt(item, "call_id")
				if id, ok := str(at(item, "id")); ok {
					callIDs[id] = callID
				}
				on(ProviderEvent{Kind: EventToolStart, CallID: callID, Name: strAt(item, "name")})
			}
		case "response.reasoning_summary_text.delta":
			if delta, ok := str(event["delta"]); ok {
				on(ProviderEvent{Kind: EventThinking, Text: delta})
			}
		case "response.output_text.delta":
			if delta, ok := str(event["delta"]); ok {
				on(ProviderEvent{Kind: EventText, Text: delta})
			}
		case "response.function_call_arguments.delta":
			delta, ok := str(event["delta"])
			if callID, known := callIDs[strAt(event, "item_id")]; ok && known {
				on(ProviderEvent{Kind: EventToolArgs, CallID: callID, Text: delta})
			}
		case "response.output_item.done":
			if item := event["item"]; item != nil {
				output = append(output, item)
			}
		case "response.completed":
			if u, ok := openAIUsage(at(event, "response", "usage")); ok {
				usage = &u
			}
		case "response.failed":
			var e any = event
			if inner := at(event, "response", "error"); inner != nil {
				e = inner
			}
			failure = label + " request failed: " + compact(e)
		}
	})
	if err != nil {
		return nil, nil, err
	}
	if failure != "" {
		return nil, nil, errors.New(failure)
	}
	return output, usage, nil
}

// responsesStep is one streamed round trip to url, signed by sign.
func responsesStep(ctx context.Context, client *http.Client, url string, sign func(*http.Request), label, model, instructions string, history []AgentTurn, tools []ToolSpec, webSearch bool, thinking Thinking, on EventSink) (StepResult, *StepUsage, error) {
	body := responsesBody(model, instructions, history, tools, webSearch, thinking, true)
	resp, err := request(ctx, client, url, body, sign)
	if err != nil {
		return StepResult{}, nil, err
	}
	output, usage, err := readResponsesStream(resp, label, on)
	if err != nil {
		return StepResult{}, nil, err
	}
	return parseResponsesOutput(output, on), usage, nil
}

// compatibleResponsesStep is one round trip to a third-party /responses,
// where a provider's own search lives only there (xAI). Not streamed: the
// citations come with the finished output.
func compatibleResponsesStep(ctx context.Context, client *http.Client, server chatServer, model, instructions string, history []AgentTurn, tools []ToolSpec, thinking Thinking, on EventSink) (StepResult, *StepUsage, error) {
	body := responsesBody(model, instructions, history, tools, true, thinking, false)
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	sign := func(req *http.Request) {
		if server.key != "" {
			req.Header.Set("Authorization", "Bearer "+server.key)
		}
	}
	parsed, err := postJSON(ctx, client, server.url("/responses"), body, sign, "Responses API request failed")
	if err != nil {
		return StepResult{}, nil, err
	}
	var usage *StepUsage
	if u, ok := openAIUsage(at(parsed, "usage")); ok {
		usage = &u
	}
	return parseResponsesOutput(list(at(parsed, "output")), on), usage, nil
}

// The markers OpenAI's models write in their own app's citation format
// (U+E200 "cite" U+E202 "turn3view0" U+E201, or with the delimiters lost:
// "citeturn3view0").
const (
	citeOpen  = '\uE200'
	citeClose = '\uE201'
	// citeFirst and citeLast bound the private-use range the format uses.
	citeFirst = '\uE200'
	citeLast  = '\uE2FF'
)

// stripCitationMarkers is text without OpenAI's citation markers, which
// would reach documents as garbage. A space left before punctuation goes
// too.
func stripCitationMarkers(text string) string {
	if !strings.ContainsRune(text, citeOpen) && !strings.Contains(text, "citeturn") {
		return text
	}
	out := make([]byte, 0, len(text))
	rest := text
	for {
		at := strings.IndexRune(rest, citeOpen)
		if bare := strings.Index(rest, "citeturn"); bare >= 0 && (at < 0 || bare < at) {
			at = bare
		}
		if at < 0 {
			break
		}
		out = append(out, rest[:at]...)
		marker := rest[at:]
		n := len(marker)
		if strings.HasPrefix(marker, string(citeOpen)) {
			if end := strings.IndexRune(marker, citeClose); end >= 0 {
				n = end + utf8.RuneLen(citeClose)
			}
		} else {
			n = bareMarkerLen(marker)
		}
		rest = marker[n:]
		// "found it citeturn1view0." reads "found it."
		next, _ := utf8.DecodeRuneInString(rest)
		if rest == "" || unicode.IsSpace(next) || strings.ContainsRune(".,;:!?)，。；：！？）、", next) {
			out = []byte(strings.TrimRight(string(out), " \t"))
		}
	}
	out = append(out, rest...)
	// A delimiter left over on its own.
	return strings.Map(func(r rune) rune {
		if r >= citeFirst && r <= citeLast {
			return -1
		}
		return r
	}, string(out))
}

// bareMarkerLen is the length of a citeturnNkindM(turnNkindM...) marker at
// the start of text.
func bareMarkerLen(text string) int {
	count := func(s string, is func(byte) bool) int {
		n := 0
		for n < len(s) && is(s[n]) {
			n++
		}
		return n
	}
	digit := func(b byte) bool { return b >= '0' && b <= '9' }
	lower := func(b byte) bool { return b >= 'a' && b <= 'z' }
	at := len("cite")
	for {
		after, ok := strings.CutPrefix(text[at:], "turn")
		if !ok {
			break
		}
		digits := count(after, digit)
		kind := count(after[digits:], lower)
		number := count(after[digits+kind:], digit)
		if digits == 0 || kind == 0 || number == 0 {
			break
		}
		at += len("turn") + digits + kind + number
	}
	return at
}

// stripMarkersInArguments is a call's arguments without citation markers in
// what they write - not in an anchor or a context, which quote the
// document and must still match it.
func stripMarkersInArguments(arguments string) string {
	if !strings.Contains(arguments, "citeturn") && !strings.ContainsRune(arguments, citeOpen) && !strings.Contains(arguments, `\ue200`) {
		return arguments
	}
	if !validJSON(arguments) {
		return arguments
	}
	var walk func(v any) any
	walk = func(v any) any {
		switch v := v.(type) {
		case string:
			return stripCitationMarkers(v)
		case []any:
			for i := range v {
				v[i] = walk(v[i])
			}
		case map[string]any:
			for key, value := range v {
				if key != "anchor" && key != "context" {
					v[key] = walk(value)
				}
			}
		}
		return v
	}
	return compact(walk(decode(arguments)))
}

// citedPartText is a message part's text with its url_citation annotations
// marked. xAI's carry the citation's number as their title, not the
// page's: a title that is only digits is no title.
func citedPartText(part any, citations *stepCitations, on EventSink) string {
	text := strAt(part, "text")
	var spans []citedSpan
	for _, a := range list(at(part, "annotations")) {
		if strAt(a, "type") != "url_citation" {
			continue
		}
		start, ok1 := u64(at(a, "start_index"))
		end, ok2 := u64(at(a, "end_index"))
		url, ok3 := str(at(a, "url"))
		if !ok1 || !ok2 || !ok3 {
			continue
		}
		title := strAt(a, "title")
		if allDigits(title) {
			title = ""
		}
		spans = append(spans, citedSpan{start: int(start), end: int(end), url: url, title: title})
	}
	if len(spans) == 0 {
		return text
	}
	return markCitedSpans(text, spans, citations, on)
}

// parseResponsesOutput reads output items (streamed or not) as a step. The
// pages a native search cited are reported from the text's annotations and
// the sources a web_search_call lists.
func parseResponsesOutput(output []any, on EventSink) StepResult {
	var (
		calls     []AgentTurn
		texts     []string
		citations stepCitations
	)
	for _, item := range output {
		switch strAt(item, "type") {
		case "web_search_call":
			for _, source := range list(at(item, "action", "sources")) {
				if url, ok := str(at(source, "url")); ok {
					citations.cite(url, strAt(source, "title"), on)
				}
			}
		case "function_call":
			arguments, ok := str(at(item, "arguments"))
			if !ok {
				arguments = "{}"
			}
			calls = append(calls, AgentTurn{Kind: ToolCalled, CallID: strAt(item, "call_id"), Name: strAt(item, "name"), Arguments: stripMarkersInArguments(arguments)})
		case "message":
			for _, part := range list(at(item, "content")) {
				if _, ok := str(at(part, "text")); ok {
					texts = append(texts, stripCitationMarkers(citedPartText(part, &citations, on)))
				}
			}
		}
	}
	if len(calls) == 0 {
		return Reply(strings.Join(texts, "\n"))
	}
	return CallsWithText(calls, strings.Join(texts, "\n"))
}
