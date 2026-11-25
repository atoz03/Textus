package aicompat

import (
	"slices"
	"strings"
	"sync"
)

// The thinking a request asks for: a level turned into the effort, budget
// or switch the provider and model take, and a model that refuses them
// remembered and asked again without.

// DeepBudget is the thinking-token budget Deep asks for where a model takes
// a budget rather than levels.
const DeepBudget = 16_384

// Thinking is one request's thinking parameters, dialect-neutral; each
// dialect writes the parts it has a field for. Empty for Auto: nothing is
// sent.
type Thinking struct {
	Format ThinkingFormat
	// On switches thinking on or off, where the model can; nil leaves it.
	On     *bool
	Effort string
	Budget uint32
	// Show asks for readable reasoning (OpenAI's summary, Claude's
	// summarized display): what the "Thinking" row shows.
	Show bool
	// Replay: see ProviderCaps.ReplayReasoning.
	Replay bool
}

func on(v bool) *bool { return &v }

// switchedOff: thinking asked off.
func (t Thinking) switchedOff() bool { return t.On != nil && !*t.On }

// SendsParameters reports whether any parameter would be sent.
func (t Thinking) SendsParameters() bool {
	return t.On != nil || t.Effort != "" || t.Budget != 0 || t.Show
}

// WithoutParameters is the same request with no thinking parameters: what
// a provider that refused them gets.
func (t Thinking) WithoutParameters() Thinking {
	return Thinking{Format: t.Format, Replay: t.Replay}
}

// wireEffort is the effort as an OpenAI-format field: switching off is
// `none`.
func (t Thinking) wireEffort() string {
	if t.switchedOff() {
		return "none"
	}
	return t.Effort
}

// applyChatCompletions writes the format's fields on the top level of body.
func (t Thinking) applyChatCompletions(body map[string]any) {
	switch t.Format {
	case OpenAIThinking:
		if effort := t.wireEffort(); effort != "" {
			body["reasoning_effort"] = effort
		}
	case ThinkingType:
		if t.On != nil {
			kind := "disabled"
			if *t.On {
				kind = "enabled"
			}
			body["thinking"] = map[string]any{"type": kind}
		}
		if !t.switchedOff() && t.Effort != "" {
			body["reasoning_effort"] = t.Effort
		}
	case GeminiThinking:
		config := map[string]any{}
		switch {
		case t.switchedOff():
			config["thinking_budget"] = 0
		case t.Effort != "":
			config["thinking_level"] = t.Effort
		case t.Budget != 0:
			config["thinking_budget"] = t.Budget
		}
		if t.Show && !t.switchedOff() {
			config["include_thoughts"] = true
		}
		if len(config) > 0 {
			body["extra_body"] = map[string]any{"google": map[string]any{"thinking_config": config}}
		}
	case QwenThinking:
		t.applyQwen(body)
	case OpenRouterThinking:
		switch {
		case t.switchedOff():
			body["reasoning"] = map[string]any{"enabled": false}
		case t.Effort != "":
			body["reasoning"] = map[string]any{"effort": t.Effort}
		case t.On != nil:
			body["reasoning"] = map[string]any{"enabled": true}
		}
	}
}

// applyResponses writes `reasoning.effort`, with `reasoning.summary` when
// the reasoning should be readable. Qwen keeps its own switch there too.
func (t Thinking) applyResponses(body map[string]any) {
	if t.Format == QwenThinking {
		t.applyQwen(body)
		return
	}
	if t.Format != OpenAIThinking {
		return
	}
	reasoning := map[string]any{}
	if effort := t.wireEffort(); effort != "" {
		reasoning["effort"] = effort
	}
	if t.Show {
		reasoning["summary"] = "auto"
	}
	if len(reasoning) > 0 {
		body["reasoning"] = reasoning
	}
}

// applyAnthropic writes `thinking` and `output_config.effort`, with room
// in `max_tokens` for what the thinking itself uses.
func (t Thinking) applyAnthropic(body map[string]any) {
	if t.Format != AnthropicThinking {
		return
	}
	if t.switchedOff() {
		body["thinking"] = map[string]any{"type": "disabled"}
		return
	}
	switch {
	case t.Budget != 0:
		// Older models: a fixed budget, which max_tokens must exceed.
		body["thinking"] = map[string]any{"type": "enabled", "budget_tokens": t.Budget}
		max, _ := body["max_tokens"].(int)
		body["max_tokens"] = max + int(t.Budget)
	case t.Show || t.Effort != "":
		adaptive := map[string]any{"type": "adaptive"}
		if t.Show {
			adaptive["display"] = "summarized"
		}
		body["thinking"] = adaptive
	}
	if t.Effort != "" {
		body["output_config"] = map[string]any{"effort": t.Effort}
	}
}

func (t Thinking) applyQwen(body map[string]any) {
	if t.On != nil {
		body["enable_thinking"] = *t.On
	}
	if !t.switchedOff() && t.Budget != 0 {
		body["thinking_budget"] = t.Budget
	}
}

// thoughtsInText reports whether a whole reply's text opens with its
// thoughts, to split out (Gemini, asked to show them).
func (t Thinking) thoughtsInText() bool {
	return t.Format == GeminiThinking && t.Show && !t.switchedOff()
}

// deepEffort is the effort Deep asks for: `high`, or the strongest level
// below `xhigh` the model has.
func deepEffort(efforts []string) string {
	if slices.Contains(efforts, "high") {
		return "high"
	}
	for i := len(efforts) - 1; i >= 0; i-- {
		if e := efforts[i]; e != "none" && e != "xhigh" && e != "max" {
			return e
		}
	}
	return ""
}

// Plan is what level sends to model on provider. Nothing for Auto, for a
// format that takes nothing, and for a model that refused thinking
// parameters earlier this run.
func Plan(provider, model string, level ThinkingLevel) Thinking {
	caps := Provider(provider)
	// Gemini thinks on every level but says nothing of it unless asked: its
	// thoughts are what the row shows while the user waits.
	base := Thinking{Format: caps.Thinking, Replay: caps.ReplayReasoning, Show: caps.Thinking == GeminiThinking}
	if caps.Thinking == NoThinking || refused(provider, model) {
		return base
	}
	r := ReasoningOf(provider, model)
	if level != Fast && level != Deep {
		if caps.AutoEffort != "" && r.Kind == ReasoningOptions && slices.Contains(r.Efforts, caps.AutoEffort) {
			base.Effort = caps.AutoEffort
		}
		return base
	}
	switch r.Kind {
	case ReasoningNo, ReasoningFixed:
		return base
	case ReasoningUnknown:
		// Fast leaves the provider's default alone; Deep asks the way the
		// provider's format usually takes it.
		switch {
		case level == Fast:
		case caps.Thinking == ThinkingType || caps.Thinking == QwenThinking:
			base.On = on(true)
		default:
			base.Effort, base.Show = "high", true
		}
		return base
	}
	if level == Fast {
		// Off where the model can be switched off, by the switch or as the
		// effort `none`.
		if r.Toggle || slices.Contains(r.Efforts, "none") {
			base.On = on(false)
		} else if len(r.Efforts) > 0 {
			base.Effort = r.Efforts[0]
		}
		return base
	}
	base.Effort, base.Show = deepEffort(r.Efforts), true
	if r.Toggle {
		base.On = on(true)
	}
	// A budget only where there is no level to ask for (or the format has
	// no field for levels): Qwen, and pre-adaptive Claude.
	if r.Budget != nil && (base.Effort == "" || caps.Thinking == QwenThinking) {
		base.Budget = uint32(min(uint64(DeepBudget), *r.Budget))
	}
	return base
}

var refusals = struct {
	sync.Mutex
	models map[string]bool
}{models: map[string]bool{}}

func refused(provider, model string) bool {
	refusals.Lock()
	defer refusals.Unlock()
	return refusals.models[provider+"/"+model]
}

func rememberRefusal(provider, model string) {
	refusals.Lock()
	defer refusals.Unlock()
	refusals.models[provider+"/"+model] = true
}

// IsThinkingRefusal reports whether a failed request's error reads as the
// provider refusing a thinking parameter: a 400 (or 422) whose body names
// one. Errors read "... request failed ({status}): {body}".
func IsThinkingRefusal(err error) bool {
	if err == nil {
		return false
	}
	lower := strings.ToLower(err.Error())
	if !strings.Contains(lower, "(400") && !strings.Contains(lower, "(422") {
		return false
	}
	for _, field := range []string{"reasoning", "thinking", "effort", "output_config", "budget", "summary"} {
		if strings.Contains(lower, field) {
			return true
		}
	}
	return false
}

// withFallback runs step with thinking; when the provider refuses the
// parameters, runs it once more without them and stops sending them to
// this model for the rest of the run.
func withFallback[T any](provider, model string, thinking Thinking, step func(Thinking) (T, error)) (T, error) {
	result, err := step(thinking)
	if err != nil && thinking.SendsParameters() && IsThinkingRefusal(err) {
		rememberRefusal(provider, model)
		return step(thinking.WithoutParameters())
	}
	return result, err
}
