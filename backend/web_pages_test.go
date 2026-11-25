package backend

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAWebPagesLabelIsItsWindowsAndTabs(t *testing.T) {
	if got := webPageLabel("window-2", "tab.1/x"); got != "page-window-2-tab_1_x" {
		t.Errorf("label: %s", got)
	}
}

func TestOnlyAnHTTPSPageOpens(t *testing.T) {
	for _, ok := range []string{"https://example.com/a?b", "HTTPS://example.com"} {
		if got, err := httpsURL(ok); err != nil || !strings.HasPrefix(got, "https://example.com") {
			t.Errorf("%s: %s %v", ok, got, err)
		}
	}
	for _, refused := range []string{"http://example.com", "file:///etc/passwd", "mygo://app/", "asset://localhost/etc/hosts", "javascript:alert(1)", "not a url", "https:foo", ""} {
		if _, err := httpsURL(refused); err == nil || !strings.Contains(err.Error(), "httpsOnly") {
			t.Errorf("%q opened: %v", refused, err)
		}
	}
}

func TestAPageGoesOnlyToHTTPSItself(t *testing.T) {
	for _, stays := range []string{"https://example.com/next", "about:blank"} {
		if !mayNavigate(stays) {
			t.Errorf("%s left the page", stays)
		}
	}
	for _, leaves := range []string{"http://example.com", "mailto:a@b.c", "textus://open?path=/etc", "file:///etc/passwd", "javascript:alert(1)", "data:text/html,x"} {
		if mayNavigate(leaves) {
			t.Errorf("%s loaded in the page", leaves)
		}
	}
}

func TestWebPageCommandsWithNoWindow(t *testing.T) {
	a := pluginApp(t)
	for name, args := range map[string]string{
		"web_page_hide":  `{"id":"t"}`,
		"web_page_close": `{"id":"t"}`,
	} {
		if got, err := run(a, name, args); err != nil || got != "null" {
			t.Errorf("%s: %s %v", name, got, err)
		}
	}
	// An address that isn't HTTPS is refused before anything else.
	if _, err := run(a, "web_page_navigate", `{"id":"t","url":"http://example.com"}`); err == nil || !strings.Contains(err.Error(), "httpsOnly") {
		t.Errorf("navigated to http: %v", err)
	}
	for name, args := range map[string]string{
		"web_page_show":     `{"id":"t","url":"https://example.com","bounds":{"x":0,"y":0,"width":10,"height":10}}`,
		"web_page_navigate": `{"id":"t","url":"https://example.com"}`,
		"web_page_reload":   `{"id":"t"}`,
	} {
		if _, err := run(a, name, args); err == nil {
			t.Errorf("%s with no window", name)
		}
	}
	// A request from the app's own pages is no web page's.
	if a.fromWebPage(httptest.NewRequest("GET", "asset://localhost/x", nil)) {
		t.Error("a request with no page came from a web page")
	}
}
