package backend

import (
	"encoding/json/v2"
	"os"
	"path/filepath"
	"testing"
)

func touch(t *testing.T, root, rel string) string {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func freshIndex(root string, _ bool) []string {
	files, _ := indexVault(root, vaultMaxFiles, vaultMaxEntries)
	return files
}

// The cases the frontend's resolver is held to as well
// (src/files/link-resolve.cases.json).
func TestLinksResolveAsTheSharedCaseTableSays(t *testing.T) {
	raw, err := os.ReadFile("../src/files/link-resolve.cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var table struct {
		Files []string `json:"files"`
		Cases []struct {
			Name   string  `json:"name"`
			From   string  `json:"from"`
			Note   string  `json:"note"`
			Wiki   bool    `json:"wiki"`
			Expect *string `json:"expect"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(raw, &table); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".obsidian"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range table.Files {
		touch(t, dir, f)
	}
	for _, c := range table.Cases {
		found, ok := resolveWith(filepath.Join(dir, filepath.FromSlash(c.From)), "", c.Note, linkNote, c.Wiki, freshIndex)
		want := ""
		if c.Expect != nil {
			want = filepath.Join(dir, filepath.FromSlash(*c.Expect))
		}
		if found != want || ok != (c.Expect != nil) {
			t.Errorf("%s: got %q, want %q", c.Name, found, want)
		}
	}
}

func TestImageLinksStayInTheVault(t *testing.T) {
	loadImageTypes([]byte(`{"png":"image/png","gif":"image/gif"}`))
	dir := t.TempDir()
	vault := filepath.Join(dir, "vault")
	touch(t, vault, ".obsidian/app.json")
	doc := touch(t, vault, "notes/doc.md")
	near := touch(t, vault, "notes/x.png")
	touch(t, vault, "far/away/x.png")
	gif := touch(t, vault, "attachments/x.gif")
	touch(t, dir, "secret.png")
	touch(t, vault, "notes/Other.md")

	cases := []struct {
		link string
		want string
	}{
		{"x.png", near},
		{"attachments/x.gif", gif},
		{"X.PNG", near},
		{"../../secret.png", ""},
		{"notes/../../secret.png", ""},
		{filepath.Join(dir, "secret.png")[1:], ""},
		{"nope.png", ""},
		{"Other.md", ""}, // a note embed is not a picture
	}
	for _, c := range cases {
		got, _ := resolveWith(doc, "", c.link, linkImage, true, freshIndex)
		if got != c.want {
			t.Errorf("%q: got %q, want %q", c.link, got, c.want)
		}
	}

	// A symlink in the vault that leads out of it is not followed.
	if err := os.Symlink(filepath.Join(dir, "secret.png"), filepath.Join(vault, "notes", "link.png")); err == nil {
		if got, ok := resolveWith(doc, "", "link.png", linkImage, true, freshIndex); ok {
			t.Errorf("followed a link out of the vault to %q", got)
		}
	}
}

func TestANewNoteGoesWhereTheVaultPutsThem(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, ".obsidian/app.json")
	doc := touch(t, dir, "notes/doc.md")
	cases := []struct {
		link string
		wiki bool
		want string
	}{
		{"Idea", true, filepath.Join(dir, "Idea.md")},
		{"sub/Idea", true, filepath.Join(dir, "sub", "Idea.md")},
		{"Idea", false, filepath.Join(dir, "notes", "Idea.md")},
		{"../../out", true, ""},
	}
	for _, c := range cases {
		got, _ := newNotePath(doc, "", c.link, c.wiki)
		if got != c.want {
			t.Errorf("%q (wiki %v): got %q, want %q", c.link, c.wiki, got, c.want)
		}
	}
}

func TestTheVaultIsTheNearestFolderWithDotObsidian(t *testing.T) {
	dir := t.TempDir()
	vault := filepath.Join(dir, "vault")
	touch(t, vault, ".obsidian/app.json")
	doc := touch(t, vault, "deep/er/doc.md")
	if got := vaultRoot(doc, ""); got != vault {
		t.Errorf("vault root %q, want %q", got, vault)
	}
	plain := touch(t, dir, "plain/notes/doc.md")
	if got := vaultRoot(plain, ""); got != filepath.Dir(plain) {
		t.Errorf("without .obsidian: %q", got)
	}
	if got := vaultRoot(plain, filepath.Join(dir, "plain")); got != filepath.Join(dir, "plain") {
		t.Errorf("with an open folder: %q", got)
	}
}

func TestHiddenFoldersAndNodeModulesAreNotIndexed(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, "a.md")
	touch(t, dir, ".git/b.md")
	touch(t, dir, "node_modules/c.md")
	touch(t, dir, "sub/.hidden.md")
	touch(t, dir, "sub/d.md")
	files, cut := indexVault(dir, vaultMaxFiles, vaultMaxEntries)
	if cut || len(files) != 2 || !containsString(files, "a.md") || !containsString(files, "sub/d.md") {
		t.Errorf("indexed %v (cut %v)", files, cut)
	}
	if _, cut := indexVault(dir, 1, vaultMaxEntries); !cut {
		t.Error("an index stopped at its limit does not say so")
	}
}

func TestFrontMatterValue(t *testing.T) {
	dir := t.TempDir()
	write := func(name, text string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	doc := write("a.md", "\uFEFF---\ntitle: x\ntypora-root-url: \"../assets\"\n---\nbody\n")
	if v, ok := frontMatterValue(doc, "typora-root-url"); !ok || v != "../assets" {
		t.Errorf("got %q %v", v, ok)
	}
	open := write("b.md", "---\ntypora-root-url: ../assets\nno end")
	if _, ok := frontMatterValue(open, "typora-root-url"); ok {
		t.Error("a front matter that never closes has no values")
	}
}
