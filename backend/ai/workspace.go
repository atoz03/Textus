package ai

import (
	"strconv"
	"strings"

	"textus/backend/ai/rules"
)

// RootSource is why the workspace root (the project folder, to the user)
// is the one it is, or why there is none.
type RootSource string

const (
	// RootNone: no folder opened or chosen, and none above the document
	// with `.textus/`.
	RootNone RootSource = "none"
	// RootEnclosingProject: the nearest folder above the document that has
	// a `.textus/` folder.
	RootEnclosingProject RootSource = "enclosingProject"
	// RootChosen: the folder the user chose (the project menu, or Settings
	// > Textus AI).
	RootChosen RootSource = "chosen"
	// RootTooBroad: the folder would be the home folder or a drive root,
	// never opened to the agent.
	RootTooBroad RootSource = "tooBroad"
)

// AgentWorkspace is what a request knows of where it works: the writing
// instructions and skills of each layer, the project folder and its style
// profile, library and memories (src/platform/bindings/ai-project.ts
// AgentWorkspace; loaded by backend/ai_workspace.go).
type AgentWorkspace struct {
	// Instructions are the global writing instructions (the global config
	// folder's agent.md, comments stripped), in prompt order.
	Instructions []string `json:"instructions"`
	// ProjectInstructions are the project's (`.textus/agent.md`), sent
	// after the global ones and winning where the two conflict.
	ProjectInstructions *string `json:"project_instructions"`
	// Skills are the built-ins (turned-off ones too, so Settings can list
	// them), then the global folder's, then the project's, each replacing
	// same-named ones before it.
	Skills []AgentSkill `json:"skills"`
	// Root is the project folder, the sandbox root for the agent's file
	// tools; nil for an unsaved draft and a folder too broad to expose.
	Root *string `json:"root"`
	// Style is `.textus/style.md`, the author's style profile.
	Style *string `json:"style"`
	// AgentsMD is the folder's AGENTS.md: its instructions for AI agents.
	AgentsMD *string `json:"-"`
	// Nearby are the text files beside the document and in a templates
	// folder.
	Nearby []string `json:"-"`
	// Library is `.textus/library/*.md`: the project's people, places and
	// terms.
	Library    []LibraryEntry `json:"library"`
	RootSource RootSource     `json:"root_source"`
	// Conventions are what the style profile's front matter sets.
	Conventions rules.ProjectConventions `json:"conventions"`
	// Memories are what Textus AI remembered by itself, with which of them
	// fit in the prompt.
	Memories Memories `json:"memories"`
	// Remember is whether Textus AI may remember things by itself.
	Remember bool `json:"remember"`
	// Attached is whether this request's conversation has files attached.
	Attached bool `json:"-"`
}

// HasUsableSkills is whether any skill can be used at all.
func (w *AgentWorkspace) HasUsableSkills() bool { return len(w.Skills) > 0 }

// ListedSkills are the skills the `/` menu and Settings list: hidden ones
// left out.
func ListedSkills(skills []AgentSkill) []AgentSkill {
	out := []AgentSkill{}
	for _, s := range skills {
		if s.Invocation != InvokeHidden {
			out = append(out, s)
		}
	}
	return out
}

// PushLayerSkill adds a layer's skill on top of the ones before it,
// replacing any of the same name.
func PushLayerSkill(skills []AgentSkill, skill AgentSkill) []AgentSkill {
	out := skills[:0]
	for _, s := range skills {
		if s.Name != skill.Name {
			out = append(out, s)
		}
	}
	return append(out, skill)
}

// MarkOverride notes on a user skill that it replaces the built-in of its
// name, and whether that built-in has changed since the copy was made.
func MarkOverride(skill *AgentSkill) {
	builtin, ok := FindBuiltin(skill.Name)
	if !ok {
		return
	}
	skill.OverridesBuiltin = true
	copied, okCopied := versionAfter(skill.BasedOn, builtin.Name+"@", true)
	current, okCurrent := versionAfter(builtin.Version, "", false)
	skill.BuiltinUpdated = okCopied && okCurrent && copied < current
}

// versionAfter is the version number after prefix in s.
func versionAfter(s *string, prefix string, trim bool) (uint64, bool) {
	if s == nil {
		return 0, false
	}
	rest, ok := strings.CutPrefix(*s, prefix)
	if !ok {
		return 0, false
	}
	if trim {
		rest = strings.TrimSpace(rest)
	}
	n, err := strconv.ParseUint(rest, 10, 32)
	return n, err == nil
}

// WritingContext appends the style profile and the library entries the
// text mentions to the completion instructions. The output rule is
// repeated after the profile, or the model restated the profile's rules
// in the suggestion.
func WritingContext(instructions string, ws *AgentWorkspace, before, after string) string {
	mentioned := PickLibrary(ws.Library, before+"\n"+after).Included
	section := WritingContextSection(ws.Style, mentioned)
	if section == "" {
		return instructions
	}
	instructions += "\n\n" + section + "\n\n"
	if ws.Style != nil {
		instructions += "Write the way the style profile describes. A limit on sentence length counts the whole sentence, including the writer's part before the cursor - end the sentence early if that part is already long. "
	}
	return instructions + "Output only the words to insert."
}
