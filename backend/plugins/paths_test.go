package plugins

import (
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func mustState(t *testing.T, raw string) State {
	t.Helper()
	s, err := ParseState([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func write(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestAnIDIsPublisherDotName(t *testing.T) {
	for _, ok := range []string{"catvinci.pdf-viewer", "a1.b2"} {
		if err := RequireID(ok); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
	for _, bad := range []string{"pdf", "Catvinci.pdf", "catvinci.", ".pdf", "catvinci.pdf.viewer", "../x.y", "a.b/c", "a.-b"} {
		if RequireID(bad) == nil {
			t.Errorf("%s is taken for an id", bad)
		}
	}
}

func TestAPathMustStayInTheFolder(t *testing.T) {
	tmp := t.TempDir()
	root := filepath.Join(tmp, "notes")
	write(t, filepath.Join(root, "sub/a.md"), "a")
	for _, ok := range []string{"sub/a.md", filepath.Join(root, "Daily/2026-10-21.md")} {
		if _, err := Inside(root, ok); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
	for _, out := range []string{"../secret", filepath.Join(tmp, "elsewhere.md")} {
		if _, err := Inside(root, out); err == nil {
			t.Errorf("%s is taken for inside", out)
		}
	}
}

func TestWindowsTakesARootedPathForTheDrivesNotTheFolders(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("drives are Windows'")
	}
	tmp := t.TempDir()
	root := filepath.Join(tmp, "notes")
	write(t, filepath.Join(root, "a.md"), "a")
	vol := filepath.VolumeName(tmp)
	// \a.md is at the drive's root; C:a.md is in the drive's current folder.
	for _, out := range []string{`\a.md`, "/a.md", vol + "a.md"} {
		if _, err := Inside(root, out); err == nil {
			t.Errorf("%s is taken for inside", out)
		}
	}
	// The folder's own file, named from the drive's root, is inside.
	rooted := strings.TrimPrefix(filepath.Join(root, "a.md"), vol)
	if got, err := Inside(root, rooted); err != nil || !strings.EqualFold(filepath.VolumeName(got), vol) {
		t.Errorf("%s: %s %v", rooted, got, err)
	}
}

func TestALinkOutOfTheFolderIsRefused(t *testing.T) {
	tmp := t.TempDir()
	root := filepath.Join(tmp, "notes")
	write(t, filepath.Join(tmp, "secret.txt"), "s")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(tmp, filepath.Join(root, "out")); err != nil {
		t.Skip("no links here:", err)
	}
	if _, err := Inside(root, "out/secret.txt"); err == nil {
		t.Error("a link out of the folder is followed")
	}
}

func TestHostsMatchTheirGrants(t *testing.T) {
	g := []string{"network:api.zotero.org", "network:*.wikipedia.org", "network:localhost:23119"}
	for _, c := range []struct {
		host string
		port int
		want bool
	}{
		{"api.zotero.org", 443, true},
		{"en.wikipedia.org", 443, true},
		{"wikipedia.org", 443, true},
		{"evil-wikipedia.org", 443, false},
		{"localhost", 23119, true},
		{"localhost", 8080, false},
		{"example.com", 443, false},
	} {
		if got := HostAllowed(g, c.host, c.port); got != c.want {
			t.Errorf("%s:%d: %v", c.host, c.port, got)
		}
	}
	if !HostAllowed([]string{"network:*"}, "example.com", 443) {
		t.Error("network:* doesn't cover a host")
	}
}

func TestPlainHTTPOnlyToThisComputer(t *testing.T) {
	g := []string{"network:*"}
	for raw, want := range map[string]bool{
		"https://example.com/x":      true,
		"http://example.com/x":       false,
		"http://localhost:23119/api": true,
		"http://127.0.0.1:23119/api": true,
		"file:///etc/passwd":         false,
	} {
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		if got := URLAllowed(g, u); got != want {
			t.Errorf("%s: %v", raw, got)
		}
	}
}

func TestAPluginReachesNoHiddenFileButTheFoldersOwnSettings(t *testing.T) {
	root := t.TempDir()
	for _, hidden := range []string{".git/hooks/post-commit", ".git/config", "notes/.vscode/tasks.json", ".env", ".obsidian/plugins/x/data.json"} {
		if _, err := InFolder(root, hidden); err == nil {
			t.Errorf("%s is open", hidden)
		}
	}
	for _, open := range []string{"notes/a.md", ".textus/sync.json", ".obsidian/app.json"} {
		if _, err := InFolder(root, open); err != nil {
			t.Errorf("%s: %v", open, err)
		}
	}
	for _, shut := range []string{".obsidian/workspace.json", ".obsidian/community-plugins.json", ".obsidian/plugins/x/main.js", "sub/.obsidian/app.json"} {
		if _, err := InFolder(root, shut); err == nil {
			t.Errorf("%s is open", shut)
		}
	}
	if err := os.MkdirAll(filepath.Join(root, ".textus/plugins/a.b"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := PluginPath(root, ".textus/plugins/a.b/x.json", "a.b"); err != nil {
		t.Errorf("a plugin's own storage: %v", err)
	}
	if _, err := PluginPath(root, ".textus/plugins/a.b/x.json", "c.d"); err == nil {
		t.Error("another plugin's storage is open")
	}
}

func TestThreeSecretFreeHiddenFilesAreReadableAndNothingElseInGit(t *testing.T) {
	for _, c := range [][2]string{{"/v", ".obsidian.vimrc"}, {"/v", "/v/.git/HEAD"}, {"/v", ".gitignore"}, {`C:\v`, `C:\v\.wakatime-project`}} {
		if !ReadOnlyHidden(c[0], c[1]) {
			t.Errorf("%s in %s is shut", c[1], c[0])
		}
	}
	for _, shut := range []string{".git/config", ".env", "sub/.git/HEAD"} {
		if ReadOnlyHidden("/v", shut) {
			t.Errorf("%s is open", shut)
		}
	}
}

func TestAHiddenFileGrantedByNameIsReadableAndOnlyThat(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, ".vitepress/config.ts"), "export default {}")
	write(t, filepath.Join(root, ".env"), "TOKEN=x")
	if err := os.Symlink(filepath.Join(root, ".env"), filepath.Join(root, ".vitepress/env.ts")); err != nil {
		t.Skip("no links here:", err)
	}
	state := mustState(t, `{"plugins": {"a.b": {"enabled": true, "granted": ["files.write", "hidden:.vitepress/config.ts", "hidden:.vitepress/env.ts"]}}}`)
	at := func(id, permission, path string) error {
		_, err := FolderPath(state, id, permission, root, path)
		return err
	}
	if err := at("a.b", "files.read", ".vitepress/config.ts"); err != nil {
		t.Errorf("the granted file: %v", err)
	}
	// Read only; not another plugin's grant; not through a link to .env.
	for _, c := range [][3]string{
		{"a.b", "files.write", ".vitepress/config.ts"},
		{"c.d", "files.read", ".vitepress/config.ts"},
		{"a.b", "files.read", ".vitepress/env.ts"},
		{"a.b", "files.read", ".env"},
	} {
		if at(c[0], c[1], c[2]) == nil {
			t.Errorf("%s may %s %s", c[0], c[1], c[2])
		}
	}
}

func TestNoPluginMayAskForAFileThatHoldsKeysOrTokens(t *testing.T) {
	for _, ok := range []string{".vitepress/config.ts", ".obsidian/app.json", ".github/workflows/ci.yml"} {
		if !HiddenFilePath(ok) {
			t.Errorf("%s may not be asked for", ok)
		}
	}
	for _, secret := range []string{
		".git/config", ".git-credentials", ".env.local", ".ssh/id_ed25519", ".config/gh/hosts.yml", ".cursor/mcp.json",
		".mcp.json", ".zsh_history", ".vault-token", ".terraformrc", ".aws/credentials", ".certs/site.pem",
		"notes/visible.md", ".a/../b",
	} {
		if HiddenFilePath(secret) {
			t.Errorf("%s may be asked for", secret)
		}
	}
}

func TestAPluginMayTrashAFileInItsFolderButNotTheFolder(t *testing.T) {
	tmp, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(tmp, "notes")
	write(t, filepath.Join(root, "sub/a.md"), "a")
	state := func(granted string) State {
		return mustState(t, `{"plugins": {"a.b": {"enabled": true, "granted": [`+granted+`]}}}`)
	}
	writer := state(`"files.write"`)
	if got, err := TrashPath(writer, "a.b", root, "sub/a.md"); err != nil || got != filepath.Join(root, "sub/a.md") {
		t.Errorf("a file: %q %v", got, err)
	}
	if _, err := TrashPath(writer, "a.b", root, "sub"); err != nil {
		t.Errorf("a folder in it: %v", err)
	}
	for _, shown := range []string{"", ".", root} {
		_, err := TrashPath(writer, "a.b", root, shown)
		if err == nil || !strings.HasSuffix(err.Error(), "trashShownFolder") {
			t.Errorf("%q: %v", shown, err)
		}
	}
	for _, c := range []struct {
		s    State
		path string
	}{{writer, ".."}, {state(`"files.read"`), "sub/a.md"}, {writer, ".env"}} {
		if _, err := TrashPath(c.s, "a.b", root, c.path); err == nil {
			t.Errorf("%s goes to the Trash", c.path)
		}
	}
}

func TestOnlyWhatTheManifestAsksForIsGranted(t *testing.T) {
	declared := DeclaredPermissions(`{"permissions": ["files.read", "network:api.example.com", "shell"]}`)
	if !slices.Equal(declared, []string{"files.read", "network:api.example.com"}) {
		t.Errorf("declared: %q", declared)
	}
	got := OnlyDeclared([]string{"files.read", "files.write", "shell", "network:api.example.com"}, declared)
	if !slices.Equal(got, []string{"files.read", "network:api.example.com"}) {
		t.Errorf("granted: %q", got)
	}
}

func TestACallIsAllowedOnlyOnARunningPluginWithTheGrant(t *testing.T) {
	state := func(allOn, enabled bool, granted ...string) State {
		return State{AllOn: allOn, Plugins: map[string]*Entry{"a.b": {Enabled: enabled, Granted: granted}}}
	}
	for _, c := range []struct {
		s        State
		id, perm string
		want     bool
	}{
		{state(true, true, "files.read"), "a.b", "files.read", true},
		{state(true, true, "files.read"), "a.b", "files.write", false},
		{state(true, false, "files.read"), "a.b", "files.read", false},
		{state(false, true, "files.read"), "a.b", "files.read", false},
		{state(true, true, "files.read"), "c.d", "files.read", false},
		// Writing includes reading, as the window's check has it.
		{state(true, true, "files.write"), "a.b", "files.read", true},
		{state(true, true, "files.write"), "a.b", "net", false},
	} {
		if got := c.s.Allows(c.id, c.perm); got != c.want {
			t.Errorf("%s %s: %v", c.id, c.perm, got)
		}
	}
}

func TestAMissingOrPartialFileReadsAsEverythingOff(t *testing.T) {
	empty := mustState(t, `{}`)
	if !empty.AllOn || len(empty.Plugins) != 0 {
		t.Errorf("an empty file: %+v", empty)
	}
	partial := mustState(t, `{"plugins":{"a.b":{"enabled":true}}}`)
	if !partial.Runs("a.b") || partial.Allows("a.b", "files.read") {
		t.Errorf("a partial file: %+v", partial)
	}
}
