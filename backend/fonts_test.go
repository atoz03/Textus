package backend

import (
	"bytes"
	"encoding/binary"
	"encoding/json/v2"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf16"

	"golang.org/x/text/encoding/charmap"
)

// testName is one record of a fixture's `name` table.
type testName struct {
	platform, encoding, language, id uint16
	text                             string
}

// win is a Windows Unicode name in language.
func win(language, id uint16, text string) testName {
	return testName{platform: 3, encoding: 1, language: language, id: id, text: text}
}

// nameTable is a format 0 `name` table holding names, in their order.
func nameTable(names ...testName) []byte {
	var records, strs []byte
	for _, n := range names {
		var raw []byte
		if n.platform == 1 {
			raw, _ = charmap.Macintosh.NewEncoder().Bytes([]byte(n.text))
		} else {
			for _, unit := range utf16.Encode([]rune(n.text)) {
				raw = binary.BigEndian.AppendUint16(raw, unit)
			}
		}
		for _, v := range []uint16{n.platform, n.encoding, n.language, n.id, uint16(len(raw)), uint16(len(strs))} {
			records = binary.BigEndian.AppendUint16(records, v)
		}
		strs = append(strs, raw...)
	}
	table := binary.BigEndian.AppendUint16(nil, 0)
	table = binary.BigEndian.AppendUint16(table, uint16(len(names)))
	table = binary.BigEndian.AppendUint16(table, uint16(6+len(records)))
	return append(append(table, records...), strs...)
}

// os2Table is a version 4 OS/2 table with a weight class and fsSelection.
func os2Table(version, weight, selection uint16) []byte {
	table := make([]byte, 96)
	binary.BigEndian.PutUint16(table, version)
	binary.BigEndian.PutUint16(table[4:], weight)
	binary.BigEndian.PutUint16(table[62:], selection)
	return table
}

// postTable is a post table with an italic angle in degrees.
func postTable(angle int16) []byte {
	table := make([]byte, 32)
	binary.BigEndian.PutUint32(table, 0x00030000)
	binary.BigEndian.PutUint32(table[4:], uint32(int32(angle)<<16))
	return table
}

// testFont is a font's tables by tag.
type testFont map[string][]byte

// layout is font laid out as it is in a file whose first byte is base
// bytes before it: its table directory, then each table, padded to four
// bytes, at an offset from the file's start.
func (font testFont) layout(base int) []byte {
	tags := make([]string, 0, len(font))
	for tag := range font {
		tags = append(tags, tag)
	}
	slices.Sort(tags)
	out := binary.BigEndian.AppendUint32(nil, tagTrueType)
	out = binary.BigEndian.AppendUint16(out, uint16(len(tags)))
	out = append(out, make([]byte, 6)...)
	at := base + 12 + 16*len(tags)
	var body []byte
	for _, tag := range tags {
		data := font[tag]
		out = append(out, tag...)
		out = binary.BigEndian.AppendUint32(out, 0)
		out = binary.BigEndian.AppendUint32(out, uint32(at+len(body)))
		out = binary.BigEndian.AppendUint32(out, uint32(len(data)))
		body = append(body, data...)
		for len(body)%4 != 0 {
			body = append(body, 0)
		}
	}
	return append(out, body...)
}

// collection is a TrueType collection of fonts.
func collection(fonts ...testFont) []byte {
	head := binary.BigEndian.AppendUint32(nil, tagCollection)
	head = binary.BigEndian.AppendUint32(head, 0x00010000)
	head = binary.BigEndian.AppendUint32(head, uint32(len(fonts)))
	head = append(head, make([]byte, 4*len(fonts))...)
	base := len(head)
	var body []byte
	for i, font := range fonts {
		binary.BigEndian.PutUint32(head[12+i*4:], uint32(base+len(body)))
		body = append(body, font.layout(base+len(body))...)
	}
	return append(head, body...)
}

func facesOf(t *testing.T, file []byte) []fontFace {
	t.Helper()
	faces, err := parseFaces(bytes.NewReader(file), int64(len(file)))
	if err != nil {
		t.Fatal(err)
	}
	return faces
}

func TestAFontSaysItsFamilyItsCJKNamesItsWeightAndItsSlant(t *testing.T) {
	font := testFont{
		"name": nameTable(
			win(0x0404, nameTypographicFamily, "測試黑體"),
			win(0x0409, nameFamily, "Test Sans Light"),
			win(0x0409, nameTypographicFamily, "Test Sans"),
			win(0x0411, nameTypographicFamily, "テストゴシック"),
			win(0x0412, nameTypographicFamily, "테스트 고딕"),
			win(0x0804, nameTypographicFamily, "测试黑体"),
			win(0x0C04, nameTypographicFamily, "測試黑體 HK"),
		),
		"OS/2": os2Table(4, 300, 1),
		"post": postTable(0),
	}
	faces := facesOf(t, font.layout(0))
	want := []LocalName{{"zh-Hant", "測試黑體"}, {"ja", "テストゴシック"}, {"ko", "테스트 고딕"}, {"zh", "测试黑体"}}
	if len(faces) != 1 || faces[0].family != "Test Sans" || !slices.Equal(faces[0].local, want) ||
		faces[0].weight != 300 || !faces[0].italic || faces[0].index != 0 {
		t.Fatalf("%+v", faces)
	}
}

func TestWithoutATypographicFamilyTheFamilyIsTheName(t *testing.T) {
	font := testFont{"name": nameTable(
		win(0x0409, 2, "Regular"),
		win(0x0409, nameFamily, "Plain Serif"),
		win(0x0804, nameFamily, "朴素宋体"),
	)}
	faces := facesOf(t, font.layout(0))
	if faces[0].family != "Plain Serif" || !slices.Equal(faces[0].local, []LocalName{{"zh", "朴素宋体"}}) ||
		faces[0].weight != 400 || faces[0].italic {
		t.Fatalf("%+v", faces[0])
	}
}

func TestTheEnglishNameIsTheFamilysWhereverItIs(t *testing.T) {
	// The table's order puts the Chinese name first.
	font := testFont{"name": nameTable(
		win(0x0804, nameFamily, "微软雅黑"),
		win(0x0809, nameFamily, "Microsoft YaHei UK"),
		win(0x0409, nameFamily, "Microsoft YaHei"),
	)}
	if face := facesOf(t, font.layout(0))[0]; face.family != "Microsoft YaHei" || !slices.Equal(face.local, []LocalName{{"zh", "微软雅黑"}}) {
		t.Fatalf("%+v", face)
	}
	// Only the Mac's own English name besides: read as Mac Roman.
	mac := testFont{"name": nameTable(
		win(0x0411, nameFamily, "ヒラギノ"),
		testName{platform: 1, encoding: 0, language: 0, id: nameFamily, text: "Café Mincho"},
	)}
	if face := facesOf(t, mac.layout(0))[0]; face.family != "Café Mincho" || !slices.Equal(face.local, []LocalName{{"ja", "ヒラギノ"}}) {
		t.Fatalf("%+v", face)
	}
	// A name in no language beats one in another.
	unicode := testFont{"name": nameTable(
		win(0x0412, nameFamily, "나눔"),
		testName{platform: 0, encoding: 3, language: 0, id: nameFamily, text: "Nanum"},
	)}
	if face := facesOf(t, unicode.layout(0))[0]; face.family != "Nanum" || len(face.local) != 1 {
		t.Fatalf("%+v", face)
	}
}

func TestAFormatOneNameTableNamesLanguagesByTag(t *testing.T) {
	table := nameTable(
		testName{platform: 3, encoding: 1, language: 0x8000, id: nameFamily, text: "Tagged Sans"},
		testName{platform: 3, encoding: 1, language: 0x8001, id: nameFamily, text: "標記黑體"},
	)
	// Format 1: two language tags after the records, "en" and "zh-Hant".
	binary.BigEndian.PutUint16(table, 1)
	records := 6 + 2*12
	strs := int(binary.BigEndian.Uint16(table[4:]))
	var tags []byte
	tags = binary.BigEndian.AppendUint16(tags, 2)
	end := len(table) - strs
	for _, tag := range []string{"en", "zh-Hant"} {
		var raw []byte
		for _, unit := range utf16.Encode([]rune(tag)) {
			raw = binary.BigEndian.AppendUint16(raw, unit)
		}
		tags = binary.BigEndian.AppendUint16(tags, uint16(len(raw)))
		tags = binary.BigEndian.AppendUint16(tags, uint16(end))
		table = append(table, raw...)
		end += len(raw)
	}
	table = slices.Insert(table, records, tags...)
	binary.BigEndian.PutUint16(table[4:], uint16(strs+len(tags)))
	face := facesOf(t, testFont{"name": table}.layout(0))[0]
	if face.family != "Tagged Sans" || !slices.Equal(face.local, []LocalName{{"zh-Hant", "標記黑體"}}) {
		t.Fatalf("%+v", face)
	}
}

func TestWeightsAreOnCSSsScaleAndAnItalicAngleSlants(t *testing.T) {
	name := nameTable(win(0x0409, nameFamily, "Weights"))
	for _, c := range []struct {
		os2    []byte
		post   []byte
		weight int
		italic bool
	}{
		{os2Table(4, 7, 0), nil, 700, false},
		{os2Table(4, 0, 0), nil, 400, false},
		{os2Table(4, 1200, 0), nil, 1000, false},
		{os2Table(4, 500, 1<<9), nil, 500, true}, // oblique, from version 4
		{os2Table(3, 500, 1<<9), nil, 500, false},
		{os2Table(2, 400, 0), postTable(-12), 400, true},
		{os2Table(2, 600, 0)[:6], nil, 600, false}, // cut short after the weight
		{nil, nil, 400, false},
	} {
		font := testFont{"name": name}
		if c.os2 != nil {
			font["OS/2"] = c.os2
		}
		if c.post != nil {
			font["post"] = c.post
		}
		if face := facesOf(t, font.layout(0))[0]; face.weight != c.weight || face.italic != c.italic {
			t.Errorf("%+v: %+v", c, face)
		}
	}
}

func TestACollectionGivesEachOfItsFaces(t *testing.T) {
	file := collection(
		testFont{"name": nameTable(win(0x0409, nameFamily, "Coll Sans")), "OS/2": os2Table(4, 400, 0)},
		testFont{"name": nameTable(win(0x0409, nameFamily, "Coll Sans Bold"), win(0x0409, nameTypographicFamily, "Coll Sans")), "OS/2": os2Table(4, 700, 0)},
		testFont{"OS/2": os2Table(4, 700, 0)}, // no names: not a face to list
		testFont{"name": nameTable(win(0x0409, nameFamily, "Coll Mono"), win(0x0804, nameFamily, "合集等宽"))},
	)
	faces := facesOf(t, file)
	if len(faces) != 3 || faces[0].family != "Coll Sans" || faces[1].family != "Coll Sans" || faces[1].weight != 700 ||
		faces[1].index != 1 || faces[2].family != "Coll Mono" || faces[2].index != 3 {
		t.Fatalf("%+v", faces)
	}
}

func TestFamiliesAreListedOnceSortedWithoutHiddenOnes(t *testing.T) {
	face := func(family string, local ...LocalName) fontFace {
		return fontFace{family: family, local: local, weight: 400}
	}
	list := familiesOf([]fontEntry{
		{"/f/beta.ttf", face("beta")},
		{"/f/alpha.ttf", face("Alpha")},
		{"/f/alpha-bold.ttf", face("ALPHA", LocalName{"zh", "阿尔法"})},
		{"/f/alpha-light.ttf", face("Alpha", LocalName{"zh", "阿尔法 细"}, LocalName{"ja", "アルファ"})},
		{"/System/Library/Fonts/SFNS.ttf", face(".SF NS")},
	}, false)
	if len(list) != 2 || list[0].Name != "Alpha" || list[1].Name != "beta" ||
		!slices.Equal(list[0].Local, []LocalName{{"zh", "阿尔法"}, {"ja", "アルファ"}}) {
		t.Fatalf("%+v", list)
	}
	// Never null: the page reads each as a list.
	raw, err := json.Marshal(list[1])
	if err != nil || string(raw) != `{"name":"beta","local":[],"files":[]}` {
		t.Fatalf("%s %v", raw, err)
	}
	if raw, _ := json.Marshal(familiesOf(nil, true)); string(raw) != "[]" {
		t.Fatalf("%s", raw)
	}
}

func TestOnlyAFaceAloneInATTFOrOTFHasItsFile(t *testing.T) {
	entries := []fontEntry{
		{"/u/Library/Fonts/Mine-Bold.TTF", fontFace{family: "Mine", weight: 700}},
		{"/u/Library/Fonts/Mine-Italic.otf", fontFace{family: "Mine", weight: 400, italic: true}},
		{"/u/Library/Fonts/Mine.ttc", fontFace{family: "Mine", weight: 400}},
		{"/u/Library/Fonts/Mine.ttc", fontFace{family: "Mine", weight: 300, index: 1}},
	}
	want := []FontFile{{"/u/Library/Fonts/Mine-Bold.TTF", 700, false}, {"/u/Library/Fonts/Mine-Italic.otf", 400, true}}
	if list := familiesOf(entries, true); len(list) != 1 || !slices.Equal(list[0].Files, want) {
		t.Fatalf("%+v", list)
	}
	if list := familiesOf(entries, false); len(list[0].Files) != 0 {
		t.Fatalf("files where the web view finds fonts by name: %+v", list)
	}
}

func TestAFontInTheUsersFontsFolderIsListedWithItsFile(t *testing.T) {
	home := t.TempDir()
	fonts := filepath.Join(home, "Library", "Fonts")
	mine := filepath.Join(fonts, "Mine Sans.ttf")
	nested := filepath.Join(fonts, "Family", "Mine Sans Bold.otf")
	font := func(weight uint16) []byte {
		return testFont{"name": nameTable(win(0x0409, nameFamily, "Mine Sans")), "OS/2": os2Table(4, weight, 0)}.layout(0)
	}
	write(t, mine, font(400))
	write(t, nested, font(700))
	write(t, filepath.Join(fonts, "notes.txt"), []byte("not a font"))
	write(t, filepath.Join(fonts, "Broken.ttf"), []byte("OTTO but nothing more"))
	if !slices.Contains(fontDirs("darwin", func(string) string { return "" }, home), fonts) {
		t.Fatal("~/Library/Fonts is not looked in")
	}
	files := walkFontDirs([]string{fonts, filepath.Join(home, "missing")})
	if !slices.Equal(files, []string{filepath.Join(fonts, "Broken.ttf"), nested, mine}) {
		t.Fatalf("%q", files)
	}
	list := familiesOf(readFontFiles(files), true)
	want := []FontFile{{nested, 700, false}, {mine, 400, false}}
	if len(list) != 1 || list[0].Name != "Mine Sans" || !slices.Equal(list[0].Files, want) {
		t.Fatalf("%+v", list)
	}
}

func TestTheWalkStopsAtItsDepthAndTakesFontsByName(t *testing.T) {
	root := t.TempDir()
	deep := root
	for range maxFontDepth + 1 {
		deep = filepath.Join(deep, "d")
	}
	write(t, filepath.Join(root, "a", "b", "Upper.TTC"), nil)
	write(t, filepath.Join(root, "Collection.otc"), nil)
	write(t, filepath.Join(root, "readme.md"), nil)
	write(t, filepath.Join(deep, "TooDeep.ttf"), nil)
	files := walkFontDirs([]string{root, root})
	want := []string{filepath.Join(root, "Collection.otc"), filepath.Join(root, "a", "b", "Upper.TTC")}
	if runtime.GOOS != "windows" {
		// A link to a font is followed; one to a folder isn't.
		if err := os.Symlink(filepath.Join(root, "Collection.otc"), filepath.Join(root, "Linked.ttf")); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(root, filepath.Join(root, "loop")); err != nil {
			t.Fatal(err)
		}
		files = walkFontDirs([]string{root})
		want = slices.Insert(want, 1, filepath.Join(root, "Linked.ttf"))
	}
	if !slices.Equal(files, want) {
		t.Fatalf("%q", files)
	}
}

func TestEachSystemsFontFolders(t *testing.T) {
	env := func(values map[string]string) func(string) string {
		return func(key string) string { return values[key] }
	}
	mac := fontDirs("darwin", env(nil), "/Users/me")
	for _, dir := range []string{"/System/Library/Fonts", "/Library/Fonts", filepath.Join("/Users/me", "Library", "Fonts")} {
		if !slices.Contains(mac, dir) {
			t.Errorf("macOS: %s missing from %q", dir, mac)
		}
	}
	windows := fontDirs("windows", env(map[string]string{"WINDIR": `D:\Win`, "LOCALAPPDATA": `C:\Users\me\AppData\Local`}), "")
	if !slices.Equal(windows, []string{filepath.Join(`D:\Win`, "Fonts"), filepath.Join(`C:\Users\me\AppData\Local`, "Microsoft", "Windows", "Fonts")}) {
		t.Errorf("Windows: %q", windows)
	}
	join := filepath.Join
	linux := fontDirs("linux", env(nil), "/home/me")
	if !slices.Equal(linux, []string{join("/home/me", ".local", "share", "fonts"), join("/home/me", ".fonts"), join("/usr/local/share", "fonts"), join("/usr/share", "fonts")}) {
		t.Errorf("Linux: %q", linux)
	}
	set := fontDirs("linux", env(map[string]string{"XDG_DATA_HOME": "/data", "XDG_DATA_DIRS": "/opt/share:"}), "/home/me")
	if !slices.Equal(set, []string{join("/data", "fonts"), join("/home/me", ".fonts"), join("/opt/share", "fonts")}) {
		t.Errorf("Linux with XDG folders: %q", set)
	}
}

func TestADamagedFontIsSkippedWithoutHarm(t *testing.T) {
	good := testFont{"name": nameTable(win(0x0409, nameFamily, "Good")), "OS/2": os2Table(4, 400, 0)}.layout(0)
	tooManyTables := slices.Clone(good)
	binary.BigEndian.PutUint16(tooManyTables[4:], 0xFFFF)
	pastTheEnd := slices.Clone(good)
	for i := range 2 {
		binary.BigEndian.PutUint32(pastTheEnd[12+16*i+8:], 1<<30) // each table's offset
	}
	hugeCollection := collection(testFont{"name": nameTable(win(0x0409, nameFamily, "C"))})
	binary.BigEndian.PutUint32(hugeCollection[8:], 1<<31)
	strayCollection := collection(testFont{"name": nameTable(win(0x0409, nameFamily, "C"))})
	binary.BigEndian.PutUint32(strayCollection[12:], 1<<20)
	oddName := nameTable(win(0x0409, nameFamily, "Odd"))
	binary.BigEndian.PutUint16(oddName[2:], 900) // more records than the table holds
	binary.BigEndian.PutUint16(oddName[6+8:], 5) // an odd length
	for name, file := range map[string][]byte{
		"empty":            nil,
		"short":            good[:10],
		"not a font":       []byte("<!doctype html><html></html>"),
		"cut":              good[:40],
		"too many tables":  tooManyTables,
		"past the end":     pastTheEnd,
		"huge collection":  hugeCollection,
		"stray collection": strayCollection,
	} {
		if faces, err := parseFaces(bytes.NewReader(file), int64(len(file))); err == nil {
			t.Errorf("%s: %+v", name, faces)
		}
	}
	if faces := facesOf(t, testFont{"name": oddName}.layout(0)); faces[0].family != "Od" {
		t.Errorf("odd name: %+v", faces)
	}
	if _, err := readFaces(filepath.Join(t.TempDir(), "missing.ttf")); err == nil {
		t.Error("a missing file read")
	}
}

func FuzzParseFaces(f *testing.F) {
	f.Add(testFont{"name": nameTable(win(0x0409, nameFamily, "Fuzz"), win(0x0804, nameTypographicFamily, "模糊")), "OS/2": os2Table(4, 400, 1), "post": postTable(-8)}.layout(0))
	f.Add(collection(testFont{"name": nameTable(win(0x0409, nameFamily, "A"))}, testFont{"name": nameTable(win(0x0411, nameFamily, "B"))}))
	f.Add([]byte("ttcf\x00\x01\x00\x00\x00\x00\x00\x02"))
	f.Fuzz(func(t *testing.T, file []byte) {
		faces, err := parseFaces(bytes.NewReader(file), int64(len(file)))
		for _, face := range faces {
			if err != nil || face.family == "" || face.weight < 1 || face.weight > 1000 {
				t.Fatalf("%+v %v", face, err)
			}
		}
	})
}

// The fonts of the machine the tests run on: the system's own on macOS
// and Windows, fontconfig's on Linux.
func TestTheInstalledFontsAreListed(t *testing.T) {
	start := time.Now()
	list := fontFamilies()
	t.Logf("%d families in %v", len(list), time.Since(start))
	names := map[string]FontFamily{}
	for i, f := range list {
		key := strings.ToLower(f.Name)
		if _, twice := names[key]; twice || f.Name == "" || strings.HasPrefix(f.Name, ".") ||
			(i > 0 && strings.ToLower(list[i-1].Name) > key) || f.Local == nil || f.Files == nil {
			t.Fatalf("%q at %d", f.Name, i)
		}
		names[key] = f
	}
	listed := func(candidates ...string) bool {
		return slices.ContainsFunc(candidates, func(name string) bool { _, ok := names[strings.ToLower(name)]; return ok })
	}
	switch runtime.GOOS {
	case "darwin":
		if !listed("Helvetica", "Menlo", "Times", "Geneva") {
			t.Fatalf("no system font among %d families", len(list))
		}
		withFiles := 0
		for _, f := range list {
			for _, file := range f.Files {
				withFiles++
				if _, err := os.Stat(file.Path); err != nil || !isFontFile(file.Path) || file.Weight < 1 || file.Weight > 1000 {
					t.Fatalf("%s: %+v", f.Name, file)
				}
			}
		}
		if withFiles == 0 {
			t.Fatal("no family has a file for an @font-face")
		}
	case "windows":
		if !listed("Arial", "Segoe UI", "Times New Roman") {
			t.Fatalf("no system font among %d families", len(list))
		}
		for _, f := range list {
			if len(f.Files) > 0 {
				t.Fatalf("%s has files", f.Name)
			}
		}
	default:
		if len(fontconfigFiles()) > 0 && len(list) == 0 {
			t.Fatal("fontconfig lists fonts and none is listed")
		}
	}
}

func write(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}
