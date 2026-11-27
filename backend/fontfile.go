package backend

import (
	"cmp"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"slices"
	"strings"
	"unicode"
	"unicode/utf16"

	"golang.org/x/text/encoding/charmap"
)

// A font file's faces, as the font list (fonts.go) needs them: the names
// from the `name` table, the weight and slant from `OS/2` and `post`. A
// TrueType or OpenType font, or a collection of them; read with bounds
// checked, as a damaged file must cost only its own faces.

// fontFace is what one face of a font file says of itself.
type fontFace struct {
	// family is its family name in English, where it gives one.
	family string
	local  []LocalName
	// weight is on CSS's scale, 1 to 1000.
	weight int
	italic bool
	// index is its place in a collection; 0 for a font alone in its file.
	index int
}

// The tags a font file starts with: a lone TrueType or OpenType font (the
// Mac's own "true" among them), or a collection.
const (
	tagTrueType   = 0x00010000
	tagOpenType   = 0x4F54544F // OTTO
	tagAppleTrue  = 0x74727565 // true
	tagCollection = 0x74746366 // ttcf
)

// The name IDs of a family's name: the family a style-linked set of up to
// four faces shares, and the typographic family every weight shares.
const (
	nameFamily            = 1
	nameTypographicFamily = 16
)

// Bounds a damaged file can't push the reader past: more faces than any
// collection has, more tables than any font, a name table bigger than any.
const (
	maxFaces     = 512
	maxTables    = 1024
	maxNameTable = 4 << 20
)

var errNotAFont = errors.New("not a font")

// readFaces are the faces of the font file at path.
func readFaces(path string) (faces []fontFace, err error) {
	// A damaged font costs its own faces, never the app.
	defer func() {
		if recover() != nil {
			faces, err = nil, errNotAFont
		}
	}()
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	return parseFaces(f, info.Size())
}

// parseFaces are the faces of a font file of size bytes: one, or each of a
// collection's that can be read.
func parseFaces(r io.ReaderAt, size int64) ([]fontFace, error) {
	head, err := section(r, size, 0, 12)
	if err != nil {
		return nil, err
	}
	if binary.BigEndian.Uint32(head) != tagCollection {
		face, err := parseFace(r, size, 0)
		if err != nil {
			return nil, err
		}
		return []fontFace{face}, nil
	}
	n := int64(binary.BigEndian.Uint32(head[8:]))
	if n == 0 || n > maxFaces {
		return nil, errNotAFont
	}
	offsets, err := section(r, size, 12, n*4)
	if err != nil {
		return nil, err
	}
	var faces []fontFace
	for i := range int(n) {
		face, err := parseFace(r, size, int64(binary.BigEndian.Uint32(offsets[i*4:])))
		if err != nil {
			continue
		}
		face.index = i
		faces = append(faces, face)
	}
	if len(faces) == 0 {
		return nil, errNotAFont
	}
	return faces, nil
}

// parseFace is the face whose table directory is at off: its names from
// the `name` table, its weight and slant from `OS/2` and `post`.
func parseFace(r io.ReaderAt, size, off int64) (fontFace, error) {
	head, err := section(r, size, off, 12)
	if err != nil {
		return fontFace{}, err
	}
	switch binary.BigEndian.Uint32(head) {
	case tagTrueType, tagOpenType, tagAppleTrue:
	default:
		return fontFace{}, errNotAFont
	}
	n := int64(binary.BigEndian.Uint16(head[4:]))
	if n > maxTables {
		return fontFace{}, errNotAFont
	}
	records, err := section(r, size, off+12, n*16)
	if err != nil {
		return fontFace{}, err
	}
	var name, os2, post []byte
	for i := range n {
		record := records[i*16:]
		at, length := int64(binary.BigEndian.Uint32(record[8:])), int64(binary.BigEndian.Uint32(record[12:]))
		// Only as much of each table as is read: the version and weight
		// class to fsSelection of OS/2, the italic angle of post.
		var into *[]byte
		switch string(record[:4]) {
		case "name":
			into, length = &name, min(length, maxNameTable)
		case "OS/2":
			into, length = &os2, min(length, 64)
		case "post":
			into, length = &post, min(length, 8)
		default:
			continue
		}
		// A table past the end of the file is as good as none.
		if data, err := section(r, size, at, length); err == nil {
			*into = data
		}
	}
	family, local, ok := familyNames(name)
	if !ok {
		return fontFace{}, errNotAFont
	}
	weight, italic := faceStyle(os2, post)
	return fontFace{family: family, local: local, weight: weight, italic: italic}, nil
}

// section is the n bytes of r at off, an error where the file holds fewer.
func section(r io.ReaderAt, size, off, n int64) ([]byte, error) {
	if off < 0 || n < 0 || off > size || n > size-off {
		return nil, errNotAFont
	}
	b := make([]byte, n)
	if read, err := r.ReadAt(b, off); int64(read) < n {
		return nil, cmp.Or(err, errNotAFont)
	}
	return b, nil
}

// faceStyle is a face's weight on CSS's scale and whether it slants: an
// OS/2 table's weight class and its italic or oblique bit, or a post
// table's italic angle.
func faceStyle(os2, post []byte) (int, bool) {
	weight, italic := 400, false
	if len(os2) >= 6 {
		weight = cssWeight(int(binary.BigEndian.Uint16(os2[4:])))
	}
	if len(os2) >= 64 {
		version, selection := binary.BigEndian.Uint16(os2), binary.BigEndian.Uint16(os2[62:])
		italic = selection&1 != 0 || (version >= 4 && selection&(1<<9) != 0)
	}
	if len(post) >= 8 && binary.BigEndian.Uint32(post[4:]) != 0 {
		italic = true
	}
	return weight, italic
}

// cssWeight is an OS/2 weight class as CSS takes it, 1 to 1000; some old
// fonts count 1 to 9 for 100 to 900.
func cssWeight(class int) int {
	switch {
	case class == 0:
		return 400
	case class < 10:
		return class * 100
	case class > 1000:
		return 1000
	}
	return class
}

// nameRecord is one family name of a `name` table, decoded.
type nameRecord struct {
	id uint16
	// english ranks how surely it is the English name: 0 for US English,
	// then the Mac's English, other English, a name in no language, and 4
	// for a name in any other language.
	english int
	// cjk is its language as LocalName spells it, "" for the others.
	cjk  string
	text string
}

// familyNames are a face's family name - its typographic family's where
// it has one, else its family's - in English where it says it in English,
// and its names in the CJK languages, as fontdb chose them.
func familyNames(table []byte) (string, []LocalName, bool) {
	records := familyRecords(table)
	id := uint16(nameTypographicFamily)
	if !slices.ContainsFunc(records, func(r nameRecord) bool { return r.id == id }) {
		id = nameFamily
	}
	var best *nameRecord
	for i := range records {
		if r := &records[i]; r.id == id && (best == nil || r.english < best.english) {
			best = r
		}
	}
	if best == nil {
		return "", nil, false
	}
	var local []LocalName
	for _, r := range records {
		if r.id == id && r.cjk != "" && r.text != best.text &&
			!slices.ContainsFunc(local, func(l LocalName) bool { return l.Language == r.cjk }) {
			local = append(local, LocalName{Language: r.cjk, Name: r.text})
		}
	}
	return best.text, local, true
}

// familyRecords are the family and typographic family names a `name`
// table holds, in its order, decoded where their encoding is Unicode or
// the Mac's Roman.
func familyRecords(table []byte) []nameRecord {
	if len(table) < 6 {
		return nil
	}
	count, strs := int(binary.BigEndian.Uint16(table[2:])), int(binary.BigEndian.Uint16(table[4:]))
	count = min(count, (len(table)-6)/12)
	// Format 1 names some languages by a tag ("zh-Hant") rather than a
	// Windows language ID.
	var tags []string
	if binary.BigEndian.Uint16(table) == 1 && 6+count*12+2 <= len(table) {
		at := 6 + count*12
		n := int(binary.BigEndian.Uint16(table[at:]))
		for i := range min(n, (len(table)-at-2)/4) {
			tag := table[at+2+i*4:]
			tags = append(tags, decodeNameText(0, 0, stringAt(table, strs, tag)))
		}
	}
	var records []nameRecord
	for i := range count {
		record := table[6+i*12:]
		platform, encoding := binary.BigEndian.Uint16(record), binary.BigEndian.Uint16(record[2:])
		language, id := binary.BigEndian.Uint16(record[4:]), binary.BigEndian.Uint16(record[6:])
		if id != nameFamily && id != nameTypographicFamily {
			continue
		}
		text := decodeNameText(platform, encoding, stringAt(table, strs, record[8:]))
		if text == "" {
			continue
		}
		english, cjk := nameLanguage(platform, language, tags)
		records = append(records, nameRecord{id: id, english: english, cjk: cjk, text: text})
	}
	return records
}

// stringAt is the string a record's length and offset (the first four
// bytes of field) point to among a `name` table's strings at strs; nil
// where they point past its end.
func stringAt(table []byte, strs int, field []byte) []byte {
	length, offset := int(binary.BigEndian.Uint16(field)), int(binary.BigEndian.Uint16(field[2:]))
	start := strs + offset
	if start+length > len(table) {
		return nil
	}
	return table[start : start+length]
}

// decodeNameText is a name's text: UTF-16 for Unicode and Windows (its
// symbol, BMP and full-repertoire encodings), the Mac's Roman for the Mac;
// "" for the legacy CJK encodings, which fonts also name in Unicode.
func decodeNameText(platform, encoding uint16, raw []byte) string {
	var text string
	switch {
	case platform == 0, platform == 3 && (encoding == 0 || encoding == 1 || encoding == 10):
		units := make([]uint16, len(raw)/2)
		for i := range units {
			units[i] = binary.BigEndian.Uint16(raw[i*2:])
		}
		text = string(utf16.Decode(units))
	case platform == 1 && encoding == 0:
		decoded, err := charmap.Macintosh.NewDecoder().Bytes(raw)
		if err != nil {
			return ""
		}
		text = string(decoded)
	}
	return strings.TrimFunc(text, func(r rune) bool { return unicode.IsSpace(r) || r == 0 })
}

// nameLanguage is how English a name is (nameRecord's english) and its
// CJK language, from its platform's language ID or its format 1 tag.
func nameLanguage(platform, language uint16, tags []string) (int, string) {
	if language >= 0x8000 {
		if i := int(language - 0x8000); i < len(tags) {
			return tagLanguage(tags[i])
		}
		return 4, ""
	}
	switch platform {
	case 0:
		return 3, ""
	case 1:
		if language == 0 {
			return 1, ""
		}
		return 4, ""
	case 3:
		// A Windows language ID: the language in the low ten bits, the
		// region or script above.
		primary, sub := language&0x3FF, language>>10
		switch {
		case language == 0x0409:
			return 0, ""
		case primary == 0x09:
			return 2, ""
		case primary == 0x04 && (sub == 1 || sub == 3 || sub == 5): // Taiwan, Hong Kong, Macao
			return 4, "zh-Hant"
		case primary == 0x04:
			return 4, "zh"
		case primary == 0x11:
			return 4, "ja"
		case primary == 0x12:
			return 4, "ko"
		}
	}
	return 4, ""
}

// tagLanguage is nameLanguage for a BCP 47 tag.
func tagLanguage(tag string) (int, string) {
	tag = strings.ToLower(tag)
	base, rest, _ := strings.Cut(tag, "-")
	switch base {
	case "en":
		if rest == "us" {
			return 0, ""
		}
		return 2, ""
	case "zh":
		for part := range strings.SplitSeq(rest, "-") {
			if part == "hant" || part == "tw" || part == "hk" || part == "mo" {
				return 4, "zh-Hant"
			}
		}
		return 4, "zh"
	case "ja", "ko":
		return 4, base
	}
	return 4, ""
}
