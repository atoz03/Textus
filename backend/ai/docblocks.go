package ai

import (
	"fmt"
	"strings"
	"unicode"
)

// The document's blocks and their ids, as the model sees them: each block
// starts with `[#k3f9] `, and an edit names its block instead of quoting it.
// The blocks are the editor's top-level nodes, and the frontend places an
// edit by its ids, never by searching for its text.

const (
	// PicturesKeptKey is an argument added to a run's rewrite that tried to
	// swap a picture: how many it took out. Not the model's to write.
	PicturesKeptKey = "pictures_kept"
	// PicturesInRunKey is an argument added to a run's rewrite with
	// pictures in it: their blocks' ids, which the rewrite keeps as they
	// were. Not the model's to write.
	PicturesInRunKey = "pictures_in_run"
)

// SentBlock is one block as the request carries it (SentBlock in
// src/platform/bindings/ai-agent.ts).
type SentBlock struct {
	ID       string `json:"id"`
	Markdown string `json:"markdown"`
	// Kind is what the editor reads it as (BlockKind): "paragraph",
	// "picture", "table"…
	Kind string `json:"kind"`
}

// Block is one block: its id, its kind and where its markdown is in the
// document's text, in bytes.
type Block struct {
	ID         string
	Start, End int
	Kind       string
}

// Document is the document a request is about: its text and its blocks.
type Document struct {
	Text   string
	Blocks []Block
}

// blockSeparator is what blocks are joined with: one blank line.
const blockSeparator = "\n\n"

// DocumentFromBlocks is the document from the editor's blocks, their
// markdown joined.
func DocumentFromBlocks(sent []SentBlock) Document {
	var text strings.Builder
	blocks := make([]Block, 0, len(sent))
	for i, b := range sent {
		if i > 0 {
			text.WriteString(blockSeparator)
		}
		start := text.Len()
		text.WriteString(b.Markdown)
		blocks = append(blocks, Block{ID: strings.TrimLeft(b.ID, "#"), Start: start, End: text.Len(), Kind: b.Kind})
	}
	return Document{Text: text.String(), Blocks: blocks}
}

// DocumentFromMarkdown is a document no editor stands behind (a project
// file, a test's) in its paragraphs, a picture alone in one read as one.
func DocumentFromMarkdown(text string) Document {
	spans := Paragraphs(text)
	texts := make([]string, len(spans))
	for i, s := range spans {
		texts[i] = text[s[0]:s[1]]
	}
	ids := blockIDs(texts)
	blocks := make([]Block, len(spans))
	for i, s := range spans {
		kind := "paragraph"
		if IsPicture(texts[i]) {
			kind = "picture"
		}
		blocks[i] = Block{ID: ids[i], Start: s[0], End: s[1], Kind: kind}
	}
	return Document{Text: text, Blocks: blocks}
}

// BlockText is a block's markdown.
func (d *Document) BlockText(b Block) string { return d.Text[b.Start:b.End] }

// Run is the run block (to lastBlock, when given) names: its first and
// last index. False when an id names no block, or the run is backwards.
func (d *Document) Run(block string, lastBlock *string) (int, int, bool) {
	find := func(id string) int {
		id = CleanID(id)
		for i, b := range d.Blocks {
			if b.ID == id {
				return i
			}
		}
		return -1
	}
	first := find(block)
	if first < 0 {
		return 0, 0, false
	}
	last := first
	if lastBlock != nil {
		if last = find(*lastBlock); last < first {
			return 0, 0, false
		}
	}
	return first, last, true
}

// RunText is the run's text, from its first block's start to its last's
// end.
func (d *Document) RunText(first, last int) string {
	return d.Text[d.Blocks[first].Start:d.Blocks[last].End]
}

// lastBlockOf is a call's `last_block`, when it is a string.
func lastBlockOf(args *Args) *string {
	if s, ok := args.Str("last_block"); ok {
		return &s
	}
	return nil
}

// TargetSpan is where a call that names its block points, in bytes: the
// run, or the words `anchor` quotes once inside it. False when the ids
// name no run, or the words aren't in it exactly once.
func (d *Document) TargetSpan(args *Args) (int, int, bool) {
	block, ok := args.Str("block")
	if !ok {
		return 0, 0, false
	}
	first, last, ok := d.Run(block, lastBlockOf(args))
	if !ok {
		return 0, 0, false
	}
	start := d.Blocks[first].Start
	text := d.RunText(first, last)
	if quote, ok := args.Str("anchor"); ok && quote != "" && quote != text {
		if strings.Count(text, quote) != 1 {
			return 0, 0, false
		}
		at := start + strings.Index(text, quote)
		return at, at + len(quote), true
	}
	return start, start + len(text), true
}

// NoBlock is told when a call names no block for document text.
const NoBlock = "Not proposed: name the passage by its block - `block` takes the id shown before it, like `k3f9` from `[#k3f9]` - and, for a few words inside it, quote them in `anchor`."

// CheckTarget is "" when a call's `block` (with `last_block` and `anchor`)
// names a place in d, else the tool result telling the model how to fix
// it. The frontend places the call by the same ids, so a call that passes
// here lands.
func CheckTarget(d *Document, args *Args) string {
	block, ok := args.Str("block")
	if !ok {
		return NoBlock
	}
	last := lastBlockOf(args)
	if _, _, ok := d.Run(block, last); !ok {
		if last != nil {
			return fmt.Sprintf("Not proposed: `%s` to `%s` isn't a run of blocks the document has now - an id is from before a change or mistyped, or `last_block` comes before `block`. Use ids the document shows now, the first as `block`.", block, *last)
		}
		return fmt.Sprintf("Not proposed: no block has the id `%s` - the document has changed since you saw it, or the id is mistyped. Use an id the document shows now, before the block (`[#k3f9]`).", block)
	}
	if _, _, ok := d.TargetSpan(args); !ok {
		return "Not proposed: the words in `anchor` aren't in that block exactly once. Quote a few words exactly as the block has them - enough to occur once in it - or leave `anchor` out to mean the whole block."
	}
	return ""
}

// CleanID is an id as a model may write it (`k3f9`, `#k3f9` or `[#k3f9]`)
// as the id.
func CleanID(id string) string {
	id = strings.TrimLeft(strings.TrimSpace(id), "[")
	id = strings.TrimLeft(strings.TrimRight(id, "]"), "#")
	return strings.TrimSpace(id)
}

// hashID is FNV-1a over the block's bytes, as base 36, matching the
// frontend's hashId.
func hashID(text string) string {
	h := uint32(0x811c9dc5)
	for i := 0; i < len(text); i++ {
		h ^= uint32(text[i])
		h *= 0x01000193
	}
	out := make([]byte, 4)
	for i := range out {
		d := byte(h % 36)
		if d < 10 {
			out[i] = '0' + d
		} else {
			out[i] = 'a' + d - 10
		}
		h /= 36
	}
	return string(out)
}

// blockIDs is each block's id: its hash, `-2`, `-3`… where the same text
// came before.
func blockIDs(texts []string) []string {
	seen := map[string]int{}
	ids := make([]string, len(texts))
	for i, text := range texts {
		base := hashID(text)
		seen[base]++
		if n := seen[base]; n == 1 {
			ids[i] = base
		} else {
			ids[i] = fmt.Sprintf("%s-%d", base, n)
		}
	}
	return ids
}

// fenceOpening is the marker that closes a fence line opens: ``` or ~~~
// code, $$ maths; "" when it opens none. Blank lines don't end the block.
func fenceOpening(line string) string {
	t := strings.TrimLeftFunc(line, unicode.IsSpace)
	for _, marker := range []string{"```", "~~~", "$$"} {
		if strings.HasPrefix(t, marker) {
			return marker
		}
	}
	return ""
}

// opensFence is the marker that closes the fence line opens, "" when it
// opens none or (a one-line `$$x$$`) closes itself.
func opensFence(line string) string {
	marker := fenceOpening(line)
	if marker == "" {
		return ""
	}
	rest := strings.TrimLeftFunc(line, unicode.IsSpace)[len(marker):]
	if marker == "$$" && strings.HasSuffix(strings.TrimRightFunc(rest, unicode.IsSpace), "$$") {
		return ""
	}
	return marker
}

// Paragraphs are text with no editor behind it in its paragraphs: runs of
// lines between blank lines, a fenced code or maths block whole, as byte
// spans.
func Paragraphs(text string) [][2]int {
	var spans [][2]int
	start, end := -1, 0
	fence := ""
	for offset := 0; offset < len(text); {
		raw := text[offset:]
		if i := strings.IndexByte(raw, '\n'); i >= 0 {
			raw = raw[:i+1]
		}
		line := strings.TrimRight(raw, "\r\n")
		lineEnd := offset + len(line)
		switch {
		case fence != "":
			end = lineEnd
			if strings.HasPrefix(strings.TrimLeftFunc(line, unicode.IsSpace), fence) {
				fence = ""
			}
		case strings.TrimSpace(line) == "":
			if start >= 0 {
				spans = append(spans, [2]int{start, end})
				start = -1
			}
		default:
			if start < 0 {
				start = offset
			}
			end = lineEnd
			fence = opensFence(line)
		}
		offset += len(raw)
	}
	if start >= 0 {
		spans = append(spans, [2]int{start, end})
	}
	return spans
}

// Annotate is the document's text from `from` to `to`, each block that
// starts in it marked with its id.
func Annotate(d *Document, from, to int) string {
	var out strings.Builder
	at := from
	for _, b := range d.Blocks {
		if b.Start < from || b.Start >= to {
			continue
		}
		out.WriteString(d.Text[at:b.Start])
		out.WriteString("[#" + b.ID + "] ")
		at = b.Start
	}
	out.WriteString(d.Text[at:to])
	return out.String()
}

// IDAt is the id of the block a byte offset falls in.
func IDAt(blocks []Block, offset int) (string, bool) {
	for _, b := range blocks {
		if offset >= b.Start && offset <= b.End {
			return b.ID, true
		}
	}
	return "", false
}

// stripMarkers takes out `[#id] ` markers a model copied into quoted or
// new text.
func stripMarkers(text string) string {
	var out strings.Builder
	rest := text
	for {
		i := strings.Index(rest, "[#")
		if i < 0 {
			break
		}
		out.WriteString(rest[:i])
		after := rest[i+2:]
		n := strings.IndexByte(after, ']')
		if n >= 4 && n <= 8 && isMarkerID(after[:n]) {
			rest = strings.TrimPrefix(after[n+1:], " ")
		} else {
			out.WriteString("[#")
			rest = after
		}
	}
	out.WriteString(rest)
	return out.String()
}

func isMarkerID(id string) bool {
	for i := 0; i < len(id); i++ {
		c := id[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '-') {
			return false
		}
	}
	return true
}

// cleaned is a call's arguments, markers copied into keys taken out and
// the fields a strict schema (OpenAI's) writes empty left out.
func cleaned(arguments string, keys ...string) (*Args, bool) {
	args, ok := ParseArgs(arguments)
	if !ok {
		return nil, false
	}
	for _, key := range keys {
		if s, ok := args.Str(key); ok && strings.Contains(s, "[#") {
			args.Set(key, stripMarkers(s))
		}
	}
	for _, key := range []string{"anchor", "block", "last_block"} {
		if s, ok := args.Str(key); ok && s == "" {
			args.Remove(key)
		}
	}
	return args, true
}

// reorder puts the keys in the order the model writes them, so the call
// reads as before.
func reorder(args *Args) string {
	return args.reordered("action", "block", "last_block", "anchor", "text").String()
}

// ResolveEditArguments is a propose_edit call's arguments with what its
// ids stand for filled in, the ids kept. `block` alone names the run; with
// `anchor` and `replace`/`delete` the whole run is replaced with those
// words changed; with an insert the words stay the anchor. Ids or words
// that don't match are left for propose_edit to report. Arguments that
// aren't JSON are returned as they were.
func ResolveEditArguments(d *Document, arguments string) string {
	args, ok := cleaned(arguments, "anchor", "text")
	if !ok {
		return arguments
	}
	block, ok := args.Str("block")
	if !ok {
		return args.String()
	}
	first, last, ok := d.Run(block, lastBlockOf(args))
	if !ok {
		return args.String()
	}
	whole := d.RunText(first, last)
	args.Set("block", d.Blocks[first].ID)
	if last > first {
		args.Set("last_block", d.Blocks[last].ID)
	} else {
		args.Remove("last_block")
	}
	action := args.StrOr("action")
	if part, ok := args.Str("anchor"); ok && part != "" && part != whole {
		if strings.Count(whole, part) != 1 || action == "insert_after" || action == "insert_before" {
			// Not in the run as quoted (propose_edit says so), or the
			// words an insertion goes by.
			return reorder(args)
		}
		if action == "replace" || action == "delete" {
			with := args.StrOr("text")
			if action == "delete" {
				with = ""
			}
			args.Set("action", "replace")
			args.Set("text", strings.Replace(whole, part, with, 1))
		}
	}
	if text, ok := args.Str("text"); ok && action == "replace" && last > first {
		var run []string
		var pictures []string
		for _, b := range d.Blocks[first : last+1] {
			run = append(run, d.BlockText(b))
			if IsPicture(d.BlockText(b)) {
				pictures = append(pictures, b.ID)
			}
		}
		kept, swapped := KeepPictures(run, text, true)
		args.Set("text", kept)
		// For the tool's result: the model is told to use generate_image.
		if swapped > 0 {
			args.Set(PicturesKeptKey, swapped)
		}
		if len(pictures) > 0 {
			args.Set(PicturesInRunKey, pictures)
		}
	}
	args.Set("anchor", whole)
	return reorder(args)
}

// ResolveTargetArguments is an add_comment or generate_image call's
// arguments with its block filled in: an `anchor` the block holds once
// stays the anchor, otherwise the anchor is the whole block. An id that
// names no block is left for the tool to report.
func ResolveTargetArguments(d *Document, arguments string) string {
	args, ok := cleaned(arguments, "anchor")
	if !ok {
		return arguments
	}
	id, ok := args.Str("block")
	if !ok {
		return args.String()
	}
	index, _, ok := d.Run(id, nil)
	if !ok {
		return args.String()
	}
	b := d.Blocks[index]
	whole := d.BlockText(b)
	args.Set("block", b.ID)
	if phrase, ok := args.Str("anchor"); !ok || phrase == whole || strings.Count(whole, phrase) != 1 {
		args.Set("anchor", whole)
	}
	return reorder(args)
}
