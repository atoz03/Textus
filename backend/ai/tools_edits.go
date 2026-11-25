package ai

import (
	"fmt"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"textus/backend/ai/rules"
)

// propose_edit: an edit the writer accepts or rejects, the blocks it names
// checked against the document before it is offered.

const (
	// editProposed is how a valid proposal's result starts - and
	// commentAdded a valid comment's: such a call needs nothing more from
	// the model (ToolOutput.Settled).
	editProposed = "Edit proposed"
	unchanged    = "Not proposed: `text` is the same as the passage it replaces, so nothing would change. If the passage already reads as asked, say so in your reply; otherwise send the proposal with the new text."
	// pictureKeptNote is told after a rewrite whose text swapped a
	// document's picture for a new one: the original stays in it, and the
	// new one is the picture tool's.
	pictureKeptNote = " A picture of the document you rewrote is kept as it was in it: text edits never change a picture. To change that picture, call generate_image with its block, in this reply."
)

// editRule is one edit operation the frontend applies, and what it needs:
// found by an anchor in the document (else it acts on the user's
// selection, the caret or the end), and with new text. The same list as
// src/editor/markdown/edit-actions.json, which a test keeps it.
type editRule struct {
	action         string
	anchored, text bool
}

var editRules = []editRule{
	{"replace", true, true},
	{"replace_selection", false, true},
	{"insert_before", true, true},
	{"insert_after", true, true},
	{"delete", true, false},
	{"append", false, true},
	{"insert_at_cursor", false, true},
}

// EditActions are the actions, as the tool's schema offers them.
func EditActions() []string {
	actions := make([]string, len(editRules))
	for i, r := range editRules {
		actions[i] = r.action
	}
	return actions
}

func ruleOf(action string) (editRule, bool) {
	i := slices.IndexFunc(editRules, func(r editRule) bool { return r.action == action })
	if i < 0 {
		return editRule{}, false
	}
	return editRules[i], true
}

func needsAnchor(action string) bool { r, ok := ruleOf(action); return !ok || r.anchored }
func needsText(action string) bool   { r, ok := ruleOf(action); return !ok || r.text }

// proposeEdit changes nothing itself: the document only changes when the
// user accepts. It validates the proposal by the frontend's rules, so the
// model gets feedback instead of the user getting a revision that can't
// land.
func proposeEdit(tc *ToolContext, arguments string) string {
	args, ok := ParseArgs(arguments)
	if !ok {
		return "Invalid arguments - expected JSON with an `action` string."
	}
	action := args.StrOr("action")
	if _, ok := ruleOf(action); !ok {
		return fmt.Sprintf("Unknown action `%s`. Use one of: %s.", action, strings.Join(EditActions(), ", "))
	}
	d := tc.Document
	// What the edit is about: what its blocks name, else the words it
	// quotes.
	from, to, placed := d.TargetSpan(args)
	anchor := args.StrOr("anchor")
	if placed {
		anchor = d.Text[from:to]
	}
	// A block it names that isn't there is said as that. Words its block
	// doesn't hold but undecided proposed text does (a rewrite of that
	// block, quoted as written) go into that proposal.
	quoteJoins := false
	if needsAnchor(action) && args.Has("block") {
		if problem := CheckTarget(d, args); problem != "" {
			_, _, inRun := d.Run(args.StrOr("block"), lastBlockOf(args))
			if !inRun || anchor == "" || !quotesUndecided(tc, anchor) {
				return problem
			}
			quoteJoins = true
		}
	}
	text, hasText := args.Str("text")
	if needsText(action) && !hasText {
		return fmt.Sprintf("`%s` requires `text` - the new content.", action)
	}
	joins, again := false, false
	if needsAnchor(action) {
		if anchor == "" {
			return fmt.Sprintf("`%s` requires `anchor` - the exact document text the edit targets.", action)
		}
		named := args.Has("block") && !quoteJoins
		switch {
		case !named:
			// Text proposed and not decided yet - this request's or an
			// earlier one's - has no block: the edit quotes it, and goes
			// into that proposal.
			if !quotesUndecided(tc, anchor) {
				return NoBlock
			}
			joins = true
		case CheckTarget(d, args) != "":
			return CheckTarget(d, args)
		default:
			// Document text an undecided rewrite replaces: the user sees
			// the rewrite there. Where the rewrite still has those words
			// the edit goes into it, and a new version of the whole passage
			// takes its place.
			at := slices.IndexFunc(tc.Undecided, func(e UndecidedEdit) bool {
				return e.Replaced != "" && strings.Contains(e.Replaced, anchor)
			})
			if at >= 0 {
				rewrite := tc.Undecided[at]
				again = action == "replace" && anchor == rewrite.Replaced
				if !again {
					if strings.Count(rewrite.Text, anchor) != 1 || !quotesUndecided(tc, anchor) {
						return "Not proposed: that passage is one you rewrote earlier and the user hasn't decided on yet - they see your rewrite in its place, and an edit of the old text can't be applied beside it. Anchor in the rewrite's text instead (quote it as you proposed it): the edit goes into that proposal."
					}
					joins = true
				}
			}
		}
	}
	// A rewrite that changes nothing: shown, it read as the passage taken
	// out and put back.
	if action == "replace" && !again && strings.Trim(text, "\n") == strings.Trim(anchor, "\n") {
		return unchanged
	}
	tc.mu.Lock()
	defer tc.mu.Unlock()
	ledger := &tc.proposed
	if problem := degenerateReplacement(action, anchor, text); problem != "" {
		// Refused once; the identical call again means it ("replace this
		// sentence with TBD").
		key := anchor + "\x00" + text
		if !slices.Contains(ledger.refusedShort, key) {
			ledger.refusedShort = append(ledger.refusedShort, key)
			return problem
		}
	}
	// A second proposal for a passage this reply already changes: only one
	// could ever be applied, so the first stands and the copy is refused.
	if !joins && (action == "replace" || action == "delete" || action == "replace_selection") {
		passage := ProposedPassage{}
		if action != "replace_selection" {
			passage = passageAt(from, to, placed, anchor)
		}
		if slices.ContainsFunc(ledger.passages, passage.overlaps) {
			return "Not proposed: an edit you already proposed in this reply covers this passage, and overlapping proposals can't both be applied. The first one stands - don't send another; describe it in your reply."
		}
		ledger.passages = append(ledger.passages, passage)
	}
	// The frontend later appends a status note to this result. The size of
	// the change is measured here so the reply can rest on it instead of
	// guessing.
	size := ""
	switch action {
	case "replace":
		n, unit := textSize(anchor)
		m, _ := textSize(text)
		size = fmt.Sprintf(" It replaces %d %s with %d.", n, unit, m)
	case "delete":
		n, unit := textSize(anchor)
		size = fmt.Sprintf(" It removes %d %s.", n, unit)
	case "replace_selection":
	default:
		n, unit := textSize(text)
		size = fmt.Sprintf(" It adds %d %s.", n, unit)
	}
	// "Even if it falls short": told the size, the model sent a second,
	// shorter overlapping proposal to meet a target.
	unmade := ""
	if text != "" {
		if problem := ledger.picturesProblem(text); problem != "" {
			return problem
		}
		ledger.texts = append(ledger.texts, text)
		unmade = ledger.takeUnmade() + ledger.picturesPlaced(text)
	}
	// A rewrite that swapped a picture keeps the original: the swap is
	// generate_image's. One that kept its pictures says so too, with their
	// ids, or the model thought the picture already changed.
	kept := args.Has(PicturesKeptKey)
	if kept {
		unmade += pictureKeptNote
	}
	// Said last, after what to reply: said before it, the reply came first
	// and the turn ended there.
	pictures := ""
	var ids []string
	if raw, ok := args.Raw(PicturesInRunKey); ok && !kept && unmarshalStrings(raw, &ids) && len(ids) > 0 {
		marked := make([]string, len(ids))
		for i, id := range ids {
			marked[i] = "[#" + id + "]"
		}
		pictures = fmt.Sprintf(" Its pictures (%s) still show what they showed; only a description you changed is taken. If the user asked for a picture to change too, it hasn't yet: before you reply, redo it with generate_image, `replace` with its block.", strings.Join(marked, ", "))
	}
	into := ""
	switch {
	case joins:
		into = " It changes text you proposed that the user hasn't decided on yet: it goes into that proposal, which now reads with it."
	case again:
		into = " It takes the place of your earlier rewrite of that passage, which the user hadn't decided on."
	}
	return fmt.Sprintf("%s - the user now sees it in the document.%s%s%s (Counts here are for you, measured the way measure_text does; the user never sees this note, so don't refer to it.) This is your proposal for that passage in this reply - don't send another one for it, even if it falls short of a target: say so in your reply instead. In your reply, say in a sentence or two what you proposed and why; don't repeat the text or list what you kept.%s", editProposed, into, size, unmade, pictures)
}

// quotesUndecided reports whether anchor is quoted from exactly one place
// in text proposed and not decided yet: this request's proposals and
// earlier ones'.
func quotesUndecided(tc *ToolContext, anchor string) bool {
	tc.mu.Lock()
	defer tc.mu.Unlock()
	found := 0
	for _, text := range tc.proposed.texts {
		found += strings.Count(text, anchor)
	}
	for _, e := range tc.Undecided {
		found += strings.Count(e.Text, anchor)
	}
	return found == 1
}

// textSize is how long a stretch of text is, in the unit its language
// counts in: characters for Chinese and Japanese, words otherwise, exactly
// as measure_text counts. A picture's line is no part of it.
func textSize(text string) (int, string) {
	var lines []string
	for _, line := range strings.Split(text, "\n") {
		if !strings.HasPrefix(strings.TrimLeftFunc(line, unicode.IsSpace), "![") {
			lines = append(lines, line)
		}
	}
	text = strings.Join(lines, "\n")
	cjk, words := 0, 0
	for _, c := range text {
		if rules.IsCJK(c) {
			cjk++
		}
	}
	for _, w := range strings.Fields(text) {
		if strings.IndexFunc(w, func(c rune) bool { return rules.IsAlphanumeric(c) && !rules.IsCJK(c) }) >= 0 {
			words++
		}
	}
	if cjk > words {
		return rules.Graphemes(strings.TrimSpace(text)), "characters"
	}
	return rules.Words(text), "words"
}

// degenerateReplacement is the refusal of a replacement that can't be what
// the model meant: a whole CJK clause replaced by one short Latin token.
// The identical call sent again goes through ("replace the sentence with
// TBD"). "" when it may be.
func degenerateReplacement(action, anchor, text string) string {
	if action != "replace" {
		return ""
	}
	anchorCJK := 0
	for _, c := range anchor {
		if rules.IsCJK(c) {
			anchorCJK++
		}
	}
	text = strings.TrimSpace(text)
	latinToken := text != "" && utf8.RuneCountInString(text) <= 12 &&
		strings.IndexFunc(text, unicode.IsSpace) < 0 && strings.IndexFunc(text, rules.IsCJK) < 0
	if anchorCJK < 12 || !latinToken {
		return ""
	}
	return fmt.Sprintf("Not proposed: `text` is only \"%s\", which looks cut off for a passage this long. Send the proposal again with the full new text - or, if the user really asked for exactly this text, send the same call again unchanged.", text)
}
