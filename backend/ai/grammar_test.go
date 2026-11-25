package ai

import (
	"encoding/json/v2"
	"reflect"
	"strings"
	"testing"

	"textus/backend/ai/rules"
)

func TestGrammarExplanationsAreInTheAppsLanguage(t *testing.T) {
	zh := GrammarInstructions("", "zh")
	if !strings.Contains(zh, "one short sentence in Chinese (the language of the writer's app") {
		t.Error("the explanations are not asked for in Chinese")
	}
	if strings.Contains(zh, "{ISSUE_LANGUAGE}") {
		t.Error("the placeholder is left in")
	}
	// No UI language (the live eval) or an unknown one: the paragraph's.
	if !strings.Contains(GrammarInstructions("", ""), "one short sentence in the paragraph's language") {
		t.Error("no UI language should ask for the paragraph's")
	}
	if !strings.Contains(GrammarInstructions("", "xx"), "the paragraph's language") {
		t.Error("an unknown UI language should ask for the paragraph's")
	}
}

func fix(original, suggestion string) GrammarIssue {
	return GrammarIssue{Issue: "test", Suggestion: suggestion, Original: &original}
}

type fixPair struct{ span, suggestion string }

// spans are the (span, suggestion) of each verified issue.
func spans(t *testing.T, paragraph string, issues []GrammarIssue) []fixPair {
	t.Helper()
	out := []fixPair{}
	chars := []rune(paragraph)
	for _, i := range ValidateIssues(issues, paragraph) {
		span := string(chars[i.Start:i.End])
		if i.Original == nil || *i.Original != span {
			t.Errorf("original %v is not the span %q", i.Original, span)
			continue
		}
		out = append(out, fixPair{*i.Original, i.Suggestion})
	}
	return out
}

func one(t *testing.T, paragraph, original, suggestion string) []fixPair {
	t.Helper()
	return spans(t, paragraph, []GrammarIssue{fix(original, suggestion)})
}

func pair(a, b string) []fixPair { return []fixPair{{a, b}} }

func TestAQuotedFixNarrowsToTheWordsItChanges(t *testing.T) {
	for _, c := range []struct{ paragraph, original, suggestion, span, replacement string }{
		{"She has a honest opinion about the exam.", "a honest opinion", "an honest opinion", "a", "an"},
		{"I go to the market and buy apples.", "go to", "went to", "go", "went"},
		// A deletion takes its space with it.
		{"check that the the value is shown", "that the the value", "that the value", "the ", ""},
		// An insertion takes the next word (or, at the end, the last one).
		{"We saw film about it.", "saw film", "saw a film", "film", "a film"},
		{"It ended", "It ended", "It ended.", "ended", "ended."},
		// A changed ending grows back to the whole word.
		{"Les enfants sont allé au parc.", "sont allé", "sont allés", "allé", "allés"},
		{"aunque hacia demasiado calor", "hacia demasiado", "hacía demasiado", "hacia", "hacía"},
		// Moved words: the span covers everything between the first and
		// last change.
		{"obwohl der Kellner hat uns lange warten lassen.", "hat uns lange warten lassen", "uns lange hat warten lassen", "hat uns lange", "uns lange hat"},
		// Without spaces, characters: 的 -> 地, and an insertion takes the
		// character before it.
		{"他高兴的跳了起来", "高兴的跳", "高兴地跳", "的", "地"},
		{"我去了商店", "去了商店", "去了一家商店", "了", "了一家"},
		{"今天下午开会,大家讨论了", "开会,大家", "开会，大家", ",", "，"},
		// A change of spacing or punctuation alone between letters shows
		// the words around it.
		{"The company changed it's logo.", "changed it's logo", "changed its logo", "it's", "its"},
		{"영수증을 두번 출력해서", "두번 출력해서", "두 번 출력해서", "두번", "두 번"},
		{"조금 당황 했다.", "당황 했다", "당황했다", "당황 했다", "당황했다"},
		{"However the plan held.", "However the", "However, the", "However", "However,"},
		// ...but never runs on into a script written without spaces.
		{"我们用React,很方便", "React,很方便", "React，很方便", "React,", "React，"},
	} {
		if got, want := one(t, c.paragraph, c.original, c.suggestion), pair(c.span, c.replacement); !reflect.DeepEqual(got, want) {
			t.Errorf("%q -> %q in %q: got %q, want %q", c.original, c.suggestion, c.paragraph, got, want)
		}
	}
}

func TestTheQuotePicksTheRightCopyOfRepeatedText(t *testing.T) {
	// 的 occurs three times; the quote with its neighbours occurs once,
	// and the fix lands on the 的 of 跑的, not of 最快的.
	p := "他认真的看着我，跑的最快的那个人是他弟弟。"
	out := ValidateIssues([]GrammarIssue{fix("跑的最快", "跑得最快")}, p)
	if len(out) != 1 || out[0].Start != 9 || out[0].End != 10 {
		t.Fatalf("got %+v, want one issue at 9-10", out)
	}
	// A bare repeated quote is ambiguous - dropped - unless a context or
	// matching offsets say which copy is meant.
	if out := ValidateIssues([]GrammarIssue{fix("的", "地")}, p); len(out) != 0 {
		t.Errorf("an ambiguous quote was kept: %+v", out)
	}
	withContext := fix("ab", "AB")
	context := "cd ab ef"
	withContext.Context = &context
	if out := ValidateIssues([]GrammarIssue{withContext}, "ab cd ab ef"); len(out) != 1 || out[0].Start != 6 {
		t.Errorf("the context should pick the second copy: %+v", out)
	}
	withOffsets := fix("ab", "AB")
	withOffsets.Start, withOffsets.End = 6, 8
	if out := ValidateIssues([]GrammarIssue{withOffsets}, "ab cd ab ef"); len(out) != 1 || out[0].Start != 6 {
		t.Errorf("the offsets should pick the second copy: %+v", out)
	}
	// Typography folded only when there is no exact match.
	if got := one(t, "He said “hallo” twice.", `said "hallo"`, `said "hello"`); !reflect.DeepEqual(got, pair("hallo", "hello")) {
		t.Errorf("got %q", got)
	}
	// Not in the paragraph at all.
	if out := ValidateIssues([]GrammarIssue{fix("zz", "z")}, "abcdef"); len(out) != 0 {
		t.Errorf("a quote not in the paragraph was kept: %+v", out)
	}
}

func TestFixesThatChangeNothingOrWriteTextTwiceAreDropped(t *testing.T) {
	// A no-op card: an underline on correct text whose Apply does nothing.
	if out := ValidateIssues([]GrammarIssue{fix("再三考虑", "再三考虑")}, "我们还需要再三考虑。"); len(out) != 0 {
		t.Errorf("a no-op fix was kept: %+v", out)
	}
	// Applied, it would read "uns uns".
	if out := ValidateIssues([]GrammarIssue{fix("hat", "uns")}, "obwohl der Kellner hat uns lange warten lassen."); len(out) != 0 {
		t.Errorf("a doubling fix was kept: %+v", out)
	}
	// A rewrite reaching past its quote: "the team" twice.
	p := "In my opinion, the end result of the project was the fact that the team became closer."
	if out := ValidateIssues([]GrammarIssue{fix("the end result of the project was the fact that", "the project resulted in the team becoming closer")}, p); len(out) != 0 {
		t.Errorf("a rewrite past its quote was kept: %+v", out)
	}
	// An ordinary fix that repeats a short pair stays ("to the").
	if got := one(t, "I went to shop and to the bank.", "to shop", "to the shop"); !reflect.DeepEqual(got, pair("shop", "the shop")) {
		t.Errorf("got %q", got)
	}
}

func TestOverlappingFixesKeepTheFirst(t *testing.T) {
	p := "Each of the participants have their own reasons."
	got := spans(t, p, []GrammarIssue{
		fix("participants have their", "participants has their"),
		fix("have their own", "has its own"),
	})
	if !reflect.DeepEqual(got, pair("have", "has")) {
		t.Errorf("got %q", got)
	}
}

func TestTheReplyIsTheLastIssueArrayInIt(t *testing.T) {
	p := "昨日は映画を見ました。面白かったでしだ。"
	// Notes before the array, and a corrected second array after a first one.
	raw := "Checking the paragraph:\n[{\"original\": \"かったでした\", \"suggestion\": \"かったでした\", \"issue\": \"x\"}]\nWait - that quote is wrong. Corrected:\n[{\"original\": \"かったでしだ\", \"suggestion\": \"かったでした\", \"issue\": \"誤字\"}]"
	out, err := ParseGrammarReply(raw, p)
	if err != nil || len(out) != 1 || *out[0].Original != "だ" || out[0].Suggestion != "た" {
		t.Fatalf("got %+v, %v", out, err)
	}
	if out, err := ParseGrammarReply("[]", p); err != nil || len(out) != 0 {
		t.Errorf("[] should be no issues: %+v, %v", out, err)
	}
	// Something that isn't the issue array: an error, so the command
	// retries once.
	for _, raw := range []string{"[10, 12]", "no issues"} {
		if _, err := ParseGrammarReply(raw, p); err == nil {
			t.Errorf("%q should be an error", raw)
		}
	}
}

func TestTheRulesEngineAddsWhatTheModelMissed(t *testing.T) {
	conv := &rules.Conventions{}
	finish := func(model, p, level string) []GrammarIssue {
		t.Helper()
		out, err := FinishGrammar(model, p, level, conv, nil, "")
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	p := "今天下午开会,大家讨论了三个问题.第一个是明年的预算。"
	model := `{"corrected": "", "issues": [{"original": "问题.第一个", "suggestion": "问题。第一个", "issue": "句号"}]}`
	out := finish(model, p, "standard")
	var fixes []fixPair
	for _, i := range out {
		fixes = append(fixes, fixPair{*i.Original, i.Suggestion})
	}
	// The model's fix for the full stop, and the comma it missed - once.
	if want := []fixPair{{",", "，"}, {".", "。"}}; !reflect.DeepEqual(fixes, want) {
		t.Fatalf("got %q, want %q", fixes, want)
	}
	if out[1].Issue != "句号" || out[0].Rule == nil || *out[0].Rule != "zh.punct.halfwidth" || out[1].Rule != nil {
		t.Errorf("got %+v", out)
	}
	// Japanese gets 、; numbers and Latin text are left alone.
	if ja := finish("[]", "今日は雨,明日は晴れ.", "standard"); len(ja) != 2 || ja[0].Suggestion != "、" || ja[1].Suggestion != "。" {
		t.Errorf("got %+v", ja)
	}
	if out := finish("[]", "备份 MySQL 8.0, 保留 30 天。Done, ok.", "standard"); len(out) != 0 {
		t.Errorf("numbers and Latin text were flagged: %+v", out)
	}
	// "typos": punctuation is not a typo, a wrong character is.
	if out := finish("[]", p, "typos"); len(out) != 0 {
		t.Errorf("punctuation flagged as a typo: %+v", out)
	}
	if typo := finish("[]", "我们要再接再励。", "typos"); len(typo) == 0 || typo[0].Suggestion != "再接再厉" {
		t.Errorf("got %+v", typo)
	}
	// A finding without a fix (es.punct.inverted) never reaches the
	// editor, which would apply its empty suggestion.
	if out := finish("[]", "Vienes mañana conmigo al cine esta noche?", "standard"); len(out) != 0 {
		t.Errorf("a finding without a fix came through: %+v", out)
	}
	// The rule id is serialized; the model's issues carry none.
	raw, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	if text := string(raw); !strings.Contains(text, `"rule":"zh.punct.halfwidth"`) || strings.Count(text, `"rule"`) != 1 {
		t.Errorf("got %s", text)
	}
}

func TestTheOlderOffsetShapeStillWorks(t *testing.T) {
	legacy := GrammarIssue{Issue: "test", Suggestion: "CD", Start: 2, End: 4}
	if out := ValidateIssues([]GrammarIssue{legacy}, "abcdef"); len(out) != 1 || *out[0].Original != "cd" {
		t.Errorf("got %+v", out)
	}
	for _, span := range [][2]int{{4, 2}, {0, 99}} {
		legacy.Start, legacy.End = span[0], span[1]
		if out := ValidateIssues([]GrammarIssue{legacy}, "abcdef"); len(out) != 0 {
			t.Errorf("%v was kept: %+v", span, out)
		}
	}
}

func TestAGrammarIssueIsTheShapeTheEditorReads(t *testing.T) {
	original := "a"
	raw, err := json.Marshal(GrammarIssue{Start: 1, End: 2, Issue: "x", Suggestion: "an", Original: &original})
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"start":1,"end":2,"issue":"x","suggestion":"an","original":"a"}`; string(raw) != want {
		t.Errorf("got %s, want %s", raw, want)
	}
}
