package ai

import (
	"cmp"
	"slices"
	"strings"
	"unicode/utf8"
)

// The system prompt the agent works under: its standing rules, the project
// folder and skills it is told about, and the mode it is in. The words are
// agent_text.go's.

// AIName is what the assistant is called in the UI (the frontend's
// AI_NAME).
const AIName = "Textus AI"

// AppGuide is the name use_skill loads the app guide by: what the app
// calls things, where they are, and how to answer a how-to question.
const AppGuide = "app-guide"

// indexDescriptionChars is the longest description the skill index shows;
// a longer one (a user's skill) is cut with "…".
const indexDescriptionChars = 500

// fill is template with each {name} replaced by the value after name in
// named (name, value pairs) and each bare {} by the next of positional.
// The template is read once, so a value with braces in it stays as it is.
func fill(template string, positional []string, named ...string) string {
	var out strings.Builder
	next := 0
	rest := template
	for {
		open := strings.IndexByte(rest, '{')
		if open < 0 {
			break
		}
		end := strings.IndexByte(rest[open:], '}')
		if end < 0 {
			break
		}
		end += open
		name := rest[open+1 : end]
		value, found := "", false
		if name == "" && next < len(positional) {
			value, found = positional[next], true
			next++
		}
		for i := 0; !found && i+1 < len(named); i += 2 {
			if named[i] == name {
				value, found = named[i+1], true
			}
		}
		if !found {
			out.WriteString(rest[:end+1])
			rest = rest[end+1:]
			continue
		}
		out.WriteString(rest[:open])
		out.WriteString(value)
		rest = rest[end+1:]
	}
	out.WriteString(rest)
	return out.String()
}

// whereToChooseTheProjectFolder is where the user changes the project
// folder. The project menu exists only while there is a project folder, so
// without one only Settings is named.
func whereToChooseTheProjectFolder(hasMenu bool) string {
	settings := fill(folderSettingsTemplate, nil, "AI_NAME", AIName)
	if hasMenu {
		return fill(folderMenuTemplate, nil, "settings", settings)
	}
	return settings
}

// workspaceSection is what the agent is told about its project folder, and
// what the app calls the things that shape its work, in English and the
// user's UI language. Without it, the model made up names and gave users
// wrong directions.
func workspaceSection(ws *AgentWorkspace, uiLanguage string) string {
	attached := ""
	if ws.Attached {
		attached = attachedNote
	}
	notes := []string{"readable", readableNote, "file_tools", fileToolsNote, "attached", attached, "one_folder", oneFolder}
	var folder string
	switch {
	case ws.Root != nil && ws.RootSource == RootChosen:
		folder = fill(folderChosenTemplate, []string{whereToChooseTheProjectFolder(true)}, append(notes, "root", *ws.Root)...)
	case ws.Root != nil:
		folder = fill(folderEnclosingTemplate, []string{whereToChooseTheProjectFolder(true)}, append(notes, "root", *ws.Root)...)
	case ws.RootSource == RootTooBroad:
		folder = fill(folderTooBroadTemplate, []string{whereToChooseTheProjectFolder(false)}, "attached", attached)
	default:
		folder = fill(folderNoneTemplate, []string{whereToChooseTheProjectFolder(false)}, "attached", attached)
	}
	names := ""
	if ui, ok := UINamesOf(uiLanguage); ok {
		rows := make([]string, len(ui.Names))
		for i, pair := range ui.Names {
			rows[i] = pair[0] + " | " + pair[1]
		}
		names = fill(uiNamesTemplate, []string{ui.OfEnglish("style profile"), ui.OfEnglish("library")},
			"english", ui.English, "AI_NAME", AIName, "native", NativeName(ui.Code), "rows", strings.Join(rows, "\n"))
	}
	// The app guide is loaded on demand where use_skill can load it: it was
	// ~900 tokens of every request, for the few that ask about the app.
	guide := appGuideText + "\n"
	if ws.HasUsableSkills() {
		guide = appGuidePointer + "\n"
	}
	return "---project---\n" + folder + "\n" + guide + names
}

// UserGuidance is what the user asks of everything written for them: the
// writing instructions (agent.md), global then the project's - with both,
// the project's section says it wins a conflict - then the folder's
// AGENTS.md, then, with memories, what Textus AI remembered, which the
// instructions win over.
func UserGuidance(ws *AgentWorkspace, memories bool) []string {
	var sections []string
	for _, instructions := range ws.Instructions {
		sections = append(sections, "---user instructions---\n"+instructions)
	}
	if ws.ProjectInstructions != nil {
		if len(ws.Instructions) == 0 {
			sections = append(sections, "---user instructions---\n"+*ws.ProjectInstructions)
		} else {
			sections = append(sections, "---user instructions for this folder (where they conflict with the ones above, follow these)---\n"+*ws.ProjectInstructions)
		}
	}
	if ws.AgentsMD != nil {
		sections = append(sections, "---the folder's AGENTS.md (its instructions for AI agents working in it; where they conflict with the user instructions above, follow those)---\n"+*ws.AgentsMD)
	}
	if memories {
		sections = append(sections, MemorySections(ws.Memories)...)
	}
	return sections
}

// WritingGuidance is UserGuidance for a piece written outside the chat:
// with the memories and the style profile and library entries focus - what
// it is about - calls for, as one block of instructions.
func WritingGuidance(ws *AgentWorkspace, focus string) string {
	ws.Memories.SelectFor(focus)
	pick := PickLibrary(ws.Library, focus)
	sections := UserGuidance(ws, true)
	if section := WritingContextSection(ws.Style, pick.Included); section != "" {
		sections = append(sections, section)
	}
	return strings.Join(sections, "\n\n")
}

// BuildInstructions is the static half of the prompt: role, writing
// instructions and skill index (and the document, tool-less). Skill bodies
// and files come through tools. focus is what the request is about (the
// user's message): library entries are picked by what it and the document
// mention. uiLanguage's names for things are given beside the English
// ones.
func BuildInstructions(ws *AgentWorkspace, document *Document, withTools bool, mode AgentMode, focus, uiLanguage string) string {
	sections := []string{agentRole}
	// Tool-less, everything goes in the system prompt; with tools, what
	// changes per request comes with the message, so the cached prefix
	// stays put.
	if !withTools {
		sections = append(sections, modeSection(mode, uiLanguage))
	} else {
		switch mode {
		case ModeReview:
			sections = append(sections, agentReadOnlyToolInstructions)
		case ModeCreate:
			sections = append(sections, agentCreateToolInstructions)
		default:
			sections = append(sections, agentToolInstructions)
		}
		// check_conventions and measure_text aren't offered in Create.
		if mode != ModeCreate {
			sections = append(sections, rulesToolInstructions)
		}
		sections = append(sections, workspaceSection(ws, uiLanguage))
	}
	// What Textus AI remembered only without tools; with them it comes with
	// the message, keeping the cache.
	sections = append(sections, UserGuidance(ws, !withTools)...)
	// The skill index: names and descriptions only; bodies load via
	// use_skill. /name-only skills (and turned-off built-ins) aren't
	// offered.
	if index := skillIndex(ws, mode, uiLanguage); withTools && index != "" {
		sections = append(sections, fill(availableSkillsTemplate, []string{strings.Join(ReferencePackNames(), ", ")}, "index", index))
	}
	// Tool-calling paths carry the document in the latest user message, so
	// this prompt stays byte-identical and the prompt cache survives. The
	// tool-less fallback keeps it here.
	if !withTools {
		context, _ := RequestContext(ws, document, LeastReading(false), focus, "")
		sections = append(sections, context)
	}
	return strings.Join(sections, "\n\n")
}

// modeSection is what the request says of its mode, in the UI's names for
// the modes: told "read-only mode", the model called it that and never said
// how to get the edit made.
func modeSection(mode AgentMode, uiLanguage string) string {
	ui, translated := UINamesOf(uiLanguage)
	named := func(mode string, first bool) string {
		switch {
		case translated && first:
			return fill(modeNamedFirstTemplate, []string{ui.OfEnglish(mode), ui.English}, "mode", mode)
		case translated:
			return fill(modeNamedTemplate, []string{ui.OfEnglish(mode)}, "mode", mode)
		}
		return fill(modePlainTemplate, nil, "mode", mode)
	}
	switch mode {
	case ModeReview:
		return fill(modeReviewTemplate, []string{named("Review", true), named("General", false)})
	case ModeCreate:
		return fill(modeCreateTemplate, []string{named("Create", true), named("General", false)})
	}
	return fill(modeWriteTemplate, []string{named("General", true)}, "UNDECIDED_TEXT", undecidedText)
}

// RequestPreamble is what a tool-calling request says before its context:
// the mode and what Textus AI remembers. Both change mid-conversation, so
// keeping them out of the system prompt keeps its cache.
func RequestPreamble(ws *AgentWorkspace, mode AgentMode, uiLanguage string) string {
	sections := append([]string{modeSection(mode, uiLanguage)}, MemorySections(ws.Memories)...)
	return strings.Join(sections, "\n\n")
}

// skillIndex is the skill index the model sees: the user's skills first,
// then built-ins by category and name under the UI's name for them. It
// depends only on the workspace and UI language, so the system prompt
// stays byte-identical.
func skillIndex(ws *AgentWorkspace, mode AgentMode, uiLanguage string) string {
	var offered []*AgentSkill
	for i := range ws.Skills {
		s := &ws.Skills[i]
		if s.OfferedToModel() && !s.Off && slices.Contains(s.Modes, mode) {
			offered = append(offered, s)
		}
	}
	rank := func(s *AgentSkill) (int, int) {
		layer := 2
		switch s.Source {
		case SourceProject:
			layer = 0
		case SourceGlobal:
			layer = 1
		}
		category := len(CategoryOrder)
		if s.Category != nil {
			if at := slices.Index(CategoryOrder, *s.Category); at >= 0 {
				category = at
			}
		}
		return layer, category
	}
	// Stable: the user's skills keep their file order.
	slices.SortStableFunc(offered, func(a, b *AgentSkill) int {
		la, ca := rank(a)
		lb, cb := rank(b)
		if c := cmp.Or(cmp.Compare(la, lb), cmp.Compare(ca, cb)); c != 0 || la != 2 {
			return c
		}
		return strings.Compare(a.Name, b.Name)
	})
	lines := make([]string, len(offered))
	for i, s := range offered {
		// Create mode's prompt asks for "the skill marked [create]".
		category := ""
		if s.Category != nil && *s.Category == "create" {
			category = " [create]"
		}
		mark := ""
		switch s.Source {
		case SourceProject:
			mark = " (project)"
		case SourceGlobal:
			mark = " (yours)"
		default:
			if aliases := s.Aliases[uiLanguage]; len(aliases) > 0 && uiLanguage != "en" {
				mark = " [/" + aliases[0] + " in their UI]"
			}
		}
		// A built-in's description also carries its request words in other
		// languages; its `textus-index` line is the short form.
		description := s.Description
		if s.Source == SourceBuiltin && s.IndexLine != nil {
			description = *s.IndexLine
		}
		if utf8.RuneCountInString(description) > indexDescriptionChars {
			description = string([]rune(description)[:indexDescriptionChars-1]) + "…"
		}
		lines[i] = "- " + s.Name + category + mark + ": " + description
	}
	return strings.Join(lines, "\n")
}
