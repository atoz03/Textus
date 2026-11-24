package backend

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestTheAssetSchemeKeepsSecretsOut(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	config := filepath.Join(home, "config")
	a := &App{dirs: Dirs{Config: config}}
	denied := []string{
		filepath.Join(config, "settings.json"),
		filepath.Join(home, ".ssh", "id_ed25519"),
		filepath.Join(home, ".config", "gh", "hosts.yml"),
		filepath.Join(home, ".netrc"),
	}
	for _, p := range denied {
		if !a.assetDenied(p) {
			t.Errorf("%s is served", p)
		}
	}
	allowed := []string{
		filepath.Join(config, "themes", "x", "light.css"),
		filepath.Join(home, "notes", "a.png"),
		filepath.Join(home, ".sshfoo", "a.png"),
	}
	for _, p := range allowed {
		if a.assetDenied(p) {
			t.Errorf("%s is refused", p)
		}
	}

	picture := filepath.Join(home, "notes", "a b.png")
	writeFile(t, picture, "png")
	secret := filepath.Join(home, ".ssh", "id")
	writeFile(t, secret, "key")
	link := filepath.Join(home, "notes", "link.png")
	if err := os.Symlink(secret, link); err != nil {
		t.Fatal(err)
	}
	serve := func(path string) int {
		r := httptest.NewRequest("GET", "asset://localhost/"+urlEncodeAll(path), nil)
		w := httptest.NewRecorder()
		a.serveAsset(w, r)
		return w.Code
	}
	if code := serve(picture); code != 200 {
		t.Errorf("picture: %d", code)
	}
	if code := serve(link); code != 403 {
		t.Errorf("a link to a secret: %d", code)
	}
	if code := serve(filepath.Join(home, "missing.png")); code != 404 {
		t.Errorf("missing: %d", code)
	}
}

// urlEncodeAll is encodeURIComponent, as convertFileSrc encodes a path.
func urlEncodeAll(s string) string {
	const safe = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_.!~*'()"
	out := make([]byte, 0, len(s)*3)
	for i := 0; i < len(s); i++ {
		c := s[i]
		if containsByte(safe, c) {
			out = append(out, c)
			continue
		}
		out = append(out, '%', "0123456789ABCDEF"[c>>4], "0123456789ABCDEF"[c&15])
	}
	return string(out)
}

func containsByte(s string, c byte) bool {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return true
		}
	}
	return false
}
