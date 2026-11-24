package ai

import (
	"fmt"
	"strings"
)

// Inline completion: the prompt for the text around the caret. The reply
// is made fit to show in reply.go and joined to the cursor in cursor.go.

// completionInstructions: continue from the cursor, not the document's
// end. The word at the cursor travels in its own tag for the model to write
// out, and is cut off again (fitToCursor): models never begin a reply with
// a space.
const completionInstructions = `You are the inline autocomplete of a markdown writing app. When the writer pauses, you propose the words they would most plausibly write next; they see it as grey text and accept it with Tab.

The input marks the insertion point: <text-before-cursor> ends exactly at the cursor and <text-after-cursor> starts exactly there (it may be empty). When the cursor is inside a word or right after one, that word is not in <text-before-cursor> but in <word-at-cursor>, which comes right after it: the letters typed so far, with the cursor at their end. In Chinese and Japanese the same goes for <clause-at-cursor>. When present, <document-title>, <section> (the headings around the cursor, outermost first) and <block> (what the cursor is in) say where in the document this is. The text is plain text with its formatting removed, and what you write is inserted as plain text.

Write ONLY the text to insert at the cursor:
- When there is a <word-at-cursor>, start with that word written out in full - the same letters, finished if they are only its beginning - and go on from there; the letters already typed are removed before the writer sees your text. It may already be a whole word (for, de, the): then write it unchanged and continue with the next words. Finish a cut-off word as the word the sentence needs ("the extra freight co" is cost or costs). That word alone is never the answer: always add what comes after it. Otherwise start right at the last character. In Chinese and Japanese, which have no spaces to tell a word by, there is a <clause-at-cursor> instead when the cursor is in running text: the characters typed so far in the current clause, which may stop partway through a word. Start your text with them exactly as written and go on from there, finishing a word they break off (所以想把 -> 所以想把提交时间…, 先跑十分 -> 先跑十分钟…); they are removed before the writer sees your text.
- Move the text forward with what the text already supports: finish the thought, draw the consequence it sets up, give the next step or point it promises. Never invent facts the writer hasn't given - figures, names, dates, times, findings, causes, quotes, product claims, what a cited study or the writer's own interviews or analysis found or did, function or option names: where the next words would need one, stay general ("move the meeting to another day", not "to next Friday") or stop. In an empty paragraph after a heading or a finished section, open or connect with what the document already says. Never restate, summarize or paraphrase what is already written. Skip generic filler and stock phrases (pivotal, crucial, delve, "plays a vital role", "not just X but Y", 至关重要, 不仅……更……) unless the writer already writes that way.
- Write as this writer writes: same language (including any mixing), register, person, tense, terminology and punctuation style, such as full-width punctuation in Chinese - and from the writer's side: in a letter, what they would say to the recipient, never the recipient's reply. In verse, keep each line about as long as the lines around it and don't repeat their words. After a finished sentence, what you write is a new sentence and starts like one, with a capital letter where the script has them.
- Stay inside the block: in a list item write only the rest of that one item, in a heading only the heading, in a table cell only the cell, in a code block only code in that language and indentation. Never add a line break (except inside code), a list marker or markdown syntax such as backticks or asterisks - outside code, write plain words.
- Where after-text exists, lead into it without repeating any of it.
- If nothing worthwhile can be added, return nothing.
Don't wrap your text in quotation marks or code fences, but do close a quotation the writer opened when its sentence ends. No explanations, notes or comments, and no internal or system XML tags: only the words to insert.`

const lengthShort = `Length: only what the text makes near-certain - usually the rest of the current clause or sentence, about 3 to 12 words, or 5 to 20 CJK characters; in a list item, table cell, heading or line of verse, just enough to finish it, about as long as its neighbours. Stop where the next words would be the writer's own choice.`

const lengthLong = `The writer asked for a continuation: carry the current paragraph on by two to four sentences - roughly 40 to 90 words, or 60 to 160 CJK characters - moving it forward with what the text already supports. Stay in this paragraph: no new paragraph, heading or list.`

// CandidateSeparator is the line between alternative continuations in one
// reply.
const CandidateSeparator = "<<<>>>"

// CodeBlockHint is the block hint the frontend sends for a code block, the
// one block where a completion may run over several lines.
const CodeBlockHint = "code block"

// CompletionHints is what the frontend knows about the cursor beyond the
// text around it (src/platform/bindings/ai.ts CompletionHints).
type CompletionHints struct {
	Title   *string  `json:"title"`
	Section []string `json:"section"`
	Block   *string  `json:"block"`
	Length  *string  `json:"length"`
	// Candidates is how many alternative continuations to return (manual
	// trigger only), separated by CandidateSeparator lines.
	Candidates *int `json:"candidates"`
	// Prose is the context's text without code blocks, diagram and math
	// source: what the language is read from.
	Prose *string `json:"prose"`
	// UILanguage is the app's language, the fallback when the prose is too
	// short to tell its own.
	UILanguage *string `json:"uiLanguage"`
}

// completionLanguage is the language the completion is written in, and
// whether it was read from the prose (true) or is the app's (false); ""
// in a code block, or with neither prose nor UI language.
func completionLanguage(before, after string, h CompletionHints) (string, bool) {
	if deref(h.Block) == CodeBlockHint {
		return "", false
	}
	prose := before + "\n\n" + after
	if h.Prose != nil {
		prose = *h.Prose
	}
	if l, ok := detect(prose); ok {
		return englishName(l.uiCode()), true
	}
	ui := deref(h.UILanguage)
	if l, ok := guess(prose); ok && !(l.isChinese() && (ui == "zh" || ui == "zh-Hant")) {
		return englishName(l.uiCode()), true
	}
	return englishName(ui), false
}

// completionUserText is the request's user turn: the optional where-am-I
// tags, then the text split at the cursor. No whitespace between the
// cursor tags and the text: the before-text must end exactly where the
// typed word starts.
func completionUserText(before, after string, h CompletionHints) string {
	var b strings.Builder
	if t := deref(h.Title); strings.TrimSpace(t) != "" {
		fmt.Fprintf(&b, "<document-title>%s</document-title>\n", t)
	}
	if len(h.Section) > 0 {
		fmt.Fprintf(&b, "<section>%s</section>\n", strings.Join(h.Section, " > "))
	}
	if block := deref(h.Block); strings.TrimSpace(block) != "" {
		fmt.Fprintf(&b, "<block>%s</block>\n", block)
	}
	typed, tag := wordAtCursor(before), "word-at-cursor"
	if typed == "" {
		typed, tag = clauseAtCursor(before), "clause-at-cursor"
	}
	fmt.Fprintf(&b, "<text-before-cursor>%s</text-before-cursor>\n", before[:len(before)-len(typed)])
	if typed != "" {
		fmt.Fprintf(&b, "<%s>%s</%s>\n", tag, typed, tag)
	}
	fmt.Fprintf(&b, "<text-after-cursor>%s</text-after-cursor>", after)
	return b.String()
}

// CompletionPrompt is the completion request as its instructions and its
// user turn. style is the tone the writer picked in Settings, if any.
func CompletionPrompt(before, after string, hints CompletionHints, style string) (string, string) {
	length := lengthShort
	if deref(hints.Length) == "long" {
		length = lengthLong
	}
	instructions := completionInstructions + "\n\n" + length
	switch language, fromProse := completionLanguage(before, after, hints); {
	case language != "" && fromProse:
		instructions += fmt.Sprintf("\n\nLanguage: the writer's prose here is in %s, so write in %s (terms they mix in from other languages stay as they are). Code and diagram source in the text are not prose and don't change the language.", language, language)
	case language != "":
		instructions += fmt.Sprintf("\n\nLanguage: the text around the cursor has too little prose to tell its language (code and diagram source are not prose), so write in %s, the language of the writer's app.", language)
	}
	// The tone the writer picked decides the register, but can't override
	// the format, length and no-invention rules.
	if style = strings.TrimSpace(style); style != "" {
		instructions += fmt.Sprintf("\n\nTone the user chose in Settings: %s It sets the register of your text in place of the writer's own; every other rule above still applies.", style)
	}
	// One request for all alternatives: cheaper, and they come out
	// genuinely different. The length is repeated per alternative, or each
	// got shortened.
	if hints.Candidates != nil && *hints.Candidates > 1 {
		instructions += fmt.Sprintf("\n\nWrite %d alternative continuations that take the text in clearly different directions (different next point, angle or wording) - each one a full continuation of the length above, following every rule above. Output them one after another, separated by a line containing only %s. Nothing else.", *hints.Candidates, CandidateSeparator)
	}
	return instructions, completionUserText(before, after, hints)
}

// deref is *s, "" for none.
func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
