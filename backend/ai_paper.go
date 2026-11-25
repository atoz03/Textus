package backend

import (
	"context"
	"errors"
	"fmt"

	"textus/backend/ai"
	"textus/backend/ai/aicompat"
	"textus/backend/ai/paper"
	"textus/backend/ai/web"
)

// Textus AI's paper writer as the app runs it (backend/ai/paper): a topic
// researched and outlined, the outline approved and kept, then the paper
// drafted, reviewed and finished. Each run reports to its card through
// the command's channel and is stopped like any request, by ai_cancel
// with its request id.

// agentRequest is what every request of the paper writer names, whatever
// it asks: the fields of AgentRequest in src/platform/bindings/ai-paper.ts
// it uses.
type agentRequest struct {
	Provider      string  `json:"provider"`
	Model         *string `json:"model"`
	DocPath       *string `json:"docPath"`
	WorkspaceRoot *string `json:"workspaceRoot"`
	// Conversation is the conversation whose scratch folder holds its
	// attached files.
	Conversation *string `json:"conversation"`
	// SearchService and SearxngURL are what web_search uses when the model
	// can't search by itself.
	SearchService *string `json:"searchService"`
	SearxngURL    *string `json:"searxngUrl"`
	RequestID     string  `json:"requestId"`
	// ImageModel is the image model; nil for none.
	ImageModel *ai.ImageModel `json:"imageModel"`
}

// paperRequest is what starting a paper takes (PaperRequest in
// src/platform/bindings/ai-paper.ts).
type paperRequest struct {
	agentRequest
	// ID is the paper to go on with, or the id a new one is kept under;
	// nil for a new id.
	ID    *string `json:"id"`
	Topic string  `json:"topic"`
	// Genre is the kind of piece to write; nil for the model to choose.
	Genre    *paper.Genre `json:"genre"`
	Language string       `json:"language"`
	// TargetChars is its length; nil for the genre's or the model's.
	TargetChars *uint32 `json:"targetChars"`
	SubHelpers  bool    `json:"subHelpers"`
}

// draftRequest is what drafting an outlined paper takes (DraftRequest in
// src/platform/bindings/ai-paper.ts).
type draftRequest struct {
	agentRequest
	// ID is the paper, outlined and its outline approved.
	ID string `json:"id"`
	// CheckClaims checks every cited sentence against its notes before the
	// review's revision.
	CheckClaims bool `json:"checkClaims"`
}

// paperRoot is the folder paper id ("" for a new one) is kept under: its
// project's data folder, or the scratch space while there is no project
// folder. A paper begun in scratch moves to the project the first time it
// is asked for there, and one an older Textus kept in the project's
// .textus/papers/ moves out of it.
func (a *App) paperRoot(docPath, workspaceRoot, id string) (string, error) {
	scratch, err := researchDir()
	if err != nil {
		return "", err
	}
	base := ""
	if root, ok := projectRoot(docPath, workspaceRoot); ok {
		global := a.globalAgentPath()
		adoptProjectData(global, root, paper.PapersFolder)
		base = projectDataDir(global, root)
	}
	return paper.RootIn(scratch, base, id)
}

// paperProvider is r's provider, refused while Textus AI is off.
func (a *App) paperProvider(r agentRequest) (ai.Provider, error) {
	if err := a.ensureAIOn(); err != nil {
		return ai.Provider{}, err
	}
	entry, ok := ai.Find(r.Provider)
	if !ok {
		return ai.Provider{}, fmt.Errorf("unknown provider: %s", r.Provider)
	}
	return entry, nil
}

// paperSession is what a paper's run steps: r's provider's agent,
// searching the web by itself with native (nil for none) - refused for
// one that can't call tools, which every stage hands its work over with.
func (a *App) paperSession(r agentRequest, entry ai.Provider, root string, native *aicompat.NativeSearch, send func(paper.Event) bool) (*paper.Session, error) {
	agent, tools, err := a.credentials().AgentProvider(r.Provider, deref(r.Model), aicompat.Auto, native, netClient(0))
	if err != nil {
		return nil, err
	}
	if !tools {
		return nil, fmt.Errorf("%s can't call tools, which writing a paper needs.", entry.Label)
	}
	docPath, workspaceRoot := deref(r.DocPath), deref(r.WorkspaceRoot)
	return &paper.Session{
		Root: root, Agent: paper.Provider{Agent: agent}, AtOnce: entry.PaperAtOnce(), Send: send,
		Guidance: func(topic string) string {
			ws := a.workspace(docPath, workspaceRoot)
			return ai.WritingGuidance(&ws, topic)
		},
	}, nil
}

// paperFolders reads the researchers' folders as the file tools do.
type paperFolders struct{}

func (paperFolders) Readable(dir string, images bool) []string {
	return walkReadableFiles(dir, images).files
}

func (paperFolders) Resolve(folder, path string) (string, string, bool) {
	abs, rel, err := resolveIn(folder, path)
	return abs, rel, err == nil
}

func (paperFolders) Text(folder, path string) (string, bool) {
	text, err := readMention(folder, path)
	return text, err == nil
}

// paperKit is the researchers' kit: the tools every mode shares, the
// project folder's files and the paper's folder's attached/ beside them,
// and the reference library.
func (a *App) paperKit(project, folder string, seesImages bool) *paper.Kit {
	tools := ai.SharedToolRegistry()
	if project != "" || folder != "" {
		for _, tool := range (agentFolders{root: project, scratch: folder, seesImages: seesImages, data: a.dirs.Data}).tools() {
			tools.Add(tool)
		}
	}
	if tool, ok := referencesTool(project); ok {
		tools.Add(tool)
	}
	return &paper.Kit{
		Tools: tools, Ctx: ai.NewToolContext(&ai.Document{}),
		Root: project, Folder: folder, SeesImages: seesImages, Files: paperFolders{},
	}
}

// paperRun researches and outlines the paper r asks for, in the
// document's project, its progress sent with send; a paper begun before
// goes on from what it kept.
func (a *App) paperRun(ctx context.Context, r paperRequest, send func(paper.Event) bool) (paper.Paper, error) {
	entry, err := a.paperProvider(r.agentRequest)
	if err != nil {
		return paper.Paper{}, err
	}
	docPath, workspaceRoot := deref(r.DocPath), deref(r.WorkspaceRoot)
	root, err := a.paperRoot(docPath, workspaceRoot, deref(r.ID))
	if err != nil {
		return paper.Paper{}, err
	}
	project, _ := projectRoot(docPath, workspaceRoot)
	native := web.NativeSearchFor(entry.ID, deref(r.Model))
	request := paper.Request{
		ID: deref(r.ID), Topic: r.Topic, Genre: r.Genre, Language: r.Language, TargetChars: r.TargetChars,
		SubHelpers: r.SubHelpers, Native: native, Service: a.searchService(deref(r.SearchService), deref(r.SearxngURL)),
		Kit: func(folder string) *paper.Kit { return a.paperKit(project, folder, entry.SupportsVision) },
	}
	// As many pictures as one send may make: the outline plans no more.
	if run, ok := a.imageRun(r.ImageModel, entry.ID, docPath, r.RequestID); ok {
		request.Pictures = uint32(run.Limit())
	}
	// The conversation's attached files, copied into the paper's folder.
	request.Conversation, _ = withAttachments(deref(r.Conversation))
	return cancellable(ctx, r.RequestID, func(ctx context.Context) (paper.Paper, error) {
		s, err := a.paperSession(r.agentRequest, entry, root, native, send)
		if err != nil {
			return paper.Paper{}, err
		}
		written, err := paper.Run(ctx, s, request)
		if err != nil {
			return paper.Paper{}, errors.New(netErrorText(err))
		}
		return written, nil
	})
}

// paperDraft drafts paper r.ID from its saved outline to the finished
// text, its progress sent with send.
func (a *App) paperDraft(ctx context.Context, r draftRequest, send func(paper.Event) bool) error {
	entry, err := a.paperProvider(r.agentRequest)
	if err != nil {
		return err
	}
	docPath := deref(r.DocPath)
	root, err := a.paperRoot(docPath, deref(r.WorkspaceRoot), r.ID)
	if err != nil {
		return err
	}
	// The paper's pictures are made with the request's image model.
	pictures, _ := a.imageRun(r.ImageModel, entry.ID, docPath, r.RequestID)
	_, err = cancellable(ctx, r.RequestID, func(ctx context.Context) (nothing, error) {
		s, err := a.paperSession(r.agentRequest, entry, root, nil, send)
		if err != nil {
			return nil, err
		}
		if err := paper.Write(ctx, s, r.ID, r.CheckClaims, pictures); err != nil {
			return nil, errors.New(netErrorText(err))
		}
		return nil, nil
	})
	return err
}

func init() {
	// A paper's events go to the card through the command's channel.
	sender := func(c *Call) func(paper.Event) bool {
		return func(e paper.Event) bool { return c.Send(paper.Wire(e)) == nil }
	}
	command("ai_paper_run", func(c *Call, a struct {
		Request paperRequest `json:"request"`
	}) (paper.Paper, error) {
		return c.app.paperRun(c.ctx, a.Request, sender(c))
	})
	command("ai_paper_draft", func(c *Call, a struct {
		Request draftRequest `json:"request"`
	}) (nothing, error) {
		return nil, c.app.paperDraft(c.ctx, a.Request, sender(c))
	})
	// What the window made of the blocks a drawFigures event sent: for
	// each, in order, why it couldn't be drawn, or null.
	command("ai_paper_figures_drawn", func(c *Call, a struct {
		CallID string    `json:"callId"`
		Errors []*string `json:"errors"`
	}) (nothing, error) {
		paper.FiguresDrawn(a.CallID, a.Errors)
		return nil, nil
	})
	// How far a kept paper came, for its card to go on from where it
	// stopped.
	command("ai_paper_status", func(c *Call, a struct {
		projectArgs
		ID string `json:"id"`
	}) (paper.PaperProgress, error) {
		root, err := c.app.paperRoot(deref(a.DocPath), deref(a.WorkspaceRoot), a.ID)
		if err != nil {
			return paper.PaperProgress{}, err
		}
		return paper.Status(root, a.ID)
	})
	// The outline as the user edited it on the card, kept for drafting.
	command("ai_paper_save_outline", func(c *Call, a struct {
		projectArgs
		ID      string        `json:"id"`
		Outline paper.Outline `json:"outline"`
	}) (paper.Outline, error) {
		root, err := c.app.paperRoot(deref(a.DocPath), deref(a.WorkspaceRoot), a.ID)
		if err != nil {
			return paper.Outline{}, err
		}
		return paper.SaveOutline(root, a.ID, a.Outline)
	})
}
