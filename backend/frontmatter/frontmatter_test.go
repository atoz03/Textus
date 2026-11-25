package frontmatter

import (
	"encoding/json/v2"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
)

// The cases the frontend's reader takes too.
func TestSplitsAsTheAppDoes(t *testing.T) {
	raw, err := os.ReadFile("../../src/utils/frontmatter-cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Name string  `json:"name"`
		Text string  `json:"text"`
		YAML *string `json:"yaml"`
		Body string  `json:"body"`
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		found, ok := Split(c.Text)
		if ok != (c.YAML != nil) {
			t.Errorf("%s: found %v", c.Name, ok)
			continue
		}
		if ok && (found.YAML() != *c.YAML || found.Body != c.Body) {
			t.Errorf("%s: %q, %q", c.Name, found.YAML(), found.Body)
		}
	}
}

func TestALinkedFileCountsOnlyWhereLinksAreTaken(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks")
	}
	dir, elsewhere := t.TempDir(), t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.md"), []byte("a"), 0o644)
	os.WriteFile(filepath.Join(elsewhere, "b.md"), []byte("b"), 0o644)
	os.Symlink(filepath.Join(elsewhere, "b.md"), filepath.Join(dir, "b.md"))
	os.Mkdir(filepath.Join(dir, "c.md"), 0o755)
	names := func(links bool) []string {
		var out []string
		for _, p := range MDFiles(dir, links) {
			out = append(out, filepath.Base(p))
		}
		return out
	}
	if got := names(true); !slices.Equal(got, []string{"a.md", "b.md"}) {
		t.Errorf("links: %v", got)
	}
	if got := names(false); !slices.Equal(got, []string{"a.md"}) {
		t.Errorf("no links: %v", got)
	}
}
