package web

import (
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"textus/backend/ai/aicompat"
)

// The web pages a conversation has drawn on, numbered once for the whole
// conversation: [3] in a later reply is the same page as in an earlier one.

// maxUncitedListed is the most sources listed under a reply that cites
// none by number (a provider that searched without marking where).
const maxUncitedListed = 8

// SourceBook is the conversation's sources by number.
type SourceBook struct {
	sources []aicompat.WebSource
	// seeded is how many came from earlier replies (the first ones).
	seeded int
	// step is the numbers the step in flight's native citations got, in
	// the order the provider reported them.
	step []uint32
}

// samePage is a URL without its fragment or trailing slash: one page, one
// number.
func samePage(u string) string {
	u, _, _ = strings.Cut(strings.TrimSpace(u), "#")
	return strings.TrimRight(u, "/")
}

// Host is the site a URL is on, without "www."; the URL itself when it has
// none.
func Host(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Hostname() == "" {
		return raw
	}
	host := strings.ToLower(u.Hostname())
	for strings.HasPrefix(host, "www.") {
		host = host[len("www."):]
	}
	return host
}

var (
	// numberPattern is `[n]` in a reply, the way the model is asked to cite.
	numberPattern = regexp.MustCompile(`\[(\d{1,3})\]`)
	// linkPattern is `[n](url)` in text proposed for the document.
	linkPattern = regexp.MustCompile(`\[(\d{1,3})\]\((https?://[^)\s]+)\)`)
)

// sourceBookFrom is the book as earlier replies in history left it.
func sourceBookFrom(history []aicompat.AgentTurn) SourceBook {
	var b SourceBook
	for _, turn := range history {
		if turn.Kind != aicompat.Assistant {
			continue
		}
		for _, s := range turn.Sources {
			if _, found := b.find(s.URL); !found && !b.Has(s.N) {
				b.sources = append(b.sources, s)
			}
		}
	}
	b.seeded = len(b.sources)
	return b
}

func (b *SourceBook) find(u string) (aicompat.WebSource, bool) {
	page := samePage(u)
	for _, s := range b.sources {
		if samePage(s.URL) == page {
			return s, true
		}
	}
	return aicompat.WebSource{}, false
}

// Add is the number of the page at u, given one if it has none yet.
func (b *SourceBook) Add(u, title string) uint32 {
	page, title := samePage(u), strings.TrimSpace(title)
	for i, s := range b.sources {
		if samePage(s.URL) == page {
			if s.Title == "" && title != "" {
				b.sources[i].Title = title
			}
			return s.N
		}
	}
	var n uint32
	for _, s := range b.sources {
		n = max(n, s.N)
	}
	b.sources = append(b.sources, aicompat.WebSource{N: n + 1, URL: strings.TrimSpace(u), Title: title})
	return n + 1
}

// Address is source n's address, if there is one.
func (b *SourceBook) Address(n uint32) (string, bool) {
	for _, s := range b.sources {
		if s.N == n {
			return s.URL, true
		}
	}
	return "", false
}

// Has reports whether some source has number n.
func (b *SourceBook) Has(n uint32) bool {
	return slices.ContainsFunc(b.sources, func(s aicompat.WebSource) bool { return s.N == n })
}

// All is every source, in number order.
func (b *SourceBook) All() []aicompat.WebSource {
	all := slices.Clone(b.sources)
	slices.SortStableFunc(all, func(x, y aicompat.WebSource) int { return int(x.N) - int(y.N) })
	return all
}

// citeNative is a page the provider's own search cited in the step in
// flight.
func (b *SourceBook) citeNative(u, title string) {
	b.step = append(b.step, b.Add(u, title))
}

// resolveStep is text with the step's citation markers as `[n]`; the step
// is done.
func (b *SourceBook) resolveStep(text string) string {
	step := b.step
	b.step = nil
	return aicompat.ReplaceCitationMarkers(text, func(k int) (string, bool) {
		if k < 1 || k > len(step) {
			return "", false
		}
		return "[" + strconv.FormatUint(uint64(step[k-1]), 10) + "]", true
	})
}

// endStep ends a step whose text isn't kept (one that called tools).
func (b *SourceBook) endStep() { b.step = nil }

// forReply is the sources to list under a reply: the ones reply cites as
// [n] and its proposals cite as [n](url), in number order. A reply that
// cites nothing by number lists what this exchange found instead.
func (b *SourceBook) forReply(reply string, proposed []string) []aicompat.WebSource {
	var cited []uint32
	for _, m := range numberPattern.FindAllStringSubmatch(reply, -1) {
		if n, err := strconv.ParseUint(m[1], 10, 32); err == nil {
			cited = append(cited, uint32(n))
		}
	}
	for _, text := range proposed {
		for _, m := range linkPattern.FindAllStringSubmatch(text, -1) {
			cited = append(cited, b.Add(m[2], ""))
		}
	}
	var listed []aicompat.WebSource
	if len(cited) == 0 {
		found := b.sources[b.seeded:]
		listed = slices.Clone(found[:min(len(found), maxUncitedListed)])
	} else {
		for _, s := range b.sources {
			if slices.Contains(cited, s.N) {
				listed = append(listed, s)
			}
		}
	}
	for i := range listed {
		if listed[i].Title == "" {
			listed[i].Title = Host(listed[i].URL)
		}
	}
	slices.SortStableFunc(listed, func(x, y aicompat.WebSource) int { return int(x.N) - int(y.N) })
	return listed
}

// WithSourcesInText is history as the model is shown it: each reply
// followed by its sources, so "is [2] reliable?" knows what is meant.
// Identical on every replay, for the prompt cache.
func WithSourcesInText(history []aicompat.AgentTurn) []aicompat.AgentTurn {
	out := make([]aicompat.AgentTurn, len(history))
	for i, turn := range history {
		if turn.Kind == aicompat.Assistant && len(turn.Sources) > 0 {
			lines := make([]string, len(turn.Sources))
			for j, s := range turn.Sources {
				lines[j] = "[" + strconv.FormatUint(uint64(s.N), 10) + "] " + s.Title + " - " + s.URL
			}
			turn.Text += "\n\nSources:\n" + strings.Join(lines, "\n")
		}
		out[i] = turn
	}
	return out
}
