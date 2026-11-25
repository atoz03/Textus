package backend

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/egoist/mygo"

	"textus/backend/ai"
	"textus/backend/ai/aicompat"
)

// Signing in with a ChatGPT or Claude subscription, one way for both: the
// browser's PKCE sign-in, its redirect caught on a port of this computer,
// the credential kept as a secret file in the config folder and refreshed
// once at a time before it lapses.

// signInTimeout is how long a sign-in waits for the browser.
const signInTimeout = 5 * time.Minute

// notSignedIn is what valid says when there is no sign-in.
const notSignedIn = "not logged in"

// openBrowser opens the sign-in page; tests open nothing.
var openBrowser = mygo.Shell.OpenExternal

// signInProvider is one provider's sign-in: where its credential is kept,
// the port and path its redirect comes back to, and its endpoints.
type signInProvider struct {
	// file is the credential's, in the config folder.
	file string
	// name is said on the page the browser shows once signed in.
	name         string
	port         int
	callbackPath string
	authorize    func() (link, verifier, state string)
	exchange     func(ctx context.Context, client *http.Client, code, state, verifier string) (aicompat.SignIn, error)
	refresh      func(ctx context.Context, client *http.Client, token string) (aicompat.SignIn, error)
	// refreshing is held while a refresh is in flight: the refresh token
	// rotates, so two at once leave the loser with invalid_grant.
	refreshing sync.Mutex
}

var chatGPTSignIn = &signInProvider{
	file: "auth-codex.json", name: "ChatGPT", port: aicompat.CodexCallbackPort, callbackPath: "/auth/callback",
	authorize: func() (string, string, string) { return aicompat.CodexAuthorize(ai.Originator) },
	exchange: func(ctx context.Context, client *http.Client, code, _, verifier string) (aicompat.SignIn, error) {
		return aicompat.CodexExchange(ctx, client, code, verifier)
	},
	refresh: aicompat.CodexRefresh,
}

var claudeSignIn = &signInProvider{
	file: "auth-claude.json", name: "Claude", port: aicompat.ClaudeCallbackPort, callbackPath: "/callback",
	authorize: aicompat.ClaudeAuthorize,
	exchange:  aicompat.ClaudeExchange,
	refresh:   aicompat.ClaudeRefresh,
}

// signIns are the sign-ins, by the provider id each signs in to: ChatGPT
// OpenAI's, Claude Anthropic's.
var signIns = map[string]*signInProvider{"openai": chatGPTSignIn, "anthropic": claudeSignIn}

func (a *App) signInPath(p *signInProvider) string { return filepath.Join(a.dirs.Config, p.file) }

// loadSignIn is the credential kept for p; false for none.
func (a *App) loadSignIn(p *signInProvider) (aicompat.SignIn, bool, error) {
	cred, err := readJSONStore[*aicompat.SignIn](a.signInPath(p))
	if err != nil || cred == nil {
		return aicompat.SignIn{}, false, err
	}
	return *cred, true, nil
}

func (a *App) saveSignIn(p *signInProvider, cred aicompat.SignIn) error {
	if err := os.MkdirAll(a.dirs.Config, 0o755); err != nil {
		return err
	}
	return writeSecretJSON(a.signInPath(p), cred)
}

// expiresSoon: a minute's margin, so it doesn't lapse mid-request.
func expiresSoon(expires int64) bool { return expires-aicompat.NowMS() < 60_000 }

// validSignIn is p's credential good for a request, refreshed first when
// it is about to lapse; one waiting on another's refresh reads the one it
// stored.
func (a *App) validSignIn(ctx context.Context, p *signInProvider) (aicompat.SignIn, error) {
	cred, ok, err := a.loadSignIn(p)
	if err != nil || !ok {
		return aicompat.SignIn{}, cmpErr(err, notSignedIn)
	}
	if !expiresSoon(cred.Expires) {
		return cred, nil
	}
	p.refreshing.Lock()
	defer p.refreshing.Unlock()
	if cred, ok, err = a.loadSignIn(p); err != nil || !ok {
		return aicompat.SignIn{}, cmpErr(err, notSignedIn)
	}
	if !expiresSoon(cred.Expires) {
		return cred, nil
	}
	used := cred.Refresh
	refreshed, err := p.refresh(ctx, netClient(writingTimeout), used)
	if err != nil {
		return aicompat.SignIn{}, errors.New(netErrorText(err))
	}
	// Signed out while it refreshed: stay signed out. Signed in again: the
	// new sign-in stands.
	now, ok, err := a.loadSignIn(p)
	switch {
	case err != nil || !ok:
		return aicompat.SignIn{}, cmpErr(err, notSignedIn)
	case now.Refresh != used:
		return now, nil
	}
	return refreshed, a.saveSignIn(p, refreshed)
}

// cmpErr is err, or one saying fallback when there is none.
func cmpErr(err error, fallback string) error {
	if err != nil {
		return err
	}
	return errors.New(fallback)
}

// signInFor is the Credentials' sign-in: provider's, valid for a
// request; false when not signed in.
func (a *App) signInFor(provider string) (aicompat.SignIn, bool, error) {
	p := signIns[provider]
	if p == nil {
		return aicompat.SignIn{}, false, nil
	}
	cred, err := a.validSignIn(context.Background(), p)
	if err != nil {
		if err.Error() == notSignedIn {
			return aicompat.SignIn{}, false, nil
		}
		return aicompat.SignIn{}, false, err
	}
	return cred, true, nil
}

// callbacks are the sign-ins waiting for their redirect, by port: a new
// sign-in takes the port over, and the one left unfinished ends.
var callbacks = struct {
	sync.Mutex
	stops map[int]func()
}{stops: map[int]func(){}}

// waitForCallback serves p's redirect and answers the URL it came back
// with, or fails after signInTimeout, when ctx ends, or when a new
// sign-in takes the port over. ready is closed once it listens.
func waitForCallback(ctx context.Context, p *signInProvider, page string, ready chan<- struct{}) (*url.URL, error) {
	callbacks.Lock()
	if stop := callbacks.stops[p.port]; stop != nil {
		stop()
	}
	callbacks.Unlock()
	var listener net.Listener
	var err error
	// The old listener lets the port go a moment after it is stopped.
	for try := 0; ; try++ {
		if listener, err = net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", p.port)); err == nil || try == 10 {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if err != nil {
		return nil, err
	}
	got := make(chan *url.URL, 1)
	server := &http.Server{ReadHeaderTimeout: 10 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != p.callbackPath {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(page))
		select {
		case got <- r.URL:
		default:
		}
	})}
	go server.Serve(listener)
	close(ready)
	stopped := make(chan struct{})
	stop := sync.OnceFunc(func() { close(stopped) })
	callbacks.Lock()
	callbacks.stops[p.port] = stop
	callbacks.Unlock()
	defer func() {
		callbacks.Lock()
		delete(callbacks.stops, p.port)
		callbacks.Unlock()
		// A moment for the page to reach the browser before the port goes.
		shutdown, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		server.Shutdown(shutdown)
	}()
	timeout := time.NewTimer(signInTimeout)
	defer timeout.Stop()
	select {
	case u := <-got:
		return u, nil
	case <-ctx.Done():
	case <-timeout.C:
	case <-stopped:
	}
	return nil, errors.New("login timed out or was cancelled")
}

// signIn signs in with p through the browser; told names the page to open,
// for the settings to offer by hand when no browser comes up.
func (a *App) signIn(ctx context.Context, p *signInProvider, told func(link string)) error {
	if ctx == nil {
		ctx = context.Background()
	}
	link, verifier, state := p.authorize()
	type answer struct {
		u   *url.URL
		err error
	}
	waited := make(chan answer, 1)
	ready := make(chan struct{})
	go func() {
		u, err := waitForCallback(ctx, p, aicompat.SignedInPage(p.name, "Textus"), ready)
		waited <- answer{u, err}
	}()
	select {
	case <-ready:
	case w := <-waited:
		return w.err
	}
	told(link)
	// Not fatal: the settings show the link, and the redirect still comes.
	if err := openBrowser(link); err != nil {
		log.Printf("textus: could not open the browser to sign in: %v", err)
	}
	w := <-waited
	if w.err != nil {
		return w.err
	}
	query := w.u.Query()
	if query.Get("state") != state {
		return errors.New("OAuth state mismatch")
	}
	code := query.Get("code")
	if code == "" {
		return errors.New("missing authorization code")
	}
	cred, err := p.exchange(ctx, netClient(writingTimeout), code, state, verifier)
	if err != nil {
		return errors.New(netErrorText(err))
	}
	return a.saveSignIn(p, cred)
}

// authStatus is whether a sign-in is set up (src/platform/bindings/auth.ts
// AuthStatus).
type authStatus struct {
	Configured bool `json:"configured"`
}

func init() {
	for prefix, p := range map[string]*signInProvider{"codex": chatGPTSignIn, "claude": claudeSignIn} {
		command(prefix+"_login", func(c *Call, _ none) (authStatus, error) {
			err := c.app.signIn(c.ctx, p, func(link string) { _ = c.Send(link) })
			return authStatus{Configured: err == nil}, err
		})
		command(prefix+"_auth_status", func(c *Call, _ none) (authStatus, error) {
			_, ok, err := c.app.loadSignIn(p)
			return authStatus{Configured: ok}, err
		})
		command(prefix+"_logout", func(c *Call, _ none) (nothing, error) {
			if err := os.Remove(c.app.signInPath(p)); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return nil, err
			}
			return nil, nil
		})
	}
}
