package web

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// The search services Textus's own web_search can use when the model has
// no search of its own. Each is a request and a parse, tested against a
// local server and canned answers.

const searchTimeout = 30 * time.Second

// deepSeekFieldChars is the longest `query` and `ori_question` DeepSeek's
// Search API takes.
const deepSeekFieldChars = 128

// Backend is one search service, by the id Settings stores (Backend in
// src/platform/bindings/ai.ts).
type Backend string

// serviceEntry is one service as src/prefs/search-services.json lists it,
// which Settings reads too; search-services.json here is a copy, kept in
// step by a test.
type serviceEntry struct {
	ID       string  `json:"id"`
	Name     string  `json:"name"`
	Key      *string `json:"key"`
	Auto     bool    `json:"auto"`
	Endpoint string  `json:"endpoint"`
}

//go:embed search-services.json
var servicesJSON []byte

var services = func() []serviceEntry {
	var list []serviceEntry
	if err := json.Unmarshal(servicesJSON, &list); err != nil {
		panic("ai/web/search-services.json: " + err.Error())
	}
	return list
}()

func (b Backend) entry() (serviceEntry, bool) {
	for _, e := range services {
		if e.ID == string(b) {
			return e, true
		}
	}
	return serviceEntry{}, false
}

// BackendOf is the service an id names; false for none ("auto").
func BackendOf(id string) (Backend, bool) {
	_, ok := Backend(id).entry()
	return Backend(id), ok
}

// Label is the name the model and the error messages use.
func (b Backend) Label() string {
	e, _ := b.entry()
	return e.Name
}

// KeyID is where its key is kept: the AI provider's own for the three
// that share it, `search-<id>` for the rest; false for SearXNG, which takes
// an address instead.
func (b Backend) KeyID() (string, bool) {
	e, _ := b.entry()
	if e.Key == nil {
		return "", false
	}
	return *e.Key, true
}

// automatic are the services "auto" tries, in order: the AI providers' own
// search.
func automatic() []Backend {
	var list []Backend
	for _, e := range services {
		if e.Auto {
			list = append(list, Backend(e.ID))
		}
	}
	return list
}

// ResolveService is the search service Textus's own web_search uses: the
// one chosen in Settings ("auto", or none: the first of DeepSeek, Zhipu
// and Kimi whose key is saved). A chosen service without its key is none,
// not a substitute. key is a saved key by its id, "" for none.
func ResolveService(choice, searxngURL string, key func(id string) string) (*Service, bool) {
	keyOf := func(b Backend) (string, bool) {
		id, ok := b.KeyID()
		if !ok {
			return "", false
		}
		k := key(id)
		return k, strings.TrimSpace(k) != ""
	}
	if b, ok := BackendOf(choice); ok {
		if b == searxng {
			u := strings.TrimSpace(searxngURL)
			if strings.HasPrefix(u, "http://") || strings.HasPrefix(u, "https://") {
				return &Service{Backend: b, Credential: u}, true
			}
			return nil, false
		}
		if k, ok := keyOf(b); ok {
			return &Service{Backend: b, Credential: k}, true
		}
		return nil, false
	}
	for _, b := range automatic() {
		if k, ok := keyOf(b); ok {
			return &Service{Backend: b, Credential: k}, true
		}
	}
	return nil, false
}

// The services whose requests differ from a bearer key and a JSON body.
const (
	deepseek Backend = "deepseek"
	zhipu    Backend = "zhipu"
	kimi     Backend = "kimi"
	bocha    Backend = "bocha"
	tavily   Backend = "tavily"
	brave    Backend = "brave"
	exa      Backend = "exa"
	searxng  Backend = "searxng"
)

// Hit is one search result.
type Hit struct {
	Title, URL, Snippet string
	// Date and Site are "" when the service doesn't say.
	Date, Site string
}

// Service is a search service ready to use: which one, and its key (or
// SearXNG's address).
type Service struct {
	Backend    Backend
	Credential string
	// Endpoint is where requests go instead of the service's own address:
	// the tests' local server.
	Endpoint string
}

func (s *Service) url() string {
	switch {
	case s.Endpoint != "":
		return s.Endpoint
	case s.Backend == searxng:
		return strings.TrimRight(s.Credential, "/") + "/search"
	}
	e, _ := s.Backend.entry()
	return e.Endpoint
}

// Search is up to count results for query. question is the user's
// message, which DeepSeek's service takes to rank by.
func (s *Service) Search(ctx context.Context, query string, count int, question string) ([]Hit, error) {
	req, err := s.request(ctx, query, count, question)
	if err != nil {
		return nil, err
	}
	value, err := s.answer(req)
	if err != nil {
		return nil, err
	}
	hits, err := parseHits(s.Backend, value)
	if err != nil {
		return nil, err
	}
	kept := hits[:0]
	for _, h := range hits {
		if strings.HasPrefix(h.URL, "http") {
			kept = append(kept, h)
		}
	}
	return kept[:min(len(kept), count)], nil
}

// fit is s cut to what DeepSeek takes.
func fit(s string) string { return firstChars(strings.TrimSpace(s), deepSeekFieldChars) }

func (s *Service) request(ctx context.Context, query string, count int, question string) (*http.Request, error) {
	key := s.Credential
	var body map[string]any
	header := map[string]string{"Authorization": "Bearer " + key}
	switch s.Backend {
	case deepseek:
		// Each field takes 1-128 characters, and DeepSeek answers 400
		// otherwise: a long query is cut to fit, an empty question left out.
		body = map[string]any{"query": fit(query), "count": count}
		if q := fit(question); q != "" {
			body["ori_question"] = q
		}
	case zhipu:
		body = map[string]any{"search_query": query, "search_engine": "search_std", "count": count}
	case kimi:
		body = map[string]any{"text_query": query, "limit": count}
	case bocha:
		body = map[string]any{"query": query, "count": count, "summary": false}
	case tavily:
		body = map[string]any{"query": query, "max_results": count, "search_depth": "basic"}
	case exa:
		body = map[string]any{"query": query, "numResults": count, "contents": map[string]any{"highlights": true}}
		header = map[string]string{"x-api-key": key}
	case brave:
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.url()+"?q="+url.QueryEscape(query)+"&count="+strconv.Itoa(count), nil)
		if err == nil {
			req.Header.Set("X-Subscription-Token", key)
			req.Header.Set("Accept", "application/json")
		}
		return req, err
	case searxng:
		return http.NewRequestWithContext(ctx, http.MethodGet, s.url()+"?q="+url.QueryEscape(query)+"&format=json", nil)
	default:
		return nil, fmt.Errorf("unknown search service %q", s.Backend)
	}
	raw, err := json.Marshal(body, json.Deterministic(true))
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.url(), bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range header {
		req.Header.Set(k, v)
	}
	return req, nil
}

// answer sends req and reads the answer as JSON; a failure in words the
// model can pass on.
func (s *Service) answer(req *http.Request) (any, error) {
	resp, err := (&http.Client{Timeout: searchTimeout, Transport: Net.Transport}).Do(req)
	if err != nil {
		return nil, s.unreachable(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, s.refused(resp.StatusCode, string(body))
	}
	var value any
	if json.Unmarshal(body, &value) != nil {
		return nil, fmt.Errorf("%s sent an answer that isn't JSON.", s.Backend.Label())
	}
	return value, nil
}

func (s *Service) unreachable(err error) error {
	if isTimeout(err) {
		return fmt.Errorf("%s took too long to answer.", s.Backend.Label())
	}
	return fmt.Errorf("Couldn't reach %s.", s.Backend.Label())
}

// isTimeout reports whether err is a request that ran out of time.
func isTimeout(err error) bool {
	var ne net.Error
	return errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &ne) && ne.Timeout())
}

// statusText is a status as "503 Service Unavailable".
func statusText(code int) string { return strconv.Itoa(code) + " " + http.StatusText(code) }

// refused is what a failed answer means, in words the model can pass on.
func (s *Service) refused(status int, body string) error {
	label, detail := s.Backend.Label(), firstChars(body, 200)
	switch {
	case s.Backend == searxng && status == http.StatusForbidden:
		return fmt.Errorf("This SearXNG server doesn't answer in JSON: add `json` under `search.formats` in its settings.yml. (%s)", detail)
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return fmt.Errorf("%s refused the key - check it in Settings > Textus AI > Search service. (%s)", label, detail)
	case status == http.StatusPaymentRequired || status == http.StatusTooManyRequests:
		return fmt.Errorf("%s says the account is out of searches or credit for now. (%s)", label, detail)
	}
	return fmt.Errorf("%s search failed (%s): %s", label, statusText(status), detail)
}

// ReadsPages reports whether the service has a page reader (DeepSeek's),
// for a page Textus couldn't download itself.
func (s *Service) ReadsPages() bool { return s.Backend == deepseek }

// FetchPage is DeepSeek's page reader on page, for a page Textus couldn't
// download itself (blocked where the user is, say): its title, and the
// page as Markdown.
func (s *Service) FetchPage(ctx context.Context, page string) (title, text string, err error) {
	u := "https://api.deepseek.com/v1/tools/fetch"
	if s.Endpoint != "" {
		u = s.Endpoint + "/fetch"
	}
	raw, _ := json.Marshal(map[string]string{"url": page})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(raw))
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+s.Credential)
	value, err := s.answer(req)
	if err != nil {
		return "", "", err
	}
	content := withoutEscapes(text0(value, "content"))
	if strings.TrimSpace(content) == "" {
		return "", "", errors.New("DeepSeek found no readable text on that page.")
	}
	return text0(value, "title"), content, nil
}

// at is the value at path in v: object members by name, array entries by
// index; nil where there is none.
func at(v any, path ...string) any {
	for _, key := range path {
		switch x := v.(type) {
		case map[string]any:
			v = x[key]
		case []any:
			i, err := strconv.Atoi(key)
			if err != nil || i < 0 || i >= len(x) {
				return nil
			}
			v = x[i]
		default:
			return nil
		}
	}
	return v
}

// list is the array at path, nil for none.
func list(v any, path ...string) []any {
	l, _ := at(v, path...).([]any)
	return l
}

// text0 is the string member key of v, trimmed; "" for none.
func text0(v any, key string) string {
	s, _ := at(v, key).(string)
	return strings.TrimSpace(s)
}

// optional is text0, or fallback where it is "".
func optional(v any, key, fallback string) string {
	if s := text0(v, key); s != "" {
		return s
	}
	return fallback
}

// withoutEscapes: DeepSeek's page reader escapes every ASCII punctuation
// mark in its Markdown (`fn main\(\)`) - the marks as written.
func withoutEscapes(s string) string {
	var out strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) && isASCIIPunct(s[i+1]) {
			i++
		}
		out.WriteByte(s[i])
	}
	return out.String()
}

func isASCIIPunct(c byte) bool {
	return strings.IndexByte("!\"#$%&'()*+,-./:;<=>?@[\\]^_`{|}~", c) >= 0
}

// withoutTags: Brave marks the query's words in its descriptions with
// <strong>.
func withoutTags(s string) string {
	var out strings.Builder
	inTag := false
	for _, r := range s {
		switch {
		case r == '<':
			inTag = true
		case r == '>' && inTag:
			inTag = false
		case !inTag:
			out.WriteRune(r)
		}
	}
	return strings.NewReplacer("&quot;", `"`, "&#39;", "'", "&amp;", "&").Replace(out.String())
}

// parseHits is the results in one service's answer, or the error it
// reports inside a successful response (Zhipu and Bocha do).
func parseHits(b Backend, value any) ([]Hit, error) {
	if e := at(value, "error"); e != nil {
		message, ok := at(e, "message").(string)
		if !ok {
			raw, _ := json.Marshal(e, json.Deterministic(true))
			message = string(raw)
		}
		return nil, fmt.Errorf("%s search failed: %s", b.Label(), message)
	}
	var hits []Hit
	each := func(results []any, hit func(r any) Hit) {
		for _, r := range results {
			hits = append(hits, hit(r))
		}
	}
	switch b {
	case deepseek:
		each(list(value, "results"), func(r any) Hit {
			return Hit{Title: text0(r, "title"), URL: text0(r, "url"), Snippet: optional(r, "snippet", text0(r, "summary")),
				Date: text0(r, "date"), Site: text0(r, "site_name")}
		})
	case zhipu:
		each(list(value, "search_result"), func(r any) Hit {
			return Hit{Title: text0(r, "title"), URL: text0(r, "link"), Snippet: text0(r, "content"),
				Date: text0(r, "publish_date"), Site: text0(r, "media")}
		})
	case kimi:
		each(list(value, "search_results"), func(r any) Hit {
			chunk, _ := at(r, "chunks", "0", "text").(string)
			return Hit{Title: text0(r, "title"), URL: text0(r, "url"), Snippet: optional(r, "snippet", chunk),
				Date: text0(r, "date"), Site: text0(r, "site_name")}
		})
	case bocha:
		// Bocha wraps its answer in {code, msg, data} - and reports a
		// failure there with a 200.
		if code, ok := at(value, "code").(float64); ok && code == float64(int64(code)) && code != 200 {
			msg, _ := at(value, "msg").(string)
			return nil, fmt.Errorf("Bocha search failed (%d): %s", int64(code), msg)
		}
		results := list(value, "data", "webPages", "value")
		if len(results) == 0 {
			results = list(value, "webPages", "value")
		}
		each(results, func(r any) Hit {
			return Hit{Title: text0(r, "name"), URL: text0(r, "url"), Snippet: optional(r, "snippet", text0(r, "summary")),
				Date: text0(r, "datePublished"), Site: text0(r, "siteName")}
		})
	case tavily:
		each(list(value, "results"), func(r any) Hit {
			return Hit{Title: text0(r, "title"), URL: text0(r, "url"), Snippet: text0(r, "content"), Date: text0(r, "published_date")}
		})
	case brave:
		each(list(value, "web", "results"), func(r any) Hit {
			site, _ := at(r, "profile", "name").(string)
			return Hit{Title: withoutTags(text0(r, "title")), URL: text0(r, "url"), Snippet: withoutTags(text0(r, "description")),
				Date: text0(r, "page_age"), Site: site}
		})
	case exa:
		each(list(value, "results"), func(r any) Hit {
			var highlights []string
			for _, h := range list(r, "highlights") {
				if s, ok := h.(string); ok {
					highlights = append(highlights, s)
				}
			}
			snippet := strings.Join(highlights, " … ")
			if len(highlights) == 0 {
				snippet = optional(r, "summary", text0(r, "text"))
			}
			return Hit{Title: text0(r, "title"), URL: text0(r, "url"), Snippet: snippet, Date: text0(r, "publishedDate")}
		})
	case searxng:
		each(list(value, "results"), func(r any) Hit {
			return Hit{Title: text0(r, "title"), URL: text0(r, "url"), Snippet: text0(r, "content"), Date: text0(r, "publishedDate")}
		})
	}
	return hits, nil
}

// cutAt is s whitespace-collapsed, cut at n characters with an ellipsis.
func cutAt(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) > n {
		return firstChars(s, n) + "…"
	}
	return s
}
