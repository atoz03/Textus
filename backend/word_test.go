package backend

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// wordFile is a .docx of body (the inside of <w:body>) and more parts.
func wordFile(body string, parts map[string]string) []byte {
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	add := func(name, content string) {
		w, _ := z.Create(name)
		w.Write([]byte(content))
	}
	add("word/document.xml", `<w:document xmlns:w="w" xmlns:r="r"><w:body>`+body+`</w:body></w:document>`)
	for name, content := range parts {
		add(name, content)
	}
	z.Close()
	return b.Bytes()
}

func TestPicturesGetUniquePastedImageNamesInTheDraftsFolder(t *testing.T) {
	assets := filepath.Join(t.TempDir(), "assets")
	first, ok1 := savePicture(assets, 7, "image1.png", []byte("one"))
	second, ok2 := savePicture(assets, 7, "image2.PNG", []byte("two"))
	root := filepath.ToSlash(assets)
	if !ok1 || !ok2 || first != root+"/image-7.png" || second != root+"/image-7-1.png" {
		t.Error(first, second)
	}
	if data, _ := os.ReadFile(filepath.Join(assets, "image-7-1.png")); string(data) != "two" {
		t.Errorf("%q", data)
	}
	for _, name := range []string{"no-extension", "a.p ng", "a.png/"} {
		if src, ok := savePicture(assets, 7, name, []byte("x")); ok {
			t.Errorf("%s: %s", name, src)
		}
	}
}

func TestOnlyADocxIsConverted(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "notes.md")
	os.WriteFile(path, []byte("# Notes"), 0o644)
	if _, err := convertWord(path, dir, 1); err == nil || !strings.Contains(err.Error(), "wordOnly") {
		t.Error(err)
	}
}

// Opening a Word document never touches it: converted, the .docx is as it
// was, byte for byte and to its time, and its picture went to the drafts.
func TestAWordDocumentBecomesMarkdownAndIsLeftAsItWas(t *testing.T) {
	root := t.TempDir()
	docx := filepath.Join(root, "docs", "Report.docx")
	assets := filepath.Join(root, "app-data", "assets")
	os.MkdirAll(filepath.Dir(docx), 0o755)
	body := `<w:p><w:r><w:t>Price: $5 *each*</w:t></w:r></w:p>` +
		`<w:p><w:r><w:drawing><wp:inline xmlns:wp="wp"><wp:docPr id="1" descr="Chart"/><a:blip xmlns:a="a" r:embed="rId4"/></wp:inline></w:drawing></w:r></w:p>`
	data := wordFile(body, map[string]string{
		"word/_rels/document.xml.rels": `<Relationships><Relationship Id="rId4" Type="image" Target="media/image1.png"/></Relationships>`,
		"word/media/image1.png":        "PNG",
	})
	os.WriteFile(docx, data, 0o644)
	old := time.Now().Add(-time.Hour).Truncate(time.Second)
	os.Chtimes(docx, old, old)

	markdown, err := convertWord(docx, assets, 9)
	if err != nil {
		t.Fatal(err)
	}
	picture := filepath.ToSlash(filepath.Join(assets, "image-9.png"))
	if markdown != "Price: \\$5 \\*each\\*\n\n![Chart]("+picture+")" {
		t.Errorf("%q", markdown)
	}
	held, _ := os.ReadFile(docx)
	info, _ := os.Stat(docx)
	if !bytes.Equal(held, data) || !info.ModTime().Equal(old) {
		t.Error("the .docx changed")
	}
	if entries, _ := os.ReadDir(filepath.Dir(docx)); len(entries) != 1 {
		t.Errorf("%d files beside the .docx", len(entries))
	}
	if saved, _ := os.ReadFile(filepath.Join(assets, "image-9.png")); string(saved) != "PNG" {
		t.Errorf("%q", saved)
	}
}
