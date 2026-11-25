package plugins

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"net/url"
	"slices"
	"strings"
	"testing"
)

// testVersion is the Textus the catalog is read for here.
const testVersion = "1.4.7"

func parseAll(t *testing.T, text string) ([]CatalogEntry, []SkillEntry) {
	t.Helper()
	plugins, skills, err := ParseCatalog(text, testVersion)
	if err != nil {
		t.Fatal(err)
	}
	return plugins, skills
}

func TestAReadmeIsInTheLanguageAskedForItsBaseOrEnglish(t *testing.T) {
	readme := map[string]string{"en": "en.md", "zh": "zh.md", "zh-Hant": "hant.md"}
	for lang, want := range map[string]string{"zh-Hant": "hant.md", "zh": "zh.md", "ja": "en.md"} {
		if got, ok := ReadmeFor(readme, lang); !ok || got != want {
			t.Errorf("%s: %s", lang, got)
		}
	}
	if _, ok := ReadmeFor(nil, "en"); ok {
		t.Error("no README is found")
	}
	if got, _ := ReadmeFor(map[string]string{"en": "en.md", "zh": "zh.md"}, "zh-Hant"); got != "en.md" {
		t.Errorf("Traditional Chinese reads %s", got)
	}
}

func TestAPluginsReadmeAndIconComeOnlyFromTheCatalogsAddress(t *testing.T) {
	ok := PackagePrefix + "catalog/readme-a.b.en.md"
	plugins, _ := parseAll(t, `{"schema": 1, "plugins": [{"id": "a.b", "version": "0.1.1", "manifest": {}, "url": "`+PackagePrefix+`a.b-v0.1.1/a.b-0.1.1.zip", "sha256": "ab", "size": 1, "readme": {"en": "`+ok+`", "zh": "https://example.com/x.md"}, "icon": "https://example.com/i.svg", "installs": 12, "installs30": 3}]}`)
	p := plugins[0]
	if len(p.Readme) != 1 || p.Readme["en"] == "" {
		t.Errorf("readme %v", p.Readme)
	}
	if p.Icon != nil {
		t.Errorf("icon %s", *p.Icon)
	}
	if p.Installs == nil || *p.Installs != 12 || p.Installs30 == nil || *p.Installs30 != 3 {
		t.Errorf("installs %v %v", p.Installs, p.Installs30)
	}
}

func TestTheCatalogListsSkillsBesidePluginsAndAnOlderOneNone(t *testing.T) {
	u := PackagePrefix + "catalog/skill-press-release.md"
	_, skills := parseAll(t, `{"schema": 1, "plugins": [], "skills": [{"name": "press-release", "version": "1.0.0", "description": "Writes a press release.", "url": "`+u+`", "sha256": "ab", "size": 10, "installs": 4}, {"name": "elsewhere", "version": "1", "description": "x", "url": "https://example.com/x.md", "sha256": "ab", "size": 10}]}`)
	if len(skills) != 1 {
		t.Fatalf("only from the catalog's own address: %d", len(skills))
	}
	s := skills[0]
	if s.Name != "press-release" || s.URL != MirrorPrefix+"catalog/skill-press-release.md" {
		t.Errorf("%+v", s)
	}
	if s.Installs == nil || *s.Installs != 4 || s.Installs30 != nil {
		t.Errorf("installs %v %v", s.Installs, s.Installs30)
	}
	if _, none := parseAll(t, `{"schema": 1, "plugins": []}`); len(none) != 0 {
		t.Errorf("an older catalog lists %d skills", len(none))
	}
}

func catalogEntry(data []byte) CatalogEntry {
	return CatalogEntry{
		ID: "catvinci.tasks", Version: "1.0.0", Official: true, Manifest: jsontext.Value("null"),
		URL:    PackagePrefix + "catvinci.tasks-v1.0.0/catvinci.tasks-1.0.0.zip",
		Sha256: HexSHA256(data), Size: uint64(len(data)),
	}
}

func TestAScreenshotFromAnywhereElseIsLeftOut(t *testing.T) {
	plugins, _ := parseAll(t, `{"schema": 1, "plugins": [{"id": "catvinci.tasks", "version": "1.0.0", "manifest": null, "url": "`+PackagePrefix+`t/t.zip", "sha256": "ab", "size": 1, "screenshots": ["`+PackagePrefix+`catalog/shot-catvinci.tasks-1.png", "https://example.com/x.png"]}]}`)
	if want := []string{MirrorPrefix + "catalog/shot-catvinci.tasks-1.png"}; !slices.Equal(plugins[0].Screenshots, want) {
		t.Errorf("%q", plugins[0].Screenshots)
	}
}

func TestAPackageMustBeTheOneTheCatalogLists(t *testing.T) {
	good := []byte("the package")
	if err := Checked(catalogEntry(good), good); err != nil {
		t.Error(err)
	}
	if err := Checked(catalogEntry(good), []byte("the packagE")); err == nil || !strings.Contains(err.Error(), "isn't the package") {
		t.Errorf("another package: %v", err)
	}
	if err := Checked(catalogEntry(good), []byte("short")); err == nil || !strings.Contains(err.Error(), "bytes") {
		t.Errorf("a short one: %v", err)
	}
}

func TestTheCatalogKeepsOnlyPackagesFromItsOwnReleases(t *testing.T) {
	text, err := json.Marshal(map[string]any{
		"schema": 1,
		"plugins": []any{
			map[string]any{"id": "catvinci.tasks", "version": "1.0.0", "official": true, "manifest": map[string]any{},
				"url": PackagePrefix + "catvinci.tasks-v1.0.0/a.zip", "sha256": "00", "size": 1},
			map[string]any{"id": "someone.else", "version": "1.0.0", "manifest": map[string]any{},
				"url": "https://example.com/else.zip", "sha256": "00", "size": 1},
			map[string]any{"id": "../escape", "version": "1.0.0", "manifest": map[string]any{},
				"url": PackagePrefix + "x/x.zip", "sha256": "00", "size": 1},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	entries, _ := parseAll(t, string(text))
	if len(entries) != 1 || entries[0].ID != "catvinci.tasks" {
		t.Fatalf("%+v", entries)
	}
	// Read from Textus's own site, never from GitHub.
	if entries[0].URL != MirrorPrefix+"catvinci.tasks-v1.0.0/a.zip" {
		t.Errorf("%s", entries[0].URL)
	}
	if _, _, err := ParseCatalog(`{"schema": 2, "plugins": []}`, testVersion); err == nil || err.Error() != "catalogNewer" {
		t.Errorf("a newer catalog: %v", err)
	}
}

func TestDownloadsComeFromTheSiteAtEitherOfItsAddresses(t *testing.T) {
	for raw, want := range map[string]bool{
		"https://textus.md/plugin-release/a.b-v1.0.0/a.b-1.0.0.zip":           true,
		"https://textus.catvinci.com/plugin-release/a.b-v1.0.0/a.b-1.0.0.zip": true,
		"http://textus.md/plugin-release/x.zip":                               false,
		"https://example.com/x.zip":                                           false,
	} {
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		if DownloadHost(u) != want {
			t.Errorf("%s: %v", raw, !want)
		}
	}
}

func TestRangesReadAsTheWindowReadsThem(t *testing.T) {
	for _, c := range []struct {
		rng, version string
		want         bool
	}{
		{">=1.4.2", "1.4.3-rc.1", true},
		{">=1.4.3", "1.4.2", false},
		{"^1.3.0", "1.4.2", true},
		{"^1.3.0", "2.0.0", false},
		{"1.4.x", "1.4.9", true},
		{"1.4.x", "1.5.0", false},
		{">=1.4.3-rc.2", "1.4.3-rc.1", false},
	} {
		if RunsOn(c.rng, c.version) != c.want {
			t.Errorf("%s on %s: %v", c.rng, c.version, !c.want)
		}
	}
}

func TestAnOlderTextusGetsTheNewestVersionItRuns(t *testing.T) {
	earlier := func(version, textus string) EarlierVersion {
		return EarlierVersion{Version: version, Textus: textus, Permissions: []string{"files.read"}, URL: "u-" + version, Sha256: "s-" + version, Size: 1}
	}
	entry := CatalogEntry{
		ID: "a.b", Version: "0.3.0", URL: "u-0.3.0",
		Manifest: jsontext.Value(`{"version": "0.3.0", "engines": {"textus": ">=1.4.3"}}`),
		Versions: []EarlierVersion{earlier("0.2.0", ">=1.4.2"), earlier("0.1.0", ">=1.3.0")},
	}
	got := Fitted(entry, "1.4.2")
	if got.Version != "0.2.0" || got.URL != "u-0.2.0" || engineOf(got.Manifest) != ">=1.4.2" {
		t.Errorf("%+v", got)
	}
	if got := Fitted(entry, "1.4.3"); got.Version != "0.3.0" {
		t.Errorf("the newest runs: %s", got.Version)
	}
	// None runs: the newest stays, to say which Textus it needs.
	if got := Fitted(entry, "1.2.0"); got.Version != "0.3.0" {
		t.Errorf("none runs: %s", got.Version)
	}
}

func TestOnlyANewerReleaseIsAnUpdate(t *testing.T) {
	// One that isn't semver, or a build ahead of the catalog, isn't counted
	// as updating.
	if IsNewer("0.2.0-dev", "0.1.0") || IsNewer("x", "1.0.0") || !IsNewer("0.1.0", "0.1.1") {
		t.Error("IsNewer")
	}
}
