package paper

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"unicode"

	"textus/backend/ai"
	"textus/backend/ai/aicompat"
)

// What turns reviewed sections into a paper: a title and an abstract
// above them, every figure numbered and captioned in reading order, and
// the list of the sources the text cites below them, in the paper's
// language.

// apparatus are the words a paper's apparatus is labelled with.
type apparatus struct{ abstract, references, figure, sources string }

// languageOf is language's primary tag, lowercased, and whether it is
// Chinese written in traditional characters.
func languageOf(language string) (string, bool) {
	lang := strings.ToLower(language)
	traditional := false
	for _, t := range []string{"zh-hant", "zh-tw", "zh-hk", "zh-mo"} {
		traditional = traditional || strings.HasPrefix(lang, t)
	}
	primary, _, _ := strings.Cut(strings.ReplaceAll(lang, "_", "-"), "-")
	return primary, traditional
}

// words are the words a paper's apparatus is labelled with, by the
// language it is written in.
func words(language string) apparatus {
	primary, traditional := languageOf(language)
	switch {
	case primary == "zh" && traditional:
		return apparatus{"摘要", "參考文獻", "圖", "資料來源"}
	case primary == "zh":
		return apparatus{"摘要", "参考文献", "图", "资料来源"}
	}
	if w, ok := map[string]apparatus{
		"ja": {"要旨", "参考文献", "図", "出典"},
		"ko": {"초록", "참고문헌", "그림", "출처"},
		"es": {"Resumen", "Referencias", "Figura", "Fuentes"},
		"fr": {"Résumé", "Références", "Figure", "Sources"},
		"de": {"Zusammenfassung", "Literatur", "Abbildung", "Quellen"},
		"ru": {"Аннотация", "Литература", "Рисунок", "Источники"},
		"pt": {"Resumo", "Referências", "Figura", "Fontes"},
	}[primary]; ok {
		return w
	}
	return apparatus{"Abstract", "References", "Figure", "Sources"}
}

// caption is a figure's caption line: "图 3　…" in Chinese and Japanese,
// else "Figure 3. …".
func caption(language string, n int, text string) string {
	figure := words(language).figure
	lang := strings.ToLower(language)
	cjk := strings.HasPrefix(lang, "zh") || strings.HasPrefix(lang, "ja")
	text = strings.TrimSpace(text)
	switch {
	case text == "":
		return fmt.Sprintf("*%s %d*", figure, n)
	case cjk:
		return fmt.Sprintf("*%s %d　%s*", figure, n, text)
	}
	return fmt.Sprintf("*%s %d. %s*", figure, n, text)
}

// pictureLine is a picture to be made, or made: its alt text says what it
// shows.
var pictureLine = regexp.MustCompile(`^[` + space + `]*!\[([^\]]*)\]\(textus-(?:picture|generated):`)

// NumberFigures gives every figure of sections a numbered caption under
// it, counted through the whole paper: a diagram or chart block, a picture
// line, a web-picture mark. A chart's caption or title, a picture's alt
// text and a mark's description say what it shows. Answers how many there
// are.
func NumberFigures(sections []string, language string) int {
	n := 0
	for s, section := range sections {
		var out []string
		open, lang := false, ""
		for _, line := range lines(section) {
			out = append(out, line)
			trimmed := strings.TrimLeftFunc(line, unicode.IsSpace)
			switch {
			case !open && strings.HasPrefix(trimmed, "```"):
				open, lang = true, strings.ToLower(strings.TrimSpace(strings.TrimLeft(trimmed, "`")))
			case !open:
				shows, found := "", false
				if c := pictureLine.FindStringSubmatch(line); c != nil {
					shows, found = c[1], true
				} else if rest, ok := strings.CutPrefix(trimmed, WebFigureMark); ok {
					shows, found = trimSuffixes(rest, "-->"), true
				}
				if found {
					n++
					out = append(out, "", caption(language, n, shows))
				}
			case strings.HasPrefix(trimmed, "```"):
				open = false
				if lang != "mermaid" && lang != "chart" {
					break
				}
				var spec map[string]any
				_ = json.Unmarshal([]byte(fenceBody(out)), &spec)
				shows, _ := spec["caption"].(string)
				if _, isText := spec["caption"].(string); !isText {
					shows, _ = spec["title"].(string)
				}
				// The caption goes under the chart, numbered, and only
				// there: drawn in it too, it said it twice.
				if _, had := spec["caption"]; had && spec != nil {
					delete(spec, "caption")
					out = withoutBody(out)
					raw, _ := json.Marshal(spec, json.Deterministic(true))
					out = slices.Insert(out, len(out)-1, string(raw))
				}
				n++
				out = append(out, "", caption(language, n, shows))
			}
		}
		sections[s] = strings.Join(out, "\n")
	}
	return n
}

// trimSuffixes is s without every suffix at its end, as Rust's
// trim_end_matches takes them.
func trimSuffixes(s, suffix string) string {
	for strings.HasSuffix(s, suffix) {
		s = strings.TrimSuffix(s, suffix)
	}
	return s
}

// lastFence is where the fence out has just closed opened.
func lastFence(out []string) int {
	end := len(out) - 1
	for i := end - 1; i >= 0; i-- {
		if strings.HasPrefix(strings.TrimLeftFunc(out[i], unicode.IsSpace), "```") {
			return i
		}
	}
	return end
}

// withoutBody is out with the body of the fence it has just closed taken
// out, the fences left.
func withoutBody(out []string) []string {
	return slices.Delete(out, lastFence(out)+1, len(out)-1)
}

// fenceBody is the body of the fence out has just closed: its lines
// between the opening and the closing fence.
func fenceBody(out []string) string {
	return strings.Join(out[lastFence(out)+1:len(out)-1], "\n")
}

// Entry is a source as the reference list gives it after its number: its
// title and address, or a library file's path.
func Entry(s aicompat.WebSource) string {
	if name, ok := materialName(s.URL); ok {
		return "`" + name + "`"
	}
	if path, ok := referencePath(s.URL); ok {
		title := s.Title
		if strings.TrimSpace(title) == "" {
			title = path
		}
		return fmt.Sprintf("%s. `%s`", strings.TrimRight(strings.TrimSpace(title), "."), path)
	}
	title := strings.TrimRight(strings.TrimSpace(s.Title), ".")
	if title == "" || title == s.URL {
		return "<" + s.URL + ">"
	}
	return fmt.Sprintf("%s. <%s>", title, s.URL)
}

// ReferencesHeading is the reference list's heading in language.
func ReferencesHeading(language string, genre Genre) string {
	if genre.Profile().Frame == Academic {
		return words(language).references
	}
	return words(language).sources
}

// executiveSummary is what an executive summary is called in language.
func executiveSummary(language string) string {
	primary, traditional := languageOf(language)
	switch {
	case primary == "zh" && traditional:
		return "執行摘要"
	case primary == "zh":
		return "执行摘要"
	}
	if s, ok := map[string]string{
		"ja": "エグゼクティブサマリー", "ko": "요약", "es": "Resumen ejecutivo", "fr": "Synthèse",
		"de": "Management Summary", "ru": "Резюме", "pt": "Sumário executivo",
	}[primary]; ok {
		return s
	}
	return "Executive summary"
}

// NumberCitations numbers sections' citations 1, 2, … in the order the
// paper first cites them, and answers the dossier with its sources
// numbered to match: the research numbers every source it saw, so a paper
// citing a few of them read [93][248]. Sources it doesn't cite are left
// out.
func NumberCitations(sections []string, dossier *Dossier) Dossier {
	var order []uint32
	for i, section := range sections {
		sections[i] = citedLink.ReplaceAllStringFunc(section, func(c string) string {
			m := citedLink.FindStringSubmatch(c)
			n64, _ := strconv.ParseUint(m[1], 10, 32)
			n := uint32(n64)
			if _, ok := dossier.source(n); !ok {
				return c
			}
			k := slices.Index(order, n)
			if k < 0 {
				order = append(order, n)
				k = len(order) - 1
			}
			return fmt.Sprintf("[%d](%s)", k+1, m[2])
		})
	}
	numbered := Dossier{Notes: slices.Clone(dossier.Notes)}
	for i, n := range order {
		if s, ok := dossier.source(n); ok {
			s.N = uint32(i + 1)
			numbered.Sources = append(numbered.Sources, s)
		}
	}
	return numbered
}

// References are the sources sections cite, as the list that ends the
// paper: each by its number, in number order, with its title and address.
// False when nothing is cited.
func References(sections []string, dossier *Dossier, heading string) (string, bool) {
	var numbers []uint32
	for _, s := range sections {
		for _, c := range citedLink.FindAllStringSubmatch(s, -1) {
			if n, err := strconv.ParseUint(c[1], 10, 32); err == nil {
				numbers = append(numbers, uint32(n))
			}
		}
	}
	slices.Sort(numbers)
	var entries []string
	for _, n := range slices.Compact(numbers) {
		if s, ok := dossier.source(n); ok {
			entries = append(entries, fmt.Sprintf("\\[%d\\] %s", s.N, Entry(s)))
		}
	}
	if len(entries) == 0 {
		return "", false
	}
	return fmt.Sprintf("## %s\n\n%s", heading, strings.Join(entries, "\n\n")), true
}

// Opening is the paper's title and abstract, from one tool-less step over
// the finished sections; the topic as the title, and no abstract, when the
// reply can't be read.
func Opening(ctx context.Context, model Model, paper *Paper, outline *Outline, sections []string) string {
	profile := paper.Genre.Profile()
	abstractWord := words(paper.Language).abstract
	if profile.Frame == Executive {
		abstractWord = executiveSummary(paper.Language)
	}
	message := fmt.Sprintf("Thesis: %s\n\n%s", outline.Thesis, strings.Join(sections, "\n\n"))
	reply, _ := ask(ctx, model, openingInstructions(paper), message, nil)
	var parsed map[string]any
	ai.JSONObject(reply, &parsed)
	field := func(key string) string {
		s, _ := parsed[key].(string)
		return strings.TrimSpace(s)
	}
	title := field("title")
	if title == "" {
		title = strings.TrimSpace(paper.Topic)
	}
	abstract := field("abstract")
	switch {
	case profile.Frame == Headline || abstract == "":
		return "# " + title
	case profile.Frame == News:
		return fmt.Sprintf("# %s\n\n**%s**", title, abstract)
	case CountsCharacters(paper.Language):
		return fmt.Sprintf("# %s\n\n**%s**：%s", title, abstractWord, abstract)
	}
	return fmt.Sprintf("# %s\n\n**%s:** %s", title, abstractWord, abstract)
}

// openingInstructions is what the step writing the title and abstract is
// told.
func openingInstructions(paper *Paper) string {
	profile := paper.Genre.Profile()
	return fill("finish.opening", kv{"genre", profile.Name}, kv{"language", paper.Language}, kv{"opening", profile.Opening})
}

// PlainCitations is section with each citation `[n](address)` as `[n]` in
// the paper's numbering, linked to its entry in the reference list
// (`#ref-n`, which the editor finds by the entry's leading `[n]`). The
// brackets are escaped so they stay text. Inside a code block it is a
// bare `[n]`, since there a backslash is no escape: in a chart's JSON `\[`
// is invalid and the chart isn't drawn.
func PlainCitations(section string) string {
	inFence := false
	out := strings.Split(section, "\n")
	for i, line := range out {
		if strings.HasPrefix(strings.TrimLeftFunc(line, unicode.IsSpace), "```") {
			inFence = !inFence
			continue
		}
		plain := `[\[$1\]](#ref-$1)`
		if inFence {
			plain = "[$1]"
		}
		out[i] = citedLink.ReplaceAllString(line, plain)
	}
	return strings.Join(out, "\n")
}

// Assemble is the whole paper: its opening, its sections and its
// reference list ("" for none), one after another.
func Assemble(opening string, sections []string, references string) string {
	var parts []string
	for _, p := range append(append([]string{opening}, sections...), references) {
		if p = strings.TrimSpace(p); p != "" {
			parts = append(parts, p)
		}
	}
	return strings.Join(parts, "\n\n")
}

// WithoutPictures is section without its picture lines: what is left when
// no image model is set.
func WithoutPictures(section string) string {
	var kept []string
	for _, line := range lines(section) {
		if !pictureLine.MatchString(line) {
			kept = append(kept, line)
		}
	}
	return closeGaps(strings.Join(kept, "\n"))
}

// doiOf is the DOI an address names, for a doi.org link.
func doiOf(url string) (string, bool) {
	_, rest, ok := strings.Cut(url, "doi.org/")
	return rest, ok && strings.HasPrefix(rest, "10.")
}

// lookupsAtOnce are the titles looked up at once: a paper can cite dozens
// of sources by their address alone, and one at a time each waits out the
// last one's round trip.
const lookupsAtOnce = 6

// TitleLookup is the title at an address; false when there is none.
type TitleLookup func(ctx context.Context, address string) (string, bool)

// TitleSources gives dossier's sources cited by their address alone a
// title, so the reference list names them, with lookUp: a few at once,
// each title landing on its own source whichever comes back first.
func TitleSources(ctx context.Context, dossier *Dossier, lookUp TitleLookup) {
	slots := make(chan struct{}, lookupsAtOnce)
	var wg sync.WaitGroup
	for i := range dossier.Sources {
		s := dossier.Sources[i]
		if strings.TrimSpace(s.Title) != "" && s.Title != s.URL {
			continue
		}
		address := decoded(s.URL)
		slots <- struct{}{}
		wg.Go(func() {
			defer func() { <-slots }()
			if title, ok := lookUp(ctx, address); ok {
				// Each goroutine writes its own source.
				dossier.Sources[i].Title = title
			}
		})
	}
	wg.Wait()
}

// LookUpTitle is the title at address: a DOI's work, with its authors,
// from OpenAlex; a page's own title, read as web_fetch reads it.
func LookUpTitle(doiTitle, pageTitle TitleLookup) TitleLookup {
	return func(ctx context.Context, address string) (string, bool) {
		if doi, ok := doiOf(address); ok {
			return doiTitle(ctx, doi)
		}
		if strings.HasPrefix(address, "http") {
			return pageTitle(ctx, address)
		}
		return "", false
	}
}
