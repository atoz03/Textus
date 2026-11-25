package ai

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"slices"
	"strings"

	"textus/backend/ai/rules"
)

// The tools that work on the open document - search it, read a section
// whole, check it, count text, comment on it, edit it - and use_skill.

const (
	// maxReadDocumentChars is the most one read_document call returns; a
	// longer range comes back cut at a line boundary with a pointer to
	// where to continue.
	maxReadDocumentChars = 12_000
	// maxConventionLines is the most lines check_conventions returns.
	maxConventionLines = 50
	// commentAdded is how a valid comment's result starts.
	commentAdded = "Comment added"
)

// documentTools are the tools that work on the open document, in every
// mode any of them is offered in. Comments change nothing, so every
// document mode has them; Review is structurally read-only: the model never
// receives the edit tool.
func documentTools() []Tool {
	return []Tool{
		{Spec: ToolSpecOf(SearchToolName, false), Access: ReadOnly, Modes: DocumentModes, Text: searchDocument},
		{Spec: ToolSpecOf(ReadDocumentToolName, false), Access: ReadOnly, Modes: DocumentModes, Text: readDocument},
		{Spec: ToolSpecOf(CheckConventionsToolName, false), Access: ReadOnly, Modes: DocumentModes, Text: checkConventions},
		{Spec: ToolSpecOf(MeasureTextToolName, false), Access: ReadOnly, Modes: DocumentModes, Text: measureText},
		{Spec: ToolSpecOf(AddCommentToolName, false), Access: Proposes, Modes: DocumentModes, Text: addComment},
		{Spec: ToolSpecOf(ProposeEditToolName, false), Access: Proposes, Modes: EditingModes, Text: proposeEdit},
	}
}

func unmarshalStrings(raw jsontext.Value, out *[]string) bool { return json.Unmarshal(raw, out) == nil }

// lineArg is a 1-based line argument, fallback when it isn't a whole
// number.
func lineArg(args *Args, key string, fallback int) int {
	if n, ok := args.Int(key); ok && n >= 0 {
		return n
	}
	return fallback
}

func clamp(n, low, high int) int { return max(low, min(n, high)) }

func searchDocument(tc *ToolContext, arguments string) string {
	query := ""
	if args, ok := ParseArgs(arguments); ok {
		query = args.StrOr("query")
	}
	if strings.TrimSpace(query) == "" {
		return "No query provided."
	}
	lower := strings.ToLower(query)
	var matches []string
	at := 0
	for i, line := range strings.SplitAfter(tc.Document.Text, "\n") {
		start := at
		at += len(line)
		line = strings.TrimRight(line, "\r\n")
		if line == "" && at == len(tc.Document.Text) && start == at {
			break
		}
		if !strings.Contains(strings.ToLower(line), lower) {
			continue
		}
		if id, ok := IDAt(tc.Document.Blocks, start); ok {
			matches = append(matches, fmt.Sprintf("%d [#%s]: %s", i+1, id, line))
		} else {
			matches = append(matches, fmt.Sprintf("%d: %s", i+1, line))
		}
		if len(matches) == 20 {
			break
		}
	}
	if len(matches) == 0 {
		return fmt.Sprintf("No matches found for \"%s\".", query)
	}
	return strings.Join(matches, "\n")
}

// readDocument reads part of the document verbatim: a section by its
// heading, or a line range. A long document is embedded only as an outline
// and excerpts.
func readDocument(tc *ToolContext, arguments string) string {
	args, ok := ParseArgs(arguments)
	if !ok {
		args = NewArgs()
	}
	d := tc.Document
	all := lines(d.Text)
	if len(all) == 0 {
		return "The document is empty."
	}
	total := len(all)
	var start, end int
	if heading := strings.TrimSpace(args.StrOr("heading")); heading != "" {
		first, last, ok := sectionRange(all, heading)
		if !ok {
			return fmt.Sprintf("No heading matching \"%s\". The outline in the document section lists them.", heading)
		}
		start, end = first, last
	} else {
		start = clamp(lineArg(args, "start_line", 1), 1, total) - 1
		end = clamp(lineArg(args, "end_line", total), start+1, total)
	}
	size, shownEnd := 0, start
	for _, line := range all[start:end] {
		if size > 0 && size+len(line)+1 > maxReadDocumentChars {
			break
		}
		size += len(line) + 1
		shownEnd++
	}
	header := fmt.Sprintf("Lines %d-%d of %d:", start+1, shownEnd, total)
	if shownEnd < end {
		header += fmt.Sprintf(" (cut for length - continue with start_line=%d)", shownEnd+1)
	}
	// The blocks in it marked with their ids, for an edit to name.
	starts := append(lineStarts(d.Text), len(d.Text))
	from := starts[min(start, len(starts)-1)]
	to := len(d.Text)
	if shownEnd < len(starts) {
		to = min(starts[shownEnd], len(d.Text))
	}
	marked := Annotate(d, from, to)
	if !strings.HasSuffix(marked, "\n") {
		marked += "\n"
	}
	return header + "\n" + marked
}

// visible is s with invisible characters spelled out, so a suggestion that
// only changes a space can be read: the model can't see a no-break space.
func visible(s string) string {
	var out strings.Builder
	for _, c := range s {
		switch c {
		case '\u00a0':
			out.WriteString("[no-break space]")
		case '\u202f':
			out.WriteString("[narrow no-break space]")
		case '\u3000':
			out.WriteString("[ideographic space]")
		case '\u200b':
			out.WriteString("[zero-width space]")
		case '\u200d':
			out.WriteString("[zero-width joiner]")
		case '\ufeff':
			out.WriteString("[BOM]")
		case '\n':
			out.WriteString("\\n")
		default:
			out.WriteRune(c)
		}
	}
	return out.String()
}

// checkConventions checks a passage against the document's writing
// conventions: a section, a line range, the whole document, or a draft
// (`text`) judged against the document's majority. Findings grouped by
// rule, at most maxConventionLines lines.
func checkConventions(tc *ToolContext, arguments string) string {
	args, ok := ParseArgs(arguments)
	if !ok {
		args = NewArgs()
	}
	level := rules.LevelStandard
	if strict, ok := args.Bool("strict"); ok && strict {
		level = rules.LevelStrict
	}
	var report rules.DocReport
	var scope string
	lineFrom, lineTo, ranged := 0, 0, false
	if text := args.StrOr("text"); strings.TrimSpace(text) != "" {
		// The draft as it will land, after the automatic fixes: checked
		// raw, fixes the app was about to make sent the model back to
		// propose again.
		text, _ = rules.ProposalText(tc.Document.Text, text, tc.Conventions)
		report = rules.CheckDocumentWith(text, tc.Document.Text, tc.Conventions, level, tc.Library)
		scope = "the text you gave"
	} else {
		all := lines(tc.Document.Text)
		total := max(len(all), 1)
		if heading := strings.TrimSpace(args.StrOr("heading")); heading != "" {
			first, last, ok := sectionRange(all, heading)
			if !ok {
				return fmt.Sprintf("No heading matching \"%s\". The outline in the document section lists them.", heading)
			}
			lineFrom, lineTo = first+1, last
		} else {
			lineFrom = clamp(lineArg(args, "start_line", 1), 1, total)
			lineTo = clamp(lineArg(args, "end_line", total), lineFrom, total)
		}
		ranged = true
		report = rules.CheckDocument(tc.Document.Text, tc.Conventions, level, tc.Library)
		scope = fmt.Sprintf("lines %d-%d", lineFrom, lineTo)
	}
	profile := "none (too little prose to tell the language)"
	if report.Profile != nil {
		profile = report.Profile.Describe()
	}
	var findings []rules.Finding
	for _, f := range report.Findings {
		if !ranged || f.Line != nil && *f.Line >= lineFrom && *f.Line <= lineTo {
			findings = append(findings, f)
		}
	}
	var out strings.Builder
	fmt.Fprintf(&out, "Writing conventions: %s.\n", profile)
	if len(findings) == 0 {
		fmt.Fprintf(&out, "No convention issues found in %s.", scope)
		return out.String()
	}
	fmt.Fprintf(&out, "%d finding(s) in %s. \"fix\" lines are mechanical and safe; \"suggest\" lines need judgement; \"check\" lines only point at something to look at.\n", len(findings), scope)
	// Grouped by rule, rules in order of first appearance.
	var order []string
	firstMessage := map[string]string{}
	for _, f := range findings {
		if !slices.Contains(order, f.Rule) {
			order = append(order, f.Rule)
			firstMessage[f.Rule] = f.Message
		}
	}
	written, shown := 0, 0
rulesLoop:
	for _, rule := range order {
		if written >= maxConventionLines {
			break
		}
		var group []rules.Finding
		for _, f := range findings {
			if f.Rule == rule {
				group = append(group, f)
			}
		}
		fmt.Fprintf(&out, "%s (%d): %s\n", rule, len(group), group[0].Message)
		written++
		for _, f := range group {
			if written >= maxConventionLines {
				break rulesLoop
			}
			line := ""
			if f.Line != nil {
				line = fmt.Sprintf("L%d", *f.Line)
			}
			kind := "check"
			switch {
			case f.Suggestion != nil && f.Fix == rules.FixSafe:
				kind = "fix"
			case f.Suggestion != nil:
				kind = "suggest"
			}
			what := fmt.Sprintf("\"%s\"", visible(f.Original))
			if f.Suggestion != nil {
				what = fmt.Sprintf("\"%s\" -> \"%s\"", visible(f.Original), visible(*f.Suggestion))
			}
			note := ""
			if f.Message != firstMessage[rule] {
				note = " (" + f.Message + ")"
			}
			fmt.Fprintf(&out, "  %s %s: %s%s\n", line, kind, what, note)
			written++
			shown++
		}
	}
	if shown < len(findings) {
		fmt.Fprintf(&out, "... and %d more; check a smaller range to see them.\n", len(findings)-shown)
	}
	// The bracketed names are for reading; told only those, the model
	// believed a plain space was a no-break one.
	if slices.ContainsFunc(findings, func(f rules.Finding) bool {
		return f.Suggestion != nil && strings.ContainsAny(*f.Suggestion, "\u00a0\u202f")
	}) {
		out.WriteString("A [no-break space] or [narrow no-break space] in a fix is that character, not an ordinary space: in propose_edit's JSON write it as \\u00a0 or \\u202f inside the string, and check the draft with `text` before proposing.\n")
	}
	return out.String()
}

// measureText is exact counts for a length limit.
func measureText(_ *ToolContext, arguments string) string {
	args, ok := ParseArgs(arguments)
	if !ok {
		args = NewArgs()
	}
	text, ok := args.Str("text")
	if !ok {
		return "No `text` given - pass the exact text to count."
	}
	if metric := args.StrOr("metric"); metric != "" && metric != "all" {
		m, ok := rules.MeasureText(text, metric)
		if !ok {
			return fmt.Sprintf("Unknown metric `%s`. Use one of: %s.", metric, strings.Join(rules.Metrics, ", "))
		}
		return m.Describe()
	}
	var counts []string
	for _, metric := range []string{"chars", "words", "cjk_chars", "utf8_bytes", "x_weighted", "bluesky"} {
		if m, ok := rules.MeasureText(text, metric); ok {
			counts = append(counts, m.Describe())
		}
	}
	return strings.Join(counts, "\n")
}

// addComment pins feedback to a passage instead of an edit to it. It
// changes nothing; its block is checked like propose_edit's so the card's
// "locate" lands on it.
func addComment(tc *ToolContext, arguments string) string {
	args, ok := ParseArgs(arguments)
	if !ok {
		args = NewArgs()
	}
	if strings.TrimSpace(args.StrOr("comment")) == "" {
		return "`comment` is required - the feedback itself."
	}
	if problem := CheckTarget(tc.Document, args); problem != "" {
		return problem
	}
	return commentAdded + " - the user sees it with a button that jumps to the passage. Don't repeat it in your reply."
}

// useSkill is a skill's full instructions on demand, with its declared
// reference-pack sections; the system prompt carries only names and
// descriptions. A pack loads by name too (`standards-fr#dialogue`).
func useSkill(tc *ToolContext, arguments string) string {
	name := ""
	if args, ok := ParseArgs(arguments); ok {
		name = strings.TrimSpace(args.StrOr("name"))
	}
	at := slices.IndexFunc(tc.Skills, func(s AgentSkill) bool { return s.Name == name })
	switch {
	case at >= 0 && tc.Skills[at].Invocation == InvokeUserOnly:
		return fmt.Sprintf("The skill `%s` runs only when the user types /%s. Handle this request without it.", name, name)
	case at >= 0:
		profile := ChosenProfile(tc.Conventions, tc.Document.Text)
		return fmt.Sprintf("Instructions for skill `%s` - follow them for this request:\n\n%s", name,
			ExpandSkill(&tc.Skills[at], tc.Document.Text, tc.UILanguage, "", profile))
	case name == AppGuide:
		return "The app guide - answer from it:\n\n" + appGuideText
	}
	if text, ok := LoadReference(name, tc.Document.Text, tc.UILanguage); ok {
		return fmt.Sprintf("Reference `%s` - apply it to this request:\n\n%s", name, text)
	}
	var available []string
	for i := range tc.Skills {
		if tc.Skills[i].OfferedToModel() {
			available = append(available, tc.Skills[i].Name)
		}
	}
	return fmt.Sprintf("No skill named `%s`. Available skills: %s. Reference packs: %s (add #section for one section).",
		name, strings.Join(available, ", "), strings.Join(ReferencePackNames(), ", "))
}
