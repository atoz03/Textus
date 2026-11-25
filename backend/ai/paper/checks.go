package paper

import (
	"encoding/json/v2"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"textus/backend/ai/rules"
)

// What a drafted section must hold - its length, its citations, its
// figures and their data - and the checks that send it back to its writer
// when it doesn't.

// most is the most a section of target may run to, as writers are told.
func most(target uint32) uint32 { return uint32(math.Round(float64(target) * pass[1])) }

// sameAddress reports whether two addresses are one, written with or
// without percent-escapes ("(" or "%28") or a trailing slash.
func sameAddress(a, b string) bool {
	plain := func(s string) string { return decoded(strings.TrimRight(s, "/")) }
	return a == b || plain(a) == plain(b)
}

// WebFigureMark is where a web picture goes until one is found for it.
const WebFigureMark = "<!-- textus-figure web:"

// space is what a Unicode-aware `\s` matches, for a character class:
// Go's own `\s` knows ASCII only.
const space = `\t-\r \x{85}\p{Z}`

var (
	// citedLink is a citation as the paper writes it, `[n](address)`: a
	// web page's address, or a local file's path (`attached/…`,
	// `references/…`).
	citedLink = regexp.MustCompile(`\[(\d{1,3})\]\((https?://[^)` + space + `]+|[^)` + space + `:#/][^)` + space + `:]*)\)`)
	// numberedFigure is a figure named by a number, "图 2" or "Figure 2":
	// the writers' own numbers, which the paper's numbering would
	// contradict.
	numberedFigure = regexp.MustCompile(`(?:图|圖|図|Figure|Fig\.|Abbildung|Figura|Рисунок|그림)[` + space + `]*\d+`)
	// bareNumber is `[n]` not followed by `(`: a citation without its link.
	bareNumber = regexp.MustCompile(`\[(\d{1,3})\]($|[^(\d])`)
	// noteID is a note's id, as the dossier numbers them ("n12"): for the
	// reader, the sources are what is cited, never the notes.
	noteID = regexp.MustCompile(`(?:^|[^A-Za-z0-9])n\d+(?:$|[^A-Za-z0-9])`)
)

// fence is a closed fenced block: its language, body, and opening and
// closing lines.
type fence struct {
	language, body string
	open, close    int
}

// fenceSpans are markdown's closed fenced blocks, its lines counted as
// strings.Split counts them.
func fenceSpans(markdown string) []fence {
	var out []fence
	var open *fence
	for i, line := range strings.Split(markdown, "\n") {
		line = strings.TrimSuffix(line, "\r")
		trimmed := strings.TrimLeftFunc(line, unicode.IsSpace)
		switch {
		case open == nil && strings.HasPrefix(trimmed, "```"):
			open = &fence{language: strings.ToLower(strings.TrimSpace(strings.TrimLeft(trimmed, "`"))), open: i, close: i}
		case open == nil:
		case strings.HasPrefix(trimmed, "```"):
			open.close = i
			out = append(out, *open)
			open = nil
		default:
			open.body += line + "\n"
		}
	}
	return out
}

// fences are markdown's fenced blocks, as language and body.
func fences(markdown string) [][2]string {
	var out [][2]string
	for _, f := range fenceSpans(markdown) {
		out = append(out, [2]string{f.language, f.body})
	}
	return out
}

// lines are text's lines as Rust's str::lines gives them.
func lines(text string) []string { return rules.Lines(text) }

// prose is the section's prose, without its fences, pictures and marks:
// what its length counts.
func prose(markdown string) string {
	var out strings.Builder
	inFence := false
	for _, line := range lines(markdown) {
		trimmed := strings.TrimLeftFunc(line, unicode.IsSpace)
		if strings.HasPrefix(trimmed, "```") {
			inFence = !inFence
			continue
		}
		if inFence || strings.HasPrefix(trimmed, "![") || strings.HasPrefix(trimmed, "<!--") {
			continue
		}
		out.WriteString(citedLink.ReplaceAllLiteralString(line, ""))
		out.WriteByte('\n')
	}
	return out.String()
}

// CountsCharacters reports whether language is written without spaces, so
// its length is counted in characters rather than words.
func CountsCharacters(language string) bool {
	language = strings.ToLower(language)
	return strings.HasPrefix(language, "zh") || strings.HasPrefix(language, "ja") || strings.HasPrefix(language, "ko")
}

// length is text's length: its characters (not spaces) for a language
// written without spaces, else its words.
func length(text, language string) uint32 {
	if !CountsCharacters(language) {
		return uint32(len(strings.Fields(text)))
	}
	n := uint32(0)
	for _, c := range text {
		if !unicode.IsSpace(c) {
			n++
		}
	}
	return n
}

// proseLength is the length of markdown's prose: its text without code,
// figures, marks or citations' addresses - what a paper's length is
// measured by.
func proseLength(markdown, language string) uint32 { return length(prose(markdown), language) }

// The most series and points a series the chart renderer draws
// (MAX_SERIES and MAX_POINTS in src/editor/chart/chart-spec.ts).
const (
	mostSeries = 8
	mostPoints = 1000
)

// chartHolds reports why a chart block's JSON is not one the chart
// renderer draws, "" when it is: it refuses all parseChartSpec
// (src/editor/chart/chart-spec.ts) refuses, which
// src/editor/chart/chart-cases.json holds both to. A chart that passed here
// but not there reached a paper as its JSON source.
func chartHolds(body string) string {
	var parsed any
	if err := json.Unmarshal([]byte(body), &parsed); err != nil {
		return fmt.Sprintf("its JSON doesn't parse (%v)", err)
	}
	spec, ok := parsed.(map[string]any)
	if !ok {
		return "it must be one JSON object"
	}
	kind := "bar"
	if t, set := spec["type"]; set && t != nil {
		kind = "?"
		if s, ok := t.(string); ok {
			kind = s
		}
	}
	if !slices.Contains([]string{"bar", "line", "area", "pie", "scatter"}, kind) {
		return fmt.Sprintf(`"type" %s isn't bar, line, area, pie or scatter`, jsonText(spec["type"]))
	}
	series, _ := spec["series"].([]any)
	if len(series) == 0 {
		return `it needs a non-empty "series"`
	}
	if len(series) > mostSeries {
		return fmt.Sprintf("at most %d series", mostSeries)
	}
	number := func(v any) bool {
		f, ok := v.(float64)
		return ok && !math.IsInf(f, 0) && !math.IsNaN(f)
	}
	points := 0
	for i, s := range series {
		n := i + 1
		item, _ := s.(map[string]any)
		data, _ := item["data"].([]any)
		if len(data) == 0 {
			return `every series needs a non-empty "data" list`
		}
		if len(data) > mostPoints {
			return fmt.Sprintf("at most %d points a series", mostPoints)
		}
		points = max(points, len(data))
		if kind == "scatter" {
			for _, p := range data {
				pair, _ := p.([]any)
				if len(pair) != 2 || !number(pair[0]) || !number(pair[1]) {
					return fmt.Sprintf(`series %d: "data" must be [x, y] number pairs`, n)
				}
			}
		} else if !all(data, number) {
			return fmt.Sprintf(`series %d: "data" must be plain numbers - no units, percent signs, quotes or nulls`, n)
		}
		if name, set := item["name"]; set && name != nil {
			if _, text := name.(string); !text {
				return fmt.Sprintf(`series %d: "name" must be text`, n)
			}
		}
		if kind == "pie" && i == 0 && slices.ContainsFunc(data, func(v any) bool { f, _ := v.(float64); return f < 0 }) {
			return "a pie's values can't be negative"
		}
	}
	if _, list := spec["x"].([]any); list {
		return `"x" is the x axis' title, one text; the categories go in "labels"`
	}
	for _, key := range []string{"title", "x", "y", "caption", "source"} {
		if v, set := spec[key]; set && v != nil {
			if _, text := v.(string); !text {
				return fmt.Sprintf(`"%s" must be text`, key)
			}
		}
	}
	switch labels := spec["labels"].(type) {
	case nil:
	case []any:
		if kind != "scatter" && len(labels) < points {
			return fmt.Sprintf(`%d "labels" for %d data points: one label each`, len(labels), points)
		}
	default:
		return `"labels" must be a list`
	}
	if source, _ := spec["source"].(string); noteID.MatchString(source) {
		return `"source" names notes by their ids; name the sources by their numbers, as [3](address)`
	}
	return ""
}

func all[T any](items []T, ok func(T) bool) bool {
	for _, item := range items {
		if !ok(item) {
			return false
		}
	}
	return true
}

// jsonText is v as compact JSON, its keys sorted.
func jsonText(v any) string {
	raw, err := json.Marshal(v, json.Deterministic(true))
	if err != nil {
		return "null"
	}
	return string(raw)
}

// Problem is one thing wrong with a drafted section, as its writer is told
// it.
type Problem struct {
	Text string
	// Soft: a section with only soft problems is kept rather than lose the
	// paper - its length off its target, a planned figure missing, a post
	// too long. A wrong citation or a chart that can't be drawn is never
	// kept.
	Soft bool
}

func hard(text string) Problem     { return Problem{Text: text} }
func softly(text string) Problem   { return Problem{Text: text, Soft: true} }
func allSoft(wrong []Problem) bool { return all(wrong, func(p Problem) bool { return p.Soft }) }

// problemTexts are the problems' texts, to join as the writer is sent them.
func problemTexts(wrong []Problem) []string {
	out := make([]string, len(wrong))
	for i, p := range wrong {
		out[i] = p.Text
	}
	return out
}

// numbered is one source a section may cite, by its number and address.
type numbered struct {
	n   uint32
	url string
}

// problems is what is wrong with a drafted section, every problem at once.
// sources are the numbers this section may cite, with their addresses.
func problems(markdown string, section *OutlineSection, sources []numbered, language string) []Problem {
	var out []Problem
	length := length(prose(markdown), language)
	ratio := float64(length) / max(float64(section.TargetWords), 1)
	most := most(section.TargetWords)
	switch {
	case ratio > soft[1]:
		out = append(out, hard(fmt.Sprintf("Far too long: %d against about %d - cut it to at most %d, keeping the strongest evidence.", length, section.TargetWords, most)))
	case ratio < soft[0]:
		out = append(out, hard(fmt.Sprintf("Far too short: %d against about %d - develop the key points with the notes' evidence.", length, section.TargetWords)))
	case ratio < pass[0] || ratio > pass[1]:
		out = append(out, softly(fmt.Sprintf("It is %d long; the section is to be about %d (at most %d).", length, section.TargetWords, most)))
	}
	cited := 0
	for _, c := range citedLink.FindAllStringSubmatch(markdown, -1) {
		n, _ := strconv.ParseUint(c[1], 10, 32)
		i := slices.IndexFunc(sources, func(s numbered) bool { return s.n == uint32(n) })
		switch {
		case i >= 0 && sameAddress(sources[i].url, c[2]):
			cited++
		case i >= 0:
			out = append(out, hard(fmt.Sprintf("[%d] links to %s but source %d is %s.", n, c[2], n, sources[i].url)))
		default:
			out = append(out, hard(fmt.Sprintf("[%d] isn't one of this section's sources.", n)))
		}
	}
	text := prose(markdown)
	if bareNumber.MatchString(text) {
		out = append(out, hard("A citation is written as [n] alone: write it [n](address), with the source's address."))
	}
	if found := numberedFigure.FindString(text); found != "" {
		out = append(out, hard(fmt.Sprintf("\"%s\" names a figure by a number: the figures are numbered afterwards - call it the diagram, chart or picture below or above.", found)))
	}
	if cited == 0 && len(sources) > 0 {
		out = append(out, hard("Nothing is cited: mark each fact from the notes with its source, [n](address)."))
	}
	blocks := fences(markdown)
	count := func(kind FigureKind) int {
		n := 0
		for _, f := range section.Figures {
			if f.Kind == kind {
				n++
			}
		}
		return n
	}
	mermaid := 0
	var charts []string
	for _, b := range blocks {
		switch {
		case b[0] == "mermaid" && strings.TrimSpace(b[1]) != "":
			mermaid++
		case b[0] == "chart":
			charts = append(charts, b[1])
		}
	}
	if mermaid < count(Mermaid) {
		out = append(out, softly(fmt.Sprintf("%d planned diagram(s), %d ```mermaid block(s).", count(Mermaid), mermaid)))
	}
	for i, body := range charts {
		if why := chartHolds(body); why != "" {
			out = append(out, hard(fmt.Sprintf("Chart %d: %s.", i+1, why)))
		}
	}
	if len(charts) < count(Chart) {
		out = append(out, softly(fmt.Sprintf("%d planned chart(s), %d ```chart block(s).", count(Chart), len(charts))))
	}
	if pictures := strings.Count(markdown, "](textus-picture:"); pictures < count(Generated) {
		out = append(out, softly(fmt.Sprintf("%d planned picture(s), %d textus-picture line(s).", count(Generated), pictures)))
	}
	if web := strings.Count(markdown, WebFigureMark); web < count(WebPicture) {
		out = append(out, softly(fmt.Sprintf("%d planned web picture(s), %d mark(s).", count(WebPicture), web)))
	}
	return out
}

// runeCount is how many characters s has.
func runeCount(s string) int { return utf8.RuneCountInString(s) }
