package backend

import (
	"context"
	"encoding/base64"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"textus/backend/ai"
	"textus/backend/plugins"
)

// The plugins and skills Textus offers to install, from the
// textus-plugins catalog: only the backend fetches the catalog and
// packages, from fixed addresses, and checks each package's SHA-256.

const (
	// maxCatalogIndexBytes is the most the catalog's index may be.
	maxCatalogIndexBytes = 8 * 1024 * 1024
	// maxReadmeBytes: a README this large is not a README.
	maxReadmeBytes = 512 * 1024
	// maxIconBytes: an icon larger than this is no icon.
	maxIconBytes = 256 * 1024
	// catalogTimeout bounds one request to the release host.
	catalogTimeout = 120 * time.Second
	// catalogCacheFile keeps the catalog as last fetched, in the cache
	// folder, so Get plugins lists it at once, even offline.
	catalogCacheFile = "plugin-catalog.json"
	// movedSkillsKey is the setting naming the moved skills seen to; all
	// of them from the start in a new profile.
	movedSkillsKey = "skillsMovedToMarket"
	// disabledBuiltinsKey is the setting listing the turned-off built-in
	// skills of an older profile.
	disabledBuiltinsKey = "disabledBuiltinSkills"
	// skillExists is the error code of a skill whose name is taken.
	skillExists = "skillExists"
)

// catalogURL is the catalog's address: the published one, or
// TEXTUS_PLUGIN_CATALOG for trying one before it is published.
func catalogURL() string {
	if u := os.Getenv("TEXTUS_PLUGIN_CATALOG"); u != "" {
		return u
	}
	return plugins.CatalogURL
}

func (a *App) catalogCachePath() string { return filepath.Join(a.dirs.Cache, catalogCacheFile) }

// proxyDisplay is where a request to target is pointed, for an error: on
// an intranet the difference between "the proxy's address failed" and
// "direct, but the site's name doesn't resolve" is the whole diagnosis.
func proxyDisplay(target string) string {
	route := "system"
	if r := currentRoute.Load(); r != nil && r.direct {
		route = "direct"
	} else if r != nil && r.via != nil {
		route = "manual"
	}
	shown := "none (direct)"
	if u := proxyURL(target); u != nil {
		hidden := *u
		if hidden.User != nil {
			hidden.User = url.UserPassword("***", "***")
		}
		shown = hidden.String()
	}
	return route + ": " + shown
}

// twice tries again once: a slow or dropped connection often holds on a
// second try.
func twice[T any](attempt func() (T, error)) (T, error) {
	if v, err := attempt(); err == nil {
		return v, nil
	}
	return attempt()
}

// releaseClient is a client for the release host, following redirects as
// follow allows.
func releaseClient(follow func(*http.Request, []*http.Request) error) *http.Client {
	client := netClient(catalogTimeout)
	client.CheckRedirect = follow
	return client
}

// toReleaseHost follows a redirect only to the site, at most five.
func toReleaseHost(req *http.Request, via []*http.Request) error {
	if plugins.DownloadHost(req.URL) && len(via) < maxRedirects {
		return nil
	}
	return http.ErrUseLastResponse
}

// fetchCatalog is the catalog's text, from its address.
func fetchCatalog(ctx context.Context) (string, error) {
	target := catalogURL()
	if path, ok := strings.CutPrefix(target, "file://"); ok {
		raw, err := os.ReadFile(path)
		return string(raw), err
	}
	client := releaseClient(toReleaseHost)
	return twice(func() (string, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
		if err != nil {
			return "", err
		}
		resp, err := client.Do(req)
		if err != nil {
			return "", fmt.Errorf("Couldn't reach the plugin catalog: %s (proxy: %s)", netErrorText(err), proxyDisplay(target))
		}
		defer resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode > 299 {
			return "", fmt.Errorf("The plugin catalog answered %s.", resp.Status)
		}
		body, err := readLimited(resp, maxCatalogIndexBytes)
		switch {
		case errors.Is(err, errTooLarge):
			return "", errors.New("The plugin catalog is too large to read.")
		case err != nil:
			return "", fmt.Errorf("The plugin catalog broke off: %v", err)
		case !utf8.Valid(body):
			return "", errors.New("invalid utf-8 sequence")
		}
		return string(body), nil
	})
}

// downloadWith downloads target with client: a package, a README, an
// icon, named what in errors.
func downloadWith(ctx context.Context, client *http.Client, target, what string) ([]byte, error) {
	if path, ok := strings.CutPrefix(target, "file://"); ok {
		return os.ReadFile(path)
	}
	return twice(func() ([]byte, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
		if err != nil {
			return nil, err
		}
		resp, err := client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("Couldn't download %s: %s", what, netErrorText(err))
		}
		defer resp.Body.Close()
		if err := checkRedirected(resp); err != nil {
			return nil, err
		}
		if resp.StatusCode < 200 || resp.StatusCode > 299 {
			return nil, fmt.Errorf("The download of %s answered %s.", what, resp.Status)
		}
		data, err := readLimited(resp, plugins.MaxPackageBytes)
		switch {
		case errors.Is(err, errTooLarge):
			return nil, fmt.Errorf("The download of %s is too large.", what)
		case err != nil:
			return nil, fmt.Errorf("The download of %s broke off: %v", what, err)
		}
		return data, nil
	})
}

// downloadCounted downloads a package, through the counting endpoint when
// counts are on.
func (a *App) downloadCounted(ctx context.Context, d extDownload, what string) ([]byte, error) {
	client := releaseClient(followRedirects(d.direct, plugins.DownloadHost))
	return countedOrDirect(a.countedURL(d), d.direct, func(from string) ([]byte, error) {
		return downloadWith(ctx, client, from, what)
	})
}

// keepCatalog keeps what a catalog's text lists: its plugins for an
// install to take an entry from (only when none are kept yet, unless
// fresh), and its skills.
func (a *App) keepCatalog(text string, fresh bool) ([]plugins.CatalogEntry, error) {
	entries, skills, err := plugins.ParseCatalog(text, appVersion)
	if err != nil {
		return nil, err
	}
	a.plug.mu.Lock()
	defer a.plug.mu.Unlock()
	a.plug.fetchedSkills = skills
	if fresh || len(a.plug.fetched) == 0 {
		a.plug.fetched = entries
	}
	return entries, nil
}

// fetchPluginCatalog is the catalog, fetched now and kept for next time.
func (a *App) fetchPluginCatalog(ctx context.Context) ([]plugins.CatalogEntry, error) {
	text, err := fetchCatalog(ctx)
	if err != nil {
		return nil, err
	}
	entries, err := a.keepCatalog(text, true)
	if err != nil {
		return nil, err
	}
	// Only a cache: a catalog that can't be kept is fetched again next time.
	_ = writeFileAtomic(a.catalogCachePath(), []byte(text))
	return entries, nil
}

// catalogEntry is plugin id in the catalog last fetched.
func (a *App) catalogEntry(id string) (plugins.CatalogEntry, bool) {
	a.plug.mu.Lock()
	defer a.plug.mu.Unlock()
	at := slices.IndexFunc(a.plug.fetched, func(e plugins.CatalogEntry) bool { return e.ID == id })
	if at < 0 {
		return plugins.CatalogEntry{}, false
	}
	return a.plug.fetched[at], true
}

// installFromCatalog installs id from the catalog last fetched after
// checking its package, marked Official when the catalog says it is
// CatVinci's.
func (a *App) installFromCatalog(ctx context.Context, id string) (plugins.Installed, error) {
	if err := plugins.RequireID(id); err != nil {
		return plugins.Installed{}, err
	}
	entry, ok := a.catalogEntry(id)
	if !ok {
		return plugins.Installed{}, fmt.Errorf("%s isn't in the plugin catalog.", id)
	}
	if entry.Size > plugins.MaxPackageBytes {
		return plugins.Installed{}, fmt.Errorf("%s's package is too large to install.", id)
	}
	root := a.pluginsRoot()
	present, has := plugins.InstalledVersion(root, id)
	data, err := a.downloadCounted(ctx, extDownload{kind: "plugin", id: id, version: entry.Version,
		update: has && plugins.IsNewer(present, entry.Version), present: has, direct: entry.URL}, id)
	if err != nil {
		return plugins.Installed{}, err
	}
	if err := plugins.Checked(entry, data); err != nil {
		return plugins.Installed{}, err
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return plugins.Installed{}, err
	}
	pkg := filepath.Join(root, ".download-"+id+".zip")
	if err := os.WriteFile(pkg, data, 0o644); err != nil {
		return plugins.Installed{}, err
	}
	installed, err := a.installPlugin(pkg, fromCatalog)
	_ = os.Remove(pkg)
	if err != nil {
		return plugins.Installed{}, err
	}
	if installed.ID != id {
		return plugins.Installed{}, fmt.Errorf("The package for %s holds %s instead.", id, installed.ID)
	}
	if err := a.updatePlugins(func(s *plugins.State) { s.Entry(id).Official = entry.Official }); err != nil {
		return plugins.Installed{}, err
	}
	a.noteInstalled("plugin", id)
	return installed, nil
}

// installedSkillVersion is the version of the catalog skill name
// installed in dir, when one of that name is there with a version (one
// the user wrote has none).
func installedSkillVersion(dir, name string) (plugins.Version, bool) {
	for _, f := range readSkillFiles(dir) {
		if strings.EqualFold(f.skill.Name, name) {
			if f.skill.Version == nil {
				return plugins.Version{}, false
			}
			return plugins.ParseVersion(strings.TrimSpace(*f.skill.Version))
		}
	}
	return plugins.Version{}, false
}

// installSkillIn writes a skill file named name into dir: never over one
// there, but over an older version of the same catalog skill (an update).
func installSkillIn(dir, name, text string) (string, error) {
	skill := ai.ParseSkillFile(name, text)
	if skill.Prompt == "" {
		return "", fmt.Errorf("%s isn't a skill file.", name)
	}
	files := readSkillFiles(dir)
	taken := slices.IndexFunc(files, func(f skillFile) bool { return strings.EqualFold(f.skill.Name, skill.Name) })
	if taken < 0 {
		return writeSkillFile(dir, name, takenRefuse(skillExists), func(string) string { return text })
	}
	path := files[taken].path
	have, ok := installedSkillVersion(dir, skill.Name)
	if !ok || skill.Version == nil || !plugins.IsNewer(have.String(), *skill.Version) {
		return "", codeError(skillExists)
	}
	return path, writeAtomic(path, []byte(text))
}

// installSkillFromCatalog installs skill name from the catalog last
// fetched into the global skills/ folder, after checking it, and answers
// its path.
func (a *App) installSkillFromCatalog(ctx context.Context, name string) (string, error) {
	a.plug.mu.Lock()
	at := slices.IndexFunc(a.plug.fetchedSkills, func(s plugins.SkillEntry) bool { return s.Name == name })
	var entry plugins.SkillEntry
	if at >= 0 {
		entry = a.plug.fetchedSkills[at]
	}
	a.plug.mu.Unlock()
	if at < 0 {
		return "", fmt.Errorf("%s isn't in the catalog.", name)
	}
	skills := filepath.Join(a.globalAgentPath(), "skills")
	have, present := installedSkillVersion(skills, entry.Name)
	data, err := a.downloadCounted(ctx, extDownload{kind: "skill", id: entry.Name, version: entry.Version,
		update: present && plugins.IsNewer(have.String(), entry.Version), present: present, direct: entry.URL}, name)
	if err != nil {
		return "", err
	}
	if uint64(len(data)) != entry.Size || !strings.EqualFold(plugins.HexSHA256(data), entry.Sha256) {
		return "", fmt.Errorf("The download of %s isn't the file the catalog lists - it was not installed.", name)
	}
	if !utf8.Valid(data) {
		return "", fmt.Errorf("%s isn't a skill file.", name)
	}
	path, err := installSkillIn(skills, entry.Name, string(data))
	if err != nil {
		return "", err
	}
	a.noteInstalled("skill", entry.Name)
	return path, nil
}

// settingValue is a setting as Go values (bool, []any...), nil when unset.
func (a *App) settingValue(key string) any {
	var v any
	if raw := a.readSetting(key); len(raw) > 0 {
		_ = json.Unmarshal(raw, &v)
	}
	return v
}

// installMovedSkills installs the skills that moved from built-in to the
// market which an older profile had on and lacks, then marks the move
// done. A failure (offline) leaves it for the next start.
func (a *App) installMovedSkills(ctx context.Context) {
	seen := plugins.MovedSkillsSeenTo(ai.MovedToMarket, a.settingValue(movedSkillsKey))
	if len(seen) == len(ai.MovedToMarket) {
		return
	}
	var have []string
	for _, f := range readSkillFiles(filepath.Join(a.globalAgentPath(), "skills")) {
		have = append(have, f.skill.Name)
	}
	var todo []string
	for _, name := range plugins.MovedSkillsHadOn(ai.MovedToMarket, a.settingValue(disabledBuiltinsKey)) {
		if !slices.Contains(seen, name) && !slices.Contains(have, name) {
			todo = append(todo, name)
		}
	}
	if len(todo) > 0 {
		if _, err := a.fetchPluginCatalog(ctx); err != nil {
			log.Printf("textus: the moved skills wait for the catalog: %v", err)
			return
		}
		for _, name := range todo {
			if _, err := a.installSkillFromCatalog(ctx, name); err != nil && !isCode(err, skillExists) {
				log.Printf("textus: the moved skill %s waits: %v", name, err)
				return
			}
		}
	}
	a.patchSettingsFromBackend(jsonObject{movedSkillsKey: mustJSON(ai.MovedToMarket), disabledBuiltinsKey: jsontext.Value("null")})
}

func init() {
	// The catalog, fetched now and kept for next time.
	command("plugin_catalog", func(c *Call, _ none) ([]plugins.CatalogEntry, error) {
		return c.app.fetchPluginCatalog(c.ctx)
	})
	// The catalog as last fetched, shown while a fresh one loads.
	// Installing from it is safe: a version's package never changes, so
	// its checksum holds.
	command("plugin_catalog_cached", func(c *Call, _ none) (*[]plugins.CatalogEntry, error) {
		raw, err := os.ReadFile(c.app.catalogCachePath())
		if err != nil {
			return nil, nil
		}
		entries, err := c.app.keepCatalog(string(raw), false)
		if err != nil {
			return nil, nil
		}
		return &entries, nil
	})
	// Plugin id's README in the catalog last fetched, in language where it
	// has one, else in English; nil when it has none.
	command("plugin_catalog_readme", func(c *Call, a struct {
		ID       string `json:"id"`
		Language string `json:"language"`
	}) (*string, error) {
		entry, _ := c.app.catalogEntry(a.ID)
		target, ok := plugins.ReadmeFor(entry.Readme, a.Language)
		if !ok {
			return nil, nil
		}
		data, err := downloadWith(c.ctx, releaseClient(toReleaseHost), target, a.ID)
		if err != nil {
			return nil, err
		}
		if len(data) > maxReadmeBytes {
			return nil, fmt.Errorf("%s's README is too large to show.", a.ID)
		}
		text := strings.ToValidUTF8(string(data), "�")
		return &text, nil
	})
	// Plugin id's icon in the catalog last fetched, as a data: URL; nil
	// when it has none. Fetched here: GitHub serves release files as
	// application/octet-stream, which an <img> won't draw as an SVG.
	command("plugin_catalog_icon", func(c *Call, a struct {
		ID string `json:"id"`
	}) (*string, error) {
		entry, _ := c.app.catalogEntry(a.ID)
		if entry.Icon == nil {
			return nil, nil
		}
		mime := "image/png"
		if strings.HasSuffix(*entry.Icon, ".svg") {
			mime = "image/svg+xml"
		}
		data, err := downloadWith(c.ctx, releaseClient(toReleaseHost), *entry.Icon, a.ID)
		if err != nil {
			return nil, err
		}
		if len(data) > maxIconBytes {
			return nil, nil
		}
		icon := "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data)
		return &icon, nil
	})
	command("plugin_install_catalog", func(c *Call, a struct {
		ID string `json:"id"`
	}) (plugins.Installed, error) {
		return c.app.installFromCatalog(c.ctx, a.ID)
	})
	// The skills the catalog last fetched (or kept) lists.
	command("skill_catalog", func(c *Call, _ none) ([]plugins.SkillEntry, error) {
		c.app.plug.mu.Lock()
		defer c.app.plug.mu.Unlock()
		return slices.Clone(c.app.plug.fetchedSkills), nil
	})
	// Installs skill name from the catalog into the global skills/ folder,
	// never over one of the same name but an older version of it, and
	// answers its path.
	command("skill_install_catalog", func(c *Call, a struct {
		Name string `json:"name"`
	}) (string, error) {
		return c.app.installSkillFromCatalog(c.ctx, a.Name)
	})
}
