package ssh

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestTakesHostNamesNeverOptions(t *testing.T) {
	for host, want := range map[string]bool{
		"box-1.lan": true, "me@[::1]": true, "-oProxyCommand=x": false, "a b": false, "": false,
		strings.Repeat("a", 256): false,
	} {
		if ValidHost(host) != want {
			t.Errorf("ValidHost(%q) = %v", host, !want)
		}
	}
}

func TestNamesWhyALinkFailed(t *testing.T) {
	if got := reason("Host key verification failed.\r\n"); !strings.HasPrefix(got, UnknownHost) {
		t.Errorf("%q", got)
	}
	if got := reason("me@box: Permission denied (publickey).\n"); !strings.HasPrefix(got, NeedsLogin) {
		t.Errorf("%q", got)
	}
	if got := reason(""); got != "The connection closed." {
		t.Errorf("%q", got)
	}
	if got := reason("first\n  ssh: Could not resolve hostname x  \n\n"); got != "ssh: Could not resolve hostname x" {
		t.Errorf("%q", got)
	}
}

func TestAServerPathIsAbsoluteAndWhole(t *testing.T) {
	if _, err := Remote("/home/me/notes"); err != nil {
		t.Error(err)
	}
	for _, bad := range []string{"notes", "", "/a\x00b"} {
		if _, err := Remote(bad); err == nil {
			t.Errorf("%q was taken", bad)
		}
	}
}

func TestReadsNamedHostsThroughIncludes(t *testing.T) {
	ssh := filepath.Join(t.TempDir(), ".ssh")
	if err := os.MkdirAll(filepath.Join(ssh, "conf.d"), 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(rel, text string) {
		if err := os.WriteFile(filepath.Join(ssh, rel), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("config", "# mine\nHost work box-1\n  HostName 10.0.0.1\nHost *.lan !x\nInclude conf.d/*.conf\nHost=gpu\n")
	write("conf.d/a.conf", "Host lab\nHost -oProxyCommand=x\n")
	write("conf.d/b.txt", "Host skipped\n")
	if got := HostsIn(filepath.Join(ssh, "config"), ssh); !slices.Equal(got, []string{"box-1", "gpu", "lab", "work"}) {
		t.Fatalf("%v", got)
	}
	if got := HostsIn(filepath.Join(ssh, "none"), ssh); got == nil || len(got) != 0 {
		t.Fatalf("%v", got)
	}
}

func TestAPatternMatchesAnyRunOfCharacters(t *testing.T) {
	for _, c := range []struct {
		name, pattern string
		want          bool
	}{
		{"a.conf", "*.conf", true}, {"a.conf.bak", "*.conf", false}, {"x-1-y", "x*1*y", true},
		{"xy", "x*y", true}, {"x", "x*x", false}, {"anything", "*", true},
	} {
		if got := matches(c.name, strings.Split(c.pattern, "*")); got != c.want {
			t.Errorf("matches(%q, %q) = %v", c.name, c.pattern, got)
		}
	}
}
