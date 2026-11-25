package backend

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"textus/backend/plugins"
	"textus/backend/plugins/ssh"
)

func sshApp(t *testing.T) *App {
	t.Helper()
	a := pluginApp(t)
	if err := a.updatePlugins(func(s *plugins.State) {
		e := s.Entry("a.ssh")
		e.Enabled, e.Granted = true, []string{"ssh"}
		s.Entry("b.other").Enabled = true
	}); err != nil {
		t.Fatal(err)
	}
	return a
}

func TestAPluginReachesAHostOnlyOnceTheUserSaidYes(t *testing.T) {
	a := sshApp(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeText(t, filepath.Join(home, ".ssh/config"), "Host box\nHost *.lan\n")
	if got, err := run(a, "plugin_ssh_hosts", `{"id":"a.ssh"}`); err != nil || got != `["box"]` {
		t.Fatalf("hosts: %s %v", got, err)
	}
	if _, err := run(a, "plugin_ssh_hosts", `{"id":"b.other"}`); err == nil {
		t.Fatal("a plugin without ssh read the hosts")
	}
	if _, err := run(a, "plugin_ssh_connect", `{"id":"a.ssh","host":"box"}`); err == nil || err.Error() != sshNotAllowed {
		t.Fatalf("connect before yes: %v", err)
	}
	if _, err := run(a, "plugin_ssh_allow", `{"id":"a.ssh","host":"-oProxyCommand=x"}`); err == nil {
		t.Fatal("an option was allowed as a host")
	}
	for range 2 {
		if _, err := run(a, "plugin_ssh_allow", `{"id":"a.ssh","host":"box"}`); err != nil {
			t.Fatal(err)
		}
	}
	if got := a.sshAllowed().Plugins["a.ssh"]; len(got) != 1 || got[0] != "box" {
		t.Fatalf("allowed: %v", got)
	}
	if _, err := run(a, "plugin_ssh_connect", `{"id":"b.other","host":"box"}`); err == nil {
		t.Fatal("another plugin connected")
	}
	if _, err := run(a, "plugin_ssh_list", `{"id":"a.ssh","session":1,"path":"/"}`); err == nil ||
		!strings.Contains(err.Error(), "closed") {
		t.Fatalf("a link never opened: %v", err)
	}
}

// localSSHD is a user's own sshd on a free port in dir, with a client
// config naming it testbox (and stranger, whose key isn't known).
func localSSHD(t *testing.T, dir string) string {
	t.Helper()
	sshd := ""
	for _, p := range []string{"/usr/sbin/sshd", "/usr/bin/sshd"} {
		if _, err := os.Stat(p); err == nil {
			sshd = p
		}
	}
	if sshd == "" {
		t.Skip("no sshd here")
	}
	for _, name := range []string{"host_key", "client_key"} {
		if out, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", filepath.Join(dir, name)).CombinedOutput(); err != nil {
			t.Fatalf("ssh-keygen: %v %s", err, out)
		}
	}
	pub, err := os.ReadFile(filepath.Join(dir, "client_key.pub"))
	if err != nil {
		t.Fatal(err)
	}
	writeText(t, filepath.Join(dir, "authorized_keys"), string(pub))
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	writeText(t, filepath.Join(dir, "sshd_config"), fmt.Sprintf("Port %d\nListenAddress 127.0.0.1\nHostKey %[2]s/host_key\n"+
		"AuthorizedKeysFile %[2]s/authorized_keys\nPidFile %[2]s/sshd.pid\nUsePAM no\nStrictModes no\n"+
		"PasswordAuthentication no\nSubsystem sftp internal-sftp\n", port, dir))
	hostKey, err := os.ReadFile(filepath.Join(dir, "host_key.pub"))
	if err != nil {
		t.Fatal(err)
	}
	key := strings.Fields(string(hostKey))
	writeText(t, filepath.Join(dir, "known_hosts"), fmt.Sprintf("[127.0.0.1]:%d %s %s\n", port, key[0], key[1]))
	user := os.Getenv("USER")
	config := filepath.Join(dir, "ssh_config")
	writeText(t, config, fmt.Sprintf("Host testbox\n  HostName 127.0.0.1\n  Port %d\n  User %s\n  IdentityFile %[3]s/client_key\n"+
		"  IdentitiesOnly yes\n  UserKnownHostsFile %[3]s/known_hosts\n  StrictHostKeyChecking yes\n"+
		"Host stranger\n  HostName 127.0.0.1\n  Port %[1]d\n  User %[2]s\n  IdentityFile %[3]s/client_key\n"+
		"  UserKnownHostsFile %[3]s/none\n", port, user, dir))
	cmd := exec.Command(sshd, "-D", "-e", "-f", filepath.Join(dir, "sshd_config"))
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	time.Sleep(500 * time.Millisecond)
	return config
}

// Starts a local sshd: run with TEXTUS_SSHD_TEST=1.
func TestReadsAndWritesAFolderOnAServerAndNamesAnUnknownHost(t *testing.T) {
	if os.Getenv("TEXTUS_SSHD_TEST") == "" {
		t.Skip("starts a local sshd: TEXTUS_SSHD_TEST=1")
	}
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	config := localSSHD(t, dir)
	notes := filepath.Join(dir, "notes")
	writeText(t, filepath.Join(notes, "a.md"), "# A\n")
	a := sshApp(t)
	session, err := a.connectSSH("a.ssh", "testbox", config)
	if err != nil {
		t.Fatal(err)
	}
	at := func(id, path string) string {
		return fmt.Sprintf(`{"id":%q,"session":%d,"path":%s`, id, session, mustJSON(path))
	}
	listed, err := run(a, "plugin_ssh_list", at("a.ssh", notes)+`}`)
	if err != nil || listed != `[{"name":"a.md","folder":false}]` {
		t.Fatalf("list: %s %v", listed, err)
	}
	// Another plugin can't use the link.
	if _, err := run(a, "plugin_ssh_list", at("b.other", notes)+`}`); err == nil {
		t.Fatal("another plugin used the link")
	}
	b, c := notes+"/b.md", notes+"/c.md"
	if _, err := run(a, "plugin_ssh_create", at("a.ssh", b)+`,"folder":false}`); err != nil {
		t.Fatal(err)
	}
	if _, err := run(a, "plugin_ssh_create", at("a.ssh", b)+`,"folder":false}`); err == nil {
		t.Fatal("a file there was made again")
	}
	if _, err := run(a, "plugin_ssh_write", at("a.ssh", b)+`,"contents":"Hello","base64":false}`); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(filepath.Join(notes, "b.md")); string(got) != "Hello" {
		t.Fatalf("b.md = %q", got)
	}
	if got, err := run(a, "plugin_ssh_read", at("a.ssh", b)+`}`); err != nil || got != `"SGVsbG8="` {
		t.Fatalf("read: %s %v", got, err)
	}
	if _, err := run(a, "plugin_ssh_rename", fmt.Sprintf(`{"id":"a.ssh","session":%d,"from":%s,"to":%s}`, session, mustJSON(b), mustJSON(c))); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(notes, "b.md")); err == nil {
		t.Fatal("b.md is still there")
	}
	if _, err := run(a, "plugin_ssh_remove", at("a.ssh", c)+`}`); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(notes, "c.md")); err == nil {
		t.Fatal("c.md is still there")
	}
	if home, err := run(a, "plugin_ssh_home", at("a.ssh", "/")+`}`); err != nil || !strings.HasPrefix(home, `"/`) {
		t.Fatalf("home: %s %v", home, err)
	}
	if _, err := run(a, "plugin_ssh_list", at("a.ssh", "notes")+`}`); err == nil {
		t.Fatal("a relative path was listed")
	}
	if _, err := run(a, "plugin_ssh_close", `{"id":"a.ssh","session":null}`); err != nil {
		t.Fatal(err)
	}
	if _, err := run(a, "plugin_ssh_list", at("a.ssh", notes)+`}`); err == nil {
		t.Fatal("a closed link was used")
	}
	// A host whose key isn't known: refused, never accepted for the user.
	if _, err := a.connectSSH("a.ssh", "stranger", config); err == nil || !strings.HasPrefix(err.Error(), ssh.UnknownHost) {
		t.Fatalf("stranger: %v", err)
	}
}
