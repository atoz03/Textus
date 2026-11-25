package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	"golang.org/x/text/encoding/simplifiedchinese"
)

func TestOnlyWebAddressesAreFetchedAndHTTPBecomesHTTPS(t *testing.T) {
	if u, err := target("http://example.com/a#b", false); err != nil || u.String() != "https://example.com/a" {
		t.Fatal(u, err)
	}
	if u, err := target("http://example.com:80/", false); err != nil || u.String() != "https://example.com/" {
		t.Fatal(u, err)
	}
	for _, raw := range []string{"file:///etc/passwd", "ftp://example.com/", "https://user:pw@example.com/", "not a url"} {
		if _, err := target(raw, false); err == nil {
			t.Error(raw)
		}
	}
}

func TestLocalAndPrivateHostsAreRefusedBeforeConnecting(t *testing.T) {
	for _, u := range []string{
		"https://localhost/", "https://127.0.0.1/", "https://[::1]:8080/", "https://169.254.169.254/latest/meta-data/",
		"https://printer.local/", "https://10.0.0.1/admin", "https://[::ffff:127.0.0.1]/", "https://[64:ff9b::a9fe:a9fe]/",
		"https://[2002:c0a8:0101::1]/", "https://100.64.0.1/",
	} {
		if _, err := fetchPage(context.Background(), u, false); err == nil || err.Error() != Refused {
			t.Error(u, err)
		}
	}
}

// replies answers each request with the next of responses, and hands over
// the requests it got.
func replies(t *testing.T, responses ...func(w http.ResponseWriter)) (string, chan *http.Request) {
	t.Helper()
	var mu sync.Mutex
	got := make(chan *http.Request, len(responses)+1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		got <- r
		if len(responses) == 0 {
			http.Error(w, "no more", http.StatusGone)
			return
		}
		responses[0](w)
		responses = responses[1:]
	}))
	t.Cleanup(server.Close)
	return server.URL, got
}

func answer(status int, headers map[string]string, body string) func(w http.ResponseWriter) {
	return func(w http.ResponseWriter) {
		for k, v := range headers {
			w.Header().Set(k, v)
		}
		w.WriteHeader(status)
		w.Write([]byte(body))
	}
}

func TestAPageComesBackAsItsReadableText(t *testing.T) {
	html := "<html><head><title>Tides</title></head><body><nav>Home | About</nav><article><h1>Tides</h1><p>The moon pulls the sea twice a day, and the water rises and falls along every coast of the world in a rhythm sailors have counted on for centuries.</p><p>Spring tides come at new and full moon, when the sun and the moon pull in a line.</p></article><script>track()</script></body></html>"
	base, _ := replies(t, answer(200, map[string]string{"Content-Type": "text/html; charset=utf-8"}, html))
	got, err := fetchPage(context.Background(), base+"/tides", true)
	if err != nil || got.MovedTo != "" || got.Page.Title != "Tides" || !strings.Contains(got.Page.Text, "Spring tides come at new and full moon") ||
		strings.Contains(got.Page.Text, "track()") || got.Page.Truncated {
		t.Fatal(got, err)
	}
}

func TestASameHostRedirectIsFollowedAndAnotherHostIsReported(t *testing.T) {
	base, _ := replies(t,
		answer(301, map[string]string{"Location": "/new"}, ""),
		answer(200, map[string]string{"Content-Type": "text/plain"}, "moved here"))
	got, err := fetchPage(context.Background(), base+"/old", true)
	if err != nil || got.Page.Text != "moved here" || !strings.HasSuffix(got.Page.URL, "/new") {
		t.Fatal(got, err)
	}
	base, _ = replies(t, answer(302, map[string]string{"Location": "https://elsewhere.example/page"}, ""))
	if got, err := fetchPage(context.Background(), base+"/old", true); err != nil || got.MovedTo != "https://elsewhere.example/page" {
		t.Fatal(got, err)
	}
}

func TestWWWAndTheBareDomainAreOneSite(t *testing.T) {
	u := func(s string) *url.URL { p, _ := url.Parse(s); return p }
	if !sameSite(u("https://www.rust-lang.org/"), u("https://rust-lang.org/")) ||
		sameSite(u("https://rust-lang.org/"), u("https://evil.example/")) ||
		sameSite(u("https://a.example/"), u("https://a.example:8443/")) {
		t.Fatal("sites judged wrong")
	}
}

func TestRedirectLoopsStop(t *testing.T) {
	var loop []func(http.ResponseWriter)
	for range 8 {
		loop = append(loop, answer(302, map[string]string{"Location": "/loop"}, ""))
	}
	base, _ := replies(t, loop...)
	if _, err := fetchPage(context.Background(), base+"/loop", true); err == nil || !strings.Contains(err.Error(), "too many times") {
		t.Fatal(err)
	}
}

func TestNoCookiesAreSentBack(t *testing.T) {
	base, got := replies(t,
		answer(302, map[string]string{"Set-Cookie": "id=1", "Location": "/next"}, ""),
		answer(200, map[string]string{"Content-Type": "text/plain"}, "ok"))
	if _, err := fetchPage(context.Background(), base+"/start", true); err != nil {
		t.Fatal(err)
	}
	<-got
	if second := <-got; second.Header.Get("Cookie") != "" {
		t.Fatal(second.Header)
	}
}

func TestALongPageIsCutAndSaysSo(t *testing.T) {
	base, _ := replies(t, answer(200, map[string]string{"Content-Type": "text/plain"}, strings.Repeat("word ", MaxPageChars)))
	got, err := fetchPage(context.Background(), base, true)
	if err != nil || !got.Page.Truncated || utf8.RuneCountInString(got.Page.Text) != MaxPageChars {
		t.Fatal(got.Page.Truncated, err)
	}
}

func TestAGBKPageIsDecodedByItsMetaCharset(t *testing.T) {
	gbk, err := simplifiedchinese.GBK.NewEncoder().String(`<html><head><meta charset="gbk"><title>潮汐</title></head><body><p>月亮每天两次牵引海水。</p></body></html>`)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse("https://example.cn/")
	page, err := readPage(u, "text/html", []byte(gbk))
	if err != nil || page.Title != "潮汐" || !strings.Contains(page.Text, "月亮每天两次牵引海水") {
		t.Fatal(page, err)
	}
}

func TestFilesThatAreNotTextAreRefused(t *testing.T) {
	u, _ := url.Parse("https://example.com/a.zip")
	if _, err := readPage(u, "application/zip", []byte("PK\x03\x04")); err == nil {
		t.Fatal("a zip read as a page")
	}
}
