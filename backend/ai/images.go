package ai

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"sync/atomic"

	"textus/backend/ai/aicompat"
)

// Pictures from Textus AI: the image model chosen in Settings (none by
// default, as a picture costs money), the generate_image tool, and the
// pictures waiting in memory for the user's decision. Only Accept saves
// one.

// ImageProviderEntry is one service Settings offers as Image model
// (ImageProviderEntry in src/platform/bindings/ai-agent.ts).
type ImageProviderEntry struct {
	ID string `json:"id"`
	// Label is a brand name; a few are localized by the frontend.
	Label string            `json:"label"`
	API   aicompat.ImageAPI `json:"api"`
	// BaseURL is nil for the compatible endpoint, whose address the user
	// gives.
	BaseURL *string `json:"baseUrl"`
	// KeyID is where the key is kept. The chat provider's own id wherever
	// the same key works for both, so a provider already set up for chat
	// needs no second key.
	KeyID string `json:"keyId"`
	// Models are the models offered, the default first; the compatible
	// endpoint's is typed in.
	Models []string `json:"models"`
	// SignInFor is the chat provider whose sign-in brings this service,
	// which then follows that chat rather than the provider's own pictures
	// on its key.
	SignInFor *string `json:"signInFor"`
}

func ref(s string) *string { return &s }

// ImageProviders are the image services Settings offers.
var ImageProviders = []ImageProviderEntry{
	// The ChatGPT sign-in's own pictures, on the plan's allowance:
	// experimental, since it is the backend ChatGPT's apps use, not a
	// published API. No key: the sign-in's credential, kept under the chat
	// provider.
	{ID: "openai-chatgpt", Label: "OpenAI (ChatGPT sign-in)", API: aicompat.ChatGPTImages, Models: []string{"gpt-image"}, SignInFor: ref("openai")},
	// An API key: the ChatGPT sign-in has no Images API. About $0.05 and
	// $0.01 a picture at medium quality.
	{ID: "openai", Label: "OpenAI", API: aicompat.OpenAIImages, BaseURL: ref("https://api.openai.com/v1"), KeyID: "openai", Models: []string{"gpt-image-2", "gpt-image-1-mini"}},
	{ID: "google", Label: "Google Gemini", API: aicompat.GeminiImages, BaseURL: ref("https://generativelanguage.googleapis.com/v1beta"), KeyID: "google", Models: []string{"gemini-nano-banana-2.1", "gemini-3.1-flash-lite-image"}},
	{ID: "doubao", Label: "Doubao (Volcengine)", API: aicompat.SeedreamImages, BaseURL: ref("https://ark.cn-beijing.volces.com/api/v3"), KeyID: "doubao", Models: []string{"doubao-seedream-5-0-pro-260628", "doubao-seedream-5-0-flash-260915"}},
	// The same region as the chat provider's endpoint, which a key
	// belongs to.
	{ID: "qwen", Label: "Alibaba Qwen", API: aicompat.DashScopeImages, BaseURL: ref("https://dashscope.aliyuncs.com/api/v1"), KeyID: "qwen", Models: []string{"qwen-image-3.0-pro", "qwen-image-3.0"}},
	{ID: "zhipu", Label: "Zhipu GLM", API: aicompat.ZhipuImages, BaseURL: ref("https://open.bigmodel.cn/api/paas/v4"), KeyID: "zhipu", Models: []string{"glm-image", "cogview-4-250304", "cogview-3-flash"}},
	{ID: "minimax", Label: "MiniMax (China)", API: aicompat.MiniMaxImages, BaseURL: ref("https://api.minimaxi.com/v1"), KeyID: "minimax", Models: []string{"image-01"}},
	{ID: "minimax-intl", Label: "MiniMax (Global)", API: aicompat.MiniMaxImages, BaseURL: ref("https://api.minimax.io/v1"), KeyID: "minimax-intl", Models: []string{"image-01"}},
	{ID: "siliconflow", Label: "SiliconFlow", API: aicompat.SiliconFlowImages, BaseURL: ref("https://api.siliconflow.cn/v1"), KeyID: "siliconflow", Models: []string{"Qwen/Qwen-Image", "Kwai-Kolors/Kolors"}},
	// Not the chat custom endpoint's key: that one is a different server
	// more often than not.
	{ID: "custom", Label: "OpenAI-compatible endpoint", API: aicompat.CompatibleImages, KeyID: "__image_model_custom", Models: []string{}},
}

// FollowChat is the image model setting that follows the chat provider
// (the default).
const FollowChat = "chat"

// FollowingChat is the image service that goes with a chat provider:
// OpenAI's through the ChatGPT sign-in while there is one, else the
// provider's own on the same key; false for a chat provider that makes
// none.
func FollowingChat(chatProvider string, chatGPTSignedIn bool) (string, bool) {
	if chatGPTSignedIn {
		for _, e := range ImageProviders {
			if e.SignInFor != nil && *e.SignInFor == chatProvider {
				return e.ID, true
			}
		}
	}
	for _, e := range ImageProviders {
		if e.KeyID == chatProvider && e.ID == chatProvider {
			return e.ID, true
		}
	}
	return "", false
}

// FindImageProvider is the image service id names.
func FindImageProvider(id string) (*ImageProviderEntry, bool) {
	for i := range ImageProviders {
		if ImageProviders[i].ID == id {
			return &ImageProviders[i], true
		}
	}
	return nil, false
}

// ImageModel is the image model setting as a request carries it
// (ImageModel in src/platform/bindings/ai-agent.ts).
type ImageModel struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	// BaseURL is the compatible endpoint's address.
	BaseURL string `json:"baseUrl"`
	// Limit is at most this many pictures per send; PerSend when unset.
	Limit *int `json:"limit"`
}

const (
	// PerSend is at most this many pictures per send by default, retries
	// included: each costs money. The setting may raise it to MaxPerSend.
	PerSend    = 2
	MaxPerSend = 8
)

// perSend is the per-send limit a setting asks for, kept within
// 1..MaxPerSend.
func perSend(limit *int) int {
	if limit == nil {
		return PerSend
	}
	return min(max(*limit, 1), MaxPerSend)
}

// ImageJob is where a picture is asked for: an address and key, or for the
// ChatGPT sign-in, how to get its credential. A picture can be made after
// the call that asked for it has returned.
type ImageJob struct {
	API     aicompat.ImageAPI
	BaseURL string
	APIKey  string
	Model   string
	// SignIn is the ChatGPT sign-in's credential, fetched (and refreshed)
	// per picture.
	SignIn func(ctx context.Context) (accessToken, accountID string, err error)
	// Client carries the requests; nil is Go's default.
	Client *http.Client
}

// generate is one picture from the chosen service - reference changed as
// prompt says, where the service takes one.
func (j ImageJob) generate(ctx context.Context, prompt string, aspect aicompat.Aspect, reference *aicompat.ImageAttachment) (aicompat.GeneratedImage, error) {
	client := j.Client
	if client == nil {
		client = http.DefaultClient
	}
	if j.API != aicompat.ChatGPTImages || j.SignIn == nil {
		return aicompat.Generate(ctx, client, aicompat.ImageTarget{API: j.API, BaseURL: j.BaseURL, APIKey: j.APIKey, Model: j.Model}, prompt, aspect, reference)
	}
	token, account, err := j.SignIn(ctx)
	if err != nil {
		return aicompat.GeneratedImage{}, aicompat.ImageError{Kind: aicompat.ImageFailed, Why: "The ChatGPT sign-in isn't usable - sign in again in Settings › Textus AI: " + err.Error()}
	}
	return aicompat.CodexImage(ctx, client, token, account, Originator, prompt, aspect, reference)
}

// ImageRun is one request's image model, and how many pictures it has
// asked for, of how many it may.
type ImageRun struct {
	job   ImageJob
	made  atomic.Int64
	limit int
	// picture reads a picture in the document's folder by its src, for one
	// being redone; nil without a folder.
	picture func(src string) (aicompat.ImageAttachment, bool)
	// request is the request it belongs to: stopping it stops its
	// pictures.
	request string
}

// ImageAccess is what resolving an image model needs from the app.
type ImageAccess struct {
	// ChatGPTSignedIn: the ChatGPT sign-in is there.
	ChatGPTSignedIn bool
	// Key is the key kept under id, "" for none.
	Key func(id string) string
	// SignIn is the ChatGPT sign-in's credential.
	SignIn func(ctx context.Context) (accessToken, accountID string, err error)
	Client *http.Client
}

// ResolveImageRun is a request's image run; false - and no
// generate_image tool - without an image model, or without the key it
// needs.
func ResolveImageRun(choice *ImageModel, chatProvider string, access ImageAccess) (*ImageRun, bool) {
	if choice == nil {
		return nil, false
	}
	id := choice.Provider
	if id == FollowChat {
		following, ok := FollowingChat(chatProvider, access.ChatGPTSignedIn)
		if !ok {
			return nil, false
		}
		id = following
	}
	entry, ok := FindImageProvider(id)
	if !ok {
		return nil, false
	}
	run := &ImageRun{limit: perSend(choice.Limit)}
	if entry.API == aicompat.ChatGPTImages {
		// Signed in with ChatGPT, or no generate_image at all.
		if !access.ChatGPTSignedIn {
			return nil, false
		}
		run.job = ImageJob{API: entry.API, Model: entry.Models[0], SignIn: access.SignIn, Client: access.Client}
		return run, true
	}
	// One of this service's own models (one kept from another service is
	// not), or any typed for the compatible endpoint.
	model := strings.TrimSpace(choice.Model)
	if model == "" || (len(entry.Models) > 0 && !slices.Contains(entry.Models, model)) {
		if len(entry.Models) == 0 {
			return nil, false
		}
		model = entry.Models[0]
	}
	baseURL := strings.TrimSpace(choice.BaseURL)
	if entry.BaseURL != nil {
		baseURL = *entry.BaseURL
	}
	if baseURL == "" {
		return nil, false
	}
	key := ""
	if access.Key != nil {
		key = access.Key(entry.KeyID)
	}
	// A compatible endpoint on this computer may need no key.
	if key == "" && entry.API != aicompat.CompatibleImages {
		return nil, false
	}
	run.job = ImageJob{API: entry.API, BaseURL: baseURL, APIKey: key, Model: model, Client: access.Client}
	return run, true
}

// OfflineImageRun is an image model that makes no picture: each fails in
// the background, while generate_image answers as for a real one. Spends
// nothing; for tests.
func OfflineImageRun(limit int) *ImageRun {
	return &ImageRun{job: ImageJob{API: aicompat.OpenAIImages, BaseURL: "https://127.0.0.1:1", APIKey: "sk", Model: "gpt-image-2"}, limit: limit}
}

// ForRequest has stopping request requestID stop these pictures.
func (r *ImageRun) ForRequest(requestID string) *ImageRun {
	r.request = requestID
	return r
}

// InFolder has a picture being redone read by read, from the document's
// folder.
func (r *ImageRun) InFolder(read func(src string) (aicompat.ImageAttachment, bool)) *ImageRun {
	r.picture = read
	return r
}

// localPicture is the file a picture's Markdown shows, read from the
// document's folder; false for a web or data URL, a file outside the
// folder, or anything else.
func (r *ImageRun) localPicture(markdown string) (aicompat.ImageAttachment, bool) {
	src, ok := PictureSrc(markdown)
	if !ok || r.picture == nil {
		return aicompat.ImageAttachment{}, false
	}
	return r.picture(src)
}
