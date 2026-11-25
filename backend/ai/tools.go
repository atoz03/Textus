package ai

import (
	_ "embed"
	"encoding/json/v2"
	"fmt"
	"slices"
	"strings"
	"sync"

	"textus/backend/ai/aicompat"
	"textus/backend/ai/rules"
)

// The tools one request offers, assembled for its mode, and what they need
// to do their work. The document's tools are here; the backend adds the
// ones that reach files, past conversations and the web.

// The built-in tools' names.
const (
	SearchToolName           = "search_document"
	ReadDocumentToolName     = "read_document"
	CheckConventionsToolName = "check_conventions"
	MeasureTextToolName      = "measure_text"
	AddCommentToolName       = "add_comment"
	ProposeEditToolName      = "propose_edit"
	UseSkillToolName         = "use_skill"
	// GenerateImageToolName is the image model's tool: its calls name a
	// block, as a comment's do.
	GenerateImageToolName = "generate_image"
)

// Access is what calling a tool does, which decides how its calls run
// (ToolRegistry.HowToRun).
type Access uint8

const (
	// ReadOnly only reads (the document, the project, the web): a step's
	// reads run together.
	ReadOnly Access = iota
	// Proposes proposes a change the user reviews, or changes something of
	// Textus AI's own: one call at a time, in call order.
	Proposes
	// ProposesInItsCall proposes, and records what it made in its own call
	// (a file change's id), which is where the frontend reads it from.
	ProposesInItsCall
)

// The modes a tool is offered in.
var (
	EveryMode = []AgentMode{ModeReview, ModeWrite, ModeCreate}
	// DocumentModes work on the document - reading, searching and checking
	// it, and commenting on it. Create makes things for the app, not the
	// document, so it is offered none of these.
	DocumentModes = []AgentMode{ModeReview, ModeWrite}
	// EditingModes change the document.
	EditingModes = []AgentMode{ModeWrite}
)

// Tool is a tool the agent can call: what the model is told, the modes it
// is offered in, and its implementation - Text for one that answers with
// text alone, else Run.
type Tool struct {
	Spec   aicompat.ToolSpec
	Access Access
	Modes  []AgentMode
	Text   func(tc *ToolContext, arguments string) string
	Run    func(tc *ToolContext, call aicompat.ToolCall) aicompat.ToolOutput
}

//go:embed agent_tools.json
var toolSpecsJSON []byte

// toolSpecs are the built-in tools' specs, as the Rust app offered them,
// by name ("+images" for the variant a model that sees images gets).
var toolSpecs = func() map[string]aicompat.ToolSpec {
	var raw map[string]struct {
		Description string `json:"description"`
		Parameters  any    `json:"parameters"`
	}
	if err := json.Unmarshal(toolSpecsJSON, &raw); err != nil {
		panic("ai/agent_tools.json: " + err.Error())
	}
	specs := map[string]aicompat.ToolSpec{}
	for key, s := range raw {
		name, _, _ := strings.Cut(key, "+")
		specs[key] = aicompat.ToolSpec{Name: name, Description: s.Description, Parameters: filledSchema(s.Parameters)}
	}
	return specs
}()

// filledSchema is a schema with its lists filled in: the edit actions and
// the measure_text metrics, kept in one place each.
func filledSchema(v any) any {
	switch v := v.(type) {
	case map[string]any:
		for k, x := range v {
			v[k] = filledSchema(x)
		}
	case []any:
		for i, x := range v {
			v[i] = filledSchema(x)
		}
	case string:
		switch v {
		case "__EDIT_ACTIONS__":
			return EditActions()
		case "__METRICS__":
			return rules.Metrics
		}
	}
	return v
}

// ToolSpecOf is a built-in tool's spec by name, with the "+images"
// variant when the model sees images and there is one.
func ToolSpecOf(name string, seesImages bool) aicompat.ToolSpec {
	if s, ok := toolSpecs[name+"+images"]; ok && seesImages {
		return s
	}
	s, ok := toolSpecs[name]
	if !ok {
		panic("no spec for tool " + name)
	}
	return s
}

// ToolContext is everything a tool implementation needs. One context
// serves one request, every step of it.
type ToolContext struct {
	// Undecided are proposals from earlier requests the user hasn't decided
	// on: their text is part of the document as the user sees it, so an
	// edit may change it.
	Undecided []UndecidedEdit
	Document  *Document
	// Skills are the merged skills: built-in, global config folder,
	// project .textus/.
	Skills []AgentSkill
	// UILanguage is which language's conventions a skill gets when the
	// document is too short to tell.
	UILanguage string
	// Conventions are the writing conventions check_conventions checks
	// against.
	Conventions *rules.Conventions
	// Library is the library's names and aliases, for the library-name
	// rule.
	Library []string

	mu sync.Mutex
	// proposed is what this request's proposals have done so far.
	proposed ProposalLedger
	// checks are how often this request has called each check tool.
	checks map[string]int
}

// NewToolContext is a context that knows only document: no skills,
// earlier proposals or library, English names, no conventions set. A
// request sets what it knows beside it.
func NewToolContext(document *Document) *ToolContext {
	return &ToolContext{Document: document, UILanguage: "en", Conventions: &rules.Conventions{}}
}

// WithPictures has the ledger know these pictures already: earlier
// requests', not decided yet, which this request's text may carry.
func (tc *ToolContext) WithPictures(pictures []string) *ToolContext {
	tc.proposed.pictures = pictures
	return tc
}

// ProposedAny reports whether this request has proposed any change yet.
func (tc *ToolContext) ProposedAny() bool {
	tc.mu.Lock()
	defer tc.mu.Unlock()
	return tc.proposed.proposedAny()
}

// ProposedTexts are the new text of each proposal this request made.
func (tc *ToolContext) ProposedTexts() []string {
	tc.mu.Lock()
	defer tc.mu.Unlock()
	return slices.Clone(tc.proposed.texts)
}

// ToolRegistry is the tools one request offers, assembled for its mode:
// the built-ins with something to work on, then what the request adds. The
// one place a tool is looked up by name.
type ToolRegistry struct {
	mode AgentMode
	// shared: a helper outside any conversation (a paper's researcher),
	// offered only the tools every mode offers.
	shared bool
	tools  []Tool
}

// NewToolRegistry is no tools yet: what mode offers is added to it.
func NewToolRegistry(mode AgentMode) *ToolRegistry { return &ToolRegistry{mode: mode} }

// SharedToolRegistry is no tools yet: only what every mode offers is
// added to it.
func SharedToolRegistry() *ToolRegistry { return &ToolRegistry{shared: true} }

// BuiltinTools is the document's tools for mode, and use_skill when there
// are skills; the backend adds the folder's.
func BuiltinTools(hasSkills bool, mode AgentMode) *ToolRegistry {
	r := NewToolRegistry(mode)
	for _, t := range documentTools() {
		r.Add(t)
	}
	if hasSkills {
		r.Add(Tool{Spec: ToolSpecOf(UseSkillToolName, false), Access: ReadOnly, Modes: EveryMode, Text: useSkill})
	}
	return r
}

// admits reports whether a tool offered in modes is offered here.
func (r *ToolRegistry) admits(modes []AgentMode) bool {
	if !r.shared {
		return slices.Contains(modes, r.mode)
	}
	for _, m := range EveryMode {
		if !slices.Contains(modes, m) {
			return false
		}
	}
	return true
}

// Add adds t if this registry offers it.
func (r *ToolRegistry) Add(t Tool) {
	if r.admits(t.Modes) {
		r.tools = append(r.tools, t)
	}
}

// Only keeps the tool named name, if it is offered: what a chapter's
// helper has.
func (r *ToolRegistry) Only(name string) *ToolRegistry {
	r.tools = slices.DeleteFunc(r.tools, func(t Tool) bool { return t.Spec.Name != name })
	return r
}

// Remove takes the tool named name out, if it is offered.
func (r *ToolRegistry) Remove(name string) {
	r.tools = slices.DeleteFunc(r.tools, func(t Tool) bool { return t.Spec.Name == name })
}

// Specs are what the model is told of the tools, in order.
func (r *ToolRegistry) Specs() []aicompat.ToolSpec {
	specs := make([]aicompat.ToolSpec, len(r.tools))
	for i, t := range r.tools {
		specs[i] = t.Spec
	}
	return specs
}

func (r *ToolRegistry) find(name string) (*Tool, bool) {
	for i := range r.tools {
		if r.tools[i].Spec.Name == name {
			return &r.tools[i], true
		}
	}
	return nil, false
}

// Offers reports whether the tool named name is offered.
func (r *ToolRegistry) Offers(name string) bool {
	_, ok := r.find(name)
	return ok
}

// Edits reports whether this request may change the document: its mode
// has propose_edit.
func (r *ToolRegistry) Edits() bool { return r.Offers(ProposeEditToolName) }

// HowToRun is how the agent loop runs name's calls. A name the model made
// up runs on its own, and is told it doesn't exist.
func (r *ToolRegistry) HowToRun(name string) aicompat.ToolRun {
	t, ok := r.find(name)
	switch {
	case ok && t.Access == ReadOnly:
		return aicompat.Concurrent
	case ok && t.Access == ProposesInItsCall:
		return aicompat.Amending
	}
	return aicompat.InOrder
}

// Execute runs one call.
func (r *ToolRegistry) Execute(tc *ToolContext, call aicompat.ToolCall) aicompat.ToolOutput {
	if refusal := spendCheck(tc, call.Name, call.Arguments); refusal != "" {
		return aicompat.Text(refusal)
	}
	t, ok := r.find(call.Name)
	switch {
	case !ok:
		return aicompat.Text("Unknown tool: " + call.Name)
	case t.Text != nil:
		text := t.Text(tc, call.Arguments)
		edited := !r.Edits() || tc.ProposedAny()
		return aicompat.ToolOutput{
			Text:              text,
			Settled:           settles(call.Name, text, edited),
			SettlesWithOthers: call.Name == AddCommentToolName,
		}
	}
	return t.Run(tc, call)
}

// checkBudgets are how often one request may call each check tool before
// it stops answering: a check that keeps answering invites a
// draft-measure-rewrite loop. Measuring against a platform's limit is not
// counted: such text is worth trimming.
var checkBudgets = map[string]int{MeasureTextToolName: 3, CheckConventionsToolName: 4}

// draftChars is how long a `text` not in the document may be before a
// check of it is a hidden draft: a sentence or a short paragraph.
const draftChars = 200

// measuresALimit reports whether a measure_text call measures against a
// platform's limit (a metric with one, such as x_weighted), not a plain
// count.
func measuresALimit(arguments string) bool {
	args, ok := ParseArgs(arguments)
	if !ok {
		return false
	}
	metric, ok := args.Str("metric")
	if !ok {
		return false
	}
	m, ok := rules.MeasureText("", metric)
	return ok && m.Limit != nil
}

// refusesADraft is the refusal of a check of a draft (new, long, not from
// the document, not measured against a limit): drafted unseen, the user
// waited minutes. Proposed instead, the text streams in where they watch.
func refusesADraft(tc *ToolContext, name, arguments string) string {
	if name != MeasureTextToolName && name != CheckConventionsToolName {
		return ""
	}
	if name == MeasureTextToolName && measuresALimit(arguments) {
		return ""
	}
	args, ok := ParseArgs(arguments)
	if !ok {
		return ""
	}
	text, ok := args.Str("text")
	text = strings.TrimSpace(text)
	if !ok || len([]rune(text)) <= draftChars || strings.Contains(tc.Document.Text, text) {
		return ""
	}
	return fmt.Sprintf("Not run: %s doesn't take a draft of new text. Propose the text with propose_edit now - the user sees it come in, its result tells you its size, and conventions are fixed on the way. If it misses a length the user asked for, say so in your reply.", name)
}

// spendCheck counts a call to a check tool, and refuses it once the
// request has used up that tool's budget or when it checks a hidden draft.
// "" otherwise.
func spendCheck(tc *ToolContext, name, arguments string) string {
	if refusal := refusesADraft(tc, name, arguments); refusal != "" {
		return refusal
	}
	budget, ok := checkBudgets[name]
	if !ok || (name == MeasureTextToolName && measuresALimit(arguments)) {
		return ""
	}
	tc.mu.Lock()
	defer tc.mu.Unlock()
	if tc.checks == nil {
		tc.checks = map[string]int{}
	}
	tc.checks[name]++
	if tc.checks[name] <= budget {
		return ""
	}
	return fmt.Sprintf("Not run: %s was already called %d times in this request, which is all it gets. Stop checking and finish with what you have: propose your latest draft (when the request changes the document), then reply.", name, budget)
}

// settles reports whether result is a valid proposal or comment with
// nothing the model has to answer. A comment settles only once the request
// can't edit or has proposed a change: otherwise the edits may be about to
// follow.
func settles(name, result string, edited bool) bool {
	switch name {
	case ProposeEditToolName:
		return strings.HasPrefix(result, editProposed)
	case AddCommentToolName:
		return edited && strings.HasPrefix(result, commentAdded)
	}
	return false
}
