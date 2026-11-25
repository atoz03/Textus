package ai

import (
	"strings"

	"textus/backend/ai/aicompat"
	"textus/backend/ai/rules"
)

// What a request carries besides the system prompt: the document (whole,
// or an outline and the section in focus when it is long), the style
// profile and the library entries it touches, and the user's message under
// a header naming its language.

// ReadingFor is how a request on provider reads the document: the room
// every model of the provider has.
func ReadingFor(tools bool, provider string) Reading {
	if aicompat.Provider(provider).Roomy {
		return Reading{Tools: tools, Whole: RoomyDocumentLimit}
	}
	return LeastReading(tools)
}

// RequestContext is the per-request context: the style profile, the
// library entries this request touches, the files beside the document
// (where the model can read them), then the document - with the library
// entries it carries and leaves out. It goes with the current user message
// only, so earlier turns never repeat an old copy of the document.
// focusHeading is the heading of the section the cursor or selection is
// in, included in full when the document is too long to embed.
func RequestContext(ws *AgentWorkspace, document *Document, r Reading, focus, focusHeading string) (string, LibraryPick) {
	documentText := DocumentSection(document, r, focusHeading)
	pick := PickLibrary(ws.Library, focus+"\n"+documentText)
	var parts []string
	if section := WritingContextSection(ws.Style, pick.Included); section != "" {
		parts = append(parts, section)
	}
	// Only where the model can read them.
	if r.Tools && ws.Root != nil {
		if nearby := NearbySection(ws.Nearby); nearby != "" {
			parts = append(parts, nearby)
		}
	}
	parts = append(parts, documentText)
	return strings.Join(parts, "\n\n"), pick
}

// RequestUserText is the current user message as sent: the request
// context, then what the user wrote, under a header naming its language.
// Without that header, a short English request with a CJK document got its
// reply in the document's language.
func RequestUserText(context, message string) string {
	if language, ok := MessageLanguage(message); ok {
		return fill(userMessageInTemplate, nil, "context", context, "language", language, "message", message)
	}
	return fill(userMessageTemplate, nil, "context", context, "message", message)
}

// RequestMessage is the current user message on the tool path: the
// preamble (the mode, what Textus AI remembers), the request context, then
// what the user wrote.
func RequestMessage(ws *AgentWorkspace, mode AgentMode, uiLanguage, context, message string) string {
	preamble := RequestPreamble(ws, mode, uiLanguage)
	// Only Write has propose_edit, which the note is about.
	if mode == ModeWrite && asksForALength(message) {
		preamble += "\n\n" + lengthTargetNote
	}
	return RequestUserText(preamble+"\n\n"+context, message)
}

// lengthPattern is a number and a unit of text ("1200 字", "500 words",
// "三千字", "2,000 characters").
var lengthPattern = rules.MustRe(`(?i)([0-9][0-9,]*|[一二两三四五六七八九十百千万]+)\s*(?:个)?\s*(?:字|词|words?\b|characters?\b|chars?\b|文字|語|단어|자\b|mots?\b|palabras?\b|wörter\b|слов)`)

// asksForALength is whether the user's own words ask for a length: told
// only in the system prompt, models wrote the piece out unseen in
// measure_text before proposing it.
func asksForALength(message string) bool { return lengthPattern.MatchString(message) }

// typedSpans are what a message carries that the user didn't type: a
// selection, the text around the cursor, attached files, an expanded
// skill's instructions.
var typedSpans = [][2]string{
	{"<skill name=", "</skill>"},
	{"<selected-text>", "</selected-text>"},
	{"<cursor>", "</cursor>"},
	{"<attached-file", "</attached-file>"},
}

// MessageLanguage is the language of what the user typed, in English
// ("Chinese"); false when there is too little of it to tell.
func MessageLanguage(message string) (string, bool) {
	typed := message
	for _, span := range typedSpans {
		for {
			start := strings.Index(typed, span[0])
			if start < 0 {
				break
			}
			end := strings.Index(typed[start:], span[1])
			if end < 0 {
				typed = typed[:start]
				break
			}
			typed = typed[:start] + " " + typed[start+end+len(span[1]):]
		}
	}
	lang, ok := rules.Guess(typed)
	if !ok {
		return "", false
	}
	// The script is part of the language: otherwise a Simplified request
	// to convert to Traditional got its reply in Traditional.
	if lang == rules.ZhHans {
		return "Simplified Chinese", true
	}
	name := rules.EnglishName(lang.UICode())
	return name, name != ""
}

// RefusesTools reports whether a request's error says the server can't
// take tools (it names them in refusing the request), rather than any
// other failure.
func RefusesTools(err error) bool {
	text := err.Error()
	lower := strings.ToLower(text)
	named := false
	for _, word := range []string{"tool", "function call", "function_call", "functions"} {
		named = named || strings.Contains(lower, word)
	}
	if !named || strings.Contains(lower, "403") {
		return false
	}
	switch aicompat.ErrorKind(text) {
	case "rateLimit", "timeout", "auth", "balance":
		return false
	}
	return true
}

// FlattenedTranscript is a conversation as one plain-text turn, for a
// provider without tool calling: each user and assistant turn, images
// dropped (a message with images was already refused for these
// providers).
func FlattenedTranscript(history []aicompat.AgentTurn, message string) string {
	var transcript []string
	for _, turn := range history {
		switch turn.Kind {
		case aicompat.User:
			transcript = append(transcript, "User: "+turn.Text)
		case aicompat.Assistant:
			transcript = append(transcript, "Assistant: "+turn.Text)
		}
	}
	transcript = append(transcript, "User: "+message)
	return strings.Join(transcript, "\n\n")
}
