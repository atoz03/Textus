package backend

import (
	"cmp"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"textus/backend/ai/rules"
)

// The project's reference library: the papers, books and notes in its
// references/ folder, searched by meaning-bearing words rather than one
// exact phrase, and answered with the best passages (BM25), each pointing
// at the lines read_file opens. Each file is extracted and indexed once per
// version of it; the index lives in memory for this run of the app.

const (
	// referencesDir is the folder, at the project's top, that holds the
	// references.
	referencesDir = "references"
	// maxPassages are the passages one search answers.
	maxPassages = 8
	// passageChars: a passage is whole lines up to about this many
	// characters.
	passageChars = 1_200
	// shownChars is what of a passage is shown; read_file has the rest.
	shownChars = 700
	// indexTime is how long one search may spend extracting new or changed
	// files; the rest wait for the next search.
	indexTime = 8 * time.Second
	// bm25K1 and bm25B are BM25's usual constants.
	bm25K1 = 1.2
	bm25B  = 0.75
)

// passage is lines first to last of a file, and the words in them.
type passage struct {
	first, last int
	text        string
	terms       map[string]int
	len         int
}

type indexedFile struct {
	size     int64
	modified time.Time
	passages []passage
}

// referenceIndexes are each project's indexed files, by path relative to
// its references folder.
var referenceIndexes struct {
	sync.Mutex
	byRoot map[string]map[string]*indexedFile
}

// referenceIndex is root's index; the caller holds the lock.
func referenceIndex(root string) map[string]*indexedFile {
	if referenceIndexes.byRoot == nil {
		referenceIndexes.byRoot = map[string]map[string]*indexedFile{}
	}
	index, ok := referenceIndexes.byRoot[root]
	if !ok {
		index = map[string]*indexedFile{}
		referenceIndexes.byRoot[root] = index
	}
	return index
}

// searchTerms are the words of text, lowercased: runs of letters and
// digits, and in Chinese and Japanese, which have no spaces, every two
// neighbouring characters.
func searchTerms(text string) []string {
	var out []string
	var word strings.Builder
	var prevCJK rune
	flush := func() {
		if utf8.RuneCountInString(word.String()) >= 2 {
			out = append(out, word.String())
		}
		word.Reset()
	}
	for _, c := range strings.ToLower(text) {
		switch {
		case rules.IsCJK(c):
			flush()
			if prevCJK != 0 {
				out = append(out, string(prevCJK)+string(c))
			} else {
				out = append(out, string(c))
			}
			prevCJK = c
		case rules.IsAlphanumeric(c):
			prevCJK = 0
			word.WriteRune(c)
		default:
			prevCJK = 0
			flush()
		}
	}
	flush()
	return out
}

// passagesOf is text cut into passages of whole lines, numbered from 1 as
// read_file numbers them.
func passagesOf(text string) []passage {
	var out []passage
	var held []string
	first, size := 1, 0
	flush := func(last int) {
		joined := strings.Join(held, "\n")
		held = held[:0]
		words := searchTerms(joined)
		if len(words) == 0 {
			return
		}
		counts := map[string]int{}
		for _, w := range words {
			counts[w]++
		}
		out = append(out, passage{first: first, last: last, text: joined, terms: counts, len: len(words)})
	}
	for i, line := range splitLines(text) {
		if len(held) == 0 {
			first, size = i+1, 0
		}
		held = append(held, line)
		size += utf8.RuneCountInString(line) + 1
		if size >= passageChars {
			flush(i + 1)
		}
	}
	if len(held) > 0 {
		flush(first + len(held) - 1)
	}
	return out
}

// referenceText is the text of one reference, extracted or read.
func referenceText(path, rel string) (string, error) {
	switch fileKindOf(path) {
	case kindDocument:
		return documentText(path)
	case kindText:
		if sizeOf(path) <= maxSourceBytes {
			return wholeText(path, rel)
		}
	}
	return "", fmt.Errorf("`%s` isn't readable.", rel)
}

// refreshIndex brings index up to date with the folder: new and changed
// files indexed until deadline, deleted ones dropped. It answers how many
// still wait.
func refreshIndex(index map[string]*indexedFile, dir string, deadline time.Time) int {
	walked := walkReadableFiles(dir, false)
	there := make(map[string]bool, len(walked.files))
	for _, rel := range walked.files {
		there[rel] = true
	}
	for rel := range index {
		if !there[rel] {
			delete(index, rel)
		}
	}
	waiting := 0
	for _, rel := range walked.files {
		path := filepath.Join(dir, rel)
		var size int64
		var modified time.Time
		if info, err := os.Stat(path); err == nil {
			size, modified = info.Size(), info.ModTime()
		}
		if f, ok := index[rel]; ok && f.size == size && f.modified.Equal(modified) {
			continue
		}
		if !time.Now().Before(deadline) {
			waiting++
			continue
		}
		var passages []passage
		if text, err := referenceText(path, rel); err == nil {
			passages = passagesOf(text)
		}
		index[rel] = &indexedFile{size: size, modified: modified, passages: passages}
	}
	return waiting
}

// warmReferences indexes all of root's references now, however long it
// takes: after an import, so the first search finds them ready.
func warmReferences(root string) {
	referenceIndexes.Lock()
	defer referenceIndexes.Unlock()
	refreshIndex(referenceIndex(root), filepath.Join(root, referencesDir), time.Now().Add(10*time.Minute))
}

// searchReferences is the passages of root's references that best answer
// query, best first, as `references/<file>:<first>-<last>` and the passage.
func searchReferences(root, query string) string {
	dir := filepath.Join(root, referencesDir)
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return fmt.Sprintf("This project has no `%s/` folder, so there are no references to search. Papers, books and notes put in it are searched here.", referencesDir)
	}
	wanted := searchTerms(query)
	slices.Sort(wanted)
	wanted = slices.Compact(wanted)
	if len(wanted) == 0 {
		return "Pass `query`: the words the passages should contain."
	}
	referenceIndexes.Lock()
	defer referenceIndexes.Unlock()
	index := referenceIndex(root)
	waiting := refreshIndex(index, dir, time.Now().Add(indexTime))

	type found struct {
		rel string
		p   *passage
	}
	var all []found
	total := 0.0
	for rel, f := range index {
		for i := range f.passages {
			all = append(all, found{rel, &f.passages[i]})
			total += float64(f.passages[i].len)
		}
	}
	n := float64(len(all))
	avg := total / max(n, 1)
	idf := make([]float64, len(wanted))
	for i, w := range wanted {
		df := 0.0
		for _, a := range all {
			if _, ok := a.p.terms[w]; ok {
				df++
			}
		}
		idf[i] = math.Log((n-df+0.5)/(df+0.5) + 1)
	}
	type scored struct {
		score float64
		found
	}
	var ranked []scored
	for _, a := range all {
		score := 0.0
		for i, w := range wanted {
			if tf, ok := a.p.terms[w]; ok {
				f := float64(tf)
				score += idf[i] * f * (bm25K1 + 1) / (f + bm25K1*(1-bm25B+bm25B*float64(a.p.len)/avg))
			}
		}
		if score > 0 {
			ranked = append(ranked, scored{score, a})
		}
	}
	slices.SortFunc(ranked, func(a, b scored) int {
		return cmp.Or(cmp.Compare(b.score, a.score), cmp.Compare(a.rel, b.rel), cmp.Compare(a.p.first, b.p.first))
	})
	var out []string
	for _, s := range ranked[:min(len(ranked), maxPassages)] {
		shown := charPrefixOf(s.p.text, shownChars)
		if utf8.RuneCountInString(s.p.text) > shownChars {
			shown += "…"
		}
		out = append(out, fmt.Sprintf("%s/%s:%d-%d\n%s", referencesDir, s.rel, s.p.first, s.p.last, shown))
	}
	if len(out) == 0 {
		out = append(out, fmt.Sprintf("No passage in the %d indexed references matches \"%s\" - try other words, or the words the sources themselves would use.", len(index), query))
	}
	if waiting > 0 {
		out = append(out, fmt.Sprintf("... (%d files not indexed yet - search again for them)", waiting))
	}
	return strings.Join(out, "\n\n")
}

// charPrefixOf is s's first n characters.
func charPrefixOf(s string, n int) string {
	for i := range s {
		if n == 0 {
			return s[:i]
		}
		n--
	}
	return s
}
