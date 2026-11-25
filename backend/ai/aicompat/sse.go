package aicompat

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Requests and their streamed answers: a JSON body posted, a failure read
// as "{what} ({status}): {body}" (the shape the frontend reads the status
// of), and an SSE body read event by event, given up on when it goes
// silent.

const (
	// maxBodyBytes bounds a whole answer read at once.
	maxBodyBytes = 32 << 20
	// requestTimeout bounds a request answered whole.
	requestTimeout = 120 * time.Second
)

// The stream's patience: streamReadTimeout is the most silence between two
// chunks (keep-alives count), streamEventTimeout the most time without a
// single event (they don't) - so a provider keeping the connection warm
// after the model stopped can't hold a request forever. Tests shorten them.
var (
	streamReadTimeout  = 120 * time.Second
	streamEventTimeout = 300 * time.Second
)

// request is one POST of body as JSON to url, signed by sign. Object keys
// go sorted, so the same request is the same bytes and a server's prompt
// cache reads it back. A request that didn't reach the server says so in
// the words the frontend reads as a network failure.
func request(ctx context.Context, client *http.Client, url string, body any, sign func(*http.Request)) (*http.Response, error) {
	raw, err := json.Marshal(body, json.Deterministic(true))
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if sign != nil {
		sign(req)
	}
	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("error sending request: %w", err)
	}
	return resp, nil
}

// succeeded is resp when it succeeded; otherwise its body as the error,
// "{what} ({status}): {body}".
func succeeded(resp *http.Response, what string) error {
	if resp.StatusCode >= 200 && resp.StatusCode <= 299 {
		return nil
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return fmt.Errorf("%s (%s): %s", what, resp.Status, body)
}

// postJSON posts body and decodes the whole answer; a failure reads "{what}
// ({status}): {body}".
func postJSON(ctx context.Context, client *http.Client, url string, body any, sign func(*http.Request), what string) (any, error) {
	resp, err := request(ctx, client, url, body, sign)
	if err != nil {
		return nil, err
	}
	if err := succeeded(resp, what); err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return nil, err
	}
	var v any
	if err := json.Unmarshal(raw, &v, lenient); err != nil {
		return nil, fmt.Errorf("%s: the answer isn't JSON (%v)", what, err)
	}
	return v, nil
}

// endsStream reports whether data is a stream-ending payload ([DONE],
// message_stop, response.completed/failed/incomplete): a server may keep
// the connection open past it.
func endsStream(data string) bool {
	if strings.TrimSpace(data) == "[DONE]" {
		return true
	}
	if !strings.Contains(data, "message_stop") && !strings.Contains(data, `"response.`) {
		return false
	}
	switch strAt(decode(data), "type") {
	case "message_stop", "response.completed", "response.failed", "response.incomplete":
		return true
	}
	return false
}

// readSSE reads resp's SSE body, calling onData with each `data:` payload,
// until the stream ends; a failed status is the error, "{label} request
// failed ({status}): {body}".
func readSSE(resp *http.Response, label string, onData func(data string)) error {
	if err := succeeded(resp, label+" request failed"); err != nil {
		return err
	}
	defer resp.Body.Close()
	// The watchdog closes the body when it goes silent, which ends the read
	// below with an error, said as why.
	var (
		mu        sync.Mutex
		lastByte  = time.Now()
		lastEvent = time.Now()
		gaveUp    error
		readLimit = streamReadTimeout
		quietMost = streamEventTimeout
	)
	done := make(chan struct{})
	defer close(done)
	go func() {
		tick := time.NewTicker(min(time.Second, readLimit/4))
		defer tick.Stop()
		for {
			select {
			case <-done:
				return
			case now := <-tick.C:
				mu.Lock()
				switch {
				case now.Sub(lastEvent) > quietMost:
					gaveUp = fmt.Errorf("%s stopped responding: nothing came for %d minutes", label, int(quietMost.Minutes()))
				case now.Sub(lastByte) > readLimit:
					gaveUp = fmt.Errorf("%s stopped responding: the connection went silent", label)
				}
				failed := gaveUp != nil
				mu.Unlock()
				if failed {
					resp.Body.Close()
					return
				}
			}
		}
	}()
	reader := bufio.NewReaderSize(resp.Body, 64<<10)
	for {
		line, err := reader.ReadBytes('\n')
		mu.Lock()
		lastByte = time.Now()
		mu.Unlock()
		if len(line) > 0 && (err == nil || errors.Is(err, io.EOF)) {
			text := strings.TrimRight(strings.ToValidUTF8(string(line), "\uFFFD"), "\r\n")
			if data, ok := strings.CutPrefix(text, "data:"); ok {
				data = strings.TrimPrefix(data, " ")
				mu.Lock()
				lastEvent = time.Now()
				mu.Unlock()
				onData(data)
				if endsStream(data) {
					return nil
				}
			}
		}
		if err != nil {
			mu.Lock()
			why := gaveUp
			mu.Unlock()
			switch {
			case why != nil:
				return why
			case errors.Is(err, io.EOF):
				return nil
			}
			return fmt.Errorf("%s stream failed: %v", label, err)
		}
	}
}
