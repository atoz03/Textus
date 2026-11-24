package backend

import (
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo/netproxy"
)

// Where the app's requests go: the proxy Settings › Network names, the
// system's by default. A GUI app started from the desktop inherits no
// HTTPS_PROXY, so following the system's settings is what reaches the web
// where only a proxy does (the catalogs, the providers, the updater).

// netRoute is the proxy in effect: direct, a proxy of the settings, or
// (neither) the system's.
type netRoute struct {
	direct bool
	via    *url.URL
}

var currentRoute atomic.Pointer[netRoute]

// proxyKeys are the settings the proxy is made from; each is patched on its
// own as the user types.
var proxyKeys = [...]string{"proxyType", "proxyHost", "proxyPort", "proxyUsername", "proxyPassword"}

func proxyKeysChanged(patch jsonObject) bool {
	for _, k := range proxyKeys {
		if _, ok := patch[k]; ok {
			return true
		}
	}
	return false
}

// routeFromSettings is the route the settings ask for. An address that
// doesn't parse is reported, and the system's proxy is followed: a typo
// must not pass for a proxy in use.
func routeFromSettings(settings jsonObject) (netRoute, error) {
	text := func(key string) string {
		var s string
		_ = json.Unmarshal(settings[key], &s)
		return s
	}
	kind := strings.TrimSpace(text("proxyType"))
	host := strings.TrimSpace(text("proxyHost"))
	port := strings.TrimSpace(text("proxyPort"))
	user := strings.TrimSpace(text("proxyUsername"))
	pass := text("proxyPassword") // as typed: spaces may be part of it
	// A SOCKS5 proxy looks the names up itself: on an intranet this
	// computer's DNS can't resolve an address elsewhere.
	if kind == "socks5" {
		kind = "socks5h"
	}
	switch {
	case kind == "direct":
		return netRoute{direct: true}, nil
	case kind != "http" && kind != "https" && kind != "socks5h", host == "":
		return netRoute{}, nil
	}
	// A whole URL pasted ("http://host:port", what a proxy app shows) is
	// taken as it is.
	raw := kind + "://" + host
	if strings.Contains(host, "://") {
		raw = host
		if inner, ok := strings.CutPrefix(host, "socks5://"); ok {
			raw = "socks5h://" + inner
		}
	} else if port != "" {
		raw += ":" + port
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return netRoute{}, fmt.Errorf("the proxy address %q doesn't parse", raw)
	}
	switch u.Scheme {
	case "http", "https", "socks5", "socks5h":
	default:
		return netRoute{}, fmt.Errorf("unknown proxy scheme %q", u.Scheme)
	}
	if user != "" {
		if pass != "" {
			u.User = url.UserPassword(user, pass)
		} else {
			u.User = url.User(user)
		}
	}
	return netRoute{via: u}, nil
}

// proxyChanged takes the proxy from the settings, at launch and as they
// change, so a request made before any window opens goes through it too.
func (a *App) proxyChanged() {
	if a.stores == nil {
		return
	}
	raw, _ := a.stores.settings.snapshot()
	var settings jsonObject
	_ = json.Unmarshal(raw, &settings)
	r, err := routeFromSettings(settings)
	if err != nil {
		log.Printf("textus: proxy setting left out, requests follow the system's: %v", err)
	}
	currentRoute.Store(&r)
}

// isLoopbackHost: a model or service on this computer is reached
// directly, whatever the settings say.
func isLoopbackHost(host string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// appProxy is the proxy of every request the app makes (an
// http.Transport's Proxy).
func appProxy(req *http.Request) (*url.URL, error) {
	if isLoopbackHost(req.URL.Hostname()) {
		return nil, nil
	}
	r := currentRoute.Load()
	switch {
	case r == nil:
	case r.direct:
		return nil, nil
	case r.via != nil:
		return r.via, nil
	}
	return netproxy.Proxy(req)
}

// proxyURL is the proxy a request to target goes through now, nil for
// direct: for a process the app starts (git, an MCP server).
func proxyURL(target string) *url.URL {
	req, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		return nil
	}
	u, _ := appProxy(req)
	return u
}

// appTransport is the one transport, so connections are reused across
// requests; its Proxy reads the route afresh for each.
var appTransport = &http.Transport{
	Proxy:                 appProxy,
	DialContext:           (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
	ForceAttemptHTTP2:     true,
	MaxIdleConns:          100,
	IdleConnTimeout:       90 * time.Second,
	TLSHandshakeTimeout:   10 * time.Second,
	ExpectContinueTimeout: time.Second,
}

// userAgent names the app to the servers it asks.
func userAgent() string {
	if appVersion == "" {
		return "Textus"
	}
	return "Textus/" + appVersion
}

// appVersion is the running version, for the User-Agent and the updater.
var appVersion string

// agentTransport adds the User-Agent to a request that has none.
type agentTransport struct{ next http.RoundTripper }

func (t agentTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Header.Get("User-Agent") == "" {
		req = req.Clone(req.Context())
		req.Header.Set("User-Agent", userAgent())
	}
	return t.next.RoundTrip(req)
}

// netClient is a client for servers elsewhere, through the app's proxy,
// giving up on the whole request after total (0: never, for a stream
// whose reader keeps its own idle timeout).
func netClient(total time.Duration) *http.Client {
	return &http.Client{Timeout: total, Transport: agentTransport{appTransport}}
}

// errTooLarge is a body over the size its reader allows.
var errTooLarge = errors.New("the file is larger than allowed")

// readLimited reads resp's body, or fails with errTooLarge past max bytes,
// judged by the declared length or else while reading.
func readLimited(resp *http.Response, max int64) ([]byte, error) {
	if resp.ContentLength > max {
		return nil, errTooLarge
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > max {
		return nil, errTooLarge
	}
	return body, nil
}

// netErrorText is a failed request's error with a hint where the OS's own
// words mislead: behind a proxy, the name that didn't resolve is the
// proxy's, which reads like the site is gone.
func netErrorText(err error) string {
	text := err.Error()
	var dns *net.DNSError
	if errors.As(err, &dns) || strings.Contains(text, "no such host") {
		text += " (a name didn't resolve - if a proxy is set, its address must resolve: use its IP, or check the system proxy)"
	}
	return text
}
