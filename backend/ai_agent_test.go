package backend

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"textus/backend/ai"
	"textus/backend/ai/aicompat"
)

// agentApp is an app with Textus AI on and its folders in a test's own,
// whose custom endpoint is server.
func agentApp(t *testing.T, server *httptest.Server) *App {
	t.Helper()
	a := &App{dirs: Dirs{Config: t.TempDir(), Data: t.TempDir(), Cache: t.TempDir()}, defaults: map[string]bool{"textusAi": true}}
	a.stores = loadStores(a.dirs)
	if server != nil {
		a.changeEndpoints(func([]ai.CustomEndpoint) []ai.CustomEndpoint {
			return []ai.CustomEndpoint{{ID: "custom", BaseURL: server.URL + "/v1", Model: "m"}}
		})
	}
	return a
}

// sse is a streamed reply of these chunks.
func sse(w http.ResponseWriter, chunks ...string) {
	w.Header().Set("Content-Type", "text/event-stream")
	for _, chunk := range chunks {
		io.WriteString(w, "data: "+chunk+"\n\n")
	}
	io.WriteString(w, "data: [DONE]\n\n")
}

func TestAMessageReadsAFileThroughItsToolAndStreamsWhatItDoes(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "notes.md"), []byte("Tides rise twice a day."), 0o644)
	var mu sync.Mutex
	var bodies []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.UnmarshalRead(r.Body, &body)
		mu.Lock()
		bodies = append(bodies, body)
		n := len(bodies)
		mu.Unlock()
		if n == 1 {
			sse(w, `{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c1","type":"function","function":{"name":"read_file","arguments":""}}]}}]}`,
				`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"path\":\"notes.md\"}"}}]}}]}`,
				`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`)
			return
		}
		sse(w, `{"choices":[{"delta":{"content":"The notes say "}}]}`, `{"choices":[{"delta":{"content":"tides."}}]}`,
			`{"choices":[{"delta":{},"finish_reason":"stop"}]}`)
	}))
	defer server.Close()
	a := agentApp(t, server)
	var events []string
	turns, err := a.agentMessage(context.Background(), messageRequest{
		Provider: "custom", Blocks: []ai.SentBlock{{ID: "b1", Markdown: "A short draft."}}, WorkspaceRoot: &root,
		Message: "What do my notes say?", Mode: ai.ModeWrite, RequestID: "r1",
	}, func(e ai.StreamEvent) {
		mu.Lock()
		defer mu.Unlock()
		raw, _ := e.MarshalJSON()
		events = append(events, string(raw))
	})
	if err != nil {
		t.Fatal(err)
	}
	kinds := func() (out []string) {
		for _, turn := range turns {
			out = append(out, string(turn.Kind))
		}
		return
	}()
	if !slices.Equal(kinds, []string{"User", "ToolCall", "ToolResult", "Assistant"}) || turns[0].Text != "What do my notes say?" ||
		turns[1].Name != "read_file" || turns[2].Output != "Tides rise twice a day." || turns[3].Text != "The notes say tides." {
		t.Fatalf("%+v", turns)
	}
	// The document is shown whole, so it has no tools to read it; the
	// folder's are offered, and the second step hears what the file says.
	var offered []string
	for _, tool := range bodies[0]["tools"].([]any) {
		offered = append(offered, tool.(map[string]any)["function"].(map[string]any)["name"].(string))
	}
	if slices.Contains(offered, "read_document") || !slices.Contains(offered, "read_file") || !slices.Contains(offered, "search_conversations") {
		t.Error(offered)
	}
	if raw, _ := json.Marshal(bodies[1]["messages"]); !strings.Contains(string(raw), "Tides rise twice a day.") {
		t.Error(string(raw))
	}
	shown := strings.Join(events, "\n")
	for _, want := range []string{`"type":"toolStart"`, `"name":"read_file"`, `{"type":"turn","turn":{"kind":"ToolResult"`, `"type":"delta"`} {
		if !strings.Contains(shown, want) {
			t.Errorf("no %s in\n%s", want, shown)
		}
	}
}

func TestAServerThatRefusesToolsIsAnsweredInOnePlainCompletion(t *testing.T) {
	var plain map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.UnmarshalRead(r.Body, &body)
		if body["tools"] != nil {
			http.Error(w, `{"error":{"message":"tools are not supported by this model"}}`, http.StatusBadRequest)
			return
		}
		plain = body
		w.Write([]byte(`{"choices":[{"message":{"content":"Plain answer."}}]}`))
	}))
	defer server.Close()
	a := agentApp(t, server)
	turns, err := a.agentMessage(context.Background(), messageRequest{
		Provider: "custom", Blocks: []ai.SentBlock{{ID: "b1", Markdown: "Draft."}},
		History: []aicompat.AgentTurn{aicompat.UserTurn("Hi"), aicompat.AssistantTurn("Hello.")}, Message: "Shorten it.",
		Mode: ai.ModeWrite, RequestID: "r2",
	}, func(ai.StreamEvent) {})
	if err != nil || len(turns) != 2 || turns[0].Text != "Shorten it." || turns[1].Text != "Plain answer." {
		t.Fatal(turns, err)
	}
	if raw, _ := json.Marshal(plain["messages"]); !strings.Contains(string(raw), `User: Hi\n\nAssistant: Hello.\n\nUser: Shorten it.`) {
		t.Error(string(raw))
	}
}

func TestAPictureGoesOnlyToAModelThatSeesIt(t *testing.T) {
	a := agentApp(t, nil)
	_, err := a.agentMessage(context.Background(), messageRequest{
		Provider: "deepseek", Message: "What is this?", Mode: ai.ModeReview, RequestID: "r3",
		Images: []aicompat.ImageAttachment{{Name: "a.png", Mime: "image/png", DataBase64: "AAAA"}},
	}, func(ai.StreamEvent) {})
	if err == nil || err.Error() != "DeepSeek can't read images. Remove the image, or switch provider in Settings > Textus AI." {
		t.Fatal(err)
	}
	if _, err := a.agentMessage(context.Background(), messageRequest{Provider: "nope", RequestID: "r4"}, func(ai.StreamEvent) {}); err == nil || err.Error() != "unknown provider: nope" {
		t.Fatal(err)
	}
}

func TestASelectedPictureIsReadFromTheDocumentsFolder(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "assets"), 0o755)
	os.WriteFile(filepath.Join(dir, "assets", "my boats.png"), []byte("png"), 0o644)
	doc := filepath.Join(dir, "draft.md")
	image, ok := selectedPicture("<selected-text>\n![Boats](assets/my%20boats.png)\n</selected-text>\n\nWhat is in it?", doc)
	if !ok || image.Name != "assets/my boats.png" || image.Mime != "image/png" || image.DataBase64 != "cG5n" {
		t.Fatal(image, ok)
	}
	if _, ok := selectedPicture("<selected-text>\n![Boats](../elsewhere.png)\n</selected-text>\n\nWhat is in it?", doc); ok {
		t.Fatal("a picture outside the folder was read")
	}
}

func TestStopEndsARequestAtOnceAndItsWorkWithIt(t *testing.T) {
	stopped := make(chan struct{})
	done := make(chan error)
	go func() {
		_, err := cancellable(context.Background(), "stop-me", func(ctx context.Context) (int, error) {
			<-ctx.Done()
			close(stopped)
			return 0, ctx.Err()
		})
		done <- err
	}()
	for {
		running.Lock()
		_, ok := running.stops["stop-me"]
		running.Unlock()
		if ok {
			break
		}
		time.Sleep(time.Millisecond)
	}
	cancelRequest("stop-me")
	if err := <-done; err == nil || err.Error() != cancelled {
		t.Fatal(err)
	}
	<-stopped
	running.Lock()
	defer running.Unlock()
	if len(running.stops) != 0 {
		t.Fatal("a finished request is still registered")
	}
}

func TestARequestThatEndsOnItsOwnKeepsItsAnswer(t *testing.T) {
	got, err := cancellable(context.Background(), "quick", func(context.Context) (string, error) { return "done", nil })
	if err != nil || got != "done" {
		t.Fatal(got, err)
	}
	if _, err := cancellable(context.Background(), "failing", func(context.Context) (int, error) { return 0, errors.New("refused") }); err == nil || err.Error() != "refused" {
		t.Fatal(err)
	}
	cancelRequest("quick") // nothing running: nothing happens
}

func TestARunningRequestTakesMessagesAndAClosedOneRefusesThem(t *testing.T) {
	if steer("r1", "early") {
		t.Fatal("a request not running took a message")
	}
	box := openInbox("r1")
	if !steer("r1", "one") || !steer("r1", "two") {
		t.Fatal("refused")
	}
	if got := box.take(); !slices.Equal(got, []string{"one", "two"}) || len(box.take()) != 0 {
		t.Fatal(got)
	}
	box.close()
	if steer("r1", "late") {
		t.Fatal("a closed request took a message")
	}
}

func TestEarlierConversationsAreShownWithWhoSaidItWhen(t *testing.T) {
	hits := []string{`{"id":"a","title":"Lighthouse","updatedAt":1760000000000,"role":"user","snippet":"Call her Lin."}`}
	var raw []jsontext.Value
	for _, h := range hits {
		raw = append(raw, jsontext.Value(h))
	}
	text := describeConversations("Lin", raw)
	if !strings.Contains(text, `2025-10-09 "Lighthouse" - the user said: Call her Lin.`) {
		t.Fatal(text)
	}
	if !strings.HasPrefix(describeConversations("Lin", nil), "No earlier conversation") {
		t.Fatal("no hits")
	}
	if conversationDate(jsontext.Value("0")) != "1970-01-01" || conversationDate(jsontext.Value("951782400000")) != "2000-02-29" {
		t.Fatal("dates")
	}
}
