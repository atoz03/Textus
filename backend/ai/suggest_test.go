package ai

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

func TestTheAnswerIsOneShortTitleOrNothing(t *testing.T) {
	for answer, want := range map[string]string{
		"春天的花园":                           "春天的花园",
		"\n\"Notes on the Q3 budget.\"\n": "Notes on the Q3 budget",
		"Title: Trip to Kyoto":            "Trip to Kyoto",
		"# 《旅行计划》。":                       "旅行计划",
		"NONE":                            "",
		"  ":                              "",
		strings.Repeat("word ", 20):       "",
	} {
		if got, ok := Title(answer); got != want || ok != (want != "") {
			t.Errorf("%q: %q %v", answer, got, ok)
		}
	}
}

func TestOnlyTheStartOfALongDocumentIsSent(t *testing.T) {
	text := TitleInput(strings.Repeat("a", longestExcerpt) + "END")
	if strings.Contains(text, "END") || !strings.Contains(text, strings.Repeat("a", 100)) {
		t.Error(text)
	}
}

func TestEachLineIsOneShortMessageOrNothing(t *testing.T) {
	for line, want := range map[string]string{
		"继续写第三章":                      "继续写第三章",
		`"Make the ending darker"`:    "Make the ending darker",
		"「把标题改短一点」":                   "把标题改短一点",
		"- Translate it into French":  "Translate it into French",
		"2. Add a summary at the top": "Add a summary at the top",
		"1、缩短这一节":                     "缩短这一节",
		"3D charts":                   "3D charts",
		"NONE":                        "",
		"   ":                         "",
		strings.Repeat("很长", 50):      "",
	} {
		if got, ok := suggestion(line); got != want || ok != (want != "") {
			t.Errorf("%q: %q %v", line, got, ok)
		}
	}
}

func TestTheAnswerIsUpToThreeDifferentMessages(t *testing.T) {
	got := Suggestions("\n1. Shorten this section\n2. Add a summary at the top\n\n- shorten this section\n3. Fix the title\n4. Translate it")
	if !slices.Equal(got, []string{"Shorten this section", "Add a summary at the top", "Fix the title"}) {
		t.Error(got)
	}
	if got := Suggestions("继续写第三章"); !slices.Equal(got, []string{"继续写第三章"}) {
		t.Error(got)
	}
	for _, none := range []string{"NONE", ""} {
		if got := Suggestions(none); got == nil || len(got) != 0 {
			t.Errorf("%q: %v", none, got)
		}
	}
}

func TestOnlyTheLastMessagesAndTheReplyAreSent(t *testing.T) {
	e := Exchange{Reply: "Done."}
	for n := 1; n <= 6; n++ {
		e.Messages = append(e.Messages, fmt.Sprintf("message %d", n))
	}
	text := NextMessageInput(e)
	if strings.Contains(text, "message 2") || !strings.Contains(text, "message 3") || !strings.Contains(text, "message 6") || !strings.Contains(text, "Done.") {
		t.Error(text)
	}
}

func TestAClippedTextIsCutByCharacters(t *testing.T) {
	if got := Clip("  春天 的花园  ", 3); got != "春天…" {
		t.Errorf("%q", got)
	}
	if got := Clip(" short ", 10); got != "short" {
		t.Errorf("%q", got)
	}
}
