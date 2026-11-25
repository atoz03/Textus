package backend

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"textus/backend/ai"
	"textus/backend/ai/aicompat"
)

// A 1x1 PNG.
const onePixelPNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg=="

func TestMadePicturesAreKeptBesideAndComeBack(t *testing.T) {
	pictures := picturesDir(filepath.Join(t.TempDir(), "story.md.json"))
	png, _ := base64.StdEncoding.DecodeString(onePixelPNG)
	made, err := aicompat.CheckedImage(png)
	if err != nil {
		t.Fatal(err)
	}
	ai.RestorePicture("kpic01", made)
	keepPictures(pictures, []string{"kpic01", "kpic02"})
	// The made one only; the other on a later write.
	if kept := keptPictures(pictures); len(kept) != 1 || kept["kpic01"] == "" {
		t.Fatal(kept)
	}
	// Gone with the app, back from the file.
	ai.DiscardPicture("kpic01")
	restorePictures(pictures)
	if bytes, _, ok := ai.ReadyPicture("kpic01"); !ok || string(bytes) != string(png) {
		t.Fatal("not back")
	}
	// Let go of, the folder goes too - nothing else in it is touched.
	keepPictures(pictures, nil)
	if _, err := os.Stat(pictures); !os.IsNotExist(err) {
		t.Fatal("the folder stayed")
	}
	ai.DiscardPicture("kpic01")
}

func TestARedonePictureIsReadFromTheDocumentFolder(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "assets"), 0o755)
	// The smallest PNG: its signature is all readImage looks at.
	os.WriteFile(filepath.Join(dir, "assets/rain.png"), []byte("\x89PNG\r\n\x1a\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "assets/my rain.png"), []byte("\x89PNG\r\n\x1a\n"), 0o644)
	read := func(markdown string) string {
		src, ok := ai.PictureSrc(markdown)
		if !ok {
			return ""
		}
		picture, _ := localPicture(dir, src)
		return picture.Mime
	}
	for _, markdown := range []string{"![Rain](assets/rain.png)", `![Rain](<assets/rain.png> "title")`, `<img src="assets/rain.png" width="320">`, "![Rain](assets/my%20rain.png)"} {
		if read(markdown) != "image/png" {
			t.Fatal(markdown)
		}
	}
	// On the web, in the text, or not a picture at all: nothing to send.
	for _, markdown := range []string{"![Rain](https://example.com/rain.png)", "![Rain](../outside.png)", "Rain on the roof."} {
		if read(markdown) != "" {
			t.Fatal(markdown)
		}
	}
}

func TestTheImageModelNeedsItsKeyOrTheSignIn(t *testing.T) {
	a := agentApp(t, nil)
	if _, ok := a.imageRun(&ai.ImageModel{Provider: "google"}, "deepseek", "", "r"); ok {
		t.Fatal("made pictures without a key")
	}
	if _, ok := a.imageRun(&ai.ImageModel{Provider: ai.FollowChat}, "openai", "", "r"); ok {
		t.Fatal("followed a chat provider with no key and no sign-in")
	}
	a.changeKeys(func(keys map[string]string) bool { keys["google"] = "g-key"; return true })
	if _, ok := a.imageRun(&ai.ImageModel{Provider: ai.FollowChat}, "google", "", "r"); !ok {
		t.Fatal("Gemini's key not used for its pictures")
	}
	// The compatible endpoint on this computer needs no key, only its
	// address.
	if _, ok := a.imageRun(&ai.ImageModel{Provider: "custom", Model: "flux"}, "deepseek", "", "r"); ok {
		t.Fatal("no address")
	}
	if _, ok := a.imageRun(&ai.ImageModel{Provider: "custom", Model: "flux", BaseURL: "http://127.0.0.1:9/v1"}, "deepseek", "", "r"); !ok {
		t.Fatal("a local endpoint refused")
	}
	if _, ok := a.imageRun(nil, "google", "", "r"); ok {
		t.Fatal("no image model")
	}
}

func TestTheImageServicesAreListed(t *testing.T) {
	a := agentApp(t, nil)
	got, err := run(a, "list_image_providers", `{}`)
	if err != nil || !strings.Contains(got, `"id":"openai-chatgpt"`) || !strings.Contains(got, `"signInFor":"openai"`) || !strings.Contains(got, `"baseUrl":null`) {
		t.Fatal(got, err)
	}
}

// pngOf is a PNG's signature and IHDR, all a picture's size is read from.
func pngOf(width, height uint32) []byte {
	b := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\x0dIHDR")
	b = binary.BigEndian.AppendUint32(b, width)
	b = binary.BigEndian.AppendUint32(b, height)
	return append(b, 8, 6, 0, 0, 0, 0, 0, 0, 0)
}

func TestAPictureWrittenIntoProposedTextIsMadeAndPlaced(t *testing.T) {
	var mu sync.Mutex
	var bodies []map[string]any
	var drawn []map[string]any
	picture := base64.StdEncoding.EncodeToString(pngOf(4, 3))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.UnmarshalRead(r.Body, &body)
		mu.Lock()
		defer mu.Unlock()
		if r.URL.Path == "/images/v1/images/generations" {
			drawn = append(drawn, body)
			w.Write([]byte(`{"data":[{"b64_json":"` + picture + `"}]}`))
			return
		}
		bodies = append(bodies, body)
		if len(bodies) == 1 {
			edit, _ := json.Marshal(map[string]any{"edits": []any{map[string]any{"action": "append", "text": "The harbour at dawn.\n\n![港口](textus-picture:16:9 A harbour at dawn)\n\nThe boats came in."}}})
			args, _ := json.Marshal(string(edit))
			sse(w, `{"choices":[{"delta":{"content":"Added a scene with a picture."}}]}`,
				`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c1","type":"function","function":{"name":"propose_edit","arguments":`+string(args)+`}}]}}]}`,
				`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`)
			return
		}
		sse(w, `{"choices":[{"delta":{"content":"Done."}}]}`, `{"choices":[{"delta":{},"finish_reason":"stop"}]}`)
	}))
	defer server.Close()
	a := agentApp(t, server)
	var events []ai.StreamEvent
	turns, err := a.agentMessage(context.Background(), messageRequest{
		Provider: "custom", Blocks: []ai.SentBlock{{ID: "b1", Markdown: "A short draft."}}, Message: "Add a harbour scene with a picture.",
		Mode: ai.ModeWrite, RequestID: "pic1", ImageModel: &ai.ImageModel{Provider: "custom", Model: "flux", BaseURL: server.URL + "/images/v1"},
	}, func(e ai.StreamEvent) { mu.Lock(); events = append(events, e); mu.Unlock() })
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	if names := toolNamesOf(bodies[0]); !slices.Contains(names, "generate_image") {
		t.Fatal(names)
	}
	mu.Unlock()
	// The line became the picture's own Markdown in the proposal.
	var call aicompat.AgentTurn
	for _, turn := range turns {
		if turn.Kind == aicompat.ToolCalled && turn.Name == "propose_edit" {
			call = turn
		}
	}
	args, _ := ai.ParseArgs(call.Arguments)
	text := args.StrOr("text")
	at := strings.Index(text, "textus-generated:")
	if at < 0 || strings.Contains(text, "textus-picture:") || !strings.Contains(text, "![港口](textus-generated:") {
		t.Fatal(call.Arguments)
	}
	id := text[at+len("textus-generated:") : at+len("textus-generated:")+6]
	// The frontend was told as the line streamed in.
	mu.Lock()
	told := slices.ContainsFunc(events, func(e ai.StreamEvent) bool { return e.Type == "picture" && e.ID == id && e.Width == 1536 })
	mu.Unlock()
	if !told {
		t.Fatal(events)
	}
	got, err := run(a, "ai_generated_image", `{"id":"`+id+`"}`)
	if err != nil || !strings.Contains(got, `"width":4`) || !strings.Contains(got, `"mime":"image/png"`) {
		t.Fatal(got, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(drawn) != 1 || drawn[0]["prompt"] != "A harbour at dawn" || drawn[0]["size"] != "1536x1024" {
		t.Fatal(drawn)
	}
	run(a, "ai_discard_generated_image", `{"id":"`+id+`"}`)
	if _, err := run(a, "ai_generated_image", `{"id":"`+id+`"}`); err == nil || !strings.Contains(err.Error(), "no longer available") {
		t.Fatal(err)
	}
}
