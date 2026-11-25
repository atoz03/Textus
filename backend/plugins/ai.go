package plugins

import (
	"math"
	"strings"
	"unicode/utf8"
)

// textus.ai.complete and textus.ai.stream: a plugin asking the user's
// writing model, with the ai.model permission, the user's yes on its first
// call, and a monthly allowance of tokens kept in plugins/ai.json, where
// the plugin can't reach it.

const (
	// MonthlyTokens are a plugin's tokens a month until the user allows
	// more.
	MonthlyTokens = 100_000
	// LongestInput is the longest system text plus prompt a plugin may
	// send, in characters.
	LongestInput = 200_000
	// NotAllowed and OverLimit are refusals the frontend turns into a
	// question for the user.
	NotAllowed = "plugin-ai-not-allowed"
	OverLimit  = "plugin-ai-limit"
)

// AIUsage is plugins/ai.json.
type AIUsage struct {
	// Month is "2026-11": counts start again in a new month.
	Month   string                    `json:"month"`
	Plugins map[string]*PluginAIUsage `json:"plugins"`
}

// PluginAIUsage is one plugin's line in AIUsage.
type PluginAIUsage struct {
	// Allowed: the user said yes to its first call.
	Allowed bool `json:"allowed"`
	// Used are its tokens this month, as Tokens counts them.
	Used uint64 `json:"used"`
	// Extra is what the user allowed beyond the allowance, this month.
	Extra uint64 `json:"extra"`
}

// AIStatus is whether Textus AI is on, and a plugin's yes and tokens this
// month (PluginAiStatus in src/platform/bindings/plugins.ts).
type AIStatus struct {
	On      bool   `json:"on"`
	Allowed bool   `json:"allowed"`
	Used    uint32 `json:"used"`
	Limit   uint32 `json:"limit"`
}

func isWide(c rune) bool {
	return c >= 0x3040 && c <= 0x30ff || c >= 0x3400 && c <= 0x4dbf || c >= 0x4e00 && c <= 0x9fff ||
		c >= 0xac00 && c <= 0xd7af || c >= 0xf900 && c <= 0xfaff
}

// Tokens is about how many tokens a text is: a CJK character is one,
// other text one for every four characters.
func Tokens(text string) uint64 {
	var wide, other uint64
	for _, c := range text {
		if isWide(c) {
			wide++
		} else {
			other++
		}
	}
	return wide + (other+3)/4
}

// Fresh is usage as it stands in month: last month's counts and extras
// gone, the yeses kept.
func (u AIUsage) Fresh(month string) AIUsage {
	if u.Plugins == nil {
		u.Plugins = map[string]*PluginAIUsage{}
	}
	if u.Month != month {
		u.Month = month
		for _, e := range u.Plugins {
			e.Used, e.Extra = 0, 0
		}
	}
	return u
}

// Entry is plugin's line, made when it has none.
func (u *AIUsage) Entry(plugin string) *PluginAIUsage {
	if u.Plugins == nil {
		u.Plugins = map[string]*PluginAIUsage{}
	}
	e := u.Plugins[plugin]
	if e == nil {
		e = &PluginAIUsage{}
		u.Plugins[plugin] = e
	}
	return e
}

func clampU32(n uint64) uint32 {
	if n > math.MaxUint32 {
		return math.MaxUint32
	}
	return uint32(n)
}

// StatusOf is a plugin's status from its line (nil for none).
func StatusOf(e *PluginAIUsage, on bool) AIStatus {
	if e == nil {
		e = &PluginAIUsage{}
	}
	return AIStatus{On: on, Allowed: e.Allowed, Used: clampU32(e.Used), Limit: clampU32(MonthlyTokens + e.Extra)}
}

// Refusal is the refusal for a call now, "" to go ahead.
func Refusal(e *PluginAIUsage) string {
	switch {
	case e == nil || !e.Allowed:
		return NotAllowed
	case e.Used >= MonthlyTokens+e.Extra:
		return OverLimit
	}
	return ""
}

// TooLong is whether system and prompt together are over LongestInput
// characters.
func TooLong(system, prompt string) bool {
	return utf8.RuneCountInString(system)+utf8.RuneCountInString(prompt) > LongestInput
}

// Spent is what a call cost: what was sent and what came back.
func Spent(sent []string, answer string) uint64 {
	var total uint64
	for _, s := range sent {
		total += Tokens(s)
	}
	return total + Tokens(answer)
}

// Counted is what a call spent of its answer: the answer, or what was
// written of it when it was cancelled; false when it failed.
func Counted(answer string, err error, soFar string) (string, bool) {
	switch {
	case err == nil:
		return answer, true
	case strings.TrimSpace(err.Error()) == Cancelled:
		return soFar, true
	}
	return "", false
}
