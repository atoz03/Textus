package aicompat

import (
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

// A step's sources: the markers that stand for them in its text until
// they are numbered, and the spans of text each one backs.

const (
	markerOpen  = '\uE000'
	markerClose = '\uE001'
)

// CitationMarker is where a step's text cites its k-th (1-based) citation
// event: the number between two private-use characters, which no model
// writes.
func CitationMarker(k int) string {
	return string(markerOpen) + strconv.Itoa(k) + string(markerClose)
}

// ReplaceCitationMarkers is text with each CitationMarker replaced by what
// number makes of its index, or removed where it answers false.
func ReplaceCitationMarkers(text string, number func(k int) (string, bool)) string {
	var out strings.Builder
	rest := text
	for {
		start := strings.IndexRune(rest, markerOpen)
		if start < 0 {
			break
		}
		out.WriteString(rest[:start])
		after := rest[start+utf8.RuneLen(markerOpen):]
		end := strings.IndexRune(after, markerClose)
		if end < 0 {
			rest = after
			continue
		}
		if k, err := strconv.Atoi(after[:end]); err == nil {
			if n, ok := number(k); ok {
				out.WriteString(n)
			}
		}
		rest = after[end+utf8.RuneLen(markerClose):]
	}
	out.WriteString(rest)
	return out.String()
}

// stepCitations are one step's citations as a provider reads them: each
// page once, in the order first cited, reported as it is found.
type stepCitations struct{ urls []string }

// cite is the marker for url, reporting the page first if this step
// hasn't.
func (c *stepCitations) cite(url, title string, on EventSink) string {
	k := slices.Index(c.urls, url) + 1
	if k == 0 {
		c.urls = append(c.urls, url)
		on(ProviderEvent{Kind: EventCitation, URL: url, Title: title})
		k = len(c.urls)
	}
	return CitationMarker(k)
}

// citedSpan is a span of a reply's text that cites a page: character (not
// byte) offsets, end exclusive.
type citedSpan struct {
	start, end int
	url, title string
}

// markCitedSpans is text with each cited span marked. A span that is the
// provider's inline link for its citation gives way to the marker; plain
// prose keeps its words and gets the marker after. A span that doesn't fit
// is cited at the end.
func markCitedSpans(text string, spans []citedSpan, c *stepCitations, on EventSink) string {
	chars := []rune(text)
	slices.SortStableFunc(spans, func(a, b citedSpan) int {
		if a.start != b.start {
			return a.start - b.start
		}
		return a.end - b.end
	})
	var out, trailing strings.Builder
	at := 0
	for _, span := range spans {
		marker := c.cite(span.url, span.title, on)
		if span.start < at || span.end > len(chars) || span.start > span.end {
			if !strings.Contains(out.String(), marker) && !strings.Contains(trailing.String(), marker) {
				trailing.WriteString(marker)
			}
			continue
		}
		out.WriteString(string(chars[at:span.start]))
		cited := string(chars[span.start:span.end])
		if strings.Contains(cited, "](") || strings.Contains(cited, "http") {
			// "fact ([site](url))." reads "fact [1]." - the parentheses
			// around the link go with it, whether or not the span has them.
			sofar := out.String()
			wrapped := strings.HasSuffix(sofar, "(") && span.end < len(chars) && chars[span.end] == ')'
			if wrapped {
				out.Reset()
				out.WriteString(strings.TrimSuffix(sofar, "("))
			}
			out.WriteString(marker)
			at = span.end
			if wrapped {
				at++
			}
		} else {
			out.WriteString(cited)
			out.WriteString(marker)
			at = span.end
		}
	}
	out.WriteString(string(chars[min(at, len(chars)):]))
	out.WriteString(trailing.String())
	return out.String()
}
