package backend

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// setHome makes dir the user's home folder, as os.UserHomeDir reads it.
func setHome(t *testing.T, dir string) {
	t.Helper()
	t.Setenv("HOME", dir)
	if runtime.GOOS == "windows" {
		t.Setenv("USERPROFILE", dir)
	}
}

// serveAssetCode is the status of an asset request for path.
func serveAssetCode(a *App, path string) int {
	r := httptest.NewRequest("GET", "asset://localhost/"+urlEncodeAll(path), nil)
	w := httptest.NewRecorder()
	a.serveAsset(w, r)
	return w.Code
}

func TestTheAssetSchemeKeepsSecretsOut(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
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
	if code := serveAssetCode(a, picture); code != 200 {
		t.Errorf("picture: %d", code)
	}
	if code := serveAssetCode(a, link); code != 403 {
		t.Errorf("a link to a secret: %d", code)
	}
	if code := serveAssetCode(a, filepath.Join(home, "missing.png")); code != 404 {
		t.Errorf("missing: %d", code)
	}
}

// The secrets asked for under other spellings of the home folder than the
// one HOME gives, each way round: through a link (/var and /private/var
// on a Mac), in another case where the file system ignores it, through a
// Mac's data volume.
func TestTheAssetSchemeKeepsSecretsOutUnderAnotherSpelling(t *testing.T) {
	home := resolvedPath(t.TempDir())
	writeFile(t, filepath.Join(home, "config", "settings.json"), "{}")
	writeFile(t, filepath.Join(home, ".ssh", "id"), "key")
	writeFile(t, filepath.Join(home, ".netrc"), "machine example.com")
	writeFile(t, filepath.Join(home, "notes", "a.png"), "png")
	secrets := func(root string) []string {
		var out []string
		for _, p := range []string{
			filepath.Join(root, "config", "settings.json"),
			filepath.Join(root, "CONFIG", "Settings.JSON"),
			filepath.Join(root, ".ssh", "id"),
			filepath.Join(root, ".SSH", "ID"),
			filepath.Join(root, ".netrc"),
			filepath.Join(root, ".NETRC"),
		} {
			// The other cases are there only where case is ignored.
			if _, err := os.Stat(p); err == nil {
				out = append(out, p)
			}
		}
		return out
	}
	check := func(homeAs, askAs string) {
		t.Helper()
		setHome(t, homeAs)
		a := &App{dirs: Dirs{Config: filepath.Join(homeAs, "config")}}
		for _, p := range secrets(askAs) {
			if code := serveAssetCode(a, p); code != 403 {
				t.Errorf("HOME %s: %s: %d", homeAs, p, code)
			}
		}
		if code := serveAssetCode(a, filepath.Join(askAs, "notes", "a.png")); code != 200 {
			t.Errorf("HOME %s: a picture: %d", homeAs, code)
		}
	}
	check(home, home)
	for _, other := range otherSpellings(t, home) {
		check(other, home)
		check(home, other)
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
