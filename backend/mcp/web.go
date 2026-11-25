package mcp

import (
	"bytes"
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
)

// A server at a URL, over Streamable HTTP: each message POSTed, its answer
// JSON or an event stream, the session the server names carried along.

// web is a server at a URL.
type web struct {
	client  *http.Client
	url     string
	headers [][2]string

	mu      sync.Mutex
	session string
}

// post sends one message; the answer to id (none for 0), read from JSON or
// from an event stream.
func (w *web) post(ctx context.Context, m message, id uint64) (jsontext.Value, error) {
	body, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("MCP-Protocol-Version", protocolVersion)
	for _, h := range w.headers {
		req.Header.Add(h[0], h[1])
	}
	w.mu.Lock()
	if w.session != "" {
		req.Header.Set("Mcp-Session-Id", w.session)
	}
	w.mu.Unlock()
	resp, err := w.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if session := resp.Header.Get("Mcp-Session-Id"); session != "" {
		w.mu.Lock()
		w.session = session
		w.mu.Unlock()
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		said, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		runes := []rune(strings.ToValidUTF8(string(said), "�"))
		return nil, fmt.Errorf("the server answered %s: %s", resp.Status, string(runes[:min(len(runes), 300)]))
	}
	if id == 0 {
		return jsontext.Value("null"), nil
	}
	streamed := strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream")
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxAnswer+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > maxAnswer {
		return nil, errors.New("the answer was over 16 MB")
	}
	var answer map[string]jsontext.Value
	if streamed {
		for _, line := range strings.Split(strings.ToValidUTF8(string(raw), "�"), "\n") {
			data, ok := strings.CutPrefix(strings.TrimRight(line, "\r"), "data:")
			if !ok {
				continue
			}
			m := fields(jsontext.Value(strings.TrimSpace(data)))
			var got uint64
			if m != nil && json.Unmarshal(m["id"], &got) == nil && got == id {
				answer = m
				break
			}
		}
		if answer == nil {
			return nil, errors.New("the event stream had no answer")
		}
	} else if answer = fields(jsontext.Value(raw)); answer == nil {
		return nil, errors.New("the answer wasn't JSON")
	}
	r := answerOf(answer)
	return r.result, r.err
}
