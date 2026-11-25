package backend

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"textus/backend/ai"
	"textus/backend/ai/aicompat"
)

// testSignIn is a sign-in on a free port whose token endpoints are refresh
// and exchange, and an app whose config folder is a test's own.
func testSignIn(t *testing.T, refresh func(token string) (aicompat.SignIn, error)) (*signInProvider, *App) {
	t.Helper()
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	p := &signInProvider{
		file: "auth-test.json", name: "Test", port: port, callbackPath: "/callback",
		authorize: func() (string, string, string) { return "https://example.invalid/authorize", "v1", "s1" },
		exchange: func(_ context.Context, _ *http.Client, code, state, verifier string) (aicompat.SignIn, error) {
			if code != "c1" || state != "s1" || verifier != "v1" {
				return aicompat.SignIn{}, errors.New("wrong exchange")
			}
			return aicompat.SignIn{Access: "a1", Refresh: "r1", Expires: aicompat.NowMS() + 3_600_000}, nil
		},
		refresh: func(_ context.Context, _ *http.Client, token string) (aicompat.SignIn, error) { return refresh(token) },
	}
	opener := openBrowser
	openBrowser = func(string) error { return nil }
	t.Cleanup(func() { openBrowser = opener })
	return p, &App{dirs: Dirs{Config: t.TempDir(), Data: t.TempDir()}}
}

// browse is the browser coming back to the sign-in's port with query.
func browse(p *signInProvider, query string) {
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d%s?%s", p.port, p.callbackPath, query))
	if err == nil {
		resp.Body.Close()
	}
}

func TestASignInTakesTheCodeTheBrowserComesBackWith(t *testing.T) {
	p, a := testSignIn(t, nil)
	var told string
	err := a.signIn(context.Background(), p, func(link string) {
		told = link
		go browse(p, "code=c1&state=s1")
	})
	if err != nil || told != "https://example.invalid/authorize" {
		t.Fatal(err, told)
	}
	cred, ok, err := a.loadSignIn(p)
	if err != nil || !ok || cred.Access != "a1" {
		t.Fatal(cred, ok, err)
	}
	if info, _ := os.Stat(a.signInPath(p)); info.Mode().Perm() != 0o600 {
		t.Fatal("the credential is readable by others:", info.Mode())
	}
	// Someone else's redirect, carrying another state, is refused.
	if err := a.signIn(context.Background(), p, func(string) { go browse(p, "code=c1&state=forged") }); err == nil || err.Error() != "OAuth state mismatch" {
		t.Fatal(err)
	}
	if err := a.signIn(context.Background(), p, func(string) { go browse(p, "state=s1") }); err == nil || err.Error() != "missing authorization code" {
		t.Fatal(err)
	}
}

func TestANewSignInTakesThePortOverFromOneLeftUnfinished(t *testing.T) {
	p, a := testSignIn(t, nil)
	first := make(chan error)
	started := make(chan struct{})
	go func() {
		first <- a.signIn(context.Background(), p, func(string) { close(started) })
	}()
	<-started
	err := a.signIn(context.Background(), p, func(string) { go browse(p, "code=c1&state=s1") })
	if err != nil {
		t.Fatal(err)
	}
	if err := <-first; err == nil || err.Error() != "login timed out or was cancelled" {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := a.signIn(ctx, p, func(string) {}); err == nil || err.Error() != "login timed out or was cancelled" {
		t.Fatal(err)
	}
}

func TestACredentialIsRefreshedOnceBeforeItLapses(t *testing.T) {
	var refreshes atomic.Int32
	p, a := testSignIn(t, func(token string) (aicompat.SignIn, error) {
		refreshes.Add(1)
		if token != "r-old" {
			return aicompat.SignIn{}, errors.New("token refresh failed (400 Bad Request): invalid_grant")
		}
		return aicompat.SignIn{Access: "a-new", Refresh: "r-new", Expires: aicompat.NowMS() + 3_600_000}, nil
	})
	if _, err := a.validSignIn(context.Background(), p); err == nil || err.Error() != notSignedIn {
		t.Fatal(err)
	}
	a.saveSignIn(p, aicompat.SignIn{Access: "a-old", Refresh: "r-old", Expires: aicompat.NowMS() + 30_000})
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			if cred, err := a.validSignIn(context.Background(), p); err != nil || cred.Access != "a-new" {
				t.Error(cred, err)
			}
		})
	}
	wg.Wait()
	if refreshes.Load() != 1 {
		t.Fatal(refreshes.Load(), "refreshes")
	}
	// Still good: no refresh.
	if cred, err := a.validSignIn(context.Background(), p); err != nil || cred.Access != "a-new" || refreshes.Load() != 1 {
		t.Fatal(cred, err)
	}
	// A refresh refused says so.
	a.saveSignIn(p, aicompat.SignIn{Access: "a", Refresh: "r-revoked", Expires: 0})
	if _, err := a.validSignIn(context.Background(), p); err == nil || !strings.Contains(err.Error(), "invalid_grant") {
		t.Fatal(err)
	}
}

func TestASignInGoesBeforeAKeyAndItsFailureIsSaid(t *testing.T) {
	a := &App{dirs: Dirs{Config: t.TempDir(), Data: t.TempDir()}, defaults: map[string]bool{"textusAi": true}}
	a.changeKeys(func(keys map[string]string) bool { keys["openai"], keys["anthropic"] = "sk-o", "sk-a"; return true })
	creds := a.credentials()
	if r, err := creds.Route("openai", ""); err != nil || r.SignIn != nil || deref(r.Key) != "sk-o" {
		t.Fatal(r, err)
	}
	a.saveSignIn(chatGPTSignIn, aicompat.SignIn{Access: "tok", Refresh: "r", Expires: aicompat.NowMS() + 3_600_000, AccountID: "acct"})
	p, _, err := creds.AgentProvider("openai", "gpt-4o", aicompat.Auto, nil, nil)
	if err != nil || p.Endpoint.Kind != aicompat.CodexEndpoint || p.Endpoint.AccountID != "acct" || p.Model != "gpt-6.1-sol" || p.Endpoint.Originator != ai.Originator {
		t.Fatal(p, err)
	}
	a.saveSignIn(claudeSignIn, aicompat.SignIn{Access: "ctok", Refresh: "r", Expires: aicompat.NowMS() + 3_600_000})
	if p, _, err := creds.AgentProvider("anthropic", "", aicompat.Auto, nil, nil); err != nil || p.Endpoint.Anthropic.OAuthToken != "ctok" || p.Endpoint.Anthropic.APIKey != "" {
		t.Fatal(p, err)
	}
	// Signed in, but its refresh failed: not quietly the key instead.
	a.saveSignIn(claudeSignIn, aicompat.SignIn{Access: "ctok", Refresh: "r", Expires: 0})
	refresh := claudeSignIn.refresh
	claudeSignIn.refresh = func(context.Context, *http.Client, string) (aicompat.SignIn, error) {
		return aicompat.SignIn{}, errors.New("token refresh failed (401 Unauthorized): revoked")
	}
	defer func() { claudeSignIn.refresh = refresh }()
	if _, err := creds.Route("anthropic", ""); err == nil || err.Error() != "Claude sign-in: token refresh failed (401 Unauthorized): revoked" {
		t.Fatal(err)
	}
	if got, err := run(a, "claude_logout", `{}`); err != nil || got != "null" {
		t.Fatal(got, err)
	}
	if got, _ := run(a, "claude_auth_status", `{}`); got != `{"configured":false}` {
		t.Fatal(got)
	}
	if got, _ := run(a, "codex_auth_status", `{}`); got != `{"configured":true}` {
		t.Fatal(got)
	}
	if r, err := creds.Route("anthropic", ""); err != nil || r.SignIn != nil || deref(r.Key) != "sk-a" {
		t.Fatal(r, err)
	}
}
