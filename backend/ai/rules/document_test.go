package rules

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
)

type docHit struct {
	rule     string
	line     int
	original string
}

func docRules(r DocReport) []docHit {
	var out []docHit
	for _, f := range r.Findings {
		out = append(out, docHit{f.Rule, *f.Line, f.Original})
	}
	return out
}

func defaultConv() *Conventions {
	return &Conventions{Project: ProjectConventions{Options: map[string]string{}}}
}

func TestCodeMathFrontMatterAndLinksAreSkipped(t *testing.T) {
	doc := "---\ntitle: a,b\n---\n\n# 标题\n\n今天开会,大家来了。见 `a,b` 和 $x,y$ 以及 [链接](https://a.cn/x,y)。\n\n```\n代码,不查\n```\n\n<div>html,不查</div>\n"
	report := CheckDocument(doc, defaultConv(), LevelStandard, nil)
	if report.Lang != ZhHans {
		t.Fatalf("lang %q", report.Lang)
	}
	if got, want := docRules(report), []docHit{{"zh.punct.halfwidth", 7, ","}}; !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	// The offset is into the source.
	f := report.Findings[0]
	if []rune(doc)[f.Start] != ',' || deref(f.Suggestion) != "，" {
		t.Errorf("%+v", f)
	}
}

func TestTheMajorityIsTheWholeDocuments(t *testing.T) {
	// Each paragraph alone has no majority; the document has one.
	doc := "我用 React 写。\n\n他用 Vue 写。\n\n她用 Go 写。\n\n你用Rust写。\n"
	report := CheckDocument(doc, defaultConv(), LevelStandard, nil)
	var spacing []Finding
	for _, f := range report.Findings {
		if f.Rule == "zh.space.cjk-latin" {
			spacing = append(spacing, f)
		}
	}
	if len(spacing) != 2 {
		t.Fatalf("%v", report.Findings)
	}
	for _, f := range spacing {
		if *f.Line != 7 {
			t.Errorf("line %d", *f.Line)
		}
	}
}

func TestBlocksKnowTheirKindAndLanguage(t *testing.T) {
	doc := "# Getting started.\n\n- 3 eggs\n- a hour\n\n> He said “the the end”.\n\nThe colour of the centre and the favourite flavor.\n\n这是中文段落,没有问题。\n"
	r := docRules(CheckDocument(doc, defaultConv(), LevelStandard, nil))
	for _, want := range []docHit{
		{"en.heading.end-punct", 1, "."},
		{"en.word.article", 4, "a"},
		{"en.spelling.variety", 8, "flavor"},
		{"zh.punct.halfwidth", 10, ","},
	} {
		if !slices.Contains(r, want) {
			t.Errorf("missing %v in %v", want, r)
		}
	}
	// A list item starting with a number is fine.
	if slices.ContainsFunc(r, func(h docHit) bool { return h.rule == "en.num.sentence-start" }) {
		t.Errorf("%v", r)
	}
}

func TestDocumentLevelRules(t *testing.T) {
	doc := "# One\n\n### Three\n\n#### Four\n\n![](a.png) and ![Image of a cat](b.png)\n\nSee [click here](https://x.y).\n"
	// P2 structure rules only at strict.
	if r := docRules(CheckDocument(doc, defaultConv(), LevelStandard, nil)); len(r) != 0 {
		t.Errorf("%v", r)
	}
	strict := CheckDocument(doc, defaultConv(), LevelStrict, nil)
	n := map[string]int{}
	for _, f := range strict.Findings {
		n[f.Rule]++
	}
	if n["shared.heading-structure"] != 2 || n["shared.image-alt"] != 2 || n["en.link.text"] == 0 {
		t.Errorf("%v", docRules(strict))
	}
	for _, f := range strict.Findings {
		if f.Rule == "en.link.text" && f.Original != "[click here](https://x.y)" {
			t.Errorf("link span %q", f.Original)
		}
		if f.Rule == "shared.image-alt" && !strings.HasPrefix(f.Original, "![") {
			t.Errorf("image span %q", f.Original)
		}
	}
}

func TestASuggestionAcrossMarkupBecomesAFlag(t *testing.T) {
	doc := "我们买了苹果、香蕉……**等**水果，讨论了价格。\n"
	report := CheckDocument(doc, defaultConv(), LevelStandard, nil)
	var f []Finding
	for _, x := range report.Findings {
		if x.Rule == "zh.punct.ellipsis-deng" {
			f = append(f, x)
		}
	}
	if len(f) != 1 {
		t.Fatalf("%v", report.Findings)
	}
	if f[0].Original != "……**等" || f[0].Suggestion != nil || f[0].Fix != FixNone {
		t.Errorf("%+v", f[0])
	}
}

func TestShortBlocksTakeTheDocumentsLanguage(t *testing.T) {
	for _, c := range []struct {
		text     string
		doc, out Lang
	}{
		{"続き書き(補完)", Ja, Ja},
		{"Une phrase", En, En},
		{"这是一段中文，讨论了三个问题。", En, ZhHans},
		{"這是", ZhHans, ZhHans},
		// A long block says what it is.
		{"Les entretiens ont duré deux mois et nous avons rencontré des habitants dans trois quartiers.", En, Fr},
	} {
		if got := blockLanguage(c.text, c.doc); got != c.out {
			t.Errorf("%q in %s: got %q, want %q", c.text, c.doc, got, c.out)
		}
	}
	// A Japanese heading of kanji in a Japanese document is checked as
	// Japanese: no Chinese parenthesis rule.
	doc := "# 概要\n\nこの文書では三つの機能を紹介します。\n\n### 1. 続き書き(補完)\n"
	for _, f := range CheckDocument(doc, defaultConv(), LevelStandard, nil).Findings {
		if strings.HasPrefix(f.Rule, "zh.") {
			t.Errorf("%+v", f)
		}
	}
}

func TestSpacesBesideEmphasisMarkersAreNotFlagged(t *testing.T) {
	doc := "这是一段说明文字。**项目文件夹就是工作的文件夹。** 默认是文档所在的文件夹。\n\n日本語の文書では **太字** や記号を使います。\n"
	if f := CheckDocument(doc, defaultConv(), LevelStandard, nil).Findings; len(f) != 0 {
		t.Errorf("%v", f)
	}
}

func TestGongwenReportMustNotAskForApproval(t *testing.T) {
	doc := "# 关于经费使用情况的报告\n\n今年经费已用完。请批准追加预算。\n"
	conv := defaultConv()
	conv.Setting = map[string]any{"zh-Hans": "zh-Hans-CN/gongwen"}
	report := CheckDocument(doc, conv, LevelStandard, nil)
	if !slices.ContainsFunc(report.Findings, func(f Finding) bool {
		return f.Rule == "zh.gongwen.report-request" && f.Original == "请批准"
	}) {
		t.Errorf("%v", report.Findings)
	}
}

func TestALongDocumentChecksWithinBudget(t *testing.T) {
	para := "我们用 React 和 Tauri 重写了编辑器，启动时间从 3 秒降到了 800ms。The rewrite took two months and the team shipped it.\n\n"
	doc := strings.Repeat(para, 300) // ~ 30,000 chars
	start := time.Now()
	CheckDocument(doc, defaultConv(), LevelStrict, nil)
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("took %v", d)
	}
}

func TestParserMapsTextBackToTheSource(t *testing.T) {
	doc := "A \\*star\\* &amp; *em* `code` $x$ and $20 or $30 <https://a.b/c> [^1]\n\n$$\na,b\n$$\n\n| a,b | c |\n|---|---|\n| 中文,表格 | d |\n\n[^1]: note\n"
	p := parseMarkdown(doc)
	src := []rune(doc)
	var got []string
	for _, b := range p.blocks {
		got = append(got, fmt.Sprintf("%d:%s", b.kind.kind, string(b.text)))
		for i, c := range b.text {
			at := b.at[i]
			switch {
			case c == Placeholder || c == '\n':
			case src[at] == '\\' || src[at] == '&':
				// Escapes and entities map to their start.
			case src[at] != c:
				t.Errorf("%q at %d maps to %q", c, i, src[at])
			}
		}
	}
	want := []string{
		"0:A *star* & em \uFFFC \uFFFC and $20 or $30 https://a.b/c \uFFFC",
		"0:\uFFFC",
		"4:a,b", "4:c", "4:中文,表格", "4:d",
		"0:note",
	}
	if !slices.Equal(got, want) {
		t.Errorf("got  %q\nwant %q", got, want)
	}
	if len(p.links) != 1 || p.links[0].text != "https://a.b/c" || string(src[p.links[0].start:p.links[0].end]) != "<https://a.b/c>" {
		t.Errorf("%+v", p.links)
	}
}
