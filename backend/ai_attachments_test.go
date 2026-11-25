package backend

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"golang.org/x/image/bmp"
)

func TestTruncationIsMarkedNotSilent(t *testing.T) {
	long := textAttachment("big.txt", strings.Repeat("字", maxExtractedChars+10), "")
	if !long.Truncated || !strings.Contains(long.Content, "truncated") || !strings.HasPrefix(long.Content, strings.Repeat("字", maxExtractedChars)+"\n\n") {
		t.Fatal(long.Truncated, len(long.Content))
	}
	if small := textAttachment("small.txt", "hello", ""); small.Truncated || small.Content != "hello" {
		t.Fatal(small)
	}
}

// The picker and the reader agree, or the dialog offers files stage
// refuses.
func TestEveryOfferedExtensionIsOneTheReaderHandles(t *testing.T) {
	asText := []string{"md", "markdown", "mdx", "txt", "text", "csv", "tsv", "json", "yaml", "yml", "svg", "zip"}
	for _, ext := range attachableExtensions {
		if !slices.Contains(attachedPictureExtensions, ext) && !slices.Contains(documentExtensions, ext) && !slices.Contains(asText, ext) {
			t.Errorf("the picker offers %s but stage has no way to read it", ext)
		}
	}
}

// attachedFolderIn is a scratch folder with attached/ in it.
func attachedFolderIn(t *testing.T) string {
	dir := filepath.Join(t.TempDir(), "attached")
	os.MkdirAll(dir, 0o755)
	return dir
}

func TestCodeIsAttachedAsTextAndKeptWholeUnderAttached(t *testing.T) {
	attached := attachedFolderIn(t)
	source := filepath.Join(t.TempDir(), "main.py")
	os.WriteFile(source, []byte("print('hi')\n"), 0o644)
	for _, want := range []string{"attached/main.py", "attached/main (2).py"} {
		got, err := stage(source, attached)
		if err != nil || got.Image || got.Content != "print('hi')\n" || got.Path != want {
			t.Fatal(got, err)
		}
	}
	// Pasted bytes are staged and read the same way.
	got, err := stageBytes("main.py", []byte("print('hi')\n"), attached)
	if err != nil || got.Path != "attached/main (3).py" {
		t.Fatal(got, err)
	}
	raw, _ := got.MarshalJSON()
	if string(raw) != `{"kind":"text","name":"main (3).py","content":"print('hi')\n","truncated":false,"path":"attached/main (3).py"}` {
		t.Fatal(string(raw))
	}
	os.WriteFile(source, []byte{0xff, 0xfe, 0x00}, 0o644)
	if _, err := stage(source, attached); err == nil || !strings.Contains(err.Error(), "isn't a text file") {
		t.Fatal(err)
	}
	os.WriteFile(source, []byte("  \n"), 0o644)
	if _, err := stage(source, attached); err == nil || !strings.Contains(err.Error(), "has no readable text") {
		t.Fatal(err)
	}
}

func TestAZipIsUnpackedAndListed(t *testing.T) {
	attached := attachedFolderIn(t)
	source := filepath.Join(t.TempDir(), "notes.zip")
	os.WriteFile(source, zipOf(t, [][2]string{{"ch1.md", "# One"}, {"sub/b.txt", "beta"}, {"__MACOSX/._ch1.md", "junk"}, {"../escape.txt", "no"}}), 0o644)
	got, err := stage(source, attached)
	if err != nil || !strings.Contains(got.Content, "- attached/notes/ch1.md\n- attached/notes/sub/b.txt") || strings.Contains(got.Content, "MACOSX") ||
		!strings.Contains(got.Content, "(2 files)") {
		t.Fatal(got.Content, err)
	}
	if text, _ := os.ReadFile(filepath.Join(attached, "notes", "sub", "b.txt")); string(text) != "beta" {
		t.Fatal(string(text))
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(attached), "escape.txt")); err == nil {
		t.Fatal("an entry escaped")
	}
	os.WriteFile(source, []byte("not a zip"), 0o644)
	if _, err := stage(source, attached); err == nil || err.Error() != "That archive isn't a zip file Textus can open." {
		t.Fatal(err)
	}
}

func TestABMPIsSentAsJPEGAndAHugeOneMadeSmaller(t *testing.T) {
	picture := image.NewRGBA(image.Rect(0, 0, 4000, 1000))
	for i := range picture.Pix {
		picture.Pix[i] = 0xff
	}
	var raw bytes.Buffer
	bmp.Encode(&raw, picture)
	path := filepath.Join(t.TempDir(), "scan.bmp")
	os.WriteFile(path, raw.Bytes(), 0o644)
	mime, data, err := sentPicture(path, "bmp")
	if err != nil || mime != "image/jpeg" {
		t.Fatal(mime, err)
	}
	back, err := jpeg.Decode(bytes.NewReader(data))
	if err != nil || back.Bounds().Dx() != longSide || back.Bounds().Dy() != 512 {
		t.Fatal(back.Bounds(), err)
	}
}

func TestASmallPNGGoesAsItIsAndAnIconAsItsPicture(t *testing.T) {
	dot := image.NewNRGBA(image.Rect(0, 0, 4, 4))
	dot.Set(1, 1, color.NRGBA{255, 0, 0, 128})
	var raw bytes.Buffer
	png.Encode(&raw, dot)
	dir := t.TempDir()
	path := filepath.Join(dir, "dot.png")
	os.WriteFile(path, raw.Bytes(), 0o644)
	if mime, data, err := sentPicture(path, "png"); err != nil || mime != "image/png" || !bytes.Equal(data, raw.Bytes()) {
		t.Fatal(mime, err)
	}
	// An .ico holding that PNG: one directory entry, then the picture.
	icon := []byte{0, 0, 1, 0, 1, 0, 4, 4, 0, 0, 1, 0, 32, 0}
	icon = append(icon, byte(raw.Len()), byte(raw.Len()>>8), 0, 0, 22, 0, 0, 0)
	icon = append(icon, raw.Bytes()...)
	ico := filepath.Join(dir, "favicon.ico")
	os.WriteFile(ico, icon, 0o644)
	if mime, _, err := sentPicture(ico, "ico"); err != nil || mime != "image/png" {
		t.Fatal(mime, err)
	}
	os.WriteFile(ico, []byte("no icon"), 0o644)
	if _, _, err := sentPicture(ico, "ico"); err == nil || !strings.Contains(err.Error(), "pictureUnreadable") {
		t.Fatal(err)
	}
}

func TestAPastedPictureIsAttachedAsOne(t *testing.T) {
	testScratch(t)
	a := &App{dirs: Dirs{Config: t.TempDir(), Data: t.TempDir()}}
	var raw bytes.Buffer
	png.Encode(&raw, image.NewNRGBA(image.Rect(0, 0, 2, 2)))
	data := base64.StdEncoding.EncodeToString(raw.Bytes())
	got, err := run(a, "attach_pasted", `{"conversation":"c1","pasted":[{"kind":"bytes","name":"image.png","dataBase64":"`+data+`"}]}`)
	if err != nil || got != `[{"kind":"image","name":"image.png","mime":"image/png","dataBase64":"`+data+`","path":"attached/image.png"}]` {
		t.Fatal(got, err)
	}
	if _, ok := withAttachments("c1"); !ok {
		t.Fatal("the conversation has no attachments folder")
	}
	if _, err := run(a, "attach_pasted", `{"conversation":"../c1","pasted":[]}`); err == nil {
		t.Fatal("a conversation id that leaves the scratch folder")
	}
}
