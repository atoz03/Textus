package web

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// sent is a request as the test server got it.
type sent struct {
	method, uri string
	header      http.Header
	body        string
}

// serve answers each request with status and body, and hands over the
// request it got.
func serve(t *testing.T, status, body string) (string, chan sent) {
	t.Helper()
	code, _ := strconv.Atoi(strings.Fields(status)[0])
	got := make(chan sent, 8)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		got <- sent{r.Method, r.URL.RequestURI(), r.Header, string(raw)}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	return server.URL + "/search", got
}

func runSearch(t *testing.T, b Backend, status, body string) ([]Hit, error, sent) {
	t.Helper()
	endpoint, got := serve(t, status, body)
	s := &Service{Backend: b, Credential: "test-key", Endpoint: endpoint}
	hits, err := s.Search(context.Background(), "tide tables", 8, "When are spring tides?")
	return hits, err, <-got
}

func TestDeepSeekSendsTheKeyAndTheQuestionAndReadsResults(t *testing.T) {
	hits, err, req := runSearch(t, deepseek, "200 OK", `{"results":[{"title":"Tides","url":"https://sea.example/tides","snippet":"Twice a day.","summary":"long","site_name":"Sea","date":"2026-02-02T16:00:00+00:00"}]}`)
	if req.method != "POST" || req.uri != "/search" || req.header.Get("Authorization") != "Bearer test-key" ||
		!strings.Contains(req.body, `"ori_question":"When are spring tides?"`) {
		t.Fatal(req)
	}
	want := []Hit{{Title: "Tides", URL: "https://sea.example/tides", Snippet: "Twice a day.", Date: "2026-02-02T16:00:00+00:00", Site: "Sea"}}
	if err != nil || !reflect.DeepEqual(hits, want) {
		t.Fatal(hits, err)
	}
}

func TestDeepSeekCutsALongQueryAndLeavesOutAnEmptyQuestion(t *testing.T) {
	endpoint, got := serve(t, "200 OK", `{"results":[]}`)
	s := &Service{Backend: deepseek, Credential: "test-key", Endpoint: endpoint}
	if _, err := s.Search(context.Background(), strings.Repeat("潮", 200), 8, "  "); err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	json.Unmarshal([]byte((<-got).body), &body)
	if body["query"] != strings.Repeat("潮", 128) {
		t.Fatal(body["query"])
	}
	if _, ok := body["ori_question"]; ok {
		t.Fatal(body)
	}
}

// The shape DeepSeek's fetch really answers in: every punctuation mark
// escaped, code included.
func TestDeepSeekFetchReadsThePageWithoutItsEscapes(t *testing.T) {
	endpoint, got := serve(t, "200 OK", `{"title":"Rust Blog","content":"\\#\\# Posts\n\nOct\\. 15 \\| Announcing Rust 1\\.99\\.1\nfn main\\(\\) \\{ \\}\nC:\\\\Users\\Ada"}`)
	s := &Service{Backend: deepseek, Credential: "test-key", Endpoint: endpoint}
	title, page, err := s.FetchPage(context.Background(), "https://blog.rust-lang.org/")
	req := <-got
	if req.method != "POST" || req.uri != "/search/fetch" || req.body != `{"url":"https://blog.rust-lang.org/"}` {
		t.Fatal(req)
	}
	if err != nil || title != "Rust Blog" || page != "## Posts\n\nOct. 15 | Announcing Rust 1.99.1\nfn main() { }\nC:\\Users\\Ada" {
		t.Fatalf("%q %q %v", title, page, err)
	}
}

func TestZhipuReadsSearchResultAndReportsItsErrors(t *testing.T) {
	hits, err, req := runSearch(t, zhipu, "200 OK", `{"search_result":[{"title":"潮汐","link":"https://sea.example/cn","content":"每天两次。","media":"海洋网","publish_date":"2026-01-01","refer":"ref_1"}]}`)
	if !strings.Contains(req.body, `"search_query":"tide tables"`) || !strings.Contains(req.body, `"search_engine":"search_std"`) {
		t.Fatal(req.body)
	}
	if err != nil || hits[0].URL != "https://sea.example/cn" || hits[0].Site != "海洋网" {
		t.Fatal(hits, err)
	}
	if _, err, _ := runSearch(t, zhipu, "200 OK", `{"error":{"code":"1701","message":"too many requests"}}`); err == nil || !strings.Contains(err.Error(), "too many requests") {
		t.Fatal(err)
	}
}

func TestKimiReadsSearchResults(t *testing.T) {
	hits, err, req := runSearch(t, kimi, "200 OK", `{"search_results":[{"title":"Tides","url":"https://sea.example/k","site_name":"Sea","date":"2026-03-01","snippet":"","chunks":[{"text":"From the chunk.","score":0.9}]}]}`)
	if !strings.Contains(req.body, `"text_query":"tide tables"`) || err != nil || hits[0].Snippet != "From the chunk." {
		t.Fatal(req.body, hits, err)
	}
}

func TestBochaReadsTheWrappedAnswerAndItsFailures(t *testing.T) {
	hits, err, req := runSearch(t, bocha, "200 OK", `{"code":200,"msg":null,"data":{"webPages":{"value":[{"name":"潮汐表","url":"https://sea.example/b","snippet":"查询潮汐。","siteName":"海洋","datePublished":"2026-04-01T08:00:00+08:00"}]}}}`)
	if req.header.Get("Authorization") != "Bearer test-key" || err != nil || hits[0].Title != "潮汐表" {
		t.Fatal(req, hits, err)
	}
	if _, err, _ := runSearch(t, bocha, "200 OK", `{"code":403,"msg":"余额不足"}`); err == nil || !strings.Contains(err.Error(), "余额不足") {
		t.Fatal(err)
	}
}

func TestTavilyUsesABearerKeyAndReadsResults(t *testing.T) {
	hits, err, req := runSearch(t, tavily, "200 OK", `{"query":"tide tables","results":[{"title":"Tides","url":"https://sea.example/t","content":"Snippet.","score":0.8}]}`)
	if req.header.Get("Authorization") != "Bearer test-key" || !strings.Contains(req.body, `"max_results":8`) || err != nil || hits[0].Snippet != "Snippet." {
		t.Fatal(req, hits, err)
	}
}

func TestBraveSendsItsTokenHeaderAndDropsMarkup(t *testing.T) {
	hits, err, req := runSearch(t, brave, "200 OK", `{"web":{"results":[{"title":"<strong>Tide</strong> tables","url":"https://sea.example/br","description":"When the <strong>tide</strong> turns &amp; why.","page_age":"2026-05-01","profile":{"name":"Sea"}}]}}`)
	if req.method != "GET" || req.uri != "/search?q=tide+tables&count=8" || req.header.Get("X-Subscription-Token") != "test-key" {
		t.Fatal(req)
	}
	if err != nil || hits[0].Title != "Tide tables" || hits[0].Snippet != "When the tide turns & why." {
		t.Fatal(hits, err)
	}
}

func TestExaSendsItsKeyHeaderAndReadsHighlights(t *testing.T) {
	hits, err, req := runSearch(t, exa, "200 OK", `{"results":[{"title":"Tides","url":"https://sea.example/e","publishedDate":"2026-01-01","highlights":["One.","Two."]}]}`)
	if req.header.Get("X-Api-Key") != "test-key" || err != nil || hits[0].Snippet != "One. … Two." {
		t.Fatal(req, hits, err)
	}
}

func TestSearXNGAsksForJSONAndExplainsA403(t *testing.T) {
	hits, err, req := runSearch(t, searxng, "200 OK", `{"query":"tide tables","results":[{"title":"Tides","url":"https://sea.example/s","content":"From SearXNG.","engine":"duckduckgo"},{"title":"No link","url":"","content":""}]}`)
	if req.method != "GET" || req.uri != "/search?q=tide+tables&format=json" {
		t.Fatal(req)
	}
	if err != nil || len(hits) != 1 {
		t.Fatal("a result without a link is dropped", hits, err)
	}
	if _, err, _ := runSearch(t, searxng, "403 Forbidden", ""); err == nil || !strings.Contains(err.Error(), "search.formats") {
		t.Fatal(err)
	}
}

func TestARefusedKeySaysWhereToFixIt(t *testing.T) {
	_, err, _ := runSearch(t, tavily, "401 Unauthorized", `{"detail":"bad key"}`)
	if err == nil || !strings.Contains(err.Error(), "Tavily refused the key") || !strings.Contains(err.Error(), "Search service") {
		t.Fatal(err)
	}
}

func TestSearchingAndReadingAPageFailInTheSameWords(t *testing.T) {
	for _, c := range [][3]string{
		{"503 Service Unavailable", "busy", "DeepSeek search failed (503 Service Unavailable): busy"},
		{"200 OK", "<html>", "DeepSeek sent an answer that isn't JSON."},
	} {
		if _, err, _ := runSearch(t, deepseek, c[0], c[1]); err == nil || err.Error() != c[2] {
			t.Fatal(err)
		}
		endpoint, _ := serve(t, c[0], c[1])
		s := &Service{Backend: deepseek, Credential: "test-key", Endpoint: endpoint}
		if _, _, err := s.FetchPage(context.Background(), "https://example.com/"); err == nil || err.Error() != c[2] {
			t.Fatal(err)
		}
	}
}

func TestIDsRoundTripAndKeysAreSharedWhereTheProviderIsTheSame(t *testing.T) {
	for _, b := range []Backend{deepseek, zhipu, kimi, bocha, tavily, brave, exa, searxng} {
		if got, ok := BackendOf(string(b)); !ok || got != b || b.Label() == "" {
			t.Fatal(b)
		}
	}
	for b, want := range map[Backend]string{deepseek: "deepseek", kimi: "moonshot", tavily: "search-tavily"} {
		if id, ok := b.KeyID(); !ok || id != want {
			t.Fatal(b, id)
		}
	}
	if _, ok := searxng.KeyID(); ok {
		t.Fatal("SearXNG takes an address, not a key")
	}
	if _, ok := BackendOf("auto"); ok {
		t.Fatal("auto is no service")
	}
	if auto := automatic(); !reflect.DeepEqual(auto, []Backend{deepseek, zhipu, kimi}) {
		t.Fatal(auto)
	}
}

// Settings reads src/prefs/search-services.json; the backend its copy.
func TestTheServicesAreTheOnesSettingsLists(t *testing.T) {
	shared, err := os.ReadFile("../../../src/prefs/search-services.json")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(shared, servicesJSON) {
		t.Fatal("backend/ai/web/search-services.json differs from src/prefs/search-services.json: copy it over")
	}
}

func TestAServiceIsTheOneChosenWithItsKeyOrTheFirstAutomaticOne(t *testing.T) {
	keys := map[string]string{"zhipu": "z-key", "search-tavily": " "}
	key := func(id string) string { return keys[id] }
	if s, ok := ResolveService("", "", key); !ok || s.Backend != zhipu || s.Credential != "z-key" {
		t.Fatal(s)
	}
	// Chosen without its key: none, not a substitute.
	if s, ok := ResolveService("tavily", "", key); ok {
		t.Fatal(s)
	}
	if s, ok := ResolveService("searxng", " https://search.example/ ", key); !ok || s.url() != "https://search.example/search" {
		t.Fatal(s)
	}
	if _, ok := ResolveService("searxng", "search.example", key); ok {
		t.Fatal("an address without its scheme")
	}
}
