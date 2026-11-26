package backend

import (
	"archive/zip"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"textus/backend/ai"
)

// project is a folder holding files, by /-separated path.
func project(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for rel, body := range files {
		writeText(t, filepath.Join(dir, filepath.FromSlash(rel)), body)
	}
	return dir
}

func paragraphsDocx(texts ...string) []byte {
	var body strings.Builder
	for _, text := range texts {
		body.WriteString("<w:p><w:r><w:t>" + text + "</w:t></w:r></w:p>")
	}
	return wordFile(body.String(), nil)
}

func TestWalkingSkipsHiddenAndBuildFoldersAndReadsSafely(t *testing.T) {
	dir := project(t, map[string]string{
		"notes/a.md": "alpha beta", "b.txt": "Beta gamma", ".secret/key.md": "beta",
		"node_modules/x.md": "beta", "image.png": "beta",
	})
	if got := walkTextFiles(dir).files; !slices.Equal(got, []string{"notes/a.md", "b.txt"}) {
		t.Errorf("got %v", got)
	}
	if got := searchFiles(dir, "beta"); got != "notes/a.md:1: alpha beta\nb.txt:1: Beta gamma" {
		t.Errorf("got %q", got)
	}
	for _, rel := range []string{".secret/key.md", "../etc/passwd"} {
		if _, err := readMention(dir, rel); err == nil {
			t.Errorf("%s was read", rel)
		}
	}
	if got, err := readMention(dir, "@notes/a.md"); err != nil || got != "alpha beta" {
		t.Errorf("got %q, %v", got, err)
	}
}

func TestTheProjectWalkFollowsGitignoreLikeTheSidebar(t *testing.T) {
	dir := project(t, map[string]string{".gitignore": "drafts/\n", "drafts/old.md": "beta", "notes.md": "beta"})
	if got := walkTextFiles(dir).files; !slices.Equal(got, []string{"notes.md"}) {
		t.Errorf("got %v", got)
	}
	if got := searchFiles(dir, "beta"); got != "notes.md:1: beta" {
		t.Errorf("got %q", got)
	}
	if got := findFiles(dir, "old", false); !strings.HasPrefix(got, "No file") {
		t.Errorf("got %q", got)
	}
}

func TestSearchReturnsTheFirstMatchesInWalkOrderAndSaysItStopped(t *testing.T) {
	files := map[string]string{}
	for i := range 60 {
		files[fmt.Sprintf("ch%d.md", i)] = "beta\nbeta"
	}
	dir := project(t, files)
	first := searchFiles(dir, "beta")
	lines := strings.Split(first, "\n")
	if len(lines) != maxSearchMatches+1 || lines[0] != "ch0.md:1: beta" || lines[2] != "ch1.md:1: beta" ||
		lines[39] != "ch19.md:2: beta" || lines[40] != "... (stopped at 40 matches)" {
		t.Errorf("got %q", first)
	}
	for range 5 {
		if again := searchFiles(dir, "beta"); again != first {
			t.Errorf("another run found %q", again)
		}
	}
}

func TestSearchSkipsFilesTooLargeOrNotText(t *testing.T) {
	dir := project(t, map[string]string{
		"huge.md": "beta " + strings.Repeat("x", maxTextBytes), "nul.txt": "beta\x00", "small.md": "beta",
	})
	if err := os.WriteFile(filepath.Join(dir, "binary.txt"), []byte{'b', 'e', 't', 'a', '\n', 0xff, 0xfe}, 0o644); err != nil {
		t.Fatal(err)
	}
	if got := searchFiles(dir, "beta"); got != "small.md:1: beta" {
		t.Errorf("got %q", got)
	}
	if _, err := readPart(dir, "huge.md", lineRange{}); err == nil || !strings.Contains(err.Error(), "too large to read (16.0 MB; the limit is 16.0 MB)") {
		t.Errorf("got %v", err)
	}
	if _, err := readPart(dir, "nul.txt", lineRange{}); err == nil || !strings.Contains(err.Error(), "isn't a readable text file") {
		t.Errorf("got %v", err)
	}
}

// A book in one file, past what an @-mention takes: read_file shows it in
// parts and search_files points into it by line, numbered as read_file
// numbers them.
func TestABigFileReadsInPartsAndSearchPointsIntoIt(t *testing.T) {
	var book strings.Builder
	for n := 1; n <= 6_000; n++ {
		if n == 4_321 {
			book.WriteString("The lighthouse keeper's name was Abel Mora.\n")
		} else {
			fmt.Fprintf(&book, "Line %d of the long manuscript, nothing to see here.\n", n)
		}
	}
	root := project(t, map[string]string{"book.md": book.String()})
	if _, err := readMention(root, "book.md"); err == nil {
		t.Error("an @-mention took the whole book")
	}
	first, err := readPart(root, "book.md", lineRange{})
	if err != nil {
		t.Fatal(err)
	}
	header, _, _ := strings.Cut(first, "\n")
	if !strings.HasPrefix(header, "`book.md`, lines 1-") || !strings.HasSuffix(header, "of 6000:") {
		t.Fatalf("header %q", header)
	}
	shown, _ := strconv.Atoi(strings.Fields(strings.TrimPrefix(header, "`book.md`, lines 1-"))[0])
	if len([]rune(first)) > maxReadUnits+200 || !strings.HasSuffix(first, fmt.Sprintf("[... %d more lines not shown - read on with start_line=%d, or read the lines around a match search_files found]", 6_000-shown, shown+1)) {
		t.Errorf("first read ends %q", first[max(0, len(first)-200):])
	}
	next, err := readPart(root, "book.md", lineRange{start: shown + 1})
	if err != nil || !strings.HasPrefix(next, fmt.Sprintf("`book.md`, lines %d-", shown+1)) || !strings.Contains(next, "[... cut for length - continue with start_line=") {
		t.Errorf("next read: %v", err)
	}
	if got := searchFiles(root, "abel mora"); got != "book.md:4321: The lighthouse keeper's name was Abel Mora." {
		t.Errorf("got %q", got)
	}
	around, err := readPart(root, "book.md", lineRange{start: 4_320, end: 4_322})
	if err != nil || around != "`book.md`, lines 4320-4322 of 6000:\nLine 4320 of the long manuscript, nothing to see here.\nThe lighthouse keeper's name was Abel Mora.\nLine 4322 of the long manuscript, nothing to see here." {
		t.Errorf("got %q, %v", around, err)
	}
}

func TestAReadCountsChineseCharactersByWeight(t *testing.T) {
	line := strings.Repeat("灯塔看守人的名字叫阿贝尔，他每天傍晚都会爬上那段螺旋楼梯。", 4)
	dir := project(t, map[string]string{"书.md": strings.Repeat(line+"\n", 400)})
	out, err := readPart(dir, "书.md", lineRange{})
	if err != nil {
		t.Fatal(err)
	}
	cjk := 0
	for _, c := range out {
		if ai.CharUnits(c) == 3 {
			cjk++
		}
	}
	if cjk < 12_000 || cjk > maxReadUnits/3 || !strings.Contains(out, "more lines not shown") {
		t.Errorf("%d CJK characters", cjk)
	}
}

func TestALineLongerThanAReadShowsItsBeginning(t *testing.T) {
	dir := project(t, map[string]string{"one-line.txt": strings.Repeat("word ", 20_000)})
	out, err := readPart(dir, "one-line.txt", lineRange{})
	if err != nil || !strings.HasPrefix(out, "`one-line.txt`, lines 1-1 of 1:\nword word") ||
		!strings.HasSuffix(out, "[... line 1 is longer than one read can show; this is its beginning]") || len(out) >= maxReadUnits+200 {
		t.Errorf("got %v", err)
	}
}

func TestASmallFileReadsWholeAndARangeSaysWhereItIs(t *testing.T) {
	dir := project(t, map[string]string{"a.md": "one\ntwo\nthree\n", "empty.md": ""})
	if whole, err := readPart(dir, "@a.md", lineRange{}); err != nil || whole != "one\ntwo\nthree\n" {
		t.Errorf("got %q, %v", whole, err)
	}
	r := lineRange{start: 2, end: 9}
	if got, err := readPart(dir, "a.md", r); err != nil || got != "`a.md`, lines 2-3 of 3:\ntwo\nthree" {
		t.Errorf("got %q, %v", got, err)
	}
	if got, err := readPart(dir, "empty.md", r); err != nil || got != "`empty.md` is empty." {
		t.Errorf("got %q, %v", got, err)
	}
	if _, err := readPart(dir, ".", r); err == nil || !strings.Contains(err.Error(), "is a folder") {
		t.Errorf("got %v", err)
	}
}

func TestDocumentsAreListedReadAndSearchedAsTheirText(t *testing.T) {
	root := project(t, map[string]string{"notes.md": "Harbour notes."})
	if err := os.MkdirAll(filepath.Join(root, "research"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "research", "interview.docx"), paragraphsDocx("Maria Lopez, 12 March", "She takes the 6:40 bus."), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "map.png"), []byte{0x89, 'P', 'N', 'G'}, 0o644); err != nil {
		t.Fatal(err)
	}
	if got := walkReadableFiles(root, false).files; !slices.Equal(got, []string{"research/interview.docx", "notes.md"}) {
		t.Errorf("got %v", got)
	}
	if got := walkTextFiles(root).files; !slices.Equal(got, []string{"notes.md"}) {
		t.Errorf("got %v", got)
	}
	if got, err := readPart(root, "research/interview.docx", lineRange{}); err != nil || got != "Maria Lopez, 12 March\n\nShe takes the 6:40 bus." {
		t.Errorf("got %q, %v", got, err)
	}
	if got, err := readPart(root, "research/interview.docx", lineRange{start: 3}); err != nil || got != "`research/interview.docx`, lines 3-3 of 3:\nShe takes the 6:40 bus." {
		t.Errorf("got %q, %v", got, err)
	}
	if got := searchFiles(root, "6:40"); got != "research/interview.docx:3: She takes the 6:40 bus." {
		t.Errorf("got %q", got)
	}
	if got := findFiles(root, "interview", false); got != "research/interview.docx" {
		t.Errorf("got %q", got)
	}
}

func TestEveryDocumentExtensionHasAnArm(t *testing.T) {
	dir := t.TempDir()
	for _, ext := range documentExtensions {
		path := filepath.Join(dir, "empty."+ext)
		writeText(t, path, "")
		if _, err := extractDocument(path); err != nil && err.Error() == codeError("notDocument").Error() {
			t.Errorf("no arm for .%s", ext)
		}
	}
}

func TestSearchStopsWhenTheTimeIsUp(t *testing.T) {
	dir := project(t, map[string]string{"a.md": "beta"})
	if found := searchInOrder(dir, []string{"a.md"}, "beta", time.Now()); found.read != 0 || len(found.hits) != 0 {
		t.Errorf("got %+v", found)
	}
}

func TestFindListsDocumentsFirstAndNamesBeforePaths(t *testing.T) {
	dir := project(t, map[string]string{
		"data/chapter-3.json": "{}", "drafts/Chapter 3.md": "", "chapter-3/notes.md": "",
		"old-chapter-3.txt": "", "chapter-13.md": "",
	})
	if got := findFiles(dir, "chapter 3", false); got != "drafts/Chapter 3.md\nold-chapter-3.txt\nchapter-3/notes.md\nchapter-13.md\ndata/chapter-3.json" {
		t.Errorf("got %q", got)
	}
	if got := findFiles(dir, "  ", false); got != "No name provided." {
		t.Errorf("got %q", got)
	}
}

func TestFindCapsItsAnswer(t *testing.T) {
	files := map[string]string{}
	for i := range 60 {
		files[fmt.Sprintf("note-%d.md", i)] = ""
	}
	lines := strings.Split(findFiles(project(t, files), "NOTE", false), "\n")
	if len(lines) != maxFoundFiles+1 || lines[0] != "note-0.md" || lines[maxFoundFiles] != "... (10 more - give more of the name)" {
		t.Errorf("got %q", lines)
	}
}

func TestReferenceTermsAreWordsAndCJKPairs(t *testing.T) {
	if got := searchTerms("Tidal-power, 2021 a"); !slices.Equal(got, []string{"tidal", "power", "2021"}) {
		t.Errorf("got %q", got)
	}
	if got := searchTerms("潮汐能发电 GPU"); !slices.Equal(got, []string{"潮", "潮汐", "汐能", "能发", "发电", "gpu"}) {
		t.Errorf("got %q", got)
	}
}

func TestReferencesRankThePassageWithMostOfTheWordsFirst(t *testing.T) {
	filler := strings.Repeat("Unrelated words about gardens and weather.\n", 40)
	dir := project(t, map[string]string{
		"references/a.md":        filler + "Tidal barrages store energy at high tide.\n",
		"references/notes/b.txt": "Wind turbines make energy.\n",
		"references/c.md":        "潮汐能是一种可再生能源。\n",
		"elsewhere.md":           "Tidal barrages everywhere.\n",
	})
	out := searchReferences(dir, "tidal barrage energy barrages")
	first, _, _ := strings.Cut(out, "\n\n")
	if !strings.HasPrefix(first, "references/a.md:") || !strings.Contains(first, "Tidal barrages store energy") ||
		!strings.Contains(out, "references/notes/b.txt:1-1") || strings.Contains(out, "elsewhere") {
		t.Errorf("got %q", out)
	}
	if got := searchReferences(dir, "潮汐能"); !strings.HasPrefix(got, "references/c.md:1-1") {
		t.Errorf("got %q", got)
	}
	if got := searchReferences(dir, "volcano"); !strings.HasPrefix(got, "No passage") {
		t.Errorf("got %q", got)
	}
	// A changed file is indexed again; a deleted one is gone.
	writeText(t, filepath.Join(dir, "references", "notes", "b.txt"), "Volcano notes.\n")
	if err := os.Remove(filepath.Join(dir, "references", "c.md")); err != nil {
		t.Fatal(err)
	}
	if got := searchReferences(dir, "volcano"); !strings.HasPrefix(got, "references/notes/b.txt") {
		t.Errorf("got %q", got)
	}
	if got := searchReferences(dir, "潮汐能"); !strings.HasPrefix(got, "No passage") {
		t.Errorf("got %q", got)
	}
}

func TestWithoutAReferencesFolderTheSearchSaysWhereTheyGo(t *testing.T) {
	if got := searchReferences(project(t, map[string]string{"a.md": "Tides."}), "tides"); !strings.Contains(got, "no `references/` folder") {
		t.Errorf("got %q", got)
	}
}

func zipOf(t *testing.T, entries [][2]string) []byte {
	t.Helper()
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	for _, e := range entries {
		w, err := z.Create(e[0])
		if err != nil {
			t.Fatal(err)
		}
		w.Write([]byte(e[1]))
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestAZipUnpacksItsReadableFilesIntoItsOwnFolderOnly(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(t.TempDir(), "Tides 2024.zip")
	if err := os.WriteFile(archive, zipOf(t, [][2]string{
		{"papers/a.md", "Tidal barrages."}, {"notes.txt", "Notes."}, {"../escape.md", "Outside!"},
		{"__MACOSX/papers/._a.md", "fork"}, {"tool.exe", "binary"},
	}), 0o644); err != nil {
		t.Fatal(err)
	}
	done, err := importReferences(dir, []string{archive})
	if err != nil || done != (importedReferences{Imported: 2, Skipped: 3}) {
		t.Fatalf("got %+v, %v", done, err)
	}
	refs := filepath.Join(dir, "references", "Tides 2024")
	if !exists(filepath.Join(refs, "papers", "a.md")) || !exists(filepath.Join(refs, "notes.txt")) {
		t.Error("the readable files are not there")
	}
	if exists(filepath.Join(dir, "escape.md")) || exists(filepath.Join(dir, "references", "escape.md")) {
		t.Error("an entry left its folder")
	}
	// Again: nothing overwritten, a second copy beside it.
	if _, err := importReferences(dir, []string{archive}); err != nil || !exists(filepath.Join(refs, "papers", "a (2).md")) {
		t.Errorf("no second copy: %v", err)
	}
	if got := searchReferences(dir, "tidal barrages"); !strings.HasPrefix(got, "references/Tides 2024/") {
		t.Errorf("got %q", got)
	}
	// Not a zip at all: said so.
	bad := filepath.Join(t.TempDir(), "bad.zip")
	writeText(t, bad, "not a zip")
	if _, err := importReferences(dir, []string{bad}); err == nil || !strings.HasPrefix(err.Error(), "Not a zip file Textus can open") {
		t.Errorf("got %v", err)
	}
}

func TestAFolderAndSingleFilesImportAndTheLibraryItselfIsSkipped(t *testing.T) {
	dir := project(t, map[string]string{"references/kept.md": "Kept."})
	outside := project(t, map[string]string{
		"Sources/one.md": "One.", "Sources/deep/two.txt": "Two.", "Sources/.hidden.md": "Hidden.",
		"Sources/photo.bin": "x", "single.md": "Single.", "program.exe": "x",
	})
	done, err := importReferences(dir, []string{
		filepath.Join(outside, "Sources"), filepath.Join(outside, "single.md"),
		filepath.Join(outside, "program.exe"), filepath.Join(dir, "references"),
	})
	if err != nil || done != (importedReferences{Imported: 3, Skipped: 2}) {
		t.Fatalf("got %+v, %v", done, err)
	}
	refs := filepath.Join(dir, "references")
	for _, rel := range []string{"Sources/one.md", "Sources/deep/two.txt", "single.md"} {
		if !exists(filepath.Join(refs, filepath.FromSlash(rel))) {
			t.Errorf("%s is missing", rel)
		}
	}
	if exists(filepath.Join(refs, "Sources", ".hidden.md")) {
		t.Error("a hidden file was imported")
	}
}

func TestStyleSamplesAreThePickedTextFilesCut(t *testing.T) {
	dir := project(t, map[string]string{
		"a.md": strings.Repeat("字", styleSampleChars+10), "b.rst": "Restructured.", ".hidden/c.md": "Hidden.",
		"d.pdf": "%PDF", "e.txt": "Plain.",
	})
	var files []string
	for _, rel := range []string{"a.md", "b.rst", ".hidden/c.md", "d.pdf", "missing.md", "e.txt"} {
		files = append(files, filepath.Join(dir, filepath.FromSlash(rel)))
	}
	samples, err := styleSamples(files)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(samples, "<sample name=\"a.md\">\n"+strings.Repeat("字", styleSampleChars)+"\n</sample>\n\n") ||
		!strings.Contains(samples, "<sample name=\"b.rst\">\nRestructured.\n</sample>") || !strings.Contains(samples, "e.txt") ||
		strings.Contains(samples, "Hidden.") || strings.Contains(samples, "%PDF") {
		t.Errorf("got %q", samples[max(0, len(samples)-300):])
	}
	if _, err := styleSamples([]string{filepath.Join(dir, "d.pdf")}); !hasCode(err, "noSamples") {
		t.Errorf("got %v", err)
	}
}

func TestLearnStyleKeepsTheProfileBefore(t *testing.T) {
	root := t.TempDir()
	path, err := learnStyle(root, "  # Style\n\nShort.  \n")
	if err != nil || readText(t, path) != "# Style\n\nShort.\n" || path != filepath.Join(root, ".textus", "style.md") {
		t.Fatalf("got %s, %v", path, err)
	}
	if _, err := learnStyle(root, "# Style\n\nLong."); err != nil {
		t.Fatal(err)
	}
	if readText(t, filepath.Join(root, ".textus", "style.previous.md")) != "# Style\n\nShort.\n" {
		t.Error("the profile before was not kept")
	}
	if _, err := learnStyle(root, " \n"); err == nil {
		t.Error("an empty profile was written")
	}
}

func TestAProjectFileIsMadeOnceAndNeverOutsideTextus(t *testing.T) {
	root := t.TempDir()
	path, err := ensureProjectFile(root, "/library/lin.md", "template")
	if err != nil || path != filepath.Join(root, ".textus", "library", "lin.md") || readText(t, path) != "template" {
		t.Fatalf("got %s, %v", path, err)
	}
	writeText(t, path, "Mine.")
	if _, err := ensureProjectFile(root, "library/lin.md", "template"); err != nil || readText(t, path) != "Mine." {
		t.Errorf("the user's file was replaced: %v", err)
	}
	for _, rel := range []string{"../x.md", "a//b.md", "", `a\..\b.md`} {
		if _, err := ensureProjectFile(root, rel, ""); !hasCode(err, "badName") {
			t.Errorf("%q: got %v", rel, err)
		}
	}
}

func TestTheLibraryMarksAreWhatTheLastRequestCarried(t *testing.T) {
	root := project(t, map[string]string{
		".textus/library/lin.md":     "---\nname: Lin\n---\nTeaches Chinese.",
		".textus/library/harbour.md": "---\nname: Harbour\n---\nA port.",
		"chapter.md":                 "Lin walked.",
	})
	doc := filepath.Join(root, "chapter.md")
	p := projectArgs{DocPath: &doc}
	use := libraryUse(p, "Lin walked in.")
	if !slices.Equal(use.Included, []string{".textus/library/lin.md"}) || use.LastRequest {
		t.Errorf("got %+v", use)
	}
	// Once a request went, its marks.
	recordLibraryUse(doc, ai.PickLibrary(readLibrary(root), "Harbour"))
	if use := libraryUse(p, "Lin walked in."); !use.LastRequest || !slices.Equal(use.Included, []string{".textus/library/harbour.md"}) {
		t.Errorf("got %+v", use)
	}
	// No project: nothing.
	lone := filepath.Join(t.TempDir(), "lone.md")
	if use := libraryUse(projectArgs{DocPath: &lone}, "Lin"); len(use.Included) != 0 || use.Cap != 0 {
		t.Errorf("got %+v", use)
	}
}
