package plugins

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
)

// A plugin's requests to the hosts it was granted (`network:<host>`). The
// app's one transport keeps connections open between requests, so a
// plugin's next request needs no new TLS handshake; a request given a
// cancelId stops when the plugin cancels it.

// MaxHTTPBytes is the most a response may be.
const MaxHTTPBytes = 32 * 1024 * 1024

// maxHTTPRedirects is how many redirects a request follows, each within
// what was granted.
const maxHTTPRedirects = 5

// HTTPRequest is a plugin's request (HttpRequest in
// src/platform/bindings/plugin-access.ts).
type HTTPRequest struct {
	URL     string            `json:"url"`
	Method  *string           `json:"method"`
	Headers map[string]string `json:"headers"`
	// Body is text, or base64 with BodyBase64.
	Body       *string `json:"body"`
	BodyBase64 bool    `json:"bodyBase64"`
	// CancelID is the plugin's name for this request, to cancel it by.
	CancelID *string `json:"cancelId"`
}

// HTTPResponse is what a request answered (HttpResponse in
// src/platform/bindings/plugin-access.ts).
type HTTPResponse struct {
	Status  int               `json:"status"`
	Headers map[string]string `json:"headers"`
	// Body is the body, base64.
	Body string `json:"body"`
}

// ErrTooBig is a response over MaxHTTPBytes.
var ErrTooBig = errors.New("The response is too big.")

// Send makes request for plugin id with its grants, through client (whose
// redirects it limits to the grants).
func Send(ctx context.Context, client *http.Client, id string, granted []string, request HTTPRequest) (HTTPResponse, error) {
	u, err := url.Parse(request.URL)
	if err != nil {
		return HTTPResponse{}, err
	}
	if !u.IsAbs() || !URLAllowed(granted, u) {
		return HTTPResponse{}, Refused(id, "network:"+u.Hostname())
	}
	method := "GET"
	if request.Method != nil {
		method = strings.ToUpper(*request.Method)
	}
	var body io.Reader
	if request.Body != nil {
		data := []byte(*request.Body)
		if request.BodyBase64 {
			if data, err = base64.StdEncoding.DecodeString(*request.Body); err != nil {
				return HTTPResponse{}, err
			}
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), body)
	if err != nil {
		return HTTPResponse{}, err
	}
	for name, value := range request.Headers {
		// The plugin is not the user's browser: no cookies of theirs.
		if strings.EqualFold(name, "cookie") {
			continue
		}
		req.Header.Set(name, value)
	}
	own := *client
	// A redirect must stay within what was granted too.
	own.CheckRedirect = func(next *http.Request, via []*http.Request) error {
		if len(via) > maxHTTPRedirects || !URLAllowed(granted, next.URL) {
			return http.ErrUseLastResponse
		}
		return nil
	}
	resp, err := own.Do(req)
	if err != nil {
		return HTTPResponse{}, err
	}
	defer resp.Body.Close()
	headers := map[string]string{}
	for name, values := range resp.Header {
		name = strings.ToLower(name)
		if name == "set-cookie" || len(values) == 0 {
			continue
		}
		headers[name] = values[len(values)-1]
	}
	if resp.ContentLength > MaxHTTPBytes {
		return HTTPResponse{}, ErrTooBig
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, MaxHTTPBytes+1))
	if err != nil {
		return HTTPResponse{}, err
	}
	if len(data) > MaxHTTPBytes {
		return HTTPResponse{}, ErrTooBig
	}
	return HTTPResponse{Status: resp.StatusCode, Headers: headers, Body: base64.StdEncoding.EncodeToString(data)}, nil
}

// pendingKey is a request given a cancelId: its plugin and that id.
type pendingKey struct{ plugin, id string }

type pendingRequest struct {
	n    uint64
	stop context.CancelFunc
	// cancelled is closed once the plugin cancels it.
	cancelled chan struct{}
}

var pending = struct {
	sync.Mutex
	m map[pendingKey]*pendingRequest
}{m: map[pendingKey]*pendingRequest{}}

var requestNumbers atomic.Uint64

// Cancelled is what a cancelled call answers.
const Cancelled = "cancelled"

// Cancellable runs work, or answers "cancelled" as soon as plugin id
// cancels cancelID (Cancel): a request, or a call to the AI model. Each
// request has its own number, so a later request under the same id never
// ends this one's.
func Cancellable[T any](ctx context.Context, id string, cancelID *string, work func(ctx context.Context) (T, error)) (T, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if cancelID == nil {
		return work(ctx)
	}
	ctx, stop := context.WithCancel(ctx)
	defer stop()
	key := pendingKey{id, *cancelID}
	own := &pendingRequest{n: requestNumbers.Add(1), stop: stop, cancelled: make(chan struct{})}
	pending.Lock()
	pending.m[key] = own
	pending.Unlock()
	defer func() {
		pending.Lock()
		if p := pending.m[key]; p != nil && p.n == own.n {
			delete(pending.m, key)
		}
		pending.Unlock()
	}()
	type answer struct {
		value T
		err   error
	}
	done := make(chan answer, 1)
	go func() {
		v, err := work(ctx)
		done <- answer{v, err}
	}()
	select {
	case a := <-done:
		return a.value, a.err
	case <-own.cancelled:
		var zero T
		return zero, errors.New(Cancelled)
	}
}

// Cancel stops plugin id's request named cancelID, if it is still going:
// its connection is dropped and the call answers "cancelled".
func Cancel(id, cancelID string) {
	pending.Lock()
	p := pending.m[pendingKey{id, cancelID}]
	delete(pending.m, pendingKey{id, cancelID})
	pending.Unlock()
	if p != nil {
		close(p.cancelled)
		p.stop()
	}
}

// isPending is whether plugin id has a request named cancelID going.
func isPending(id, cancelID string) bool {
	pending.Lock()
	defer pending.Unlock()
	_, ok := pending.m[pendingKey{id, cancelID}]
	return ok
}
