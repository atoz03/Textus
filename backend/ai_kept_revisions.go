package backend

import (
	"encoding/json/v2"
	"fmt"
	"os"
	"strings"
)

// What waits in a file the agent changes: the frontend's stores of the
// revisions Textus AI kept for it (file-revisions.ts) and of those placed
// in its editor, which the file tools read so a change builds on a
// revision kept before, or holds off while the user decides.

// changesSuffix marks the frontend's store of the changes Textus AI kept as
// revisions for a file it didn't have open.
const changesSuffix = "#ai-changes"

// noteMost is the most of a file's revised text said after read_file's
// own excerpt.
const noteMost = 40_000

// keptStore is a store kept for file (suffix marks which), as JSON; nil
// where there is none.
func keptStore(data, file, suffix string) map[string]any {
	kept, ok := keptFile(file+suffix, "", data)
	if !ok {
		return nil
	}
	raw, err := os.ReadFile(kept)
	if err != nil {
		return nil
	}
	var store map[string]any
	if json.Unmarshal(raw, &store) != nil {
		return nil
	}
	return store
}

// listOf is store[key] when it is a list.
func listOf(store map[string]any, key string) []any {
	list, _ := store[key].([]any)
	return list
}

func fieldOf(item any, key string) any {
	m, _ := item.(map[string]any)
	return m[key]
}

// pendingText is file with the change Textus AI kept as a revision there
// and not yet placed in its editor: what a later change builds on.
func pendingText(data, file string) (string, bool) {
	incoming := listOf(keptStore(data, file, changesSuffix), "incoming")
	for i := len(incoming) - 1; i >= 0; i-- {
		if conflict, _ := fieldOf(incoming[i], "conflict").(bool); conflict {
			continue
		}
		after, ok := fieldOf(incoming[i], "after").(string)
		return after, ok
	}
	return "", false
}

// hasUndecided reports whether file's editor holds revisions of the AI's
// the user hasn't decided on.
func hasUndecided(data, file string) bool {
	return len(listOf(keptStore(data, file, ""), "proposals")) > 0
}

// waitingNote is what Textus AI left waiting in file, said to the model
// after the file's text: its own revisions the user hasn't decided, and
// changes that met other edits; "" where nothing waits.
func waitingNote(data, file string) string {
	var parts []string
	if text, ok := pendingText(data, file); ok {
		if len([]rune(text)) > noteMost {
			text = charPrefixOf(text, noteMost) + "\n… (cut here; read_file the lines you need)"
		}
		parts = append(parts, "Your revision to this file waits for the user to accept or reject it; the text above is the file on disk. With your revision it reads:\n"+text)
	}
	met := 0
	for _, r := range listOf(keptStore(data, file, changesSuffix), "incoming") {
		if conflict, ok := fieldOf(r, "conflict").(bool); ok && conflict {
			met++
		}
	}
	if met > 0 {
		parts = append(parts, "An earlier revision of yours met other edits to this file and was not placed; the user sees it listed.")
	}
	var undecided []string
	for _, p := range listOf(keptStore(data, file, ""), "proposals") {
		proposal := fieldOf(p, "proposal")
		action, ok := fieldOf(proposal, "action").(string)
		if !ok {
			continue
		}
		text, _ := fieldOf(proposal, "text").(string)
		undecided = append(undecided, fmt.Sprintf("- %s: %s", action, charPrefixOf(text, 400)))
	}
	if len(undecided) > 0 {
		parts = append(parts, "Your revisions in this file the user hasn't decided yet (pending, not in the text above):\n"+strings.Join(undecided, "\n"))
	}
	return strings.Join(parts, "\n\n")
}
