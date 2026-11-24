package backend

import (
	"encoding/json/jsontext"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"
)

// The AI records a settings file can carry: every saved conversation and
// every memory, the user's own and each project's. An import only adds: a
// conversation comes in when it is new here or newer than this computer's
// copy, a memory when no file of its name is here. Nothing is deleted.

// projectsFolder holds each project's memories in the global folder, by
// the project's key.
const projectsFolder = "projects"

// AiRecords are the conversations and memories of a settings file
// (src/platform/bindings/files.ts AiRecords).
type AiRecords struct {
	// Conversations are whole, turns included, as the chat history keeps them.
	Conversations []jsontext.Value `json:"conversations"`
	Memories      []MemoryFile     `json:"memories"`
}

// MemoryFile is a memory by its path under the global folder:
// memory/<name>.md, or projects/<key>/memory/<name>.md for a project's
// (src/platform/bindings/files.ts MemoryFile).
type MemoryFile struct {
	Path string `json:"path"`
	Text string `json:"text"`
}

// Imported is how many of each an import added
// (src/platform/bindings/files.ts Imported).
type Imported struct {
	Conversations int `json:"conversations"`
	Memories      int `json:"memories"`
}

// globalAgentPath is the global layer's folder: agent.md, skills/,
// memory/ and each project's memories.
func (a *App) globalAgentPath() string { return filepath.Join(a.dirs.Config, globalAgentDir) }

func collectRecords(chat *ChatHistory, global string) AiRecords {
	r := AiRecords{Conversations: []jsontext.Value{}, Memories: []MemoryFile{}}
	for _, id := range chat.ids() {
		if entry := chat.get(id); entry != nil {
			r.Conversations = append(r.Conversations, entry)
		}
	}
	readMemories(global, memoryFolder, &r.Memories)
	projects, _ := os.ReadDir(filepath.Join(global, projectsFolder))
	for _, p := range projects {
		readMemories(global, projectsFolder+"/"+p.Name()+"/"+memoryFolder, &r.Memories)
	}
	return r
}

func readMemories(global, folder string, out *[]MemoryFile) {
	dir := filepath.Join(global, filepath.FromSlash(folder))
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".md") || !e.Type().IsRegular() {
			continue
		}
		if text, err := os.ReadFile(filepath.Join(dir, e.Name())); err == nil && utf8.Valid(text) {
			*out = append(*out, MemoryFile{Path: folder + "/" + e.Name(), Text: string(text)})
		}
	}
}

// memoryTarget is where a memory's path puts it under global: only the two
// shapes a memory has, each part a plain name. A file's path is untrusted.
func memoryTarget(global, path string) (string, bool) {
	parts := strings.Split(path, "/")
	plain := func(s string) bool {
		return s != "" && !strings.HasPrefix(s, ".") && !strings.ContainsAny(s, `\:`) && !strings.ContainsFunc(s, unicode.IsControl)
	}
	shaped := len(parts) == 2 && parts[0] == memoryFolder && plain(parts[1]) ||
		len(parts) == 4 && parts[0] == projectsFolder && plain(parts[1]) && parts[2] == memoryFolder && plain(parts[3])
	if !shaped || !strings.HasSuffix(path, ".md") {
		return "", false
	}
	return filepath.Join(append([]string{global}, parts...)...), true
}

// mergeRecords adds r; it answers the counts and each conversation saved,
// as its index line, for the other windows.
func mergeRecords(chat *ChatHistory, global string, r AiRecords) (Imported, []jsontext.Value) {
	updated := func(entry jsontext.Value) float64 {
		if at := fieldsOf(entry).UpdatedAt; at != nil {
			return *at
		}
		return 0
	}
	var saved []jsontext.Value
	for _, entry := range r.Conversations {
		if !historyChat.valid(entry) {
			continue
		}
		if mine := chat.get(*fieldsOf(entry).ID); mine == nil || updated(entry) > updated(mine) {
			saved = append(saved, chat.save(entry))
		}
	}
	memories := 0
	for _, m := range r.Memories {
		path, ok := memoryTarget(global, m.Path)
		if !ok {
			continue
		}
		if _, err := os.Lstat(path); err == nil {
			continue
		}
		if writeFileAtomic(path, []byte(m.Text)) == nil {
			memories++
		}
	}
	return Imported{Conversations: len(saved), Memories: memories}, saved
}

func init() {
	// Every conversation and memory, for a settings file.
	command("ai_records_export", func(c *Call, _ none) (AiRecords, error) {
		return collectRecords(c.app.stores.chat, c.app.globalAgentPath()), nil
	})
	// Adds a settings file's conversations and memories; every window's
	// history hears of each conversation saved.
	command("ai_records_import", func(c *Call, a struct {
		Records AiRecords `json:"records"`
	}) (Imported, error) {
		imported, saved := mergeRecords(c.app.stores.chat, c.app.globalAgentPath(), a.Records)
		for _, entry := range saved {
			c.app.emitAll("history-changed", historyChanged{Kind: historyChat, Op: historyOp{Type: "upsert", Entry: entry}})
		}
		return imported, nil
	})
}
