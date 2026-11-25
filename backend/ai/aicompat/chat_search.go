package aicompat

import (
	"reflect"
	"slices"
	"strings"
	"unicode/utf8"
)

// A vendor's own web search in a Chat Completions reply: what it found,
// and its reference marks turned into numbered citations.

// searchReport is what a native search said about the pages it used,
// gathered from a reply or its chunks - each provider puts it somewhere
// else.
type searchReport struct {
	// annotations are OpenRouter's url_citation annotations on the message.
	annotations []any
	// zhipuResults are Zhipu's top-level web_search list, which the text
	// cites as [ref_1] (or [来源：ref_1]) by each result's refer.
	zhipuResults []any
	// executedTools are the browser_search calls a Groq model made, with
	// their results.
	executedTools []any
}

// take keeps what message (a message, or a chunk's delta) and its response
// (or chunk) carry. A stream may repeat a list in several chunks; each
// entry is kept once.
func (r *searchReport) take(message, response any) {
	add := func(into *[]any, l any) {
		for _, v := range list(l) {
			if !slices.ContainsFunc(*into, func(x any) bool { return reflect.DeepEqual(x, v) }) {
				*into = append(*into, v)
			}
		}
	}
	add(&r.annotations, at(message, "annotations"))
	add(&r.executedTools, at(message, "executed_tools"))
	add(&r.zhipuResults, at(response, "web_search"))
}

// cite is text with the pages it cites marked, every page reported.
func (r *searchReport) cite(text string, on EventSink) string {
	var citations stepCitations
	var spans []citedSpan
	for _, a := range r.annotations {
		c := at(a, "url_citation")
		start, ok1 := u64(at(c, "start_index"))
		end, ok2 := u64(at(c, "end_index"))
		url, ok3 := str(at(c, "url"))
		if ok1 && ok2 && ok3 {
			spans = append(spans, citedSpan{start: int(start), end: int(end), url: url, title: strAt(c, "title")})
		}
	}
	if len(spans) > 0 {
		text = markCitedSpans(text, spans, &citations, on)
	}
	if len(r.zhipuResults) > 0 {
		text = citeZhipuRefs(text, r.zhipuResults, &citations, on)
	}
	if len(r.executedTools) > 0 {
		// Groq's text points into its browser's pages as 【2†L6-L10】, which
		// name no page: the marks go, the pages are still listed.
		text = stripBetween(text, '【', '】', func(inner string) bool { return strings.ContainsRune(inner, '†') })
		for _, tool := range r.executedTools {
			for _, result := range list(at(tool, "search_results", "results")) {
				if url, ok := str(at(result, "url")); ok {
					citations.cite(url, strAt(result, "title"), on)
				}
			}
		}
	}
	return text
}

// stripBetween is text without the open...close runs whose inside drop
// accepts.
func stripBetween(text string, open, close rune, drop func(string) bool) string {
	var out strings.Builder
	rest := text
	for {
		start := strings.IndexRune(rest, open)
		if start < 0 {
			break
		}
		after := rest[start+utf8.RuneLen(open):]
		if end := strings.IndexRune(after, close); end >= 0 && drop(after[:end]) {
			out.WriteString(rest[:start])
			rest = after[end+utf8.RuneLen(close):]
			continue
		}
		out.WriteString(rest[:start+utf8.RuneLen(open)])
		rest = after
	}
	out.WriteString(rest)
	return out.String()
}

func allDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// citeZhipuRefs is text with Zhipu's [ref_1], [来源：ref_1] or 【ref_1】 as
// citation markers, by each result's refer - or, when the text marks none,
// every result with a link reported.
func citeZhipuRefs(text string, results []any, citations *stepCitations, on EventSink) string {
	page := func(refer string) (url, title string, ok bool) {
		for _, r := range results {
			if s, isStr := str(at(r, "refer")); isStr && s == refer {
				return strAt(r, "link"), strAt(r, "title"), true
			}
		}
		return "", "", false
	}
	var out strings.Builder
	matched := false
	rest := text
	for {
		start := strings.IndexAny(rest, "[【")
		if start < 0 {
			break
		}
		out.WriteString(rest[:start])
		open, size := utf8.DecodeRuneInString(rest[start:])
		close := ']'
		if open == '【' {
			close = '】'
		}
		after := rest[start+size:]
		if end := strings.IndexRune(after, close); end >= 0 && end <= 40 {
			inner := after[:end]
			if at := strings.Index(inner, "ref_"); at >= 0 && allDigits(inner[at+4:]) {
				if url, title, ok := page(inner[at:]); ok && url != "" {
					out.WriteString(citations.cite(url, title, on))
					matched = true
					rest = after[end+utf8.RuneLen(close):]
					continue
				}
			}
		}
		out.WriteRune(open)
		rest = after
	}
	out.WriteString(rest)
	// A reply that searched but marked nothing still names its pages.
	if !matched {
		for _, r := range results {
			if url := strAt(r, "link"); url != "" {
				citations.cite(url, strAt(r, "title"), on)
			}
		}
	}
	return out.String()
}
