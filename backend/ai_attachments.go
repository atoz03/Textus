package backend

import (
	"archive/zip"
	"encoding/base64"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/egoist/mygo"
)

// Files attached to a chat: copied into the conversation's scratch folder
// (`attached/`), where the agent's file tools read them, and turned into
// what the message carries - text, or a picture.

// maxExtractedChars is how much extracted text may enter the prompt: a
// 300-page PDF would otherwise evict the document itself from the context
// window.
const maxExtractedChars = 60_000

// The most an attached archive may unpack to, in files and bytes: a zip
// bomb stops here rather than filling the disk.
const (
	maxArchiveFiles = 2000
	maxArchiveBytes = 512 << 20
)

// attachableExtensions are the kinds the picker lists first; any other
// file can be attached too and is read as text when it is text.
var attachableExtensions = []string{
	"md", "markdown", "mdx", "txt", "text", "csv", "tsv", "json", "yaml", "yml", "svg", "pdf",
	"docx", "pptx", "xlsx", "xlsm", "xlsb", "xls", "ods", "epub", "rtf", "html", "htm", "xhtml",
	"zip", "png", "jpg", "jpeg", "gif", "webp", "bmp", "tif", "tiff", "ico", "heic", "heif",
	"avif",
}

// chatAttachment is a chat attachment: whatever the user picked, turned
// into something a model can read (src/platform/bindings/ai-agent.ts
// ChatAttachment). Image is set for a picture, else it is text.
type chatAttachment struct {
	Name string
	// Content is the extracted text, inlined into the prompt by the
	// frontend; Truncated: it hit maxExtractedChars and was cut, which the
	// chip says.
	Content   string
	Truncated bool
	// Mime and DataBase64 are a picture's.
	Image            bool
	Mime, DataBase64 string
	// Path is where read_file finds the whole file: `attached/<name>`.
	Path string
}

func (a chatAttachment) MarshalJSON() ([]byte, error) {
	if a.Image {
		return json.Marshal(struct {
			Kind       string `json:"kind"`
			Name       string `json:"name"`
			Mime       string `json:"mime"`
			DataBase64 string `json:"dataBase64"`
			Path       string `json:"path"`
		}{"image", a.Name, a.Mime, a.DataBase64, a.Path})
	}
	return json.Marshal(struct {
		Kind      string `json:"kind"`
		Name      string `json:"name"`
		Content   string `json:"content"`
		Truncated bool   `json:"truncated"`
		Path      string `json:"path"`
	}{"text", a.Name, a.Content, a.Truncated, a.Path})
}

// textAttachment is text, cut to maxExtractedChars, on a character
// boundary so CJK text isn't cut mid-character.
func textAttachment(name, text, path string) chatAttachment {
	chars := 0
	for at := range text {
		if chars == maxExtractedChars {
			text = text[:at] + "\n\n[... truncated: the whole file is at the path above - read_file reads the rest]"
			return chatAttachment{Name: name, Content: text, Truncated: true, Path: path}
		}
		chars++
	}
	return chatAttachment{Name: name, Content: text, Path: path}
}

// fileNameOf is the last part of path, which names the attached copy.
func fileNameOf(path string) string {
	name := filepath.Base(strings.ReplaceAll(path, `\`, "/"))
	if name == "." || name == "/" || name == "" {
		return "pasted"
	}
	return name
}

func withinAttachLimit(size int64) error {
	if size > maxSourceBytes {
		return fmt.Errorf("That file is too large to attach (%d MB; the limit is %d MB).", size>>20, maxSourceBytes>>20)
	}
	return nil
}

// stage copies one picked file into attached and reads it.
func stage(source, attached string) (chatAttachment, error) {
	info, err := os.Stat(source)
	if err != nil {
		return chatAttachment{}, err
	}
	if err := withinAttachLimit(info.Size()); err != nil {
		return chatAttachment{}, err
	}
	name := freeName(attached, fileNameOf(source), numberedParen)
	copied := filepath.Join(attached, name)
	if err := copyFile(source, copied); err != nil {
		return chatAttachment{}, err
	}
	return readStaged(copied, name, attached)
}

// stageBytes writes pasted bytes into attached as name and reads them.
func stageBytes(name string, data []byte, attached string) (chatAttachment, error) {
	if err := withinAttachLimit(int64(len(data))); err != nil {
		return chatAttachment{}, err
	}
	name = freeName(attached, fileNameOf(name), numberedParen)
	copied := filepath.Join(attached, name)
	if err := os.WriteFile(copied, data, 0o644); err != nil {
		return chatAttachment{}, err
	}
	return readStaged(copied, name, attached)
}

// readStaged is a file staged in attached as name, turned into what the
// message carries.
func readStaged(copied, name, attached string) (chatAttachment, error) {
	path := attachedFolder + "/" + name
	ext := fileExt(copied)
	if slices.Contains(attachedPictureExtensions, ext) {
		mime, data, err := sentPicture(copied, ext)
		if err != nil {
			return chatAttachment{}, err
		}
		return chatAttachment{Name: name, Image: true, Mime: mime, DataBase64: base64.StdEncoding.EncodeToString(data), Path: path}, nil
	}
	if ext == "zip" {
		folder := freeName(attached, strings.TrimSuffix(name, ".zip"), numberedParen)
		files, err := unpackArchive(copied, filepath.Join(attached, folder))
		if err != nil {
			return chatAttachment{}, err
		}
		var listing []string
		for _, f := range files {
			listing = append(listing, fmt.Sprintf("- %s/%s/%s", attachedFolder, folder, f))
		}
		content := fmt.Sprintf("A zip archive, unpacked to `%s/%s/` (%d files) - read_file reads each:\n%s", attachedFolder, folder, len(files), strings.Join(listing, "\n"))
		return textAttachment(name, content, path), nil
	}
	var text string
	if slices.Contains(documentExtensions, ext) {
		var err error
		if text, err = documentText(copied); err != nil {
			return chatAttachment{}, err
		}
	} else {
		raw, err := os.ReadFile(copied)
		if err != nil || !utf8.Valid(raw) {
			return chatAttachment{}, fmt.Errorf("`%s` isn't a text file, and Textus can't read this format yet.", name)
		}
		text = string(raw)
	}
	if strings.TrimSpace(text) == "" {
		return chatAttachment{}, fmt.Errorf("`%s` has no readable text.", name)
	}
	return textAttachment(name, text, path), nil
}

// unpackArchive unpacks archive into to, and answers its files, relative
// to to. Links, folders and what macOS adds (`__MACOSX`) are left out; a
// name that would land outside to is skipped.
func unpackArchive(archive, to string) ([]string, error) {
	r, err := zip.OpenReader(archive)
	if err != nil && !errors.Is(err, zip.ErrInsecurePath) {
		return nil, errors.New("That archive isn't a zip file Textus can open.")
	}
	defer r.Close()
	var files []string
	var total int64
	for _, entry := range r.File {
		mode := entry.Mode()
		if mode.IsDir() || mode&os.ModeSymlink != 0 {
			continue
		}
		rel, ok := enclosedName(entry.Name)
		if !ok || slices.Contains(strings.Split(rel, "/"), "__MACOSX") {
			continue
		}
		if len(files) == maxArchiveFiles {
			return nil, fmt.Errorf("That archive holds more than %d files.", maxArchiveFiles)
		}
		target := filepath.Join(to, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return nil, err
		}
		in, err := entry.Open()
		if err != nil {
			return nil, err
		}
		data, err := io.ReadAll(io.LimitReader(in, maxArchiveBytes+1-total))
		in.Close()
		if err != nil {
			return nil, err
		}
		if total += int64(len(data)); total > maxArchiveBytes {
			return nil, codeError("archiveTooLarge")
		}
		if err := os.WriteFile(target, data, 0o644); err != nil {
			return nil, err
		}
		files = append(files, rel)
	}
	return files, nil
}

// pasted is what was pasted into the composer: a copied picture's bytes,
// or a file copied in a file manager, by its path
// (src/platform/bindings/ai-agent.ts Pasted).
type pasted struct {
	Kind       string `json:"kind"`
	Path       string `json:"path"`
	Name       string `json:"name"`
	DataBase64 string `json:"dataBase64"`
}

func init() {
	// The chat composer's "+" button: pick files, then copy and read each
	// into the conversation's attachments. Empty when the picker was
	// cancelled; a file that can't be read fails the whole pick, naming it.
	command("pick_attachment_files", func(c *Call, a struct {
		Conversation string `json:"conversation"`
	}) ([]chatAttachment, error) {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: c.parent(), Multiple: true, Filters: []mygo.FileFilter{
			{Name: "Documents, archives and pictures", Extensions: attachableExtensions},
			{Name: "All files", Extensions: []string{"*"}},
		}})
		if err != nil || len(paths) == 0 {
			return []chatAttachment{}, err
		}
		attached, err := attachedDir(a.Conversation)
		if err != nil {
			return nil, err
		}
		out := []chatAttachment{}
		for _, path := range paths {
			staged, err := stage(path, attached)
			if err != nil {
				return nil, err
			}
			out = append(out, staged)
		}
		return out, nil
	})
	// The chat composer's paste: each pasted picture or file copied and
	// read into the conversation's attachments, as the "+" button's are.
	command("attach_pasted", func(c *Call, a struct {
		Conversation string   `json:"conversation"`
		Pasted       []pasted `json:"pasted"`
	}) ([]chatAttachment, error) {
		attached, err := attachedDir(a.Conversation)
		if err != nil {
			return nil, err
		}
		out := []chatAttachment{}
		for _, item := range a.Pasted {
			var staged chatAttachment
			if item.Kind == "path" {
				staged, err = stage(item.Path, attached)
			} else {
				var data []byte
				if data, err = base64.StdEncoding.DecodeString(item.DataBase64); err == nil {
					staged, err = stageBytes(item.Name, data, attached)
				}
			}
			if err != nil {
				return nil, err
			}
			out = append(out, staged)
		}
		return out, nil
	})
}
