package ai

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Two short suggestions from the writing model: a title for an untitled
// document (src/ai/doc-title.ts falls back to its first line), and the
// messages the writer will probably send next (src/ai/chat/next-message.ts).

// TitleInstructions ask for a document's title.
const TitleInstructions = "You name a writer's document. Read the start of it and answer with one short title for it: at most 8 words, or 20 characters in Chinese or Japanese, in the language the document is written in. Plain text on one line: no quotes, no Markdown, no full stop at the end, no \"Title:\". If the text is too short or empty to tell what it is about, answer NONE."

const (
	// longestExcerpt: the start of a document says what it is about.
	longestExcerpt = 4000
	// longestTitle: a title longer than this is a sentence, not a name.
	longestTitle = 60
	titleQuotes  = "\"'“”‘’「」《》`*"
	titleTail    = ".。!！?？,，;；:：…"
)

// TitleInput is the request's text for a document's excerpt.
func TitleInput(excerpt string) string {
	return "The start of the document:\n\n" + firstChars(strings.TrimSpace(excerpt), longestExcerpt) + "\n\nIts title:"
}

// Title is the model's answer as a title: false for NONE, nothing, or one
// too long.
func Title(answer string) (string, bool) {
	line := ""
	for _, l := range strings.Split(answer, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			line = l
			break
		}
	}
	line = strings.TrimSpace(strings.TrimLeft(line, "#-*>"))
	if label, rest, ok := strings.Cut(line, ":"); ok && strings.EqualFold(strings.TrimSpace(label), "title") {
		line = strings.TrimSpace(rest)
	}
	// A full stop may sit inside the quotes or outside them.
	line = strings.TrimSpace(strings.TrimRight(line, titleTail))
	line = strings.TrimSpace(strings.TrimRight(strings.Trim(line, titleQuotes), titleTail))
	if line == "" || strings.EqualFold(line, "none") || utf8.RuneCountInString(line) > longestTitle {
		return "", false
	}
	return line, true
}

// NextMessageInstructions ask for the writer's next messages.
const NextMessageInstructions = "You predict the next message a writer will send to their writing assistant, from the conversation so far. Give up to three different ones, one per line, most likely first, each as they would type it: short (at most 6 words, or 15 characters in Chinese or Japanese), in the language they write their messages in, plain text with no quotes or numbering. Make each a concrete next step on their document - go on with the next part, change something in what was just done, or a follow-up question - never a thank-you or a goodbye, and never asking again for what the assistant just did: what it wrote or changed is already in the document. If there is no clear next step, answer NONE."

const (
	messagesSent    = 4
	longestMessage  = 80
	mostSuggestions = 3
	messageChars    = 600
	replyChars      = 1500
)

// Exchange is one turn of a conversation: the writer's messages so far,
// oldest first, and the reply to the last (Exchange in
// src/platform/bindings/ai.ts and ai-memory.ts).
type Exchange struct {
	Messages []string `json:"messages"`
	Reply    string   `json:"reply"`
}

// NextMessageInput is the request's text: the last few messages and the
// reply.
func NextMessageInput(e Exchange) string {
	var b strings.Builder
	b.WriteString("The writer's messages, oldest first:\n")
	for _, m := range e.Messages[max(len(e.Messages)-messagesSent, 0):] {
		fmt.Fprintf(&b, "- %s\n", Clip(m, messageChars))
	}
	fmt.Fprintf(&b, "\nThe assistant's reply to the last one (what it wrote or changed is already in the document):\n%s\n\nTheir next messages:", Clip(e.Reply, replyChars))
	return b.String()
}

// suggestion is one line of the answer as a message to offer: false for
// NONE, nothing, or something too long to be a typed message.
func suggestion(line string) (string, bool) {
	line = strings.TrimLeftFunc(strings.TrimLeft(strings.TrimSpace(line), "-*>•"), unicode.IsSpace)
	// "1." and "2)" numbering, which the model adds though asked not to.
	digits := len(line) - len(strings.TrimLeft(line, "0123456789"))
	if digits > 0 {
		for _, mark := range []string{".", ")", "、"} {
			if rest, ok := strings.CutPrefix(line[digits:], mark); ok {
				line = rest
				break
			}
		}
	}
	line = strings.TrimSpace(strings.Trim(strings.TrimSpace(line), "\"'“”「」`"))
	if line == "" || strings.EqualFold(line, "none") || utf8.RuneCountInString(line) > longestMessage {
		return "", false
	}
	return line, true
}

// Suggestions are the model's answer as up to three different messages,
// most likely first.
func Suggestions(answer string) []string {
	found := []string{}
	for _, l := range strings.Split(answer, "\n") {
		s, ok := suggestion(l)
		if !ok {
			continue
		}
		seen := false
		for _, f := range found {
			seen = seen || strings.ToLower(f) == strings.ToLower(s)
		}
		if !seen {
			found = append(found, s)
		}
		if len(found) == mostSuggestions {
			break
		}
	}
	return found
}

// Clip is text trimmed and cut to at most most characters, an ellipsis
// where it was cut.
func Clip(text string, most int) string {
	text = strings.TrimSpace(text)
	if utf8.RuneCountInString(text) <= most {
		return text
	}
	return strings.TrimRightFunc(firstChars(text, most), unicode.IsSpace) + "…"
}

func firstChars(text string, n int) string {
	if utf8.RuneCountInString(text) <= n {
		return text
	}
	return string([]rune(text)[:n])
}
