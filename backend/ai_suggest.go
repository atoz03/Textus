package backend

import (
	"context"
	"errors"
	"strings"
	"time"

	"textus/backend/ai"
)

// The short writing requests: one call to the writing model, no tools and
// no stream, for the inline completion at the cursor, a title for an
// untitled document and the messages the writer will probably send next.

// writingTimeout bounds one whole request, so a hung proxy or server can't
// hold it forever.
const writingTimeout = 120 * time.Second

// credentials are where a request's key or endpoint is looked up.
func (a *App) credentials() ai.Credentials {
	return ai.Credentials{Key: a.providerKey, Endpoint: a.customEndpoint}
}

// complete sends one short request to provider's model ("" for its
// default) and answers its text.
func (a *App) complete(ctx context.Context, provider, model, instructions, text string) (string, error) {
	route, err := a.credentials().Route(provider, model)
	if err != nil {
		return "", err
	}
	answer, err := ai.Complete(ctx, netClient(writingTimeout), route, instructions, text)
	if err != nil {
		return "", errors.New(netErrorText(err))
	}
	return answer, nil
}

// completionRequest is ai_complete's input (src/platform/bindings/ai.ts
// CompletionRequest). Its docPath and workspaceRoot pick the project whose
// style profile and library a completion follows.
type completionRequest struct {
	Provider      string              `json:"provider"`
	Before        string              `json:"before"`
	After         string              `json:"after"`
	Style         *string             `json:"style"`
	Model         *string             `json:"model"`
	Hints         *ai.CompletionHints `json:"hints"`
	DocPath       *string             `json:"docPath"`
	WorkspaceRoot *string             `json:"workspaceRoot"`
}

// grammarRequest is ai_grammar_check's input (src/platform/bindings/ai.ts
// GrammarRequest). The document and project folder pick the conventions
// profile and the library names the rules check; the UI language is the
// explanations'.
type grammarRequest struct {
	Provider      string  `json:"provider"`
	Paragraph     string  `json:"paragraph"`
	Strictness    *string `json:"strictness"`
	Model         *string `json:"model"`
	DocPath       *string `json:"docPath"`
	WorkspaceRoot *string `json:"workspaceRoot"`
	UILanguage    *string `json:"uiLanguage"`
}

func init() {
	// The words to insert at the cursor, cleaned and joined to what was
	// typed; candidates come separated by ai.CandidateSeparator lines.
	command("ai_complete", func(c *Call, a struct {
		Request completionRequest `json:"request"`
	}) (string, error) {
		if err := c.app.ensureAIOn(); err != nil {
			return "", err
		}
		r := a.Request
		var hints ai.CompletionHints
		if r.Hints != nil {
			hints = *r.Hints
		}
		instructions, text := ai.CompletionPrompt(r.Before, r.After, hints, deref(r.Style))
		// The project's style profile and the library entries the text
		// mentions, as the agent gets them.
		ws := c.app.cachedWorkspace(deref(r.DocPath), deref(r.WorkspaceRoot))
		instructions = ai.WritingContext(instructions, &ws, r.Before, r.After)
		raw, err := c.app.complete(c.ctx, r.Provider, deref(r.Model), instructions, text)
		if err != nil {
			return "", err
		}
		return ai.FinishSuggestion(r.Before, r.After, raw, hints), nil
	})
	// The paragraph's issues, each placed exactly, with the rules engine's
	// findings the model missed.
	command("ai_grammar_check", func(c *Call, a struct {
		Request grammarRequest `json:"request"`
	}) ([]ai.GrammarIssue, error) {
		if err := c.app.ensureAIOn(); err != nil {
			return nil, err
		}
		r := a.Request
		ws := c.app.cachedWorkspace(deref(r.DocPath), deref(r.WorkspaceRoot))
		conv, library := c.app.conventionsFor(&ws), ai.LibraryNames(ws.Library)
		strictness, ui := deref(r.Strictness), deref(r.UILanguage)
		check := func() ([]ai.GrammarIssue, error) {
			raw, err := c.app.complete(c.ctx, r.Provider, deref(r.Model), ai.GrammarInstructions(strictness, ui), ai.GrammarUserText(r.Paragraph))
			if err != nil {
				return nil, err
			}
			return ai.FinishGrammar(raw, r.Paragraph, strictness, conv, library, ui)
		}
		// One retry on a malformed reply: the cheap default model now and
		// then answers something that isn't the issue array, and a second
		// sample almost always parses.
		issues, err := check()
		if errors.Is(err, ai.ErrNoIssues) {
			return check()
		}
		return issues, err
	})
	// A short title for a document from its first few thousand characters,
	// or null; the frontend falls back to the first line.
	command("suggest_doc_title", func(c *Call, a struct {
		Provider string  `json:"provider"`
		Model    *string `json:"model"`
		Excerpt  string  `json:"excerpt"`
	}) (*string, error) {
		if err := c.app.ensureAIOn(); err != nil {
			return nil, err
		}
		if strings.TrimSpace(a.Excerpt) == "" {
			return nil, nil
		}
		answer, err := c.app.complete(c.ctx, a.Provider, deref(a.Model), ai.TitleInstructions, ai.TitleInput(a.Excerpt))
		if err != nil {
			return nil, err
		}
		if title, ok := ai.Title(answer); ok {
			return &title, nil
		}
		return nil, nil
	})
	// The messages the writer will probably send next, most likely first;
	// none when there is no clear next step.
	command("suggest_next_message", func(c *Call, a struct {
		Provider string      `json:"provider"`
		Model    *string     `json:"model"`
		Exchange ai.Exchange `json:"exchange"`
	}) ([]string, error) {
		if err := c.app.ensureAIOn(); err != nil {
			return nil, err
		}
		if len(a.Exchange.Messages) == 0 || strings.TrimSpace(a.Exchange.Reply) == "" {
			return []string{}, nil
		}
		answer, err := c.app.complete(c.ctx, a.Provider, deref(a.Model), ai.NextMessageInstructions, ai.NextMessageInput(a.Exchange))
		if err != nil {
			return nil, err
		}
		return ai.Suggestions(answer), nil
	})
}
