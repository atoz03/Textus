package paper

import (
	"encoding/json/v2"
	"math"
)

// JSON read as serde_json reads a Value: any JSON at all, its members
// looked up by name whatever it is.

// object is a JSON value's members; nil for what isn't an object, so a
// member looked up in it is missing.
type object = map[string]any

// parseValue is the JSON in text as an object - nil when it is another
// kind of value - or why it isn't JSON.
func parseValue(text string) (object, error) {
	var v any
	if err := json.Unmarshal([]byte(text), &v); err != nil {
		return nil, err
	}
	o, _ := v.(object)
	return o, nil
}

// str is o's member key as text, "" when it isn't text.
func str(o object, key string) string {
	s, _ := o[key].(string)
	return s
}

// list is o's member key as a list, nil when it isn't one.
func list(o object, key string) []any {
	l, _ := o[key].([]any)
	return l
}

// wholeNumber is v as a whole number at or above zero, as serde's as_u64
// reads one; false for anything else.
func wholeNumber(v any) (uint64, bool) {
	f, ok := v.(float64)
	if !ok || f < 0 || f >= 1<<64 || f != math.Trunc(f) {
		return 0, false
	}
	return uint64(f), true
}
