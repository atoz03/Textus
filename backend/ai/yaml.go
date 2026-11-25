package ai

import (
	"strings"
	"unicode"

	"textus/backend/frontmatter"
)

// The front matter of a skill file or a library entry: the small YAML
// subset Textus reads (scalars, lists, folded and literal blocks).
// Hand-written: a mistake only costs one file's metadata, not a dependency.

// Frontmatter is a skill file's front matter: the top-level `key: value`
// pairs and the `metadata:` map's (Agent Skills keeps tool-specific keys
// there, string values only).
type Frontmatter struct {
	Fields   map[string]string
	Metadata map[string]string
}

// SplitFrontmatter splits a skill file into its front matter and body. No
// front matter (or an unterminated one) means the whole file is the body.
func SplitFrontmatter(content string) (*Frontmatter, string) {
	front, ok := frontmatter.Split(content)
	if !ok {
		return nil, frontmatter.WithoutBOM(content)
	}
	fm := &Frontmatter{Fields: map[string]string{}, Metadata: map[string]string{}}
	for _, kv := range parseYAMLMap(front.Lines) {
		switch {
		case !kv.nested:
			fm.Fields[kv.key] = kv.scalar
		case kv.key == "metadata":
			for _, inner := range parseYAMLMap(dedent(kv.block)) {
				if !inner.nested {
					fm.Metadata[inner.key] = inner.scalar
				}
			}
		}
	}
	return fm, front.Body
}

// fields is the front matter's fields, none for no front matter.
func (f *Frontmatter) fields() map[string]string {
	if f == nil {
		return map[string]string{}
	}
	return f.Fields
}

// yamlPair is one key: a scalar, or a nested block (a map or a list) as
// its raw lines.
type yamlPair struct {
	key    string
	scalar string
	nested bool
	block  []string
}

func indented(l string) bool { return strings.HasPrefix(l, " ") || strings.HasPrefix(l, "\t") }

// parseYAMLMap reads `key: value`, plain and quoted scalars, block scalars
// (`|`, `>`), and nested blocks returned raw.
func parseYAMLMap(lines []string) []yamlPair {
	var out []yamlPair
	for i := 0; i < len(lines); {
		line := lines[i]
		i++
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || indented(line) {
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		// The block under this key: every following indented or blank line.
		start := i
		for i < len(lines) && (strings.TrimSpace(lines[i]) == "" || indented(lines[i])) {
			i++
		}
		block := append([]string(nil), lines[start:i]...)
		for len(block) > 0 && strings.TrimSpace(block[len(block)-1]) == "" {
			block = block[:len(block)-1]
		}
		pair := yamlPair{key: key}
		switch {
		case value == "":
			first := ""
			for _, l := range block {
				if strings.TrimSpace(l) != "" {
					first = l
					break
				}
			}
			switch {
			case first == "":
			case strings.HasPrefix(strings.TrimLeftFunc(first, unicode.IsSpace), "- ") || strings.Contains(first, ":"):
				pair.nested, pair.block = true, block
			default:
				pair.scalar = foldPlain("", block)
			}
		case strings.HasPrefix(value, "|") || strings.HasPrefix(value, ">"):
			text := dedent(block)
			if strings.HasPrefix(value, "|") {
				pair.scalar = strings.TrimRightFunc(strings.Join(text, "\n"), unicode.IsSpace)
			} else {
				pair.scalar = foldLines(text)
			}
		case strings.HasPrefix(value, `"`) || strings.HasPrefix(value, "'"):
			raw := value
			for _, l := range block {
				raw += " " + strings.TrimSpace(l)
			}
			pair.scalar = unquoteYAML(raw)
		default:
			pair.scalar = foldPlain(value, block)
		}
		out = append(out, pair)
	}
	return out
}

// foldPlain is a plain scalar and its continuation lines folded into one
// line, minus a trailing ` # comment`.
func foldPlain(first string, rest []string) string {
	var parts []string
	for _, p := range append([]string{first}, rest...) {
		if p = strings.TrimSpace(p); p != "" {
			parts = append(parts, p)
		}
	}
	joined := strings.Join(parts, " ")
	if at := strings.Index(joined, " #"); at >= 0 {
		return strings.TrimRight(joined[:at], " \t")
	}
	return joined
}

// foldLines folds a `>` block: lines join with spaces, a blank line
// becomes a line break.
func foldLines(lines []string) string {
	var b strings.Builder
	pendingBreak := false
	for _, line := range lines {
		line = strings.TrimRightFunc(line, unicode.IsSpace)
		if line == "" {
			pendingBreak = true
			continue
		}
		if b.Len() > 0 {
			if pendingBreak {
				b.WriteByte('\n')
			} else {
				b.WriteByte(' ')
			}
		}
		pendingBreak = false
		b.WriteString(line)
	}
	return b.String()
}

// dedent is the block's lines with their common indentation removed.
func dedent(lines []string) []string {
	indent := -1
	for _, l := range lines {
		if strings.TrimSpace(l) == "" {
			continue
		}
		if n := len(l) - len(strings.TrimLeft(l, " \t")); indent < 0 || n < indent {
			indent = n
		}
	}
	out := make([]string, len(lines))
	for i, l := range lines {
		if strings.TrimSpace(l) != "" {
			out[i] = l[max(indent, 0):]
		}
	}
	return out
}

// unquoteYAML is a quoted scalar's value: `"..."` with backslash escapes,
// `'...'` with `”` for a quote. Whatever follows the closing quote is
// dropped.
func unquoteYAML(raw string) string {
	runes := []rune(raw)
	if len(runes) == 0 {
		return ""
	}
	quote := runes[0]
	var b strings.Builder
	for i := 1; i < len(runes); i++ {
		c := runes[i]
		switch {
		case quote == '"' && c == '\\':
			if i+1 < len(runes) {
				i++
				switch runes[i] {
				case 'n':
					b.WriteByte('\n')
				case 't':
					b.WriteByte('\t')
				default:
					b.WriteRune(runes[i])
				}
			}
		case quote == '\'' && c == '\'':
			if i+1 < len(runes) && runes[i+1] == '\'' {
				i++
				b.WriteByte('\'')
			} else {
				return b.String()
			}
		case quote == '"' && c == '"':
			return b.String()
		default:
			b.WriteRune(c)
		}
	}
	return b.String()
}

// StripHTMLComments strips `<!-- -->` comments from a `.textus/` file or
// the global agent.md before it reaches a prompt. An unterminated comment
// swallows the rest, as HTML would.
func StripHTMLComments(text string) string {
	var b strings.Builder
	rest := text
	for {
		start := strings.Index(rest, "<!--")
		if start < 0 {
			break
		}
		b.WriteString(rest[:start])
		end := strings.Index(rest[start:], "-->")
		if end < 0 {
			return b.String()
		}
		rest = rest[start+end+3:]
	}
	b.WriteString(rest)
	return b.String()
}

// FieldsAndBody are a file's front-matter fields (none without front
// matter) and its body.
func FieldsAndBody(content string) (map[string]string, string) {
	fm, body := SplitFrontmatter(content)
	return fm.fields(), body
}
