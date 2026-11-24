package ai

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json/v2"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
)

// A provider's model list, and how each kind of server takes a key.

// nonChat are fragments of model ids that are never chat models.
// Conservative: a listed model that slips through is harmless, a chat model
// wrongly hidden is not (the user can still type any id).
var nonChat = []string{
	"embed", "tts", "whisper", "transcri", "dall-e", "gpt-image", "-image", "image-", "imagen",
	"moderation", "rerank", "realtime", "-audio", "audio-", "speech", "veo-", "sora", "lyria",
	"babbage", "davinci-002", "guard",
}

func isChatModel(id string) bool {
	id = strings.ToLower(id)
	return !slices.ContainsFunc(nonChat, func(f string) bool { return strings.Contains(id, f) })
}

// listed is one model of a listing: its id and, when the provider says, a
// key that sorts by creation.
type listed struct {
	id, created string
	dated       bool
}

// tidy is what a model picker should offer of a listing: Gemini's models/
// prefix gone, non-chat models and repeats dropped, newest first when the
// provider dates every model, else in the provider's order.
func tidy(entries []listed) []string {
	seen := map[string]bool{}
	var kept []listed
	for _, e := range entries {
		e.id = strings.TrimPrefix(e.id, "models/")
		if e.id == "" || !isChatModel(e.id) || seen[e.id] {
			continue
		}
		seen[e.id] = true
		kept = append(kept, e)
	}
	if !slices.ContainsFunc(kept, func(e listed) bool { return !e.dated }) {
		slices.SortStableFunc(kept, func(a, b listed) int { return strings.Compare(b.created, a.created) })
	}
	ids := []string{}
	for _, e := range kept {
		ids = append(ids, e.id)
	}
	return ids
}

// opencodeSession is one session id for this run of the app: stable, so
// OpenCode's routing and prompt cache hold across a conversation's steps.
var opencodeSession = sync.OnceValue(func() string {
	b := make([]byte, 8)
	rand.Read(b)
	return "textus-" + hex.EncodeToString(b)
})

// WithKey puts key on req as the server at baseURL takes it. Google's APIs
// (Vertex reads a bearer token as OAuth) get x-goog-api-key, except
// Gemini's OpenAI-compatible layer, which wants a bearer like everyone
// else; OpenCode refuses a request without a session id to route it by.
func WithKey(req *http.Request, baseURL, key string) {
	host := ""
	if u, err := url.Parse(baseURL); err == nil {
		host = u.Hostname()
	}
	switch {
	case host == "generativelanguage.googleapis.com":
		req.Header.Set("Authorization", "Bearer "+key)
	case strings.HasSuffix(host, ".googleapis.com"):
		req.Header.Set("x-goog-api-key", key)
	case host == "opencode.ai":
		req.Header.Set("Authorization", "Bearer "+key)
		req.Header.Set("x-opencode-session", opencodeSession())
	default:
		req.Header.Set("Authorization", "Bearer "+key)
	}
}

// getJSON GETs u and reads its JSON answer into v; a failure says the
// status and what the server said.
func getJSON(ctx context.Context, client *http.Client, u string, header func(*http.Request), v any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	header(req)
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("Could not list models (%s): %s", resp.Status, body)
	}
	return json.Unmarshal(body, v)
}

// ListModels lists an OpenAI-compatible endpoint's /models. It doubles as
// the connection test: if it answers, the endpoint is reachable and the
// key works.
func ListModels(ctx context.Context, client *http.Client, baseURL string, key *string) ([]string, error) {
	var page struct {
		Data []struct {
			ID string `json:"id"`
			// Created is Unix seconds, where the endpoint says.
			Created *int64 `json:"created"`
		} `json:"data"`
	}
	err := getJSON(ctx, client, strings.TrimRight(baseURL, "/")+"/models", func(req *http.Request) {
		if key != nil {
			WithKey(req, baseURL, *key)
		}
	}, &page)
	if err != nil {
		return nil, err
	}
	var entries []listed
	for _, m := range page.Data {
		e := listed{id: m.ID}
		if m.Created != nil {
			e.created, e.dated = fmt.Sprintf("%020d", *m.Created), true
		}
		entries = append(entries, e)
	}
	return tidy(entries), nil
}

// AnthropicAuth is how a request to Anthropic is signed: an API key, or a
// Claude sign-in's token.
type AnthropicAuth struct {
	APIKey, OAuthToken string
}

// Sign puts the auth and API version on req.
func (a AnthropicAuth) Sign(req *http.Request) {
	req.Header.Set("anthropic-version", "2023-06-01")
	if a.OAuthToken != "" {
		req.Header.Set("Authorization", "Bearer "+a.OAuthToken)
		req.Header.Set("anthropic-beta", "claude-code-20250219,oauth-2025-04-20")
		return
	}
	req.Header.Set("x-api-key", a.APIKey)
}

// anthropicAPI is Anthropic's API root; tests point it elsewhere.
var anthropicAPI = "https://api.anthropic.com"

// ListAnthropicModels lists Anthropic's models, every page of them.
func ListAnthropicModels(ctx context.Context, client *http.Client, auth AnthropicAuth) ([]string, error) {
	var entries []listed
	after := ""
	for range 10 {
		u := anthropicAPI + "/v1/models?limit=100"
		if after != "" {
			u += "&after_id=" + url.QueryEscape(after)
		}
		var page struct {
			Data []struct {
				ID        string  `json:"id"`
				CreatedAt *string `json:"created_at"`
			} `json:"data"`
			HasMore bool    `json:"has_more"`
			LastID  *string `json:"last_id"`
		}
		if err := getJSON(ctx, client, u, auth.Sign, &page); err != nil {
			return nil, err
		}
		for _, m := range page.Data {
			e := listed{id: m.ID}
			if m.CreatedAt != nil {
				e.created, e.dated = *m.CreatedAt, true
			}
			entries = append(entries, e)
		}
		if !page.HasMore || page.LastID == nil {
			break
		}
		after = *page.LastID
	}
	return tidy(entries), nil
}
