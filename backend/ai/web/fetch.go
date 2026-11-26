package web

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/net/html/charset"

	"textus/backend/ai/aicompat"
	"textus/backend/extract"
)

// web_fetch's download: one page from the open web, read as text. Only
// public addresses are fetched, the connection pinned to the checked
// addresses (no DNS rebinding); redirects only on the same host; no
// cookies; time and size limits.

// Net is how the package reaches the web; the app sets it to its own (its
// proxy, its name), and tests keep the defaults.
var Net = struct {
	// Transport carries the requests to the search services and the
	// scholarly indexes.
	Transport http.RoundTripper
	// Proxy is the proxy a request to target goes through now, nil for
	// direct.
	Proxy func(target string) *url.URL
	// UserAgent names the app to the pages it reads.
	UserAgent func() string
}{
	Transport: http.DefaultTransport,
	Proxy:     func(string) *url.URL { return nil },
	UserAgent: func() string { return "Textus" },
}

// MaxPageChars is the most of a page's text handed to the model: a
// writing request needs a page's facts, and a long page is too expensive
// to resend every step.
const MaxPageChars = 30_000

const (
	// maxBodyBytes is the most downloaded for one page, before any of it
	// is read as text.
	maxBodyBytes = 8 << 20
	maxRedirects = 5
	fetchTimeout = 30 * time.Second
)

// Page is a page, read.
type Page struct {
	// URL is where the page was in the end, after same-host redirects.
	URL   string
	Title string
	// Text is Markdown, or plain text.
	Text string
	// Truncated: Text was cut at MaxPageChars.
	Truncated bool
}

// Fetched is a page read, or, MovedTo set, where it moved: another host,
// which isn't followed blind.
type Fetched struct {
	Page    Page
	MovedTo string
}

// Refused is web_fetch's answer for an address that isn't public.
const Refused = "That address is on this computer or a private network; web_fetch only reads public web pages."

// target is the URL web_fetch is asked for, as it will be fetched:
// http(s) only, no credentials, and plain http upgraded to https.
func target(raw string, allowLocal bool) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" {
		return nil, fmt.Errorf("Not a web address: %s", raw)
	}
	switch {
	case u.Scheme == "https":
	case u.Scheme == "http" && allowLocal:
	case u.Scheme == "http":
		u.Scheme = "https"
		if u.Port() == "80" {
			u.Host = u.Hostname()
			if strings.Contains(u.Host, ":") {
				u.Host = "[" + u.Host + "]"
			}
		}
	default:
		return nil, errors.New("web_fetch reads http and https addresses only.")
	}
	if u.User != nil {
		return nil, errors.New("web_fetch doesn't send user names or passwords.")
	}
	u.Fragment, u.RawFragment = "", ""
	return u, nil
}

// portOf is u's port, or its scheme's.
func portOf(u *url.URL) string {
	if p := u.Port(); p != "" {
		return p
	}
	if u.Scheme == "http" {
		return "80"
	}
	return "443"
}

// clientFor is a client for one request to u, refusing a host not on the
// public internet: checked and pinned when direct. Through the app's
// proxy, which resolves names itself, only literal private addresses and
// local names can be refused.
func clientFor(ctx context.Context, u *url.URL, allowLocal bool) (*http.Client, error) {
	host := u.Hostname()
	if host == "" {
		return nil, errors.New(Refused)
	}
	literal, err := netip.ParseAddr(host)
	isLiteral := err == nil
	if !allowLocal && (aicompat.IsLocalName(host) || (isLiteral && !aicompat.IsPublic(literal))) {
		return nil, errors.New(Refused)
	}
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	transport := &http.Transport{DialContext: dialer.DialContext, TLSHandshakeTimeout: 10 * time.Second, DisableKeepAlives: true}
	client := &http.Client{Timeout: fetchTimeout, Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	if !allowLocal {
		if proxy := Net.Proxy(u.String()); proxy != nil {
			transport.Proxy = http.ProxyURL(proxy)
			return client, nil
		}
	}
	if isLiteral {
		return client, nil
	}
	addrs, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil || len(addrs) == 0 {
		return nil, fmt.Errorf("Couldn't find the site %s.", host)
	}
	if !allowLocal && !aicompat.AllPublic(addrs) {
		return nil, errors.New(Refused)
	}
	port := portOf(u)
	transport.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		var last error
		for _, ip := range addrs {
			conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip.Unmap().String(), port))
			if err == nil {
				return conn, nil
			}
			last = err
		}
		return nil, last
	}
	return client, nil
}

// sameSite reports whether a redirect stays on one site: the same host and
// port, `www.` or not.
func sameSite(a, b *url.URL) bool {
	host := func(u *url.URL) string { return strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.") }
	return host(a) == host(b) && portOf(a) == portOf(b)
}

// fetchPage downloads raw and reads it as text: an HTML page's main
// content as Markdown, plain text and Markdown as they are. allowLocal
// loosens it for the tests' server on 127.0.0.1.
func fetchPage(ctx context.Context, raw string, allowLocal bool) (Fetched, error) {
	u, err := target(raw, allowLocal)
	if err != nil {
		return Fetched{}, err
	}
	for range maxRedirects + 1 {
		client, err := clientFor(ctx, u, allowLocal)
		if err != nil {
			return Fetched{}, err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
		if err != nil {
			return Fetched{}, err
		}
		req.Header.Set("Accept", "text/html,application/xhtml+xml,text/plain,text/markdown,application/pdf;q=0.9,*/*;q=0.5")
		req.Header.Set("User-Agent", Net.UserAgent())
		resp, err := client.Do(req)
		if err != nil {
			if isTimeout(err) {
				return Fetched{}, fmt.Errorf("%s took too long to answer.", u.Hostname())
			}
			return Fetched{}, fmt.Errorf("Couldn't reach %s.", u.Hostname())
		}
		if resp.StatusCode >= 300 && resp.StatusCode <= 399 {
			resp.Body.Close()
			location := resp.Header.Get("Location")
			next, err := u.Parse(location)
			if location == "" || err != nil {
				return Fetched{}, fmt.Errorf("The page redirected without saying where (%s).", statusText(resp.StatusCode))
			}
			if next, err = target(next.String(), allowLocal); err != nil {
				return Fetched{}, err
			}
			if !sameSite(u, next) {
				return Fetched{MovedTo: next.String()}, nil
			}
			u = next
			continue
		}
		page, err := readResponse(resp, u)
		return Fetched{Page: page}, err
	}
	return Fetched{}, errors.New("The page redirected too many times.")
}

func readResponse(resp *http.Response, u *url.URL) (Page, error) {
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return Page{}, fmt.Errorf("The site answered %s.", statusText(resp.StatusCode))
	}
	if resp.ContentLength > maxBodyBytes {
		return Page{}, errors.New("The page is too large to read.")
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes+1))
	if err != nil {
		return Page{}, err
	}
	if len(body) > maxBodyBytes {
		return Page{}, errors.New("The page is too large to read.")
	}
	return readPage(u, strings.ToLower(resp.Header.Get("Content-Type")), body)
}

// readPage is what a downloaded body says, as a Page.
func readPage(u *url.URL, contentType string, body []byte) (Page, error) {
	mime, _, _ := strings.Cut(contentType, ";")
	mime = strings.TrimSpace(mime)
	looksHTML := func() bool {
		head := strings.ToLower(string(body[:min(len(body), 512)]))
		return strings.Contains(head, "<html") || strings.Contains(head, "<!doctype html")
	}
	var title, text string
	switch {
	case mime == "application/pdf" || (mime == "" && bytes.HasPrefix(body, []byte("%PDF"))):
		var err error
		if text, err = extract.PDFTextOf(body); err != nil {
			return Page{}, err
		}
	case mime == "text/html" || mime == "application/xhtml+xml" || (mime == "" && looksHTML()):
		title, text = extract.PageMarkdown(decode(contentType, body), u.String())
	case strings.HasPrefix(mime, "text/") || mime == "application/json" || strings.HasSuffix(mime, "+json") ||
		strings.HasSuffix(mime, "+xml") || mime == "application/xml" || mime == "":
		text = decode(contentType, body)
	default:
		return Page{}, fmt.Errorf("This address is a file web_fetch can't read (%s).", mime)
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return Page{}, errors.New("The page has no readable text - it may need JavaScript to show anything.")
	}
	truncated := utf8.RuneCountInString(text) > MaxPageChars
	if truncated {
		text = firstChars(text, MaxPageChars)
	}
	return Page{URL: u.String(), Title: strings.TrimSpace(title), Text: text, Truncated: truncated}, nil
}

// decode is body as text, in the charset the Content-Type header or the
// page's own `<meta charset>` names - many Chinese sites still serve GBK.
func decode(contentType string, body []byte) string {
	declared := func(s string) func([]byte) ([]byte, error) {
		lower := strings.ToLower(s)
		at := strings.Index(lower, "charset=")
		if at < 0 {
			return nil
		}
		rest := strings.TrimLeft(lower[at+len("charset="):], `"'`)
		end := strings.IndexFunc(rest, func(r rune) bool {
			return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_')
		})
		if end >= 0 {
			rest = rest[:end]
		}
		if e, _ := charset.Lookup(rest); e != nil {
			return e.NewDecoder().Bytes
		}
		return nil
	}
	decoder := declared(contentType)
	if decoder == nil {
		decoder = declared(string(body[:min(len(body), 2048)]))
	}
	if decoder != nil {
		if text, err := decoder(body); err == nil {
			return string(text)
		}
	}
	return strings.ToValidUTF8(string(bytes.TrimPrefix(body, []byte("\xef\xbb\xbf"))), "\uFFFD")
}
