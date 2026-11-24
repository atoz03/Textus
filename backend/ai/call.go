package ai

import (
	"bytes"
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// One request with no tools, for the short writing calls (a title, the
// next message, an inline completion), in each provider's dialect.

const (
	// AnthropicCompletionModel is Anthropic's low-cost default.
	AnthropicCompletionModel = "claude-haiku-5-5"
	// OpenAICompletionModel is the public API's low-cost default.
	OpenAICompletionModel = "gpt-6-luna"

	openAIResponsesURL = "https://api.openai.com/v1/responses"
	// maxAnswerBytes bounds one answer read whole.
	maxAnswerBytes = 16 << 20
)

// Route is where one request goes, credentials resolved (Credentials.Route).
type Route struct {
	Provider Provider
	// Model is the model asked; "" is the dialect's low-cost default, which
	// only the OpenAI and Anthropic dialects have.
	Model string
	// BaseURL is a chat-completions server; the other dialects have their own.
	BaseURL string
	// Key is the API key; nil for a server that takes none.
	Key *string
}

// key is the route's key, "" for none.
func (r Route) key() string {
	if r.Key == nil {
		return ""
	}
	return *r.Key
}

// Complete sends instructions and userText as one request and answers the
// model's text.
func Complete(ctx context.Context, client *http.Client, r Route, instructions, userText string) (string, error) {
	switch r.Provider.Dialect {
	case "openai-responses":
		return completeResponses(ctx, client, r, instructions, userText)
	case "anthropic-messages":
		return completeAnthropic(ctx, client, r, instructions, userText)
	case "openai-chat-completions":
		return completeChat(ctx, client, r, instructions, userText)
	}
	return "", fmt.Errorf("unhandled dialect: %s", r.Provider.Dialect)
}

// post sends body as JSON and reads the answer into out; a failure reads
// "{what} ({status}): {body}", the shape the frontend reads the status of.
func post(ctx context.Context, client *http.Client, url string, sign func(*http.Request), body, out any, what string) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	sign(req)
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	answer, err := io.ReadAll(io.LimitReader(resp.Body, maxAnswerBytes))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("%s (%s): %s", what, resp.Status, answer)
	}
	return json.Unmarshal(answer, out)
}

// quickReasoningEffort is the lowest reasoning effort a model takes, for
// short one-shot requests: GPT-6 reasons at medium unless told; its Astra
// and Sol refuse none; older models refuse the field.
func quickReasoningEffort(model string) string {
	switch {
	case !strings.HasPrefix(model, "gpt-6"):
		return ""
	case strings.Contains(model, "luna"):
		return "none"
	}
	return "low"
}

func completeResponses(ctx context.Context, client *http.Client, r Route, instructions, userText string) (string, error) {
	model := r.Model
	if model == "" {
		model = OpenAICompletionModel
	}
	body := map[string]any{
		"model": model, "store": false, "stream": false, "instructions": instructions,
		"input": []any{map[string]any{"role": "user", "content": []any{map[string]any{"type": "input_text", "text": userText}}}},
	}
	// text.verbosity is a GPT-5-only field; older models refuse it.
	if strings.HasPrefix(model, "gpt-5") {
		body["text"] = map[string]string{"verbosity": "low"}
	}
	if effort := quickReasoningEffort(model); effort != "" {
		body["reasoning"] = map[string]string{"effort": effort}
	}
	var answer struct {
		Output []struct {
			Content []struct {
				Text *string `json:"text"`
			} `json:"content"`
		} `json:"output"`
	}
	sign := func(req *http.Request) { req.Header.Set("Authorization", "Bearer "+r.key()) }
	if err := post(ctx, client, openAIResponsesURL, sign, body, &answer, "OpenAI request failed"); err != nil {
		return "", err
	}
	for _, item := range answer.Output {
		for _, c := range item.Content {
			if c.Text != nil {
				return *c.Text, nil
			}
		}
	}
	return "", nil
}

func completeAnthropic(ctx context.Context, client *http.Client, r Route, instructions, userText string) (string, error) {
	model := r.Model
	if model == "" {
		model = AnthropicCompletionModel
	}
	body := map[string]any{
		"model": model,
		// Room for the longest single-call output; short ones never near it.
		"max_tokens": 4096,
		// The instructions end in a cache breakpoint: these calls fire often
		// with the same instructions before changing text.
		"system":   []any{map[string]any{"type": "text", "text": instructions, "cache_control": map[string]string{"type": "ephemeral"}}},
		"messages": []any{map[string]string{"role": "user", "content": userText}},
	}
	// Haiku thinks by default, latency an inline suggestion can't afford.
	// Only for this model: one the user picked may refuse "disabled".
	if model == AnthropicCompletionModel {
		body["thinking"] = map[string]string{"type": "disabled"}
	}
	var answer struct {
		Content []struct {
			Text *string `json:"text"`
		} `json:"content"`
	}
	auth := AnthropicAuth{APIKey: r.key()}
	if err := post(ctx, client, anthropicAPI+"/v1/messages", auth.Sign, body, &answer, "Claude request failed"); err != nil {
		return "", err
	}
	for _, c := range answer.Content {
		if c.Text != nil {
			return *c.Text, nil
		}
	}
	return "", nil
}

func completeChat(ctx context.Context, client *http.Client, r Route, instructions, userText string) (string, error) {
	body := map[string]any{
		"model": r.Model,
		"messages": []any{
			map[string]string{"role": "system", "content": instructions},
			map[string]string{"role": "user", "content": userText},
		},
	}
	// Each vendor's switch for its thinking models, off for a writing call.
	switch ChatVendor(r.BaseURL) {
	case Ark:
		body["reasoning_effort"] = "minimal"
	case MiniMax:
		body["reasoning_effort"] = "low"
		body["reasoning_split"] = true
	case DeepSeek:
		body["thinking"] = map[string]string{"type": "disabled"}
	}
	var answer struct {
		Choices []struct {
			Message struct {
				Content jsontext.Value `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	sign := func(req *http.Request) {
		if r.Key != nil {
			WithKey(req, r.BaseURL, *r.Key)
		}
	}
	url := strings.TrimRight(r.BaseURL, "/") + "/chat/completions"
	if err := post(ctx, client, url, sign, body, &answer, "Custom endpoint request failed"); err != nil {
		return "", err
	}
	for _, c := range answer.Choices {
		var text string
		if json.Unmarshal(c.Message.Content, &text) == nil {
			return text, nil
		}
	}
	return "", nil
}
