package aicompat

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func call(id, name, arguments string) AgentTurn {
	return AgentTurn{Kind: ToolCalled, CallID: id, Name: name, Arguments: arguments}
}

// scripted is a "model" that answers each step with the next result of its
// script, and records what each step was shown.
type scripted struct {
	script []StepResult
	seen   [][]AgentTurn
}

func (s *scripted) step(turns []AgentTurn) (StepResult, error) {
	s.seen = append(s.seen, turns)
	if len(s.script) == 0 {
		return StepResult{}, errors.New("script ran out")
	}
	next := s.script[0]
	s.script = s.script[1:]
	return next, nil
}

func runScripted(t *testing.T, script []StepResult, maxSteps int, execute func(name, arguments string) ToolOutput) ([]AgentTurn, [][]AgentTurn) {
	t.Helper()
	s := &scripted{script: script}
	turns, err := Loop{
		MaxSteps: maxSteps,
		Step:     s.step,
		Execute:  func(c ToolCall) ToolOutput { return execute(c.Name, c.Arguments) },
		HowToRun: func(string) ToolRun { return InOrder },
	}.Run(nil, UserTurn("go"))
	if err != nil {
		t.Fatal(err)
	}
	return turns, s.seen
}

func TestCitationMarkersAreSwappedOrDropped(t *testing.T) {
	text := "A" + CitationMarker(1) + "B" + CitationMarker(2) + "C"
	got := ReplaceCitationMarkers(text, func(k int) (string, bool) { return "[7]", k == 1 })
	if got != "A[7]BC" {
		t.Errorf("%q", got)
	}
	if got := ReplaceCitationMarkers("plain [1]", func(int) (string, bool) { return "", false }); got != "plain [1]" {
		t.Errorf("%q", got)
	}
}

func TestASpanThatDoesNotFitTheTextIsStillCitedAtTheEnd(t *testing.T) {
	var citations stepCitations
	got := markCitedSpans("Short.", []citedSpan{{start: 40, end: 50, url: "https://a.example/"}}, &citations, Ignore)
	if got != "Short."+CitationMarker(1) {
		t.Errorf("%q", got)
	}
}

func TestToolResultsAreFedBackBeforeTheFinalAnswer(t *testing.T) {
	turns, seen := runScripted(t, []StepResult{
		{Calls: []AgentTurn{call("1", "search_document", `{"query":"x"}`)}},
		Reply("found it"),
	}, 12, func(name, _ string) ToolOutput { return Text("ran " + name) })
	if len(turns) != 4 || turns[2].Kind != ToolResult || turns[2].Output != "ran search_document" ||
		turns[3].Kind != Assistant || turns[3].Text != "found it" {
		t.Fatalf("%+v", turns)
	}
	if len(seen[1]) != 3 {
		t.Errorf("the second step saw %d turns, not the call and its result", len(seen[1]))
	}
}

func TestAMessageSentInMeanwhileGoesOnAfterTheStepsResults(t *testing.T) {
	s := &scripted{script: []StepResult{{Calls: []AgentTurn{call("1", "read_document", "{}")}}, Reply("done")}}
	inbox := [][]AgentTurn{{UserTurn("shorter, please")}, {UserTurn("late")}}
	var reported []string
	turns, err := Loop{
		MaxSteps: 12,
		Step:     s.step,
		Execute:  func(ToolCall) ToolOutput { return Text("text") },
		HowToRun: func(string) ToolRun { return InOrder },
		OnEvent: func(e LoopEvent) {
			if e.Turn != nil && e.Turn.Kind == User {
				reported = append(reported, e.Turn.Text)
			}
		},
		Steered: func() []AgentTurn {
			if len(inbox) == 0 {
				return nil
			}
			next := inbox[0]
			inbox = inbox[1:]
			return next
		},
	}.Run(nil, UserTurn("go"))
	if err != nil {
		t.Fatal(err)
	}
	if len(turns) != 5 || turns[3].Kind != User || turns[3].Text != "shorter, please" {
		t.Fatalf("%+v", turns)
	}
	if last := s.seen[1][len(s.seen[1])-1]; last.Text != "shorter, please" {
		t.Errorf("the next step read %+v", last)
	}
	if !slices.Equal(reported, []string{"shorter, please"}) {
		t.Errorf("reported %v", reported)
	}
	// The answer came before it was asked again: "late" is left unread.
	if len(inbox) != 1 {
		t.Errorf("inbox %v", inbox)
	}
}

func TestEachStepRecordsItsCallsThenTheirResults(t *testing.T) {
	turns, seen := runScripted(t, []StepResult{
		{Calls: []AgentTurn{call("a", "t1", "{}"), call("b", "t2", "{}")}},
		{Calls: []AgentTurn{call("c", "t3", "{}")}},
		Reply(""),
	}, 12, func(name, _ string) ToolOutput { return Text(name) })
	var ids []string
	for _, turn := range turns {
		switch turn.Kind {
		case ToolCalled:
			ids = append(ids, "call:"+turn.CallID)
		case ToolResult:
			ids = append(ids, "result:"+turn.CallID)
		}
	}
	if want := []string{"call:a", "call:b", "result:a", "result:b", "call:c", "result:c"}; !slices.Equal(ids, want) {
		t.Errorf("%v", ids)
	}
	// Each step is shown the one before it plus what it added: earlier
	// turns never change, which keeps the prefix cached.
	if len(seen[2]) != len(seen[1])+2 || fmt.Sprint(seen[2][:len(seen[1])]) != fmt.Sprint(seen[1]) {
		t.Error("an earlier step's turns changed")
	}
}

func TestAToolsImagesLandInItsResultTurn(t *testing.T) {
	turns, _ := runScripted(t, []StepResult{{Calls: []AgentTurn{call("v", "view_image", "{}")}}, Reply("")}, 12,
		func(string, string) ToolOutput {
			return ToolOutput{Text: "Here is map.png.", Images: []ImageAttachment{{Name: "map.png", Mime: "image/png", DataBase64: "AAAA"}}}
		})
	if turns[2].Kind != ToolResult || turns[2].Output != "Here is map.png." || turns[2].Images[0].Name != "map.png" {
		t.Errorf("%+v", turns[2])
	}
}

func TestRunningOutOfStepsStillEndsWithAReply(t *testing.T) {
	var looping []StepResult
	for i := range 3 {
		looping = append(looping, StepResult{Calls: []AgentTurn{call(fmt.Sprint(i), "search_document", "{}")}})
	}
	turns, _ := runScripted(t, looping, 3, func(string, string) ToolOutput { return Text("nothing") })
	if last := turns[len(turns)-1]; last.Kind != Assistant || last.Text != OutOfSteps {
		t.Errorf("%+v", last)
	}
}

func TestTheChatKnowsTheOutOfStepsReply(t *testing.T) {
	ts, err := os.ReadFile("../../../src/ai/chat/out-of-steps.ts")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(ts), `"`+OutOfSteps+`"`) {
		t.Error("out-of-steps.ts has another reply")
	}
}

func TestTheLastStepsAreAnnouncedToTheModelOnly(t *testing.T) {
	var looping []StepResult
	for i := range 4 {
		looping = append(looping, StepResult{Calls: []AgentTurn{call(fmt.Sprint(i), "measure_text", "{}")}})
	}
	turns, seen := runScripted(t, looping, 4, func(string, string) ToolOutput { return Text("12 chars") })
	noted := func(history []AgentTurn) bool {
		return slices.ContainsFunc(history, func(t AgentTurn) bool {
			return t.Kind == ToolResult && strings.Contains(t.Output, "left in this request")
		})
	}
	if noted(seen[1]) {
		t.Error("the second step was told")
	}
	last := func(h []AgentTurn) AgentTurn { return h[len(h)-1] }
	if l := last(seen[2]); !strings.HasPrefix(l.Output, "12 chars") || !strings.Contains(l.Output, "Only 2 steps left") {
		t.Errorf("third step: %q", l.Output)
	}
	if l := last(seen[3]); !strings.Contains(l.Output, "Only 1 step left") {
		t.Errorf("last step: %q", l.Output)
	}
	if fmt.Sprint(seen[3][:len(seen[2])]) != fmt.Sprint(seen[2]) {
		t.Error("earlier turns weren't shown as first sent")
	}
	if noted(turns) {
		t.Error("the note was recorded")
	}
}

func runCountingRetries(t *testing.T, script []StepResult) ([]AgentTurn, int) {
	t.Helper()
	s := &scripted{script: script}
	retries := 0
	turns, err := Loop{
		MaxSteps: 12,
		Step:     s.step,
		Execute:  func(c ToolCall) ToolOutput { return Text(c.Name) },
		HowToRun: func(string) ToolRun { return InOrder },
		OnEvent: func(e LoopEvent) {
			if e.Retry {
				retries++
			}
		},
	}.Run(nil, UserTurn("go"))
	if err != nil {
		t.Fatal(err)
	}
	return turns, retries
}

func TestAGarbledStepIsAskedForAgainOnce(t *testing.T) {
	edit := func(text string) StepResult {
		return StepResult{Calls: []AgentTurn{call("e", "propose_edit", `{"action":"append","text":"`+text+`"}`)}}
	}
	turns, retries := runCountingRetries(t, []StepResult{
		edit("《百年孤\uFFFD\uFFFD\uFFFD》"), edit("《百年孤独》"), Reply("《百年孤\uFFFD》"),
	})
	if retries != 1 {
		t.Errorf("%d retries", retries)
	}
	if !strings.Contains(turns[1].Arguments, "《百年孤独》") {
		t.Errorf("the garbled call ran: %s", turns[1].Arguments)
	}
	// Once per request: a later garbled reply stands.
	if last := turns[len(turns)-1]; !strings.ContainsRune(last.Text, '\uFFFD') {
		t.Errorf("%q", last.Text)
	}
}

func TestAGarbledReplyIsAskedForAgain(t *testing.T) {
	turns, retries := runCountingRetries(t, []StepResult{Reply("《百年孤\uFFFD》"), Reply("《百年孤独》")})
	if retries != 1 || turns[1].Text != "《百年孤独》" {
		t.Errorf("%d retries, %+v", retries, turns)
	}
	// An escaped replacement character in arguments counts too.
	if !garbled(StepResult{Calls: []AgentTurn{call("e", "propose_edit", `{"text":"\ufffd"}`)}}) {
		t.Error("an escaped one passed")
	}
	if garbled(Reply("《百年孤独》")) {
		t.Error("a whole reply is garbled")
	}
}

func resultsOf(turns []AgentTurn) [][2]string {
	var out [][2]string
	for _, turn := range turns {
		if turn.Kind == ToolResult {
			out = append(out, [2]string{turn.CallID, turn.Output})
		}
	}
	return out
}

func TestReadsInOneStepRunTogetherAndAreRecordedInCallOrder(t *testing.T) {
	fastDone := make(chan struct{})
	s := &scripted{script: []StepResult{
		{Calls: []AgentTurn{call("1", "web_fetch", "slow"), call("2", "web_fetch", "fast")}}, Reply(""),
	}}
	turns, err := Loop{
		MaxSteps: 12,
		Step:     s.step,
		Execute: func(c ToolCall) ToolOutput {
			if c.Arguments == "fast" {
				close(fastDone)
				return Text("fast")
			}
			// Run one call at a time, it would wait forever: it gives up,
			// and says so.
			select {
			case <-fastDone:
				return Text("slow")
			case <-time.After(5 * time.Second):
				return Text("ran alone")
			}
		},
		HowToRun: func(string) ToolRun { return Concurrent },
	}.Run(nil, UserTurn("go"))
	if err != nil {
		t.Fatal(err)
	}
	if got := resultsOf(turns); !slices.Equal(got, [][2]string{{"1", "slow"}, {"2", "fast"}}) {
		t.Errorf("%v", got)
	}
}

func TestAProposalWaitsForTheReadsBeforeIt(t *testing.T) {
	var readDone atomic.Bool
	s := &scripted{script: []StepResult{
		{Calls: []AgentTurn{call("r", "read_document", "{}"), call("p", "propose_edit", "{}")}}, Reply(""),
	}}
	turns, err := Loop{
		MaxSteps: 12,
		Step:     s.step,
		Execute: func(c ToolCall) ToolOutput {
			switch {
			case c.Name == "read_document":
				time.Sleep(20 * time.Millisecond)
				readDone.Store(true)
				return Text("read")
			case readDone.Load():
				return Text("proposed after the read")
			}
			return Text("proposed first")
		},
		HowToRun: func(name string) ToolRun {
			if name == "propose_edit" {
				return InOrder
			}
			return Concurrent
		},
	}.Run(nil, UserTurn("go"))
	if err != nil {
		t.Fatal(err)
	}
	if got := resultsOf(turns)[1][1]; got != "proposed after the read" {
		t.Error(got)
	}
}

// proposeThen runs a step of one propose_edit call, with text beside it,
// whose output is settled or not, then (if asked) a closing reply.
func proposeThen(t *testing.T, text string, settled bool) ([]AgentTurn, int) {
	t.Helper()
	s := &scripted{script: []StepResult{
		CallsWithText([]AgentTurn{call("p", "propose_edit", "{}")}, text), Reply("asked again"),
	}}
	turns, err := Loop{
		MaxSteps: 12,
		Step:     s.step,
		Execute:  func(ToolCall) ToolOutput { return ToolOutput{Text: "Edit proposed", Settled: settled} },
		HowToRun: func(string) ToolRun { return InOrder },
	}.Run(nil, UserTurn("go"))
	if err != nil {
		t.Fatal(err)
	}
	return turns, len(s.seen)
}

func replyOf(turns []AgentTurn) string {
	if last := turns[len(turns)-1]; last.Kind == Assistant {
		return last.Text
	}
	return "(no reply)"
}

func TestASettledProposalWithItsReplyEndsTheExchange(t *testing.T) {
	turns, steps := proposeThen(t, "Tightened the opening.", true)
	if steps != 1 {
		t.Errorf("%d steps: a request just for the closing sentence", steps)
	}
	if replyOf(turns) != "Tightened the opening." || turns[2].Kind != ToolResult {
		t.Errorf("%+v", turns)
	}
}

func commentsThen(t *testing.T, calls int) int {
	t.Helper()
	var step []AgentTurn
	for i := range calls {
		step = append(step, call(fmt.Sprint("c", i), "add_comment", "{}"))
	}
	s := &scripted{script: []StepResult{CallsWithText(step, "Glossed the hard words."), Reply("and the rest")}}
	if _, err := (Loop{
		MaxSteps: 12,
		Step:     s.step,
		Execute: func(ToolCall) ToolOutput {
			return ToolOutput{Text: "Comment added", Settled: true, SettlesWithOthers: true}
		},
		HowToRun: func(string) ToolRun { return InOrder },
	}).Run(nil, UserTurn("go")); err != nil {
		t.Fatal(err)
	}
	return len(s.seen)
}

func TestACommentPinnedAloneGoesOnOneMoreStepAndSeveralEndThere(t *testing.T) {
	if one, several := commentsThen(t, 1), commentsThen(t, 3); one != 2 || several != 1 {
		t.Errorf("one comment took %d steps, three %d", one, several)
	}
}

func TestWithoutAReplyOrASettledCallTheModelIsAskedAgain(t *testing.T) {
	if turns, steps := proposeThen(t, "", true); steps != 2 || replyOf(turns) != "asked again" {
		t.Errorf("no reply: %d steps, %q", steps, replyOf(turns))
	}
	// A refused proposal: the model must see why, so the text it wrote
	// before knowing is dropped.
	if turns, steps := proposeThen(t, "Tightened the opening.", false); steps != 2 || replyOf(turns) != "asked again" {
		t.Errorf("refused: %d steps, %q", steps, replyOf(turns))
	}
}

func TestAnAmendingToolRewritesItsCallBeforeItIsReported(t *testing.T) {
	s := &scripted{script: []StepResult{{Calls: []AgentTurn{call("i", "generate_image", `{"prompt":"a cat"}`)}}, Reply("")}}
	var reported []AgentTurn
	amended := `{"prompt":"a cat","image":{"id":"x"}}`
	turns, err := Loop{
		MaxSteps: 12,
		Step:     s.step,
		Execute: func(ToolCall) ToolOutput {
			return ToolOutput{Text: "Image proposed", CallArguments: &amended}
		},
		HowToRun: func(string) ToolRun { return Amending },
		OnEvent: func(e LoopEvent) {
			if e.Turn != nil {
				reported = append(reported, *e.Turn)
			}
		},
	}.Run(nil, UserTurn("go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(turns[1].Arguments, `"id":"x"`) {
		t.Errorf("recorded %s", turns[1].Arguments)
	}
	if len(reported) != 2 || reported[0].Kind != ToolCalled || !strings.Contains(reported[0].Arguments, `"id":"x"`) {
		t.Errorf("reported %+v", reported)
	}
}

func TestOpenAIStyleUsageReadsTheCachedPartOutOfTheInput(t *testing.T) {
	cases := []struct {
		usage string
		want  StepUsage
	}{
		{`{"prompt_tokens":6461,"completion_tokens":40,"prompt_tokens_details":{"cached_tokens":5944}}`, StepUsage{InputTokens: 517, CacheReadInputTokens: 5944, OutputTokens: 40}},
		{`{"input_tokens":1200,"input_tokens_details":{"cached_tokens":1024},"output_tokens":9}`, StepUsage{InputTokens: 176, CacheReadInputTokens: 1024, OutputTokens: 9}},
		{`{"prompt_tokens":100,"completion_tokens":5,"prompt_cache_hit_tokens":64}`, StepUsage{InputTokens: 36, CacheReadInputTokens: 64, OutputTokens: 5}},
		{`{"prompt_tokens":100,"completion_tokens":5,"cached_tokens":80}`, StepUsage{InputTokens: 20, CacheReadInputTokens: 80, OutputTokens: 5}},
	}
	for _, c := range cases {
		if got, ok := openAIUsage(decode(c.usage)); !ok || got != c.want {
			t.Errorf("%s: %+v", c.usage, got)
		}
	}
	if _, ok := openAIUsage(nil); ok {
		t.Error("no usage read as some")
	}
}

func TestAStepErrorAbortsTheExchange(t *testing.T) {
	_, err := Loop{
		MaxSteps: 12,
		Step:     func([]AgentTurn) (StepResult, error) { return StepResult{}, errors.New("429 rate limited") },
		Execute:  func(ToolCall) ToolOutput { return Text("") },
		HowToRun: func(string) ToolRun { return InOrder },
	}.Run(nil, UserTurn("go"))
	if err == nil || err.Error() != "429 rate limited" {
		t.Errorf("%v", err)
	}
}

func TestATurnRoundTripsAsTheFrontendReadsIt(t *testing.T) {
	turns := []AgentTurn{
		UserTurn("hi"),
		{Kind: Assistant, Text: "yes", Sources: []WebSource{{N: 1, URL: "https://a.example/", Title: "A"}}},
		{Kind: ToolCalled, CallID: "c", Name: "read_document", Arguments: "{}", Reasoning: rawOf("why")},
		{Kind: ToolResult, CallID: "c", Output: "text"},
	}
	for _, turn := range turns {
		raw, err := turn.MarshalJSON()
		if err != nil {
			t.Fatal(err)
		}
		var back AgentTurn
		if err := back.UnmarshalJSON(raw); err != nil {
			t.Fatal(err)
		}
		again, _ := back.MarshalJSON()
		if string(again) != string(raw) {
			t.Errorf("%s came back as %s", raw, again)
		}
	}
	if raw, _ := UserTurn("hi").MarshalJSON(); string(raw) != `{"kind":"User","text":"hi","images":[]}` {
		t.Errorf("%s", raw)
	}
}
