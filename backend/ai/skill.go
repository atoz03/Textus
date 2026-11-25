package ai

import (
	"maps"
	"slices"
	"strings"
)

// Skills as the app sees them: a skill file parsed into its name,
// description, body and the Textus-only `metadata:` keys.

// MetaPrefix is the prefix of the app's own keys under a skill's
// `metadata:`.
const MetaPrefix = "textus"

// Invocation is who may start a skill (`metadata.textus-invocation`):
// "both" the model and /name, "userOnly" /name only, "hidden" neither -
// loaded by exact name with use_skill (the reference packs).
type Invocation string

const (
	InvokeBoth     Invocation = "both"
	InvokeUserOnly Invocation = "userOnly"
	InvokeHidden   Invocation = "hidden"
)

// SkillSource is which layer a skill comes from: built in, the global
// config folder, or the project. Each layer replaces same-named skills of
// the ones before it.
type SkillSource string

const (
	SourceBuiltin SkillSource = "builtin"
	SourceGlobal  SkillSource = "global"
	SourceProject SkillSource = "project"
)

// SkillLabel is a skill's name and one-line hint in one UI language.
type SkillLabel struct {
	Label string `json:"label"`
	Hint  string `json:"hint"`
}

// AgentSkill is one skill (src/platform/bindings/ai-skills.ts AgentSkill).
type AgentSkill struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Prompt      string `json:"prompt"`
	// Category is `textus-category`: write, revise, check, format or
	// create (the `/` menu's groups).
	Category *string `json:"category"`
	// Modes are `textus-modes`: the panel modes the skill applies in.
	Modes []AgentMode `json:"modes"`
	// Off is `textus-off`: turned off in Settings for now, its modes kept.
	Off              bool        `json:"off"`
	Invocation       Invocation  `json:"invocation"`
	Source           SkillSource `json:"source"`
	OverridesBuiltin bool        `json:"overridesBuiltin"`
	// Labels are what people see it called, and its hint, per UI language
	// (`textus-label-<code>`, `textus-hint-<code>`).
	Labels map[string]SkillLabel `json:"labels"`
	// Aliases are extra words to type after `/`, per UI language
	// (`textus-aliases-<code>`, comma-separated).
	Aliases map[string][]string `json:"aliases"`
	// Packs are `textus-packs`: the reference-pack sections appended to the
	// body when the skill is used ("craft#fiction", "standards#dialogue").
	Packs []string `json:"packs"`
	// Version is a built-in's `textus-version`.
	Version *string `json:"version"`
	// BasedOn is `textus-based-on: "<name>@<version>"`, written into a
	// Customize… copy; what allows Reset to delete it.
	BasedOn *string `json:"basedOn"`
	// BuiltinUpdated: a customized copy whose built-in has a newer version.
	BuiltinUpdated bool `json:"builtinUpdated"`
	// TargetPacks are `textus-target-packs`: sections of the target
	// language's pack to attach when the request names one (translate).
	TargetPacks []string `json:"-"`
	// IndexLine is `textus-index`: what the model's skill index says of a
	// built-in skill in place of its description.
	IndexLine *string `json:"-"`
}

// NewSkill is a skill with the defaults a file that says nothing gets.
func NewSkill(name string) AgentSkill {
	return AgentSkill{
		Name: name, Modes: []AgentMode{ModeWrite}, Invocation: InvokeBoth, Source: SourceGlobal,
		Labels: map[string]SkillLabel{}, Aliases: map[string][]string{}, Packs: []string{},
	}
}

// OfferedToModel is whether the model is told about the skill in the
// skill index and may load it by description.
func (s *AgentSkill) OfferedToModel() bool { return s.Invocation == InvokeBoth }

// Clone is a copy sharing nothing with s.
func (s AgentSkill) Clone() AgentSkill {
	s.Modes = slices.Clone(s.Modes)
	s.Labels = maps.Clone(s.Labels)
	s.Aliases = maps.Clone(s.Aliases)
	s.Packs = slices.Clone(s.Packs)
	s.TargetPacks = slices.Clone(s.TargetPacks)
	return s
}

// ParseSkillFile reads a skill file; fileStem names it when its front
// matter doesn't.
func ParseSkillFile(fileStem, content string) AgentSkill {
	fm, body := SplitFrontmatter(content)
	skill := NewSkill(fileStem)
	skill.Prompt = strings.TrimSpace(StripHTMLComments(body))
	fields := fm.fields()
	meta := map[string]string{}
	if fm != nil {
		meta = fm.Metadata
	}
	if name := strings.TrimSpace(fields["name"]); name != "" {
		skill.Name = strings.Join(strings.Fields(name), "-")
	}
	skill.Description = strings.TrimSpace(fields["description"])
	if strings.TrimSpace(fields["disable-model-invocation"]) == "true" {
		skill.Invocation = InvokeUserOnly
	}
	get := func(key string) (string, bool) {
		v := strings.TrimSpace(meta[MetaPrefix+"-"+key])
		return v, v != ""
	}
	if inv, ok := get("invocation"); ok {
		switch strings.ToLower(inv) {
		case "user-only", "user_only", "useronly", "user":
			skill.Invocation = InvokeUserOnly
		case "hidden":
			skill.Invocation = InvokeHidden
		default:
			skill.Invocation = InvokeBoth
		}
	}
	if c, ok := get("category"); ok {
		skill.Category = &c
	}
	for key, value := range meta {
		lang, ok := strings.CutPrefix(key, MetaPrefix+"-label-")
		if label := strings.TrimSpace(value); ok && label != "" {
			hint, _ := get("hint-" + lang)
			skill.Labels[lang] = SkillLabel{Label: label, Hint: hint}
		}
	}
	if modes, ok := get("modes"); ok {
		var list []AgentMode
		for _, w := range strings.FieldsFunc(modes, func(r rune) bool { return r == ' ' || r == ',' }) {
			if m, ok := ModeFromWord(w); ok {
				list = append(list, m)
			}
		}
		slices.Sort(list)
		if list = slices.Compact(list); len(list) > 0 {
			skill.Modes = list
		}
	}
	off, _ := get("off")
	skill.Off = off == "true"
	if v, ok := get("version"); ok {
		skill.Version = &v
	}
	if v, ok := get("based-on"); ok {
		skill.BasedOn = &v
	}
	if v, ok := get("index"); ok {
		skill.IndexLine = &v
	}
	packs, _ := get("packs")
	skill.Packs = append([]string{}, strings.Fields(packs)...)
	targets, _ := get("target-packs")
	skill.TargetPacks = strings.Fields(targets)
	for key, value := range meta {
		lang, ok := strings.CutPrefix(key, MetaPrefix+"-aliases-")
		if !ok {
			continue
		}
		var list []string
		for _, alias := range strings.FieldsFunc(value, func(r rune) bool { return r == ',' || r == '，' }) {
			alias = strings.TrimSpace(alias)
			if alias != "" && !slices.ContainsFunc(list, func(a string) bool { return strings.ToLower(a) == strings.ToLower(alias) }) {
				list = append(list, alias)
			}
		}
		if len(list) > 0 {
			skill.Aliases[lang] = list
		}
	}
	return skill
}
