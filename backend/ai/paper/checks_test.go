package paper

import (
	"encoding/json/v2"
	"os"
	"slices"
	"strings"
	"testing"

	"textus/backend/ai/aicompat"
)

// draftingDossier is testDossier with the source its note cites, 3.
func draftingDossier() Dossier {
	d := testDossier()
	d.Sources = []aicompat.WebSource{{N: 3, URL: "https://example.org/study", Title: "Study"}}
	return d
}

// sectionText is a section of n words citing source 3, with extra after
// it.
func sectionText(n int, extra string) string {
	return strings.Repeat("Green ", n) + "roofs cool [3](https://example.org/study).\n\n" + extra
}

// testSection is testOutline's section, words long.
func testSection(words uint32) OutlineSection {
	section := testOutline().Sections[0]
	section.TargetWords = words
	return section
}

var studySource = []numbered{{3, "https://example.org/study"}}

func joined(wrong []Problem) string { return strings.Join(problemTexts(wrong), "\n") }

func TestAChartPassesOnlyIfTheRendererWouldDrawIt(t *testing.T) {
	raw, err := os.ReadFile("../../../src/editor/chart/chart-cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases struct {
		Cases []struct {
			Spec  any  `json:"spec"`
			Draws bool `json:"draws"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases.Cases) == 0 {
		t.Fatal("no cases")
	}
	for _, c := range cases.Cases {
		spec, _ := json.Marshal(c.Spec)
		if why := chartHolds(string(spec)); (why == "") != c.Draws {
			t.Errorf("%s: %q", spec, why)
		}
	}
}

func TestASeriesPastTheRenderersPointsIsRefused(t *testing.T) {
	data := make([]int, 1001)
	for i := range data {
		data[i] = i
	}
	spec, _ := json.Marshal(map[string]any{"series": []any{map[string]any{"data": data}}})
	if why := chartHolds(string(spec)); !strings.Contains(why, "at most 1000") {
		t.Fatal(why)
	}
}

func TestASectionIsCheckedForLengthCitationsAndFigures(t *testing.T) {
	section := testSection(100)
	section.Figures = []FigurePlan{{Kind: Mermaid, Spec: "flow"}, {Kind: Chart, Spec: "temps"}}
	chart := "```chart\n{\"type\": \"bar\", \"labels\": [\"a\"], \"series\": [{\"data\": [12]}]}\n```"
	good := sectionText(97, "```mermaid\nflowchart LR\n  A --> B\n```\n\n"+chart)
	if found := problems(good, &section, studySource, "en"); len(found) != 0 {
		t.Fatal(found)
	}

	bad := strings.Repeat("Green ", 20) + " cool [3](https://wrong.example/) and [4](https://x.example/) and [3] alone.\n\n```chart\n{\"type\": \"radar\", \"series\": []}\n```"
	found := joined(problems(bad, &section, studySource, "en"))
	for _, want := range []string{
		"Far too short: 25 against about 100",
		"[3] links to https://wrong.example/ but source 3 is https://example.org/study",
		"[4] isn't one of this section's sources",
		"written as [n] alone",
		`Chart 1: "type" "radar"`,
		"1 planned diagram(s), 0 ```mermaid",
		"Nothing is cited",
	} {
		if !strings.Contains(found, want) {
			t.Errorf("no %q in %s", want, found)
		}
	}
}

func TestChineseSectionsCountCharactersAndSkipFigures(t *testing.T) {
	if got := length("屋顶 绿化\n降温", "zh-CN"); got != 6 {
		t.Fatal(got)
	}
	if got := length("Green roofs cool", "en"); got != 3 {
		t.Fatal(got)
	}
}

func TestAChartsCategoriesLabelsAndSourceAreChecked(t *testing.T) {
	section := testSection(100)
	section.Figures = []FigurePlan{{Kind: Chart, Spec: "votes"}}
	found := func(chart string) string {
		return joined(problems(sectionText(97, "```chart\n"+chart+"\n```"), &section, studySource, "en"))
	}
	if got := found(`{"x": ["Mayor", "President"], "series": [{"data": [57, 50]}]}`); !strings.Contains(got, `the categories go in "labels"`) {
		t.Error(got)
	}
	if got := found(`{"labels": ["a"], "series": [{"data": [1, 2]}]}`); !strings.Contains(got, `1 "labels" for 2 data points`) {
		t.Error(got)
	}
	if got := found(`{"labels": ["a"], "series": [{"data": [1]}], "source": "n28、n9"}`); !strings.Contains(got, "by their numbers") {
		t.Error(got)
	}
	if got := found(`{"labels": ["a"], "series": [{"data": [1]}], "source": "Election commission"}`); got != "" {
		t.Error(got)
	}
}

func TestOnlyLengthAndMissingFiguresAreSoft(t *testing.T) {
	section := testSection(100)
	// Too long, a planned figure missing: soft, whatever the wording.
	long := problems(sectionText(127, ""), &section, studySource, "en")
	if len(long) == 0 || !allSoft(long) {
		t.Fatal(long)
	}
	// A source that isn't the section's, nothing cited: never soft.
	wrong := problems(strings.ReplaceAll(sectionText(100, ""), "[3]", "[4]"), &section, studySource, "en")
	if allSoft(wrong) {
		t.Fatal(wrong)
	}
	if !allSoft(nil) {
		t.Fatal("nothing isn't soft")
	}
}

func TestASectionsLengthHoldsIsSoftOrIsSentBack(t *testing.T) {
	section := testSection(100)
	section.Figures = nil
	found := func(words int) []Problem { return problems(sectionText(words, ""), &section, studySource, "en") }
	text := func(words int) string { return strings.Join(problemTexts(found(words)), " ") }
	if got := text(107); got != "" {
		t.Fatal(got)
	}
	if got := text(127); !strings.HasPrefix(got, "It is 130 long; the section is to be about 100 (at most 120)") || !allSoft(found(127)) {
		t.Fatal(got)
	}
	if got := text(147); !strings.HasPrefix(got, "Far too long: 150 against about 100 - cut it to at most 120") || allSoft(found(147)) {
		t.Fatal(got)
	}
	if got := text(57); !strings.HasPrefix(got, "It is 60 long") {
		t.Fatal(got)
	}
	if got := text(37); !strings.HasPrefix(got, "Far too short") {
		t.Fatal(got)
	}
	if most(900) != 1080 {
		t.Fatal(most(900))
	}
}

func TestAnAddressWithParenthesesIsCitedWhole(t *testing.T) {
	doi := "https://doi.org/10.1061/40927(243)238"
	safe := linkSafe(doi)
	if safe != "https://doi.org/10.1061/40927%28243%29238" {
		t.Fatal(safe)
	}
	for _, kept := range []string{"https://a.example/%E4%B8%AD", "references/Tides%202024/a.md"} {
		if got := linkSafe(kept); got != kept {
			t.Fatal(got)
		}
	}
	section := testSection(10)
	section.Figures = nil
	cited := "Green roofs cool the roof surface by day, as measured [66](" + safe + ")."
	// Either way of writing it is the same source.
	for _, address := range []string{safe, doi} {
		if found := problems(cited, &section, []numbered{{66, address}}, "en"); len(found) != 0 {
			t.Fatal(address, found)
		}
	}
}

func TestAFigureNamedByNumberIsSentBackAndASlashDoesntMatter(t *testing.T) {
	section := testSection(10)
	section.Figures = nil
	wrong := problems("Roofs cool by day, as 图1 shows, and so on here [3](https://example.org/study/).", &section, studySource, "en")
	if !strings.Contains(joined(wrong), `"图1" names a figure by a number`) || allSoft(wrong) {
		t.Fatal(wrong)
	}
	fine := "Roofs cool by day, as the chart below shows and more [3](https://example.org/study/)."
	if found := problems(fine, &section, studySource, "en"); len(found) != 0 {
		t.Fatal(found)
	}
}

func TestAFigureIsOneValueHoweverItIsWritten(t *testing.T) {
	for text, want := range map[string][]string{
		"3,5 % and 3.50%":          {"3.5"},
		"1,200 people, 1200 seats": {"1200"},
		"约1.2万人":                   {"12000"},
		"12,000 visitors":          {"12000"},
		"3亿元，即 300 million":        {"300000000"},
		"4.5 Million users":        {"4500000"},
		"1,200 roofs, 3.50 m, 7 cities, 1200 again, 2024 [12](https://x.example/12)": {"1200", "3.5", "2024"},
		"增长 30% 至 45.0 亿": {"30", "4500000000"},
	} {
		if got := figures(text); !slices.Equal(got, want) {
			t.Errorf("%s: %q", text, got)
		}
	}
	if got := missingFigures("Cooler by 30 °C on 12 roofs in 2019.", []string{"12 roofs", "30 °C"}); !slices.Equal(got, []string{"2019"}) {
		t.Fatal(got)
	}
}

func TestACitationAfterTheFullStopStaysWithItsSentence(t *testing.T) {
	for text, want := range map[string][]string{
		"屋顶降温 30 度。[1](https://a.example/1)成本较高。[2]":                      {"屋顶降温 30 度。[1](https://a.example/1)", "成本较高。[2]"},
		"Roofs cool. [1](https://a.example/1) They cost more.[2][3] Done": {"Roofs cool. [1](https://a.example/1)", "They cost more.[2][3]", "Done"},
		"Up 3.5 m. Why? 涨了。再涨\nNext":                                      {"Up 3.5 m.", "Why?", "涨了。", "再涨", "Next"},
	} {
		if got := sentences(text); !slices.Equal(got, want) {
			t.Errorf("%s: %q", text, got)
		}
	}
}

func TestAQuoteIsFoundWhateverItsSpacingAndPunctuation(t *testing.T) {
	page := "Roofs were “up to 30 °C cooler” at noon. 绿色屋顶 能 降温。"
	for quote, found := range map[string]bool{
		"up to 30 °C cooler":  true,
		`"Up to 30°C cooler"`: true,
		"绿色屋顶能降温":             true,
		"up to 40 °C cooler":  false,
		" … ":                 true,
	} {
		if hasQuote(page, quote) != found {
			t.Error(quote)
		}
	}
}

func TestACitedSentenceGivesOnlyItsSourcesFigures(t *testing.T) {
	notes := []Note{
		{Claim: "Roofs were 30 °C cooler.", Evidence: "12 roofs, since 2019.", Sources: []uint32{1}, Unmatched: []string{"2019"}},
		{Claim: "It cost 4.5 million.", Sources: []uint32{2}},
	}
	given := figuresBySource(notes)
	section := "Roofs were 30 °C cooler on 12 roofs [1](https://a.example/1). They cost 4.5 million [1](https://a.example/1). Since 2019 [1](https://a.example/1), and 40 more uncited.\n\n```chart\n{\"data\": [77]}\n```"
	found := unsupportedFigures(section, given)
	if len(found) != 2 {
		t.Fatal(found)
	}
	if found[0].Text != `A figure its sources don't give: "They cost 4.5 million ." has 4.5 million but the notes of [1] don't - give the figure as the notes do, cite the source that gives it, or leave it out.` {
		t.Fatal(found[0].Text)
	}
	// Sent back, but kept on a writer's last try, as a length is.
	if !strings.Contains(found[1].Text, "has 2019") || !found[0].Soft {
		t.Fatal(found)
	}
}

func TestTheExcerptIsTheSentenceTheNoteRestsOn(t *testing.T) {
	page := "Green roofs are common in Europe. Measured on 12 roofs, surfaces were up to 30 °C cooler than tar. Costs vary widely by city and by the plants chosen for them."
	got, ok := excerpt(page, "Green roofs lower surface temperature by up to 30 °C, on 12 roofs.")
	if !ok || got != "Measured on 12 roofs, surfaces were up to 30 °C cooler than tar. Costs vary widely by city and by the plants chosen for them." {
		t.Fatal(got, ok)
	}
	if _, ok := excerpt(page, "Pandas eat bamboo."); ok {
		t.Fatal("an excerpt for pandas")
	}
}
