package aicompat

import (
	"context"
	"encoding/base64"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

// Text-to-image generation: the wire format each service speaks and the
// picture back as checked bytes. Every service is asked for one picture at
// about a megapixel, downloaded at once because the services' links
// expire.

// Aspect is a shape a picture can take. The agent's tool offers these and
// nothing finer: each service rounds them to the nearest size it makes.
type Aspect int

const (
	Square Aspect = iota
	Landscape
	Portrait
	Wide
	Tall
)

// Aspects are the ratios, in Aspect's order.
var Aspects = []string{"1:1", "4:3", "3:4", "16:9", "9:16"}

// ParseAspect is the aspect value names, false for none.
func ParseAspect(value string) (Aspect, bool) {
	for i, ratio := range Aspects {
		if ratio == value {
			return Aspect(i), true
		}
	}
	return Square, false
}

// Ratio is the aspect as "w:h".
func (a Aspect) Ratio() string { return Aspects[a] }

// How a service is asked for a picture.
const (
	// OpenAIImages is OpenAI's Images API with a GPT image model.
	OpenAIImages ImageAPI = "open-ai"
	// CompatibleImages is any other server with an OpenAI-style
	// `/images/generations`.
	CompatibleImages ImageAPI = "compatible"
	// GeminiImages is Gemini's generateContent with an image model.
	GeminiImages ImageAPI = "gemini"
	// SeedreamImages is Volcengine Ark's Seedream models.
	SeedreamImages ImageAPI = "seedream"
	// DashScopeImages is Alibaba Cloud Model Studio (DashScope),
	// Qwen-Image.
	DashScopeImages ImageAPI = "dash-scope"
	// ZhipuImages is Zhipu's GLM-Image and CogView.
	ZhipuImages ImageAPI = "zhipu"
	// SiliconFlowImages is SiliconFlow, OpenAI's path with its own fields.
	SiliconFlowImages ImageAPI = "silicon-flow"
	// MiniMaxImages is MiniMax's image-01.
	MiniMaxImages ImageAPI = "mini-max"
	// ChatGPTImages is the ChatGPT sign-in: the image_generation tool on
	// ChatGPT's backend, not an Images API, so never through Generate.
	ChatGPTImages ImageAPI = "chat-gpt"
)

// TakesReference reports whether a picture being redone is sent along
// with its description, for the service to change rather than draw anew.
func (api ImageAPI) TakesReference() bool {
	return api == GeminiImages || api == SeedreamImages || api == ChatGPTImages
}

// ImageTarget is where one request goes. APIKey may be empty only for a
// local server.
type ImageTarget struct {
	API     ImageAPI
	BaseURL string
	APIKey  string
	Model   string
}

// GeneratedImage is a picture, checked to be a PNG, JPEG or WebP of the
// size it says.
type GeneratedImage struct {
	Bytes  []byte
	Mime   string
	Ext    string
	Width  uint32
	Height uint32
}

// ImageErrorKind is why no picture came back, in the three kinds the user
// can act on differently.
type ImageErrorKind int

const (
	// ImageFailed is anything else: the key, the model, the network, a
	// malformed reply.
	ImageFailed ImageErrorKind = iota
	// ImageRefused: the service's content checks turned the description,
	// or the picture it made, down. Not retried: the user decides what to
	// ask for instead.
	ImageRefused
	// ImageQuota: out of balance, quota or rate.
	ImageQuota
)

// ImageError is why no picture came back, with the service's own words
// where it gave any.
type ImageError struct {
	Kind ImageErrorKind
	Why  string
}

func (e ImageError) Error() string {
	switch e.Kind {
	case ImageRefused:
		return "The service refused this description: " + e.Why
	case ImageQuota:
		return "The service's quota or balance ran out: " + e.Why
	}
	return e.Why
}

func imageFailed(why string) error { return ImageError{ImageFailed, why} }

// imageRequest is one request as a service wants it.
type imageRequest struct {
	url  string
	body map[string]any
}

// imageReply is what a service's answer holds: the picture, or where it
// is.
type imageReply struct {
	bytes []byte
	url   string
}

// GenerationTimeout is how long a picture may take: the slowest models
// take about two minutes.
const GenerationTimeout = 180 * time.Second

// checkEndpoint: a local server may speak plain HTTP - nothing leaves the
// machine - and every other address must be HTTPS: the key travels with
// the request.
func checkEndpoint(baseURL string) error {
	u, err := url.Parse(strings.TrimSpace(baseURL))
	if err == nil && u.Scheme == "" {
		err = errors.New("relative URL without a base")
	}
	if err != nil {
		return imageFailed("The image endpoint isn't a valid address: " + err.Error())
	}
	host := u.Hostname()
	ip, isIP := netip.ParseAddr(host)
	local := host == "localhost" || (isIP == nil && ip.IsLoopback())
	if u.Scheme == "https" || (u.Scheme == "http" && local) {
		return nil
	}
	return imageFailed("The image endpoint must be an https:// address (or a server on this computer).")
}

func join(baseURL, path string) string {
	return strings.TrimRight(strings.TrimSpace(baseURL), "/") + "/" + path
}

func requestFor(target ImageTarget, prompt string, aspect Aspect) imageRequest {
	base, model := target.BaseURL, target.Model
	switch target.API {
	case GeminiImages:
		return geminiRequest(base, model, prompt, aspect)
	case SeedreamImages:
		return seedreamRequest(base, model, prompt, aspect)
	case DashScopeImages:
		return dashScopeRequest(base, model, prompt, aspect)
	case ZhipuImages:
		return zhipuRequest(base, model, prompt, aspect)
	case SiliconFlowImages:
		return siliconFlowRequest(base, model, prompt, aspect)
	case MiniMaxImages:
		return miniMaxRequest(base, model, prompt, aspect)
	}
	return openAIImageRequest(target, prompt, aspect)
}

func readImageReply(api ImageAPI, reply any) (imageReply, error) {
	switch api {
	case GeminiImages:
		return geminiReply(reply)
	case SeedreamImages:
		return seedreamReply(reply)
	case DashScopeImages:
		return dashScopeReply(reply)
	case ZhipuImages:
		return zhipuReply(reply)
	case MiniMaxImages:
		return miniMaxReply(reply)
	}
	// SiliconFlow's `images` is read by OpenAI's reader too.
	return openAIImageReply(reply)
}

// imageRefusal is the reason a failed request's body gives for a content
// block, false for none.
func imageRefusal(api ImageAPI, body any) (string, bool) {
	switch api {
	case GeminiImages:
		// An HTTP error is never a content block on this API.
		return "", false
	case SeedreamImages:
		return seedreamRefusal(body)
	case DashScopeImages:
		return dashScopeRefusal(body)
	case ZhipuImages:
		return zhipuRefusal(body)
	case SiliconFlowImages:
		return siliconFlowRefusal(body)
	case MiniMaxImages:
		return miniMaxRefusal(body)
	}
	return openAIImageRefusal(body)
}

// imageQuota reports whether a failed request's body says the account is
// out of balance, quota or rate.
func imageQuota(api ImageAPI, body any) bool {
	switch api {
	case SeedreamImages:
		return seedreamQuota(body)
	case DashScopeImages:
		return dashScopeQuota(body)
	case ZhipuImages:
		return zhipuQuota(body)
	case MiniMaxImages:
		return miniMaxQuota(body)
	}
	return false
}

// readImageFailure is a failed request, by its status and body, as one of
// the three kinds.
func readImageFailure(api ImageAPI, status int, statusText, body string) error {
	parsed := decode(body)
	message, ok := imageErrorMessage(parsed)
	if !ok {
		message = short(body)
	}
	if reason, refused := imageRefusal(api, parsed); refused {
		return ImageError{ImageRefused, reason}
	}
	if imageQuota(api, parsed) || status == http.StatusPaymentRequired || status == http.StatusTooManyRequests {
		return ImageError{ImageQuota, message}
	}
	return imageFailed(fmt.Sprintf("Image request failed (%s): %s", statusText, message))
}

// imageErrorMessage is the human-readable part of an error body, in the
// shapes these services use: `{"error": {"message"}}`, `{"message"}` or
// `{"error": "..."}`.
func imageErrorMessage(body any) (string, bool) {
	for _, v := range []any{at(body, "error", "message"), at(body, "message"), at(body, "error")} {
		if s, ok := str(v); ok {
			if s = strings.TrimSpace(s); s != "" {
				return s, true
			}
			return "", false
		}
	}
	return "", false
}

// short is a body that isn't JSON (a proxy's HTML page), cut to something
// a sentence can carry.
func short(body string) string {
	body = strings.TrimSpace(body)
	n := 0
	for i := range body {
		if n == 300 {
			return body[:i] + "…"
		}
		n++
	}
	return body
}

func decodeImageBase64(encoded string) ([]byte, error) {
	bytes, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encoded))
	if err != nil {
		return nil, malformed("its picture data isn't valid base64")
	}
	return bytes, nil
}

// DecodeBase64 is base64 picture data, decoded.
func DecodeBase64(encoded string) ([]byte, error) { return decodeImageBase64(encoded) }

func malformed(what string) error {
	return imageFailed("The image service sent a reply Textus can't read: " + what + ".")
}

// first is v's first element, nil for none.
func first(v any) any {
	if l := list(v); len(l) > 0 {
		return l[0]
	}
	return nil
}

// withReference puts reference (the picture being redone) into a request
// for a service that takes one; leaves it out otherwise.
func withReference(api ImageAPI, body map[string]any, reference *ImageAttachment) {
	if reference == nil || !api.TakesReference() {
		return
	}
	switch api {
	case GeminiImages:
		// Before the description, as Gemini's image editing reads them.
		content := object(first(body["contents"]))
		if parts, ok := content["parts"].([]any); ok {
			inline := map[string]any{"inlineData": map[string]any{"mimeType": reference.Mime, "data": reference.DataBase64}}
			content["parts"] = append([]any{inline}, parts...)
		}
	case SeedreamImages:
		body["image"] = reference.DataURL()
	}
}

// Generate makes one picture - from reference where one is given and the
// service takes it, changed as prompt says - through client.
func Generate(ctx context.Context, client *http.Client, target ImageTarget, prompt string, aspect Aspect, reference *ImageAttachment) (GeneratedImage, error) {
	if target.API == ChatGPTImages {
		return GeneratedImage{}, imageFailed("A picture through the ChatGPT sign-in is asked for with CodexImage.")
	}
	if err := checkEndpoint(target.BaseURL); err != nil {
		return GeneratedImage{}, err
	}
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return GeneratedImage{}, imageFailed("There is no description to draw.")
	}
	r := requestFor(target, prompt, aspect)
	withReference(target.API, r.body, reference)
	ctx, cancel := context.WithTimeout(ctx, GenerationTimeout)
	defer cancel()
	sign := func(req *http.Request) {
		switch {
		case target.APIKey == "":
		case target.API == GeminiImages:
			req.Header.Set("x-goog-api-key", target.APIKey)
		default:
			req.Header.Set("Authorization", "Bearer "+target.APIKey)
		}
	}
	resp, err := request(ctx, client, r.url, r.body, sign)
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return GeneratedImage{}, imageFailed("The image service didn't answer within three minutes.")
		}
		return GeneratedImage{}, imageFailed("Couldn't reach the image service: " + err.Error())
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return GeneratedImage{}, imageFailed("Reading the image service's reply failed: " + err.Error())
	}
	text := strings.ToValidUTF8(string(raw), "\uFFFD")
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return GeneratedImage{}, readImageFailure(target.API, resp.StatusCode, resp.Status, text)
	}
	var reply any
	if json.Unmarshal(raw, &reply, lenient) != nil {
		return GeneratedImage{}, malformed("it isn't JSON")
	}
	got, err := readImageReply(target.API, reply)
	if err != nil {
		return GeneratedImage{}, err
	}
	if got.url != "" {
		if got.bytes, err = download(ctx, got.url); err != nil {
			return GeneratedImage{}, err
		}
	}
	return CheckedImage(got.bytes)
}

// CheckedImage is bytes a service sent, checked to be a picture: its type
// and size read from the file itself.
func CheckedImage(bytes []byte) (GeneratedImage, error) {
	found, ok := sniff(bytes)
	if !ok {
		return GeneratedImage{}, malformed("the file it sent isn't a PNG, JPEG or WebP picture")
	}
	return GeneratedImage{Bytes: bytes, Mime: found.mime, Ext: found.ext, Width: found.width, Height: found.height}, nil
}

// GPTImageSize is the size a GPT image model is asked for at aspect.
func GPTImageSize(aspect Aspect) string { return openAIImageSize(aspect) }
