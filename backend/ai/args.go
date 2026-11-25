package ai

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"io"
	"slices"
	"strings"
)

// Args is a tool call's arguments: a JSON object whose keys keep the order
// the model wrote them in, so a call changed here reads as before.
type Args struct {
	keys   []string
	values map[string]jsontext.Value
}

// NewArgs is an empty object.
func NewArgs() *Args { return &Args{values: map[string]jsontext.Value{}} }

// ParseArgs reads a call's arguments; false when they are not a JSON
// object.
func ParseArgs(arguments string) (*Args, bool) {
	dec := jsontext.NewDecoder(strings.NewReader(arguments), jsontext.AllowDuplicateNames(true))
	if tok, err := dec.ReadToken(); err != nil || tok.Kind() != '{' {
		return nil, false
	}
	a := NewArgs()
	for dec.PeekKind() != '}' {
		tok, err := dec.ReadToken()
		if err != nil {
			return nil, false
		}
		// The token is void once the decoder reads on.
		name := tok.String()
		value, err := dec.ReadValue()
		if err != nil {
			return nil, false
		}
		a.setRaw(name, value.Clone())
	}
	if _, err := dec.ReadToken(); err != nil {
		return nil, false
	}
	if _, err := dec.ReadToken(); err != io.EOF {
		return nil, false
	}
	return a, true
}

func (a *Args) setRaw(key string, value jsontext.Value) {
	if _, ok := a.values[key]; !ok {
		a.keys = append(a.keys, key)
	}
	a.values[key] = value
}

// Set sets key to v, in its place when it is there, else last.
func (a *Args) Set(key string, v any) {
	raw, err := json.Marshal(v)
	if err != nil {
		return
	}
	a.setRaw(key, raw)
}

// Remove takes key out.
func (a *Args) Remove(key string) {
	if _, ok := a.values[key]; ok {
		delete(a.values, key)
		a.keys = slices.DeleteFunc(a.keys, func(k string) bool { return k == key })
	}
}

// Has reports whether key is there, whatever its value.
func (a *Args) Has(key string) bool {
	_, ok := a.values[key]
	return ok
}

// Str is key's value when it is a string.
func (a *Args) Str(key string) (string, bool) {
	raw, ok := a.values[key]
	if !ok || raw.Kind() != '"' {
		return "", false
	}
	var s string
	if json.Unmarshal(raw, &s) != nil {
		return "", false
	}
	return s, true
}

// StrOr is key's string value, or "".
func (a *Args) StrOr(key string) string {
	s, _ := a.Str(key)
	return s
}

// Int is key's value when it is a whole number not below zero.
func (a *Args) Int(key string) (int, bool) {
	raw, ok := a.values[key]
	if !ok || raw.Kind() != '0' {
		return 0, false
	}
	var n uint64
	if json.Unmarshal(raw, &n) != nil {
		return 0, false
	}
	return int(min(n, 1<<53)), true
}

// Bool is key's value when it is true or false.
func (a *Args) Bool(key string) (bool, bool) {
	raw, ok := a.values[key]
	if !ok || (raw.Kind() != 't' && raw.Kind() != 'f') {
		return false, false
	}
	return raw.Kind() == 't', true
}

// Raw is key's value as written.
func (a *Args) Raw(key string) (jsontext.Value, bool) {
	raw, ok := a.values[key]
	return raw, ok
}

// Keys are the keys in order.
func (a *Args) Keys() []string { return slices.Clone(a.keys) }

// String is the arguments as compact JSON, in their order.
func (a *Args) String() string {
	b := []byte{'{'}
	for i, k := range a.keys {
		if i > 0 {
			b = append(b, ',')
		}
		b, _ = jsontext.AppendQuote(b, k)
		b = append(b, ':')
		v := a.values[k].Clone()
		_ = v.Compact()
		b = append(b, v...)
	}
	return string(append(b, '}'))
}

// reordered is a with keys first, in that order, then the rest as they
// were.
func (a *Args) reordered(keys ...string) *Args {
	out := NewArgs()
	for _, k := range keys {
		if v, ok := a.values[k]; ok {
			out.setRaw(k, v)
		}
	}
	for _, k := range a.keys {
		if !out.Has(k) {
			out.setRaw(k, a.values[k])
		}
	}
	return out
}
