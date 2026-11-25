package ai

import (
	"net/url"
	"strings"
	"unicode"

	"textus/backend/ai/aicompat"
)

// Text and pictures proposed and not decided yet - this request's and
// earlier ones' - that a new edit or picture may go into or stand next to.

const (
	// undecidedNote is in the status note the frontend appends to a
	// proposal the user hasn't decided on.
	undecidedNote = "has NOT yet accepted or rejected this proposed edit"
	// joinedNote is in the note of an edit that went into such a proposal.
	joinedNote = "this went into the proposed text it changes"
)

// PlaceholderSrc is the src a picture being made has in a proposal's
// Markdown, until saved (the frontend's generatedImageSrc).
func PlaceholderSrc(id string) string { return "textus-generated:" + id }

// UndecidedEdit is a propose_edit from an earlier request the user hasn't
// decided on yet.
type UndecidedEdit struct {
	// Replaced is the document text a replace rewrites, which the user now
	// sees as Text; "" for any other action.
	Replaced string
	// Text is its new text.
	Text string
}

// resultOf is the output of callID's result in history, "" for none.
func resultOf(history []aicompat.AgentTurn, callID string) string {
	for _, t := range history {
		if t.Kind == aicompat.ToolResult && t.CallID == callID {
			return t.Output
		}
	}
	return ""
}

// UndecidedEdits is each propose_edit in history the user hasn't decided
// on yet, as the user sees it: a later edit joined into one is made in its
// text, so a quote of the joined text matches.
func UndecidedEdits(history []aicompat.AgentTurn) []UndecidedEdit {
	var edits []UndecidedEdit
	for _, t := range history {
		if t.Kind != aicompat.ToolCalled || t.Name != ProposeEditToolName {
			continue
		}
		args, ok := ParseArgs(t.Arguments)
		if !ok {
			continue
		}
		action, anchor, text := args.StrOr("action"), args.StrOr("anchor"), args.StrOr("text")
		note := resultOf(history, t.CallID)
		switch {
		case strings.Contains(note, undecidedNote) && text != "":
			replaced := ""
			if action == "replace" {
				replaced = anchor
			}
			edits = append(edits, UndecidedEdit{Replaced: replaced, Text: text})
		case strings.Contains(note, joinedNote):
			for i := range edits {
				if made, ok := editedText(edits[i].Text, action, anchor, text); ok {
					edits[i].Text = made
					break
				}
			}
		}
	}
	return edits
}

// editedText is text with an edit quoted from it made in it, as the
// frontend's editedText. False when the anchor isn't there exactly once or
// the action isn't an anchored edit.
func editedText(text, action, anchor, add string) (string, bool) {
	if anchor == "" || strings.Count(text, anchor) != 1 {
		return "", false
	}
	at := strings.Index(text, anchor)
	before, after := text[:at], text[at+len(anchor):]
	paragraph := func(t string) string { return "\n\n" + strings.TrimSpace(t) }
	switch action {
	case "replace":
		return before + add + after, true
	case "delete":
		return strings.TrimSpace(strings.ReplaceAll(before+after, "\n\n\n\n", "\n\n")), true
	case "insert_after":
		if strings.Contains(add, "\n\n") || after == "" || strings.HasPrefix(after, "\n\n") {
			end := strings.Index(after, "\n\n")
			if end < 0 {
				end = len(after)
			}
			return before + anchor + after[:end] + paragraph(add) + after[end:], true
		}
		return before + anchor + gap(anchor, add) + add + after, true
	case "insert_before":
		// A paragraph of its own before the anchor's paragraph (else a
		// title would make the whole paragraph a heading), or words before
		// the anchor.
		if strings.Contains(add, "\n\n") || before == "" || strings.HasSuffix(before, "\n\n") {
			start := 0
			if i := strings.LastIndex(before, "\n\n"); i >= 0 {
				start = i + 2
			}
			return before[:start] + strings.TrimSpace(add) + "\n\n" + before[start:] + anchor + after, true
		}
		return before + add + gap(add, anchor) + anchor + after, true
	}
	return "", false
}

// gap is the space between left and right run together: one between two
// words of a spaced language; none after CJK, before punctuation, or if
// one is there.
func gap(left, right string) string {
	if left == "" || right == "" {
		return ""
	}
	last, first := []rune(left)[len([]rune(left))-1], []rune(right)[0]
	cjk := func(c rune) bool {
		return c >= 0x3000 && c <= 0x303f || c >= 0x3040 && c <= 0x30ff || c >= 0x3400 && c <= 0x4dbf ||
			c >= 0x4e00 && c <= 0x9fff || c >= 0xff00 && c <= 0xffef
	}
	if unicode.IsSpace(last) || unicode.IsSpace(first) || cjk(last) || cjk(first) || strings.ContainsRune(".,;:!?)]}", first) {
		return ""
	}
	return " "
}

// UndecidedPictures are the pictures earlier requests made that are still
// undecided, by id: a rewrite of the text may carry them.
func UndecidedPictures(history []aicompat.AgentTurn) []string {
	var ids []string
	for _, t := range history {
		if t.Kind != aicompat.ToolCalled || t.Name != GenerateImageToolName {
			continue
		}
		if !strings.Contains(resultOf(history, t.CallID), undecidedNote) {
			continue
		}
		args, ok := ParseArgs(t.Arguments)
		if !ok {
			continue
		}
		raw, ok := args.Raw("image")
		if !ok {
			continue
		}
		image, ok := ParseArgs(string(raw))
		if !ok {
			continue
		}
		if id, ok := image.Str("id"); ok {
			ids = append(ids, id)
		}
	}
	return ids
}

// IsPictureMarkdown reports whether anchor is the whole Markdown of one
// picture (`![alt](src)` or `<img ...>`), the only thing a picture may
// replace.
func IsPictureMarkdown(anchor string) bool {
	anchor = strings.TrimSpace(anchor)
	if tag, ok := strings.CutPrefix(anchor, "<img"); ok {
		return tag != "" && unicode.IsSpace([]rune(tag)[0]) && strings.HasSuffix(tag, ">") &&
			!strings.Contains(tag[:len(tag)-1], ">")
	}
	rest, ok := strings.CutPrefix(anchor, "![")
	if !ok {
		return false
	}
	alt, after, ok := strings.Cut(rest, "](")
	return ok && !strings.Contains(alt, "]") && strings.HasSuffix(after, ")") && !strings.Contains(after[:len(after)-1], ")")
}

// PictureSrc is the file a picture's Markdown shows, relative to the
// document's folder; false for a web or data URL, or anything that isn't
// one picture.
func PictureSrc(markdown string) (string, bool) {
	markdown = strings.TrimSpace(markdown)
	if !IsPictureMarkdown(markdown) {
		return "", false
	}
	var src string
	if tag, ok := strings.CutPrefix(markdown, "<img"); ok {
		_, rest, ok := strings.Cut(tag, `src="`)
		if !ok {
			return "", false
		}
		src, _, ok = strings.Cut(rest, `"`)
		if !ok {
			return "", false
		}
	} else {
		_, rest, _ := strings.Cut(markdown, "](")
		rest = strings.TrimSuffix(rest, ")")
		// A title after the src, `![alt](src "title")`, goes.
		src, _, _ = strings.Cut(rest, ` "`)
		src = strings.TrimSpace(src)
	}
	if inner, ok := strings.CutPrefix(src, "<"); ok && strings.HasSuffix(inner, ">") {
		src = strings.TrimSuffix(inner, ">")
	}
	if src == "" || strings.Contains(src, "://") || strings.HasPrefix(src, "data:") {
		return "", false
	}
	// Textus writes a space in a link as %20.
	if decoded, err := url.PathUnescape(src); err == nil {
		src = decoded
	}
	return src, true
}

// SelectedPicture is the picture a message's selection is, when it is one
// picture and nothing else: its src, relative to the document's folder.
func SelectedPicture(message string) (string, bool) {
	rest, ok := strings.CutPrefix(message, "<selected-text>")
	if !ok {
		return "", false
	}
	inner, _, ok := strings.Cut(rest, "</selected-text>")
	inner = strings.TrimSpace(inner)
	if !ok || !strings.HasPrefix(inner, "![") {
		return "", false
	}
	return PictureSrc(inner)
}
