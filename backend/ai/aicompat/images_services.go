package aicompat

import (
	"slices"
	"strconv"
	"strings"
)

// Each image service's request, reply, content refusal and quota error.

// OpenAI's Images API, and the servers that copy it. GPT image models
// always answer in base64 and refuse `response_format`; other models may
// answer with a link unless asked for base64, so they are asked.

// openAIImageSize is one of the three sizes every GPT image model makes;
// the other aspects take the nearest of them.
func openAIImageSize(aspect Aspect) string {
	switch aspect {
	case Landscape, Wide:
		return "1536x1024"
	case Portrait, Tall:
		return "1024x1536"
	}
	return "1024x1024"
}

func openAIImageRequest(target ImageTarget, prompt string, aspect Aspect) imageRequest {
	gptImage := strings.HasPrefix(target.Model, "gpt-image") || strings.HasPrefix(target.Model, "chatgpt-image")
	body := map[string]any{"model": target.Model, "prompt": prompt, "size": openAIImageSize(aspect), "n": 1}
	if target.API == OpenAIImages {
		// Medium is a fifth of high's price and plenty for a page; JPEG is
		// a fraction of the PNG's size, and every export reads it.
		body["quality"] = "medium"
		body["output_format"] = "jpeg"
	} else if !gptImage {
		body["response_format"] = "b64_json"
	}
	return imageRequest{join(target.BaseURL, "images/generations"), body}
}

// pictureIn is a reply item's picture data, else its link.
func pictureIn(item any) (imageReply, error) {
	if data, ok := str(at(item, "b64_json")); ok {
		bytes, err := decodeImageBase64(data)
		return imageReply{bytes: bytes}, err
	}
	if link, ok := str(at(item, "url")); ok {
		return imageReply{url: link}, nil
	}
	return imageReply{}, malformed("it holds neither picture data nor a link")
}

func openAIImageReply(reply any) (imageReply, error) {
	// `images` is SiliconFlow's shape, which some compatible servers use.
	item := first(at(reply, "data"))
	if item == nil {
		item = first(at(reply, "images"))
	}
	if item == nil {
		return imageReply{}, malformed("it holds no picture")
	}
	return pictureIn(item)
}

// openAIImageRefusal: GPT image's `moderation_blocked`, or DALL-E's
// `content_policy_violation` that compatible servers often pass on.
func openAIImageRefusal(body any) (string, bool) {
	e := at(body, "error")
	if e == nil {
		return "", false
	}
	code, message := strAt(e, "code"), strAt(e, "message")
	if code != "moderation_blocked" && code != "content_policy_violation" {
		return "", false
	}
	if message == "" {
		return "it was blocked by the safety system", true
	}
	return message, true
}

// Gemini's image models ("Nano Banana") through generateContent, asked
// for an image part. A block shows up in the reply, not as an HTTP error:
// the prompt's `promptFeedback.blockReason`, or the candidate's
// `finishReason`.

func geminiRequest(baseURL, model, prompt string, aspect Aspect) imageRequest {
	return imageRequest{join(baseURL, "models/"+model+":generateContent"), map[string]any{
		"contents": []any{map[string]any{"role": "user", "parts": []any{map[string]any{"text": prompt}}}},
		"generationConfig": map[string]any{
			"responseModalities": []any{"TEXT", "IMAGE"},
			// 1K: every image model makes it, the Lite model nothing else.
			"imageConfig": map[string]any{"aspectRatio": aspect.Ratio(), "imageSize": "1K"},
		},
	}}
}

// geminiBlocked are the finish reasons that mean the picture was held back
// for its content.
var geminiBlocked = []string{"SAFETY", "PROHIBITED_CONTENT", "BLOCKLIST", "IMAGE_SAFETY", "IMAGE_PROHIBITED_CONTENT", "IMAGE_RECITATION", "RECITATION", "SPII"}

// orAt is the value at the first of keys that v has.
func orAt(v any, keys ...string) any {
	for _, k := range keys {
		if found := at(v, k); found != nil {
			return found
		}
	}
	return nil
}

func geminiReply(reply any) (imageReply, error) {
	if reason, ok := str(at(reply, "promptFeedback", "blockReason")); ok {
		if message, ok := str(at(reply, "promptFeedback", "blockReasonMessage")); ok {
			reason = message
		}
		return imageReply{}, ImageError{ImageRefused, reason}
	}
	candidate := first(at(reply, "candidates"))
	if candidate == nil {
		return imageReply{}, malformed("it holds no picture")
	}
	parts := list(at(candidate, "content", "parts"))
	// The REST reply writes camelCase; snake_case is accepted too.
	for _, p := range parts {
		inline := orAt(p, "inlineData", "inline_data")
		if inline == nil {
			continue
		}
		mime, _ := str(orAt(inline, "mimeType", "mime_type"))
		if !strings.HasPrefix(mime, "image/") {
			continue
		}
		if data, ok := str(at(inline, "data")); ok {
			bytes, err := decodeImageBase64(data)
			return imageReply{bytes: bytes}, err
		}
		break
	}
	finish := strAt(candidate, "finishReason")
	said, saidAny := str(at(candidate, "finishMessage"))
	if !saidAny {
		// The model answered in words instead of a picture.
		var text []string
		for _, p := range parts {
			if t, ok := str(at(p, "text")); ok {
				text = append(text, t)
			}
		}
		said, saidAny = strings.TrimSpace(strings.Join(text, " ")), len(text) > 0
	}
	if slices.Contains(geminiBlocked, finish) {
		if !saidAny {
			said = finish
		}
		return imageReply{}, ImageError{ImageRefused, said}
	}
	if saidAny {
		return imageReply{}, imageFailed("The model sent no picture: " + said)
	}
	return imageReply{}, imageFailed("The model sent no picture (" + finish + ").")
}

// Volcengine Ark's Seedream models (Doubao): OpenAI's path and fields,
// with Ark's sizes and error codes. The sizes here are the smallest
// Seedream makes at each aspect.

func seedreamRequest(baseURL, model, prompt string, aspect Aspect) imageRequest {
	size := [...]string{"1024x1024", "1152x864", "864x1152", "1280x720", "720x1280"}[aspect]
	return imageRequest{join(baseURL, "images/generations"), map[string]any{
		"model": model, "prompt": prompt, "size": size,
		// In the reply itself: no second request, no link to expire.
		"response_format": "b64_json",
	}}
}

func seedreamReply(reply any) (imageReply, error) {
	item := first(at(reply, "data"))
	// A picture that failed in a batch carries its own error.
	failed := at(item, "error")
	if failed == nil {
		failed = at(reply, "error")
	}
	if failed != nil {
		wrapped := map[string]any{"error": failed}
		if reason, ok := seedreamRefusal(wrapped); ok {
			return imageReply{}, ImageError{ImageRefused, reason}
		}
		message, ok := imageErrorMessage(wrapped)
		if !ok {
			message = "The service made no picture."
		}
		return imageReply{}, imageFailed(message)
	}
	if item == nil {
		return imageReply{}, malformed("it holds no picture")
	}
	return pictureIn(item)
}

// seedreamRefusal: `InputTextSensitiveContentDetected`,
// `OutputImageSensitiveContentDetected` and their `.Violence`-style
// variants, the `...RiskDetection` and copyright `...PolicyViolation`
// codes.
func seedreamRefusal(body any) (string, bool) {
	code := strAt(body, "error", "code")
	if !strings.Contains(code, "SensitiveContentDetected") && !strings.Contains(code, "RiskDetection") && !strings.Contains(code, "PolicyViolation") {
		return "", false
	}
	if message, ok := imageErrorMessage(body); ok {
		return message, true
	}
	return code, true
}

// seedreamQuota: out of balance (`AccountOverdueError`), or over a quota
// or rate.
func seedreamQuota(body any) bool {
	code := strAt(body, "error", "code")
	return strings.Contains(code, "Overdue") || strings.Contains(code, "QuotaExceeded") || strings.Contains(code, "RateLimit")
}

// Alibaba Cloud Model Studio (DashScope) with Qwen-Image, through the
// synchronous multimodal-generation route. The picture comes back as a
// link, good for a day.

func dashScopeRequest(baseURL, model, prompt string, aspect Aspect) imageRequest {
	// Qwen-Image's own sizes, about 1.8 megapixels each; DashScope writes
	// them with `*`.
	size := [...]string{"1328*1328", "1472*1104", "1104*1472", "1664*928", "928*1664"}[aspect]
	return imageRequest{join(baseURL, "services/aigc/multimodal-generation/generation"), map[string]any{
		"model": model,
		"input": map[string]any{"messages": []any{map[string]any{"role": "user", "content": []any{map[string]any{"text": prompt}}}}},
		"parameters": map[string]any{
			"size": size, "n": 1,
			// The agent already wrote a full description; rewritten, the
			// picture drifts from what it was asked to show.
			"prompt_extend": false,
		},
	}}
}

func dashScopeReply(reply any) (imageReply, error) {
	content, ok := at(first(at(reply, "output", "choices")), "message", "content").([]any)
	if ok {
		for _, part := range content {
			if link, ok := str(at(part, "image")); ok {
				return imageReply{url: link}, nil
			}
		}
	}
	return imageReply{}, malformed("it holds no picture")
}

// dashScopeRefusal: `DataInspectionFailed` (the input or the output failed
// the content check), and the copyright and role blocks.
func dashScopeRefusal(body any) (string, bool) {
	code := strAt(body, "code")
	if !strings.EqualFold(code, "DataInspectionFailed") && !strings.EqualFold(code, "data_inspection_failed") &&
		code != "IPInfringementSuspect" && code != "CustomRoleBlocked" {
		return "", false
	}
	if message, ok := imageErrorMessage(body); ok {
		return message, true
	}
	return code, true
}

// dashScopeQuota: `Arrearage` (the account owes money) and the throttling
// codes.
func dashScopeQuota(body any) bool {
	code := strAt(body, "code")
	return code == "Arrearage" || strings.HasPrefix(code, "Throttling")
}

// Zhipu's image models (GLM-Image and CogView) on OpenAI's path. GLM-Image
// wants sides in multiples of 32 from 1024 and only `quality: "hd"`.

func zhipuRequest(baseURL, model, prompt string, aspect Aspect) imageRequest {
	glm := strings.HasPrefix(model, "glm-image")
	sizes := [...]string{"1024x1024", "1152x864", "864x1152", "1344x768", "768x1344"}
	if glm {
		sizes = [...]string{"1280x1280", "1472x1088", "1088x1472", "1728x960", "960x1728"}
	}
	body := map[string]any{"model": model, "prompt": prompt, "size": sizes[aspect]}
	if glm {
		body["quality"] = "hd"
	}
	return imageRequest{join(baseURL, "images/generations"), body}
}

func zhipuReply(reply any) (imageReply, error) {
	if link, ok := str(at(first(at(reply, "data")), "url")); ok {
		return imageReply{url: link}, nil
	}
	return imageReply{}, malformed("it holds no picture")
}

// zhipuCode: Zhipu writes its codes as strings ("1301"), now and then as
// numbers.
func zhipuCode(body any) string {
	switch code := at(body, "error", "code").(type) {
	case string:
		return code
	case float64:
		return strconv.FormatFloat(code, 'f', -1, 64)
	}
	return ""
}

// zhipuRefusal: 1301, the content check turned the request down.
func zhipuRefusal(body any) (string, bool) {
	if zhipuCode(body) != "1301" {
		return "", false
	}
	if message, ok := imageErrorMessage(body); ok {
		return message, true
	}
	return "content check", true
}

// zhipuQuota: 1113, the account is in arrears; 1302 and 1305, over the
// rate, or the model is overloaded.
func zhipuQuota(body any) bool {
	return slices.Contains([]string{"1113", "1302", "1305"}, zhipuCode(body))
}

// SiliconFlow: OpenAI's path, but `image_size` instead of `size` and the
// picture as a link under `images` that expires within the hour. Kolors
// makes only square and upright sizes, so a wide request gets its square.

func siliconFlowRequest(baseURL, model, prompt string, aspect Aspect) imageRequest {
	// Qwen-Image's sizes, which the FLUX and Z-Image models take too.
	size := [...]string{"1328x1328", "1472x1140", "1140x1472", "1664x928", "928x1664"}[aspect]
	if strings.Contains(model, "Kolors") {
		size = map[Aspect]string{Portrait: "768x1024", Tall: "720x1280"}[aspect]
		if size == "" {
			size = "1024x1024"
		}
	}
	return imageRequest{join(baseURL, "images/generations"), map[string]any{"model": model, "prompt": prompt, "image_size": size}}
}

// siliconFlowRefusal: SiliconFlow documents no code for a content block;
// its message says so in words.
func siliconFlowRefusal(body any) (string, bool) {
	message, ok := imageErrorMessage(body)
	if !ok {
		return "", false
	}
	lower := strings.ToLower(message)
	for _, word := range []string{"sensitive", "inappropriate", "moderation", "敏感", "违规", "不合规"} {
		if strings.Contains(lower, word) {
			return message, true
		}
	}
	return "", false
}

// MiniMax's image-01, on its own path. It takes the aspect itself and
// sends the picture back as base64. A failure often comes as HTTP 200 with
// a nonzero `base_resp.status_code`, so the reply is read for one.

func miniMaxRequest(baseURL, model, prompt string, aspect Aspect) imageRequest {
	return imageRequest{join(baseURL, "image_generation"), map[string]any{
		"model": model, "prompt": prompt, "aspect_ratio": aspect.Ratio(), "response_format": "base64", "n": 1,
	}}
}

func miniMaxReply(reply any) (imageReply, error) {
	if err := miniMaxFailure(reply); err != nil {
		return imageReply{}, err
	}
	encoded, ok := str(first(at(reply, "data", "image_base64")))
	if !ok {
		return imageReply{}, malformed("it holds no picture")
	}
	bytes, err := decodeImageBase64(encoded)
	return imageReply{bytes: bytes}, err
}

func miniMaxStatus(body any) (int64, string) {
	code, _ := at(body, "base_resp", "status_code").(float64)
	return int64(code), strAt(body, "base_resp", "status_msg")
}

// miniMaxFailure is the failure a `base_resp` names, as one of the three
// kinds; nil when it says success.
func miniMaxFailure(body any) error {
	code, message := miniMaxStatus(body)
	switch {
	case code == 0:
		return nil
	case miniMaxRefused(code):
		return ImageError{ImageRefused, message}
	case miniMaxOverQuota(code):
		return ImageError{ImageQuota, message}
	}
	return imageFailed("Image request failed (" + strconv.FormatInt(code, 10) + "): " + message)
}

// miniMaxRefused: 1026 and 1027, the description, or the picture made,
// failed the content check.
func miniMaxRefused(code int64) bool { return code == 1026 || code == 1027 }

// miniMaxOverQuota: 1002, over the rate; 1008, out of balance.
func miniMaxOverQuota(code int64) bool { return code == 1002 || code == 1008 }

func miniMaxRefusal(body any) (string, bool) {
	code, message := miniMaxStatus(body)
	return message, miniMaxRefused(code)
}

func miniMaxQuota(body any) bool {
	code, _ := miniMaxStatus(body)
	return miniMaxOverQuota(code)
}
