package backend

import (
	"errors"
	"math"
	"net/http"
	"net/url"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo"
)

// Web pages in a tab as native pages of their own, laid over the tab's
// area by the frontend: unlike a frame, they open every site. MyGo puts no
// second page in a window of pages, so each is a frameless child window of
// its editor window, kept over the tab as the window moves. HTTPS only,
// and untrusted: a page calls nothing of the backend, hears none of its
// events and reads none of its files; other links go to the browser.

const (
	// eventWebPageNavigated tells a window's tabs where their pages are.
	// Payload: webPageNavigated.
	eventWebPageNavigated = "web-page-navigated"
	// webPageCloseAfter is how long a closed page waits before it goes:
	// WebKit can still be drawing or scrolling it, and a page let go
	// mid-frame can crash.
	webPageCloseAfter = 500 * time.Millisecond
	// safariUserAgent is what a page is told it runs in on macOS. WKWebView's
	// own name stops before Safari's "Version/… Safari/…", and sites read
	// that as an old browser: a plain Google, Bilibili's "your browser is
	// too old".
	safariUserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.6 Safari/605.1.15"
)

// pageBounds is where a page goes in its window, in CSS pixels from the top
// left of the window's page. Mirrors PageBounds in
// src/platform/bindings/app.ts.
type pageBounds struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

// webPageNavigated: a page started to load an address, or finished - the
// address its tab's bar shows. A load that never finishes is one that
// failed (useNativeWebPage.ts).
type webPageNavigated struct {
	ID      string `json:"id"`
	URL     string `json:"url"`
	Loading bool   `json:"loading"`
}

// webPage is a tab's page.
type webPage struct {
	id    string
	owner *appWindow
	win   *mygo.Window
	// bounds is where in the owner's page it goes; closes counts the
	// closes, which a show since cancels. Both under webPages.mu.
	bounds pageBounds
	closes uint64
	// stop takes its listeners off the owner.
	stop []func()
}

// webPages are the pages open, by label.
type webPages struct {
	mu   sync.Mutex
	open map[string]*webPage
}

// webPageLabel is tab id's page in window owner: one per window and tab.
func webPageLabel(owner, id string) string {
	var b strings.Builder
	b.WriteString("page-" + owner + "-")
	for _, c := range id {
		if c < 128 && (c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-') {
			b.WriteRune(c)
		} else {
			b.WriteByte('_')
		}
	}
	return b.String()
}

// httpsURL is raw if a page may show it: an HTTPS address.
func httpsURL(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || !strings.EqualFold(u.Scheme, "https") || u.Host == "" {
		return "", codeError("httpsOnly")
	}
	u.Scheme = "https"
	return u.String(), nil
}

// mayNavigate is whether a page goes to an address itself: an HTTPS one,
// or about:blank and the like; any other opens outside.
func mayNavigate(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	return strings.EqualFold(u.Scheme, "https") && u.Host != "" || strings.EqualFold(u.Scheme, "about")
}

func (a *App) webPage(label string) *webPage {
	a.pages.mu.Lock()
	defer a.pages.mu.Unlock()
	return a.pages.open[label]
}

// webPageOf is the page window w shows, if it is one.
func (a *App) webPageOf(w *mygo.Window) *webPage {
	if w == nil {
		return nil
	}
	a.pages.mu.Lock()
	defer a.pages.mu.Unlock()
	for _, p := range a.pages.open {
		if p.win == w {
			return p
		}
	}
	return nil
}

// fromWebPage is whether a request to the app's schemes comes from a web
// page in a tab: a site never reads the disk.
func (a *App) fromWebPage(r *http.Request) bool {
	page := mygo.CallerPage(r.Context())
	return page != nil && a.webPageOf(page.Window()) != nil
}

// placeWebPage puts the page where its tab is in the owner's page, now.
func (a *App) placeWebPage(p *webPage) {
	if p.owner.win.IsDestroyed() || p.win.IsDestroyed() {
		return
	}
	a.pages.mu.Lock()
	b := p.bounds
	a.pages.mu.Unlock()
	at := p.owner.win.ContentBounds()
	round := func(v float64) int { return int(math.Round(v)) }
	p.win.SetBounds(mygo.Rectangle{
		X: at.X + round(b.X), Y: at.Y + round(b.Y),
		Width: max(1, round(b.Width)), Height: max(1, round(b.Height)),
	})
}

// showWebPage shows tab id's page at bounds, making it the first time; an
// open one is moved and shown again where it was left.
func (a *App) showWebPage(owner *appWindow, id, address string, bounds pageBounds) error {
	label := webPageLabel(owner.label, id)
	if p := a.webPage(label); p != nil {
		a.pages.mu.Lock()
		p.closes++
		p.bounds = bounds
		a.pages.mu.Unlock()
		a.placeWebPage(p)
		p.win.ShowInactive()
		return nil
	}
	address, err := httpsURL(address)
	if err != nil {
		return err
	}
	page := mygo.PageOptions{Untrusted: true}
	if runtime.GOOS == "darwin" {
		page.UserAgent = safariUserAgent
	}
	win := mygo.NewWindow(mygo.WindowOptions{
		Parent: owner.win, Hidden: true, Frameless: true,
		SkipTaskbar: true, DisableResize: true, DisableShadow: true,
		DisableMinimize: true, DisableMaximize: true, DisableFullScreen: true,
		Width: 1, Height: 1, Page: page,
	})
	p := &webPage{id: id, owner: owner, win: win, bounds: bounds}
	a.pages.mu.Lock()
	if a.pages.open == nil {
		a.pages.open = map[string]*webPage{}
	}
	a.pages.open[label] = p
	a.pages.mu.Unlock()
	a.wireWebPage(label, p)
	a.placeWebPage(p)
	win.ShowInactive()
	a.navigated(p, address, true)
	return win.Page().LoadURL(address)
}

// wireWebPage keeps the page over its tab, its links in their place, and
// its tab told where it is.
func (a *App) wireWebPage(label string, p *webPage) {
	page := p.win.Page()
	page.OnWillNavigate(func(e *mygo.NavigateEvent) {
		if !mayNavigate(e.URL) {
			e.PreventDefault()
			openOutside(e.URL)
		}
	})
	// A link to a new window opens in the browser; a pop-up the page
	// opens on its own, nowhere.
	page.SetWindowOpenHandler(func(req mygo.WindowOpenRequest) *mygo.WindowOptions {
		if req.UserInitiated {
			openOutside(req.URL)
		}
		return nil
	})
	page.OnDidNavigate(func(address string) { a.navigated(p, address, true) })
	page.OnDidFinishLoad(func() { a.navigated(p, page.URL(), false) })
	// The window moved or changed size: its pages go with their tabs.
	place := func() { a.placeWebPage(p) }
	p.stop = append(p.stop, p.owner.win.OnMove(place), p.owner.win.OnResize(place))
	p.win.OnClosed(func() {
		a.pages.mu.Lock()
		if a.pages.open[label] == p {
			delete(a.pages.open, label)
		}
		a.pages.mu.Unlock()
		for _, stop := range p.stop {
			stop()
		}
	})
}

// navigated tells the page's tab where it is.
func (a *App) navigated(p *webPage, address string, loading bool) {
	if address == "" || strings.HasPrefix(address, "about:") {
		return
	}
	_ = p.owner.emit(eventWebPageNavigated, webPageNavigated{ID: p.id, URL: address, Loading: loading})
}

// closeWebPage lets tab id's page go: hidden now, gone a moment later
// unless shown again first.
func (a *App) closeWebPage(owner, id string) {
	label := webPageLabel(owner, id)
	p := a.webPage(label)
	if p == nil {
		return
	}
	p.win.Hide()
	a.pages.mu.Lock()
	p.closes++
	closes := p.closes
	a.pages.mu.Unlock()
	time.AfterFunc(webPageCloseAfter, func() {
		a.pages.mu.Lock()
		still := p.closes == closes && a.pages.open[label] == p
		a.pages.mu.Unlock()
		if still {
			p.win.Destroy()
		}
	})
}

// closeWebPagesOf lets the pages of an editor window that closed go.
func (a *App) closeWebPagesOf(owner string) {
	a.pages.mu.Lock()
	var gone []*webPage
	for _, p := range a.pages.open {
		if p.owner.label == owner {
			gone = append(gone, p)
		}
	}
	a.pages.mu.Unlock()
	for _, p := range gone {
		p.win.Destroy()
	}
}

// openWebPage is tab id's page in the calling window, which must be open.
func openWebPage(c *Call, id string) (*webPage, error) {
	owner, err := c.win()
	if err != nil {
		return nil, err
	}
	p := c.app.webPage(webPageLabel(owner.label, id))
	if p == nil {
		return nil, errors.New("This page is closed.")
	}
	return p, nil
}

func init() {
	command("web_page_show", func(c *Call, a struct {
		ID     string     `json:"id"`
		URL    string     `json:"url"`
		Bounds pageBounds `json:"bounds"`
	}) (nothing, error) {
		owner, err := c.win()
		if err != nil {
			return nil, err
		}
		return nil, c.app.showWebPage(owner, a.ID, a.URL, a.Bounds)
	})
	// Hidden: its tab went out of sight, or a menu or dialog is over it
	// that a native page would cover.
	command("web_page_hide", func(c *Call, a struct {
		ID string `json:"id"`
	}) (nothing, error) {
		if p, err := openWebPage(c, a.ID); err == nil {
			p.win.Hide()
		}
		return nil, nil
	})
	// Its tab closed.
	command("web_page_close", func(c *Call, a struct {
		ID string `json:"id"`
	}) (nothing, error) {
		if c.window != nil {
			c.app.closeWebPage(c.window.label, a.ID)
		}
		return nil, nil
	})
	// An address typed in its tab's bar.
	command("web_page_navigate", func(c *Call, a struct {
		ID  string `json:"id"`
		URL string `json:"url"`
	}) (nothing, error) {
		address, err := httpsURL(a.URL)
		if err != nil {
			return nil, err
		}
		p, err := openWebPage(c, a.ID)
		if err != nil {
			return nil, err
		}
		c.app.navigated(p, address, true)
		return nil, p.win.Page().LoadURL(address)
	})
	command("web_page_reload", func(c *Call, a struct {
		ID string `json:"id"`
	}) (nothing, error) {
		p, err := openWebPage(c, a.ID)
		if err != nil {
			return nil, err
		}
		c.app.navigated(p, p.win.Page().URL(), true)
		p.win.Page().Reload()
		return nil, nil
	})
}
