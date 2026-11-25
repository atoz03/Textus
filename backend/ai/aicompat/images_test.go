package aicompat

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json/v2"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"
)

// Each service's request and reply against a local HTTP fixture: what is
// sent (path, auth, body) and how each kind of answer is read. No request
// leaves the machine.

func init() { PictureNet.Loopback = true }

// seenImage is a request as the fixture received it.
type seenImage struct {
	line   string
	header http.Header
	body   map[string]any
}

type imageAnswer struct {
	status int
	kind   string
	body   []byte
}

// serveImages answers the requests that come in with replies, one each, in
// order; the requests come back over the channel.
func serveImages(t *testing.T, replies ...imageAnswer) (string, chan seenImage) {
	t.Helper()
	var mu sync.Mutex
	got := make(chan seenImage, len(replies)+1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		json.Unmarshal(raw, &body)
		mu.Lock()
		defer mu.Unlock()
		got <- seenImage{r.Method + " " + r.URL.RequestURI(), r.Header, body}
		if len(replies) == 0 {
			http.Error(w, "no more", http.StatusGone)
			return
		}
		reply := replies[0]
		replies = replies[1:]
		w.Header().Set("Content-Type", reply.kind)
		w.WriteHeader(reply.status)
		w.Write(reply.body)
	}))
	t.Cleanup(server.Close)
	return server.URL, got
}

func jsonAnswer(status int, body string) imageAnswer {
	return imageAnswer{status, "application/json", []byte(body)}
}

// pngOf is a PNG's signature and IHDR, all sniff reads.
func pngOf(width, height uint32) []byte {
	b := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\x0dIHDR")
	b = binary.BigEndian.AppendUint32(b, width)
	b = binary.BigEndian.AppendUint32(b, height)
	return append(b, 8, 6, 0, 0, 0, 0, 0, 0, 0)
}

func picture() []byte { return pngOf(4, 3) }

func b64() string { return base64.StdEncoding.EncodeToString(picture()) }

func generateAt(api ImageAPI, base, model string, aspect Aspect) (GeneratedImage, error) {
	return Generate(context.Background(), http.DefaultClient, ImageTarget{API: api, BaseURL: base, APIKey: "sk-test", Model: model},
		"A lighthouse on a cliff at dusk", aspect, nil)
}

func TestOpenAIAsksForOneMediumJPEGAndReadsTheBase64(t *testing.T) {
	base, seen := serveImages(t, jsonAnswer(200, `{"data":[{"b64_json":"`+b64()+`"}]}`))
	image, err := generateAt(OpenAIImages, base, "gpt-image-2", Wide)
	if err != nil || image.Mime != "image/png" || image.Width != 4 || image.Height != 3 {
		t.Fatal(image, err)
	}
	req := <-seen
	if req.line != "POST /images/generations" || req.header.Get("Authorization") != "Bearer sk-test" ||
		req.body["size"] != "1536x1024" || req.body["quality"] != "medium" || req.body["n"] != 1.0 {
		t.Fatal(req)
	}
	// GPT image models refuse it.
	if _, ok := req.body["response_format"]; ok {
		t.Fatal(req.body)
	}
}

func TestOpenAIModerationIsARefusalWithItsReason(t *testing.T) {
	base, _ := serveImages(t, jsonAnswer(400, `{"error":{"code":"moderation_blocked","type":"image_generation_user_error","message":"Your request was rejected by the safety system."}}`))
	if _, err := generateAt(OpenAIImages, base, "gpt-image-2", Square); err != (ImageError{ImageRefused, "Your request was rejected by the safety system."}) {
		t.Fatal(err)
	}
}

func TestACompatibleServerIsAskedForBase64AndMayAnswerWithALink(t *testing.T) {
	pictures, _ := serveImages(t, imageAnswer{200, "image/png", picture()})
	base, seen := serveImages(t, jsonAnswer(200, `{"data":[{"url":"`+pictures+`/p.png"}]}`))
	image, err := generateAt(CompatibleImages, base, "flux-dev", Portrait)
	if err != nil || image.Height != 3 {
		t.Fatal(image, err)
	}
	req := <-seen
	if req.body["response_format"] != "b64_json" || req.body["size"] != "1024x1536" || req.body["quality"] != nil {
		t.Fatal(req.body)
	}
}

func TestALinkIsDownloadedAndCheckedToBeAPicture(t *testing.T) {
	pictures, _ := serveImages(t, imageAnswer{200, "image/png", picture()})
	base, seen := serveImages(t, jsonAnswer(200, `{"data":[{"url":"`+pictures+`/a.png"}]}`))
	image, err := generateAt(ZhipuImages, base, "cogview-4-250304", Tall)
	if err != nil || image.Ext != "png" {
		t.Fatal(image, err)
	}
	if req := <-seen; req.body["size"] != "768x1344" || req.body["quality"] != nil {
		t.Fatal(req.body)
	}
}

func TestAPageServedAsAPictureIsTurnedDown(t *testing.T) {
	pages, _ := serveImages(t, imageAnswer{200, "image/png", []byte("<html>expired</html>")})
	base, _ := serveImages(t, jsonAnswer(200, `{"data":[{"url":"`+pages+`/a.png"}]}`))
	if _, err := generateAt(ZhipuImages, base, "glm-image", Square); err == nil || !strings.Contains(err.Error(), "isn't a PNG, JPEG or WebP") {
		t.Fatal(err)
	}
}

func TestALinkThatIsntHTTPSIsNeverOpened(t *testing.T) {
	base, _ := serveImages(t, jsonAnswer(200, `{"data":[{"url":"http://cdn.example.com/a.png"}]}`))
	if _, err := generateAt(ZhipuImages, base, "glm-image", Square); err == nil || !strings.Contains(err.Error(), "isn't an HTTPS address") {
		t.Fatal(err)
	}
}

func TestZhipuGLMImageAsksForHDAnd1301IsARefusal(t *testing.T) {
	base, seen := serveImages(t, jsonAnswer(400, `{"error":{"code":"1301","message":"系统检测到输入或生成内容可能包含不安全或敏感内容"}}`))
	_, err := generateAt(ZhipuImages, base, "glm-image", Landscape)
	var e ImageError
	if !errors.As(err, &e) || e.Kind != ImageRefused || !strings.Contains(e.Why, "敏感") {
		t.Fatal(err)
	}
	if req := <-seen; req.body["quality"] != "hd" || req.body["size"] != "1472x1088" {
		t.Fatal(req.body)
	}
}

func TestZhipuArrearsIsAQuotaError(t *testing.T) {
	base, _ := serveImages(t, jsonAnswer(429, `{"error":{"code":"1113","message":"您的账户已欠费"}}`))
	if _, err := generateAt(ZhipuImages, base, "glm-image", Square); err != (ImageError{ImageQuota, "您的账户已欠费"}) {
		t.Fatal(err)
	}
}

func TestGeminiSendsTheKeyHeaderAndReadsTheInlinePicture(t *testing.T) {
	base, seen := serveImages(t, jsonAnswer(200, `{"candidates":[{"content":{"parts":[{"text":"Here it is."},{"inlineData":{"mimeType":"image/png","data":"`+b64()+`"}}]}}]}`))
	image, err := generateAt(GeminiImages, base, "gemini-nano-banana-2.1", Portrait)
	if err != nil || image.Width != 4 {
		t.Fatal(image, err)
	}
	req := <-seen
	if req.line != "POST /models/gemini-nano-banana-2.1:generateContent" || req.header.Get("x-goog-api-key") != "sk-test" || req.header.Get("Authorization") != "" {
		t.Fatal(req)
	}
	if got := at(req.body, "generationConfig", "imageConfig"); !reflect.DeepEqual(got, map[string]any{"aspectRatio": "3:4", "imageSize": "1K"}) {
		t.Fatal(got)
	}
}

func TestGeminiBlocksAreRefusalsWhetherOfThePromptOrThePicture(t *testing.T) {
	base, _ := serveImages(t,
		jsonAnswer(200, `{"promptFeedback":{"blockReason":"PROHIBITED_CONTENT"}}`),
		jsonAnswer(200, `{"candidates":[{"finishReason":"IMAGE_SAFETY","finishMessage":"Unable to show the generated image.","content":{"parts":[]}}]}`),
		jsonAnswer(200, `{"candidates":[{"finishReason":"STOP","content":{"parts":[{"text":"I can only describe it."}]}}]}`))
	model := "gemini-3.1-flash-lite-image"
	for _, want := range []error{
		ImageError{ImageRefused, "PROHIBITED_CONTENT"},
		ImageError{ImageRefused, "Unable to show the generated image."},
		ImageError{ImageFailed, "The model sent no picture: I can only describe it."},
	} {
		if _, err := generateAt(GeminiImages, base, model, Square); err != want {
			t.Fatal(err)
		}
	}
}

func TestSeedreamAsksForBase64AtItsSmallestSizeAndReadsItsCodes(t *testing.T) {
	base, seen := serveImages(t,
		jsonAnswer(200, `{"data":[{"b64_json":"`+b64()+`","size":"1280x720"}]}`),
		jsonAnswer(400, `{"error":{"code":"OutputImageSensitiveContentDetected","message":"The generated image may contain sensitive content.","type":"BadRequest"}}`),
		jsonAnswer(403, `{"error":{"code":"AccountOverdueError","message":"overdue"}}`))
	model := "doubao-seedream-5-0-flash-260915"
	if _, err := generateAt(SeedreamImages, base, model, Wide); err != nil {
		t.Fatal(err)
	}
	req := <-seen
	// Ark's own default (its mark on the picture) is kept.
	if req.body["size"] != "1280x720" || req.body["response_format"] != "b64_json" || req.body["watermark"] != nil {
		t.Fatal(req.body)
	}
	if _, err := generateAt(SeedreamImages, base, model, Square); err != (ImageError{ImageRefused, "The generated image may contain sensitive content."}) {
		t.Fatal(err)
	}
	if _, err := generateAt(SeedreamImages, base, model, Square); err != (ImageError{ImageQuota, "overdue"}) {
		t.Fatal(err)
	}
}

func TestDashScopeUsesTheSynchronousRouteAndDownloadsTheLink(t *testing.T) {
	pictures, _ := serveImages(t, imageAnswer{200, "image/png", picture()})
	base, seen := serveImages(t,
		jsonAnswer(200, `{"output":{"choices":[{"message":{"role":"assistant","content":[{"image":"`+pictures+`/x.png"}]}}]}}`),
		jsonAnswer(400, `{"request_id":"r","code":"DataInspectionFailed","message":"Input data may contain inappropriate content."}`))
	if _, err := generateAt(DashScopeImages, base, "qwen-image-3.0", Landscape); err != nil {
		t.Fatal(err)
	}
	req := <-seen
	if req.line != "POST /services/aigc/multimodal-generation/generation" || at(req.body, "parameters", "size") != "1472*1104" ||
		at(req.body, "parameters", "prompt_extend") != false ||
		at(first(at(first(at(req.body, "input", "messages")), "content")), "text") != "A lighthouse on a cliff at dusk" {
		t.Fatal(req)
	}
	if _, err := generateAt(DashScopeImages, base, "qwen-image-3.0", Square); err != (ImageError{ImageRefused, "Input data may contain inappropriate content."}) {
		t.Fatal(err)
	}
}

func TestSiliconFlowSendsImageSizeAndReadsImages(t *testing.T) {
	pictures, _ := serveImages(t, imageAnswer{200, "image/png", picture()})
	base, seen := serveImages(t, jsonAnswer(200, `{"images":[{"url":"`+pictures+`/k.png"}],"seed":1}`))
	if _, err := generateAt(SiliconFlowImages, base, "Kwai-Kolors/Kolors", Wide); err != nil {
		t.Fatal(err)
	}
	// Kolors makes no wide picture: the square one.
	if req := <-seen; req.body["image_size"] != "1024x1024" || req.body["size"] != nil {
		t.Fatal(req.body)
	}
}

func TestMiniMaxSendsTheAspectAndReadsItsCodesFromA200(t *testing.T) {
	base, seen := serveImages(t,
		jsonAnswer(200, `{"data":{"image_base64":["`+b64()+`"]},"base_resp":{"status_code":0,"status_msg":"success"}}`),
		jsonAnswer(200, `{"data":{},"base_resp":{"status_code":1026,"status_msg":"input new_sensitive"}}`),
		jsonAnswer(200, `{"base_resp":{"status_code":1008,"status_msg":"insufficient balance"}}`))
	if _, err := generateAt(MiniMaxImages, base, "image-01", Landscape); err != nil {
		t.Fatal(err)
	}
	if req := <-seen; req.line != "POST /image_generation" || req.body["aspect_ratio"] != "4:3" || req.body["response_format"] != "base64" {
		t.Fatal(req)
	}
	if _, err := generateAt(MiniMaxImages, base, "image-01", Square); err != (ImageError{ImageRefused, "input new_sensitive"}) {
		t.Fatal(err)
	}
	if _, err := generateAt(MiniMaxImages, base, "image-01", Square); err != (ImageError{ImageQuota, "insufficient balance"}) {
		t.Fatal(err)
	}
}

func TestAnEndpointMustBeHTTPSUnlessItIsThisComputer(t *testing.T) {
	if _, err := generateAt(CompatibleImages, "http://images.example.com/v1", "m", Square); err == nil || !strings.Contains(err.Error(), "https://") {
		t.Fatal(err)
	}
	for _, ok := range []string{"http://localhost:8080/v1", "http://127.0.0.1:8080/v1", "https://api.example.com/v1"} {
		if err := checkEndpoint(ok); err != nil {
			t.Fatal(ok, err)
		}
	}
}

func TestANonJSONErrorPageIsReportedShortly(t *testing.T) {
	page := "<html>" + strings.Repeat("x", 1000) + "</html>"
	base, _ := serveImages(t, imageAnswer{502, "text/html", []byte(page)})
	_, err := generateAt(OpenAIImages, base, "gpt-image-2", Square)
	var e ImageError
	if !errors.As(err, &e) || e.Kind != ImageFailed {
		t.Fatal("a bad gateway is an ordinary failure", err)
	}
	if !strings.HasPrefix(e.Why, "Image request failed (502") || utf8.RuneCountInString(e.Why) >= 400 {
		t.Fatal(e.Why)
	}
}

func TestEveryAspectHasItsRatio(t *testing.T) {
	for _, ratio := range Aspects {
		if a, ok := ParseAspect(ratio); !ok || a.Ratio() != ratio {
			t.Fatal(ratio)
		}
	}
	if _, ok := ParseAspect("2:1"); ok {
		t.Fatal("2:1")
	}
}

func TestAPictureBeingRedoneGoesWithItsDescriptionWhereTheServiceTakesOne(t *testing.T) {
	reference := &ImageAttachment{Name: "old.png", Mime: "image/png", DataBase64: "aGk="}
	target := func(api ImageAPI) ImageTarget {
		return ImageTarget{API: api, BaseURL: "https://example.com/v1", APIKey: "k", Model: "m"}
	}
	// Gemini: an inline picture before the description.
	gemini := requestFor(target(GeminiImages), "Make her hair red", Square).body
	withReference(GeminiImages, gemini, reference)
	parts := list(at(first(gemini["contents"]), "parts"))
	if at(parts[0], "inlineData", "mimeType") != "image/png" || at(parts[0], "inlineData", "data") != "aGk=" || at(parts[1], "text") != "Make her hair red" {
		t.Fatal(parts)
	}
	// Seedream: `image`, as a data URL.
	seedream := requestFor(target(SeedreamImages), "Make her hair red", Square).body
	withReference(SeedreamImages, seedream, reference)
	if seedream["image"] != "data:image/png;base64,aGk=" {
		t.Fatal(seedream)
	}
	// A service that draws from text only gets the description alone.
	for _, api := range []ImageAPI{OpenAIImages, MiniMaxImages, DashScopeImages} {
		before := requestFor(target(api), "Make her hair red", Square).body
		after := requestFor(target(api), "Make her hair red", Square).body
		withReference(api, after, reference)
		if !reflect.DeepEqual(before, after) {
			t.Fatal(api)
		}
	}
}

func TestOnlyPublicHTTPSAddressesAreDownloaded(t *testing.T) {
	check := func(raw string) string { u, _ := url.Parse(raw); return checkPictureURL(u) }
	if check("https://cdn.example.com/a.png") != "" {
		t.Fatal("a public https address")
	}
	for _, raw := range []string{
		"http://cdn.example.com/a.png", "https://user:pw@cdn.example.com/a.png", "https://10.0.0.8/a.png", "https://[::1]/a.png",
		"https://[::ffff:192.168.0.1]/a.png", "https://169.254.169.254/latest/meta-data/", "https://localhost/a.png",
		"https://printer.local/a.png", "ftp://cdn.example.com/a.png", "file:///etc/passwd",
	} {
		if check(raw) == "" {
			t.Error(raw)
		}
	}
}

// DNS rebinding: a name is checked by what it resolves to, and the
// connection uses those same addresses.
func TestANameResolvingToAPrivateAddressIsRefused(t *testing.T) {
	if _, err := publicDial(context.Background(), "tcp", "localhost:443"); err == nil {
		t.Fatal("localhost dialled")
	}
}

func TestAnOSSLinkWrittenAsHTTPIsFetchedOverHTTPS(t *testing.T) {
	u, _ := url.Parse("http://x.oss-cn-beijing.aliyuncs.com/a.png?e=1")
	if got := upgraded(u).String(); got != "https://x.oss-cn-beijing.aliyuncs.com/a.png?e=1" {
		t.Fatal(got)
	}
	other, _ := url.Parse("http://cdn.example.com/a.png")
	if upgraded(other).Scheme != "http" {
		t.Fatal(other)
	}
}

func TestPNGJPEGAndWebPAreReadFromTheirHeaders(t *testing.T) {
	if got, ok := sniff(pngOf(2, 3)); !ok || got != (sniffed{"image/png", "png", 2, 3}) {
		t.Fatal(got)
	}
	// SOI, an APP0 segment, then SOF0 with height 0x0300, width 0x0400.
	jpeg := []byte{0xff, 0xd8, 0xff, 0xe0, 0, 4, 0, 0, 0xff, 0xc0, 0, 11, 8, 0x03, 0x00, 0x04, 0x00, 3}
	if got, ok := sniff(jpeg); !ok || got.ext != "jpg" || got.width != 1024 || got.height != 768 {
		t.Fatal(got)
	}
	webp := append([]byte("RIFF\x00\x00\x00\x00WEBPVP8X\x0a\x00\x00\x00\x00\x00\x00\x00"), 0xff, 0x03, 0x00, 0xff, 0x02, 0x00)
	if got, ok := sniff(webp); !ok || got.mime != "image/webp" || got.width != 1024 || got.height != 768 {
		t.Fatal(got)
	}
}

func TestAnythingElseIsNotAnImage(t *testing.T) {
	for _, b := range [][]byte{
		[]byte("<svg xmlns='http://www.w3.org/2000/svg'/>"), []byte("<!doctype html><title>404</title>"), nil,
		// Cut off before the size.
		pngOf(2, 3)[:18], pngOf(0, 3),
	} {
		if got, ok := sniff(b); ok {
			t.Fatal(got)
		}
	}
}

func TestAChatGPTPictureRequestOffersOnlyTheImageToolAtTheAspectsSize(t *testing.T) {
	body := codexImageBody("A harbour at dusk", Wide, nil)
	tool := first(body["tools"])
	if at(tool, "type") != "image_generation" || at(tool, "size") != "1536x1024" || at(body["tool_choice"], "type") != "image_generation" ||
		body["stream"] != true || body["store"] != false ||
		at(first(at(first(body["input"]), "content")), "text") != "A harbour at dusk\n\nAspect ratio 16:9, 1536x1024 pixels." {
		t.Fatal(body)
	}
}

func TestAChatGPTPictureBeingRedoneGoesInAsAnInputImage(t *testing.T) {
	body := codexImageBody("Make her hair red", Square, &ImageAttachment{Name: "old.jpg", Mime: "image/jpeg", DataBase64: "aGk="})
	content := list(at(first(body["input"]), "content"))
	if at(content[0], "type") != "input_image" || at(content[0], "image_url") != "data:image/jpeg;base64,aGk=" ||
		!strings.HasPrefix(strAt(content[1], "text"), "Make her hair red") {
		t.Fatal(content)
	}
}

func TestTheChatGPTPictureIsReadFromTheImageGenerationCall(t *testing.T) {
	// "hi" in base64, beside a message item.
	got, err := codexImageFrom([]any{map[string]any{"type": "message", "content": []any{}}, map[string]any{"type": "image_generation_call", "status": "completed", "result": "aGk="}})
	if err != nil || string(got) != "hi" {
		t.Fatal(got, err)
	}
}

func TestNoChatGPTPictureSaysWhy(t *testing.T) {
	if _, err := codexImageFrom([]any{map[string]any{"type": "message"}}); err == nil || !strings.Contains(err.Error(), "plan") {
		t.Fatal(err)
	}
	if _, err := codexImageFrom([]any{map[string]any{"type": "image_generation_call", "status": "failed", "result": ""}}); err == nil || !strings.Contains(err.Error(), "failed") {
		t.Fatal(err)
	}
}

func TestChatGPTPictureFailuresAreToldApart(t *testing.T) {
	for message, want := range map[string]ImageErrorKind{
		"ChatGPT request failed: moderation_blocked":        ImageRefused,
		"ChatGPT request failed (429): usage limit reached": ImageQuota,
		"ChatGPT request failed (500)":                      ImageFailed,
	} {
		if err := codexImageFailure(message); err.(ImageError).Kind != want {
			t.Fatal(message, err)
		}
	}
}

func TestAChatGPTPictureComesFromTheStream(t *testing.T) {
	var body map[string]any
	var header http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header = r.Header
		json.UnmarshalRead(r.Body, &body)
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte("data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"image_generation_call\",\"status\":\"completed\",\"result\":\"" + b64() + "\"}}\n\n" +
			"data: {\"type\":\"response.completed\",\"response\":{}}\n\n"))
	}))
	defer server.Close()
	defer func(was string) { CodexResponsesURL = was }(CodexResponsesURL)
	CodexResponsesURL = server.URL + "/codex/responses"
	image, err := CodexImage(context.Background(), http.DefaultClient, "tok", "acct", "textus", "A harbour", Square, nil)
	if err != nil || image.Width != 4 || header.Get("chatgpt-account-id") != "acct" || header.Get("Authorization") != "Bearer tok" || body["model"] != CodexCompletionModel {
		t.Fatal(image, err, header)
	}
}
