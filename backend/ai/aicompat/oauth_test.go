package aicompat

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json/v2"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestTheAuthorizeLinksCarryTheChallengeOfTheirVerifier(t *testing.T) {
	link, verifier, state := CodexAuthorize("textus")
	u, _ := url.Parse(link)
	q := u.Query()
	sum := sha256.Sum256([]byte(verifier))
	if u.Host != "auth.openai.com" || q.Get("code_challenge") != base64.RawURLEncoding.EncodeToString(sum[:]) || q.Get("state") != state ||
		len(state) != 32 || q.Get("redirect_uri") != CodexRedirectURI || q.Get("originator") != "textus" || q.Get("codex_cli_simplified_flow") != "true" {
		t.Fatal(link)
	}
	link, verifier, state = ClaudeAuthorize()
	q = mustQuery(t, link)
	if state != verifier || q.Get("state") != verifier || q.Get("code") != "true" || q.Get("redirect_uri") != ClaudeRedirectURI ||
		!strings.Contains(q.Get("scope"), "user:inference") {
		t.Fatal(link)
	}
	if again, _, _ := ClaudeAuthorize(); again == link {
		t.Fatal("two sign-ins share a verifier")
	}
}

func mustQuery(t *testing.T, link string) url.Values {
	u, err := url.Parse(link)
	if err != nil {
		t.Fatal(err)
	}
	return u.Query()
}

// jwt is an access token whose claims are claims.
func jwt(claims string) string {
	return "h." + base64.RawURLEncoding.EncodeToString([]byte(claims)) + ".s"
}

func TestACodeIsExchangedForACredentialWithItsAccount(t *testing.T) {
	var form url.Values
	var claude map[string]string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if r.URL.Path == "/claude" {
			json.Unmarshal(body, &claude)
			w.Write([]byte(`{"access_token":"ca","refresh_token":"cr","expires_in":3600}`))
			return
		}
		form, _ = url.ParseQuery(string(body))
		if form.Get("code") == "bad" {
			http.Error(w, `{"error":"invalid_grant"}`, http.StatusBadRequest)
			return
		}
		w.Write([]byte(`{"access_token":"` + jwt(`{"https://api.openai.com/auth":{"chatgpt_account_id":"acct-1"},"exp":1}`) + `","refresh_token":"r1","expires_in":3600}`))
	}))
	defer server.Close()
	codexURL, claudeURL := CodexTokenURL, ClaudeTokenURL
	CodexTokenURL, ClaudeTokenURL = server.URL+"/codex", server.URL+"/claude"
	defer func() { CodexTokenURL, ClaudeTokenURL = codexURL, claudeURL }()

	before := NowMS()
	cred, err := CodexExchange(context.Background(), server.Client(), "c1", "v1")
	if err != nil || cred.AccountID != "acct-1" || cred.Refresh != "r1" || cred.Expires < before+3_600_000 ||
		form.Get("grant_type") != "authorization_code" || form.Get("code_verifier") != "v1" || form.Get("redirect_uri") != CodexRedirectURI {
		t.Fatal(cred, err, form)
	}
	if _, err := CodexRefresh(context.Background(), server.Client(), "r1"); err != nil || form.Get("grant_type") != "refresh_token" || form.Get("refresh_token") != "r1" {
		t.Fatal(err, form)
	}
	if _, err := CodexExchange(context.Background(), server.Client(), "bad", "v1"); err == nil || !strings.HasPrefix(err.Error(), "token exchange failed (400 Bad Request): ") {
		t.Fatal(err)
	}
	cred, err = ClaudeExchange(context.Background(), server.Client(), "c2", "s2", "v2")
	// Refreshed five minutes early.
	if err != nil || cred.Access != "ca" || cred.AccountID != "" || cred.Expires > NowMS()+3_300_000 || cred.Expires < before+3_300_000-1000 ||
		claude["code"] != "c2" || claude["state"] != "s2" || claude["code_verifier"] != "v2" {
		t.Fatal(cred, err, claude)
	}
	if raw, _ := json.Marshal(cred); strings.Contains(string(raw), "account_id") {
		t.Fatal("a Claude credential has an account:", string(raw))
	}
}

func TestATokenWithoutItsAccountIsRefused(t *testing.T) {
	if _, ok := codexAccount(jwt(`{"sub":"x"}`)); ok {
		t.Fatal("no account claim")
	}
	if _, ok := codexAccount("not a token"); ok {
		t.Fatal("not a token")
	}
	if id, ok := codexAccount(jwt(`{"https://api.openai.com/auth":{"chatgpt_account_id":"a"},"aud":["x"]}`)); !ok || id != "a" {
		t.Fatal(id)
	}
}

func TestASignInUsesOnlyTheModelsItsBackendTakes(t *testing.T) {
	if CodexUsableModel("gpt-6-astra", "gpt-6-luna") != "gpt-6-astra" || CodexUsableModel("gpt-4o", "gpt-6-luna") != "gpt-6-luna" ||
		CodexUsableModel("", "gpt-6.1-sol") != "gpt-6.1-sol" {
		t.Fatal("clamped wrong")
	}
	if page := SignedInPage("Claude", "Textus"); !strings.Contains(page, "<h1>Claude connected</h1>") || !strings.Contains(page, "border-radius: 50%;") {
		t.Fatal(page)
	}
}
