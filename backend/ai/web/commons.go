package web

import (
	"cmp"
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"textus/backend/ai/aicompat"
)

// Real pictures from Wikimedia Commons: searched by description, each with
// its author and licence, so a document can show it with the credit its
// licence asks for. Commons only - a picture found on any other page comes
// with no licence to use it.

const (
	commonsAPI = "https://commons.wikimedia.org/w/api.php"
	// commonsResults are the results asked for per search, best first.
	commonsResults = 8
	// commonsWidth is the width a picture is fetched at: enough for a
	// page, small to keep.
	commonsWidth = 1280
	// commonsMaxBytes: larger than this, a picture isn't kept.
	commonsMaxBytes = 6 << 20
)

// CommonsImage is one picture on Commons and what showing it takes.
type CommonsImage struct {
	// Page is its file page, where the credit links to.
	Page string
	// URL is the picture at commonsWidth, to fetch.
	URL           string
	Mime          string
	Width, Height uint32
	// Author is who made it, as plain text ("" when Commons doesn't say).
	Author string
	// License is "CC BY-SA 4.0", "Public domain", …
	License string
}

// Credit is the credit its licence asks for, in a line: author, licence,
// and where it is from.
func (c CommonsImage) Credit() string {
	who := ""
	if c.Author != "" {
		who = c.Author + " · "
	}
	return fmt.Sprintf("%s%s · [Wikimedia Commons](%s)", who, c.License, c.Page)
}

// commonsGet is a GET from Commons, its body at most limit bytes.
func commonsGet(ctx context.Context, address string, limit int64) (*http.Response, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("User-Agent", Net.UserAgent())
	resp, err := (&http.Client{Timeout: searchTimeout, Transport: Net.Transport}).Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return resp, nil, fmt.Errorf("Wikimedia Commons answered %s", statusText(resp.StatusCode))
	}
	if resp.ContentLength > limit {
		return resp, nil, errTooLarge
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err == nil && int64(len(body)) > limit {
		err = errTooLarge
	}
	return resp, body, err
}

var errTooLarge = errors.New("too large")

// CommonsSearch are the pictures on Commons that best match query -
// photographs and drawings, not documents - best first.
func CommonsSearch(ctx context.Context, query string) ([]CommonsImage, error) {
	params := url.Values{
		"action":              {"query"},
		"format":              {"json"},
		"generator":           {"search"},
		"gsrnamespace":        {"6"},
		"gsrsearch":           {query + " filetype:bitmap"},
		"gsrlimit":            {strconv.Itoa(commonsResults)},
		"prop":                {"imageinfo"},
		"iiprop":              {"url|mime|size|extmetadata"},
		"iiurlwidth":          {strconv.Itoa(commonsWidth)},
		"iiextmetadatafilter": {"Artist|LicenseShortName"},
	}
	resp, body, err := commonsGet(ctx, commonsAPI+"?"+params.Encode(), maxBodyBytes)
	if err != nil {
		if resp == nil {
			return nil, fmt.Errorf("Wikimedia Commons couldn't be reached: %v", err)
		}
		return nil, err
	}
	var answer any
	if err := json.Unmarshal(body, &answer); err != nil {
		return nil, err
	}
	return commonsImages(answer), nil
}

// member is v's member at path, nil where there is none.
func member(v any, path ...string) any {
	for _, key := range path {
		o, _ := v.(map[string]any)
		v = o[key]
	}
	return v
}

// count is v as a whole number, as serde's as_u64 reads one.
func count(v any) (uint64, bool) {
	f, ok := v.(float64)
	if !ok || f < 0 || f >= 1<<64 || f != math.Trunc(f) {
		return 0, false
	}
	return uint64(f), true
}

// commonsImages are the pictures in a Commons answer, in its search order;
// those without a licence or a picture to fetch are left out.
func commonsImages(answer any) []CommonsImage {
	pages, _ := member(answer, "query", "pages").(map[string]any)
	type page struct {
		index uint64
		value any
	}
	var ordered []page
	for _, p := range pages {
		index, ok := count(member(p, "index"))
		if !ok {
			index = math.MaxUint64
		}
		ordered = append(ordered, page{index, p})
	}
	slices.SortStableFunc(ordered, func(a, b page) int { return cmp.Compare(a.index, b.index) })
	var out []CommonsImage
	for _, p := range ordered {
		infos, _ := member(p.value, "imageinfo").([]any)
		if len(infos) == 0 {
			continue
		}
		info := infos[0]
		text := func(path ...string) (string, bool) {
			s, ok := member(info, path...).(string)
			return s, ok
		}
		licenseHTML, ok := text("extmetadata", "LicenseShortName", "value")
		if !ok {
			continue
		}
		license := plainHTML(licenseHTML)
		address, ok := text("thumburl")
		if !ok {
			if address, ok = text("url"); !ok {
				continue
			}
		}
		mime, _ := text("mime")
		pageURL, ok := text("descriptionurl")
		if license == "" || !strings.HasPrefix(mime, "image/") || !ok {
			continue
		}
		size := func(thumb, whole string) uint32 {
			if n, ok := count(member(info, thumb)); ok {
				return uint32(n)
			}
			n, _ := count(member(info, whole))
			return uint32(n)
		}
		author, _ := text("extmetadata", "Artist", "value")
		out = append(out, CommonsImage{
			Page: pageURL, URL: address, Mime: mime,
			Width: size("thumbwidth", "width"), Height: size("thumbheight", "height"),
			Author: plainHTML(author), License: license,
		})
	}
	return out
}

// plainHTML is Commons' HTML-marked text ("<a href=…>Ann</a>") as plain
// words.
func plainHTML(html string) string {
	var out strings.Builder
	inTag := false
	for _, c := range html {
		switch {
		case c == '<':
			inTag = true
		case c == '>':
			inTag = false
		case !inTag:
			out.WriteRune(c)
		}
	}
	return strings.Join(strings.Fields(strings.ReplaceAll(out.String(), "&amp;", "&")), " ")
}

// CommonsFetch is the picture's bytes, with its type and file extension as
// the picture store keeps them.
func CommonsFetch(ctx context.Context, image CommonsImage) (aicompat.GeneratedImage, error) {
	resp, body, err := commonsGet(ctx, image.URL, commonsMaxBytes)
	switch {
	case errors.Is(err, errTooLarge):
		return aicompat.GeneratedImage{}, errors.New("The picture is too large.")
	case err != nil && resp == nil:
		return aicompat.GeneratedImage{}, fmt.Errorf("The picture couldn't be fetched: %v", err)
	case err != nil:
		return aicompat.GeneratedImage{}, err
	}
	mime, ext := pictureType(resp.Header.Get("Content-Type"), image.URL)
	return aicompat.GeneratedImage{Bytes: body, Mime: mime, Ext: ext, Width: image.Width, Height: image.Height}, nil
}

// pictureType is a fetched picture's type and extension: what the server
// says it is, else what its address ends in (before any `?query`), else
// JPEG.
func pictureType(declared, address string) (string, string) {
	path, _, _ := strings.Cut(address, "?")
	path, _, _ = strings.Cut(path, "#")
	ext := strings.ToLower(path[strings.LastIndex(path, ".")+1:])
	kind := ext
	declaredType, _, _ := strings.Cut(declared, ";")
	switch strings.TrimSpace(declaredType) {
	case "image/png":
		kind = "png"
	case "image/gif":
		kind = "gif"
	case "image/webp":
		kind = "webp"
	case "image/jpeg":
		kind = "jpg"
	}
	switch kind {
	case "png":
		return "image/png", "png"
	case "gif":
		return "image/gif", "gif"
	case "webp":
		return "image/webp", "webp"
	}
	return "image/jpeg", "jpg"
}
