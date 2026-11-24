package backend

import (
	"os"
	"strings"
	"time"
)

const (
	maxDocumentBytes = 4 * 1024 * 1024
	maxTotalBytes    = 64 * 1024 * 1024
	maxSearchHits    = 1000
	searchTime       = 5 * time.Second
)

type IndexedFile struct {
	Path string `json:"path"`
	Rel  string `json:"rel"`
}

type FolderIndex struct {
	Files     []IndexedFile `json:"files"`
	Truncated bool          `json:"truncated"`
}

type WorkspaceDocument struct {
	Path string `json:"path"`
	Rel  string `json:"rel"`
	Text string `json:"text"`
}

type WorkspaceDocuments struct {
	Documents []WorkspaceDocument `json:"documents"`
	Truncated bool                `json:"truncated"`
}

type SearchHit struct {
	Path string `json:"path"`
	Rel  string `json:"rel"`
	Line uint32 `json:"line"` // 1-based
	Text string `json:"text"`
}

type SearchResult struct {
	Hits      []SearchHit `json:"hits"`
	Truncated bool        `json:"truncated"`
}

func indexFolder(root string, lim limits) FolderIndex {
	w := walk(root, lim, func(string) bool { return true })
	files := make([]IndexedFile, len(w.files))
	for i, f := range w.files {
		files[i] = IndexedFile{f.path, f.rel}
	}
	return FolderIndex{files, w.truncated}
}

func documentsIn(root string) WorkspaceDocuments {
	w := walk(root, walkLimits, isMarkdown)
	out := WorkspaceDocuments{Documents: []WorkspaceDocument{}, Truncated: w.truncated}
	var total int64
	for _, f := range w.files {
		info, err := os.Stat(f.path)
		if err != nil || info.Size() > maxDocumentBytes {
			continue
		}
		if total+info.Size() > maxTotalBytes {
			out.Truncated = true
			break
		}
		text, err := readUTF8(f.path)
		if err != nil {
			continue
		}
		total += info.Size()
		out.Documents = append(out.Documents, WorkspaceDocument{f.path, f.rel, text})
	}
	return out
}

func searchIn(root, query string, limit int, deadline time.Time) SearchResult {
	needle := strings.ToLower(strings.TrimSpace(query))
	out := SearchResult{Hits: []SearchHit{}}
	if needle == "" {
		return out
	}
	w := walk(root, walkLimits, isTextFile)
	out.Truncated = w.truncated
files:
	for _, f := range w.files {
		if time.Now().After(deadline) {
			out.Truncated = true
			break
		}
		info, err := os.Stat(f.path)
		if err != nil || info.Size() > maxDocumentBytes {
			continue
		}
		text, err := readUTF8(f.path)
		if err != nil {
			continue
		}
		for at, line := range splitLines(text) {
			lower := strings.ToLower(line)
			if !strings.Contains(lower, needle) {
				continue
			}
			if len(out.Hits) == limit {
				out.Truncated = true
				break files
			}
			out.Hits = append(out.Hits, SearchHit{f.path, f.rel, uint32(at + 1), excerptAround(line, lower, needle)})
		}
	}
	return out
}

// splitLines splits text as Rust's str::lines: at \n, a \r before it
// dropped, no empty last line.
func splitLines(text string) []string {
	lines := strings.Split(text, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	for i, l := range lines {
		lines[i] = strings.TrimSuffix(l, "\r")
	}
	return lines
}

// checkFolder refuses the home folder and a drive root ("tooBroad"): a walk
// there is everything the user owns.
func checkFolder(root string) error {
	if err := requireNonEmpty(root); err != nil {
		return err
	}
	if isTooBroad(root) {
		return codeError("tooBroad")
	}
	if info, err := os.Stat(root); err != nil || !info.IsDir() {
		return codeError("notFolder")
	}
	return nil
}

func init() {
	command("index_folder", func(c *Call, a struct {
		Root string `json:"root"`
	}) (FolderIndex, error) {
		if err := checkFolder(a.Root); err != nil {
			return FolderIndex{}, err
		}
		return indexFolder(a.Root, walkLimits), nil
	})
	command("folder_documents", func(c *Call, a struct {
		Root string `json:"root"`
	}) (WorkspaceDocuments, error) {
		if err := checkFolder(a.Root); err != nil {
			return WorkspaceDocuments{}, err
		}
		return documentsIn(a.Root), nil
	})
	command("search_folder_text", func(c *Call, a struct {
		Root  string `json:"root"`
		Query string `json:"query"`
	}) (SearchResult, error) {
		if err := checkFolder(a.Root); err != nil {
			return SearchResult{}, err
		}
		return searchIn(a.Root, a.Query, maxSearchHits, time.Now().Add(searchTime)), nil
	})
}
