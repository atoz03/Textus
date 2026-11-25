package ai

import (
	"strings"
	"unicode"

	"textus/backend/frontmatter"
)

// WithMetadata is raw with its `metadata:` key `textus-<key>` set to value:
// replaced, or added under `metadata:` at its keys' indent, adding that or
// the front matter when missing. A file with no front matter keeps its
// file's name (none is written); a leading BOM goes.
func WithMetadata(raw, key, value string) string {
	raw = strings.TrimPrefix(raw, "\ufeff")
	field := MetaPrefix + "-" + key + ":"
	entry := func(indent string) string { return indent + field + ` "` + value + `"` }
	indentOf := func(l string) string { return l[:len(l)-len(strings.TrimLeftFunc(l, unicode.IsSpace))] }
	out := lines(raw)
	front, hasFront := frontmatter.Split(raw)
	if hasFront {
		// The closing `---` line.
		end := len(front.Lines) + 1
		at := func(match func(string) bool) int {
			for i, l := range out[:end] {
				if match(l) {
					return i
				}
			}
			return -1
		}
		if i := at(func(l string) bool { return strings.HasPrefix(strings.TrimLeftFunc(l, unicode.IsSpace), field) }); i >= 0 {
			out[i] = entry(indentOf(out[i]))
		} else if i := at(func(l string) bool { return strings.TrimRightFunc(l, unicode.IsSpace) == "metadata:" }); i >= 0 {
			// Beside the keys already there, at their indent.
			indent := "  "
			if i+1 < end {
				if d := indentOf(out[i+1]); d != "" {
					indent = d
				}
			}
			out = append(out[:i+1], append([]string{entry(indent)}, out[i+1:]...)...)
		} else {
			out = append(out[:end], append([]string{"metadata:", entry("  ")}, out[end:]...)...)
		}
	} else {
		out = append([]string{"---", "metadata:", entry("  "), "---"}, out...)
	}
	text := strings.Join(out, "\n")
	if strings.HasSuffix(raw, "\n") || !hasFront {
		text += "\n"
	}
	return text
}
