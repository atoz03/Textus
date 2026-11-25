package backend

import (
	"context"
	"errors"
	"fmt"

	"textus/backend/ai"
	"textus/backend/ai/aicompat"
	"textus/backend/ai/rules"
	"textus/backend/ai/web"
)

// The chat's agent: one user message, answered by the model with the
// document's and the folder's tools, its stream sent to the window as it
// comes.

// maxSteps: skill loads, file reads and web searches all take steps.
const maxSteps = 12

// messageRequest is one user message for the agent, as the frontend sends
// it (src/platform/bindings/ai-agent.ts MessageRequest).
type messageRequest struct {
	Provider string `json:"provider"`
	// Blocks are the document, block by block as the editor has it.
	Blocks        []ai.SentBlock `json:"blocks"`
	DocPath       *string        `json:"docPath"`
	WorkspaceRoot *string        `json:"workspaceRoot"`
	// Conversation is the conversation, whose scratch folder holds its
	// attached files.
	Conversation *string              `json:"conversation"`
	History      []aicompat.AgentTurn `json:"history"`
	Message      string               `json:"message"`
	// Images are attachments with no text to extract; text-shaped ones
	// were already inlined into Message by the frontend.
	Images    []aicompat.ImageAttachment `json:"images"`
	WebSearch bool                       `json:"webSearch"`
	// Thinking is how hard the model thinks; nil is Auto.
	Thinking      *aicompat.ThinkingLevel `json:"thinking"`
	SearchService *string                 `json:"searchService"`
	SearxngURL    *string                 `json:"searxngUrl"`
	Mode          ai.AgentMode            `json:"mode"`
	Model         *string                 `json:"model"`
	RequestID     string                  `json:"requestId"`
	// FocusHeading is the heading of the section the user is working in.
	FocusHeading *string `json:"focusHeading"`
	// UILanguage is the UI's, for the names the agent is told the app
	// uses; nil is English.
	UILanguage *string `json:"uiLanguage"`
	// ImageModel is the image model, nil for none.
	ImageModel *ai.ImageModel `json:"imageModel"`
	// AskBeforeFileChanges: General mode's file changes wait to be
	// applied. Nil is the app's own default, true.
	AskBeforeFileChanges *bool `json:"askBeforeFileChanges"`
}

// agentRun is one message's request, as the agent works on it.
type agentRun struct {
	app  *App
	r    messageRequest
	mode ai.AgentMode
	ui   string
	// window is the asking window's label, whose plugins' tools the
	// request offers; "" for none.
	window string
	// send sends a live event to the window, paced.
	send func(ai.StreamEvent)
}

// agentMessage answers r: the new turns for the frontend's history.
func (a *App) agentMessage(ctx context.Context, r messageRequest, send func(ai.StreamEvent)) ([]aicompat.AgentTurn, error) {
	return a.agentMessageFrom(ctx, "", r, send)
}

// agentMessageFrom answers r for the window labelled window, whose
// plugins' tools it offers.
func (a *App) agentMessageFrom(ctx context.Context, window string, r messageRequest, send func(ai.StreamEvent)) ([]aicompat.AgentTurn, error) {
	if err := a.ensureAIOn(); err != nil {
		return nil, err
	}
	entry, ok := ai.Find(r.Provider)
	if !ok {
		return nil, fmt.Errorf("unknown provider: %s", r.Provider)
	}
	run := agentRun{app: a, r: r, mode: r.Mode, ui: deref(r.UILanguage), window: window, send: send}
	if run.ui == "" {
		run.ui = "en"
	}
	return run.answer(ctx, entry)
}

func (run *agentRun) answer(ctx context.Context, entry ai.Provider) ([]aicompat.AgentTurn, error) {
	r, a := run.r, run.app
	docPath := deref(r.DocPath)
	ws := a.workspace(docPath, deref(r.WorkspaceRoot))
	scratch, attached := withAttachments(deref(r.Conversation))
	ws.Attached = attached
	conv := a.conventionsFor(&ws)
	document := ai.DocumentFromBlocks(r.Blocks)
	// What the request is about, for picking library entries and, past the
	// budget, memories: the message as the user's side sent it, before the
	// reference packs go in.
	focus := r.Message
	ws.Memories.SelectFor(focus)
	// A /name call gets the skill's instructions and reference packs
	// filled in here, and history keeps the expanded message.
	message := ai.ExpandMessage(r.Message, ws.Skills, document.Text, run.ui, ai.ChosenProfile(conv, document.Text))
	// Numbered on from the sources earlier replies cited - so from the
	// history as it came, before it is cut down for sending.
	plan := run.webPlan(ctx)
	history, forgot := ai.CompactTelling(web.WithSourcesInText(r.History))
	if forgot {
		run.send(ai.StreamEvent{Type: "forgot"})
	}
	// A selected picture is shown to a model that can see, not only its
	// Markdown: "what's in this picture", "redo it brighter".
	images := r.Images
	if entry.SupportsVision {
		if picture, ok := selectedPicture(focus, docPath); ok {
			images = append(images, picture)
		}
	}
	// Refused rather than dropped: the model would answer confidently
	// about a picture it never saw. The frontend hides attach for these.
	if len(images) > 0 && !entry.SupportsVision {
		return nil, fmt.Errorf("%s can't read images. Remove the image, or switch provider in Settings > %s.", entry.Label, ai.AIName)
	}
	// The tool paths' document rides in this message; the tool-less
	// fallback builds its own. Create can't read the document, so it isn't
	// told to.
	reading := ai.ReadingFor(ai.BuiltinTools(false, run.mode).Offers(ai.ReadDocumentToolName), entry.ID)
	context, sent := ai.RequestContext(&ws, &document, reading, focus, deref(r.FocusHeading))
	recordLibraryUse(docPath, sent)
	text := ai.RequestMessage(&ws, run.mode, run.ui, context, message)
	askFirst := r.AskBeforeFileChanges == nil || *r.AskBeforeFileChanges
	// Written straight in: said before all else in the request.
	if !askFirst && run.mode == ai.ModeWrite {
		text = ai.ChangesWrittenIn + "\n\n" + text
	}
	thinking := aicompat.Auto
	if r.Thinking != nil {
		thinking = *r.Thinking
	}
	provider, tools, err := a.credentials().AgentProvider(r.Provider, deref(r.Model), thinking, plan.Native, netClient(0))
	if err != nil {
		return nil, err
	}
	pictures, _ := a.imageRun(r.ImageModel, entry.ID, docPath, r.RequestID)
	var turns []aicompat.AgentTurn
	if !tools {
		instructions := ai.BuildInstructions(&ws, &document, false, run.mode, focus, run.ui)
		turns, err = run.plainChat(ctx, instructions, history, message, deref(r.Model))
	} else {
		turns, err = run.withTools(ctx, toolRun{
			provider: provider, ws: &ws, document: &document, conv: conv, history: history,
			user: aicompat.UserTurn(text, images...), focus: focus, message: message, reading: reading, plan: plan, pictures: pictures,
			folders: agentFolders{root: deref(ws.Root), scratch: scratch, seesImages: entry.SupportsVision, data: a.dirs.Data},
			edits:   a.newFileEdits(deref(ws.Root), deref(r.Conversation), docPath, askFirst),
		})
	}
	if err != nil {
		return nil, err
	}
	// History keeps the message as the user wrote it: replaying the
	// document copy in every later request would multiply the prompt.
	for i := range turns {
		turns[i] = ai.ForHistory(turns[i])
	}
	if len(turns) > 0 && turns[0].Kind == aicompat.User {
		turns[0].Text = message
	}
	plan.AttachSources(turns)
	return turns, nil
}

// webPlan is how the request searches the web: the provider's own search,
// or the search service Settings chose, while the Web search button is on.
func (run *agentRun) webPlan(ctx context.Context) *web.Plan {
	r := run.r
	var service *web.Service
	if r.WebSearch {
		service = run.app.searchService(deref(r.SearchService), deref(r.SearxngURL))
	}
	return web.NewPlan(r.WebSearch, web.NativeSearchFor(r.Provider, deref(r.Model)), service, r.History).WithContext(ctx)
}

// toolRun is what a request with tools runs on.
type toolRun struct {
	provider       aicompat.AgentProvider
	ws             *ai.AgentWorkspace
	document       *ai.Document
	conv           *rules.Conventions
	history        []aicompat.AgentTurn
	user           aicompat.AgentTurn
	focus, message string
	reading        ai.Reading
	folders        agentFolders
	edits          *fileEdits
	plan           *web.Plan
	// pictures are the request's image model's, nil without one.
	pictures *ai.ImageRun
}

// registry is the tools the request offers.
func (run *agentRun) registry(ctx context.Context, t toolRun) *ai.ToolRegistry {
	tools := ai.BuiltinTools(t.ws.HasUsableSkills(), run.mode)
	if t.folders.root != "" || t.folders.scratch != "" {
		for _, tool := range t.folders.tools() {
			tools.Add(tool)
		}
	}
	// The folder's other files, in General mode.
	if t.edits != nil {
		for _, tool := range t.edits.tools() {
			tools.Add(tool)
		}
	}
	// The reference library's search, once there is a library.
	if tool, ok := referencesTool(t.folders.root); ok {
		tools.Add(tool)
	}
	// A document shown whole needs no tools to read it: offered, they were
	// read again and again instead of edited.
	if ai.ShownWhole(t.document.Text, ai.Reading{Tools: true, Whole: t.reading.Whole}) {
		tools.Remove(ai.SearchToolName)
		tools.Remove(ai.ReadDocumentToolName)
	}
	if t.pictures != nil {
		tools.Add(t.pictures.Tool(run.send))
	}
	for _, tool := range t.plan.Tools(t.focus) {
		tools.Add(tool)
	}
	// The user's earlier conversations, when they refer to one.
	tools.Add(searchConversationsTool(run.app.stores.chat))
	for _, tool := range run.app.installedTools(ctx, run.window) {
		tools.Add(tool)
	}
	// What the folder's index knows: tasks, backlinks, tags.
	for _, tool := range run.app.folderTools(ctx, run.window, t.folders.root) {
		tools.Add(tool)
	}
	// Create mode reads the plugins installed, to change one.
	if run.mode == ai.ModeCreate {
		tools.Add(run.app.readPluginTool())
	}
	return tools
}

// withTools runs the agent loop: each step streamed to the window, its
// calls run, until the model replies or the steps run out.
func (run *agentRun) withTools(ctx context.Context, t toolRun) ([]aicompat.AgentTurn, error) {
	instructions := ai.BuildInstructions(t.ws, t.document, true, run.mode, t.focus, run.ui)
	if section, ok := t.plan.PromptSection(); ok {
		instructions += "\n\n" + section
	}
	tc := ai.NewToolContext(t.document)
	// The text of proposals still undecided from earlier requests: an edit
	// or a picture may go into it, each picture once.
	tc.Undecided = ai.UndecidedEdits(t.history)
	tc.WithPictures(ai.UndecidedPictures(t.history))
	tc.Skills, tc.UILanguage, tc.Conventions, tc.Library = t.ws.Skills, run.ui, t.conv, ai.LibraryNames(t.ws.Library)
	tools := run.registry(ctx, t)
	specs := tools.Specs()
	// Pictures written into the text, begun as their lines stream in.
	var inline *ai.InlinePictures
	if t.pictures != nil && tools.Edits() {
		inline = t.pictures.Inline(tc)
	}
	// A native search's citation goes to the request's sources instead.
	forward := func(e aicompat.ProviderEvent) {
		if inline != nil {
			for _, picture := range inline.Observe(e) {
				run.send(picture)
			}
		}
		if e.Kind == aicompat.EventCitation {
			t.plan.Cite(e.URL, e.Title)
		} else if event, ok := ai.EventOf(e); ok {
			run.send(event)
		}
	}
	step := func(turns []aicompat.AgentTurn) (aicompat.StepResult, error) {
		// Edits and comments as lists to the model, one call per item past
		// it.
		var lists ai.ListStream
		result, _, err := t.provider.Step(ctx, instructions, ai.JoinCalls(turns), specs, func(e aicompat.ProviderEvent) { lists.Feed(e, forward) })
		if err != nil {
			return aicompat.StepResult{}, err
		}
		// Native search citations numbered; pictures written into the text
		// placed.
		settled, _ := ai.SettleStep(t.plan.FinishStep(result), t.document, t.conv)
		if inline != nil {
			settled = inline.Place(settled)
		}
		return settled, nil
	}
	// Messages the user sends in while it works, read between steps.
	box := openInbox(run.r.RequestID)
	defer box.close()
	turns, err := aicompat.Loop{
		MaxSteps: maxSteps,
		Step:     step,
		Execute:  func(call aicompat.ToolCall) aicompat.ToolOutput { return tools.Execute(tc, call) },
		HowToRun: tools.HowToRun,
		OnEvent: func(e aicompat.LoopEvent) {
			if e.Retry {
				run.send(ai.StreamEvent{Type: "retry"})
			} else {
				run.send(ai.StreamEvent{Type: "turn", Turn: ai.ForHistory(*e.Turn)})
			}
		},
		Steered: func() []aicompat.AgentTurn {
			var steered []aicompat.AgentTurn
			for _, text := range box.take() {
				steered = append(steered, aicompat.UserTurn(text))
			}
			return steered
		},
	}.Run(t.history, t.user)
	// A server that doesn't implement tools properly gets one tool-less
	// completion instead of failing the request. Only that: a rate limit,
	// a key refused or a timeout is said as it is, not hidden behind a
	// second request.
	if err != nil && t.provider.FallsBackToPlainChat() && ai.RefusesTools(err) && ctx.Err() == nil {
		instructions := ai.BuildInstructions(t.ws, t.document, false, run.mode, t.focus, run.ui)
		// No tools here, so no web_search: the user's message is searched
		// up front instead.
		if results, ok := t.plan.SearchAhead(t.focus); ok {
			instructions += "\n\n" + results
		}
		return run.plainChat(ctx, instructions, t.history, t.message, t.provider.Model)
	}
	if err != nil {
		return nil, errors.New(netErrorText(err))
	}
	return turns, nil
}

// plainChat is the answer of a provider without tool calling: the
// conversation flattened into one plain-text turn and a single completion.
// Images are dropped on purpose: a message with any was already refused.
func (run *agentRun) plainChat(ctx context.Context, instructions string, history []aicompat.AgentTurn, message, model string) ([]aicompat.AgentTurn, error) {
	text, err := run.app.complete(ctx, run.r.Provider, model, instructions, ai.FlattenedTranscript(history, message))
	if err != nil {
		return nil, err
	}
	return []aicompat.AgentTurn{aicompat.UserTurn(message), aicompat.AssistantTurn(text)}, nil
}

func init() {
	// One user message, answered with the new turns for the frontend's
	// history; its stream goes to onEvent as it comes, paced. Stopped by
	// ai_cancel with its request id.
	command("ai_agent_message", func(c *Call, a struct {
		Request messageRequest `json:"request"`
	}) ([]aicompat.AgentTurn, error) {
		paced := ai.NewPaced(func(e ai.StreamEvent) {
			// Never fails the request: a closed window only stops the show.
			_ = c.Send(e)
		})
		defer paced.Flush()
		return cancellable(c.ctx, a.Request.RequestID, func(ctx context.Context) ([]aicompat.AgentTurn, error) {
			return c.app.agentMessageFrom(ctx, c.Label(), a.Request, paced.Send)
		})
	})
	// Whether the chat composer offers Deep thinking for this provider and
	// Agent model (null: the provider's agent default).
	command("ai_can_think_deeper", func(c *Call, a struct {
		Provider string  `json:"provider"`
		Model    *string `json:"model"`
	}) (bool, error) {
		return ai.CanThinkDeeper(a.Provider, deref(a.Model)), nil
	})
}
