package backend

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

// Searching the project folder's files for words, and finding files by
// name - each bounded in matches and time.

const (
	// maxSearchMatches are the most lines search_files answers.
	maxSearchMatches = 40
	// maxFoundFiles are the most names find_files answers: past this the
	// answer is a longer name.
	maxFoundFiles = 50
	// fileSearchTime is how long search_files reads files once they are
	// listed: a disk of code must not hold up the request.
	fileSearchTime = 5 * time.Second
)

// searchFiles is a case-insensitive line search across the project's text
// files and documents, as `path:line: text`. Capped so a common word can't
// flood the context.
func searchFiles(root, query string) string {
	needle := strings.ToLower(strings.TrimSpace(query))
	if needle == "" {
		return "No query provided."
	}
	walked := walkReadableFiles(root, false)
	found := searchInOrder(root, walked.files, needle, time.Now().Add(fileSearchTime))
	hits := found.hits
	switch {
	case len(hits) > maxSearchMatches:
		hits = append(hits[:maxSearchMatches], fmt.Sprintf("... (stopped at %d matches)", maxSearchMatches))
	case found.read < len(walked.files):
		hits = append(hits, fmt.Sprintf("... (stopped after %d of %d files - the search took too long; a more specific word, or find_files for a file by name, is faster)", found.read, len(walked.files)))
	case walked.truncated:
		hits = append(hits, walkTruncatedNote)
	}
	if len(hits) == 0 {
		return fmt.Sprintf("No matches for \"%s\" in the project folder.", query)
	}
	return strings.Join(hits, "\n")
}

// foundLines is what searchInOrder found: the hits in file order, then
// line order (more than maxSearchMatches when there are more), and how
// many of the files, from the first, were read.
type foundLines struct {
	hits []string
	read int
}

// searchInOrder is the matching lines of files, read on every core. The
// search stops once the files before the first unread one hold enough
// matches, so the results are the same however the goroutines ran.
func searchInOrder(root string, files []string, needle string, deadline time.Time) foundLines {
	slots := make([][]string, len(files))
	filled := make([]bool, len(files))
	var next atomic.Int64
	var stop atomic.Bool
	var mu sync.Mutex
	// The files read in a row from the first, and the matches in them.
	read, matches := 0, 0
	workers := min(max(runtime.NumCPU(), 1), 8, len(files))
	var wg sync.WaitGroup
	for range workers {
		wg.Go(func() {
			for !stop.Load() {
				if !time.Now().Before(deadline) {
					stop.Store(true)
					return
				}
				i := int(next.Add(1) - 1)
				if i >= len(files) {
					return
				}
				lines := matchingLines(root, files[i], needle)
				mu.Lock()
				slots[i], filled[i] = lines, true
				for read < len(files) && filled[read] {
					matches += len(slots[read])
					read++
				}
				if matches > maxSearchMatches {
					stop.Store(true)
				}
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	var hits []string
	for _, slot := range slots[:read] {
		hits = append(hits, slot...)
		if len(hits) > maxSearchMatches {
			break
		}
	}
	return foundLines{hits: hits, read: read}
}

// matchingLines is one file's lines holding needle (lowercase), at most
// one more than a search answers, numbered as read_file numbers them. Text
// is read a line at a time; unreadable files and failed extractions have
// none.
func matchingLines(root, rel, needle string) []string {
	path := filepath.Join(root, rel)
	var hits []string
	add := func(number int, line string) bool {
		if strings.Contains(strings.ToLower(line), needle) {
			hits = append(hits, fmt.Sprintf("%s:%d: %s", rel, number, charPrefixOf(strings.TrimSpace(line), 200)))
		}
		return len(hits) <= maxSearchMatches
	}
	if fileKindOf(path) == kindDocument {
		text, err := documentText(path)
		if err != nil {
			return nil
		}
		for i, line := range splitLines(text) {
			if !add(i+1, line) {
				break
			}
		}
		return hits
	}
	if sizeOf(path) > maxTextBytes {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	reader := bufio.NewReader(f)
	for number := 1; ; number++ {
		line, err := reader.ReadString('\n')
		if line == "" && err != nil {
			break
		}
		// Not UTF-8, or a NUL byte: a binary file, whatever its name.
		if strings.ContainsRune(line, 0) || !utf8.ValidString(line) {
			return nil
		}
		text := strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
		if !add(number, text) || err != nil {
			break
		}
	}
	return hits
}

// foldName is a name as find_files compares it: NFC (macOS hands names
// over decomposed), no case, and `-`/`_` as spaces, so "chapter 3" finds
// chapter-3.md.
func foldName(text string) string {
	return strings.Map(func(c rune) rune {
		if c == '-' || c == '_' {
			return ' '
		}
		return c
	}, strings.ToLower(norm.NFC.String(text)))
}

// findFiles is the project files whose path holds every word of query,
// ignoring case; images too when the model can see. Documents first, then
// names that start with the query, contain it, then paths; at most
// maxFoundFiles.
func findFiles(root, query string, images bool) string {
	words := strings.Fields(foldName(strings.TrimSpace(query)))
	if len(words) == 0 {
		return "No name provided."
	}
	whole := strings.Join(words, " ")
	walked := walkReadableFiles(root, images)
	// document: name starts, name contains, path holds; other: the same.
	var groups [6][]string
	for _, rel := range walked.files {
		key := foldName(rel)
		all := true
		for _, w := range words {
			all = all && strings.Contains(key, w)
		}
		if !all {
			continue
		}
		name := key[strings.LastIndex(key, "/")+1:]
		class := 2
		if strings.HasPrefix(name, whole) {
			class = 0
		} else if strings.Contains(name, whole) {
			class = 1
		}
		other := 0
		if !isTextFile(rel) {
			other = 1
		}
		groups[other*3+class] = append(groups[other*3+class], rel)
	}
	var found []string
	for _, g := range groups {
		found = append(found, g...)
	}
	if len(found) == 0 {
		reply := fmt.Sprintf("No file in the project folder has \"%s\" in its name or path. search_files looks inside files.", query)
		if walked.truncated {
			reply += "\n" + walkTruncatedNote
		}
		return reply
	}
	var lines []string
	for _, rel := range found[:min(len(found), maxFoundFiles)] {
		lines = append(lines, listedFile(rel))
	}
	if len(found) > maxFoundFiles {
		lines = append(lines, fmt.Sprintf("... (%d more - give more of the name)", len(found)-maxFoundFiles))
	} else if walked.truncated {
		lines = append(lines, walkTruncatedNote)
	}
	return strings.Join(lines, "\n")
}
