package ai

import (
	"cmp"
	"encoding/json/v2"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"
)

// Now and then, the memory tidied: repeats merged and overturned rules
// removed, by one small request once enough was added or a week has
// passed. A removed memory goes to Recently forgotten.

const (
	// tidyFrom: no tidy below this many memories, too few to repeat each
	// other.
	tidyFrom = 8
	// tidyAfterAdded are the memories added since the last tidy that call
	// for another.
	tidyAfterAdded = 6
	// tidyAfterSecs: or this long since the last, when something changed.
	tidyAfterSecs = 7 * 24 * 60 * 60
	// TidyMarker is when the last tidy was, in the memory folder.
	TidyMarker = ".tidied.json"
)

// TidyInstructions is what the tidy is told.
const TidyInstructions = `You keep the memory of Textus AI, a writing assistant, tidy. Below is every memory it has, each with its id, scope and kind. Answer with the changes that tidy it, as JSON only.

- Repeats: memories that say the same thing (in other words, or one inside another) become one - update the clearest with the whole of what they say together, and forget the others "into" it.
- Overturned: when a newer memory (lower in its list) contradicts an older one about the same thing, forget the older "into" the newer - unless the newer says from where it holds ("From chapter 4 on…"); then both stay.
- Narrowed: a newer memory that makes an exception to an older one ("Use 您 in letters to clients" after "Write 你, never 您") overturns nothing - keep both, or update the older into one memory that says the rule and its exception; never lose the exception.
- Wordy: a memory that takes many words to say one thing may be updated shorter, saying exactly the same.
- Nothing else: never change what a memory means, never move one between scopes, never merge a personal memory with a project one, never add one. Most memories need nothing.

Write each update in the language the memory is written in.

Answer with exactly this JSON and nothing else:
{"changes": [{"op": "update" | "forget", "id": "<the memory's id>", "kind": "<its kind>", "scope": "<its scope>", "text": "<for update: the memory as it should read>", "into": "<for forget: the id of the memory that now says it, or overturns it>"}]}
- {"changes": []} when it is tidy already.`

// LastTidy is when (seconds) the last tidy was, and how many memories
// there were after it.
type LastTidy struct {
	At    uint64 `json:"at"`
	Count int    `json:"count"`
}

// TidyDue reports whether count memories call for a tidy now, given the
// last one (nil for none).
func TidyDue(count int, last *LastTidy, now uint64) bool {
	if count < tidyFrom {
		return false
	}
	if last == nil {
		return true
	}
	return count >= last.Count+tidyAfterAdded || now >= last.At+tidyAfterSecs && count != last.Count
}

// TidyInput is what the tidy is shown: every memory, by scope, oldest
// first.
func TidyInput(memories Memories) string {
	items := slices.Clone(memories.Items)
	slices.SortStableFunc(items, func(a, b Memory) int {
		return cmp.Or(strings.Compare(string(a.Scope), string(b.Scope)), strings.Compare(a.Remembered.Updated, b.Remembered.Updated))
	})
	lines := make([]string, len(items))
	for i, m := range items {
		lines[i] = Listed(m)
	}
	return strings.Join(lines, "\n")
}

// TidyChanges are the tidy's changes: only updates and removals of
// memories it was shown. A removal names the memory it goes into, which
// stays (a merge, or a newer one overturning it), so a newer exception is
// never lost to an older rule.
func TidyChanges(answer string, memories Memories) []MemoryChange {
	changes := ParseChanges(answer)
	shown := func(id string) *Memory {
		for i := range memories.Items {
			if memories.Items[i].ID == id {
				return &memories.Items[i]
			}
		}
		return nil
	}
	var updated []string
	for _, c := range changes {
		if c.TheOp() == "update" && c.TheID() != "" {
			updated = append(updated, c.TheID())
		}
	}
	// A removal into a memory of its own scope: merged into one updated
	// here, or overturned by a newer one.
	fits := func(c MemoryChange) bool {
		me, into := shown(c.TheID()), shown(c.MergedInto())
		return me != nil && into != nil && into.ID != me.ID && into.Scope == me.Scope &&
			(slices.Contains(updated, into.ID) || into.Remembered.Updated > me.Remembered.Updated)
	}
	var fitting []MemoryChange
	var gone []string
	for _, c := range changes {
		if c.TheOp() == "forget" && fits(c) {
			fitting = append(fitting, c)
			gone = append(gone, c.TheID())
		}
	}
	var out []MemoryChange
	for _, c := range changes {
		if c.TheOp() == "update" && c.TheID() != "" && shown(c.TheID()) != nil && !slices.Contains(gone, c.TheID()) {
			out = append(out, c)
		}
	}
	// ...and that memory stays.
	for _, c := range fitting {
		if !slices.Contains(gone, c.MergedInto()) {
			out = append(out, c)
		}
	}
	return out
}

// A task worth doing again, offered as a skill: after an exchange whose
// request was a procedure of its own, one small request decides whether
// to offer it. Nothing is saved unasked.

// offerFromChars: a request shorter than this is never a procedure.
const offerFromChars = 60

// OfferInstructions is what the skill offer is told.
const OfferInstructions = `You help Textus AI, a writing assistant, learn the user's tasks. You are shown the user's last request, the assistant's reply, and the skills (saved procedures) the user has. Decide whether the request is a procedure worth saving as a new skill: several particular requirements for a kind of task the user is likely to ask for again in other documents (how to translate their newsletters, the checklist for a weekly report, the way to turn notes into minutes). Not a request about this text only ("make this shorter", "fix the typo", "translate this paragraph"), not one a skill they have already does, not a single requirement (that is a memory, not a skill). Most requests are not worth it: when in doubt, offer nothing.

Answer with exactly this JSON and nothing else:
{"skill": null}
or
{"skill": {"name": "<two to four words naming the task, in the user's language>", "description": "<one sentence: what it does and when to use it>", "instructions": "<the procedure, as instructions to the assistant: every requirement the user gave, in order, in the user's language, nothing about this document in particular>"}}`

// SkillOffer is a skill offered after an exchange (SkillOffer in
// src/platform/bindings/ai-memory.ts).
type SkillOffer struct {
	Name         string `json:"name"`
	Description  string `json:"description"`
	Instructions string `json:"instructions"`
}

// MayOffer reports whether the exchange's request is long enough to be a
// procedure.
func MayOffer(exchange Exchange) bool {
	n := len(exchange.Messages)
	return n > 0 && utf8.RuneCountInString(strings.TrimSpace(exchange.Messages[n-1])) >= offerFromChars
}

// OfferInput is what the skill offer is shown.
func OfferInput(exchange Exchange, skills []AgentSkill) string {
	var have []string
	for _, s := range skills {
		have = append(have, fmt.Sprintf("- %s: %s", s.Name, s.Description))
	}
	list := "(none)"
	if len(have) > 0 {
		list = strings.Join(have, "\n")
	}
	request := ""
	if n := len(exchange.Messages); n > 0 {
		request = exchange.Messages[n-1]
	}
	return fmt.Sprintf("---the user's skills---\n%s\n\n---the user's request---\n%s\n\n---the assistant's reply---\n%s",
		list, request, firstChars(exchange.Reply, 1_500))
}

// ParseOffer is the offer in the answer, or nil: an offer needs all three
// parts, and a name no skill of the user's has.
func ParseOffer(answer string, skills []AgentSkill) *SkillOffer {
	var a struct {
		Skill *SkillOffer `json:"skill"`
	}
	if !JSONObject(answer, &a) || a.Skill == nil {
		return nil
	}
	// As the `/` menu names a skill (ParseSkillFile): words joined by -.
	offer := SkillOffer{
		Name:         strings.Join(strings.Fields(a.Skill.Name), "-"),
		Description:  strings.TrimSpace(a.Skill.Description),
		Instructions: strings.TrimSpace(a.Skill.Instructions),
	}
	taken := slices.ContainsFunc(skills, func(s AgentSkill) bool { return strings.EqualFold(s.Name, offer.Name) })
	if offer.Name == "" || utf8.RuneCountInString(offer.Name) > 60 || offer.Description == "" || offer.Instructions == "" || taken {
		return nil
	}
	return &offer
}

// OfferedSkillFile is the skill file for offer: its name and description
// as front matter, then its instructions.
func OfferedSkillFile(offer SkillOffer) string {
	quote := func(s string) string {
		raw, _ := json.Marshal(s)
		return string(raw)
	}
	return fmt.Sprintf("---\nname: %s\ndescription: %s\nmetadata:\n  textus-invocation: \"both\"\n---\n\n%s\n",
		quote(offer.Name), quote(offer.Description), offer.Instructions)
}
