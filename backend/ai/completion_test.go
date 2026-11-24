package ai

import (
	"strings"
	"testing"
)

func str(s string) *string { return &s }

func hintsWith(prose string, ui *string) CompletionHints {
	return CompletionHints{Block: str("paragraph"), Prose: &prose, UILanguage: ui}
}

func language(before string, h CompletionHints) [2]any {
	name, fromProse := completionLanguage(before, "", h)
	return [2]any{name, fromProse}
}

func TestADiagramWithNoProseAroundItCompletesInTheAppsLanguage(t *testing.T) {
	before := "graph TD\n  A[Start] --> B{Is it?}\n  B -->|Yes| C[OK]"
	hints := hintsWith("", str("zh"))
	if got := language(before, hints); got != [2]any{"Chinese", false} {
		t.Error(got)
	}
	if instructions, _ := CompletionPrompt(before, "", hints, ""); !strings.Contains(instructions, "so write in Chinese, the language of the writer's app") {
		t.Error(instructions)
	}
	// Without the frontend's prose, bare diagram keywords still don't read
	// as a language.
	if got := language("graph TD\n  A --> B\n  B --> C", CompletionHints{UILanguage: str("zh")}); got != [2]any{"Chinese", false} {
		t.Error(got)
	}
}

func TestTheProseDecidesTheLanguageOverTheAppAndTheCode(t *testing.T) {
	code := "SELECT id, name FROM users WHERE active = true ORDER BY name"
	for _, c := range []struct {
		before, prose, ui string
		want              [2]any
	}{
		{code, "这项研究在三个街区进行，我们和居民做了访谈。", "en", [2]any{"Chinese", true}},
		{code, "The survey ran in three districts, and we talked to the people who live there.", "zh", [2]any{"English", true}},
		// Too short for the detector, but its script says enough.
		{"", "Привет", "zh", [2]any{"Russian", true}},
		// A few ideographs: the Chinese the app is in, not a guess between
		// Simplified and Traditional.
		{"", "圖表", "zh-Hant", [2]any{"Traditional Chinese", false}},
	} {
		if got := language(c.before, hintsWith(c.prose, &c.ui)); got != c.want {
			t.Errorf("%s: %v", c.prose, got)
		}
	}
}

func TestCodeAndBareRequestsGetNoLanguageLine(t *testing.T) {
	if got := language("fn main() {", CompletionHints{Block: str(CodeBlockHint), UILanguage: str("zh")}); got != [2]any{"", false} {
		t.Error(got)
	}
	if got := language("", CompletionHints{}); got != [2]any{"", false} {
		t.Error(got)
	}
	if instructions, _ := CompletionPrompt("", "", CompletionHints{}, ""); strings.Contains(instructions, "Language:") {
		t.Error("a language line with nothing to tell")
	}
	if strings.ContainsFunc(completionInstructions, func(r rune) bool { return r >= 0x400 && r <= 0x4ff }) {
		t.Error("an example word in Cyrillic: what a context with no prose of its own fell back on")
	}
}

func TestTheToneAndTheAlternativesAreAskedFor(t *testing.T) {
	n := 3
	instructions, _ := CompletionPrompt("It was", "", CompletionHints{Length: str("long"), Candidates: &n}, " Formal. ")
	for _, want := range []string{lengthLong, "Tone the user chose in Settings: Formal. It sets", "Write 3 alternative continuations", "only <<<>>>."} {
		if !strings.Contains(instructions, want) {
			t.Errorf("no %q", want)
		}
	}
}

func TestAChineseClauseAtTheCursorIsWrittenOutAndTakenOff(t *testing.T) {
	for before, want := range map[string]string{
		"告诉自己今天先跑十分":        "告诉自己今天先跑十分",
		"她每天六点准时出现在操场上的跑道边": "出现在操场上的跑道边",
		"好的，":     "",
		"Rust 重写": "重写",
		"the end": "",
	} {
		if got := clauseAtCursor(before); got != want {
			t.Errorf("%s: %q", before, got)
		}
	}
	user := completionUserText("我每天先跑十分", "", CompletionHints{})
	if !strings.Contains(user, "<text-before-cursor></text-before-cursor>") || !strings.Contains(user, "<clause-at-cursor>我每天先跑十分</clause-at-cursor>") {
		t.Error(user)
	}
	fits(t, [][3]string{
		{"我每天先跑十分", "我每天先跑十分钟就好。", "钟就好。"},
		{"没", "没想到会坚持这么久。", "想到会坚持这么久。"},
	})
}

func fits(t *testing.T, cases [][3]string) {
	t.Helper()
	for _, c := range cases {
		if got := fitToCursor(c[0], c[1]); got != c[2] {
			t.Errorf("%q + %q: %q, not %q", c[0], c[1], got, c[2])
		}
	}
}

func TestAnAsYouTypeSuggestionStopsAtItsFirstClause(t *testing.T) {
	short := CompletionHints{Length: str("short")}
	for _, c := range [][3]string{
		{"我每天先跑十分", "我每天先跑十分钟就好。后来越跑越远。", "钟就好。"},
		{"The tide went", "went out at dawn. Boats lay still.", " out at dawn."},
		{"我这边可能", "我这边可能需要延期一周，主要是问卷回收比预期慢。", "需要延期一周，"},
	} {
		if got := FinishSuggestion(c[0], "", c[1], short); got != c[2] {
			t.Errorf("%q: %q", c[1], got)
		}
	}
	for text, want := range map[string]string{
		"跑得不快": "跑得不快",
		" and it only drips when the flat above uses water, mostly in the evening":                          " and it only drips when the flat above uses water,",
		" one two three four five six seven eight nine ten eleven twelve thirteen fourteen fifteen sixteen": " one two three four five six seven eight nine ten eleven twelve thirteen fourteen fifteen",
	} {
		if got := firstClause(text); got != want {
			t.Errorf("%q: %q", text, got)
		}
	}
	// "3.5" and a closing quote don't end a sentence early.
	if got := firstSentence("version 3.5 is out.” Then"); got != "version 3.5 is out.”" {
		t.Error(got)
	}
	// An explicit continuation keeps all its sentences.
	if got := FinishSuggestion("The tide went", "", "went out. Boats lay still.", CompletionHints{Length: str("long")}); got != " out. Boats lay still." {
		t.Error(got)
	}
}

func TestNothingDoublesUpAtTheJoin(t *testing.T) {
	fits(t, [][3]string{
		// A clause mark the text already ends with.
		{"只收回了 120 份，", "，离计划还差不少。", "离计划还差不少。"},
		{"If all goes well,", ", we finish.", " we finish."},
		// An ellipsis the reply opened with.
		{"请他们在班会上再提醒", "……学生填写。", "学生填写。"},
		// A different mark, or one in the middle, stays.
		{"她笑着说，", "“记不清了。”", "“记不清了。”"},
	})
}

func TestTheHintsComeBeforeTheCursorSplit(t *testing.T) {
	hints := CompletionHints{Title: str("Field notes"), Section: []string{"Methods", "Sampling"}, Block: str("list item"), Length: str("short")}
	if got := completionUserText("We took, ", "", hints); got != "<document-title>Field notes</document-title>\n<section>Methods > Sampling</section>\n<block>list item</block>\n<text-before-cursor>We took, </text-before-cursor>\n<text-after-cursor></text-after-cursor>" {
		t.Error(got)
	}
	// No hints: just the split.
	if got := completionUserText("it is ", "b", CompletionHints{}); got != "<text-before-cursor>it is </text-before-cursor>\n<text-after-cursor>b</text-after-cursor>" {
		t.Error(got)
	}
	// The word the cursor is in or right after gets a tag of its own.
	if got := completionUserText("a copy of the docu", "", CompletionHints{}); got != "<text-before-cursor>a copy of the </text-before-cursor>\n<word-at-cursor>docu</word-at-cursor>\n<text-after-cursor></text-after-cursor>" {
		t.Error(got)
	}
}

func TestTheWordAtTheCursorIsTheWholeWrittenWord(t *testing.T) {
	for before, want := range map[string]string{
		"a full copy of the docu": "docu",
		"it quickly":              "quickly",
		"Lessons":                 "Lessons",
		"I don't":                 "don't",
		"a well-known":            "well-known",
		"version 3.5":             "3.5",
		"closed 2,300":            "2,300",
		"직장인들이":                   "직장인들이",
		// A short word before an apostrophe: an elision or a contraction.
		"réserver l'": "l'",
		"I don'":      "don'",
		// Joiners only inside a word.
		"see e.g.": "",
		`("hello`:  "hello",
		// Nothing to complete: a space, punctuation, a closing quote or
		// plural possessive, or a script written without spaces.
		"thank you for ": "",
		"Berufstätige,":  "",
		"the students'":  "",
		"规划者在制定方案时":      "",
		"":               "",
	} {
		if got := wordAtCursor(before); got != want {
			t.Errorf("%q: %q", before, got)
		}
	}
}

func TestCompletionsJoinTheTextBeforeTheCursor(t *testing.T) {
	fits(t, [][3]string{
		// The word at the cursor written out in full is cut off again.
		{"a full copy of the docu", "document grew", "ment grew"},
		{"on long files it quickly", "quickly grew", " grew"},
		{"직장인들이", "직장인들이 들른다", " 들른다"},
		{"준비물\n\n상비", "상비약", "약"},
		{"réserver l'", "l'hôtel", "hôtel"},
		// A longer echo goes too: two words of a heading, a list item.
		{"Lessons from", "Lessons from a year of Tauri", " a year of Tauri"},
		{"« Tu pars ? »\n\n« Seulement si", "Seulement si tu viens", " tu viens"},
		// ...and in scripts written without spaces, from two characters.
		{"护照\n\n常用", "常用药品", "药品"},
		{"结果", "结果排队的人很多", "排队的人很多"},
		{"但内存占用", "占用反而上升了", "反而上升了"},
		{"「雨が止むまで、", "止むまで、ここで待つ", "ここで待つ"},
		{"一滴，两滴", "，三滴", "，三滴"},
		// The word left out, and letters follow: a word the text already
		// uses whole gets its space; otherwise nothing is shown.
		{"it creaked, and it was narrow, and", "the shelves", " the shelves"},
		{"on long files it quickly", "grew", ""},
		{"a full copy of the docu", "ument", ""},
		{"Deshalb kommen viele Berufst", "ätige", ""},
		// Anything else: a space before a new start, none before what
		// attaches to the word, none after an elision.
		{"set the", "`max_retries`", " `max_retries`"},
		{"if a", "< b", " < b"},
		{"Berufstätige", ", die abends", ", die abends"},
		{"réserver l'", "hôtel", "hôtel"},
		// After punctuation, a new word gets its space.
		{"the students'", "books", " books"},
		{"doubled in six months.", "Le problème", " Le problème"},
		// A word with an apostrophe, hyphen or dot is written out whole.
		{"I don't", "don't think so", " think so"},
		{"I don'", "don't know", "t know"},
		{"I don'", "t know", "t know"},
		{"C'est", "C'est la vie", " la vie"},
		{"a well-known", "well-known fact", " fact"},
		{"a well-known", "known fact", " fact"},
		{"version 3.5", "3.5 adds", " adds"},
		// The repeat and nothing more: nothing to add.
		{"it quickly", "quickly", ""},
		// Unspaced scripts and spaces already typed: nothing added.
		{"规划者在制定方案时", "应当", "应当"},
		{"我们用", "React 重写", "React 重写"},
		{"ฉันชอบ", "กินข้าว", "กินข้าว"},
		{"thank you for ", "your note", "your note"},
		// Code indentation at the start of a line is kept.
		{"for s in xs:\n", "    total += s", "    total += s"},
	})
	finishes(t, [][4]string{
		{"def f():", "\n    return 1", "\n    return 1", "code"},
		// Each candidate is fitted, and each later one starts on the line
		// after its separator, so its joining space survives the split.
		{"it quickly", "quickly grew\n<<<>>>\nquickly slowed", " grew\n<<<>>>\n slowed"},
		{"it quickly", "quickly grew\n\n<<<>>>\n\nquickly slowed\n<<<>>>\n", " grew\n\n<<<>>>\n slowed\n<<<>>>\n"},
	})
}

// finishes checks finishCompletion: before, the reply, the result, and
// "code" for a code block.
func finishes(t *testing.T, cases [][4]string) {
	t.Helper()
	for _, c := range cases {
		if got := finishCompletion(c[0], "", c[1], c[3] == "code"); got != c[2] {
			t.Errorf("%q + %q: %q, not %q", c[0], c[1], got, c[2])
		}
	}
}

func TestRepliesThatAreNotTextToInsertAreCleanedOrDropped(t *testing.T) {
	finishes(t, [][4]string{
		// A leaked tag goes; one the document itself uses stays.
		{"준비물\n\n상비", "상비약</reasoning_effort>", "약"},
		{"Use <br> for breaks and", "and <br> again", " <br> again"},
		{"if a", " < b", " < b"},
		// Several lines outside code: the model's reasoning, or a second
		// block - dropped whole. Inside code they are the code.
		{"a Markdown editor that", "that\"s where it stops? No - the text ends with \"that\".\n\nthat keeps notes", ""},
		{"指标\n\n崩溃", "率\n\nWait - the rules say...", ""},
		// ...unless it ended by giving its first line again.
		{"指标\n\n崩溃", "率\n\n</reasoning_effort>\n\nWait, I must not include tags. Output the cell content only.\n\n率", "率"},
		// A figure the text doesn't have is dropped; one it has, or the
		// next in its count, stays.
		{"启动服务并检查", "，确认健康检查接口返回 200", ""},
		{"We closed 2,300 tickets, up from", "up from 1,980 last quarter.", ""},
		{"We closed 2,300 tickets and", "and 2300 is a record.", " 2300 is a record."},
		{"Step 2: install. Step", "Step 3: run it.", " 3: run it."},
		// Chinese wrapped whole in ASCII quotes: the quotes are the
		// model's. Quotes that are part of the text stay.
		{"規劃者在制定方案時", "'就需要先傾聽居民的意見。'", "就需要先傾聽居民的意見。"},
		{"He said", "\"I'm done.\"", " \"I'm done.\""},
		{"for s in xs:\n", "    total += s\n    n += 1", "    total += s\n    n += 1", "code"},
		// Line breaks at either end outside code go, the text kept.
		{"Notes\n\n", "\nThe pilot ran", "The pilot ran"},
		{"指标\n\n崩溃", "率\n\n<br>", "率"},
		{"指标\n\n崩溃", "率\n", "率"},
		{"def f():", "\n    return 1\n", "\n    return 1\n", "code"},
		// Backticks a plain-text context doesn't use would be inserted
		// literally.
		{"To change it, set the", "the `max_retries` option", " max_retries option"},
		{"Call `upload` with", "with `path`", " `path`"},
		// An alternative that breaks the rules is dropped, the others kept.
		{"it quickly", "quickly grew\n<<<>>>\nquickly slowed\nand stopped\n<<<>>>\nquickly shrank", " grew\n<<<>>>\n\n<<<>>>\n shrank"},
	})
}
