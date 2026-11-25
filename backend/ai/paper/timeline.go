package paper

import (
	"fmt"
	"strings"
	"unicode"
)

// A paper's long timelines, redrawn as what fits a page's column.

// acrossPeriods are the most periods a timeline is drawn across the page
// with: one with more, laid out side by side, was scaled down past
// reading.
const acrossPeriods = 4

type period struct {
	name   string
	events []string
}

type timelineSection struct {
	name    string
	named   bool
	periods []period
}

// uprightTimelines is markdown with every ```mermaid timeline of more than
// acrossPeriods periods, or written `timeline TD`, as a top-down
// flowchart, one box a period and one group a section. Mermaid's own
// top-down timeline wraps no text and draws a section's bar over the
// period before it.
func uprightTimelines(markdown string) string {
	lines := strings.Split(markdown, "\n")
	out := make([]string, 0, len(lines))
	for i := 0; i < len(lines); {
		out = append(out, lines[i])
		trimmed := strings.TrimLeftFunc(lines[i], unicode.IsSpace)
		opens := strings.HasPrefix(trimmed, "```") && strings.TrimSpace(strings.TrimLeft(trimmed, "`")) == "mermaid"
		i++
		if !opens {
			continue
		}
		end := len(lines)
		for j := i; j < len(lines); j++ {
			if strings.HasPrefix(strings.TrimLeftFunc(lines[j], unicode.IsSpace), "```") {
				end = j
				break
			}
		}
		if chart, ok := upright(lines[i:end]); ok {
			out = append(out, chart)
		} else {
			out = append(out, lines[i:end]...)
		}
		i = end
	}
	return strings.Join(out, "\n")
}

// upright is a timeline's body as a top-down flowchart; false when it is
// another diagram, laid out across (`timeline LR`), or short enough to be.
func upright(body []string) (string, bool) {
	var lines []string
	for _, l := range body {
		if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "%%") {
			lines = append(lines, l)
		}
	}
	if len(lines) == 0 {
		return "", false
	}
	var down bool
	switch lines[0] {
	case "timeline":
	case "timeline TD":
		down = true
	default:
		return "", false
	}
	title, titled := "", false
	sections := []timelineSection{{}}
	for _, line := range lines[1:] {
		if text, ok := strings.CutPrefix(line, "title "); ok {
			title, titled = strings.TrimSpace(text), true
			continue
		}
		if strings.HasPrefix(line, "accTitle") || strings.HasPrefix(line, "accDescr") {
			continue
		}
		if name, ok := strings.CutPrefix(line, "section "); ok {
			sections = append(sections, timelineSection{name: strings.TrimSpace(name), named: true})
			continue
		}
		periods := &sections[len(sections)-1].periods
		parts := strings.Split(line, ":")
		first := strings.TrimSpace(parts[0])
		var events []string
		for _, e := range parts[1:] {
			if e = strings.TrimSpace(e); e != "" {
				events = append(events, e)
			}
		}
		switch last := len(*periods) - 1; {
		// `: event`, and a line with no colon at all, which writers mean
		// as the next event though Mermaid reads it as a period.
		case last >= 0 && first == "":
			(*periods)[last].events = append((*periods)[last].events, events...)
		case last >= 0 && !strings.Contains(line, ":"):
			(*periods)[last].events = append((*periods)[last].events, line)
		default:
			*periods = append(*periods, period{name: first, events: events})
		}
	}
	count := 0
	for _, s := range sections {
		count += len(s.periods)
	}
	if count == 0 || !down && count <= acrossPeriods {
		return "", false
	}
	if !titled {
		return flowchart(nil, sections), true
	}
	return flowchart(&title, sections), true
}

func flowchart(title *string, sections []timelineSection) string {
	var out strings.Builder
	if title != nil {
		quoted := strings.ReplaceAll(strings.ReplaceAll(*title, `\`, `\\`), `"`, `\"`)
		fmt.Fprintf(&out, "---\ntitle: \"%s\"\n---\n", quoted)
	}
	out.WriteString("flowchart TB")
	var ids []string
	for k, section := range sections {
		if len(section.periods) == 0 {
			continue
		}
		indent := "    "
		if section.named {
			fmt.Fprintf(&out, "\n    subgraph s%d[\"%s\"]", k, timelineLabel(section.name))
			indent = "        "
		}
		for _, p := range section.periods {
			id := fmt.Sprintf("p%d", len(ids)+1)
			text := "<b>" + timelineLabel(p.name) + "</b>"
			for _, event := range p.events {
				text += "<br>" + timelineLabel(event)
			}
			fmt.Fprintf(&out, "\n%s%s[\"%s\"]", indent, id, text)
			ids = append(ids, id)
		}
		if section.named {
			out.WriteString("\n    end")
		}
	}
	if len(ids) > 1 {
		out.WriteString("\n    " + strings.Join(ids, " --> "))
	}
	return out.String()
}

// timelineLabel is text for inside a quoted Mermaid label, which reads `"`
// and `<` itself.
func timelineLabel(text string) string {
	return strings.NewReplacer(`"`, "#quot;", "<", "#lt;", ">", "#gt;").Replace(text)
}
