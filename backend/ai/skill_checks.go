package ai

import (
	"regexp"
	"slices"
	"strings"
)

// What a skill a reply wrote is checked for before it is saved. Its
// instructions join every later prompt in its modes, so a skill the model
// wrote is a prompt that persists: the user confirms a second time when it
// runs in Create mode (where it could write the next skill) or reads like
// an attempt to override the instructions around it.

const (
	// SkillNeedsConfirm is the error a written skill is refused with,
	// followed by `:` and the warnings joined by `,`, until confirmed.
	SkillNeedsConfirm = "SKILL_NEEDS_CONFIRM"
	// WarnCreateMode: it applies in Create mode, where it could write or
	// rewrite skills itself.
	WarnCreateMode = "create-mode"
	// WarnPromptInjection: its instructions read like an attempt to
	// override the ones around them.
	WarnPromptInjection = "prompt-injection"
	// WarnHiddenText: it holds characters a reader can't see.
	WarnHiddenText = "hidden-text"
)

// injection are phrasings of "drop the instructions you were given", in
// English and Chinese, and fake role markers, matched on lower-cased text
// with its whitespace collapsed.
var injection = func() []*regexp.Regexp {
	var res []*regexp.Regexp
	for _, p := range []string{
		`\b(?:ignore|disregard|forget|override|bypass|skip)\b(?:\s+\S+){0,3}?\s+(?:previous|prior|above|earlier|preceding|original|system|developer|all|your)\b(?:\s+\S+){0,2}?\s+(?:instructions?|prompts?|rules|directives?|guidelines|guardrails|messages?)\b`,
		`\b(?:reveal|print|show|repeat|output|leak)\b(?:\s+\S+){0,3}?\s+(?:system|developer|hidden)\s+(?:prompt|instructions?|message)`,
		`\b(?:jailbreak|developer mode|dan mode|do anything now)\b`,
		`\byou are no longer\b`,
		`\bnew (?:system )?instructions\s*:`,
		// Role markers that pretend a new message starts here.
		`<\|?(?:im_start|im_end|system|endoftext)\|?>`,
		`</?(?:system|assistant|developer)>`,
		`^\s*(?:\[|#+\s*)(?:system|developer)\b(?:\]|\s*(?:prompt|message)?\s*:)`,
		`(?:忽略|无视|忽视|忘记|忘掉|不要理会|跳过|绕过|覆盖)(?:掉)?(?:你)?(?:之前|以上|上面|前面|先前|此前|上述|原来|原有|原先|所有|全部|一切|系统)[^。\n]{0,8}?(?:指令|指示|提示词?|规则|要求|设定|约束|限制|消息)`,
		`(?:输出|显示|告诉我|泄露|打印|重复)[^。\n]{0,6}?(?:系统提示词?|系统指令|系统消息)`,
		`(?:系统提示词?|系统指令|系统消息)[^。\n]{0,6}?(?:输出|显示|告诉|泄露|打印|重复|发给)`,
		`(?:越狱|开发者模式)`,
	} {
		res = append(res, regexp.MustCompile("(?m)"+p))
	}
	return res
}()

// isHiddenChar: zero-width spaces and joiners, direction overrides and
// isolates, soft hyphens, invisible fillers, a byte-order mark, tag
// characters and the supplementary variation selectors (an emoji's own
// are left alone) - text that reads differently than it shows.
func isHiddenChar(c rune) bool {
	switch {
	case c == 0x00AD, c == 0x034F, c >= 0x115F && c <= 0x1160, c == 0x180E,
		c >= 0x200B && c <= 0x200F, c >= 0x202A && c <= 0x202E, c >= 0x2060 && c <= 0x2064,
		c >= 0x2066 && c <= 0x2069, c == 0x3164, c == 0xFEFF, c == 0xFFA0,
		c >= 0xE0000 && c <= 0xE007F, c >= 0xE0100 && c <= 0xE01EF:
		return true
	}
	return false
}

// LooksLikeInjection is whether prompt reads like it overrides the
// instructions around it.
func LooksLikeInjection(prompt string) bool {
	var collapsed []string
	for _, l := range lines(prompt) {
		collapsed = append(collapsed, strings.Join(strings.Fields(l), " "))
	}
	text := strings.ToLower(strings.Join(collapsed, "\n"))
	return slices.ContainsFunc(injection, func(re *regexp.Regexp) bool { return re.MatchString(text) })
}

// SkillWarnings is what a user should know before saving a skill with
// prompt (its instructions, comments stripped) in modes, most serious
// first.
func SkillWarnings(prompt string, modes []AgentMode) []string {
	var warnings []string
	if LooksLikeInjection(prompt) {
		warnings = append(warnings, WarnPromptInjection)
	}
	if strings.ContainsFunc(prompt, isHiddenChar) {
		warnings = append(warnings, WarnHiddenText)
	}
	if slices.Contains(modes, ModeCreate) {
		warnings = append(warnings, WarnCreateMode)
	}
	return warnings
}
