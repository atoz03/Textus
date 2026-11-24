package backend

import (
	"context"
	"encoding/json/v2"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"slices"
	"strings"
	"testing"
)

const packageURL = "https://textus.md/plugin-release/a.b-v1.0.0/a.b-1.0.0.zip"

func jsonMarshalForTest(v any) (string, error) {
	raw, err := json.Marshal(v)
	return string(raw), err
}

func TestTheFirstInstallOnADeviceIsAnInstallThenAReinstall(t *testing.T) {
	if extEvent(false, false) != "install" || extEvent(false, true) != "reinstall" || extEvent(true, true) != "update" {
		t.Error("wrong events")
	}
}

func TestTheRecordRemembersEachKindsIDsApart(t *testing.T) {
	a := &App{dirs: Dirs{Data: t.TempDir() + "/sub"}}
	if a.installedBefore("plugin", "a.b") {
		t.Error("seen before anything was installed")
	}
	a.noteInstalled("plugin", "a.b")
	a.noteInstalled("plugin", "a.b")
	if !a.installedBefore("plugin", "a.b") || a.installedBefore("theme", "a.b") {
		t.Error("the record mixed kinds up")
	}
	raw, _ := os.ReadFile(a.extRecordPath())
	if string(raw) != `{"plugin":["a.b"]}` {
		t.Errorf("%s", raw)
	}
	os.WriteFile(a.extRecordPath(), []byte("not json"), 0o600)
	if a.installedBefore("plugin", "a.b") {
		t.Error("a record that doesn't parse was read")
	}
}

func TestACountedDownloadNamesTheExtensionAndTheEventAndNothingElse(t *testing.T) {
	d := extDownload{kind: "plugin", id: "a.b", version: "1.0.0", direct: packageURL}
	u, err := url.Parse(countedDownloadURL(extStatsEndpoint, d, "install"))
	if err != nil {
		t.Fatal(err)
	}
	if u.Scheme+"://"+u.Host+u.Path != "https://textus.md/api/ext/download" {
		t.Error(u)
	}
	q := u.Query()
	var keys []string
	for k := range q {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	if !slices.Equal(keys, []string{"event", "id", "kind", "version", "via"}) || q.Get("kind") != "plugin" || q.Get("event") != "install" {
		t.Error(u)
	}
	theme := extDownload{kind: "theme", id: "plain", version: "2026-10-18T04:55:06Z",
		direct: "https://textus.md/theme-gallery/themes/plain/theme.css"}
	got := countedDownloadURL(extStatsEndpoint, theme, "update")
	if !strings.Contains(got, "file=theme.css") || !strings.Contains(got, "version=2026-10-18T04%3A55%3A06Z") {
		t.Error(got)
	}
	// A base with no slash at its end still gets /download.
	if got := countedDownloadURL("http://127.0.0.1:8787", d, "install"); !strings.HasPrefix(got, "http://127.0.0.1:8787/download?kind=plugin&") {
		t.Error(got)
	}
}

func TestOnlyAPackageOnTheWebIsCounted(t *testing.T) {
	t.Setenv("TEXTUS_EXT_STATS", extStatsEndpoint)
	a := &App{dirs: Dirs{Data: t.TempDir()}}
	if got := a.countedURL(extDownload{kind: "plugin", id: "a.b", direct: "file:///tmp/a.zip"}); got != "" {
		t.Error(got)
	}
	a.noteInstalled("plugin", "a.b")
	if got := a.countedURL(extDownload{kind: "plugin", id: "a.b", direct: packageURL}); !strings.Contains(got, "event=reinstall") {
		t.Error(got)
	}
}

func TestFromTheEndpointOnlyThePackageAskedForIsFollowed(t *testing.T) {
	at := func(s string) []*http.Request {
		u, _ := url.Parse(s)
		return []*http.Request{{URL: u}}
	}
	parse := func(s string) *url.URL { u, _ := url.Parse(s); return u }
	never := func(*url.URL) bool { return false }
	always := func(*url.URL) bool { return true }
	from := at(extStatsEndpoint + "download?id=a.b")
	other := parse("https://github.com/someone/else/releases/download/x/x.zip")
	if !mayFollow(from, parse(packageURL), packageURL, extStatsEndpoint, never) {
		t.Error("the package wasn't followed")
	}
	if mayFollow(from, other, packageURL, extStatsEndpoint, always) {
		t.Error("the endpoint led elsewhere")
	}
	// A redirect onwards from the package's address is the caller's to judge.
	if !mayFollow(at(packageURL), other, packageURL, extStatsEndpoint, always) ||
		mayFollow(at(packageURL), other, packageURL, extStatsEndpoint, never) {
		t.Error("the caller's rule was not asked")
	}
	if !atStatsEndpoint(parse(extStatsEndpoint+"download?kind=plugin"), extStatsEndpoint) ||
		atStatsEndpoint(parse(packageURL), extStatsEndpoint) || atStatsEndpoint(parse("https://textus.md/download"), extStatsEndpoint) {
		t.Error("the endpoint was misread")
	}
}

func TestACountedDownloadGoesThroughTheEndpointOnlyWhenItRedirects(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/ext/download" && r.URL.Query().Get("via") == "site":
			http.Redirect(w, r, server.URL+"/pkg.css", http.StatusFound)
		case r.URL.Path == "/api/ext/download":
			w.Write([]byte("a page, no counter"))
		default:
			w.Write([]byte("#write {}"))
		}
	}))
	defer server.Close()
	t.Setenv("TEXTUS_EXT_STATS", server.URL+"/api/ext/")
	direct := server.URL + "/pkg.css"
	follow := followRedirects(direct, func(*url.URL) bool { return false })
	ctx := context.Background()
	if data, err := galleryFetch(ctx, server.URL+"/api/ext/download?via=site", 1<<10, follow); err != nil || string(data) != "#write {}" {
		t.Error(string(data), err)
	}
	// An endpoint that answers itself is not the package.
	if _, err := galleryFetch(ctx, server.URL+"/api/ext/download", 1<<10, follow); err == nil {
		t.Error("the endpoint's own page was taken for the package")
	}
}

func TestAnEndpointThatFailsFallsBackToThePackagesOwnAddress(t *testing.T) {
	got := func(counted, fails string) (string, error) {
		return countedOrDirect(counted, packageURL, func(from string) (string, error) {
			if from == fails {
				return "", errors.New("unreachable")
			}
			return from, nil
		})
	}
	if s, _ := got("https://counted", ""); s != "https://counted" {
		t.Error(s)
	}
	if s, _ := got("https://counted", "https://counted"); s != packageURL {
		t.Error(s)
	}
	if s, _ := got("", ""); s != packageURL {
		t.Error(s)
	}
	if _, err := got("", packageURL); err == nil {
		t.Error("no error")
	}
}
