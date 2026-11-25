package ai

import (
	"encoding/json/v2"
	"fmt"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"textus/backend/ai/rules"
)

// What is remembered is decided after each exchange, apart from it: one
// small request sees the messages, the reply and the memory, and answers
// with the changes. A remember tool in the main request was too often
// forgotten.

const (
	// passClipChars are how much of each message and of the reply the pass
	// is shown: what is worth remembering is said in a sentence, not in a
	// pasted chapter.
	passClipChars = 1_500
	// recentMessages are the user's messages shown, newest last: enough for
	// a correction to be read with what it corrects.
	recentMessages = 6
)

// PassInstructions is what the pass is told - the same for every
// provider.
const PassInstructions = `You keep the memory of Textus AI, a writing assistant: what should still hold in the user's later conversations. You are shown the newest exchange of a conversation - the user's messages (the last one is new) and the assistant's reply - and what is already remembered. Answer with the changes to make, as JSON only.

What to keep:
- requirement: something the user wants always or never done ("from now on", "always", "never", a rule of their style guide or house style).
- preference: what they like or dislike in replies and edits - said outright, or in a correction that states a general taste ("too flowery, I like it plain"), or in the same correction asked again.
- style: a trait of how they write.
- fact: about their work - a person, place or term and what is true of it, with its name and other names it goes by - even told in passing as background to a request ("Lin is the heroine, a lighthouse keeper - write the next scene").
What not to keep: a task, however large or however it is put ("translate the whole text into English", "write a story with two pictures", "note the hard words") - a task is done, not remembered; a request about this text only ("make this more formal", "shorter", "add a line about X"), what the document already says, the assistant's own suggestions, anything private (phone numbers, addresses, health, money, passwords). When in doubt, keep nothing: most exchanges change nothing.

Where it goes (scope): personal - about the user, for everything they write (how to reply to them, their habits and tastes); project - about this work (its people and terms, its spelling, voice, tense, audience, format). "I don't like…", "reply to me in…", "from now on…" are personal unless they name the work; "in this book…", "our product…", "our style guide…" are project. A fact is always the project's. Without a project folder, keep only what is personal - nothing about this work.

Against what is remembered: what is already there needs nothing. A change to or correction of a memory updates it (its id). When the user takes one back ("forget…", "X is fine again"), forget it (its id). A fact about someone or something already in the library uses that entry's name. A fact that changes partway through the work - from a chapter, a scene or a date on - says from where, in the text itself ("From chapter 4 on, Lin trusts the stranger."), so it is added to what held before rather than written over it; a correction of what was wrong all along has no "from".

Write each memory as what holds, not as the user said it: one or a few plain sentences, in the language the user writes to you in, stating a fact about the user or about the work - "Prefers replies in English.", "Writes English with British spelling.", "Internal documents use 你, never 您." Never the user's words as an order to you ("from now on…", "don't…"), never the request that showed it, nothing about this conversation. A fact is about one person, place or thing of the work, under its name, its body what is true of it ("A lighthouse keeper; the heroine.").

Answer with exactly this JSON and nothing else:
{"changes": [{"op": "add" | "update" | "forget", "id": "<the id, for update and forget>", "kind": "requirement" | "preference" | "style" | "fact", "scope": "personal" | "project", "name": "<for a fact>", "aliases": ["<for a fact>"], "text": "<the memory>"}]}
- {"changes": []} when nothing should change.`

// MemorySource is where something is remembered from, for the record: the
// conversation as ai_agent_message gets it and the document open in it
// (MemorySource in src/platform/bindings/ai-memory.ts).
type MemorySource struct {
	Conversation *string `json:"id"`
	Title        *string `json:"title"`
	Document     *string `json:"-"`
}

// Remembering is what a remember call did, which the chat's chip shows and
// undoes; Previous is the file as it was before an update, for Undo to put
// back (Remembering in src/platform/bindings/ai-memory.ts).
type Remembering struct {
	Remembered string  `json:"remembered"`
	Scope      Scope   `json:"scope"`
	Created    bool    `json:"created"`
	Text       string  `json:"text"`
	Previous   *string `json:"previous"`
	// Name is, for a library entry, whom or what it is about, which Text
	// (its body) doesn't say.
	Name *string `json:"name,omitzero"`
	// Forgot: what was remembered was taken back - Text is what it said,
	// Previous the file Undo puts back.
	Forgot bool `json:"forgot,omitzero"`
}

// RememberArgs is what to remember: a change the pass asked for, or one
// the user asked for with a click.
type RememberArgs struct {
	Kind     string
	Scope    *string
	Text     string
	Name     *string
	Aliases  []string
	Replaces *string
}

// MemoryChange is one change the pass (or a tidy) asked for.
type MemoryChange struct {
	Op      string   `json:"op"`
	ID      *string  `json:"id"`
	Kind    string   `json:"kind"`
	Scope   *string  `json:"scope"`
	Text    string   `json:"text"`
	Name    *string  `json:"name"`
	Aliases []string `json:"aliases"`
	// Into is a tidy's forget: the memory that now says it, or that
	// overturns it.
	Into *string `json:"into"`
}

func trimmedID(id *string) string {
	if id == nil {
		return ""
	}
	return strings.TrimSpace(*id)
}

// TheID is the change's id, trimmed; "" for none.
func (c MemoryChange) TheID() string { return trimmedID(c.ID) }

// MergedInto is the memory a tidy's forget goes into; "" for none.
func (c MemoryChange) MergedInto() string { return trimmedID(c.Into) }

// TheOp is the change's op, trimmed.
func (c MemoryChange) TheOp() string { return strings.TrimSpace(c.Op) }

// ParseChanges are the changes in the pass's answer - the JSON object in
// it, however it is wrapped (a code fence, a sentence before it). Nothing
// when there is none to read.
func ParseChanges(answer string) []MemoryChange {
	var a struct {
		Changes []MemoryChange `json:"changes"`
	}
	if !JSONObject(answer, &a) {
		return nil
	}
	return a.Changes
}

// Listed is a memory as the pass and the tidy are shown it, with the id
// they answer with.
func Listed(m Memory) string {
	return fmt.Sprintf("- [%s] (%s, %s) %s", m.ID, m.Scope, m.Kind, m.Text)
}

// PassInput is what the pass is shown: what is remembered (with ids), the
// library entries the exchange names, whether there is a project, the
// exchange.
func PassInput(memories Memories, library []LibraryEntry, project bool, exchange Exchange) string {
	var remembered []string
	for _, m := range memories.Items {
		remembered = append(remembered, Listed(m))
	}
	said := strings.Join(exchange.Messages, "\n") + "\n" + exchange.Reply
	var entries []string
	for _, e := range library {
		if !slices.ContainsFunc(append([]string{e.Name}, e.Aliases...), func(n string) bool { return strings.Contains(said, n) }) {
			continue
		}
		whose := " - the user's own entry: never update or forget it"
		if e.Remembered != nil {
			whose = fmt.Sprintf(" [project/library/%s]", fileStemOf(e.File))
		}
		aliases := ""
		if len(e.Aliases) > 0 {
			aliases = " (also " + strings.Join(e.Aliases, ", ") + ")"
		}
		entries = append(entries, fmt.Sprintf("- %s%s%s: %s", e.Name, aliases, whose, Clip(e.Body, passClipChars)))
	}
	recent := max(len(exchange.Messages)-recentMessages, 0)
	var messages []string
	for i, m := range exchange.Messages[recent:] {
		messages = append(messages, fmt.Sprintf("%d. %s", i+1, Clip(m, passClipChars)))
	}
	join := func(lines []string) string {
		if len(lines) == 0 {
			return "(none)"
		}
		return strings.Join(lines, "\n")
	}
	folder := "none - keep only what is personal"
	if project {
		folder = "yes"
	}
	return fmt.Sprintf("---already remembered---\n%s\n\n---library entries the exchange names---\n%s\n\n---project folder---\n%s\n\n---the user's messages (oldest first; the last is new)---\n%s\n\n---the assistant's reply to the last one---\n%s",
		join(remembered), join(entries), folder, join(messages), Clip(exchange.Reply, passClipChars))
}

// fileStemOf is a path's last part without its extension.
func fileStemOf(path string) string {
	base := path[strings.LastIndexAny(path, `/\`)+1:]
	if i := strings.LastIndexByte(base, '.'); i > 0 {
		return base[:i]
	}
	return base
}

// TooShortAnAlias reports whether alias is too short to stand for a name:
// one CJK character (晚 for 林晚 would match every 晚上), or one or two
// letters.
func TooShortAnAlias(alias string) bool {
	chars := utf8.RuneCountInString(alias)
	unspaced := strings.ContainsFunc(alias, func(c rune) bool {
		return c >= 0x3040 && c <= 0x30ff || c >= 0x3400 && c <= 0x9fff
	})
	return chars == 0 || unspaced && chars < 2 || !unspaced && chars < 3
}

// Normalized is what a duplicate check compares: the letters and digits,
// lowercased, with full-width forms folded - "Keep it short." and "keep it
// short" are one memory.
func Normalized(text string) string {
	var out strings.Builder
	for _, c := range text {
		c = unicode.ToLower(NormalizeChar(c))
		if rules.IsAlphanumeric(c) {
			out.WriteRune(c)
		}
	}
	return out.String()
}

// OneLine is text with its whitespace runs one space.
func OneLine(text string) string { return strings.Join(strings.Fields(text), " ") }

// MemorySections are the prompt sections for what Textus AI remembered:
// personal first, then the project's, which wins a conflict. Facts go with
// the library instead.
func MemorySections(memories Memories) []string {
	lines := func(scope Scope) []string {
		var out []string
		for _, m := range memories.Items {
			if m.Scope == scope && m.Included {
				out = append(out, fmt.Sprintf("- (%s) %s", m.Kind, m.Text))
			}
		}
		return out
	}
	var sections []string
	personal, project := lines(ScopePersonal), lines(ScopeProject)
	if len(personal) > 0 {
		sections = append(sections, "---what you remember about the user (personal, from earlier conversations; the user instructions above win where they conflict)---\n"+strings.Join(personal, "\n"))
	}
	if len(project) > 0 {
		wins := ""
		if len(personal) > 0 {
			wins = "where these conflict with the personal memories above, follow these; "
		}
		sections = append(sections, "---what you remember about this project ("+wins+"the user instructions above win where they conflict)---\n"+strings.Join(project, "\n"))
	}
	leftOut := 0
	for _, m := range memories.Items {
		if !m.Included {
			leftOut++
		}
	}
	if leftOut > 0 {
		sections = append(sections, fmt.Sprintf("(%d older memories don't fit here and are not shown.)", leftOut))
	}
	return sections
}

// JSONObject reads the JSON object in reply - from its first `{` to its
// last `}` - into v; false when there is none, or it isn't one. Every
// request that asks a model for a JSON answer in plain text reads it here.
func JSONObject(reply string, v any) bool { return jsonBetween(reply, '{', '}', v) }

// JSONArray reads the JSON array in reply - from its first `[` to its last
// `]` - into v.
func JSONArray(reply string, v any) bool { return jsonBetween(reply, '[', ']', v) }

func jsonBetween(reply string, open, close byte, v any) bool {
	start, end := strings.IndexByte(reply, open), strings.LastIndexByte(reply, close)
	if start < 0 || end < start {
		return false
	}
	return json.Unmarshal([]byte(reply[start:end+1]), v) == nil
}
