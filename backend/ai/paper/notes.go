package paper

import (
	"encoding/json/v2"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"textus/backend/ai"
	"textus/backend/ai/aicompat"
	"textus/backend/ai/web"
)

// add_note, the one tool of its own a paper's researcher has: a finding
// kept with the sources it rests on - web results by their numbers, local
// files by their paths.

const addNoteTool = "add_note"

// fileBytes are the most of a cited file read to check notes against.
const fileBytes = 200_000

// noteTaker is what one question's add_note keeps its notes against, and
// where.
type noteTaker struct {
	question string
	index    uint32
	plan     *web.Plan
	// kit is where cited files are looked for: the project folder, and the
	// paper's folder for `attached/...`.
	kit  *Kit
	desk *desk
	send Send
	// sentBack are claims sent back once for figures their sources don't
	// give: sent again unchanged, they are kept, the figures marked.
	sentBack map[string]bool
}

// tool is add_note, for a researcher's registry.
func (t *noteTaker) tool() ai.Tool {
	return ai.Tool{Spec: noteSpec(), Access: ai.Proposes, Modes: ai.EveryMode, Run: func(_ *ai.ToolContext, call aicompat.ToolCall) aicompat.ToolOutput {
		return aicompat.Text(t.keep(call))
	}}
}

// keep is one add_note call: its note kept, or why not.
func (t *noteTaker) keep(call aicompat.ToolCall) string {
	note, why := readNote(call.Arguments, t.question, t.plan, t.kit)
	if why == "" {
		note, why = t.ground(note)
	}
	if why != "" {
		return why
	}
	arguments, _ := json.Marshal(map[string]string{"claim": clip(note.Claim)})
	t.send(ActivityEvent{Question: t.index, Tool: addNoteTool, Arguments: string(arguments)})
	t.desk.mu.Lock()
	t.desk.notes = append(t.desk.notes, note)
	t.desk.mu.Unlock()
	return "Kept."
}

// ground is note checked against what its sources said when the
// researcher saw them: a quote they don't have is refused; figures they
// don't give are sent back once. Kept with the passage it rests on.
func (t *noteTaker) ground(note Note) (Note, string) {
	var seen []web.Seen
	for _, n := range note.Sources {
		if s, ok := t.plan.Seen(n); ok {
			seen = append(seen, s)
		}
	}
	if len(seen) == 0 {
		return note, ""
	}
	words := make([]string, len(seen))
	for i, s := range seen {
		words[i] = s.Text
	}
	var cited strings.Builder
	for _, n := range note.Sources {
		fmt.Fprintf(&cited, "[%d]", n)
	}
	if note.Quote != nil && !slices.ContainsFunc(words, func(w string) bool { return hasQuote(w, *note.Quote) }) {
		return note, fmt.Sprintf("Not kept: the quote isn't in what %s says. Copy it word for word from the source, or leave `quote` out.", cited.String())
	}
	said := note.Claim + " " + note.Evidence
	missing := missingFigures(said, words)
	if len(missing) > 0 && !t.sentBack[said] {
		t.sentBack[said] = true
		return note, fmt.Sprintf("Not kept: %s isn't in what %s says. Give each figure as its source does and cite the source it comes from, or leave it out. Sent again unchanged, the note is kept with the figure marked unconfirmed.", strings.Join(missing, ", "), cited.String())
	}
	note.Unmatched = missing
	note.Grounding = SnippetGrounded
	if slices.ContainsFunc(seen, func(s web.Seen) bool { return s.Reach == web.Read }) {
		note.Grounding = ReadGrounded
	}
	restsOn := said + " " + deref(note.Quote)
	for _, w := range words {
		if e, ok := excerpt(w, restsOn); ok {
			note.Excerpt = &e
			break
		}
	}
	return note, ""
}

// fileText is a file's opening as text, for checking notes against; false
// for one that can't be read or isn't text.
func fileText(path string) (string, bool) {
	f, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, fileBytes))
	if err != nil {
		return "", false
	}
	text := strings.ToValidUTF8(string(raw), "\uFFFD")
	if strings.ContainsRune(text, 0) {
		return "", false
	}
	return text, true
}

// noteSpec is what add_note looks like to the model.
func noteSpec() aicompat.ToolSpec {
	str := func(description string) map[string]any {
		return map[string]any{"type": "string", "description": description}
	}
	return aicompat.ToolSpec{
		Name:        addNoteTool,
		Description: "Keep one finding for the paper: a claim, the evidence for it, and the sources it rests on. Call it once per finding as you go - several calls in one step are fine. Only what you keep with add_note reaches the paper; your replies don't.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"claim":    str("The finding, in one or two sentences"),
				"evidence": str("What supports it: figures, results, examples, with their dates"),
				"quote":    str("Words taken verbatim from a source, if worth quoting"),
				"sources": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Each source's number from the search or page results (\"3\"), its full address when it has none, or a file's path as read_file reads it (attached/..., references/...)",
				},
				"leads": map[string]any{
					"type":        "array",
					"description": "Pictures or data in the sources a figure could use",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"kind":        map[string]any{"type": "string", "enum": []any{"image", "data"}},
							"description": map[string]any{"type": "string"},
							"url":         map[string]any{"type": "string"},
						},
						"required": []any{"kind", "description"},
					},
				},
			},
			"required": []any{"claim", "evidence", "sources"},
		},
	}
}

// readNote is an add_note call as a note, or why it can't be one. Its
// sources must be numbers the web plan has, addresses (numbered here), or
// files that are there.
func readNote(arguments, question string, plan *web.Plan, kit *Kit) (Note, string) {
	args, err := parseValue(arguments)
	if err != nil {
		return Note{}, err.Error()
	}
	text := func(key string) string { return strings.TrimSpace(str(args, key)) }
	claim, evidence := text("claim"), text("evidence")
	if claim == "" || evidence == "" {
		return Note{}, "Pass `claim` and `evidence`."
	}
	var cited []string
	for _, c := range list(args, "sources") {
		if s, ok := c.(string); ok {
			cited = append(cited, s)
		}
	}
	var sources []uint32
	// Cited by addresses no source had: nothing shows what they say.
	unread := len(cited) > 0
	for _, c := range cited {
		var n uint32
		var ok bool
		if file, found := kit.citedFileOf(c); found {
			n, ok = plan.AddSource(file.address, file.title), true
			if _, seen := plan.Seen(n); !seen {
				if text, read := fileText(file.path); read {
					plan.Saw(n, text, web.Read)
				}
			}
			unread = false
		} else {
			trimmed := strings.TrimSpace(c)
			address := ""
			if strings.HasPrefix(trimmed, "http") {
				address = trimmed
			} else if number, isNumber := parseNumber(strings.Trim(c, "[] ")); isNumber {
				address, _ = plan.SourceAddress(number)
			}
			if address != "" && isSearchPage(address) {
				return Note{}, fmt.Sprintf("%s is a search page, not a source: read the page or paper a result points to, and cite that.", debugQuoted(c))
			}
			known := !strings.HasPrefix(trimmed, "http") || plan.Knows(trimmed)
			unread = unread && !known
			n, ok = plan.SourceNumber(c)
		}
		switch {
		case ok && !slices.Contains(sources, n):
			sources = append(sources, n)
		case !ok:
			return Note{}, fmt.Sprintf("No source is numbered %s: cite the number a search or page result has, the page's full address, or the path of a file you read.", debugQuoted(c))
		}
	}
	if len(sources) == 0 {
		return Note{}, "A note needs its sources: the numbers of the results it rests on. Search or read first."
	}
	var leads []Lead
	for _, l := range list(args, "leads") {
		item, _ := l.(object)
		var kind LeadKind
		switch str(item, "kind") {
		case "image":
			kind = ImageLead
		case "data":
			kind = DataLead
		default:
			continue
		}
		description, ok := item["description"].(string)
		if !ok {
			continue
		}
		lead := Lead{Kind: kind, Description: strings.TrimSpace(description)}
		if u, ok := item["url"].(string); ok {
			lead.URL = &u
		}
		leads = append(leads, lead)
	}
	note := Note{Question: question, Claim: claim, Evidence: evidence, Sources: sources, Leads: leads, Grounding: Unchecked}
	if quote := text("quote"); quote != "" {
		note.Quote = &quote
	}
	if unread {
		note.Grounding = Unread
	}
	return note, ""
}

// debugQuoted is s quoted as Rust's {:?} quotes a string.
func debugQuoted(s string) string {
	var out strings.Builder
	out.WriteByte('"')
	for _, c := range s {
		switch c {
		case '"':
			out.WriteString(`\"`)
		case '\\':
			out.WriteString(`\\`)
		case '\n':
			out.WriteString(`\n`)
		case '\r':
			out.WriteString(`\r`)
		case '\t':
			out.WriteString(`\t`)
		default:
			if c < 0x20 || c == 0x7f {
				fmt.Fprintf(&out, `\u{%x}`, c)
			} else {
				out.WriteRune(c)
			}
		}
	}
	out.WriteByte('"')
	return out.String()
}
