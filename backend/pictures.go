package backend

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	_ "golang.org/x/image/bmp"
	_ "golang.org/x/image/tiff"
	_ "golang.org/x/image/webp"
)

// Pictures and files kept beside a document: where they go, the names
// they are written under, and the size a picture is drawn at.

// SavedImage is a picture or file written next to a document.
type SavedImage struct {
	// Src goes into the Markdown: relative to the document, or absolute for
	// a draft that has no folder yet.
	Src   string  `json:"src"`
	Embed *string `json:"embed"`
	// Path is the file written where it is the user's; nil for a draft's.
	Path *string `json:"path"`
}

type ImageMigration struct {
	Old string  `json:"old"`
	New *string `json:"new"`
}

type PictureSize struct {
	Width  uint32 `json:"width"`
	Height uint32 `json:"height"`
}

var pictureExtensions = []string{"png", "jpg", "jpeg", "gif", "webp", "bmp", "svg", "tif", "tiff", "avif", "ico", "heic", "heif"}

func isPicture(path string) bool { return slices.Contains(pictureExtensions, fileExt(path)) }

// imageFolder is where a document's pictures go, from the setting the
// frontend sends: "./" or a folder below it, never "..", nothing absolute.
type imageFolder struct {
	dir       string
	srcPrefix string // as the Markdown names it, "" for the document's own folder
}

func resolveImageFolder(docPath, setting string) (imageFolder, error) {
	invalid := fmt.Errorf("invalid image folder: %q", setting)
	setting = strings.TrimSpace(setting)
	if setting == "" || filepath.IsAbs(setting) || strings.HasPrefix(setting, "/") || strings.HasPrefix(setting, `\`) ||
		(runtime.GOOS == "windows" && filepath.VolumeName(setting) != "") {
		return imageFolder{}, invalid
	}
	segments := strings.FieldsFunc(setting, func(r rune) bool { return r == '/' || r == '\\' })
	if strings.HasSuffix(setting, "/") || strings.HasSuffix(setting, `\`) {
		segments = append(segments, "")
	}
	if strings.Contains(setting, "//") || strings.Contains(setting, `\\`) {
		return imageFolder{}, invalid
	}
	var parts []string
	for i, s := range segments {
		switch {
		case s == "" && i > 0 && i == len(segments)-1:
		case s == "" || s == "..":
			return imageFolder{}, invalid
		case s == ".":
		default:
			parts = append(parts, s)
		}
	}
	return imageFolder{
		dir:       filepath.Join(append([]string{filepath.Dir(docPath)}, parts...)...),
		srcPrefix: strings.Join(parts, "/"),
	}, nil
}

func (f imageFolder) src(name string) string {
	if f.srcPrefix == "" {
		return markdownPath(name)
	}
	return markdownPath(f.srcPrefix + "/" + name)
}

// markdownPath escapes what would end or break a Markdown link.
func markdownPath(path string) string {
	r := strings.NewReplacer("%", "%25", " ", "%20", "(", "%28", ")", "%29", "#", "%23")
	return r.Replace(path)
}

func (a *App) draftAssetsDir() string { return filepath.Join(a.dirs.Data, "assets") }

func copyImage(from, destDir string) *string {
	data, err := os.ReadFile(from)
	if err != nil {
		return nil
	}
	if os.MkdirAll(destDir, 0o755) != nil {
		return nil
	}
	name, err := writeNewFile(destDir, filepath.Base(from), data, true)
	if err != nil {
		return nil
	}
	return &name
}

// pictureSize reads a picture's size from its header.
func pictureSize(path string) *PictureSize {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	if fileExt(path) == "svg" {
		return svgSize(f)
	}
	cfg, _, err := image.DecodeConfig(f)
	if err != nil || cfg.Width <= 0 || cfg.Height <= 0 {
		return nil
	}
	return &PictureSize{uint32(cfg.Width), uint32(cfg.Height)}
}

// svgSize reads an SVG's width and height, or its viewBox.
func svgSize(r io.Reader) *PictureSize {
	head := make([]byte, 64*1024)
	n, _ := io.ReadFull(r, head)
	tag := head[:n]
	start := bytes.Index(tag, []byte("<svg"))
	if start < 0 {
		return nil
	}
	end := bytes.IndexByte(tag[start:], '>')
	if end < 0 {
		return nil
	}
	tag = tag[start : start+end+1]
	attr := func(name string) string {
		re := regexp.MustCompile(`\b` + name + `\s*=\s*["']([^"']*)["']`)
		if m := re.FindSubmatch(tag); m != nil {
			return string(m[1])
		}
		return ""
	}
	num := func(s string) float64 {
		s = strings.TrimSuffix(strings.TrimSpace(s), "px")
		v, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return 0
		}
		return v
	}
	w, h := num(attr("width")), num(attr("height"))
	if w <= 0 || h <= 0 {
		fields := strings.Fields(strings.ReplaceAll(attr("viewBox"), ",", " "))
		if len(fields) == 4 {
			w, h = num(fields[2]), num(fields[3])
		}
	}
	if w <= 0 || h <= 0 {
		return nil
	}
	return &PictureSize{uint32(w + 0.5), uint32(h + 0.5)}
}

func init() {
	command("picture_size", func(c *Call, a struct {
		Path string `json:"path"`
	}) (*PictureSize, error) {
		return pictureSize(a.Path), nil
	})
	command("save_pasted_image", func(c *Call, a struct {
		DocPath    *string `json:"docPath"`
		DataBase64 string  `json:"dataBase64"`
		Ext        string  `json:"ext"`
		Folder     string  `json:"folder"`
	}) (SavedImage, error) {
		if a.Ext == "" || strings.IndexFunc(a.Ext, func(r rune) bool {
			return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9')
		}) >= 0 {
			return SavedImage{}, errors.New("invalid extension")
		}
		data, err := base64.StdEncoding.DecodeString(a.DataBase64)
		if err != nil {
			return SavedImage{}, err
		}
		var folder *imageFolder
		dir := c.app.draftAssetsDir()
		if a.DocPath != nil {
			f, err := resolveImageFolder(*a.DocPath, a.Folder)
			if err != nil {
				return SavedImage{}, err
			}
			folder, dir = &f, f.dir
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return SavedImage{}, err
		}
		name, err := writeNewFile(dir, fmt.Sprintf("image-%d.%s", time.Now().UnixMilli(), a.Ext), data, false)
		if err != nil {
			return SavedImage{}, err
		}
		written := filepath.Join(dir, name)
		if folder == nil {
			return SavedImage{Src: markdownPath(written)}, nil
		}
		return SavedImage{Src: folder.src(name), Path: &written}, nil
	})
	command("save_dropped_file", func(c *Call, a struct {
		DocPath string `json:"docPath"`
		Source  string `json:"source"`
		Folder  string `json:"folder"`
	}) (SavedImage, error) {
		if err := requireNonEmpty(a.DocPath); err != nil {
			return SavedImage{}, err
		}
		if err := requireNonEmpty(a.Source); err != nil {
			return SavedImage{}, err
		}
		if err := c.app.notASecret(a.Source); err != nil {
			return SavedImage{}, err
		}
		folder, err := resolveImageFolder(a.DocPath, a.Folder)
		if err != nil {
			return SavedImage{}, err
		}
		info, err := os.Stat(a.Source)
		if err != nil || !info.Mode().IsRegular() {
			return SavedImage{}, errors.New("not a file")
		}
		if err := os.MkdirAll(folder.dir, 0o755); err != nil {
			return SavedImage{}, err
		}
		name := filepath.Base(a.Source)
		srcDir, _ := filepath.EvalSymlinks(filepath.Dir(a.Source))
		dstDir, _ := filepath.EvalSymlinks(folder.dir)
		if srcDir == "" || srcDir != dstDir {
			name, err = intoFreeName(folder.dir, numberedNames(name, numberedDash, false), func(to string) error {
				return copyFileNew(a.Source, to)
			}, func(path string) bool { return sameBytes(a.Source, path) })
			if err != nil {
				return SavedImage{}, err
			}
		}
		written := filepath.Join(folder.dir, name)
		return SavedImage{Src: folder.src(name), Path: &written}, nil
	})
	command("migrate_draft_images", func(c *Call, a struct {
		DocPath       string   `json:"docPath"`
		Srcs          []string `json:"srcs"`
		Folder        string   `json:"folder"`
		SourceDocPath *string  `json:"sourceDocPath"`
	}) ([]ImageMigration, error) {
		if err := requireNonEmpty(a.DocPath); err != nil {
			return nil, err
		}
		folder, err := resolveImageFolder(a.DocPath, a.Folder)
		if err != nil {
			return nil, err
		}
		drafts := c.app.draftAssetsDir()
		// Saved into the folder it was in: its relative links all still hold.
		sourceDir := ""
		if a.SourceDocPath != nil && filepath.Dir(*a.SourceDocPath) != filepath.Dir(a.DocPath) {
			sourceDir = filepath.Dir(*a.SourceDocPath)
		}
		results := []ImageMigration{}
		for _, old := range a.Srcs {
			var moved *string
			if filepath.IsAbs(old) || strings.HasPrefix(old, "/") {
				if filepath.Dir(old) != drafts {
					continue // not a draft's picture: skipped, not a failure
				}
				if moved = copyImage(old, folder.dir); moved != nil {
					_ = os.Remove(old)
				}
			} else {
				if sourceDir == "" {
					continue
				}
				from := filepath.Join(sourceDir, old)
				if info, err := os.Stat(from); !isPicture(from) || err != nil || !info.Mode().IsRegular() {
					continue
				}
				moved = copyImage(from, folder.dir)
			}
			m := ImageMigration{Old: old}
			if moved != nil {
				src := folder.src(*moved)
				m.New = &src
			}
			results = append(results, m)
		}
		return results, nil
	})
}
