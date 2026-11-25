package aicompat

import (
	"context"
	"fmt"
	"net/http"
	"strings"
)

// A picture through the ChatGPT sign-in: the Responses API's
// image_generation tool on ChatGPT's backend, counted against the plan's
// own image allowance. This backend is not a published API and can change
// without notice, so Settings calls it experimental.

// codexImageBody is the request for one picture: the image_generation
// tool, made the only thing the model may do.
func codexImageBody(prompt string, aspect Aspect, reference *ImageAttachment) map[string]any {
	// The tool's `size` alone was not kept to, so the shape is said in the
	// request too.
	size := GPTImageSize(aspect)
	text := fmt.Sprintf("%s\n\nAspect ratio %s, %s pixels.", prompt, aspect.Ratio(), size)
	content := []any{map[string]any{"type": "input_text", "text": text}}
	if reference != nil {
		content = append([]any{map[string]any{"type": "input_image", "image_url": reference.DataURL()}}, content...)
	}
	return map[string]any{
		"model": CodexCompletionModel,
		"store": false,
		// The ChatGPT backend only serves SSE.
		"stream":       true,
		"instructions": "Draw the picture the user describes with the image_generation tool, exactly as described - with a picture given, change that picture as described, keeping the rest of it. Say nothing.",
		"input":        []any{map[string]any{"role": "user", "content": content}},
		"tools": []any{map[string]any{
			"type": "image_generation",
			"size": size,
			// Plenty for a page, and the plan's image allowance goes
			// further.
			"quality":       "medium",
			"output_format": "jpeg",
		}},
		"tool_choice": map[string]any{"type": "image_generation"},
	}
}

// codexImageFrom is the picture in a finished response's output: an
// image_generation_call item's base64 `result`.
func codexImageFrom(output []any) ([]byte, error) {
	for _, item := range output {
		if strAt(item, "type") != "image_generation_call" {
			continue
		}
		result := strAt(item, "result")
		if strings.TrimSpace(result) == "" {
			status, ok := str(at(item, "status"))
			if !ok {
				status = "unknown"
			}
			return nil, imageFailed("ChatGPT's picture didn't finish (status: " + status + ").")
		}
		return decodeImageBase64(result)
	}
	return nil, imageFailed("ChatGPT answered without making a picture - the sign-in's plan may not include image generation.")
}

// codexImageFailure is a failed request's words, as one of the three
// kinds the user can act on: a limit or a balance as every provider's
// error reads (ErrorKind).
func codexImageFailure(message string) error {
	lower := strings.ToLower(message)
	if strings.Contains(lower, "moderation") || strings.Contains(lower, "safety") || strings.Contains(lower, "content policy") {
		return ImageError{ImageRefused, message}
	}
	if kind := ErrorKind(message); kind == "rateLimit" || kind == "balance" {
		return ImageError{ImageQuota, message}
	}
	return imageFailed(message)
}

// CodexImage makes one picture through the ChatGPT sign-in - from
// reference where one is given, changed as prompt says.
func CodexImage(ctx context.Context, client *http.Client, accessToken, accountID, originator, prompt string, aspect Aspect, reference *ImageAttachment) (GeneratedImage, error) {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return GeneratedImage{}, imageFailed("There is no description to draw.")
	}
	sign := func(req *http.Request) {
		req.Header.Set("Authorization", "Bearer "+accessToken)
		req.Header.Set("chatgpt-account-id", accountID)
		req.Header.Set("originator", originator)
		req.Header.Set("OpenAI-Beta", "responses=experimental")
	}
	resp, err := request(ctx, client, CodexResponsesURL, codexImageBody(prompt, aspect, reference), sign)
	if err != nil {
		return GeneratedImage{}, imageFailed("Couldn't reach ChatGPT: " + err.Error())
	}
	output, _, err := readResponsesStream(resp, "ChatGPT", func(ProviderEvent) {})
	if err != nil {
		return GeneratedImage{}, codexImageFailure(err.Error())
	}
	bytes, err := codexImageFrom(output)
	if err != nil {
		return GeneratedImage{}, err
	}
	return CheckedImage(bytes)
}
