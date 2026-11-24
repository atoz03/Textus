package backend

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json/v2"
	"errors"
	"fmt"
	"mime/multipart"
	"net"
	"net/http"
	"net/textproto"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Pasted pictures uploaded to the image host Settings chooses. Each host
// is a preset of one upload path; "custom" takes a multipart `file`, an
// optional bearer token, and answers a plain URL or JSON with it at the
// field Settings names.

const (
	maxUploadBytes = 25 << 20
	// An answer is a URL or a little JSON; one far larger is neither.
	maxAnswerBytes = 1 << 20
	// An error page's first line, cut for the editor's one-line message.
	maxErrorChars = 160
	// Long enough for 25 MB on a slow uplink.
	uploadTimeout = 120 * time.Second
)

// imageHost is how one host takes a picture (bindings/files.ts ImageHost).
type imageHost struct {
	name     string // for messages
	endpoint string // "": the one Settings gives
	// field is the multipart field of the file; "" sends {"list": [path]},
	// naming a file on this computer, as PicGo's server takes it.
	field string
	// auth is how the token travels: "bearer", "plain" (Authorization:
	// <token>), "client-id" (Imgur's anonymous upload), "key-query".
	auth string
	// urlFields is where the JSON answer keeps the URL, tried in order;
	// none: the field Settings gives.
	urlFields []string
	// loopbackOnly: the host runs on this computer, and a request
	// anywhere else would send the file's path, and the key, off it.
	loopbackOnly bool
	// keyID is where its token is kept, beside the providers' keys under
	// an id never shown as a provider. Custom's predates the presets.
	keyID string
}

var imageHosts = map[string]imageHost{
	"picgo": {name: "PicGo", auth: "key-query", urlFields: []string{"result.0"}, loopbackOnly: true, keyID: "__image_host_picgo"},
	// A picture SM.MS has already is answered success: false, with its URL
	// in `images`.
	"smms":   {name: "SM.MS", endpoint: "https://sm.ms/api/v2/upload", field: "smfile", auth: "plain", urlFields: []string{"data.url", "images"}, keyID: "__image_host_smms"},
	"imgur":  {name: "Imgur", endpoint: "https://api.imgur.com/3/image", field: "image", auth: "client-id", urlFields: []string{"data.link"}, keyID: "__image_host_imgur"},
	"custom": {name: "The image host", field: "file", auth: "bearer", keyID: "__image_host"},
}

func imageHostOf(name string) (imageHost, error) {
	h, ok := imageHosts[name]
	if !ok {
		return imageHost{}, fmt.Errorf("unknown image host %q", name)
	}
	return h, nil
}

// uploadURL is where the upload goes: the preset's own address, or the
// one from Settings.
func (h imageHost) uploadURL(endpoint string) (*url.URL, error) {
	raw := h.endpoint
	if raw == "" {
		raw = strings.TrimSpace(endpoint)
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return nil, errors.New("invalid upload URL")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, errors.New("upload URL must use http or https")
	}
	if h.loopbackOnly && !isLoopbackAddress(u.Hostname()) {
		return nil, fmt.Errorf("%s runs on this computer: its address must be 127.0.0.1 or localhost", h.name)
	}
	return u, nil
}

// isLoopbackAddress is localhost or a loopback IP, written as such.
func isLoopbackAddress(host string) bool {
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return strings.EqualFold(host, "localhost")
}

// jsonPath is the text at a dot-separated path of value; a number indexes
// an array ("result.0").
func jsonPath(value any, path string) (string, bool) {
	for _, segment := range strings.Split(path, ".") {
		if segment == "" {
			continue
		}
		switch v := value.(type) {
		case map[string]any:
			value = v[segment]
		case []any:
			i, err := strconv.Atoi(segment)
			if err != nil || i < 0 || i >= len(v) {
				return "", false
			}
			value = v[i]
		default:
			return "", false
		}
	}
	s, ok := value.(string)
	return s, ok
}

func isWebURL(s string) bool {
	u, err := url.Parse(s)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

// firstLine is an error answer's first line, cut short.
func firstLine(text string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(text), "\n")
	line = strings.TrimRight(line, " \r\t")
	if utf8.RuneCountInString(line) > maxErrorChars {
		return string([]rune(line)[:maxErrorChars]) + "…"
	}
	return line
}

// upload is one picture on its way.
type upload struct {
	data     []byte
	filename string
	mime     string
	path     string // the file PicGo reads
}

// send uploads p and reads the picture's URL out of the answer.
func (h imageHost) send(ctx context.Context, u *url.URL, token, urlField string, p upload) (string, error) {
	if h.auth == "key-query" && token != "" {
		q := u.Query()
		q.Set("key", token)
		u.RawQuery = q.Encode()
	}
	var body bytes.Buffer
	contentType := "application/json"
	if h.field == "" {
		raw, _ := json.Marshal(map[string][]string{"list": {p.path}})
		body.Write(raw)
	} else {
		w := multipart.NewWriter(&body)
		header := textproto.MIMEHeader{}
		header.Set("Content-Disposition", fmt.Sprintf(`form-data; name=%q; filename=%q`, h.field, p.filename))
		header.Set("Content-Type", p.mime)
		part, err := w.CreatePart(header)
		if err != nil {
			return "", err
		}
		part.Write(p.data)
		if err := w.Close(); err != nil {
			return "", err
		}
		contentType = w.FormDataContentType()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), &body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", contentType)
	if token != "" {
		switch h.auth {
		case "bearer":
			req.Header.Set("Authorization", "Bearer "+token)
		case "plain":
			req.Header.Set("Authorization", token)
		case "client-id":
			req.Header.Set("Authorization", "Client-ID "+token)
		}
	}
	resp, err := netClient(uploadTimeout).Do(req)
	if err != nil {
		var urlErr *url.Error
		var opErr *net.OpError
		switch {
		case h.loopbackOnly && errors.As(err, &opErr) && opErr.Op == "dial":
			return "", fmt.Errorf("%s isn't running, or its server is turned off", h.name)
		case errors.As(err, &urlErr) && urlErr.Timeout():
			return "", fmt.Errorf("%s didn't answer in time", h.name)
		case errors.As(err, &urlErr):
			// Without its address, which may carry the key.
			return "", fmt.Errorf("couldn't reach %s: %v", h.name, urlErr.Err)
		}
		return "", fmt.Errorf("couldn't reach %s", h.name)
	}
	defer resp.Body.Close()
	answer, err := readLimited(resp, maxAnswerBytes)
	switch {
	case errors.Is(err, errTooLarge):
		return "", fmt.Errorf("%s returned %s and an answer too large to read", h.name, resp.Status)
	case err != nil:
		return "", fmt.Errorf("couldn't reach %s: %v", h.name, err)
	}
	text := strings.ToValidUTF8(string(answer), "�")
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		if strings.HasPrefix(strings.TrimSpace(resp.Header.Get("Content-Type")), "text/html") || strings.HasPrefix(strings.TrimSpace(text), "<") {
			return "", fmt.Errorf("%s returned %s.", h.name, resp.Status)
		}
		return "", fmt.Errorf("%s returned %s: %s", h.name, resp.Status, firstLine(text))
	}
	return h.pictureURL(strings.TrimSpace(text), urlField)
}

// pictureURL reads the URL out of a successful answer: the answer itself,
// or a field of its JSON.
func (h imageHost) pictureURL(text, urlField string) (string, error) {
	if isWebURL(text) {
		return text, nil
	}
	var answer any
	if err := json.Unmarshal([]byte(text), &answer); err != nil {
		return "", fmt.Errorf("%s answered with neither a URL nor JSON", h.name)
	}
	fields := h.urlFields
	if len(fields) == 0 {
		fields = []string{strings.TrimSpace(urlField)}
	}
	for _, field := range fields {
		if src, ok := jsonPath(answer, field); ok {
			if !isWebURL(src) {
				return "", fmt.Errorf("%s returned an invalid public URL", h.name)
			}
			return src, nil
		}
	}
	// PicGo and SM.MS say why with success: false and a message.
	if message, ok := jsonPath(answer, "message"); ok {
		return "", fmt.Errorf("%s: %s", h.name, message)
	}
	if len(h.urlFields) == 0 {
		return "", fmt.Errorf("image URL field `%s` was not found", strings.TrimSpace(urlField))
	}
	return "", fmt.Errorf("%s's answer has no image URL", h.name)
}

// UploadedImage is where the picture is now (bindings/files.ts).
type UploadedImage struct {
	Src string `json:"src"`
}

func (a *App) uploadImage(ctx context.Context, host, dataBase64, mime, filename, endpoint, urlField string) (UploadedImage, error) {
	h, err := imageHostOf(host)
	if err != nil {
		return UploadedImage{}, err
	}
	u, err := h.uploadURL(endpoint)
	if err != nil {
		return UploadedImage{}, err
	}
	if name, ok := oneName(filename); !ok || name != filename {
		return UploadedImage{}, errors.New("invalid image filename")
	}
	data, err := base64.StdEncoding.DecodeString(dataBase64)
	if err != nil {
		return UploadedImage{}, err
	}
	if len(data) == 0 || len(data) > maxUploadBytes {
		return UploadedImage{}, codeError("imageTooBig")
	}
	token, err := a.providerKey(h.keyID)
	if err != nil {
		return UploadedImage{}, err
	}
	p := upload{data: data, filename: filename, mime: mime}
	if h.field == "" {
		// PicGo reads the picture from a file named as it is uploaded, in a
		// private folder removed after.
		dir, err := os.MkdirTemp("", "textus-upload-")
		if err != nil {
			return UploadedImage{}, err
		}
		p.path = filepath.Join(dir, filename)
		defer func() {
			os.Remove(p.path)
			os.Remove(dir)
		}()
		if err := os.WriteFile(p.path, data, 0o600); err != nil {
			return UploadedImage{}, err
		}
	}
	src, err := h.send(ctx, u, token, urlField, p)
	return UploadedImage{Src: src}, err
}

func init() {
	command("image_host_key_id", func(c *Call, a struct {
		Host string `json:"host"`
	}) (string, error) {
		h, err := imageHostOf(a.Host)
		return h.keyID, err
	})
	command("upload_image", func(c *Call, a struct {
		Host       string `json:"host"`
		DataBase64 string `json:"dataBase64"`
		Mime       string `json:"mime"`
		Filename   string `json:"filename"`
		Endpoint   string `json:"endpoint"`
		URLField   string `json:"urlField"`
	}) (UploadedImage, error) {
		return c.app.uploadImage(c.ctx, a.Host, a.DataBase64, a.Mime, a.Filename, a.Endpoint, a.URLField)
	})
}
