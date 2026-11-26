package backend

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo"
)

// The in-app update check on the chosen channel. Textus publishes
// textus-latest.json, never latest.json: Levis installs read that name
// from this repository and trust the same key. Beta reads textus-beta.json
// (GitHub's releases/latest skips pre-releases), then stable. Only newer
// versions are offered, except on an explicit back-to-stable.
//
// Both manifests are atoz03/Textus's, whose releases are signed with the
// key in mygo.json (updates.publicKey): CatVinci-Studio/Textus's are
// signed with another, which this build would refuse. No workflow writes
// this repository's textus-beta.json yet (update-beta-manifest.yml writes
// CatVinci-Studio's download bucket), so beta finds none there and reads
// stable.
//
// A Go build's entry in the manifest is MyGo's update of its target,
// "mygo-<GOOS>-<GOARCH>": the archive's URL, size and Ed25519 signature
// (and its deltas), as `mygo build` writes them in update-<target>.json.
// The Tauri builds' installers keep their keys (darwin-aarch64, ...).
// MyGo's updater checks the signature against the key this build was made
// with and replaces the app.

const (
	stableManifestURL = "https://github.com/atoz03/Textus/releases/latest/download/textus-latest.json"
	betaManifestURL   = "https://github.com/atoz03/Textus/releases/latest/download/textus-beta.json"
	maxManifestBytes  = 1 << 20
)

// manifestURLs are the manifests to ask, in order, for channel ("stable"
// or "beta"; anything else is stable).
func manifestURLs(channel string) []string {
	if channel == "beta" {
		return []string{betaManifestURL, stableManifestURL}
	}
	return []string{stableManifestURL}
}

// updateManifest is textus-latest.json (scripts/updater-manifest.mjs
// writes it).
type updateManifest struct {
	Version   string                    `json:"version"`
	Notes     string                    `json:"notes"`
	PubDate   string                    `json:"pub_date"`
	Platforms map[string]jsontext.Value `json:"platforms"`
}

// mygoUpdate is a Go build's entry: MyGo's manifest of its target, less
// the version, notes and date the whole manifest gives.
type mygoUpdate struct {
	URL       string           `json:"url"`
	Size      int64            `json:"size"`
	Signature string           `json:"signature"`
	Deltas    []jsontext.Value `json:"deltas,omitempty"`
}

// updateKeys are the manifest keys this build installs from, in order.
func updateKeys(goos, goarch string) []string {
	keys := []string{"mygo-" + goos + "-" + goarch}
	if goos == "darwin" {
		keys = append(keys, "mygo-darwin-universal")
	}
	return keys
}

// entryFor is this build's entry in m, if the manifest has one.
func (m updateManifest) entryFor(goos, goarch string) (mygoUpdate, bool) {
	for _, key := range updateKeys(goos, goarch) {
		raw, ok := m.Platforms[key]
		if !ok {
			continue
		}
		var u mygoUpdate
		if json.Unmarshal(raw, &u) == nil && u.URL != "" && u.Signature != "" && u.Size > 0 {
			return u, true
		}
	}
	return mygoUpdate{}, false
}

// wanted reports whether remote is offered to an install of current: a
// newer version, or with backToStable a release in place of a pre-release.
func wanted(current, remote string, backToStable bool) bool {
	if backToStable {
		return isPrerelease(current) && !isPrerelease(remote)
	}
	return compareVersions(remote, current) > 0
}

// UpdateMetadata is the check's answer (src/platform/bindings/app.ts
// UpdateMetadata_Serialize), from which the page builds its Update.
type UpdateMetadata struct {
	Rid            int            `json:"rid"`
	CurrentVersion string         `json:"currentVersion"`
	Version        string         `json:"version"`
	Date           *string        `json:"date"`
	Body           *string        `json:"body"`
	RawJSON        jsontext.Value `json:"rawJson"`
}

// foundUpdate is an update a check offered, kept for its install by rid.
type foundUpdate struct {
	manifest updateManifest
	entry    mygoUpdate
	older    bool // back to stable: older than the running version
}

var updates struct {
	sync.Mutex
	next  int
	found map[int]foundUpdate
	// installing: MyGo's updater has one source at a time.
	installing sync.Mutex
}

// relaunchAfterQuit: an update is installed, and the app starts again
// once every window has closed.
var relaunchAfterQuit atomic.Bool

func fetchManifest(ctx context.Context, url string) (updateManifest, jsontext.Value, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return updateManifest{}, nil, err
	}
	resp, err := netClient(30 * time.Second).Do(req)
	if err != nil {
		return updateManifest{}, nil, errors.New(netErrorText(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return updateManifest{}, nil, fmt.Errorf("the update server answered %s", resp.Status)
	}
	body, err := readLimited(resp, maxManifestBytes)
	if err != nil {
		return updateManifest{}, nil, err
	}
	var m updateManifest
	if err := json.Unmarshal(body, &m); err != nil || m.Version == "" {
		return updateManifest{}, nil, errors.New("the update manifest is not one")
	}
	return m, jsontext.Value(body), nil
}

// checkForUpdate asks the manifests at urls in order; the first that
// answers decides, as the Tauri updater did.
func checkForUpdate(ctx context.Context, current string, urls []string, backToStable bool) (*UpdateMetadata, error) {
	var lastErr error
	for _, url := range urls {
		m, raw, err := fetchManifest(ctx, url)
		if err != nil {
			lastErr = err
			continue
		}
		entry, ok := m.entryFor(runtime.GOOS, runtime.GOARCH)
		if !ok || !wanted(current, m.Version, backToStable) {
			return nil, nil
		}
		updates.Lock()
		updates.next++
		rid := updates.next
		if updates.found == nil {
			updates.found = map[int]foundUpdate{}
		}
		updates.found[rid] = foundUpdate{manifest: m, entry: entry, older: compareVersions(m.Version, current) <= 0}
		updates.Unlock()
		meta := &UpdateMetadata{Rid: rid, CurrentVersion: current, Version: m.Version, RawJSON: raw}
		if m.PubDate != "" {
			meta.Date = &m.PubDate
		}
		if m.Notes != "" {
			meta.Body = &m.Notes
		}
		return meta, nil
	}
	return nil, lastErr
}

// updateSource serves one update to MyGo's updater: the manifest of this
// build's target, made from textus-latest.json's entry, and its files.
type updateSource struct{ found foundUpdate }

func (s updateSource) Manifest(context.Context, bool) (io.ReadCloser, error) {
	m := s.found.manifest
	manifest := map[string]any{
		"version": m.Version, "notes": m.Notes, "date": m.PubDate,
		"url": s.found.entry.URL, "size": s.found.entry.Size, "signature": s.found.entry.Signature,
	}
	if len(s.found.entry.Deltas) > 0 {
		manifest["deltas"] = s.found.entry.Deltas
	}
	raw, err := json.Marshal(manifest)
	if err != nil {
		return nil, err
	}
	return io.NopCloser(bytes.NewReader(raw)), nil
}

func (s updateSource) Open(ctx context.Context, url string) (io.ReadCloser, error) {
	if !strings.HasPrefix(url, "https://") {
		return nil, fmt.Errorf("refusing to download %s: updates need HTTPS", url)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	// GitHub's asset store refuses a User-Agent it can't read.
	req.Header.Set("User-Agent", "textus-updater")
	resp, err := netClient(0).Do(req)
	if err != nil {
		return nil, errors.New(netErrorText(err))
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("the download answered %s", resp.Status)
	}
	return resp.Body, nil
}

// installUpdate downloads rid's update, checks its signature and replaces
// the app with it; the new version runs after the relaunch.
func installUpdate(ctx context.Context, rid int) error {
	updates.Lock()
	found, ok := updates.found[rid]
	updates.Unlock()
	if !ok {
		return errors.New("that update is no longer known: check for updates again")
	}
	if found.older {
		return fmt.Errorf("Textus can't install an older version over this one itself: download Textus %s from https://textus.md and install it", found.manifest.Version)
	}
	if !mygo.Updater.Enabled() {
		return errors.New("this copy of Textus can't update itself (it isn't a signed release, or it can't write where it is installed): download the new version from https://textus.md")
	}
	updates.installing.Lock()
	defer updates.installing.Unlock()
	mygo.Updater.SetSource(updateSource{found})
	defer mygo.Updater.SetSource(nil)
	up, err := mygo.Updater.Check(ctx)
	if err != nil {
		return err
	}
	if up == nil {
		return errors.New("the update is no newer than this version")
	}
	return up.Install(ctx, nil)
}

// isPrerelease reports whether v has a pre-release part (1.3.0-beta.1).
func isPrerelease(v string) bool {
	core, _, _ := strings.Cut(strings.TrimPrefix(v, "v"), "+")
	return strings.Contains(core, "-")
}

// compareVersions orders two semantic versions as semver does, build
// metadata ignored: 1.3.0-beta.2 < 1.3.0-beta.10 < 1.3.0. A version that
// doesn't parse is older than any that does.
func compareVersions(a, b string) int {
	pa, oka := parseVersion(a)
	pb, okb := parseVersion(b)
	switch {
	case !oka && !okb:
		return 0
	case !oka:
		return -1
	case !okb:
		return 1
	}
	for i := range 3 {
		if c := cmp.Compare(pa.core[i], pb.core[i]); c != 0 {
			return c
		}
	}
	switch {
	case len(pa.pre) == 0 && len(pb.pre) == 0:
		return 0
	case len(pa.pre) == 0:
		return 1
	case len(pb.pre) == 0:
		return -1
	}
	for i := 0; i < max(len(pa.pre), len(pb.pre)); i++ {
		if i >= len(pa.pre) {
			return -1
		}
		if i >= len(pb.pre) {
			return 1
		}
		if c := comparePart(pa.pre[i], pb.pre[i]); c != 0 {
			return c
		}
	}
	return 0
}

type version struct {
	core [3]int
	pre  []string
}

func parseVersion(v string) (version, bool) {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	v, _, _ = strings.Cut(v, "+")
	core, pre, hasPre := strings.Cut(v, "-")
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return version{}, false
	}
	var out version
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return version{}, false
		}
		out.core[i] = n
	}
	if hasPre {
		if pre == "" {
			return version{}, false
		}
		out.pre = strings.Split(pre, ".")
	}
	return out, true
}

// comparePart orders two pre-release identifiers: numbers by value, below
// any word; words as text.
func comparePart(a, b string) int {
	na, errA := strconv.Atoi(a)
	nb, errB := strconv.Atoi(b)
	switch {
	case errA == nil && errB == nil:
		return cmp.Compare(na, nb)
	case errA == nil:
		return -1
	case errB == nil:
		return 1
	}
	return strings.Compare(a, b)
}

func init() {
	command("check_for_update", func(c *Call, a struct {
		Channel      string `json:"channel"`
		BackToStable bool   `json:"backToStable"`
	}) (*UpdateMetadata, error) {
		channel := a.Channel
		if a.BackToStable {
			// Going back reads the stable manifest only.
			channel = "stable"
		}
		return checkForUpdate(c.ctx, appVersion, manifestURLs(channel), a.BackToStable)
	})
	command("download_and_install_update", func(c *Call, a struct {
		Rid int `json:"rid"`
	}) (nothing, error) {
		return nil, installUpdate(c.ctx, a.Rid)
	})
	// Windows' installer once had to run after the quit; MyGo's updater
	// moves the running program aside, so the update installs at once and
	// the windows close as at Quit before the new version starts.
	command("install_after_quit", func(c *Call, a struct {
		Rid int `json:"rid"`
	}) (nothing, error) {
		if err := installUpdate(c.ctx, a.Rid); err != nil {
			return nil, err
		}
		relaunchAfterQuit.Store(true)
		c.app.quit()
		return nil, nil
	})
}
