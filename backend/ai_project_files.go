package backend

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"textus/backend/ai"
	"textus/backend/extract"
	"textus/backend/plugins"
)

// The project folder's files as Textus AI reads them: which are text, the
// walk over them, and reading one whole or in parts (documents through
// extract/, cached).

const (
	// maxMentionBytes is the most an @-mention inlines: the whole file goes
	// into the message.
	maxMentionBytes = 100 << 10
	// maxTextBytes is the largest text file read_file and search_files
	// read: a whole book, well short of a log or data dump.
	maxTextBytes = 16 << 20
	// maxReadUnits is the most of a file one read_file call returns, in
	// char units (CJK counts 3): a chapter, not a book.
	maxReadUnits = 40_000
	// maxImageBytes: images go to the provider as base64 (4/3 larger) and
	// providers cap request size.
	maxImageBytes = 6 << 20
	// walkTruncatedNote is said after a listing that hit the walk's limits.
	walkTruncatedNote = "... (the project folder is too large to go through completely; some files were not looked at)"
)

// agentTextExtensions are read as text by listing, search and @-mentions.
var agentTextExtensions = []string{
	"md", "markdown", "mdx", "txt", "text", "rst", "org", "tex", "csv", "tsv", "json", "yaml",
	"yml", "toml", "html", "htm", "xml",
}

// documentExtensions are the formats read as the Markdown extracted from
// them (an .html file is text: source to read as written).
var documentExtensions = []string{
	"pdf", "docx", "pptx", "xlsx", "xlsm", "xlsb", "xls", "ods", "epub", "rtf", "html", "htm", "xhtml",
}

// imageMime is the type of the image formats every vision-capable
// provider accepts; "" for anything else.
func imageMime(ext string) string {
	switch ext {
	case "png":
		return "image/png"
	case "jpg", "jpeg":
		return "image/jpeg"
	case "gif":
		return "image/gif"
	case "webp":
		return "image/webp"
	}
	return ""
}

func isAgentTextFile(path string) bool {
	ext := fileExt(path)
	return slices.Contains(agentTextExtensions, ext) || plugins.IsDocumentType(ext)
}

type fileKind int

const (
	kindNone fileKind = iota
	kindText
	// kindDocument is PDF, Word, PowerPoint, a spreadsheet, EPUB or RTF,
	// read as the Markdown extracted from it.
	kindDocument
	// kindImage is shown to a model that can see.
	kindImage
)

func fileKindOf(path string) fileKind {
	ext := fileExt(path)
	switch {
	case slices.Contains(agentTextExtensions, ext), plugins.IsDocumentType(ext):
		return kindText
	case slices.Contains(documentExtensions, ext):
		return kindDocument
	case imageMime(ext) != "":
		return kindImage
	}
	return kindNone
}

// projectFiles are a walk's files as relative paths, in the sidebar's tree
// order, and whether it hit a limit.
type projectFiles struct {
	files     []string
	truncated bool
}

func walkFiles(root string, keep func(path string) bool) projectFiles {
	w := walk(root, walkLimits, keep)
	out := projectFiles{files: []string{}, truncated: w.truncated}
	for _, f := range w.files {
		out.files = append(out.files, f.rel)
	}
	return out
}

// walkTextFiles is every text file under root: what an @-mention can
// attach.
func walkTextFiles(root string) projectFiles { return walkFiles(root, isAgentTextFile) }

// walkReadableFiles is every file the agent's tools can read: text files
// and documents, and images when the model can see them.
func walkReadableFiles(root string, images bool) projectFiles {
	return walkFiles(root, func(path string) bool {
		k := fileKindOf(path)
		return k == kindText || k == kindDocument || k == kindImage && images
	})
}

// listedFile is a file as find_files shows it: images marked, so the
// model knows read_file shows them rather than reads them.
func listedFile(rel string) string {
	if fileKindOf(rel) == kindImage {
		return rel + " (image)"
	}
	return rel
}

// resolveIn is rel inside root, checked: canonical containment (no `..`,
// absolute paths or links out) and no hidden parts. It answers the path
// and rel as the model should see it.
func resolveIn(root, rel string) (string, string, error) {
	rel = strings.TrimLeft(strings.TrimSpace(rel), "@")
	if rel == "" {
		return "", "", errors.New("No path provided.")
	}
	rootCanon, err1 := filepath.EvalSymlinks(root)
	path, err2 := filepath.EvalSymlinks(filepath.Join(root, rel))
	if err1 != nil || err2 != nil {
		return "", "", fmt.Errorf("`%s` doesn't exist. Use find_files to see what's there.", rel)
	}
	parts, inside := pathUnder(path, rootCanon)
	if !inside {
		return "", "", errors.New("That path is outside the project folder - only files inside it can be read.")
	}
	// Hidden entries are never needed for writing, and in a project at ~
	// they would put .ssh or .env one guessed path away from the provider.
	if slices.ContainsFunc(parts, func(p string) bool { return strings.HasPrefix(p, ".") }) {
		return "", "", errors.New(`Hidden files and folders (names starting with ".") can't be read.`)
	}
	if info, err := os.Stat(path); err == nil && info.IsDir() {
		return "", "", fmt.Errorf("`%s` is a folder, not a file.", rel)
	}
	return path, rel, nil
}

func sizeOf(path string) int64 {
	if info, err := os.Stat(path); err == nil {
		return info.Size()
	}
	return 0
}

func megabytes(bytes int64) string { return fmt.Sprintf("%.1f MB", float64(bytes)/(1024*1024)) }

// wholeText is the whole text of path, or why it can't be read as text. A
// NUL byte is a binary file with a text-looking extension.
func wholeText(path, rel string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil || !utf8.Valid(raw) || strings.ContainsRune(string(raw), 0) {
		return "", fmt.Errorf("`%s` isn't a readable text file.", rel)
	}
	return string(raw), nil
}

// readMention is rel from inside root whole, for an @-mention: text only,
// size-capped.
func readMention(root, rel string) (string, error) {
	path, rel, err := resolveIn(root, rel)
	if err != nil {
		return "", err
	}
	if size := sizeOf(path); size > maxMentionBytes {
		return "", fmt.Errorf("`%s` is too large to read (%d KB; the limit is %d KB).", rel, size/1024, maxMentionBytes/1024)
	}
	return wholeText(path, rel)
}

// extracted is a document's text, kept while the file is unchanged, so
// reading it in parts or searching it again doesn't extract it each call.
type extracted struct {
	path     string
	size     int64
	modified time.Time
	text     string
}

const (
	maxCachedDocuments = 16
	maxCachedBytes     = 32 << 20
)

var extractedCache struct {
	sync.Mutex
	list []extracted
}

// documentText is the text of a document (kindDocument), extracted once
// per version of the file.
func documentText(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if info.Size() > maxSourceBytes {
		return "", fmt.Errorf("It is too large to read (%s; the limit is %s).", megabytes(info.Size()), megabytes(maxSourceBytes))
	}
	extractedCache.Lock()
	for i, e := range extractedCache.list {
		if e.path == path && e.size == info.Size() && e.modified.Equal(info.ModTime()) {
			extractedCache.list = append([]extracted{e}, slices.Delete(extractedCache.list, i, i+1)...)
			extractedCache.Unlock()
			return e.text, nil
		}
	}
	extractedCache.Unlock()
	// Extracted outside the lock: a slow file holds up no other read.
	text, err := extractDocument(path)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(text) == "" {
		return "", codeError("noText")
	}
	extractedCache.Lock()
	defer extractedCache.Unlock()
	list := slices.DeleteFunc(extractedCache.list, func(e extracted) bool { return e.path == path })
	list = append([]extracted{{path, info.Size(), info.ModTime(), text}}, list...)
	total, keep := 0, 0
	for _, e := range list {
		total += len(e.text)
		if keep >= maxCachedDocuments || total > maxCachedBytes {
			break
		}
		keep++
	}
	extractedCache.list = list[:max(keep, 1)]
	return text, nil
}

// extractDocument is the Markdown of a document, by its extension.
func extractDocument(path string) (string, error) {
	switch fileExt(path) {
	case "docx":
		return extract.DocxMarkdown(path)
	case "pptx":
		return extract.PptxMarkdown(path)
	case "html", "htm", "xhtml":
		raw, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		return extract.HTMLMarkdown(strings.ToValidUTF8(string(raw), "\uFFFD")), nil
	}
	return "", codeError("notDocument")
}

// lineUnits is a line's weight, its line break included.
func lineUnits(line string) int {
	n := 1
	for _, c := range line {
		n += ai.CharUnits(c)
	}
	return n
}

// lineRange is which lines read_file shows: 1-based and inclusive; 0
// reads from the start / to the end.
type lineRange struct{ start, end int }

// readPart is read_file: rel's text, or the part r asks for, at most
// maxReadUnits; a larger file's answer says how to read on. Images here
// are ones the model can't see.
func readPart(root, rel string, r lineRange) (string, error) {
	path, rel, err := resolveIn(root, rel)
	if err != nil {
		return "", err
	}
	var text string
	switch fileKindOf(path) {
	case kindImage:
		return "", fmt.Errorf("`%s` is an image, and the model in use can't see images - ask the user to describe it, or to switch to a provider that can.", rel)
	case kindDocument:
		if text, err = documentText(path); err != nil {
			return "", fmt.Errorf("`%s` can't be read: %v", rel, err)
		}
	default:
		if size := sizeOf(path); size > maxTextBytes {
			return "", fmt.Errorf("`%s` is too large to read (%s; the limit is %s).", rel, megabytes(size), megabytes(maxTextBytes))
		}
		if text, err = wholeText(path, rel); err != nil {
			return "", err
		}
	}
	return excerpt(rel, text, r), nil
}

// excerpt is the lines of text r asks for, cut to maxReadUnits.
func excerpt(rel, text string, r lineRange) string {
	all := splitLines(text)
	total := len(all)
	if total == 0 {
		return fmt.Sprintf("`%s` is empty.", rel)
	}
	start := min(max(or0(r.start, 1), 1), total) - 1
	end := min(max(or0(r.end, total), start+1), total)
	var out strings.Builder
	used, shownEnd, longLine := 0, start, false
	for _, line := range all[start:end] {
		units := lineUnits(line)
		if used+units > maxReadUnits {
			// One line longer than a whole read (a PDF without line breaks,
			// minified data): its beginning, rather than nothing at all.
			if out.Len() == 0 {
				budget := maxReadUnits
				for _, c := range line {
					if ai.CharUnits(c) > budget {
						break
					}
					budget -= ai.CharUnits(c)
					out.WriteRune(c)
				}
				out.WriteByte('\n')
				shownEnd++
				longLine = true
			}
			break
		}
		out.WriteString(line + "\n")
		used += units
		shownEnd++
	}
	ranged := r.start != 0 || r.end != 0
	if !ranged && shownEnd == total && !longLine {
		return text
	}
	reply := fmt.Sprintf("`%s`, lines %d-%d of %d:\n%s", rel, start+1, shownEnd, total, out.String())
	switch {
	case longLine:
		reply += fmt.Sprintf("[... line %d is longer than one read can show; this is its beginning]", shownEnd)
	case shownEnd < end && ranged:
		reply += fmt.Sprintf("[... cut for length - continue with start_line=%d]", shownEnd+1)
	case shownEnd < end:
		reply += fmt.Sprintf("[... %d more lines not shown - read on with start_line=%d, or read the lines around a match search_files found]", total-shownEnd, shownEnd+1)
	}
	return strings.TrimRightFunc(reply, unicode.IsSpace)
}

func or0(n, fallback int) int {
	if n == 0 {
		return fallback
	}
	return n
}
