package ai

import (
	"cmp"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"

	"textus/backend/ai/aicompat"
)

// Edits and comments travel as lists (one propose_edit call with `edits`,
// one add_comment with `comments`), so a service that holds parallel calls
// back still streams each item. Past the model they are split into one
// call per item (`<call>#<n>`), and joined again in the history sent back.

// listKey is the list a tool's calls carry their items in, "" for a tool
// without one.
func listKey(tool string) string {
	switch tool {
	case ProposeEditToolName:
		return "edits"
	case AddCommentToolName:
		return "comments"
	}
	return ""
}

// itemID is the id of a list call's nth item.
func itemID(callID string, n int) string { return fmt.Sprintf("%s#%d", callID, n) }

// listOf is a list item's call id as its list's and its place in it.
func listOf(item string) (string, int, bool) {
	at := strings.LastIndexByte(item, '#')
	if at < 0 {
		return "", 0, false
	}
	n, err := strconv.ParseUint(item[at+1:], 10, 32)
	if err != nil {
		return "", 0, false
	}
	return item[:at], int(n), true
}

// listItems is a list call's items, false when it carries no list.
func listItems(name, arguments string) ([]jsontext.Value, bool) {
	key := listKey(name)
	if key == "" {
		return nil, false
	}
	args, ok := ParseArgs(arguments)
	if !ok {
		return nil, false
	}
	raw, ok := args.Raw(key)
	if !ok || raw.Kind() != '[' {
		return nil, false
	}
	var items []jsontext.Value
	if err := json.Unmarshal(raw, &items); err != nil || len(items) == 0 {
		return nil, false
	}
	for i := range items {
		items[i] = items[i].Clone()
		_ = items[i].Compact()
	}
	return items, true
}

// SplitCalls is a step's list calls as one call per item. A call without
// its list stays as it came, for its tool to answer.
func SplitCalls(result aicompat.StepResult) aicompat.StepResult {
	if result.Done {
		return result
	}
	out := make([]aicompat.AgentTurn, 0, len(result.Calls))
	for _, t := range result.Calls {
		items, ok := listItems(t.Name, t.Arguments)
		if t.Kind != aicompat.ToolCalled || !ok {
			out = append(out, t)
			continue
		}
		for n, item := range items {
			call := aicompat.AgentTurn{Kind: aicompat.ToolCalled, CallID: itemID(t.CallID, n), Name: t.Name, Arguments: string(item)}
			// The reasoning before the call goes with it once.
			if n == 0 {
				call.Reasoning = t.Reasoning
			}
			out = append(out, call)
		}
	}
	result.Calls = out
	return result
}

// JoinCalls is history for the model: each list's item calls joined into
// the call the model made, at the first item's place, and their results
// into one, at the first result's - each item's numbered when there are
// several.
func JoinCalls(turns []aicompat.AgentTurn) []aicompat.AgentTurn {
	type part struct {
		n    int
		turn aicompat.AgentTurn
	}
	items := map[string][]part{}
	results := map[string][]part{}
	for _, t := range turns {
		list, n, ok := listOf(t.CallID)
		switch {
		case !ok:
		case t.Kind == aicompat.ToolCalled && listKey(t.Name) != "":
			items[list] = append(items[list], part{n, t})
		case t.Kind == aicompat.ToolResult:
			results[list] = append(results[list], part{n, t})
		}
	}
	if len(items) == 0 {
		return turns
	}
	lists := map[string]bool{}
	for list := range items {
		lists[list] = true
	}
	joined := func(id string) (string, bool) {
		list, _, ok := listOf(id)
		return list, ok && lists[list]
	}
	byPlace := func(a, b part) int { return cmp.Compare(a.n, b.n) }
	out := make([]aicompat.AgentTurn, 0, len(turns))
	for _, t := range turns {
		list, ok := joined(t.CallID)
		switch {
		case !ok || (t.Kind != aicompat.ToolCalled && t.Kind != aicompat.ToolResult):
			out = append(out, t)
		case t.Kind == aicompat.ToolCalled:
			parts, first := items[list]
			if !first {
				continue // joined at its first item
			}
			delete(items, list)
			slices.SortStableFunc(parts, byPlace)
			values := make([]jsontext.Value, len(parts))
			for i, p := range parts {
				values[i] = jsontext.Value("null")
				if jsontext.Value(p.turn.Arguments).IsValid() {
					values[i] = jsontext.Value(p.turn.Arguments)
				}
			}
			key := listKey(t.Name)
			if key == "" {
				key = "items"
			}
			arguments := NewArgs()
			arguments.Set(key, values)
			out = append(out, aicompat.AgentTurn{Kind: aicompat.ToolCalled, CallID: list, Name: t.Name, Arguments: arguments.String(), Reasoning: t.Reasoning})
		default:
			parts, first := results[list]
			if !first {
				continue
			}
			delete(results, list)
			slices.SortStableFunc(parts, byPlace)
			var images []aicompat.ImageAttachment
			outputs := make([]string, len(parts))
			for i, p := range parts {
				images = append(images, p.turn.Images...)
				outputs[i] = p.turn.Output
				if len(parts) > 1 {
					outputs[i] = fmt.Sprintf("%d. %s", p.n+1, p.turn.Output)
				}
			}
			out = append(out, aicompat.AgentTurn{Kind: aicompat.ToolResult, CallID: list, Output: strings.Join(outputs, "\n"), Images: images})
		}
	}
	return out
}

// listScan is where a list call's streaming arguments are: the JSON
// scanned so far, for the item now open.
type listScan struct {
	tool, key string
	// stack is the nesting: '{' and '[' still open.
	stack            []rune
	inString, escape bool
	// lastKey is the last string closed at the top level - the key a value
	// follows.
	lastKey string
	str     strings.Builder
	// inList: inside the list's array.
	inList bool
	// open is the item being written ("" for none), items how many were.
	open  string
	items int
}

// ListStream splits a list call's streaming arguments into one call per
// item as they arrive: a ToolStart as an item opens, its JSON as ToolArgs,
// so a reader of the stream sees each edit or comment as a call of its
// own.
type ListStream struct {
	mu    sync.Mutex
	calls map[string]*listScan
}

// Feed passes event on to out, a list call's split into its items.
func (s *ListStream) Feed(e aicompat.ProviderEvent, out aicompat.EventSink) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.calls == nil {
		s.calls = map[string]*listScan{}
	}
	switch e.Kind {
	case aicompat.EventToolStart:
		if key := listKey(e.Name); key != "" {
			s.calls[e.CallID] = &listScan{tool: e.Name, key: key}
			return
		}
	case aicompat.EventToolArgs:
		if scan, ok := s.calls[e.CallID]; ok {
			for _, p := range scan.feed(e.CallID, e.Text) {
				if p.opened {
					out(aicompat.ProviderEvent{Kind: aicompat.EventToolStart, CallID: p.id, Name: scan.tool})
				}
				out(aicompat.ProviderEvent{Kind: aicompat.EventToolArgs, CallID: p.id, Text: p.piece})
			}
			return
		}
	}
	out(e)
}

// listPiece is a piece of delta that belongs to an item: the item's id,
// its JSON, and whether the item opens here.
type listPiece struct {
	id, piece string
	opened    bool
}

func (s *listScan) feed(callID, delta string) []listPiece {
	var pieces []listPiece
	var piece strings.Builder
	opened := false
	for _, ch := range delta {
		inItem := s.open != ""
		if s.inString {
			if inItem {
				piece.WriteRune(ch)
			} else {
				s.str.WriteRune(ch)
			}
			switch {
			case s.escape:
				s.escape = false
			case ch == '\\':
				s.escape = true
			case ch == '"':
				s.inString = false
				if !inItem && len(s.stack) == 1 {
					key := s.str.String()
					s.lastKey = key[:len(key)-1]
				}
			}
			continue
		}
		switch ch {
		case '"':
			s.inString = true
			s.str.Reset()
			if inItem {
				piece.WriteRune(ch)
			}
		case '{', '[':
			if s.inList && len(s.stack) == 2 && ch == '{' {
				// The one before closed at its '}', its piece out.
				s.open = itemID(callID, s.items)
				s.items++
				opened = true
			}
			if ch == '[' && len(s.stack) == 1 && s.lastKey == s.key {
				s.inList = true
			}
			s.stack = append(s.stack, ch)
			if s.open != "" {
				piece.WriteRune(ch)
			}
		case '}', ']':
			if len(s.stack) > 0 {
				s.stack = s.stack[:len(s.stack)-1]
			}
			if s.open != "" {
				piece.WriteRune(ch)
			}
			if s.inList && len(s.stack) == 2 && ch == '}' && s.open != "" {
				pieces = append(pieces, listPiece{s.open, piece.String(), opened})
				piece.Reset()
				s.open, opened = "", false
			}
			if ch == ']' && len(s.stack) == 1 {
				s.inList = false
			}
		default:
			if s.open != "" {
				piece.WriteRune(ch)
			}
		}
	}
	if s.open != "" && (piece.Len() > 0 || opened) {
		pieces = append(pieces, listPiece{s.open, piece.String(), opened})
	}
	return pieces
}
