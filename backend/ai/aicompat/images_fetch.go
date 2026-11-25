package aicompat

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

// Downloading a picture a service left at an address, and reading what it
// is. The address comes from the service, not the user, so: HTTPS only, no
// credentials, no private address when connecting directly, and a size
// cap counted while the body arrives.

// PictureNet is how a picture is downloaded: the app sets Proxy to its
// own.
var PictureNet = struct {
	// Proxy is the proxy a request to target goes through now, nil for
	// direct.
	Proxy func(target string) *url.URL
	// Loopback lets a test's fixture serve pictures from 127.0.0.1 over
	// plain HTTP; nothing outside the tests sets it.
	Loopback bool
}{Proxy: func(string) *url.URL { return nil }}

// MaxImageBytes is the largest image accepted, encoded. The biggest
// picture any of these services makes (4096 x 4096 PNG) stays well under
// it.
const MaxImageBytes = 20 << 20

const maxPictureRedirects = 3

// loopbackFixture reports whether u is a test's fixture on this computer.
func loopbackFixture(u *url.URL) bool {
	if !PictureNet.Loopback || u.Scheme != "http" {
		return false
	}
	ip, err := netip.ParseAddr(u.Hostname())
	return err == nil && ip.Is4() && ip.IsLoopback()
}

// upgraded: some services (DashScope's OSS links) still write `http://`
// for a host that serves HTTPS; asked for over HTTPS instead, never
// fetched as given.
func upgraded(u *url.URL) *url.URL {
	if u.Scheme == "http" && strings.HasSuffix(u.Hostname(), ".aliyuncs.com") && u.Port() == "" {
		u.Scheme = "https"
	}
	return u
}

// checkPictureURL is why u won't be opened, "" when it may be.
func checkPictureURL(u *url.URL) string {
	if loopbackFixture(u) {
		return ""
	}
	if u.Scheme != "https" {
		return "it isn't an HTTPS address"
	}
	if u.User != nil {
		return "it carries a user name or password"
	}
	host := u.Hostname()
	if ip, err := netip.ParseAddr(host); err == nil {
		if IsPublic(ip) {
			return ""
		}
	} else if host != "" && !IsLocalName(host) {
		return ""
	}
	return "it isn't a public address"
}

// publicDial resolves names as usual, but refuses a name that resolves to
// any non-public address, so a public-looking host can't point into the
// user's network; it connects to the addresses it checked.
func publicDial(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	addrs, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return nil, err
	}
	if !AllPublic(addrs) {
		return nil, fmt.Errorf("%s doesn't resolve to a public address", host)
	}
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	var last error
	for _, ip := range addrs {
		conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		if err == nil {
			return conn, nil
		}
		last = err
	}
	return nil, last
}

// pictureClient is a client for one download from u: through the app's
// proxy, or direct to public addresses only; each redirect checked.
func pictureClient(u *url.URL) *http.Client {
	transport := &http.Transport{TLSHandshakeTimeout: 10 * time.Second, DisableKeepAlives: true}
	if proxy := PictureNet.Proxy(u.String()); proxy != nil {
		transport.Proxy = http.ProxyURL(proxy)
	} else if PictureNet.Loopback {
		transport.DialContext = (&net.Dialer{Timeout: 10 * time.Second}).DialContext
	} else {
		transport.DialContext = publicDial
	}
	return &http.Client{Transport: transport, Timeout: GenerationTimeout, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= maxPictureRedirects {
			return errors.New("too many redirects")
		}
		if why := checkPictureURL(req.URL); why != "" {
			return errors.New("redirected to an address refused because " + why)
		}
		return nil
	}}
}

func tooLarge() error {
	return imageFailed(fmt.Sprintf("The picture is larger than %d MB.", MaxImageBytes/(1<<20)))
}

// download is the picture at raw, at most MaxImageBytes of it.
func download(ctx context.Context, raw string) ([]byte, error) {
	refused := func(why string) error {
		return imageFailed("The service sent a picture address Textus won't open (" + why + ").")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, refused(err.Error())
	}
	u = upgraded(u)
	if why := checkPictureURL(u); why != "" {
		return nil, refused(why)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, refused(err.Error())
	}
	resp, err := pictureClient(u).Do(req)
	if err != nil {
		return nil, imageFailed("Downloading the picture failed: " + err.Error())
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, imageFailed("Downloading the picture failed (" + resp.Status + ")")
	}
	if resp.ContentLength > MaxImageBytes {
		return nil, tooLarge()
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, MaxImageBytes+1))
	if err != nil {
		return nil, imageFailed("Downloading the picture failed: " + err.Error())
	}
	if len(body) > MaxImageBytes {
		return nil, tooLarge()
	}
	return body, nil
}

// What a generated image really is, read from its bytes rather than the
// provider's word for it. Only PNG, JPEG and WebP pass, so an SVG or an
// HTML error page never reaches the document; the size comes from the
// header.

// sniffed is a recognised image: its MIME type, the file extension it is
// saved under, and its size in pixels.
type sniffed struct {
	mime, ext     string
	width, height uint32
}

func sniff(b []byte) (sniffed, bool) {
	if len(b) > MaxImageBytes {
		return sniffed{}, false
	}
	var (
		found sniffed
		ok    bool
	)
	switch {
	case strings.HasPrefix(string(b[:min(len(b), 8)]), "\x89PNG\r\n\x1a\n"):
		found = sniffed{mime: "image/png", ext: "png"}
		found.width, found.height, ok = pngSize(b)
	case len(b) >= 3 && b[0] == 0xff && b[1] == 0xd8 && b[2] == 0xff:
		found = sniffed{mime: "image/jpeg", ext: "jpg"}
		found.width, found.height, ok = jpegSize(b)
	case len(b) >= 12 && string(b[:4]) == "RIFF" && string(b[8:12]) == "WEBP":
		found = sniffed{mime: "image/webp", ext: "webp"}
		found.width, found.height, ok = webpSize(b)
	}
	return found, ok && found.width > 0 && found.height > 0
}

func be32(b []byte, at int) (uint32, bool) {
	if at+4 > len(b) {
		return 0, false
	}
	return binary.BigEndian.Uint32(b[at:]), true
}

func be16(b []byte, at int) (uint32, bool) {
	if at+2 > len(b) {
		return 0, false
	}
	return uint32(binary.BigEndian.Uint16(b[at:])), true
}

func le16(b []byte, at int) (uint32, bool) {
	if at+2 > len(b) {
		return 0, false
	}
	return uint32(binary.LittleEndian.Uint16(b[at:])), true
}

func le24(b []byte, at int) (uint32, bool) {
	if at+3 > len(b) {
		return 0, false
	}
	return uint32(b[at]) | uint32(b[at+1])<<8 | uint32(b[at+2])<<16, true
}

// pngSize reads the IHDR chunk, which PNG requires first.
func pngSize(b []byte) (uint32, uint32, bool) {
	if len(b) < 16 || string(b[12:16]) != "IHDR" {
		return 0, 0, false
	}
	w, ok1 := be32(b, 16)
	h, ok2 := be32(b, 20)
	return w, h, ok1 && ok2
}

// jpegSize reads the first start-of-frame segment. Markers C4 (Huffman
// tables), C8 and CC (arithmetic coding) share the SOF range without being
// frames.
func jpegSize(b []byte) (uint32, uint32, bool) {
	at := 2
	for at+4 <= len(b) {
		if b[at] != 0xff {
			return 0, 0, false
		}
		marker := b[at+1]
		// Fill bytes before a marker.
		if marker == 0xff {
			at++
			continue
		}
		length, ok := be16(b, at+2)
		if !ok {
			return 0, 0, false
		}
		if marker >= 0xc0 && marker <= 0xcf && marker != 0xc4 && marker != 0xc8 && marker != 0xcc {
			h, ok1 := be16(b, at+5)
			w, ok2 := be16(b, at+7)
			return w, h, ok1 && ok2
		}
		at += 2 + int(length)
	}
	return 0, 0, false
}

// webpSize: lossy (VP8), lossless (VP8L) or extended (VP8X) - the first
// chunk names which, and each keeps the size in its own place.
func webpSize(b []byte) (uint32, uint32, bool) {
	if len(b) < 16 {
		return 0, 0, false
	}
	switch string(b[12:16]) {
	case "VP8 ":
		// After the frame tag and the 9d 01 2a start code; 14 bits each.
		if len(b) < 26 || b[23] != 0x9d || b[24] != 0x01 || b[25] != 0x2a {
			return 0, 0, false
		}
		w, ok1 := le16(b, 26)
		h, ok2 := le16(b, 28)
		return w & 0x3fff, h & 0x3fff, ok1 && ok2
	case "VP8L":
		if len(b) < 25 || b[20] != 0x2f {
			return 0, 0, false
		}
		bits := binary.LittleEndian.Uint32(b[21:25])
		return bits&0x3fff + 1, (bits>>14)&0x3fff + 1, true
	case "VP8X":
		w, ok1 := le24(b, 24)
		h, ok2 := le24(b, 27)
		return w + 1, h + 1, ok1 && ok2
	}
	return 0, 0, false
}
