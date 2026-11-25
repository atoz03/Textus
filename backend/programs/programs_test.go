package programs

import (
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestAProgramIsFoundOnThePathAndAPathIsTakenAsGiven(t *testing.T) {
	dir := t.TempDir()
	name := "tool"
	if runtime.GOOS == "windows" {
		name = "tool.exe"
	}
	tool := filepath.Join(dir, name)
	if err := os.WriteFile(tool, nil, 0o755); err != nil {
		t.Fatal(err)
	}
	if got, ok := Find("tool", dir); !ok || got != tool {
		t.Errorf("Find(tool) = %q %v", got, ok)
	}
	if _, ok := Find("missing", dir); ok {
		t.Error("a missing program was found")
	}
	if got, ok := Find(tool, ""); !ok || got != tool {
		t.Errorf("Find(path) = %q %v", got, ok)
	}
	if _, ok := Find(filepath.Join(dir, "nothing"), dir); ok {
		t.Error("a missing path was taken")
	}
}

func TestWindowsTakesNpxCmdOverTheShellScriptBesideIt(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows's extensions")
	}
	dir := t.TempDir()
	for _, name := range []string{"npx", "npx.cmd"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if got, _ := Find("npx", dir); got != filepath.Join(dir, "npx.cmd") {
		t.Errorf("Find(npx) = %q", got)
	}
}

func TestTheLoginPathKeepsThisProcesssPath(t *testing.T) {
	path := LoginPath()
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if dir != "" && !strings.Contains(string(filepath.ListSeparator)+path+string(filepath.ListSeparator), string(filepath.ListSeparator)+dir+string(filepath.ListSeparator)) {
			t.Errorf("%s is missing from %s", dir, path)
		}
	}
}

func TestATailKeepsTheEndOfWhatWasSaidByLine(t *testing.T) {
	var tail Tail
	_, _ = tail.Write([]byte("first\r\nsec"))
	_, _ = tail.Write([]byte("ond\n"))
	if got := tail.String(); got != "first\nsecond\n" {
		t.Fatalf("%q", got)
	}
	_, _ = tail.Write([]byte(strings.Repeat("é", 3000) + "\nlast"))
	tail.ended()
	got := tail.String()
	if len(got) > stderrKept || !strings.HasSuffix(got, "é\nlast\n") || !strings.HasPrefix(got, "é") {
		t.Fatalf("%d %q", len(got), got[:10])
	}
}

func TestAStartedProgramIsReadToItsEndAndItsCodeKept(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sh")
	}
	p, err := Start("sh", []string{"-c", `read line; echo "got $line"; echo "$TEXTUS_TEST" >&2; exit 3`}, [][2]string{{"TEXTUS_TEST", "said"}}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(p.Stdin, "hello\n"); err != nil {
		t.Fatal(err)
	}
	out, err := io.ReadAll(p.Stdout)
	if err != nil || string(out) != "got hello\n" {
		t.Fatalf("%q %v", out, err)
	}
	_ = p.Wait()
	if code := p.ExitCode(); code == nil || *code != 3 {
		t.Fatalf("code %v", code)
	}
	if got := p.Stderr.String(); got != "said\n" {
		t.Fatalf("stderr %q", got)
	}
	if _, err := Start("textus-no-such-program", nil, nil, ""); err == nil || !strings.Contains(err.Error(), "isn't installed") {
		t.Fatalf("%v", err)
	}
}
