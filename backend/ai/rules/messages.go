package rules

import (
	"embed"
	"strconv"
	"strings"
)

// Messages for the shared rules, which fire on text in any language, in the
// UI languages: messages/<code>.txt, lines of `rule.key = message` with
// `{0}` for values. English fills any gap.

//go:embed messages/*.txt
var messageFiles embed.FS

var messageTables = func() map[string]map[string]string {
	tables := map[string]map[string]string{}
	for _, l := range UILanguages {
		raw, err := messageFiles.ReadFile("messages/" + l.Code + ".txt")
		if err != nil {
			panic(err)
		}
		table := map[string]string{}
		for _, line := range Lines(string(raw)) {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			if key, text, ok := strings.Cut(line, " = "); ok {
				table[key] = text
			}
		}
		tables[l.Code] = table
	}
	return tables
}()

// message is the message key in the language of lang's text, values
// filled in.
func message(key string, lang Lang, values ...string) string {
	text, ok := messageTables[lang.UICode()][key]
	if !ok {
		text, ok = messageTables["en"][key]
	}
	if !ok {
		text = key
	}
	for i, v := range values {
		text = strings.ReplaceAll(text, "{"+strconv.Itoa(i)+"}", v)
	}
	return text
}
