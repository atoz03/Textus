package backend

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"textus/backend/extract"
)

// Opening a Word document (.docx) as a new, untitled Markdown document;
// the .docx is only read. Pictures go to the drafts' image folder, named
// as pasted ones, so the first save moves them as it moves a paste.

// maxSourceBytes is the largest document opened or read whole.
const maxSourceBytes = 64 << 20

// WordDocument is what open_word_document hands the frontend
// (src/platform/bindings/files.ts WordDocument).
type WordDocument struct {
	Markdown string `json:"markdown"`
}

func convertWord(path, assets string, stamp int64) (string, error) {
	if fileExt(path) != "docx" {
		return "", codeError("wordOnly")
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if info.Size() > maxSourceBytes {
		return "", codeError("fileTooBig")
	}
	// One stamp for the whole document: image-<stamp>.png, image-<stamp>-1.jpeg...
	return extract.DocxDocument(path, func(name string, data []byte) (string, bool) {
		return savePicture(assets, stamp, name, data)
	})
}

// savePicture writes one picture of the document into assets and answers
// its src: an absolute path with forward slashes, since a backslash would
// escape in Markdown.
func savePicture(assets string, stamp int64, name string, data []byte) (string, bool) {
	ext := fileExt(name)
	if ext == "" || strings.ContainsFunc(ext, func(c rune) bool {
		return !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9')
	}) {
		return "", false
	}
	if err := os.MkdirAll(assets, 0o755); err != nil {
		return "", false
	}
	saved, err := writeNewFile(assets, fmt.Sprintf("image-%d.%s", stamp, ext), data, false)
	if err != nil {
		return "", false
	}
	return strings.ReplaceAll(filepath.Join(assets, saved), `\`, "/"), true
}

func init() {
	command("open_word_document", func(c *Call, a struct {
		Path string `json:"path"`
	}) (WordDocument, error) {
		if err := requireNonEmpty(a.Path); err != nil {
			return WordDocument{}, err
		}
		markdown, err := convertWord(a.Path, c.app.draftAssetsDir(), time.Now().UnixMilli())
		return WordDocument{Markdown: markdown}, err
	})
}
