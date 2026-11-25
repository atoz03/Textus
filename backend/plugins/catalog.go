package plugins

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"textus/backend/ai"
)

// The plugins and skills Textus offers to install, from the
// textus-plugins catalog. Only the backend fetches the catalog and
// packages, from fixed addresses, and checks each package's SHA-256, so
// "Official" is decided here, not by the window.

const (
	// PackagePrefix is where the catalog names its files: the
	// textus-plugins repository's releases. Anything else in it is left
	// out.
	PackagePrefix = "https://github.com/CatVinci-Studio/textus-plugins/releases/download/"
	// MirrorPrefix is where the app reads them: Textus's own site passes
	// each release file on, so the store works where GitHub can't be
	// reached.
	MirrorPrefix = "https://textus.md/plugin-release/"
	// CatalogURL is the catalog's published address.
	CatalogURL = "https://textus.md/plugin-release/catalog/index.json"
	// MaxSkillBytes: a skill file this large is not a skill.
	MaxSkillBytes = 256 * 1024
)

// Mirrored is a release file's address on the site; false for one from
// elsewhere.
func Mirrored(u string) (string, bool) {
	rest, ok := strings.CutPrefix(u, PackagePrefix)
	if !ok {
		return "", false
	}
	return MirrorPrefix + rest, true
}

// downloadHosts are the site's address, and the one it had before (which
// still serves).
var downloadHosts = []string{"textus.md", "textus.catvinci.com"}

// DownloadHost is whether a download may be redirected to u.
func DownloadHost(u *url.URL) bool {
	if u.Scheme != "https" {
		return false
	}
	for _, h := range downloadHosts {
		if u.Hostname() == h {
			return true
		}
	}
	return false
}

// EarlierVersion is an earlier version the catalog keeps (textus-plugins
// scripts/history.mjs; EarlierVersion in src/platform/bindings/plugins.ts).
type EarlierVersion struct {
	Version string `json:"version"`
	// Textus is its engines.textus.
	Textus      string   `json:"textus"`
	Permissions []string `json:"permissions"`
	URL         string   `json:"url"`
	Sha256      string   `json:"sha256"`
	Size        uint64   `json:"size"`
}

// CatalogEntry is one plugin in the catalog (CatalogEntry in
// src/platform/bindings/plugins.ts).
type CatalogEntry struct {
	ID       string `json:"id"`
	Version  string `json:"version"`
	Official bool   `json:"official"`
	// Manifest is its whole manifest, for Settings to show before it is
	// installed.
	Manifest jsontext.Value `json:"manifest"`
	URL      string         `json:"url"`
	Sha256   string         `json:"sha256"`
	Size     uint64         `json:"size"`
	// Screenshots are pictures of it at work, from the repository's
	// releases.
	Screenshots []string `json:"screenshots"`
	// Readme is its README's address by language code ("en" for
	// README.md).
	Readme map[string]string `json:"readme"`
	// Icon is its icon (SVG or PNG), from the repository's releases.
	Icon *string `json:"icon"`
	// Installs and Installs30 are how many devices installed it, ever and
	// in the last 30 days.
	Installs   *uint64 `json:"installs"`
	Installs30 *uint64 `json:"installs30"`
	// Published and Updated are when its first and latest versions came
	// out (YYYY-MM-DD).
	Published *string `json:"published"`
	Updated   *string `json:"updated"`
	// Unlisted: kept for those who have it but no longer offered.
	Unlisted bool `json:"unlisted"`
	// Versions are its earlier versions, newest first.
	Versions []EarlierVersion `json:"versions"`
}

// SkillEntry is one skill in the catalog (SkillEntry in
// src/platform/bindings/plugins.ts).
type SkillEntry struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	Description string `json:"description"`
	Author      string `json:"author"`
	Official    bool   `json:"official"`
	// Modes are the panel modes it works in; Write's when the catalog
	// doesn't say.
	Modes []ai.AgentMode `json:"modes"`
	// Labels are its name and one-line hint by UI language code.
	Labels     map[string]ai.SkillLabel `json:"labels"`
	URL        string                   `json:"url"`
	Sha256     string                   `json:"sha256"`
	Size       uint64                   `json:"size"`
	Installs   *uint64                  `json:"installs"`
	Installs30 *uint64                  `json:"installs30"`
}

// ParseCatalog reads the catalog's text for Textus appVersion: only its
// own releases' files kept, at the site's addresses, each plugin at the
// version this Textus runs.
func ParseCatalog(text, appVersion string) ([]CatalogEntry, []SkillEntry, error) {
	var catalog struct {
		Schema  *uint32         `json:"schema"`
		Plugins *[]CatalogEntry `json:"plugins"`
		Skills  []SkillEntry    `json:"skills"`
	}
	if err := json.Unmarshal([]byte(text), &catalog); err != nil {
		return nil, nil, fmt.Errorf("The plugin catalog can't be read: %v", err)
	}
	if catalog.Schema == nil || catalog.Plugins == nil {
		return nil, nil, fmt.Errorf("The plugin catalog can't be read: missing field `%s`", map[bool]string{true: "schema", false: "plugins"}[catalog.Schema == nil])
	}
	if *catalog.Schema != 1 {
		return nil, nil, Coded("catalogNewer")
	}
	plugins := []CatalogEntry{}
	for _, p := range *catalog.Plugins {
		if RequireID(p.ID) != nil {
			continue
		}
		var ok bool
		if p.URL, ok = Mirrored(p.URL); !ok {
			continue
		}
		shots := []string{}
		for _, s := range p.Screenshots {
			if m, ok := Mirrored(s); ok {
				shots = append(shots, m)
			}
		}
		p.Screenshots = shots
		readme := map[string]string{}
		for lang, u := range p.Readme {
			if m, ok := Mirrored(u); ok {
				readme[lang] = m
			}
		}
		p.Readme = readme
		if p.Icon != nil {
			if m, ok := Mirrored(*p.Icon); ok {
				p.Icon = &m
			} else {
				p.Icon = nil
			}
		}
		versions := []EarlierVersion{}
		for _, v := range p.Versions {
			if m, ok := Mirrored(v.URL); ok {
				v.URL = m
				if v.Permissions == nil {
					v.Permissions = []string{}
				}
				versions = append(versions, v)
			}
		}
		p.Versions = versions
		if len(p.Manifest) == 0 {
			p.Manifest = jsontext.Value("null")
		}
		plugins = append(plugins, Fitted(p, appVersion))
	}
	skills := []SkillEntry{}
	for _, s := range catalog.Skills {
		if strings.TrimSpace(s.Name) == "" || s.Size > MaxSkillBytes {
			continue
		}
		var ok bool
		if s.URL, ok = Mirrored(s.URL); !ok {
			continue
		}
		if s.Modes == nil {
			s.Modes = []ai.AgentMode{ai.ModeWrite}
		}
		if s.Labels == nil {
			s.Labels = map[string]ai.SkillLabel{}
		}
		skills = append(skills, s)
	}
	return plugins, skills, nil
}

// engineOf is a manifest's engines.textus, "*" without one.
func engineOf(manifest jsontext.Value) string {
	var m struct {
		Engines struct {
			Textus *string `json:"textus"`
		} `json:"engines"`
	}
	if json.Unmarshal(manifest, &m) != nil || m.Engines.Textus == nil {
		return "*"
	}
	return *m.Engines.Textus
}

// Fitted is entry as the version that runs on Textus appVersion: the
// newest, or the newest earlier one that does. When none does, the newest
// stays, and Settings says which Textus it needs.
func Fitted(entry CatalogEntry, appVersion string) CatalogEntry {
	if RunsOn(engineOf(entry.Manifest), appVersion) {
		return entry
	}
	for _, earlier := range entry.Versions {
		if !RunsOn(earlier.Textus, appVersion) {
			continue
		}
		var manifest map[string]any
		if json.Unmarshal(entry.Manifest, &manifest) != nil || manifest == nil {
			manifest = map[string]any{}
		}
		manifest["version"] = earlier.Version
		engines, _ := manifest["engines"].(map[string]any)
		if engines == nil {
			engines = map[string]any{}
		}
		engines["textus"] = earlier.Textus
		manifest["engines"] = engines
		manifest["permissions"] = earlier.Permissions
		if raw, err := json.Marshal(manifest, json.Deterministic(true)); err == nil {
			entry.Manifest = raw
		}
		entry.Version, entry.URL, entry.Sha256, entry.Size = earlier.Version, earlier.URL, earlier.Sha256, earlier.Size
		return entry
	}
	return entry
}

// ReadmeFor is the README for language among readme's: the language
// itself, its base ("pt" for "pt-BR"), then English. Traditional Chinese
// reads the English, never the Simplified.
func ReadmeFor(readme map[string]string, language string) (string, bool) {
	base, _, _ := strings.Cut(language, "-")
	if language == "zh-Hant" {
		base = "en"
	}
	for _, lang := range []string{language, base, "en"} {
		if u, ok := readme[lang]; ok {
			return u, true
		}
	}
	return "", false
}

// HexSHA256 is data's SHA-256 in lowercase hex.
func HexSHA256(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// Checked is whether data is the package entry describes.
func Checked(entry CatalogEntry, data []byte) error {
	if uint64(len(data)) != entry.Size {
		return fmt.Errorf("The download of %s is %d bytes, not the %d the catalog says - try again.", entry.ID, len(data), entry.Size)
	}
	if !strings.EqualFold(HexSHA256(data), entry.Sha256) {
		return fmt.Errorf("The download of %s isn't the package the catalog lists - it was not installed.", entry.ID)
	}
	return nil
}

// InstalledVersion is the version of plugin id installed in root, if one
// is.
func InstalledVersion(root, id string) (string, bool) {
	raw, err := os.ReadFile(filepath.Join(root, id, Manifest))
	if err != nil {
		return "", false
	}
	var m struct {
		Version *string `json:"version"`
	}
	if json.Unmarshal(raw, &m) != nil || m.Version == nil {
		return "", false
	}
	return *m.Version, true
}
