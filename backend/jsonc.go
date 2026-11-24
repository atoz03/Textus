package backend

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"os"
	"strings"
)

// Settings files as other apps write them: JSON with comments and
// trailing commas, read field by field.

type jsonDoc = map[string]jsontext.Value

// readJSONObject reads a settings file, comments and trailing commas
// allowed; nil when it is not a JSON object.
func readJSONObject(path string) jsonDoc {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var doc jsonDoc
	if json.Unmarshal(raw, &doc) == nil {
		return doc
	}
	if json.Unmarshal([]byte(stripJSONC(string(raw))), &doc) == nil {
		return doc
	}
	return nil
}

// stripJSONC drops // and /* */ comments and trailing commas outside strings.
func stripJSONC(text string) string {
	var b strings.Builder
	inString, escaped := false, false
	for i := 0; i < len(text); i++ {
		c := text[i]
		if inString {
			b.WriteByte(c)
			switch {
			case escaped:
				escaped = false
			case c == '\\':
				escaped = true
			case c == '"':
				inString = false
			}
			continue
		}
		switch {
		case c == '"':
			inString = true
			b.WriteByte(c)
		case c == '/' && i+1 < len(text) && text[i+1] == '/':
			for i < len(text) && text[i] != '\n' {
				i++
			}
			b.WriteByte('\n')
		case c == '/' && i+1 < len(text) && text[i+1] == '*':
			end := strings.Index(text[i+2:], "*/")
			if end < 0 {
				return b.String()
			}
			i += end + 3
		case c == ',':
			j := i + 1
			for j < len(text) && strings.ContainsRune(" \t\r\n", rune(text[j])) {
				j++
			}
			if j < len(text) && (text[j] == '}' || text[j] == ']') {
				continue
			}
			b.WriteByte(c)
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

func docStr(doc jsonDoc, key string) *string {
	var s string
	if v, ok := doc[key]; ok && json.Unmarshal(v, &s) == nil {
		return &s
	}
	return nil
}

func docBool(doc jsonDoc, key string) *bool {
	var b bool
	if v, ok := doc[key]; ok && json.Unmarshal(v, &b) == nil {
		return &b
	}
	return nil
}

func docObj(doc jsonDoc, key string) jsonDoc {
	var o jsonDoc
	if v, ok := doc[key]; ok && json.Unmarshal(v, &o) == nil {
		return o
	}
	return nil
}

func docStrings(doc jsonDoc, key string) []string {
	var list []jsontext.Value
	out := []string{}
	if v, ok := doc[key]; ok && json.Unmarshal(v, &list) == nil {
		for _, item := range list {
			var s string
			if json.Unmarshal(item, &s) == nil {
				out = append(out, s)
			}
		}
	}
	return out
}
