package aicompat

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"strings"
)

// Reading the providers' JSON as it comes: decoded into any, walked by key.

// lenient reads what servers send: a name twice, or a broken character,
// is no reason to drop an event.
var lenient = json.JoinOptions(jsontext.AllowDuplicateNames(true), jsontext.AllowInvalidUTF8(true))

// decode is raw decoded into any; nil when it isn't JSON.
func decode(raw string) any {
	var v any
	if json.Unmarshal([]byte(raw), &v, lenient) != nil {
		return nil
	}
	return v
}

// at is the value at path (object keys) in v; nil where there is none.
func at(v any, path ...string) any {
	for _, key := range path {
		m, ok := v.(map[string]any)
		if !ok {
			return nil
		}
		v = m[key]
	}
	return v
}

// str is v as a string.
func str(v any) (string, bool) {
	s, ok := v.(string)
	return s, ok
}

// strAt is the string at path, "" for none.
func strAt(v any, path ...string) string {
	s, _ := str(at(v, path...))
	return s
}

// u64 is v as a whole, non-negative number.
func u64(v any) (uint64, bool) {
	f, ok := v.(float64)
	if !ok || f < 0 || f != float64(uint64(f)) {
		return 0, false
	}
	return uint64(f), true
}

// list is v as an array, nil for none.
func list(v any) []any {
	l, _ := v.([]any)
	return l
}

// object is v as an object, nil for none.
func object(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

// compact is v as compact JSON, object keys sorted.
func compact(v any) string {
	raw, err := json.Marshal(v, json.Deterministic(true))
	if err != nil {
		return ""
	}
	return string(raw)
}

// rawOf is v as a raw JSON value, nil for nil.
func rawOf(v any) jsontext.Value {
	if v == nil {
		return nil
	}
	raw, err := json.Marshal(v, json.Deterministic(true))
	if err != nil {
		return nil
	}
	return raw
}

// validJSON reports whether s is one JSON value.
func validJSON(s string) bool { return jsontext.Value(strings.TrimSpace(s)).IsValid() }

// argumentsOf is a call's JSON arguments as an object to send, {} when
// they don't parse.
func argumentsOf(arguments string) any {
	if v, ok := decode(arguments).(map[string]any); ok {
		return v
	}
	return map[string]any{}
}
