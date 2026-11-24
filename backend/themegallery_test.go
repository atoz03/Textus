package backend

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
)

func TestOnlyThemeFilesInTheGalleryAreFetched(t *testing.T) {
	if got := galleryCSSURL("themes/plain/theme.css"); got != "https://textus.md/theme-gallery/themes/plain/theme.css" {
		t.Error(got)
	}
	if galleryPreviewURL("themes/midnight-ink/preview.png") == "" || galleryPreviewURL("themes/lumen/preview-2.WEBP") == "" {
		t.Error("a screenshot was refused")
	}
	for _, path := range []string{
		"https://example.com/theme.css", "//example.com/theme.css", "/themes/plain/theme.css",
		"themes/../index.json", "themes/plain/../../x/theme.css", "themes/plain/sub/theme.css",
		"themes/Plain/theme.css", "themes/plain/.theme.css", "themes/plain/theme.css?x=1",
		"themes/plain/theme.css#x", "themes/plain%2f../theme.css", `themes/plain\theme.css`,
		"pending/plain/theme.css", "themes/plain/theme.js", "themes//theme.css", "",
	} {
		if galleryCSSURL(path) != "" {
			t.Errorf("%s was allowed", path)
		}
	}
	// A stylesheet is not a screenshot, nor the other way round.
	if galleryPreviewURL("themes/plain/theme.css") != "" || galleryCSSURL("themes/plain/preview.png") != "" ||
		galleryPreviewURL("themes/plain/preview.svg") != "" {
		t.Error("a file of the wrong kind was allowed")
	}
}

func TestTheCatalogKeepsWellFormedThemesAndDropsTheRest(t *testing.T) {
	themes, err := parseCatalog([]byte(`{
		"version": 1, "updated": "2026-10-18T04:55:06Z", "count": 5,
		"themes": [
			{"id": "plain", "name": "Plain", "author": "CatVinci-Studio",
			 "description": "Default look.", "license": "MIT",
			 "source": "https://github.com/CatVinci-Studio/textus-themes",
			 "created": "2026-10-18T04:55:06Z", "updated": "2026-10-18T04:55:06Z",
			 "css": "themes/plain/theme.css", "darkCss": "themes/plain/theme-dark.css",
			 "previews": ["themes/plain/preview.png", "https://evil.test/x.png", "themes/plain/b.jpg", "themes/plain/c.webp"],
			 "installs": 12, "l10n": {"zh": {"name": "朴素"}}},
			{"id": "evil", "name": "Evil", "updated": "2026-10-18T00:00:00Z", "css": "https://evil.test/theme.css"},
			{"id": "../up", "name": "Up", "updated": "2026-10-18T00:00:00Z", "css": "themes/up/theme.css"},
			{"id": "no-name", "updated": "2026-10-18T00:00:00Z", "css": "themes/no-name/theme.css"},
			{"id": "plain", "name": "Plain again", "updated": "2026-10-19T00:00:00Z", "css": "themes/plain/theme.css"},
			{"id": "count", "name": "Count", "updated": "x", "css": "themes/count/theme.css", "installs": -1},
			{"id": "minimal", "name": "  Minimal  ", "updated": "2026-10-17T00:00:00Z",
			 "css": "themes/minimal/theme.css", "source": "javascript:alert(1)",
			 "darkCss": "https://evil.test/dark.css", "extra": "ignored"}
		]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, theme := range themes {
		ids = append(ids, theme.ID)
	}
	if !slices.Equal(ids, []string{"plain", "minimal"}) {
		t.Fatal(ids)
	}
	plain, minimal := themes[0], themes[1]
	if plain.Author != "CatVinci-Studio" || !slices.Equal(plain.Previews, []string{"themes/plain/preview.png", "themes/plain/b.jpg"}) ||
		plain.DarkCSS == nil || *plain.DarkCSS != "themes/plain/theme-dark.css" || *plain.Installs != 12 || *plain.L10n["zh"].Name != "朴素" {
		t.Errorf("%+v", plain)
	}
	// A dark form only from the gallery itself; a source only on GitHub.
	if minimal.Name != "Minimal" || minimal.Source != "" || minimal.DarkCSS != nil || len(minimal.Previews) != 0 {
		t.Errorf("%+v", minimal)
	}
	// Written back, the lists are lists and the map a map, never null.
	raw, _ := jsonMarshalForTest(minimal)
	if !strings.Contains(raw, `"previews":[]`) || !strings.Contains(raw, `"l10n":{}`) || !strings.Contains(raw, `"darkCss":null`) {
		t.Error(raw)
	}
}

func TestACatalogThatIsNotOneIsAnError(t *testing.T) {
	for _, raw := range []string{"<html>", `{"version": 1}`, `[1]`, `{"themes": 3}`} {
		if _, err := parseCatalog([]byte(raw)); err == nil {
			t.Errorf("%s was read", raw)
		}
	}
	if themes, err := parseCatalog([]byte(`{"themes": []}`)); err != nil || themes == nil || len(themes) != 0 {
		t.Error(themes, err)
	}
}

func TestOnlySelfContainedStylesheetsAreInstalled(t *testing.T) {
	for css, ok := range map[string]bool{
		"#write { color: red; }": true,
		`@font-face { src: url("data:font/woff2;base64,AAAA"); } #write { background: URL( 'data:image/png;base64,AA' ) }`: true,
		`@import url("https://x.test/a.css");`:             false,
		"@IMPORT 'b.css';":                                 false,
		"#write { background: url(https://x.test/p.png) }": false,
		"#write { background: url( 'fonts/a.woff2' ) }":    false,
		// Comments and text that mention imports or URLs are not rules.
		"/* No @import here: every url(...) is a data: URI. */\n#write { color: red; }": true,
		`#write p::after { content: "@import 'x.css';" }`:                               true,
		`#write::before { content: 'it\'s @import' }`:                                   true,
		// A rule after a comment still counts; a comment left open hides the rest.
		"/* x */ @import 'a.css';":                            false,
		"/* a */ #w { background: url(http://x.test/a.png) }": false,
		"#w { color: red } /* @import 'a.css';":               true,
		`#w { background: url("https://x.test/a.png") }`:      false,
	} {
		if err := checkSelfContained(css); (err == nil) != ok {
			t.Errorf("%q: %v", css, err)
		}
	}
}

func TestAGalleryFetchFollowsNoRedirectAndKeepsToItsLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/moved":
			http.Redirect(w, r, "/theme.css", http.StatusFound)
		case "/big":
			w.Write([]byte(strings.Repeat("x", 100)))
		default:
			w.Write([]byte("#write {}"))
		}
	}))
	defer server.Close()
	ctx := context.Background()
	if data, err := galleryFetch(ctx, server.URL+"/theme.css", 50, nil); err != nil || string(data) != "#write {}" {
		t.Error(string(data), err)
	}
	if _, err := galleryFetch(ctx, server.URL+"/moved", 50, nil); err == nil || err.Error() != "the gallery answered 302 Found" {
		t.Error(err)
	}
	if _, err := galleryFetch(ctx, server.URL+"/big", 50, nil); err == nil || err.Error() != "the file is larger than the gallery allows" {
		t.Error(err)
	}
}
