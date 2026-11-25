package backend

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/egoist/mygo"
)

// The schemes pages load from besides the app's own: asset:// serves a
// file of the disk to an <img> or <video> (src/platform/file-src.ts), and
// textus-plugin:// a plugin's own files to its frames (plugin_menu.go).

// assetDenied keeps the files that hold secrets out of the asset scheme,
// whatever a page asks for: the app's own settings and keys, and the
// usual places of the user's credentials.
func (a *App) assetDenied(path string) bool {
	fold := func(p string) string {
		if runtime.GOOS == "darwin" || runtime.GOOS == "windows" {
			return strings.ToLower(p)
		}
		return p
	}
	p := fold(filepath.Clean(path))
	if dir := a.dirs.Config; dir != "" {
		if filepath.Dir(p) == fold(filepath.Clean(dir)) && strings.EqualFold(filepath.Ext(p), ".json") {
			return true
		}
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return false
	}
	home = fold(filepath.Clean(home))
	for _, dir := range []string{".ssh", ".gnupg", ".aws", ".azure", ".kube", ".docker", ".config/gcloud", ".config/gh", "Library/Keychains"} {
		if _, ok := pathUnder(p, filepath.Join(home, fold(filepath.FromSlash(dir)))); ok {
			return true
		}
	}
	for _, file := range []string{".netrc", ".npmrc", ".git-credentials"} {
		if p == filepath.Join(home, file) {
			return true
		}
	}
	return false
}

// assetPath is the file an asset URL names: its path, percent-encoded
// whole (convertFileSrc).
func assetPath(r *http.Request) (string, bool) {
	raw := strings.TrimPrefix(r.URL.EscapedPath(), "/")
	path, err := url.PathUnescape(raw)
	if err != nil || path == "" {
		return "", false
	}
	if !filepath.IsAbs(path) {
		return "", false
	}
	return filepath.Clean(path), true
}

func (a *App) serveAsset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	path, ok := assetPath(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	// The file a link leads to is the one checked: a link in a document's
	// folder can lead anywhere.
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if a.assetDenied(path) || a.assetDenied(real) || isSecretFile(real) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	f, err := os.Open(real)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || info.IsDir() {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "no-cache")
	http.ServeContent(w, r, filepath.Base(real), info.ModTime(), f)
}

// isSecretFile: one of the app's files that hold keys and tokens.
func isSecretFile(path string) bool {
	return containsString(secretFiles, filepath.Base(path))
}

func registerSchemes(a *App) {
	if err := mygo.Protocol.HandleFunc("asset", a.serveAsset); err != nil {
		panic(err)
	}
	if err := mygo.Protocol.HandleFunc("textus-plugin", a.servePluginFile); err != nil {
		panic(err)
	}
}
