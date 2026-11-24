package backend

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestTheURLFieldIsADottedPath(t *testing.T) {
	var nested any = map[string]any{"data": map[string]any{"image": map[string]any{"url": "https://img.test/a.png"}}}
	if got, ok := jsonPath(nested, "data.image.url"); !ok || got != "https://img.test/a.png" {
		t.Errorf("nested: %q", got)
	}
	// A number indexes an array (PicGo's result.0).
	var list any = map[string]any{"success": true, "result": []any{"https://img.test/a.png"}}
	if got, ok := jsonPath(list, "result.0"); !ok || got != "https://img.test/a.png" {
		t.Errorf("array: %q", got)
	}
	for _, path := range []string{"result.1", "result.x", "success", "nope"} {
		if got, ok := jsonPath(list, path); ok {
			t.Errorf("%s: %q", path, got)
		}
	}
}

func TestOnlyWebURLsAreAccepted(t *testing.T) {
	for s, want := range map[string]bool{
		"https://img.test/a.png": true,
		"http://img.test/a.png":  true,
		"file:///tmp/a.png":      false,
		"not a url":              false,
		"https://":               false,
	} {
		if isWebURL(s) != want {
			t.Errorf("%q: %v", s, !want)
		}
	}
}

func TestPicGoIsReachedOnlyOnThisComputer(t *testing.T) {
	picgo := imageHosts["picgo"]
	for _, ok := range []string{"http://127.0.0.1:36677/upload", "http://localhost:36677/upload", "http://[::1]:36677/upload"} {
		if _, err := picgo.uploadURL(ok); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
	for _, refused := range []string{"http://192.168.1.2:36677/upload", "https://picgo.example.com/upload", "http://127.0.0.1.example.com/upload"} {
		if _, err := picgo.uploadURL(refused); err == nil {
			t.Errorf("%s was accepted", refused)
		}
	}
}

func TestHostedServicesIgnoreTheEndpointSetting(t *testing.T) {
	for host, want := range map[string]string{
		"smms":   "https://sm.ms/api/v2/upload",
		"imgur":  "https://api.imgur.com/3/image",
		"custom": "http://elsewhere.test/",
	} {
		u, err := imageHosts[host].uploadURL(" http://elsewhere.test/ ")
		if err != nil || u.String() != want {
			t.Errorf("%s: %v %v", host, u, err)
		}
	}
	if _, err := imageHosts["custom"].uploadURL("ftp://x.test/"); err == nil {
		t.Error("ftp was accepted")
	}
	if _, err := imageHostOf("dropbox"); err == nil {
		t.Error("an unknown host was accepted")
	}
}

func TestEveryHostKeepsItsOwnToken(t *testing.T) {
	seen := map[string]string{}
	for name, h := range imageHosts {
		if other, ok := seen[h.keyID]; ok {
			t.Errorf("%s and %s share %s", name, other, h.keyID)
		}
		seen[h.keyID] = name
	}
	// Custom's id predates the presets, and keys saved under it stay.
	if imageHosts["custom"].keyID != "__image_host" {
		t.Error(imageHosts["custom"].keyID)
	}
}

// seen is one request an upload made.
type seen struct {
	path, query, auth, contentType string
	body                           string
}

// uploadTo sends a PNG to a server answering status and answer, and hands
// back the URL read from it and the request the server got.
func uploadTo(t *testing.T, host, token, urlField, filePath string, status int, answer string) (string, error, seen) {
	t.Helper()
	var got seen
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		got = seen{r.URL.Path, r.URL.RawQuery, r.Header.Get("Authorization"), r.Header.Get("Content-Type"), string(body)}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		io.WriteString(w, answer)
	}))
	defer server.Close()
	u, _ := url.Parse(server.URL + "/upload")
	p := upload{data: []byte("PNGDATA"), filename: "shot.png", mime: "image/png", path: filePath}
	src, err := imageHosts[host].send(context.Background(), u, token, urlField, p)
	return src, err, got
}

func TestPicGoGetsTheFilePathAndPicListItsKey(t *testing.T) {
	src, err, got := uploadTo(t, "picgo", "secret", "", "/tmp/textus-upload-1/shot.png", 200,
		`{"success":true,"result":["https://cdn.test/shot.png"]}`)
	if err != nil || src != "https://cdn.test/shot.png" {
		t.Fatal(src, err)
	}
	if got.path != "/upload" || got.query != "key=secret" || got.auth != "" ||
		got.body != `{"list":["/tmp/textus-upload-1/shot.png"]}` {
		t.Errorf("%+v", got)
	}
}

func TestPicGoSaysWhyAnUploadFailed(t *testing.T) {
	_, err, _ := uploadTo(t, "picgo", "", "", "/tmp/shot.png", 200, `{"success":false,"message":"upload error"}`)
	if err == nil || err.Error() != "PicGo: upload error" {
		t.Error(err)
	}
}

func TestAPicGoThatIsNotRunningIsNamed(t *testing.T) {
	// A port that was free a moment ago: nothing listens on it.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()
	u, _ := url.Parse("http://" + addr + "/upload")
	_, err = imageHosts["picgo"].send(context.Background(), u, "", "", upload{path: "/tmp/a.png"})
	if err == nil || err.Error() != "PicGo isn't running, or its server is turned off" {
		t.Error(err)
	}
}

func TestEachHostGetsItsFieldAndToken(t *testing.T) {
	for _, c := range []struct {
		host, token, urlField, answer, src, auth, field string
	}{
		{"smms", "tok", "", `{"success":true,"code":"success","data":{"url":"https://s2.loli.net/a.png"}}`,
			"https://s2.loli.net/a.png", "tok", "smfile"},
		// A picture SM.MS already has comes back with its URL.
		{"smms", "tok", "", `{"success":false,"code":"image_repeated","message":"exists","images":"https://s2.loli.net/a.png"}`,
			"https://s2.loli.net/a.png", "tok", "smfile"},
		{"imgur", "abc123", "", `{"data":{"link":"https://i.imgur.com/a.png"},"success":true,"status":200}`,
			"https://i.imgur.com/a.png", "Client-ID abc123", "image"},
		{"custom", "tok", "data.src", `{"data":{"src":"https://img.test/a.png"}}`,
			"https://img.test/a.png", "Bearer tok", "file"},
		// An answer that is the URL itself.
		{"custom", "", "ignored", "https://img.test/b.png\n", "https://img.test/b.png", "", "file"},
	} {
		src, err, got := uploadTo(t, c.host, c.token, c.urlField, "", 200, c.answer)
		if err != nil || src != c.src {
			t.Errorf("%s: %q %v", c.host, src, err)
			continue
		}
		if got.auth != c.auth || !strings.HasPrefix(got.contentType, "multipart/form-data") ||
			!strings.Contains(got.body, `name="`+c.field+`"; filename="shot.png"`) || !strings.Contains(got.body, "PNGDATA") {
			t.Errorf("%s: %+v", c.host, got)
		}
	}
}

func TestAnAnswerWithoutAURLSaysWhy(t *testing.T) {
	for _, c := range []struct{ host, urlField, answer, want string }{
		{"custom", "data.src", `{"data":{}}`, "image URL field `data.src` was not found"},
		{"imgur", "", `{"data":{}}`, "Imgur's answer has no image URL"},
		{"custom", "url", `{"url":"file:///etc/passwd"}`, "The image host returned an invalid public URL"},
		{"custom", "url", `Saved!`, "The image host answered with neither a URL nor JSON"},
	} {
		if _, err, _ := uploadTo(t, c.host, "", c.urlField, "", 200, c.answer); err == nil || err.Error() != c.want {
			t.Errorf("%s %s: %v", c.host, c.answer, err)
		}
	}
}

func TestAnErrorStatusIsReportedWithTheHostsName(t *testing.T) {
	_, err, _ := uploadTo(t, "imgur", "", "", "", 403, `{"data":{"error":"Invalid client_id"}}`)
	if err == nil || !strings.HasPrefix(err.Error(), "Imgur returned 403 Forbidden: ") {
		t.Error(err)
	}
	// An error page is not pasted into the message.
	page := "<!DOCTYPE html>\n<html><body>" + strings.Repeat("<p>Bad gateway</p>", 500) + "</body></html>"
	if _, err, _ := uploadTo(t, "custom", "", "url", "", 502, page); err == nil || err.Error() != "The image host returned 502 Bad Gateway." {
		t.Error(err)
	}
}

func TestAnErrorAnswerIsCutToItsFirstShortLine(t *testing.T) {
	if got := firstLine("\n  Bad token \nmore detail"); got != "Bad token" {
		t.Errorf("%q", got)
	}
	long := firstLine(strings.Repeat("é", maxErrorChars+10))
	if n := len([]rune(long)); n != maxErrorChars+1 || !strings.HasSuffix(long, "…") {
		t.Errorf("%d runes: %q", n, long)
	}
}

func TestUploadImageChecksWhatItIsGiven(t *testing.T) {
	a := &App{dirs: Dirs{Config: t.TempDir()}}
	ctx := context.Background()
	for _, c := range []struct{ name, host, data, filename, endpoint string }{
		{"unknown host", "dropbox", "UE5H", "a.png", ""},
		{"path in the name", "custom", "UE5H", "../a.png", "http://127.0.0.1/"},
		{"no picture", "custom", "", "a.png", "http://127.0.0.1/"},
		{"not base64", "custom", "%%%", "a.png", "http://127.0.0.1/"},
		{"no endpoint", "custom", "UE5H", "a.png", ""},
	} {
		if _, err := a.uploadImage(ctx, c.host, c.data, "image/png", c.filename, c.endpoint, "url"); err == nil {
			t.Errorf("%s: uploaded", c.name)
		}
	}
}
