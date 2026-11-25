package ai

import (
	"context"
	"encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"slices"
	"testing"
)

func TestEveryProviderWritesAPaperAFewCallsAtOnce(t *testing.T) {
	for _, p := range Catalog {
		if n, ok := paperAtOnce[p.ID]; !ok || n < 1 {
			t.Errorf("%s: %d calls at once", p.ID, n)
		}
	}
	if custom, _ := Find("custom:mine"); custom.PaperAtOnce() != 3 {
		t.Fatal(custom.PaperAtOnce())
	}
}

func TestTheCatalogIsTheFrontendsCatalog(t *testing.T) {
	raw, err := os.ReadFile("../../src/platform/bindings/ai.ts")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?m)^export const PROVIDER_CATALOG = (\[.*\]) as const;$`).FindSubmatch(raw)
	if m == nil {
		t.Fatal("bindings/ai.ts has no PROVIDER_CATALOG")
	}
	var frontend []Provider
	if err := json.Unmarshal(m[1], &frontend); err != nil {
		t.Fatal(err)
	}
	a, _ := json.Marshal(frontend)
	b, _ := json.Marshal(Catalog)
	if string(a) != string(b) {
		t.Errorf("backend/ai/catalog.json differs from PROVIDER_CATALOG:\n%s\n%s", b, a)
	}
	if p, ok := Find("custom:1a2b3c4d"); !ok || p.ID != "custom" {
		t.Error("a custom endpoint's id finds no entry")
	}
	if _, ok := Find("customer"); ok {
		t.Error("an unknown id was found")
	}
}

func TestVendorsAreRecognizedByHost(t *testing.T) {
	for url, want := range map[string]Vendor{
		"https://ark.cn-beijing.volces.com/api/v3":  Ark,
		"https://ark.cn-shanghai.volces.com/api/v3": Ark,
		"https://api.deepseek.com/v1":               DeepSeek,
		"https://api.minimaxi.com/v1":               MiniMax,
		"https://api.minimax.io/v1":                 MiniMax,
		"https://api.moonshot.cn/v1":                Generic,
	} {
		if got := ChatVendor(url); got != want {
			t.Errorf("%s: %v", url, got)
		}
	}
}

func TestTidyDropsNonChatModelsAndSortsNewestFirst(t *testing.T) {
	got := tidy([]listed{
		{"models/gemini-3.8-flash", "0300", true},
		{"text-embedding-3-large", "0100", true},
		{"gpt-image-2", "0400", true},
		{"gpt-6-luna", "0350", true},
		{"whisper-1", "0200", true},
		{"gpt-6-luna", "0350", true},
	})
	if !slices.Equal(got, []string{"gpt-6-luna", "gemini-3.8-flash"}) {
		t.Error(got)
	}
	// Without dates, the provider's order.
	if got := tidy([]listed{{id: "b"}, {id: "a"}}); !slices.Equal(got, []string{"b", "a"}) {
		t.Error(got)
	}
	if got := tidy(nil); got == nil || len(got) != 0 {
		t.Error("no models is not an empty list")
	}
}

func TestEachServerGetsTheKeyAsItTakesIt(t *testing.T) {
	for base, want := range map[string][2]string{
		"https://generativelanguage.googleapis.com/v1beta/openai": {"Authorization", "Bearer k"},
		"https://aiplatform.googleapis.com/v1":                    {"x-goog-api-key", "k"},
		"https://opencode.ai/zen/go/v1":                           {"Authorization", "Bearer k"},
		"https://api.deepseek.com/v1":                             {"Authorization", "Bearer k"},
	} {
		req, _ := http.NewRequest("GET", base, nil)
		WithKey(req, base, "k")
		if req.Header.Get(want[0]) != want[1] {
			t.Errorf("%s: %v", base, req.Header)
		}
	}
	session := func() string {
		req, _ := http.NewRequest("GET", "https://opencode.ai/", nil)
		WithKey(req, "https://opencode.ai/zen/go/v1", "k")
		return req.Header.Get("x-opencode-session")
	}
	if s := session(); s == "" || s != session() {
		t.Error("OpenCode got no session, or a new one each request")
	}
}

func TestAModelListIsAskedWithTheKey(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" || r.Header.Get("Authorization") != "Bearer sk" {
			http.Error(w, `{"error":"bad key"}`, http.StatusUnauthorized)
			return
		}
		w.Write([]byte(`{"data":[{"id":"old","created":1},{"id":"new","created":2},{"id":"tts-1","created":3}]}`))
	}))
	defer server.Close()
	key := "sk"
	got, err := ListModels(context.Background(), server.Client(), server.URL+"/v1/", &key)
	if err != nil || !slices.Equal(got, []string{"new", "old"}) {
		t.Error(got, err)
	}
	if _, err := ListModels(context.Background(), server.Client(), server.URL+"/v1", nil); err == nil ||
		err.Error() != `Could not list models (401 Unauthorized): {"error":"bad key"}`+"\n" {
		t.Errorf("%q", err)
	}
}

func TestAnthropicsModelsAreListedPageByPage(t *testing.T) {
	pages := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pages++
		if r.Header.Get("x-api-key") != "sk-ant" || r.Header.Get("anthropic-version") == "" {
			http.Error(w, "unsigned", http.StatusUnauthorized)
			return
		}
		if r.URL.Query().Get("after_id") == "" {
			w.Write([]byte(`{"data":[{"id":"claude-a","created_at":"2026-01-01"}],"has_more":true,"last_id":"claude-a"}`))
			return
		}
		w.Write([]byte(`{"data":[{"id":"claude-b","created_at":"2026-06-01"}],"has_more":false,"last_id":"claude-b"}`))
	}))
	defer server.Close()
	anthropicAPI = server.URL
	defer func() { anthropicAPI = "https://api.anthropic.com" }()
	got, err := ListAnthropicModels(context.Background(), server.Client(), AnthropicAuth{APIKey: "sk-ant"})
	if err != nil || pages != 2 || !slices.Equal(got, []string{"claude-b", "claude-a"}) {
		t.Error(got, err, pages)
	}
}

func TestTheSingleEndpointFileBecomesTheFirstEntry(t *testing.T) {
	endpoints, ok := ParseEndpoints([]byte(`{"base_url":"https://relay.example/v1","api_key":"sk-old","model":"gpt-6-luna"}`))
	if !ok || len(endpoints) != 1 || endpoints[0].ID != "custom" || *endpoints[0].APIKey != "sk-old" || endpoints[0].Model != "gpt-6-luna" {
		t.Fatalf("%+v", endpoints)
	}
	// Written back as a list, it reads the same.
	raw, _ := EndpointsJSON(endpoints)
	again, ok := ParseEndpoints(raw)
	if !ok || *again[0].APIKey != "sk-old" {
		t.Errorf("%s", raw)
	}
	for _, broken := range []string{"{", `{"model":"m"}`, `[]`} {
		if _, ok := ParseEndpoints([]byte(broken)); ok {
			t.Errorf("%s was read", broken)
		}
	}
}

func ptr(s string) *string { return &s }

func TestTheStoredKeyOnlyGoesToTheURLItWasSavedFor(t *testing.T) {
	stored := &CustomEndpoint{ID: "custom", BaseURL: "https://api.example/v1", APIKey: ptr("sk-stored")}
	key := func(k *string) string {
		if k == nil {
			return "<none>"
		}
		return *k
	}
	for _, c := range []struct {
		stored *CustomEndpoint
		url    string
		typed  *string
		want   string
	}{
		{stored, "https://api.example/v1/", nil, "sk-stored"},
		{stored, "https://api.example/v1", ptr(" "), "sk-stored"},
		{stored, "http://other.example/v1", nil, "<none>"},
		{stored, "http://other.example/v1", ptr("sk-new"), "sk-new"},
		{nil, "https://api.example/v1", nil, "<none>"},
	} {
		if got := key(ResolveEndpointKey(c.stored, c.url, c.typed)); got != c.want {
			t.Errorf("%s %v: %s", c.url, c.typed, got)
		}
	}
}

func TestABlankKeyKeepsOnlyThatEndpointsOwnKey(t *testing.T) {
	list := []CustomEndpoint{
		{ID: "custom", BaseURL: "https://a.example/v1", APIKey: ptr("sk-a"), Model: "m"},
		{ID: "custom:b", BaseURL: "https://b.example/v1", APIKey: ptr("sk-b"), Model: "m"},
	}
	saved, id := SaveEndpoint(list, EndpointEdit{ID: "custom:b", Name: " Relay ", BaseURL: "https://b.example/v1", Model: "m2"},
		func() string { panic("no new id for a known endpoint") })
	if id != "custom:b" || len(saved) != 2 || *saved[1].APIKey != "sk-b" || saved[1].Name != "Relay" || saved[0] != list[0] {
		t.Errorf("%+v", saved)
	}
	// A new endpoint at another's address doesn't take that one's key.
	saved, id = SaveEndpoint(list, EndpointEdit{BaseURL: "https://a.example/v1", Model: "m"}, func() string { return "custom:new" })
	if id != "custom:new" || len(saved) != 3 || saved[2].APIKey != nil {
		t.Errorf("%+v", saved)
	}
	if !IsCustomID("custom") || !IsCustomID(NewCustomID()) || IsCustomID("openai") || IsCustomID("customer") {
		t.Error("custom ids misread")
	}
}
