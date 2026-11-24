package backend

import (
	"context"
	"encoding/json/v2"
	"io"
	"net/http"
	"net/http/httptest"
	"path"
	"runtime"
	"strings"
	"testing"
)

func TestBetaReadsTheBetaManifestThenStable(t *testing.T) {
	if got := manifestURLs("stable"); len(got) != 1 || got[0] != stableManifestURL {
		t.Errorf("stable: %v", got)
	}
	for _, channel := range []string{"", "nightly"} {
		if got := manifestURLs(channel); len(got) != 1 || got[0] != stableManifestURL {
			t.Errorf("%q is not stable: %v", channel, got)
		}
	}
	if got := manifestURLs("beta"); len(got) != 2 || got[0] != betaManifestURL || got[1] != stableManifestURL {
		t.Errorf("beta: %v", got)
	}
}

func TestNoChannelNamesTheOldAppsManifest(t *testing.T) {
	for _, channel := range []string{"stable", "beta"} {
		for _, url := range manifestURLs(channel) {
			if name := path.Base(url); name == "latest.json" || !strings.HasPrefix(name, "textus-") {
				t.Errorf("%s reads %s", channel, url)
			}
		}
	}
}

func TestVersionsCompareAsSemverDoes(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want int
	}{
		{"1.0.5", "1.0.5-rc.2", 1},
		{"1.0.5-rc.2", "1.0.5-rc.1", 1},
		{"1.0.5-rc.1", "1.0.4", 1},
		{"1.3.0-beta.10", "1.3.0-beta.2", 1},
		{"1.3.0-beta.2", "1.3.0-beta.2", 0},
		{"v1.2.3", "1.2.3+build.5", 0},
		{"1.3.0-alpha", "1.3.0-1", 1},
		{"1.10.0", "1.9.9", 1},
		{"not-a-version", "0.0.1", -1},
	} {
		if got := compareVersions(c.a, c.b); got != c.want {
			t.Errorf("compare(%s, %s) = %d, want %d", c.a, c.b, got, c.want)
		}
		if got := compareVersions(c.b, c.a); got != -c.want {
			t.Errorf("compare(%s, %s) = %d, want %d", c.b, c.a, got, -c.want)
		}
	}
}

func TestOnlyNewerVersionsAreOfferedButOnTheWayBack(t *testing.T) {
	for _, c := range []struct {
		current, remote string
		back, want      bool
	}{
		{"1.0.5-rc.2", "1.0.5", false, true},
		{"1.0.4", "1.0.5-rc.1", false, true},
		{"1.0.5-rc.2", "1.0.4", false, false},
		{"1.0.5", "1.0.5", false, false},
		// A beta goes back to an older stable only when asked.
		{"1.3.0-beta.1", "1.2.1", false, false},
		{"1.3.0-beta.1", "1.2.1", true, true},
		{"1.3.0-beta.1", "1.3.0-beta.2", true, false},
		{"1.2.1", "1.2.0", true, false},
	} {
		if got := wanted(c.current, c.remote, c.back); got != c.want {
			t.Errorf("wanted(%s, %s, %v) = %v", c.current, c.remote, c.back, got)
		}
	}
}

// manifestServer serves the manifests named by path, 404 for the rest.
func manifestServer(t *testing.T, manifests map[string]string) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := manifests[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestACheckFindsThisBuildsEntry(t *testing.T) {
	key := updateKeys("linux", "amd64")[0]
	manifest := `{"version":"1.5.0","notes":"Notes","pub_date":"2026-11-01T00:00:00Z","platforms":{
		"linux-x86_64":{"url":"https://example.com/Textus.AppImage","signature":"tauri"},
		"` + key + `":{"url":"https://example.com/textus-1.5.0-linux-amd64.tar.gz","size":42,"signature":"c2ln"}}}`
	srv := manifestServer(t, map[string]string{"/textus-latest.json": manifest})
	found, err := checkForUpdate(context.Background(), "1.4.7", []string{srv.URL + "/textus-latest.json"}, false)
	if err != nil || found == nil {
		t.Fatalf("%v, %v", found, err)
	}
	if found.Version != "1.5.0" || found.CurrentVersion != "1.4.7" || *found.Body != "Notes" || *found.Date != "2026-11-01T00:00:00Z" {
		t.Errorf("%+v", found)
	}
	entry := updates.found[found.Rid].entry
	if entry.Size != 42 || entry.URL != "https://example.com/textus-1.5.0-linux-amd64.tar.gz" {
		t.Errorf("entry %+v", entry)
	}
	// MyGo's manifest, made from it.
	body, _ := updateSource{updates.found[found.Rid]}.Manifest(context.Background(), false)
	var m map[string]any
	if err := json.UnmarshalRead(body, &m); err != nil || m["version"] != "1.5.0" || m["size"] != float64(42) || m["signature"] != "c2ln" {
		t.Errorf("manifest %v, %v", m, err)
	}
	// Up to date: nothing.
	if found, err := checkForUpdate(context.Background(), "1.5.0", []string{srv.URL + "/textus-latest.json"}, false); found != nil || err != nil {
		t.Errorf("%v, %v", found, err)
	}
}

func TestAManifestWithoutThisBuildOffersNothing(t *testing.T) {
	srv := manifestServer(t, map[string]string{"/m.json": `{"version":"9.0.0","platforms":{"linux-x86_64":{"url":"https://x","signature":"s"}}}`})
	if found, err := checkForUpdate(context.Background(), "1.0.0", []string{srv.URL + "/m.json"}, false); found != nil || err != nil {
		t.Errorf("%v, %v", found, err)
	}
}

func TestTheFirstManifestThatAnswersDecides(t *testing.T) {
	key := updateKeys(runtime.GOOS, runtime.GOARCH)[0]
	stable := `{"version":"1.2.0","platforms":{"` + key + `":{"url":"https://x/a","size":1,"signature":"s"}}}`
	srv := manifestServer(t, map[string]string{"/stable.json": stable})
	// The beta manifest is missing: stable answers.
	found, err := checkForUpdate(context.Background(), "1.1.0", []string{srv.URL + "/beta.json", srv.URL + "/stable.json"}, false)
	if err != nil || found == nil || found.Version != "1.2.0" {
		t.Fatalf("%+v, %v", found, err)
	}
	// Every manifest failing is an error.
	if _, err := checkForUpdate(context.Background(), "1.1.0", []string{srv.URL + "/none.json"}, false); err == nil {
		t.Error("no error for a missing manifest")
	}
}
