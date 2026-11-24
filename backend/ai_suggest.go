package backend

import (
	"context"
	"errors"
	"strings"
	"time"

	"textus/backend/ai"
)

// The short writing requests: one call to the writing model, no tools and
// no stream, for a title for an untitled document and the messages the
// writer will probably send next.

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

func init() {
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
