package backend

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"textus/backend/plugins"
)

func TestAPluginsGitCallsTakeTheirArgumentsByName(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git isn't installed")
	}
	a := pluginApp(t)
	if err := a.updatePlugins(func(s *plugins.State) {
		e := s.Entry("a.b")
		e.Enabled, e.Granted = true, []string{"files.read", "git.commit"}
	}); err != nil {
		t.Fatal(err)
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"}, {"config", "user.email", "a@b.c"}, {"config", "user.name", "A"},
		{"config", "commit.gpgsign", "false"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	writeText(t, filepath.Join(root, "a.md"), "one\n")
	at := `"id":"a.b","root":` + string(mustJSON(root))
	file := string(mustJSON(filepath.Join(root, "a.md")))
	hash, err := run(a, "plugin_git_commit", `{`+at+`,"message":"First","paths":[`+file+`]}`)
	if err != nil || len(hash) != 42 {
		t.Fatalf("commit: %s %v", hash, err)
	}
	status, err := run(a, "plugin_git_status", `{`+at+`}`)
	if err != nil || !strings.Contains(status, `"branch":"main"`) || !strings.Contains(status, `"changes":[]`) {
		t.Fatalf("status: %s %v", status, err)
	}
	log, err := run(a, "plugin_git_log", `{`+at+`,"path":`+file+`,"limit":5}`)
	if err != nil || !strings.Contains(log, `"subject":"First"`) {
		t.Fatalf("log: %s %v", log, err)
	}
	shown, err := run(a, "plugin_git_show", `{`+at+`,"hash":`+hash+`,"path":`+file+`}`)
	if err != nil || shown != `"one\n"` {
		t.Fatalf("show: %s %v", shown, err)
	}
	if _, err := run(a, "plugin_git_fetch", `{`+at+`}`); err == nil || !strings.Contains(err.Error(), "git.sync") {
		t.Fatalf("fetch without git.sync: %v", err)
	}
	// Trusting writes the list in the app's data.
	if _, err := run(a, "git_trust_repository", `{"root":`+string(mustJSON(root))+`}`); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(a.gitTrusted()); err != nil {
		t.Fatalf("no trusted list: %v", err)
	}
	// Not a folder the app keeps.
	if _, err := run(a, "plugin_git_status", `{"id":"a.b","root":`+string(mustJSON(a.dirs.Data))+`}`); err == nil {
		t.Fatal("the app's own data was read")
	}
}
