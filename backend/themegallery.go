package backend

import (
	"context"
	"encoding/base64"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"
)

// The public theme gallery, browsed from Settings. Every request goes to
// Textus's own site, which passes the gallery repository's files on, by a
// path checked against the gallery's shape, so nothing can point the app
// at another host.

const (
	galleryBase    = "https://textus.md/theme-gallery/"
	galleryCatalog = "index.json"
	// The gallery takes a stylesheet up to 200 KB and screenshots up to
	// 2 MB; a little over each, so a file at the limit still loads.
	maxCatalogBytes = 1 << 20
	maxCSSBytes     = 256 << 10
	maxPreviewBytes = 2<<20 + 64<<10
	maxPreviews     = 2
	// Short: an offline machine should see the "couldn't reach" message,
	// not a long spinner.
	galleryTimeout = 15 * time.Second
)

// GalleryTheme is one theme of the catalog, as the frontend gets it
// (src/platform/bindings/app.ts GalleryTheme).
type GalleryTheme struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Author      string `json:"author"`
	Description string `json:"description"`
	License     string `json:"license"`
	Source      string `json:"source"`
	Created     string `json:"created"`
	Updated     string `json:"updated"`
	// CSS is repository-relative, e.g. themes/plain/theme.css.
	CSS string `json:"css"`
	// DarkCSS is its dark form; none for one design in both appearances.
	DarkCSS  *string  `json:"darkCss"`
	Previews []string `json:"previews"`
	// Installs is how many devices installed it, ever and in the last 30
	// days (counted by services/ext-stats).
	Installs   *uint64 `json:"installs"`
	Installs30 *uint64 `json:"installs30"`
	// L10n is its name and description in another UI language ("zh").
	L10n map[string]GalleryText `json:"l10n"`
}

// GalleryText is a theme's words in one UI language; what is missing stays
// English (bindings/app.ts GalleryText).
type GalleryText struct {
	Name        *string `json:"name"`
	Description *string `json:"description"`
}

// GalleryInstall is a stylesheet fetched to install the theme, which
// counts the install (bindings/app.ts GalleryInstall).
type GalleryInstall struct {
	// Version is the catalog's `updated`, which stands for a version.
	Version string `json:"version"`
	// Update: it replaces the installed copy.
	Update bool `json:"update"`
}

// galleryID is what the gallery's own check allows (lowercase letters,
// digits, -), and a safe path component.
func galleryID(id string) bool {
	if id == "" || len(id) > 64 {
		return false
	}
	for _, c := range id {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
			return false
		}
	}
	return true
}

func galleryFileName(name string) bool {
	if name == "" || len(name) > 64 || name[0] == '.' {
		return false
	}
	for _, c := range name {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.') {
			return false
		}
	}
	return true
}

// galleryFileURL is the address of a theme's file in the gallery, or ""
// for anything that isn't themes/<id>/<file>.<one of extensions>.
func galleryFileURL(path string, extensions ...string) string {
	parts := strings.Split(path, "/")
	if len(parts) != 3 || parts[0] != "themes" || !galleryID(parts[1]) || !galleryFileName(parts[2]) {
		return ""
	}
	dot := strings.LastIndexByte(parts[2], '.')
	if dot < 0 || !containsString(extensions, strings.ToLower(parts[2][dot+1:])) {
		return ""
	}
	return galleryBase + path
}

func galleryCSSURL(path string) string { return galleryFileURL(path, "css") }

func galleryPreviewURL(path string) string {
	return galleryFileURL(path, "png", "jpg", "jpeg", "webp")
}

// clip is text trimmed and cut to max characters.
func clip(text string, max int) string {
	text = strings.TrimSpace(text)
	if utf8.RuneCountInString(text) > max {
		return string([]rune(text)[:max])
	}
	return text
}

// parseCatalog is the catalog's themes the app can show and install. A
// malformed entry is left out rather than failing the whole list.
func parseCatalog(raw []byte) ([]GalleryTheme, error) {
	if !jsontext.Value(raw).IsValid() {
		return nil, errors.New("catalog is not JSON")
	}
	var root struct {
		Themes []jsontext.Value `json:"themes"`
	}
	if err := json.Unmarshal(raw, &root); err != nil || root.Themes == nil {
		return nil, errors.New("catalog has no theme list")
	}
	themes := []GalleryTheme{}
	seen := map[string]bool{}
	for _, entry := range root.Themes {
		var theme GalleryTheme
		if json.Unmarshal(entry, &theme) != nil {
			continue
		}
		if !galleryID(theme.ID) || strings.TrimSpace(theme.Name) == "" || strings.TrimSpace(theme.Updated) == "" ||
			galleryCSSURL(theme.CSS) == "" || seen[theme.ID] {
			continue
		}
		seen[theme.ID] = true
		if theme.DarkCSS != nil && galleryCSSURL(*theme.DarkCSS) == "" {
			theme.DarkCSS = nil
		}
		previews := []string{}
		for _, p := range theme.Previews {
			if galleryPreviewURL(p) != "" && len(previews) < maxPreviews {
				previews = append(previews, p)
			}
		}
		theme.Previews = previews
		if theme.L10n == nil {
			theme.L10n = map[string]GalleryText{}
		}
		theme.Name = clip(theme.Name, 80)
		theme.Author = clip(theme.Author, 80)
		theme.Description = clip(theme.Description, 400)
		theme.License = clip(theme.License, 40)
		theme.Updated = clip(theme.Updated, 40)
		theme.Created = clip(theme.Created, 40)
		// Only a link to the author's repository means anything here.
		if !strings.HasPrefix(theme.Source, "https://github.com/") {
			theme.Source = ""
		}
		themes = append(themes, theme)
	}
	return themes, nil
}

// withoutComments is css without its comments, so a remark that mentions
// @import or url( isn't read as one. An unclosed comment runs to the end.
func withoutComments(css string) string {
	var out strings.Builder
	for {
		start := strings.Index(css, "/*")
		if start < 0 {
			break
		}
		out.WriteString(css[:start])
		out.WriteByte(' ')
		end := strings.Index(css[start+2:], "*/")
		if end < 0 {
			css = ""
			break
		}
		css = css[start+2+end+2:]
	}
	out.WriteString(css)
	return out.String()
}

// withoutStrings is css with every quoted string emptied (content:
// "@import" is text, not a rule). The quotes stay; a backslash escapes
// the next character.
func withoutStrings(css string) string {
	var out strings.Builder
	for i := 0; i < len(css); i++ {
		c := css[i]
		out.WriteByte(c)
		if c != '"' && c != '\'' {
			continue
		}
		for i++; i < len(css); i++ {
			if css[i] == '\\' {
				i++
			} else if css[i] == c {
				out.WriteByte(c)
				break
			}
		}
	}
	return out.String()
}

// checkSelfContained is the gallery's rule: one file, no @import, and every
// url() a data: URI, so a theme never fetches when shown. Comments never
// count.
func checkSelfContained(css string) error {
	code := strings.ToLower(withoutComments(css))
	if strings.Contains(withoutStrings(code), "@import") {
		return errors.New("the theme loads another stylesheet (@import)")
	}
	for rest := code; ; {
		at := strings.Index(rest, "url(")
		if at < 0 {
			return nil
		}
		rest = rest[at+4:]
		target := strings.TrimLeft(strings.TrimSpace(rest), `"'`)
		if !strings.HasPrefix(strings.TrimSpace(target), "data:") {
			return errors.New("the theme loads a file from the internet (url())")
		}
	}
}

// galleryFetch gets one file, following redirects only as follow allows
// (none: the allow-list is the address asked for).
func galleryFetch(ctx context.Context, target string, limit int64, follow func(*http.Request, []*http.Request) error) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	client := netClient(galleryTimeout)
	client.CheckRedirect = follow
	if follow == nil {
		client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, errors.New(netErrorText(err))
	}
	defer resp.Body.Close()
	if err := checkRedirected(resp); err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("the gallery answered %s", resp.Status)
	}
	data, err := readLimited(resp, limit)
	if errors.Is(err, errTooLarge) {
		return nil, errors.New("the file is larger than the gallery allows")
	}
	return data, err
}

// galleryCSS is a gallery theme's stylesheet, by its catalog path; with
// install, the download counts as that theme's install.
func (a *App) galleryCSS(ctx context.Context, path string, install *GalleryInstall) (string, error) {
	direct := galleryCSSURL(path)
	if direct == "" {
		return "", errors.New("not a gallery theme stylesheet")
	}
	id := strings.Split(path, "/")[1]
	counted := ""
	if install != nil {
		counted = a.countedURL(extDownload{kind: "theme", id: id, version: install.Version,
			update: install.Update, present: install.Update, direct: direct})
	}
	// From the endpoint only to this stylesheet; nowhere further.
	follow := followRedirects(direct, func(*url.URL) bool { return false })
	data, err := countedOrDirect(counted, direct, func(from string) ([]byte, error) {
		return galleryFetch(ctx, from, maxCSSBytes, follow)
	})
	if err != nil {
		return "", err
	}
	if install != nil {
		a.noteInstalled("theme", id)
	}
	if !utf8.Valid(data) {
		return "", errors.New("the theme is not UTF-8 text")
	}
	css := string(data)
	return css, checkSelfContained(css)
}

// galleryPreview is a gallery screenshot as a data: URL, so the page never
// loads from the network itself.
func galleryPreview(ctx context.Context, path string) (string, error) {
	target := galleryPreviewURL(path)
	if target == "" {
		return "", errors.New("not a gallery screenshot")
	}
	data, err := galleryFetch(ctx, target, maxPreviewBytes, nil)
	if err != nil {
		return "", err
	}
	// By its bytes: a screenshot is a bitmap, never a page or an SVG.
	mime := sniffPicture(data)
	if !containsString([]string{"image/png", "image/jpeg", "image/gif", "image/webp", "image/avif"}, mime) {
		return "", errors.New("the screenshot is not an image")
	}
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data), nil
}

func init() {
	command("theme_gallery_catalog", func(c *Call, _ none) ([]GalleryTheme, error) {
		data, err := galleryFetch(c.ctx, galleryBase+galleryCatalog, maxCatalogBytes, nil)
		if err != nil {
			return nil, err
		}
		return parseCatalog(data)
	})
	command("theme_gallery_css", func(c *Call, a struct {
		Path    string          `json:"path"`
		Install *GalleryInstall `json:"install"`
	}) (string, error) {
		return c.app.galleryCSS(c.ctx, a.Path, a.Install)
	})
	command("theme_gallery_preview", func(c *Call, a struct {
		Path string `json:"path"`
	}) (string, error) {
		return galleryPreview(c.ctx, a.Path)
	})
}
