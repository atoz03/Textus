package backend

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"textus/backend/ai"
	"textus/backend/ai/aicompat"
	"textus/backend/plugins"
)

// textus.ai.complete and textus.ai.stream: a plugin that is on, with the
// ai.model permission, asking the user's writing model; what it used this
// month is counted in plugins/ai.json, where the plugin can't reach it.

func (a *App) aiUsagePath() string { return filepath.Join(a.pluginsRoot(), "ai.json") }

// thisMonth is "2026-11", the month counts are kept for.
func thisMonth() string { return time.Now().Format("2006-01") }

// aiUsage is plugins/ai.json as it stands this month; a file that doesn't
// parse set aside.
func (a *App) aiUsage() plugins.AIUsage {
	var usage plugins.AIUsage
	if raw, err := os.ReadFile(a.aiUsagePath()); err == nil {
		if err := json.Unmarshal(raw, &usage); err != nil {
			log.Printf("textus: %s is not valid (%v); moving it aside", a.aiUsagePath(), err)
			moveAside(a.aiUsagePath())
			usage = plugins.AIUsage{}
		}
	}
	return usage.Fresh(thisMonth())
}

// updateAIUsage reads the usage, changes it and writes it back, as one step.
func (a *App) updateAIUsage(change func(u *plugins.AIUsage)) error {
	a.plug.aiMu.Lock()
	defer a.plug.aiMu.Unlock()
	usage := a.aiUsage()
	change(&usage)
	return writePrettyJSON(a.aiUsagePath(), usage)
}

// pluginModel is the provider and model a plugin's call goes to, once
// everything allows it: Textus AI on, the plugin running with ai.model,
// and no more than plugins.LongestInput.
func (a *App) pluginModel(plugin, system, prompt string) (string, string, error) {
	if err := plugins.RequireID(plugin); err != nil {
		return "", "", err
	}
	if err := a.ensureAIOn(); err != nil {
		return "", "", err
	}
	s := a.pluginState()
	e := s.Plugins[plugin]
	if !s.AllOn || e == nil || !e.Enabled || !slices.Contains(e.Granted, "ai.model") {
		return "", "", errors.New("This plugin may not use your AI model (ai.model).")
	}
	if plugins.TooLong(system, prompt) {
		return "", "", fmt.Errorf("At most %d characters can be sent.", plugins.LongestInput)
	}
	text := func(raw []byte) string {
		var s string
		_ = json.Unmarshal(raw, &s)
		return s
	}
	provider := text(a.readSetting("writingProvider"))
	if provider == "" {
		provider = text(a.readSetting("aiProvider"))
	}
	if provider == "" {
		return "", "", errors.New(ai.NotConfigured)
	}
	var models map[string]any
	_ = json.Unmarshal(a.readSetting("writingModels"), &models)
	model, _ := models[provider].(string)
	return provider, model, nil
}

// spendPluginAI counts what a call cost: what was sent, and the answer or
// what was written of it before a cancel. A call that failed costs
// nothing.
func (a *App) spendPluginAI(plugin, system, prompt, answer string, err error, soFar string) error {
	written, counted := plugins.Counted(answer, err, soFar)
	if !counted {
		return nil
	}
	spent := plugins.Spent([]string{system, prompt}, written)
	return a.updateAIUsage(func(u *plugins.AIUsage) { u.Entry(plugin).Used += spent })
}

// streamPluginAI streams provider's answer to system and prompt, each
// piece of text to onText as it comes.
func (a *App) streamPluginAI(ctx context.Context, provider, model, system, prompt string, onText func(string)) (string, error) {
	entry, ok := ai.Find(provider)
	if !ok {
		return "", fmt.Errorf("unknown provider: %s", provider)
	}
	agent, handled, err := a.credentials().AgentProvider(provider, model, aicompat.Auto, nil, netClient(0))
	if err != nil {
		return "", err
	}
	if !handled {
		return "", fmt.Errorf("unhandled dialect: %s", entry.Dialect)
	}
	result, _, err := agent.Step(ctx, system, []aicompat.AgentTurn{aicompat.UserTurn(prompt)}, nil, func(e aicompat.ProviderEvent) {
		if e.Kind == aicompat.EventText {
			onText(e.Text)
		}
	})
	if err != nil {
		return "", errors.New(netErrorText(err))
	}
	if !result.Done {
		return "", errors.New("The model asked for a tool it wasn't given.")
	}
	return result.Text, nil
}

// pluginAIRequest is a plugin's prompt for the writing model; cancelId
// stops it (plugin_http_cancel).
type pluginAIRequest struct {
	Plugin   string  `json:"plugin"`
	System   string  `json:"system"`
	Prompt   string  `json:"prompt"`
	CancelID *string `json:"cancelId"`
}

func init() {
	// Whether Textus AI is on, and the plugin's tokens this month.
	command("plugin_ai_status", func(c *Call, a struct {
		Plugin string `json:"plugin"`
	}) (plugins.AIStatus, error) {
		if err := plugins.RequireID(a.Plugin); err != nil {
			return plugins.AIStatus{}, err
		}
		return plugins.StatusOf(c.app.aiUsage().Plugins[a.Plugin], c.app.ensureAIOn() == nil), nil
	})
	// One answer from the user's writing model, whole. Stopped with
	// plugin_http_cancel and cancelId.
	command("plugin_ai_complete", func(c *Call, a pluginAIRequest) (string, error) {
		provider, model, err := c.app.pluginModel(a.Plugin, a.System, a.Prompt)
		if err != nil {
			return "", err
		}
		answer, err := plugins.Cancellable(c.ctx, a.Plugin, a.CancelID, func(ctx context.Context) (string, error) {
			return c.app.complete(ctx, provider, model, a.System, a.Prompt)
		})
		if spendErr := c.app.spendPluginAI(a.Plugin, a.System, a.Prompt, answer, err, ""); spendErr != nil {
			return "", spendErr
		}
		return answer, err
	})
	// The same, its text sent to the window as it comes; the whole answer
	// at the end.
	command("plugin_ai_stream", func(c *Call, a pluginAIRequest) (string, error) {
		provider, model, err := c.app.pluginModel(a.Plugin, a.System, a.Prompt)
		if err != nil {
			return "", err
		}
		var mu sync.Mutex
		var written strings.Builder
		answer, err := plugins.Cancellable(c.ctx, a.Plugin, a.CancelID, func(ctx context.Context) (string, error) {
			return c.app.streamPluginAI(ctx, provider, model, a.System, a.Prompt, func(text string) {
				mu.Lock()
				written.WriteString(text)
				mu.Unlock()
				_ = c.Send(text)
			})
		})
		mu.Lock()
		soFar := written.String()
		mu.Unlock()
		if spendErr := c.app.spendPluginAI(a.Plugin, a.System, a.Prompt, answer, err, soFar); spendErr != nil {
			return "", spendErr
		}
		return answer, err
	})
}
