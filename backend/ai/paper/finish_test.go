package paper

import (
	"encoding/json/v2"
	"fmt"
	"strings"
	"testing"

	"textus/backend/ai"
	"textus/backend/ai/aicompat"
)

func TestFiguresAreNumberedThroughThePaperAndCaptionedByWhatTheyShow(t *testing.T) {
	sections := []string{
		"## One\n\nText.\n\n```mermaid\nflowchart LR\n  A --> B\n```\n\n```rust\nfn main() {}\n```",
		"## Two\n\n```chart\n{\"title\": \"Temperatures\", \"caption\": \"Roof surfaces at noon\", \"series\": [{\"data\": [1]}]}\n```\n\n![A green roof in Basel](textus-picture:16:9 a roof)\n\n<!-- textus-figure web: satellite heat map -->",
	}
	if n := NumberFigures(sections, "zh-CN"); n != 4 {
		t.Fatal(n)
	}
	if !strings.Contains(sections[0], "```\n\n*图 1*\n\n```rust") {
		t.Fatal(sections[0])
	}
	if strings.Contains(sections[0], "图 2") {
		t.Fatal("a code block is no figure:", sections[0])
	}
	for _, want := range []string{
		"```\n\n*图 2　Roof surfaces at noon*",
		`"title":"Temperatures"`,
		"(textus-picture:16:9 a roof)\n\n*图 3　A green roof in Basel*",
	} {
		if !strings.Contains(sections[1], want) {
			t.Fatalf("no %q in %s", want, sections[1])
		}
	}
	// Said once, under the chart, not drawn in it as well.
	if strings.Contains(sections[1], `"caption"`) {
		t.Fatal(sections[1])
	}
	if !strings.HasSuffix(sections[1], "-->\n\n*图 4　satellite heat map*") {
		t.Fatal(sections[1])
	}

	english := []string{"```chart\n{\"title\": \"Temps\", \"series\": []}\n```"}
	NumberFigures(english, "en")
	if !strings.HasSuffix(english[0], "*Figure 1. Temps*") {
		t.Fatal(english[0])
	}
}

func TestTheReferencesListOnlyWhatTheTextCitesInNumberOrder(t *testing.T) {
	d := draftingDossier()
	d.Sources = append(d.Sources,
		aicompat.WebSource{N: 1, URL: "https://a.example/"},
		aicompat.WebSource{N: 9, URL: "https://unused.example/", Title: "Unused"})
	sections := []string{
		"Cool [3](https://example.org/study).",
		"Again [3](https://example.org/study) and [1](https://a.example/).",
	}
	listed, ok := References(sections, &d, ReferencesHeading("zh", GenrePaper))
	if want := "## 参考文献\n\n\\[1\\] <https://a.example/>\n\n\\[3\\] Study. <https://example.org/study>"; !ok || listed != want {
		t.Fatalf("got  %q\nwant %q", listed, want)
	}
	for language, heading := range map[string]string{"zh-Hant": "## 參考文獻", "en-US": "## References"} {
		if listed, _ := References(sections, &d, ReferencesHeading(language, GenrePaper)); !strings.HasPrefix(listed, heading) {
			t.Fatal(language, listed)
		}
	}
	if listed, ok := References([]string{"No citations."}, &d, ReferencesHeading("en", GenrePaper)); ok {
		t.Fatal(listed)
	}
}

func TestAnUntitledSourceIsListedByItsAddressOnce(t *testing.T) {
	d := draftingDossier()
	d.Sources = []aicompat.WebSource{
		{N: 3, URL: "https://doi.org/10.1/x"},
		{N: 4, URL: "https://b.example/", Title: "https://b.example/"},
	}
	listed, _ := References([]string{"A [3](https://doi.org/10.1/x) and [4](https://b.example/)."}, &d, ReferencesHeading("en", GenrePaper))
	if want := "## References\n\n\\[3\\] <https://doi.org/10.1/x>\n\n\\[4\\] <https://b.example/>"; listed != want {
		t.Fatalf("got  %q\nwant %q", listed, want)
	}
}

func TestCitationsAreNumberedInTheOrderThePaperFirstCitesThem(t *testing.T) {
	d := draftingDossier()
	for _, s := range []aicompat.WebSource{{N: 93, URL: "https://a.example/"}, {N: 248, URL: "https://b.example/"}, {N: 500, URL: "https://unused.example/"}} {
		s.Title = fmt.Sprintf("Source %d", s.N)
		d.Sources = append(d.Sources, s)
	}
	sections := []string{
		"Old [93](https://a.example/)[248](https://b.example/).",
		"Again [248](https://b.example/), [3](https://example.org/study) and [93](https://a.example/).\n\n```chart\n{\"source\": \"[248](https://b.example/)\"}\n```",
	}
	numbered := NumberCitations(sections, &d)
	if sections[0] != "Old [1](https://a.example/)[2](https://b.example/)." {
		t.Fatal(sections[0])
	}
	if !strings.HasPrefix(sections[1], "Again [2](https://b.example/), [3](https://example.org/study) and [1](https://a.example/).") ||
		!strings.Contains(sections[1], `"[2](https://b.example/)"`) {
		t.Fatal(sections[1])
	}
	var numbers []string
	for _, s := range numbered.Sources {
		numbers = append(numbers, fmt.Sprintf("%d %s", s.N, s.URL))
	}
	sameJSON(t, numbers, []string{"1 https://a.example/", "2 https://b.example/", "3 https://example.org/study"})
	listed, _ := References(sections, &numbered, ReferencesHeading("en", GenrePaper))
	if !strings.HasPrefix(listed, "## References\n\n\\[1\\] Source 93. <https://a.example/>\n\n\\[2\\] Source 248.") || strings.Contains(listed, "unused") {
		t.Fatal(listed)
	}
	if got := PlainCitations(sections[0]); got != `Old [\[1\]](#ref-1)[\[2\]](#ref-2).` {
		t.Fatal(got)
	}
}

// opener answers the opening step with reply.
func opener(reply string) Model {
	return stepFunc(func(string, []aicompat.AgentTurn, []aicompat.ToolSpec) (aicompat.StepResult, error) {
		return aicompat.Reply(reply), nil
	})
}

// opening is the opening model writes for a paper of genre in language.
func opening(t *testing.T, model Model, genre Genre, language string) string {
	paper, outline := testPaper(), testOutline()
	paper.Genre, paper.Language = genre, language
	if language == "en" {
		paper.Topic = "Green roofs"
	}
	return Opening(t.Context(), model, &paper, &outline, nil)
}

func TestTheOpeningIsATitleAndAnAbstractOrTheTopicAlone(t *testing.T) {
	for _, c := range []struct {
		reply, language, want string
	}{
		{`Here: {"title": "屋顶绿化如何为城市降温", "abstract": "本文综述……"}`, "zh", "# 屋顶绿化如何为城市降温\n\n**摘要**：本文综述……"},
		{`{"title": "Cooler roofs", "abstract": "We review…"}`, "en", "# Cooler roofs\n\n**Abstract:** We review…"},
		{"Sorry.", "en", "# Green roofs"},
	} {
		if got := opening(t, opener(c.reply), GenrePaper, c.language); got != c.want {
			t.Errorf("got %q, want %q", got, c.want)
		}
	}
}

func TestAReportOpensOnAHeadlineAndAStandfirstAndListsItsSources(t *testing.T) {
	made := opening(t, opener(`{"title": "城市正在变热", "abstract": "屋顶上的草能让街道凉下来。"}`), GenreReport, "zh")
	if made != "# 城市正在变热\n\n**屋顶上的草能让街道凉下来。**" {
		t.Fatal(made)
	}
	for _, c := range []struct {
		language string
		genre    Genre
		want     string
	}{
		{"zh", GenreReport, "资料来源"},
		{"en", GenreReport, "Sources"},
		{"en", GenrePaper, "References"},
		{"en", GenreThread, "Sources"},
		{"zh", GenreReview, "参考文献"},
		{"zh", GenreAnalysis, "资料来源"},
	} {
		if got := ReferencesHeading(c.language, c.genre); got != c.want {
			t.Errorf("%s %s: %s", c.language, c.genre, got)
		}
	}
}

func TestAThreadOpensOnItsHeadlineAlone(t *testing.T) {
	if made := opening(t, opener(`{"title": "屋顶能降温吗", "abstract": "不该有"}`), GenreThread, "zh"); made != "# 屋顶能降温吗" {
		t.Fatal(made)
	}
}

func TestAReviewIsResearchedInTheLiteratureAndFramedAsAPaper(t *testing.T) {
	if GenreBudget(GenreReview).Limits.PaperLookups <= PaperBudget().Limits.PaperLookups {
		t.Fatal(GenreBudget(GenreReview).Limits)
	}
	if every := GenreReview.Profile().Scale.FigureEvery; every != 3 {
		t.Fatal(every)
	}
	if form := GenreReview.Form(); !form.Checkpoint || !form.Headings {
		t.Fatal(form)
	}
	made := opening(t, opener(`{"title": "屋顶绿化降温研究综述", "abstract": "三类研究。"}`), GenreReview, "zh")
	if made != "# 屋顶绿化降温研究综述\n\n**摘要**：三类研究。" {
		t.Fatal(made)
	}
}

func TestAnAnalysisOpensOnAnExecutiveSummaryAndListsItsSources(t *testing.T) {
	if form := GenreAnalysis.Form(); !form.Checkpoint || !form.Headings {
		t.Fatal(form)
	}
	model := opener(`{"title": "储能需求三年翻倍", "abstract": "建议先布局工商业储能。"}`)
	if made := opening(t, model, GenreAnalysis, "zh"); made != "# 储能需求三年翻倍\n\n**执行摘要**：建议先布局工商业储能。" {
		t.Fatal(made)
	}
	paper, outline := testPaper(), testOutline()
	paper.Genre, paper.Language = GenreAnalysis, "en"
	if made := Opening(t.Context(), model, &paper, &outline, nil); made != "# 储能需求三年翻倍\n\n**Executive summary:** 建议先布局工商业储能。" {
		t.Fatal(made)
	}
	if genre, chars := readChoice(`{"genre": "analysis", "chars": 7000}`, nil); genre != GenreAnalysis || chars != 7000 {
		t.Fatal(genre, chars)
	}
	if genre, chars := readChoice(`{"genre": "review"}`, nil); genre != GenreReview || chars != 7500 {
		t.Fatal(genre, chars)
	}
}

func TestPictureLinesAreBegunUpToTheLimitAndTheRestLeftOut(t *testing.T) {
	text := "Intro.\n\n![Roof](textus-picture:16:9 a green roof)\n\nMid.\n\n![Map](textus-picture:4:3 a heat map)\n\n![Third](textus-picture:1:1 one too many)\n\nEnd."
	placed, unmade := ai.PlacePictures(ai.OfflineImageRun(2), text)
	if strings.Count(placed, "](textus-generated:") != 2 || !strings.Contains(placed, "![Roof](textus-generated:") ||
		strings.Contains(placed, "textus-picture:") || strings.Contains(placed, "\n\n\n") || !strings.HasSuffix(placed, "End.") {
		t.Fatal(placed)
	}
	if len(unmade) != 1 {
		t.Fatal(unmade)
	}
	if got := WithoutPictures(text); got != "Intro.\n\nMid.\n\nEnd." {
		t.Fatalf("%q", got)
	}
}

func TestACitationInAChartStaysValidJSON(t *testing.T) {
	section := "See [3](https://a.example/x).\n\n```chart\n{\"type\": \"bar\", \"x\": [\"a\"], \"series\": [{\"name\": \"n\", \"data\": [1]}], \"source\": \"Survey [3](https://a.example/x)\"}\n```\n\nAfter [3](https://a.example/x)."
	plain := PlainCitations(section)
	if !strings.HasPrefix(plain, `See [\[3\]](#ref-3).`) || !strings.HasSuffix(plain, `After [\[3\]](#ref-3).`) {
		t.Fatal(plain)
	}
	_, body, _ := strings.Cut(plain, "```chart\n")
	body, _, _ = strings.Cut(body, "\n```")
	var spec map[string]any
	if err := json.Unmarshal([]byte(body), &spec); err != nil || spec["source"] != "Survey [3]" {
		t.Fatal(body, err)
	}
}

func TestThePaperIsAssembledWithItsOwnCitationNumbers(t *testing.T) {
	if got := PlainCitations("Cooler [3](https://example.org/study), as [12](https://b.example/x?y=1) found."); got != `Cooler [\[3\]](#ref-3), as [\[12\]](#ref-12) found.` {
		t.Fatal(got)
	}
	if got := Assemble("# Title", []string{"## One\n\nA.\n", "## Two\n\nB."}, "## References\n\nR."); got != "# Title\n\n## One\n\nA.\n\n## Two\n\nB.\n\n## References\n\nR." {
		t.Fatalf("%q", got)
	}
	if got := Assemble("# Title", nil, ""); got != "# Title" {
		t.Fatalf("%q", got)
	}
}

func TestAPaperCardsTurnsAreNotSentToTheModel(t *testing.T) {
	kept := ai.WithoutMemoryTurns([]aicompat.AgentTurn{
		aicompat.UserTurn("Write a paper"),
		{Kind: aicompat.ToolCalled, CallID: "paper-1", Name: ai.PaperTurn, Arguments: "{}"},
		{Kind: aicompat.ToolResult, CallID: "paper-1", Output: "{}"},
		aicompat.AssistantTurn("Written."),
	})
	if len(kept) != 2 {
		t.Fatal(kept)
	}
}

func TestAReferenceFilesAddressHasNoSpacesAndReadsBack(t *testing.T) {
	address := referenceAddress("references/Tides 2024/潮汐.md")
	if strings.Contains(address, " ") || !strings.HasPrefix(address, "references/Tides%202024/") {
		t.Fatal(address)
	}
	if path, ok := referencePath(address); !ok || path != "references/Tides 2024/潮汐.md" {
		t.Fatal(path, ok)
	}
	if path, ok := referencePath("https://a.example/"); ok {
		t.Fatal(path)
	}
}

func TestALongTimelineIsDrawnAsATopDownFlowchartAndAShortOneLeftAcross(t *testing.T) {
	var periods strings.Builder
	for i := 1; i <= 6; i++ {
		fmt.Fprintf(&periods, "        P%d : event %d\n", i, i)
	}
	long := "Text.\n\n```mermaid\ntimeline\n    title A history: told\n    section One\n" + periods.String() + "```\n\nMore."
	upright := uprightTimelines(long)
	if !strings.HasPrefix(upright, "Text.\n\n```mermaid\n---\ntitle: \"A history: told\"\n---\nflowchart TB\n    subgraph s1[\"One\"]\n        p1[\"<b>P1</b><br>event 1\"]") {
		t.Fatal(upright)
	}
	if !strings.HasSuffix(upright, "    end\n    p1 --> p2 --> p3 --> p4 --> p5 --> p6\n```\n\nMore.") {
		t.Fatal(upright)
	}
	// Short, laid out across by its writer, or another diagram: left as it
	// is.
	for _, kept := range []string{
		"```mermaid\ntimeline\n    A : a\n    B : b\n```",
		strings.ReplaceAll(long, "timeline\n", "timeline LR\n"),
		"```mermaid\nflowchart LR\n  A : 1 --> B\n```",
	} {
		if got := uprightTimelines(kept); got != kept {
			t.Fatal(got)
		}
	}
}

func TestATimelineWrittenTopDownKeepsEachPeriodsEventsTogether(t *testing.T) {
	// As a writer put it: `timeline TD`, a second event on a line of its
	// own without its colon, and a quote in a label.
	written := "```mermaid\ntimeline TD\n    section Fish\n        2021-09 : Edited \"sea bream\" filed\n            Edible part 1.2x\n        2021-10 : Puffer approved : a second event\n```"
	want := "```mermaid\nflowchart TB\n    subgraph s1[\"Fish\"]\n        p1[\"<b>2021-09</b><br>Edited #quot;sea bream#quot; filed<br>Edible part 1.2x\"]\n        p2[\"<b>2021-10</b><br>Puffer approved<br>a second event\"]\n    end\n    p1 --> p2\n```"
	if got := uprightTimelines(written); got != want {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
}

// TestEveryPromptFillsInForEveryGenre: the prompt files and the code agree
// on what goes in them.
func TestEveryPromptFillsInForEveryGenre(t *testing.T) {
	unfilled := func(text string) {
		t.Helper()
		if strings.Contains(text, "{{") {
			t.Error(text)
		}
	}
	for _, asked := range []*uint32{nil, ptr(uint32(3000))} {
		unfilled(chooseInstructions(asked))
	}
	outline, dossier := testOutline(), testDossier()
	for _, genre := range Genres {
		for _, language := range []string{"en", "zh"} {
			paper := testPaper()
			paper.Genre, paper.Language = genre, language
			unfilled(openingInstructions(&paper))
			unfilled(planInstructions(&paper))
			unfilled(helperInstructions(&paper, materialPrompt([]string{"notes.md"}, true), true))
			for _, images := range []uint32{0, 3} {
				unfilled(outlineInstructions(&paper, shapeOf(genre.Profile().Scale, paper.TargetChars, images)))
			}
			unfilled(writerInstructions(&paper))
			unfilled(revisionMessage(&paper, &outline, 0, &dossier, "Earlier.", []string{"Shorter."}))
			unfilled(reviewInstructions(&paper))
		}
	}
	unfilled(reviewMessage(&outline, []string{"One."}))
}
