package backend

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"
)

// What the backend fetches from the web for the page: a font or picture a
// document names by URL, and a problem report sent on.

const webAssetMaxBytes = 20 * 1024 * 1024

// publicClient fetches from the public web only: an address on this
// machine or the local network is refused, wherever a name leads.
func publicClient(timeout time.Duration) *http.Client {
	dialer := &net.Dialer{
		Timeout: 10 * time.Second,
		Control: func(network, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			ip := net.ParseIP(host)
			if ip == nil || !isPublicIP(ip) {
				return fmt.Errorf("%s is not a public address", host)
			}
			return nil
		},
	}
	transport := &http.Transport{DialContext: dialer.DialContext, Proxy: http.ProxyFromEnvironment, TLSHandshakeTimeout: 10 * time.Second}
	return &http.Client{Timeout: timeout, Transport: transport}
}

func isPublicIP(ip net.IP) bool {
	return !(ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsUnspecified() || ip.IsMulticast() || ip.IsInterfaceLocalMulticast())
}

func publicURL(raw string) (*url.URL, bool) {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" {
		return nil, false
	}
	host := strings.ToLower(u.Hostname())
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") {
		return nil, false
	}
	if ip := net.ParseIP(host); ip != nil && !isPublicIP(ip) {
		return nil, false
	}
	return u, true
}

func fetchLimited(ctx context.Context, u *url.URL) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Safari/605.1.15")
	resp, err := publicClient(30 * time.Second).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("the server answered %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, webAssetMaxBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > webAssetMaxBytes {
		return nil, errors.New("the file is too large")
	}
	return body, nil
}

func fontMime(b []byte, u string) string {
	switch {
	case bytes.HasPrefix(b, []byte("wOF2")):
		return "font/woff2"
	case bytes.HasPrefix(b, []byte("wOFF")):
		return "font/woff"
	case bytes.HasPrefix(b, []byte("OTTO")):
		return "font/otf"
	case bytes.HasPrefix(b, []byte{0, 1, 0, 0}) || bytes.HasPrefix(b, []byte("true")):
		return "font/ttf"
	case strings.Contains(strings.ToLower(u), ".eot") && len(b) > 34:
		return "application/vnd.ms-fontobject"
	}
	return ""
}

// sniffPicture is a picture's type from its first bytes.
func sniffPicture(b []byte) string {
	switch {
	case bytes.HasPrefix(b, []byte("\x89PNG\r\n\x1a\n")):
		return "image/png"
	case bytes.HasPrefix(b, []byte{0xff, 0xd8, 0xff}):
		return "image/jpeg"
	case bytes.HasPrefix(b, []byte("GIF87a")) || bytes.HasPrefix(b, []byte("GIF89a")):
		return "image/gif"
	case len(b) >= 12 && string(b[:4]) == "RIFF" && string(b[8:12]) == "WEBP":
		return "image/webp"
	case bytes.HasPrefix(b, []byte("BM")):
		return "image/bmp"
	case len(b) >= 12 && string(b[4:8]) == "ftyp" && (string(b[8:12]) == "avif" || string(b[8:12]) == "avis"):
		return "image/avif"
	case bytes.HasPrefix(b, []byte("II*\x00")) || bytes.HasPrefix(b, []byte("MM\x00*")):
		return "image/tiff"
	}
	head := strings.ToLower(string(b[:min(len(b), 1024)]))
	if strings.Contains(head, "<svg") {
		return "image/svg+xml"
	}
	return ""
}

func (a *App) webAsset(ctx context.Context, raw, kind string) (string, error) {
	u, ok := publicURL(raw)
	if !ok {
		return "", errors.New("not a web address")
	}
	var cached string
	if kind != "image" {
		sum := sha256.Sum256([]byte(raw))
		cached = filepath.Join(a.dirs.Cache, "web-assets", hex.EncodeToString(sum[:]))
		if text, err := os.ReadFile(cached); err == nil {
			return string(text), nil
		}
	}
	body, err := fetchLimited(ctx, u)
	if err != nil {
		return "", err
	}
	dataURL := func(mime string) string {
		return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(body)
	}
	var answer string
	switch kind {
	case "stylesheet":
		if !utf8.Valid(body) {
			return "", errors.New("the stylesheet is not text")
		}
		if strings.HasPrefix(strings.TrimSpace(string(body)), "<") {
			return "", errors.New("not a stylesheet")
		}
		answer = string(body)
	case "font":
		mime := fontMime(body, raw)
		if mime == "" {
			return "", errors.New("not a font")
		}
		answer = dataURL(mime)
	case "image":
		mime := sniffPicture(body)
		if mime == "" {
			return "", errors.New("not a picture")
		}
		answer = dataURL(mime)
	default:
		return "", fmt.Errorf("unknown kind %q", kind)
	}
	if cached != "" {
		_ = writeFileAtomic(cached, []byte(answer))
	}
	return answer, nil
}

type SentReport struct {
	URL    string `json:"url"`
	Number int    `json:"number"`
}

func sendProblemReport(ctx context.Context, report jsontext.Value) (SentReport, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://textus.md/api/feedback", bytes.NewReader(report))
	if err != nil {
		return SentReport{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := publicClient(30 * time.Second).Do(req)
	if err != nil {
		return SentReport{}, err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusCreated:
		var sent SentReport
		body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		if err == nil {
			err = json.Unmarshal(body, &sent)
		}
		return sent, err
	case http.StatusTooManyRequests:
		return SentReport{}, codeError("sendLimit")
	}
	return SentReport{}, fmt.Errorf("The feedback service answered %d.", resp.StatusCode)
}
