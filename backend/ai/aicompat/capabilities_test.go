package aicompat

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

func TestTheGeneratedTableKnowsThePinnedModels(t *testing.T) {
	haiku := ReasoningOf("anthropic", "claude-haiku-5-5")
	if haiku.Kind != ReasoningOptions || !haiku.Toggle || haiku.Budget != nil ||
		!slices.Equal(haiku.Efforts, []string{"low", "medium", "high", "xhigh", "max"}) {
		t.Errorf("haiku: %+v", haiku)
	}
	if r := ReasoningOf("mistral", "codestral-latest"); r.Kind != ReasoningNo {
		t.Errorf("codestral: %+v", r)
	}
	if r := ReasoningOf("custom", "my-model"); r.Kind != ReasoningUnknown {
		t.Errorf("a custom model: %+v", r)
	}
}

func TestDeepThinkingIsOfferedOnlyWhereItChangesSomething(t *testing.T) {
	for _, m := range [][2]string{{"anthropic", "claude-sonnet-5-5"}, {"deepseek", "deepseek-v4-pro"}, {"zhipu", "glm-4.7-flash"}, {"ollama", "qwen3:8b"}} {
		if !CanThinkDeeper(m[0], m[1]) {
			t.Errorf("%v isn't offered", m)
		}
	}
	for _, m := range [][2]string{{"mistral", "mistral-large-latest"}, {"openai", "gpt-4o"}} {
		if CanThinkDeeper(m[0], m[1]) {
			t.Errorf("%v is offered", m)
		}
	}
}

func TestStreamedUsageIsAskedForOnlyWhereTheServerTakesIt(t *testing.T) {
	if !Provider("deepseek").StreamUsage || !Provider("doubao").StreamUsage {
		t.Error("deepseek and doubao take it")
	}
	if Provider("mistral").StreamUsage || Provider("custom").StreamUsage || Provider("new-entry").StreamUsage {
		t.Error("asked of a server that may refuse it")
	}
}

func TestAutoSendsNothing(t *testing.T) {
	for _, m := range [][2]string{{"openai", "gpt-6.1-sol"}, {"anthropic", "claude-sonnet-5-5"}, {"deepseek", "deepseek-v4-pro"}, {"qwen", "qwen3.7-plus"}} {
		if Plan(m[0], m[1], Auto).SendsParameters() {
			t.Errorf("%v sends parameters", m)
		}
	}
}

func TestAutoTurnsArkDownToMinimalWhereTheModelTakesIt(t *testing.T) {
	if e := Plan("doubao", "doubao-seed-2-1-pro-260628", Auto).Effort; e != "minimal" {
		t.Errorf("auto: %q", e)
	}
	if e := Plan("doubao", "doubao-seed-2-1-pro-260628", Deep).Effort; e != "high" {
		t.Errorf("deep: %q", e)
	}
	if Plan("doubao", "doubao-unknown", Auto).SendsParameters() {
		t.Error("a model the table doesn't know was sent an effort")
	}
}

func TestDeepAsksForHighNeverXhighOrMax(t *testing.T) {
	if e := Plan("openai", "gpt-6.1-sol", Deep).Effort; e != "high" {
		t.Errorf("%q", e)
	}
	if e := deepEffort([]string{"low", "medium", "xhigh"}); e != "medium" {
		t.Errorf("%q", e)
	}
	if e := deepEffort([]string{"low", "high", "max"}); e != "high" {
		t.Errorf("%q", e)
	}
}

func TestFastTurnsThinkingOffOrDownAsFarAsTheModelAllows(t *testing.T) {
	off := func(th Thinking) bool { return th.On != nil && !*th.On }
	if !off(Plan("openai", "gpt-6-luna", Fast)) {
		t.Error("luna stays on")
	}
	if e := Plan("openai", "gpt-6.1-sol", Fast).Effort; e != "low" {
		t.Errorf("sol: %q", e)
	}
	if !off(Plan("anthropic", "claude-haiku-5-5", Fast)) {
		t.Error("haiku stays on")
	}
	if e := Plan("anthropic", "claude-sonnet-5-5", Fast).Effort; e != "low" {
		t.Errorf("sonnet: %q", e)
	}
	if Plan("custom", "local", Fast).SendsParameters() {
		t.Error("a model the table doesn't know lost its default")
	}
}

func chatThinking(th Thinking) string {
	body := map[string]any{"model": "m"}
	th.applyChatCompletions(body)
	return compact(body)
}

func TestEachChatCompletionsFormatHasItsOwnFields(t *testing.T) {
	cases := []struct {
		provider, model string
		level           ThinkingLevel
		want            string
	}{
		{"groq", "openai/gpt-oss-120b", Deep, `{"model":"m","reasoning_effort":"high"}`},
		{"deepseek", "deepseek-v4-pro", Deep, `{"model":"m","reasoning_effort":"high","thinking":{"type":"enabled"}}`},
		{"deepseek", "deepseek-v4-pro", Fast, `{"model":"m","thinking":{"type":"disabled"}}`},
		{"qwen", "qwen3.7-plus", Deep, `{"enable_thinking":true,"model":"m","thinking_budget":16384}`},
		{"qwen", "qwen3.8-flash", Fast, `{"enable_thinking":false,"model":"m"}`},
		{"openrouter", "openrouter/auto", Deep, `{"model":"m","reasoning":{"effort":"high"}}`},
		{"google", "gemini-3.8-flash", Fast, `{"extra_body":{"google":{"thinking_config":{"include_thoughts":true,"thinking_level":"low"}}},"model":"m"}`},
		{"mistral", "mistral-large-latest", Deep, `{"model":"m"}`},
	}
	for _, c := range cases {
		if got := chatThinking(Plan(c.provider, c.model, c.level)); got != c.want {
			t.Errorf("%s %s %s:\n got %s\nwant %s", c.provider, c.model, c.level, got, c.want)
		}
	}
	if got := chatThinking(Plan("zhipu", "glm-4.7-flash", Deep)); !strings.Contains(got, `"thinking":{"type":"enabled"}`) {
		t.Errorf("glm: %s", got)
	}
	if got := chatThinking(Plan("moonshot", "kimi-k3", Deep)); !strings.Contains(got, `"reasoning_effort":"high"`) {
		t.Errorf("kimi: %s", got)
	}
}

func TestResponsesRequestsAskForASummaryWhenDeep(t *testing.T) {
	deep := map[string]any{}
	Plan("openai", "gpt-6.1-sol", Deep).applyResponses(deep)
	if got := compact(deep["reasoning"]); got != `{"effort":"high","summary":"auto"}` {
		t.Errorf("deep: %s", got)
	}
	fast := map[string]any{}
	Plan("openai", "gpt-6-luna", Fast).applyResponses(fast)
	if got := compact(fast["reasoning"]); got != `{"effort":"none"}` {
		t.Errorf("fast: %s", got)
	}
}

func TestClaudeGetsAdaptiveThinkingAndAnEffort(t *testing.T) {
	deep := map[string]any{"max_tokens": 16384}
	Plan("anthropic", "claude-sonnet-5-5", Deep).applyAnthropic(deep)
	if got := compact(deep); got != `{"max_tokens":16384,"output_config":{"effort":"high"},"thinking":{"display":"summarized","type":"adaptive"}}` {
		t.Errorf("deep: %s", got)
	}
	fast := map[string]any{"max_tokens": 16384}
	Plan("anthropic", "claude-haiku-5-5", Fast).applyAnthropic(fast)
	if got := compact(fast); got != `{"max_tokens":16384,"thinking":{"type":"disabled"}}` {
		t.Errorf("fast: %s", got)
	}
	// A budget model: max_tokens grows by the budget it must exceed.
	old := map[string]any{"max_tokens": 16384}
	Thinking{Format: AnthropicThinking, Budget: DeepBudget, Show: true}.applyAnthropic(old)
	if got := compact(old); got != `{"max_tokens":32768,"thinking":{"budget_tokens":16384,"type":"enabled"}}` {
		t.Errorf("budget: %s", got)
	}
	auto := map[string]any{"max_tokens": 16384}
	Plan("anthropic", "claude-sonnet-5-5", Auto).applyAnthropic(auto)
	if got := compact(auto); got != `{"max_tokens":16384}` {
		t.Errorf("auto: %s", got)
	}
}

func TestARefusedParameterIsRetriedWithoutAndNotSentAgain(t *testing.T) {
	thinking := Plan("custom", "refuses-effort", Deep)
	if !thinking.SendsParameters() {
		t.Fatal("deep sends nothing to a model the table doesn't know")
	}
	var sent []bool
	answer, err := withFallback("custom", "refuses-effort", thinking, func(th Thinking) (string, error) {
		sent = append(sent, th.SendsParameters())
		if th.SendsParameters() {
			return "", errors.New(`Custom endpoint request failed (400 Bad Request): {"error":"Unrecognized request argument supplied: reasoning_effort"}`)
		}
		return "answer", nil
	})
	if err != nil || answer != "answer" || !slices.Equal(sent, []bool{true, false}) {
		t.Errorf("%q %v %v", answer, err, sent)
	}
	if Plan("custom", "refuses-effort", Deep).SendsParameters() {
		t.Error("sent again after the refusal")
	}
}

func TestQwenSearchEndpointTracksModelGeneration(t *testing.T) {
	for _, model := range []string{"qwen3.7-plus", "qwen3.5-flash", "qwen3-max"} {
		if r := SearchRoute("qwen", model); r.Kind != CompatibleResponsesSearch {
			t.Errorf("%s: %+v", model, r)
		}
	}
	for _, model := range []string{"qwen-plus", "qwen-flash"} {
		if r := SearchRoute("qwen", model); r != (NativeSearch{Kind: ChatSearch, Chat: QwenSearch}) {
			t.Errorf("%s: %+v", model, r)
		}
	}
	if r := SearchRoute("xai", "grok-4.7"); r.Kind != CompatibleResponsesSearch {
		t.Errorf("xai: %+v", r)
	}
	if r := SearchRoute("deepseek", "deepseek-v4-pro"); r.Kind != NoSearch {
		t.Errorf("deepseek: %+v", r)
	}
}

func TestOtherErrorsAreNotMistakenForARefusal(t *testing.T) {
	for text, want := range map[string]bool{
		"Claude request failed (429 Too Many Requests): rate limited":                       false,
		"Claude request failed (400 Bad Request): prompt is too long":                       false,
		"Claude request failed (400 Bad Request): thinking.type: disabled is not supported": true,
	} {
		if IsThinkingRefusal(errors.New(text)) != want {
			t.Errorf("%s: %v", text, !want)
		}
	}
}
