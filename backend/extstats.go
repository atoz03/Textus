package backend

import (
	"encoding/json/v2"
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
)

// Download counts for plugins, themes and skills (services/ext-stats): a
// download goes through the store's counting address, which redirects to
// the package. It says which extension and version, and whether it is a
// first install, a reinstall or an update - nothing about the device. When
// the address fails, the package is downloaded straight from its own.

const (
	extStatsEndpoint = "https://textus.md/api/ext/"
	// extRecordFile lists the extensions ever installed on this device, so
	// a second install counts as a reinstall. Kept in app data, never synced.
	extRecordFile = "ext-installed.json"
	// maxRedirects is how many redirects a download follows.
	maxRedirects = 5
)

// extDownload is one download to count.
type extDownload struct {
	kind    string // "plugin", "theme" or "skill"
	id      string
	version string
	update  bool   // it replaces an older version installed now
	present bool   // a copy is installed now (this version, or an older one)
	direct  string // the package's own address, where the endpoint redirects
}

// statsEndpoint is where downloads are counted: TEXTUS_EXT_STATS for trying
// a local one; a development build counts only there, so development never
// adds to the real counts.
func statsEndpoint() string {
	if base, ok := os.LookupEnv("TEXTUS_EXT_STATS"); ok {
		return base
	}
	if devBuild {
		return ""
	}
	return extStatsEndpoint
}

func extEvent(update, before bool) string {
	switch {
	case update:
		return "update"
	case before:
		return "reinstall"
	}
	return "install"
}

// countedDownloadURL is the endpoint's address for d, "" for a base that
// isn't a URL.
func countedDownloadURL(base string, d extDownload, event string) string {
	u, err := url.Parse(base)
	if err != nil || u.Host == "" {
		return ""
	}
	u = u.ResolveReference(&url.URL{Path: "download"})
	query := []string{"kind", d.kind, "id", d.id, "version", d.version, "event", event,
		// Redirect to Textus's own site, not GitHub (older versions leave
		// this out).
		"via", "site"}
	// A theme's stylesheet may be named otherwise.
	if d.kind == "theme" {
		query = append(query, "file", d.direct[strings.LastIndex(d.direct, "/")+1:])
	}
	var q strings.Builder
	for i := 0; i < len(query); i += 2 {
		if i > 0 {
			q.WriteByte('&')
		}
		q.WriteString(url.QueryEscape(query[i]) + "=" + url.QueryEscape(query[i+1]))
	}
	u.RawQuery = q.String()
	return u.String()
}

// countedURL is where to download d from to count it; "" for a package
// that isn't on the web, or when nothing counts.
func (a *App) countedURL(d extDownload) string {
	base := statsEndpoint()
	if !strings.HasPrefix(d.direct, "https://") || base == "" {
		return ""
	}
	before := d.present || a.installedBefore(d.kind, d.id)
	return countedDownloadURL(base, d, extEvent(d.update, before))
}

// atStatsEndpoint is whether u is the endpoint's own address.
func atStatsEndpoint(u *url.URL, base string) bool {
	return base != "" && strings.HasPrefix(u.String(), base)
}

// checkRedirected fails for an answer from the endpoint itself rather than
// the package it redirects to (a page where no endpoint is deployed, say).
func checkRedirected(resp *http.Response) error {
	if atStatsEndpoint(resp.Request.URL, statsEndpoint()) {
		return errors.New("the install counter didn't redirect")
	}
	return nil
}

// countedOrDirect is get from counted, or from direct when there is no
// counted address or it failed: counting never stops an install.
func countedOrDirect[T any](counted, direct string, get func(from string) (T, error)) (T, error) {
	if counted != "" {
		if found, err := get(counted); err == nil {
			return found, nil
		}
	}
	return get(direct)
}

// mayFollow is whether a redirect to next is followed: from the endpoint
// only to direct, the package asked for; anywhere else as then allows.
func mayFollow(via []*http.Request, next *url.URL, direct, base string, then func(*url.URL) bool) bool {
	if len(via) >= maxRedirects {
		return false
	}
	if len(via) > 0 && atStatsEndpoint(via[len(via)-1].URL, base) {
		return next.String() == direct
	}
	return then(next)
}

// followRedirects is a CheckRedirect for downloading direct, perhaps
// through the endpoint. A redirect it doesn't follow is answered as it
// came, which the caller's status check refuses.
func followRedirects(direct string, then func(*url.URL) bool) func(*http.Request, []*http.Request) error {
	base := statsEndpoint()
	return func(req *http.Request, via []*http.Request) error {
		if mayFollow(via, req.URL, direct, base, then) {
			return nil
		}
		return http.ErrUseLastResponse
	}
}

// extRecordMu makes each note one step.
var extRecordMu sync.Mutex

func (a *App) extRecordPath() string { return filepath.Join(a.dirs.Data, extRecordFile) }

// installedBefore is whether this device ever installed id.
func (a *App) installedBefore(kind, id string) bool {
	record, _ := readJSONStore[map[string][]string](a.extRecordPath())
	return slices.Contains(record[kind], id)
}

// noteInstalled notes id as installed on this device; it never leaves the
// device. Only a hint: a record that can't be written makes a later install
// count as a first one.
func (a *App) noteInstalled(kind, id string) {
	extRecordMu.Lock()
	defer extRecordMu.Unlock()
	record, err := readJSONStore[map[string][]string](a.extRecordPath())
	if err != nil || slices.Contains(record[kind], id) {
		return
	}
	if record == nil {
		record = map[string][]string{}
	}
	record[kind] = append(record[kind], id)
	slices.Sort(record[kind])
	if raw, err := json.Marshal(record); err == nil {
		writeFileAtomic(a.extRecordPath(), raw)
	}
}
