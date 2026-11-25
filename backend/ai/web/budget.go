package web

import (
	"strings"
	"sync"
	"unicode/utf8"
)

// How many searches, page reads and paper searches web_search, web_fetch
// and search_papers may make - per request by default, or a budget shared
// by several runs (a paper's research helpers) - and what each source said
// when it was shown.

// Per request: the searches and page reads the model may make before it is
// told to answer with what it has.
const (
	MaxSearches      = 5
	MaxFetches       = 5
	MaxPaperSearches = 5
)

// Use is what a budget counts.
type Use int

const (
	Searches Use = iota
	Fetches
	// PaperSearches are search_papers calls.
	PaperSearches
)

// Budget is what a Plan counts its searches and page reads against.
type Budget interface {
	// Take counts one use of kind; false, and nothing counted, when none
	// is left.
	Take(kind Use) bool
	// Limit is how many of kind it allows in all, for the prompt and the
	// refusal.
	Limit(kind Use) int
}

// perRequest is one request's own limits: what chat has always had.
type perRequest struct {
	mu   sync.Mutex
	used [3]int
}

func (b *perRequest) Take(kind Use) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.used[kind] >= b.Limit(kind) {
		return false
	}
	b.used[kind]++
	return true
}

func (b *perRequest) Limit(kind Use) int {
	return [...]int{MaxSearches, MaxFetches, MaxPaperSearches}[kind]
}

// maxSeenChars is the most kept for one source: a page read in full is
// cut at MaxPageChars already; snippets from many searches add up.
const maxSeenChars = 40_000

// Reach is how much of a source the model saw.
type Reach int

const (
	// Snippet is a search result's snippet or a paper's abstract.
	Snippet Reach = iota
	// Read is the page itself, read with web_fetch.
	Read
)

// Seen is one source's words, as shown.
type Seen struct {
	Text  string
	Reach Reach
}

// seenText is what each source said when it was shown to the model - a
// page's text, a result's snippet, a paper's abstract - by its number, so
// a note taken from it can be checked against its words.
type seenText map[uint32]*Seen

// record keeps text as shown for source n: added to what it had, unless it
// had it already; a page read makes the source Read.
func (s seenText) record(n uint32, text string, reach Reach) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	seen := s[n]
	if seen == nil {
		seen = &Seen{Reach: reach}
		s[n] = seen
	}
	if reach == Read {
		seen.Reach = Read
	}
	had := utf8.RuneCountInString(seen.Text)
	if strings.Contains(seen.Text, text) || had >= maxSeenChars {
		return
	}
	if seen.Text != "" {
		seen.Text += "\n\n"
		had += 2
	}
	seen.Text += firstChars(text, max(maxSeenChars-had, 0))
}

// firstChars is s's first n characters.
func firstChars(s string, n int) string {
	for i := range s {
		if n == 0 {
			return s[:i]
		}
		n--
	}
	return s
}
