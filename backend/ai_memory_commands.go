package backend

import (
	"encoding/json/v2"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"textus/backend/ai"
)

// The memory commands: the pass after an exchange, the memory tidied now
// and then, a skill offered, and what the Memory tab does - restore, undo,
// move, delete, remember.

// tidyMarker is where the last tidy is kept: the personal memory folder,
// else the project's.
func (d memoryDirs) tidyMarker() (string, bool) {
	dir, ok := d.folder(ai.ScopePersonal, memoryFolder)
	if !ok {
		dir, ok = d.folder(ai.ScopeProject, memoryFolder)
	}
	return filepath.Join(dir, ai.TidyMarker), ok
}

// lastTidy is the last tidy, nil for none (or a marker that doesn't say).
func (d memoryDirs) lastTidy() *ai.LastTidy {
	path, ok := d.tidyMarker()
	if !ok {
		return nil
	}
	marker, err := readJSONStore[struct {
		At    *uint64 `json:"at"`
		Count *int    `json:"count"`
	}](path)
	if err != nil || marker.At == nil || marker.Count == nil {
		return nil
	}
	return &ai.LastTidy{At: *marker.At, Count: *marker.Count}
}

// markTidied keeps a tidy at at, after which there were count memories.
func (d memoryDirs) markTidied(count int, at uint64) {
	path, ok := d.tidyMarker()
	if !ok {
		return
	}
	raw, err := json.Marshal(ai.LastTidy{At: at, Count: count})
	if err == nil && os.MkdirAll(filepath.Dir(path), 0o755) == nil {
		_ = writeFileAtomic(path, raw)
	}
}

// tidyIfDue is a tidy, when one is due: asked through call, its changes
// made, the marker moved on. Nothing when none is due.
func (d memoryDirs) tidyIfDue(source ai.MemorySource, call modelCall) ([]ai.Remembering, error) {
	memories := d.load()
	at := uint64(time.Now().Unix())
	if !ai.TidyDue(len(memories.Items), d.lastTidy(), at) {
		return nil, nil
	}
	answer, err := call(ai.TidyInstructions, ai.TidyInput(memories))
	if err != nil {
		return nil, err
	}
	made := d.applyChanges(ai.TidyChanges(answer, memories), source, memoryTimestamp(time.Now()))
	d.markTidied(len(d.load().Items), at)
	return made, nil
}

// saveOfferIn writes offer into skillsDir, never over a skill already
// there (a number is added to its file's name instead), and answers its
// path.
func saveOfferIn(skillsDir string, offer ai.SkillOffer, modes []ai.AgentMode, confirmed bool) (string, error) {
	if len(modes) == 0 {
		return "", errors.New("A skill needs at least one mode.")
	}
	// Checked as a skill a reply wrote is: the model wrote this one too.
	if warnings := ai.SkillWarnings(offer.Description+"\n"+offer.Instructions, modes); !confirmed && len(warnings) > 0 {
		return "", errors.New(ai.SkillNeedsConfirm + ":" + strings.Join(warnings, ","))
	}
	return writeSkillFile(skillsDir, offer.Name, takenNextFree, func(name string) string {
		named := offer
		named.Name = name
		return ai.WithMetadata(ai.OfferedSkillFile(named), "modes", modeWords(modes))
	})
}

// passRequest is memory_pass's input (PassRequest in
// src/platform/bindings/ai-memory.ts); ai_offer_skill's (OfferRequest) is
// the same without the conversation.
type passRequest struct {
	Provider      string           `json:"provider"`
	Model         *string          `json:"model"`
	DocPath       *string          `json:"docPath"`
	WorkspaceRoot *string          `json:"workspaceRoot"`
	Conversation  *ai.MemorySource `json:"conversation"`
	Exchange      ai.Exchange      `json:"exchange"`
}

// callModel asks provider's model through c, as the pass and the offer do.
func callModel(c *Call, provider string, model *string) modelCall {
	return func(instructions, input string) (string, error) {
		return c.app.complete(c.ctx, provider, deref(model), instructions, input)
	}
}

// memoryCall names a memory of a document's (or of none's), as the Memory
// tab's commands do.
type memoryCall struct {
	DocPath       *string `json:"docPath"`
	WorkspaceRoot *string `json:"workspaceRoot"`
	ID            string  `json:"id"`
}

func (m memoryCall) dirs(c *Call) memoryDirs {
	return c.app.memoryDirsFor(deref(m.DocPath), deref(m.WorkspaceRoot))
}

func init() {
	// After each exchange: what to remember, update or forget, decided by
	// the conversation's own model, each change for a chip under the
	// reply. Nothing while Remember automatically is off.
	command("memory_pass", func(c *Call, a struct {
		Request passRequest `json:"request"`
	}) ([]ai.Remembering, error) {
		r := a.Request
		if err := c.app.ensureAIOn(); err != nil {
			return nil, err
		}
		if !c.app.readFlag("rememberAutomatically") {
			return []ai.Remembering{}, nil
		}
		dirs := c.app.memoryDirsFor(deref(r.DocPath), deref(r.WorkspaceRoot))
		var source ai.MemorySource
		if r.Conversation != nil {
			source = *r.Conversation
		}
		source.Document = r.DocPath
		call := callModel(c, r.Provider, r.Model)
		made, err := dirs.runPass(r.Exchange, source, call)
		if err != nil {
			return nil, err
		}
		// Now and then, the whole memory tidied. Its failing takes nothing
		// from the exchange's own changes.
		tidied, _ := dirs.tidyIfDue(source, call)
		return append(made, tidied...), nil
	})
	// After an exchange: its request offered as a skill to save, when it
	// was a procedure of its own.
	command("ai_offer_skill", func(c *Call, a struct {
		Request passRequest `json:"request"`
	}) (*ai.SkillOffer, error) {
		r := a.Request
		if err := c.app.ensureAIOn(); err != nil {
			return nil, err
		}
		if !ai.MayOffer(r.Exchange) {
			return nil, nil
		}
		skills := c.app.workspace(deref(r.DocPath), deref(r.WorkspaceRoot)).Skills
		answer, err := callModel(c, r.Provider, r.Model)(ai.OfferInstructions, ai.OfferInput(r.Exchange, skills))
		if err != nil {
			return nil, err
		}
		return ai.ParseOffer(answer, skills), nil
	})
	// The offered skill saved, for every document ("personal") or this
	// project's; its file's path.
	command("save_offered_skill", func(c *Call, a struct {
		Offer         ai.SkillOffer  `json:"offer"`
		Scope         string         `json:"scope"`
		DocPath       *string        `json:"docPath"`
		WorkspaceRoot *string        `json:"workspaceRoot"`
		Modes         []ai.AgentMode `json:"modes"`
		Confirmed     bool           `json:"confirmed"`
	}) (string, error) {
		scope, ok := ai.ParseScope(a.Scope)
		if !ok {
			return "", errors.New("Unknown scope.")
		}
		source := ai.SourceGlobal
		if scope == ai.ScopeProject {
			source = ai.SourceProject
		}
		dir, err := c.app.skillsDir(source, deref(a.DocPath), deref(a.WorkspaceRoot))
		if err != nil {
			return "", err
		}
		return saveOfferIn(dir, a.Offer, a.Modes, a.Confirmed)
	})
	// Restore in the Memory tab's Recently forgotten: a forgotten memory or
	// library entry put back; its id.
	command("memory_restore", func(c *Call, a memoryCall) (string, error) {
		return a.dirs(c).restore(a.ID)
	})
	// The chat chip's Undo: what a remember call created deleted, or
	// `previous`, the file before it was updated, written back.
	command("memory_undo", func(c *Call, a struct {
		memoryCall
		Previous *string `json:"previous"`
	}) (nothing, error) {
		return nil, a.dirs(c).undo(a.ID, a.Previous)
	})
	// "Make personal" / "Make project", in the chat chip and the Memory
	// tab; the memory's new id.
	command("memory_move", func(c *Call, a struct {
		memoryCall
		Scope string `json:"scope"`
	}) (string, error) {
		scope, ok := ai.ParseScope(a.Scope)
		if !ok {
			return "", errors.New("Unknown scope.")
		}
		return a.dirs(c).moveTo(a.ID, scope)
	})
	// The Memory tab's Delete, for a memory or a library entry.
	command("memory_delete", func(c *Call, a memoryCall) (nothing, error) {
		return nil, a.dirs(c).delete(a.ID)
	})
	// "Remember" on a suggestion learned from revision choices: one memory
	// written as the pass writes one. A click, not the model, so it saves
	// even while Remember automatically is off.
	command("memory_remember", func(c *Call, a struct {
		DocPath       *string `json:"docPath"`
		WorkspaceRoot *string `json:"workspaceRoot"`
		Kind          string  `json:"kind"`
		Scope         string  `json:"scope"`
		Text          string  `json:"text"`
	}) (ai.Remembering, error) {
		dirs := c.app.memoryDirsFor(deref(a.DocPath), deref(a.WorkspaceRoot))
		return dirs.keep(a.Kind, a.Scope, a.Text, ai.MemorySource{Document: a.DocPath})
	})
}
