package paper

import (
	"fmt"
	"strings"
	"testing"

	"textus/backend/ai/aicompat"
)

func TestAStringIsReadAsFarAsItHasCome(t *testing.T) {
	if got := stringValue(`{"markdown": "Line one\nsays \"hi\" é`, `"markdown"`); got != "Line one\nsays \"hi\" é" {
		t.Fatal(got)
	}
	// An escape cut in half waits for the rest.
	for _, cut := range []string{`"ab\u00`, `"ab\`, `"ab\ud83d`, `"ab\ud83d\`} {
		if got := readAfterKey(":" + cut); got != (afterKey{text: "ab", end: -1}) {
			t.Errorf("%s: %+v", cut, got)
		}
	}
	if got := stringValue(`{"mark`, `"markdown"`); got != "" {
		t.Fatal(got)
	}
}

func TestAWholeStringEndsAfterItsClosingQuote(t *testing.T) {
	text := ` : "a 😀\r\n b", "next"`
	if got := readAfterKey(text); got != (afterKey{text: "a 😀\n b", end: strings.Index(text, ",")}) {
		t.Fatalf("%+v", got)
	}
	// Half a pair, or no character at all, is left out.
	if got := readAfterKey(`: "a\ud83d b\uZZZZ"`); got != (afterKey{text: "a b", end: 19}) {
		t.Fatalf("%+v", got)
	}
}

func TestWhatIsntAStringIsReadPastOrWaitedFor(t *testing.T) {
	for text, want := range map[string]afterKey{
		": 700, ": {other: true, at: 2},
		" x":      {other: true, at: 1},
		"  ":      {pending: true},
		" : ":     {pending: true},
	} {
		if got := readAfterKey(text); got != want {
			t.Errorf("%q: %+v", text, got)
		}
	}
}

// counting hands over the number of tool results it has seen so far,
// until one is kept.
var counting = stepFunc(func(_ string, turns []aicompat.AgentTurn, _ []aicompat.ToolSpec) (aicompat.StepResult, error) {
	seen := 0
	for _, t := range turns {
		if t.Kind == aicompat.ToolResult {
			seen++
		}
	}
	if last := turns[len(turns)-1]; last.Kind == aicompat.ToolResult && strings.HasPrefix(last.Output, "Kept.") {
		return aicompat.Reply("Done."), nil
	}
	return calls(aicompat.AgentTurn{Kind: aicompat.ToolCalled, CallID: fmt.Sprintf("c%d", seen), Name: "submit", Arguments: fmt.Sprint(seen)}), nil
})

func numberTask(steps int) task {
	return task{
		instructions: "Hand over a number.",
		message:      "Go.",
		tool:         aicompat.ToolSpec{Name: "submit", Parameters: map[string]any{"type": "object"}},
		steps:        steps,
	}
}

func atLeastTwo(arguments string) checked[int] {
	var n int
	if _, err := fmt.Sscan(arguments, &n); err == nil && n >= 2 {
		return keptAs(n)
	}
	return notKept[int]("number", []string{"It is less than 2."})
}

func TestAHandOverIsSentBackWithWhyUntilOneHolds(t *testing.T) {
	kept, turns, err := handOver(t.Context(), counting, numberTask(5), atLeastTwo)
	if err != nil || kept == nil || *kept != 2 {
		t.Fatal(kept, err)
	}
	var sentBack []string
	for _, turn := range turns {
		if turn.Kind == aicompat.ToolResult {
			sentBack = append(sentBack, turn.Output)
		}
	}
	if len(sentBack) != 3 || !strings.HasPrefix(sentBack[0], "Not kept - fix these and send the whole number again") ||
		!strings.Contains(sentBack[0], "It is less than 2.") || !strings.HasPrefix(sentBack[2], "Kept.") {
		t.Fatalf("%q", sentBack)
	}
}

func TestStepsThatRunOutKeepNothing(t *testing.T) {
	kept, _, err := handOver(t.Context(), counting, numberTask(2), atLeastTwo)
	if err != nil || kept != nil {
		t.Fatal(kept, err)
	}
}
