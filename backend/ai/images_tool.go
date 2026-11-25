package ai

import (
	"encoding/json/v2"
	"fmt"
	"slices"
	"strings"

	"textus/backend/ai/aicompat"
)

// One request's pictures: the generate_image tool, each picture's job
// against the request's limit, and what its result tells the model.

// ImageActions are generate_image's actions: the insertions, and `replace`
// - a picture already in the document, for one that redoes it. A picture
// for new text is written into that text instead (inline).
var ImageActions = []string{"insert_after", "insert_before", "insert_at_cursor", "append", "replace"}

// started is a picture begun: its id and the size it is asked for, the
// frame's shape until it comes.
type started struct {
	id            string
	width, height uint32
}

// start starts making one picture in the background.
func (r *ImageRun) start(prompt string, aspect aicompat.Aspect, reference *Reference) started {
	id := newPicture()
	makePicture(id, &asked{job: r.job, prompt: prompt, aspect: aspect, reference: reference}, r.request)
	w, h := nominalSize(aspect)
	return started{id, w, h}
}

// beginPicture starts making a picture written into new text (inline), if
// this send has pictures left; or why not.
func (r *ImageRun) beginPicture(prompt string, aspect aicompat.Aspect, like string) (started, error) {
	if err := r.spend(); err != nil {
		return started{}, err
	}
	return r.start(prompt, aspect, r.like(like)), nil
}

// like is the picture `like` names, by its src in the document or a
// proposed `textus-generated:` src. Nil where the service takes no
// picture, when not found, or while still being made: it is not waited
// for.
func (r *ImageRun) like(like string) *Reference {
	like = strings.TrimSpace(like)
	if like == "" || !r.job.API.TakesReference() {
		return nil
	}
	var (
		picture aicompat.ImageAttachment
		ok      bool
	)
	if id, made := strings.CutPrefix(like, "textus-generated:"); made {
		picture, ok = madePicture(id)
	} else {
		picture, ok = r.localPicture("![](<" + like + ">)")
	}
	if !ok {
		return nil
	}
	return &Reference{Like: true, Picture: picture}
}

// spend is one of this send's pictures, or why there are none left.
func (r *ImageRun) spend() error {
	if r.made.Add(1)-1 >= int64(r.limit) {
		return errorText(r.limitReached())
	}
	return nil
}

// limitReached is what the model is told once this send has made all its
// pictures.
func (r *ImageRun) limitReached() string {
	return fmt.Sprintf("This request has already made %d pictures, the most one request may (the user's setting). Ask the user whether they want more.", r.limit)
}

// check: the call is fit to spend a picture on - a description, a place
// that exists in the document, and pictures left in this send.
func (r *ImageRun) check(args *Args, tc *ToolContext) (string, aicompat.Aspect, *Reference, error) {
	text := func(key string) string { return strings.TrimSpace(args.StrOr(key)) }
	prompt := text("prompt")
	if prompt == "" {
		return "", 0, nil, errorText("`prompt` is empty - describe the picture.")
	}
	action := text("action")
	if !slices.Contains(ImageActions, action) {
		return "", 0, nil, errorText(fmt.Sprintf("Unknown action `%s`. Use one of: %s.", action, strings.Join(ImageActions, ", ")))
	}
	var reference *Reference
	if action == "replace" {
		anchor := text("anchor")
		if !IsPictureMarkdown(anchor) {
			return "", 0, nil, errorText("`replace` takes `block`, the id of a picture's own block - a picture replaces only a picture; put one next to text with insert_after.")
		}
		if why := CheckTarget(tc.Document, args); why != "" {
			return "", 0, nil, errorText(why)
		}
		if r.job.API.TakesReference() {
			if picture, ok := r.localPicture(anchor); ok {
				reference = &Reference{Picture: picture}
			}
		}
	}
	if action == "insert_after" || action == "insert_before" {
		// Next to text proposed and not decided yet - which has no block -
		// the picture joins that proposal.
		joins := !args.Has("block") && quotesUndecided(tc, text("anchor"))
		if !joins {
			if why := CheckTarget(tc.Document, args); why != "" {
				return "", 0, nil, errorText(why)
			}
		}
	}
	aspect := aicompat.Square
	if ratio := text("aspect"); ratio != "" {
		var ok bool
		if aspect, ok = aicompat.ParseAspect(ratio); !ok {
			return "", 0, nil, errorText(fmt.Sprintf("Unknown aspect `%s`. Use one of: %s.", ratio, strings.Join(aicompat.Aspects, ", ")))
		}
	}
	if err := r.spend(); err != nil {
		return "", 0, nil, err
	}
	// A new picture may be drawn like an earlier one; one redone is drawn
	// from itself.
	if reference == nil {
		reference = r.like(text("like"))
	}
	return prompt, aspect, reference, nil
}

// runCall is one generate_image call's arguments, with `image` (id and
// size) or `not_generated` (why not) recorded in them. Returns at once
// while the picture is made in the background, so the reply goes on.
func (r *ImageRun) runCall(arguments string, tc *ToolContext, announce func()) string {
	args, ok := ParseArgs(arguments)
	if !ok {
		return arguments
	}
	args.Remove("image")
	args.Remove("not_generated")
	prompt, aspect, reference, err := r.check(args, tc)
	if err != nil {
		args.Set("not_generated", err.Error())
		// The user is told this one in their language, by its number.
		if err.Error() == r.limitReached() {
			args.Set("limit_reached", r.limit)
		}
		return args.String()
	}
	announce()
	s := r.start(prompt, aspect, reference)
	args.Set("image", map[string]any{"id": s.id, "width": s.width, "height": s.height, "pending": true})
	return args.String()
}

// Tool is generate_image for this request, offered in Write; send tells
// the frontend a picture is being generated.
func (r *ImageRun) Tool(send func(StreamEvent)) Tool {
	return Tool{Spec: imageSpec(r.limit), Access: ProposesInItsCall, Modes: EditingModes, Run: func(tc *ToolContext, call aicompat.ToolCall) aicompat.ToolOutput {
		// The provider may not have streamed the call's start; the frontend
		// says "Generating an image…" from this.
		announce := func() { send(StreamEvent{Type: "toolStart", CallID: call.CallID, Name: call.Name}) }
		arguments := r.runCall(call.Arguments, tc, announce)
		return aicompat.ToolOutput{Text: imageOutcome(arguments), CallArguments: &arguments}
	}}
}

// imageDescription is what the model is told about generate_image, with
// this request's limit.
func imageDescription(limit int) string {
	return fmt.Sprintf("Make a picture with the user's image model and propose it for the document: the user sees it in place, and it is saved only if they accept. Use it when the user asks for a picture, an illustration, a figure or a cover - not for charts or diagrams you can write as Markdown or Mermaid. Write `prompt` as a full visual description in English: subject, setting, composition, style, light and colours, and no text in the picture unless asked.\n\nHow many: each picture costs the user money, and this request may make at most %[1]d. When the user says how many, make that many (up to %[1]d); when they don't, make one, or - for a longer piece they asked to illustrate - one for each part that gains from a picture.\n\nNew text with pictures (an empty document, a new section, a piece to write and illustrate): don't call generate_image - write each picture into the text you propose with propose_edit, as a line of its own between paragraphs where it goes: `![alt](%[2]sASPECT description)` - alt a short caption in the document's language, ASPECT one of %[3]s, then the full English description, as for `prompt`. Each picture starts being made the moment its line is written, while you go on writing; the user sees its frame there and accepts the text and its pictures together. These lines count toward the limit, and each describes a different picture.\n\nThe same people, the same look: when a person, an animal or a place appears in more than one picture, describe them in the same words each time - age, face, hair, build, clothes, colours - and keep one style for the whole piece. When an earlier picture shows them, name it: `like` its src (in a picture line, `like:SRC` right after ASPECT, as in `![alt](%[2]s16:9 like:assets/lin.png description)`); the image model is then given that picture to keep them as they are. Only when someone recurs - it makes the picture a little slower.\n\nA picture for text already in the document goes insert_after the block it illustrates, named by its id as `block`; for text you proposed that the user hasn't accepted or rejected yet, quote `anchor` from that text instead. Between paragraphs, never inside a list, a table, a quote or a heading's line, and not two pictures in a row.\n\nRedoing a picture: to change or replace one already in the document, use `replace` with `block` the picture's id, and describe the new picture in full in `prompt` (what to keep, what to change); where the image model can, it is given the old picture to change. The user decides whether the old file goes once they accept. When this request also rewrites the text the picture stands in - a translation, a rewrite of its section or of the whole document - the rewrite's `text` keeps the picture's line as it is (a text edit never changes a picture), and the picture is redone here first, in the same reply: `replace` with its `block`, the new picture described in full; then the rewrite.\n\nIf the service refuses the description, tell the user what it said in plain words; don't change the description and try again on your own.",
		limit, PictureScheme, strings.Join(aicompat.Aspects, " "))
}

// imageSpec is what the model is told about generate_image.
func imageSpec(limit int) aicompat.ToolSpec {
	str := func(description string) map[string]any {
		return map[string]any{"type": "string", "description": description}
	}
	aspect, action := str("Width to height. Default 1:1."), str("Where it goes: insert_after a paragraph (the usual place for an illustration of existing text), insert_before one, insert_at_cursor, append - or replace a picture already in the document.")
	aspect["enum"], action["enum"] = slices.Clone(aicompat.Aspects), slices.Clone(ImageActions)
	return aicompat.ToolSpec{Name: GenerateImageToolName, Description: imageDescription(limit), Parameters: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"prompt": str("What the picture shows, in English, in detail."),
			"alt":    str("A short description of the picture for its alt text, in the document's language."),
			"aspect": aspect,
			"action": action,
			"block":  str("For insert_after and insert_before: the id of the block the picture goes next to, like `k3f9` from `[#k3f9]`. For replace: the picture's own block id."),
			"anchor": str("Only next to text proposed and not decided yet, which has no id: its last sentence, verbatim."),
			"like":   str("Only when someone or something recurs: the src of an earlier picture showing them - a picture in the document, or `textus-generated:ID` proposed before - to keep them and the style as they are there."),
		},
		"required": []any{"prompt", "alt", "action"},
	}}
}

// imageOutcome is what the model is told came of its call, read off its
// recorded arguments.
func imageOutcome(arguments string) string {
	var args map[string]any
	_ = json.Unmarshal([]byte(arguments), &args)
	if image, ok := args["image"].(map[string]any); ok {
		number := func(key string) uint64 {
			n, _ := image[key].(float64)
			if n < 0 {
				return 0
			}
			return uint64(n)
		}
		size := fmt.Sprintf("%d x %d", number("width"), number("height"))
		// Its Markdown, for a later rewrite of the text it stands by to carry
		// it before the user decides on it.
		alt, _ := args["alt"].(string)
		alt = strings.NewReplacer("[", "", "]", "").Replace(alt)
		id, _ := image["id"].(string)
		markdown := fmt.Sprintf("![%s](%s)", strings.Join(strings.Fields(alt), " "), PlaceholderSrc(id))
		carry := fmt.Sprintf(" Until the user decides on it, its Markdown is %s: if you rewrite the paragraph it stands by, put that line in your new text where the picture goes.", markdown)
		if pending, _ := image["pending"].(bool); pending {
			return fmt.Sprintf("Image proposed and being made (%s pixels) while you go on, and appears in place by itself with Accept and Reject once it is ready (or, if the service turns it down, the user sees why there). It is saved only if they accept. Carry on with the rest of the request now - don't wait for it. In your reply, say in a sentence what it shows and where it goes; don't repeat the prompt.%s", size, carry)
		}
		return fmt.Sprintf("Image proposed - the user now sees the picture (%s pixels) in the document with Accept and Reject; it is saved only if they accept. In your reply, say in a sentence what it shows and where it goes; don't repeat the prompt.%s", size, carry)
	}
	why, ok := args["not_generated"].(string)
	if !ok {
		why = "there is no image model set up for this request"
	}
	return fmt.Sprintf("Not generated: %s Tell the user in plain words; don't change the description and call again unless they ask.", why)
}
