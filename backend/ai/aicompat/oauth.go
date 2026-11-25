package aicompat

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
)

// Signing in with a ChatGPT or Claude subscription: the authorize link a
// browser opens, with its PKCE verifier and state, and the code and the
// refresh token exchanged for a credential. Both use the clients of the
// providers' own command-line tools, which is all their sign-in takes.

const (
	codexClientID     = "app_EMoamEEZ73f0CkXaXp7hrann"
	codexAuthorizeURL = "https://auth.openai.com/oauth/authorize"
	codexScope        = "openid profile email offline_access"
	codexClaimPath    = "https://api.openai.com/auth"
	// CodexCallbackPort and CodexRedirectURI are where the browser comes
	// back to once signed in with ChatGPT.
	CodexCallbackPort = 1455
	CodexRedirectURI  = "http://localhost:1455/auth/callback"
	// CodexCompletionModel is a ChatGPT sign-in's low-cost default.
	CodexCompletionModel = "gpt-6-luna"

	claudeClientID     = "9d1c250a-e61b-44d9-88ed-5944d1962f5e"
	claudeAuthorizeURL = "https://claude.ai/oauth/authorize"
	claudeScopes       = "org:create_api_key user:profile user:inference user:sessions:claude_code user:mcp_servers user:file_upload"
	// ClaudeCallbackPort and ClaudeRedirectURI are where the browser comes
	// back to once signed in with Claude.
	ClaudeCallbackPort = 53692
	ClaudeRedirectURI  = "http://localhost:53692/callback"
)

// The token endpoints; tests point them elsewhere.
var (
	CodexTokenURL  = "https://auth.openai.com/oauth/token"
	ClaudeTokenURL = "https://platform.claude.com/v1/oauth/token"
)

// CodexModels are the only models the ChatGPT backend takes; anything else
// is a 400, models of the public API included. Keep in step with
// OPENAI_OAUTH_*_MODEL_PRESETS in agent-models.ts.
var CodexModels = []string{"gpt-6-astra", "gpt-6.1-sol", "gpt-6-luna"}

// CodexUsableModel is requested when a ChatGPT sign-in can use it, else
// fallback: a saved model can outlive the sign-in method it was picked
// under, or be a retired preset.
func CodexUsableModel(requested, fallback string) string {
	if slices.Contains(CodexModels, requested) {
		return requested
	}
	return fallback
}

// SignIn is a sign-in's credential, as kept in its file: the tokens, when
// the access token lapses (ms since the epoch), and a ChatGPT sign-in's
// account.
type SignIn struct {
	Access    string `json:"access"`
	Refresh   string `json:"refresh"`
	Expires   int64  `json:"expires"`
	AccountID string `json:"account_id,omitempty"`
}

// NowMS is the time in ms since the epoch.
func NowMS() int64 { return time.Now().UnixMilli() }

// NewPKCE is a PKCE S256 pair: the verifier, kept for the exchange, and
// the challenge the authorize link carries.
func NewPKCE() (verifier, challenge string) {
	raw := make([]byte, 32)
	rand.Read(raw)
	verifier = base64.RawURLEncoding.EncodeToString(raw)
	sum := sha256.Sum256([]byte(verifier))
	return verifier, base64.RawURLEncoding.EncodeToString(sum[:])
}

func newState() string {
	raw := make([]byte, 16)
	rand.Read(raw)
	return hex.EncodeToString(raw)
}

// CodexAuthorize is the ChatGPT sign-in's authorize link, its verifier
// and its state; originator names the app to OpenAI.
func CodexAuthorize(originator string) (link, verifier, state string) {
	verifier, challenge := NewPKCE()
	state = newState()
	q := url.Values{}
	for _, kv := range [][2]string{
		{"response_type", "code"}, {"client_id", codexClientID}, {"redirect_uri", CodexRedirectURI},
		{"scope", codexScope}, {"code_challenge", challenge}, {"code_challenge_method", "S256"},
		{"state", state}, {"id_token_add_organizations", "true"}, {"codex_cli_simplified_flow", "true"},
		{"originator", originator},
	} {
		q.Add(kv[0], kv[1])
	}
	return codexAuthorizeURL + "?" + q.Encode(), verifier, state
}

// ClaudeAuthorize is the Claude sign-in's authorize link and its verifier,
// which is its state too, as the sign-in expects.
func ClaudeAuthorize() (link, verifier, state string) {
	verifier, challenge := NewPKCE()
	q := url.Values{}
	for _, kv := range [][2]string{
		{"code", "true"}, {"client_id", claudeClientID}, {"response_type", "code"},
		{"redirect_uri", ClaudeRedirectURI}, {"scope", claudeScopes}, {"code_challenge", challenge},
		{"code_challenge_method", "S256"}, {"state", verifier},
	} {
		q.Add(kv[0], kv[1])
	}
	return claudeAuthorizeURL + "?" + q.Encode(), verifier, verifier
}

// tokenResponse is what a token endpoint answers.
type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
}

// postToken sends a token request; a failure reads "{what} ({status}):
// {body}".
func postToken(ctx context.Context, client *http.Client, u, contentType string, body []byte, what string) (tokenResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		return tokenResponse{}, err
	}
	req.Header.Set("Content-Type", contentType)
	resp, err := client.Do(req)
	if err != nil {
		return tokenResponse{}, err
	}
	defer resp.Body.Close()
	answer, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return tokenResponse{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return tokenResponse{}, fmt.Errorf("%s (%s): %s", what, resp.Status, answer)
	}
	var token tokenResponse
	if err := json.Unmarshal(answer, &token); err != nil {
		return tokenResponse{}, err
	}
	return token, nil
}

// codexAccount is the ChatGPT account an access token is for, from its
// JWT claims; false when it says none.
func codexAccount(accessToken string) (string, bool) {
	parts := strings.Split(accessToken, ".")
	if len(parts) < 2 {
		return "", false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", false
	}
	var claims map[string]jsontext.Value
	if json.Unmarshal(payload, &claims) != nil {
		return "", false
	}
	var auth struct {
		AccountID string `json:"chatgpt_account_id"`
	}
	if json.Unmarshal(claims[codexClaimPath], &auth) != nil || auth.AccountID == "" {
		return "", false
	}
	return auth.AccountID, true
}

func codexSignIn(token tokenResponse) (SignIn, error) {
	account, ok := codexAccount(token.AccessToken)
	if !ok {
		return SignIn{}, fmt.Errorf("failed to extract account id from token")
	}
	return SignIn{Access: token.AccessToken, Refresh: token.RefreshToken, Expires: NowMS() + token.ExpiresIn*1000, AccountID: account}, nil
}

// CodexExchange exchanges a ChatGPT sign-in's code for its credential.
func CodexExchange(ctx context.Context, client *http.Client, code, verifier string) (SignIn, error) {
	form := url.Values{"grant_type": {"authorization_code"}, "client_id": {codexClientID}, "code": {code}, "code_verifier": {verifier}, "redirect_uri": {CodexRedirectURI}}
	token, err := postToken(ctx, client, CodexTokenURL, "application/x-www-form-urlencoded", []byte(form.Encode()), "token exchange failed")
	if err != nil {
		return SignIn{}, err
	}
	return codexSignIn(token)
}

// CodexRefresh is a ChatGPT sign-in's credential renewed with its refresh
// token.
func CodexRefresh(ctx context.Context, client *http.Client, refresh string) (SignIn, error) {
	form := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh}, "client_id": {codexClientID}}
	token, err := postToken(ctx, client, CodexTokenURL, "application/x-www-form-urlencoded", []byte(form.Encode()), "token refresh failed")
	if err != nil {
		return SignIn{}, err
	}
	return codexSignIn(token)
}

// claudeSignIn is a Claude credential, refreshed a little early.
func claudeSignIn(token tokenResponse) SignIn {
	return SignIn{Access: token.AccessToken, Refresh: token.RefreshToken, Expires: NowMS() + token.ExpiresIn*1000 - 5*60*1000}
}

// ClaudeExchange exchanges a Claude sign-in's code for its credential.
func ClaudeExchange(ctx context.Context, client *http.Client, code, state, verifier string) (SignIn, error) {
	body, _ := json.Marshal(map[string]string{
		"grant_type": "authorization_code", "client_id": claudeClientID, "code": code, "state": state,
		"redirect_uri": ClaudeRedirectURI, "code_verifier": verifier,
	})
	token, err := postToken(ctx, client, ClaudeTokenURL, "application/json", body, "token exchange failed")
	if err != nil {
		return SignIn{}, err
	}
	return claudeSignIn(token), nil
}

// ClaudeRefresh is a Claude sign-in's credential renewed with its refresh
// token.
func ClaudeRefresh(ctx context.Context, client *http.Client, refresh string) (SignIn, error) {
	body, _ := json.Marshal(map[string]string{"grant_type": "refresh_token", "client_id": claudeClientID, "refresh_token": refresh})
	token, err := postToken(ctx, client, ClaudeTokenURL, "application/json", body, "token refresh failed")
	if err != nil {
		return SignIn{}, err
	}
	return claudeSignIn(token), nil
}

// SignedInPage is the page the browser shows once the sign-in is done.
func SignedInPage(provider, app string) string {
	return fmt.Sprintf(`<!doctype html>
<html><head><meta charset="utf-8"><title>%[2]s</title>
<style>
  body {
    margin: 0;
    display: flex;
    align-items: center;
    justify-content: center;
    height: 100vh;
    background: #1e1e1e;
    color: #d8d6d0;
    font-family: -apple-system, "SF Pro Text", "PingFang SC", "Noto Sans SC", Inter, system-ui, sans-serif;
  }
  .card {
    text-align: center;
    padding: 2.5rem 3rem;
  }
  .check {
    width: 48px;
    height: 48px;
    margin: 0 auto 1rem;
    border-radius: 50%%;
    background: #6c9bf7;
    display: flex;
    align-items: center;
    justify-content: center;
  }
  h1 { font-size: 18px; font-weight: 650; margin: 0 0 0.4rem; }
  p { font-size: 14px; color: #8c8c8c; margin: 0; }
</style></head>
<body>
  <div class="card">
    <div class="check">
      <svg width="22" height="22" viewBox="0 0 24 24" fill="none">
        <path d="M5 13l4 4L19 7" stroke="white" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"/>
      </svg>
    </div>
    <h1>%[1]s connected</h1>
    <p>You can close this window and go back to %[2]s.</p>
  </div>
</body></html>`, provider, app)
}
