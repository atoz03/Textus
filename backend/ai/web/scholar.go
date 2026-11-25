package web

import (
	"context"
	"encoding/json/v2"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"textus/backend/ai/aicompat"
)

// search_papers: scholarly works from OpenAlex (journals, conferences,
// books, by way of Crossref and others) and arXiv (preprints), both public
// and keyless. A paper is cited like a page: by its DOI, else its landing
// page.

const (
	// maxFeedBytes is the most an index's answer may be.
	maxFeedBytes = 8 << 20
	// perIndex is the works asked of each index per query.
	perIndex = 6
	// abstractChars is where an abstract is cut in the results: enough to
	// judge the paper by.
	abstractChars = 600
)

// The indexes; tests point them elsewhere.
var (
	OpenAlexURL = "https://api.openalex.org/works"
	ArXivURL    = "https://export.arxiv.org/api/query"
)

// Paper is one scholarly work, as either index describes it.
type Paper struct {
	Title   string
	Authors []string
	// Year is 0 when the index doesn't say.
	Year  int
	Venue string
	DOI   string
	// URL is the DOI's address, else the index's own page for the work.
	URL          string
	AbstractText string
	// CitedBy is nil when the index doesn't say.
	CitedBy *uint64
}

// Byline is who and when, as a reader cites it: "Lovelace, Babbage et al.
// (1843)".
func (p Paper) Byline() string {
	var who string
	switch len(p.Authors) {
	case 0:
	case 1:
		who = p.Authors[0]
	case 2:
		who = p.Authors[0] + ", " + p.Authors[1]
	default:
		who = p.Authors[0] + ", " + p.Authors[1] + " et al."
	}
	switch {
	case p.Year == 0:
		return who
	case who == "":
		return fmt.Sprintf("(%d)", p.Year)
	}
	return fmt.Sprintf("%s (%d)", who, p.Year)
}

// Describe is the paper as numbered result n for the model.
func (p Paper) Describe(n uint32) string {
	about := []string{p.URL}
	if p.Venue != "" {
		about = append(about, p.Venue)
	}
	if p.CitedBy != nil {
		about = append(about, fmt.Sprintf("cited by %d", *p.CitedBy))
	}
	return fmt.Sprintf("[%d] %s\n%s\n%s\n%s", n, p.Title, p.Byline(), strings.Join(about, " · "), cutAt(p.AbstractText, abstractChars))
}

// SearchPapers is both indexes searched for query at once, merged:
// OpenAlex's first (it ranks by relevance and citations), then arXiv's
// that it doesn't have. Fails only when both do. since is the earliest
// year wanted, 0 for any.
func SearchPapers(ctx context.Context, query string, since int) ([]Paper, error) {
	type found struct {
		papers []Paper
		err    error
	}
	fromArXiv := make(chan found, 1)
	go func() {
		papers, err := arXiv(ctx, query)
		fromArXiv <- found{papers, err}
	}()
	papers, errOpenAlex := openAlex(ctx, query, since)
	more := <-fromArXiv
	if errOpenAlex != nil && more.err != nil {
		return nil, fmt.Errorf("%v; %v", errOpenAlex, more.err)
	}
	for _, paper := range more.papers {
		if since != 0 && paper.Year != 0 && paper.Year < since {
			continue
		}
		if !slices.ContainsFunc(papers, func(p Paper) bool { return sameWork(p, paper) }) {
			papers = append(papers, paper)
		}
	}
	return papers, nil
}

// sameWork reports whether a and b are the same work in both indexes: one
// DOI, or one title.
func sameWork(a, b Paper) bool {
	key := func(p Paper) string {
		var k strings.Builder
		for _, r := range p.Title {
			if unicode.IsLetter(r) || unicode.IsNumber(r) {
				k.WriteRune(unicode.ToLower(r))
			}
		}
		return k.String()
	}
	return (a.DOI != "" && b.DOI != "" && strings.EqualFold(a.DOI, b.DOI)) || key(a) == key(b)
}

// indexGet is the body of a GET from one of the indexes.
func indexGet(ctx context.Context, name, address string, query url.Values) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address+"?"+query.Encode(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := (&http.Client{Timeout: searchTimeout, Transport: Net.Transport}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s couldn't be reached: %v", name, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("%s answered %s", name, statusText(resp.StatusCode))
	}
	if resp.ContentLength > maxFeedBytes {
		return nil, fmt.Errorf("%s's answer was too large.", name)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxFeedBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxFeedBytes {
		return nil, fmt.Errorf("%s's answer was too large.", name)
	}
	return body, nil
}

func openAlex(ctx context.Context, query string, since int) ([]Paper, error) {
	q := url.Values{
		"search":   {query},
		"per-page": {strconv.Itoa(perIndex)},
		"select":   {"id,doi,title,publication_year,authorships,primary_location,abstract_inverted_index,cited_by_count"},
	}
	if since != 0 {
		q.Set("filter", fmt.Sprintf("from_publication_date:%d-01-01", since))
	}
	body, err := indexGet(ctx, "OpenAlex", OpenAlexURL, q)
	if err != nil {
		return nil, err
	}
	var value any
	if err := json.Unmarshal(body, &value); err != nil {
		return nil, err
	}
	return openAlexPapers(value), nil
}

// openAlexPapers is OpenAlex's `results`, as papers.
func openAlexPapers(body any) []Paper {
	var papers []Paper
	for _, work := range list(body, "results") {
		title, ok := at(work, "title").(string)
		if !ok {
			continue
		}
		p := Paper{Title: strings.TrimSpace(title)}
		if doi, ok := at(work, "doi").(string); ok {
			for strings.HasPrefix(doi, "https://doi.org/") {
				doi = doi[len("https://doi.org/"):]
			}
			p.DOI, p.URL = doi, "https://doi.org/"+doi
		} else if id, ok := at(work, "id").(string); ok {
			p.URL = id
		} else {
			continue
		}
		for _, a := range list(work, "authorships") {
			if name, ok := at(a, "author", "display_name").(string); ok {
				p.Authors = append(p.Authors, name)
			}
		}
		if year, ok := wholeNumber(at(work, "publication_year")); ok {
			p.Year = int(year)
		}
		p.Venue, _ = at(work, "primary_location", "source", "display_name").(string)
		p.AbstractText = uninvert(at(work, "abstract_inverted_index"))
		if cited, ok := wholeNumber(at(work, "cited_by_count")); ok {
			p.CitedBy = &cited
		}
		papers = append(papers, p)
	}
	return papers
}

// wholeNumber is v as a JSON number that is a whole one, not negative.
func wholeNumber(v any) (uint64, bool) {
	f, ok := v.(float64)
	if !ok || f < 0 || f != float64(uint64(f)) {
		return 0, false
	}
	return uint64(f), true
}

// uninvert: OpenAlex keeps an abstract as word -> positions; the words in
// order.
func uninvert(index any) string {
	words, ok := index.(map[string]any)
	if !ok {
		return ""
	}
	type placed struct {
		at   uint64
		word string
	}
	var all []placed
	for word, positions := range words {
		for _, at := range list(positions) {
			if n, ok := wholeNumber(at); ok {
				all = append(all, placed{n, word})
			}
		}
	}
	slices.SortFunc(all, func(a, b placed) int {
		if a.at != b.at {
			return int(a.at) - int(b.at)
		}
		return strings.Compare(a.word, b.word)
	})
	out := make([]string, len(all))
	for i, p := range all {
		out[i] = p.word
	}
	return strings.Join(out, " ")
}

func arXiv(ctx context.Context, query string) ([]Paper, error) {
	body, err := indexGet(ctx, "arXiv", ArXivURL, url.Values{
		"search_query": {arXivQuery(query)},
		"max_results":  {strconv.Itoa(perIndex)},
	})
	if err != nil {
		return nil, err
	}
	return arXivPapers(body), nil
}

// arXivQuery: arXiv reads a space between words as OR - every word
// required instead.
func arXivQuery(query string) string {
	words := strings.Fields(query)
	for i, w := range words {
		words[i] = "all:" + w
	}
	return strings.Join(words, " AND ")
}

// arXivPapers is arXiv's Atom feed, as papers.
func arXivPapers(atom []byte) []Paper {
	var feed struct {
		Entries []struct {
			ID        string `xml:"id"`
			Published string `xml:"published"`
			Title     string `xml:"title"`
			Summary   string `xml:"summary"`
			DOI       string `xml:"doi"`
			Authors   []struct {
				Name string `xml:"name"`
			} `xml:"author"`
		} `xml:"entry"`
	}
	if err := xml.Unmarshal(atom, &feed); err != nil && !errors.Is(err, io.EOF) {
		return nil
	}
	squash := func(s string) string { return strings.Join(strings.Fields(s), " ") }
	var papers []Paper
	for _, e := range feed.Entries {
		p := Paper{
			Title: squash(e.Title), AbstractText: squash(e.Summary), Venue: "arXiv",
			URL: strings.ReplaceAll(strings.TrimSpace(e.ID), "http://", "https://"),
			DOI: strings.TrimSpace(e.DOI),
		}
		if len(e.Published) >= 4 {
			p.Year, _ = strconv.Atoi(e.Published[:4])
		}
		for _, a := range e.Authors {
			if name := squash(a.Name); name != "" {
				p.Authors = append(p.Authors, name)
			}
		}
		if p.Title != "" && p.URL != "" {
			papers = append(papers, p)
		}
	}
	return papers
}

// DOITitle is the work a DOI names, as "Title - Authors (year)", from
// OpenAlex: for a source cited by its address alone. False when OpenAlex
// doesn't know it.
func DOITitle(ctx context.Context, doi string) (string, bool) {
	body, err := indexGet(ctx, "OpenAlex", OpenAlexURL+"/doi:"+doi, url.Values{"select": {"id,doi,title,publication_year,authorships"}})
	if err != nil {
		return "", false
	}
	var work any
	if json.Unmarshal(body, &work) != nil {
		return "", false
	}
	papers := openAlexPapers(map[string]any{"results": []any{work}})
	if len(papers) == 0 {
		return "", false
	}
	p := papers[len(papers)-1]
	return p.Title + " - " + p.Byline(), true
}

// searchPapersSpec is what search_papers looks like to the model.
func searchPapersSpec() aicompat.ToolSpec {
	return aicompat.ToolSpec{
		Name:        SearchPapersTool,
		Description: "Search scholarly literature - journal and conference papers, books and arXiv preprints - for research, evidence and figures a claim can rest on. Pass one short query in English, the language most papers are indexed in, and optionally `since`, the earliest year wanted. Results come back numbered like web results, with authors, year, venue, citation count and the abstract; cite what you use by its number. Read an open-access paper in full with web_fetch on its address.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{"type": "string", "description": "What the papers are about, in a few keywords"},
				"since": map[string]any{"type": "integer", "description": "The earliest publication year wanted"},
			},
			"required": []string{"query"},
		},
	}
}
