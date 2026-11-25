package sftp

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
)

// server is OpenSSH's sftp-server on this computer, spoken to through its
// pipes as ssh would.
func server(t *testing.T) *Client {
	t.Helper()
	var program string
	for _, p := range []string{"/usr/lib/openssh/sftp-server", "/usr/libexec/openssh/sftp-server", "/usr/libexec/sftp-server", "/usr/lib/ssh/sftp-server"} {
		if _, err := os.Stat(p); err == nil {
			program = p
			break
		}
	}
	if program == "" {
		t.Skip("no sftp-server here")
	}
	cmd := exec.Command(program)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	c, err := New(stdout, stdin)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = c.Close()
		_ = cmd.Wait()
	})
	return c
}

func TestAFolderIsListedReadAndWrittenOverSFTP(t *testing.T) {
	c := server(t)
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.md"), []byte("# A\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if real, err := c.RealPath(dir + "/sub/.."); err != nil || real != dir {
		t.Fatalf("realpath = %q %v", real, err)
	}
	entries, err := c.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var seen []string
	for _, e := range entries {
		if e.Name != "." && e.Name != ".." {
			seen = append(seen, e.Name+map[bool]string{true: "/", false: ""}[e.Attrs.IsDir()])
		}
	}
	slices.Sort(seen)
	if !slices.Equal(seen, []string{"a.md", "sub/"}) {
		t.Fatalf("listed %v", seen)
	}

	// A file bigger than many pieces, and one that ends mid-piece.
	big := bytes.Repeat([]byte("0123456789abcdef"), 70_001)
	b := dir + "/b.bin"
	if err := c.WriteFile(b, big); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(b); !bytes.Equal(got, big) {
		t.Fatalf("wrote %d bytes, want %d", len(got), len(big))
	}
	if got, err := c.ReadFile(b, len(big)); err != nil || !bytes.Equal(got, big) {
		t.Fatalf("read %d bytes, %v", len(got), err)
	}
	if _, err := c.ReadFile(b, len(big)-1); err == nil {
		t.Fatal("a file past the limit was read")
	}
	// Written again, shorter: the rest is gone.
	if err := c.WriteFile(b, []byte("short")); err != nil {
		t.Fatal(err)
	}
	if got, err := c.ReadFile(b, 100); err != nil || string(got) != "short" {
		t.Fatalf("%q %v", got, err)
	}
	if got, err := c.ReadFile(dir+"/a.md", 100); err != nil || string(got) != "# A\n" {
		t.Fatalf("%q %v", got, err)
	}
	if err := c.WriteFile(dir+"/empty", nil); err != nil {
		t.Fatal(err)
	}
	if got, err := c.ReadFile(dir+"/empty", 100); err != nil || len(got) != 0 {
		t.Fatalf("%q %v", got, err)
	}

	attrs, err := c.Stat(b)
	if err != nil || attrs.Size == nil || *attrs.Size != 5 || attrs.IsDir() {
		t.Fatalf("stat %+v %v", attrs, err)
	}
	if ok, err := c.Exists(dir + "/none"); ok || err != nil {
		t.Fatalf("exists none: %v %v", ok, err)
	}
	if _, err := c.Stat(dir + "/none"); !NotFound(err) {
		t.Fatalf("stat none: %v", err)
	}
	if err := c.Create(dir + "/c.md"); err != nil {
		t.Fatal(err)
	}
	if err := c.Create(dir + "/c.md"); err == nil {
		t.Fatal("a file there was made again")
	}
	if err := c.Rename(dir+"/c.md", dir+"/d.md"); err != nil {
		t.Fatal(err)
	}
	if err := c.Mkdir(dir + "/e"); err != nil {
		t.Fatal(err)
	}
	if attrs, err := c.Stat(dir + "/e"); err != nil || !attrs.IsDir() {
		t.Fatalf("stat e: %+v %v", attrs, err)
	}
	if err := c.Rmdir(dir + "/e"); err != nil {
		t.Fatal(err)
	}
	if err := c.Remove(dir + "/d.md"); err != nil {
		t.Fatal(err)
	}
	for _, gone := range []string{"c.md", "d.md", "e"} {
		if _, err := os.Stat(filepath.Join(dir, gone)); err == nil {
			t.Errorf("%s is still there", gone)
		}
	}
	if err := c.Rmdir(dir + "/sub/none"); err == nil {
		t.Fatal("a missing folder was removed")
	}
}

func TestALinkThatEndsFailsWhatIsAsked(t *testing.T) {
	c := server(t)
	_ = c.Close()
	<-c.Done()
	if _, err := c.RealPath("."); err != ErrClosed {
		t.Fatalf("%v", err)
	}
}

func TestAStreamThatIsntSFTPIsRefused(t *testing.T) {
	r := bytes.NewReader([]byte("Welcome to my server!\n"))
	if _, err := New(r, nopWriter{io.Discard}); err == nil {
		t.Fatal("a greeting was taken for SFTP")
	}
}

type nopWriter struct{ io.Writer }

func (nopWriter) Close() error { return nil }
