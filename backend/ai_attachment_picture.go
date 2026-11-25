package backend

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/jpeg"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"golang.org/x/image/draw"
)

// Attached pictures as every model reads them: PNG, JPEG, GIF or WebP,
// under the size limit and no longer than longSide on the long side. The
// decoders (BMP, TIFF and WebP besides the standard ones) are registered
// in pictures.go.

// longSide is the longest side a picture is sent at; models scale larger
// ones down anyway.
const longSide = 2048

// attachedPictureExtensions are attached as pictures, sent as they are or
// converted.
var attachedPictureExtensions = []string{
	"png", "jpg", "jpeg", "gif", "webp", "bmp", "tif", "tiff", "ico", "heic", "heif", "avif",
}

// sentPicture is the picture at path as it is sent - converted and made
// smaller as needed - and its type.
func sentPicture(path, ext string) (string, []byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", nil, err
	}
	if mime := imageMime(ext); mime != "" && len(data) <= maxImageBytes {
		return mime, data, nil
	}
	var decoded image.Image
	switch ext {
	case "heic", "heif", "avif":
		if decoded, err = systemDecode(path); err != nil {
			return "", nil, err
		}
	case "ico":
		decoded, err = decodeIcon(data)
	default:
		decoded, _, err = image.Decode(bytes.NewReader(data))
	}
	if err != nil {
		return "", nil, codeError("pictureUnreadable")
	}
	return encodePicture(decoded)
}

// encodePicture is PNG when it has transparency, JPEG otherwise, at
// longSide at most.
func encodePicture(picture image.Image) (string, []byte, error) {
	size := picture.Bounds().Size()
	if longest := max(size.X, size.Y); longest > longSide {
		w, h := max(size.X*longSide/longest, 1), max(size.Y*longSide/longest, 1)
		smaller := image.NewNRGBA(image.Rect(0, 0, w, h))
		draw.BiLinear.Scale(smaller, smaller.Bounds(), picture, picture.Bounds(), draw.Src, nil)
		picture = smaller
	}
	var out bytes.Buffer
	mime := "image/jpeg"
	if opaque, ok := picture.(interface{ Opaque() bool }); ok && !opaque.Opaque() {
		mime = "image/png"
		if err := png.Encode(&out, picture); err != nil {
			return "", nil, err
		}
	} else if err := jpeg.Encode(&out, picture, &jpeg.Options{Quality: 75}); err != nil {
		return "", nil, err
	}
	if out.Len() > maxImageBytes {
		return "", nil, codeError("pictureTooLarge")
	}
	return mime, out.Bytes(), nil
}

// decodeIcon is an .ico file's largest picture, when it is stored as PNG,
// as icons of any size now are.
func decodeIcon(data []byte) (image.Image, error) {
	if len(data) < 6 || binary.LittleEndian.Uint16(data[2:]) != 1 {
		return nil, image.ErrFormat
	}
	var best image.Image
	for i := range int(binary.LittleEndian.Uint16(data[4:])) {
		entry := 6 + 16*i
		if len(data) < entry+16 {
			break
		}
		size, at := binary.LittleEndian.Uint32(data[entry+8:]), binary.LittleEndian.Uint32(data[entry+12:])
		if uint64(at)+uint64(size) > uint64(len(data)) {
			continue
		}
		picture, err := png.Decode(bytes.NewReader(data[at : at+size]))
		if err == nil && (best == nil || picture.Bounds().Dx() > best.Bounds().Dx()) {
			best = picture
		}
	}
	if best == nil {
		return nil, image.ErrFormat
	}
	return best, nil
}

// systemDecode reads HEIC and AVIF, which only the system can: on macOS
// sips turns one into a PNG first.
func systemDecode(path string) (image.Image, error) {
	if runtime.GOOS != "darwin" {
		return nil, codeError("pictureFormat")
	}
	dir, err := os.MkdirTemp("", "textus-picture-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	out := filepath.Join(dir, "picture.png")
	if err := exec.Command("/usr/bin/sips", "-s", "format", "png", path, "--out", out).Run(); err != nil {
		return nil, codeError("pictureUnreadable")
	}
	f, err := os.Open(out)
	if err != nil {
		return nil, codeError("pictureUnreadable")
	}
	defer f.Close()
	picture, err := png.Decode(f)
	if err != nil {
		return nil, codeError("pictureUnreadable")
	}
	return picture, nil
}
