package ai

import (
	"fmt"
	"strings"
	"unicode"

	"textus/backend/ai/rules"
)

// A `/name` in the user's message expanded: the skill found, its
// references rendered for the document and the profile in use.

// ExpandSkill is a skill's instructions as the model gets them: the body,
// then the pack sections it declares for the document's language (and a
// translation's target). profile filters the variants; "" keeps them all.
func ExpandSkill(skill *AgentSkill, document, uiLanguage, request, profile string) string {
	if len(skill.Packs) == 0 && len(skill.TargetPacks) == 0 {
		return skill.Prompt
	}
	language := DocumentLanguage(document, uiLanguage)
	var refs []packRef
	add := func(found []packRef) {
		for _, r := range found {
			if !containsRef(refs, r.label) {
				refs = append(refs, r)
			}
		}
	}
	for _, reference := range skill.Packs {
		add(resolvePack(reference, language.Code))
	}
	target := ""
	if len(skill.TargetPacks) > 0 {
		target = TargetLanguage(request)
	}
	if target != "" {
		for _, section := range skill.TargetPacks {
			add(resolvePack("standards-"+target+"#"+section, ""))
		}
	}
	if len(refs) == 0 {
		return skill.Prompt
	}
	return fmt.Sprintf("%s\n\n---references for this skill---\n%s", skill.Prompt, renderReferences(refs, language, profile, target))
}

func containsRef(refs []packRef, label string) bool {
	for _, r := range refs {
		if r.label == label {
			return true
		}
	}
	return false
}

// FindInvocable is the skill the user can call by token (what follows
// `/`): its name, or one of its aliases in any language. Hidden ones
// can't be called.
func FindInvocable(skills []AgentSkill, token string) (*AgentSkill, bool) {
	for i := range skills {
		if skills[i].Invocation != InvokeHidden && skills[i].Name == token {
			return &skills[i], true
		}
	}
	token = strings.ToLower(token)
	for i := range skills {
		if skills[i].Invocation == InvokeHidden {
			continue
		}
		for _, aliases := range skills[i].Aliases {
			for _, a := range aliases {
				if strings.ToLower(a) == token {
					return &skills[i], true
				}
			}
		}
	}
	return nil, false
}

// SkillBlock is the `<skill name="…">` block a `/name` call becomes in
// the user's message.
func SkillBlock(name, instructions string) string {
	return fmt.Sprintf("<skill name=\"%s\">\n%s\n</skill>", name, instructions)
}

// ChosenProfile is the writing-conventions profile the user chose for the
// document's language; "" under Automatic, where every variant stays.
func ChosenProfile(conv *rules.Conventions, document string) string {
	lang, ok := rules.Detect(document)
	if !ok {
		return ""
	}
	if profile := conv.ProfileFor(lang); profile.Explicit {
		return profile.ID
	}
	return ""
}

// ExpandMessage fills in the skill a message calls: a `<skill name="…">`
// block or a message starting with `/name` gets the expanded instructions,
// so `/name` needs no extra round trip; packs filtered by profile.
// Anything else comes back unchanged.
func ExpandMessage(message string, skills []AgentSkill, document, uiLanguage, profile string) string {
	const open = `<skill name="`
	// The last block: the composer's own comes after attached files and
	// the selection, so text inside those never expands.
	if start := strings.LastIndex(message, open); start >= 0 {
		nameStart := start + len(open)
		nameLen := strings.IndexByte(message[nameStart:], '"')
		if nameLen < 0 {
			return message
		}
		name := message[nameStart : nameStart+nameLen]
		gt := strings.IndexByte(message[nameStart:], '>')
		if gt < 0 {
			return message
		}
		openEnd := nameStart + gt + 1
		closeAt := strings.Index(message[openEnd:], "</skill>")
		if closeAt < 0 {
			return message
		}
		closeAt += openEnd
		skill, ok := FindInvocable(skills, name)
		if !ok {
			return message
		}
		rest := message[closeAt+len("</skill>"):]
		expanded := ExpandSkill(skill, document, uiLanguage, strings.TrimSpace(rest), profile)
		// Other attributes (`typed="/续写"`: what the user typed, which the
		// transcript shows) stay as they were.
		attrs := ""
		if from, to := nameStart+nameLen+1, openEnd-1; from < to {
			attrs = message[from:to]
		}
		return fmt.Sprintf("%s<skill name=\"%s\"%s>\n%s\n</skill>%s", message[:start], skill.Name, attrs, expanded, rest)
	}
	rest, ok := strings.CutPrefix(message, "/")
	if !ok {
		if rest, ok = strings.CutPrefix(message, "／"); !ok {
			return message
		}
	}
	token, tail := rest, ""
	if at := strings.IndexFunc(rest, unicode.IsSpace); at >= 0 {
		token = rest[:at]
		tail = strings.TrimLeftFunc(rest[at:], unicode.IsSpace)
	}
	skill, ok := FindInvocable(skills, token)
	if !ok {
		return message
	}
	block := SkillBlock(skill.Name, ExpandSkill(skill, document, uiLanguage, tail, profile))
	if tail == "" {
		return block
	}
	return block + "\n\n" + tail
}
