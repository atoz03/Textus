package web

import (
	"encoding/json/v2"
	"testing"
)

func TestCommonsPicturesComeInSearchOrderWithTheirCredit(t *testing.T) {
	var answer any
	err := json.Unmarshal([]byte(`{"query": {"pages": {
		"7": {"index": 2, "imageinfo": [{
			"thumburl": "https://upload.example/b.jpg", "url": "https://upload.example/b-full.jpg",
			"thumbwidth": 1280, "thumbheight": 960, "mime": "image/jpeg",
			"descriptionurl": "https://commons.wikimedia.org/wiki/File:B.jpg",
			"extmetadata": {"LicenseShortName": {"value": "Public domain"}}}]},
		"3": {"index": 1, "imageinfo": [{
			"url": "https://upload.example/a.png", "width": 640, "height": 480, "mime": "image/png",
			"descriptionurl": "https://commons.wikimedia.org/wiki/File:A.png",
			"extmetadata": {"Artist": {"value": "<a href=\"//x\">Ann</a> &amp; Bo"}, "LicenseShortName": {"value": "CC BY-SA 4.0"}}}]},
		"9": {"index": 3, "imageinfo": [{
			"url": "https://upload.example/c.pdf", "mime": "application/pdf",
			"descriptionurl": "https://commons.wikimedia.org/wiki/File:C.pdf",
			"extmetadata": {"LicenseShortName": {"value": "CC BY 4.0"}}}]},
		"11": {"index": 4, "imageinfo": [{
			"url": "https://upload.example/d.jpg", "mime": "image/jpeg",
			"descriptionurl": "https://commons.wikimedia.org/wiki/File:D.jpg"}]}
	}}}`), &answer)
	if err != nil {
		t.Fatal(err)
	}
	// A document and a picture with no licence are left out.
	images := commonsImages(answer)
	if len(images) != 2 {
		t.Fatal(images)
	}
	a, b := images[0], images[1]
	if a.URL != "https://upload.example/a.png" || a.Width != 640 || a.Height != 480 || a.Author != "Ann & Bo" {
		t.Fatal(a)
	}
	if b.URL != "https://upload.example/b.jpg" || b.Width != 1280 || b.Author != "" {
		t.Fatal(b)
	}
	if got := a.Credit(); got != "Ann & Bo · CC BY-SA 4.0 · [Wikimedia Commons](https://commons.wikimedia.org/wiki/File:A.png)" {
		t.Fatal(got)
	}
	if got := b.Credit(); got != "Public domain · [Wikimedia Commons](https://commons.wikimedia.org/wiki/File:B.jpg)" {
		t.Fatal(got)
	}
	if images := commonsImages(map[string]any{}); len(images) != 0 {
		t.Fatal(images)
	}
}

func TestAFetchedPicturesTypeIsWhatTheServerSaysElseItsAddress(t *testing.T) {
	for _, c := range []struct{ declared, address, mime, ext string }{
		{"image/png; charset=binary", "https://a.example/x.jpg", "image/png", "png"},
		{"", "https://a.example/x.WEBP?width=1280", "image/webp", "webp"},
		{"application/octet-stream", "https://a.example/x.gif#top", "image/gif", "gif"},
		{"", "https://a.example/x", "image/jpeg", "jpg"},
	} {
		if mime, ext := pictureType(c.declared, c.address); mime != c.mime || ext != c.ext {
			t.Errorf("%s %s: %s %s", c.declared, c.address, mime, ext)
		}
	}
}
