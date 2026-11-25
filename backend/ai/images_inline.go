package ai

import (
	"slices"
	"strconv"
	"strings"
	"sync"
	"unicode"
	"unicode/utf16"

	"textus/backend/ai/aicompat"
)

// Pictures written into new text: each is a line of its own in the
// proposed text, `![alt](textus-picture:16:9 what it shows)`, and starts
// being made the moment its line has streamed in. Placed, the lines become
// `![alt](textus-generated:ID)` as for any other picture.

// PictureScheme is the src of a picture line (the frontend's
// PICTURE_LINE).
const PictureScheme = "textus-picture:"

// pictureLine is one picture line read: its alt text, the description it
// is made from, its shape, and the src of the picture it is drawn like (or
// "").
type pictureLine struct {
	alt, prompt string
	aspect      aicompat.Aspect
	like        string
}

// readPictureLine is line as a picture line, if it is one:
// `![alt](textus-picture:` then an optional aspect, an optional `like:SRC`,
// and the description, up to the line's last `)` (a description may have
// parentheses of its own).
func readPictureLine(line string) (pictureLine, bool) {
	rest, ok := strings.CutPrefix(strings.TrimSpace(line), "![")
	if !ok {
		return pictureLine{}, false
	}
	alt, after, ok := strings.Cut(rest, "](")
	if !ok || strings.Contains(alt, "]") {
		return pictureLine{}, false
	}
	body, ok := strings.CutPrefix(after, PictureScheme)
	if !ok || !strings.HasSuffix(body, ")") {
		return pictureLine{}, false
	}
	body = strings.TrimSpace(strings.TrimSuffix(body, ")"))
	aspect, rest := aicompat.Square, body
	if i := strings.IndexFunc(body, unicode.IsSpace); i >= 0 {
		if a, known := aicompat.ParseAspect(body[:i]); known {
			aspect, rest = a, strings.TrimSpace(body[i:])
		}
	}
	like, prompt := "", rest
	if after, ok := strings.CutPrefix(rest, "like:"); ok {
		if i := strings.IndexFunc(after, unicode.IsSpace); i >= 0 {
			like, prompt = after[:i], strings.TrimSpace(after[i:])
		} else {
			like, prompt = after, ""
		}
	}
	if prompt == "" {
		return pictureLine{}, false
	}
	return pictureLine{alt: strings.TrimSpace(alt), prompt: prompt, aspect: aspect, like: like}, true
}

// streaming is a tool call streaming in: whether it proposes text, its
// arguments so far, and the picture lines the frontend has been told of.
type streaming struct {
	proposes bool
	args     strings.Builder
	told     []string
}

// lineStarted is what came of a picture line: the picture being made, or
// why none is.
type lineStarted struct {
	line    string
	picture started
	err     error
}

// InlinePictures are one request's pictures written into its text.
type InlinePictures struct {
	run *ImageRun
	tc  *ToolContext
	mu  sync.Mutex
	// streams are the calls streaming in, by call id.
	streams map[string]*streaming
	// started are each picture line seen, trimmed, and what came of it: the
	// same line again (the finished step, or a retried step) is the same
	// picture, not a second one paid for.
	started []lineStarted
}

// Inline is this request's pictures written into its text, recorded in
// the ledger its propose_edit calls check them against.
func (r *ImageRun) Inline(tc *ToolContext) *InlinePictures {
	return &InlinePictures{run: r, tc: tc, streams: map[string]*streaming{}}
}

// Observe takes a streamed fragment of the step: each picture line it
// completes in a propose_edit's text starts its picture, and the frontend
// is told (before the fragment itself, so the line never shows as text).
func (p *InlinePictures) Observe(e aicompat.ProviderEvent) []StreamEvent {
	p.mu.Lock()
	defer p.mu.Unlock()
	switch e.Kind {
	case aicompat.EventToolStart:
		p.streams[e.CallID] = &streaming{proposes: e.Name == ProposeEditToolName}
		return nil
	case aicompat.EventToolArgs:
	default:
		return nil
	}
	// A provider that didn't say which tool: read it as propose_edit, the
	// one with a `text`.
	s, ok := p.streams[e.CallID]
	if !ok {
		s = &streaming{proposes: true}
		p.streams[e.CallID] = s
	}
	if !s.proposes {
		return nil
	}
	s.args.WriteString(e.Text)
	text, closed, ok := partialString(s.args.String(), "text")
	if !ok {
		return nil
	}
	lines := strings.Split(text, "\n")
	// The last line is complete only once the text is.
	complete := len(lines)
	if !closed {
		complete--
	}
	var events []StreamEvent
	for _, line := range lines[:complete] {
		line = strings.TrimSpace(line)
		if slices.Contains(s.told, line) {
			continue
		}
		picture, ok := readPictureLine(line)
		if !ok {
			continue
		}
		s.told = append(s.told, line)
		if begun := p.start(line, picture); begun.err == nil {
			events = append(events, StreamEvent{Type: "picture", CallID: e.CallID, Line: line, ID: begun.picture.id, Width: begun.picture.width, Height: begun.picture.height})
		}
	}
	return events
}

// start is the picture for line: the one already begun for it, or one
// begun now. Called with p locked.
func (p *InlinePictures) start(line string, picture pictureLine) lineStarted {
	for _, s := range p.started {
		if s.line == line {
			return s
		}
	}
	begun, err := p.run.beginPicture(picture.prompt, picture.aspect, picture.like)
	if err == nil {
		p.tc.mu.Lock()
		p.tc.proposed.pictures = append(p.tc.proposed.pictures, begun.id)
		p.tc.mu.Unlock()
	}
	outcome := lineStarted{line, begun, err}
	p.started = append(p.started, outcome)
	return outcome
}

// Place is the step as it came back, each propose_edit's picture lines
// turned into their pictures' Markdown. A line whose picture can't be made
// is left out, and propose_edit's result says why.
func (p *InlinePictures) Place(step aicompat.StepResult) aicompat.StepResult {
	p.mu.Lock()
	defer p.mu.Unlock()
	clear(p.streams)
	if step.Done {
		return step
	}
	calls := slices.Clone(step.Calls)
	for i, t := range calls {
		if t.Kind == aicompat.ToolCalled && t.Name == ProposeEditToolName {
			if placed, ok := p.placeIn(t.Arguments); ok {
				calls[i].Arguments = placed
			}
		}
	}
	step.Calls = calls
	return step
}

// placeIn is one propose_edit's arguments with its picture lines placed;
// false when it has none.
func (p *InlinePictures) placeIn(arguments string) (string, bool) {
	args, ok := ParseArgs(arguments)
	if !ok {
		return "", false
	}
	text, ok := args.Str("text")
	if !ok || !strings.Contains(text, PictureScheme) {
		return "", false
	}
	placed := placeLines(text, func(line string, picture pictureLine) (started, error) {
		begun := p.start(strings.TrimSpace(line), picture)
		if begun.err != nil {
			p.tc.mu.Lock()
			p.tc.proposed.noteUnmade(begun.err.Error())
			p.tc.mu.Unlock()
		}
		return begun.picture, begun.err
	})
	args.Set("text", strings.Trim(placed, "\n"))
	return args.String(), true
}

// placeLines is text with each picture line begun by begin and placed as
// `![alt](textus-generated:ID)`; a line that can't be made is left out.
func placeLines(text string, begin func(line string, picture pictureLine) (started, error)) string {
	var out []string
	for _, line := range strings.Split(text, "\n") {
		picture, ok := readPictureLine(line)
		if !ok {
			out = append(out, line)
			continue
		}
		if begun, err := begin(line, picture); err == nil {
			out = append(out, "!["+strings.ReplaceAll(picture.alt, "[", "")+"]("+PlaceholderSrc(begun.id)+")")
		}
	}
	placed := strings.Join(out, "\n")
	// A line left out leaves its blank lines behind.
	for strings.Contains(placed, "\n\n\n") {
		placed = strings.ReplaceAll(placed, "\n\n\n", "\n\n")
	}
	return placed
}

// PlacePictures is text with each picture line begun on run and placed as
// `![alt](textus-generated:ID)`, for text that didn't stream through a
// request (a paper's sections). A line that can't be made - past the
// limit, or refused - is left out; why comes back with the text.
func PlacePictures(run *ImageRun, text string) (string, []string) {
	var unmade []string
	placed := placeLines(text, func(_ string, picture pictureLine) (started, error) {
		begun, err := run.beginPicture(picture.prompt, picture.aspect, picture.like)
		if err != nil {
			unmade = append(unmade, err.Error())
		}
		return begun, err
	})
	return placed, unmade
}

// partialString is the value of the string field key in JSON that is
// still streaming in, as far as it has come, and whether it is complete.
// False until its opening quote has arrived.
func partialString(json, key string) (string, bool, bool) {
	quoted := strconv.Quote(key)
	from, start := 0, -1
	for start < 0 {
		at := strings.Index(json[from:], quoted)
		if at < 0 {
			return "", false, false
		}
		at += from
		// A key, not the same letters inside a string (`\"text\"`).
		slashes := 0
		for i := at - 1; i >= 0 && json[i] == '\\'; i-- {
			slashes++
		}
		after := strings.TrimLeftFunc(json[at+len(quoted):], unicode.IsSpace)
		if slashes%2 == 0 {
			if value, ok := strings.CutPrefix(after, ":"); ok {
				if value, ok := strings.CutPrefix(strings.TrimLeftFunc(value, unicode.IsSpace), `"`); ok {
					start = len(json) - len(value)
					break
				}
			}
		}
		from = at + len(quoted)
	}
	var out strings.Builder
	rest := json[start:]
	for len(rest) > 0 {
		c := rest[0]
		switch {
		case c == '"':
			return out.String(), true, true
		case c != '\\':
			i := 1
			for i < len(rest) && rest[i] != '"' && rest[i] != '\\' {
				i++
			}
			out.WriteString(rest[:i])
			rest = rest[i:]
			continue
		}
		if len(rest) < 2 {
			return out.String(), false, true
		}
		escape := rest[1]
		rest = rest[2:]
		switch escape {
		case 'n':
			out.WriteByte('\n')
		case 't':
			out.WriteByte('\t')
		case 'r':
			out.WriteByte('\r')
		case 'b':
			out.WriteByte('\b')
		case 'f':
			out.WriteByte('\f')
		case 'u':
			unit, ok := hex4(rest)
			if !ok {
				// Cut off inside the escape: what came before it.
				return out.String(), false, true
			}
			rest = rest[4:]
			switch {
			case unit >= 0xd800 && unit < 0xdc00:
				low, ok := uint16(0), false
				if after, escaped := strings.CutPrefix(rest, `\u`); escaped {
					low, ok = hex4(after)
				}
				if !ok {
					return out.String(), false, true
				}
				rest = rest[6:]
				if r := utf16.DecodeRune(rune(unit), rune(low)); r != unicode.ReplacementChar {
					out.WriteRune(r)
				} else if low < 0xd800 || low >= 0xe000 {
					out.WriteRune(rune(low))
				}
			case unit >= 0xdc00 && unit < 0xe000:
				// A lone low surrogate is no character.
			default:
				out.WriteRune(rune(unit))
			}
		default:
			out.WriteByte(escape)
		}
	}
	return out.String(), false, true
}

// hex4 is the four hex digits s starts with, as a UTF-16 unit.
func hex4(s string) (uint16, bool) {
	if len(s) < 4 {
		return 0, false
	}
	n, err := strconv.ParseUint(s[:4], 16, 16)
	return uint16(n), err == nil
}
