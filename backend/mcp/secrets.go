package mcp

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
)

// The secrets of the MCP servers - their environment's and headers'
// values, often tokens - kept with the AI providers' keys, never in the
// settings: there each stands as Mask, so no window holds it once saved.

// Mask is what a saved secret reads as in the settings (and the form).
const Mask = "••••••••"

// StoreID is their place among the providers' keys: an id never shown as a
// provider.
const StoreID = "__mcp"

var secretFields = []string{"env", "headers"}

// Kept are the secrets by server id, then field (env, headers), then name.
type Kept map[string]map[string]map[string]string

// ParseKept reads the store's text; nothing for none.
func ParseKept(text string) Kept {
	var k Kept
	if text == "" || json.Unmarshal([]byte(text), &k) != nil || k == nil {
		return Kept{}
	}
	return k
}

func (k Kept) lookup(id, field, name string) (string, bool) {
	v, ok := k[id][field][name]
	return v, ok
}

func (k Kept) put(id, field, name, secret string) {
	if k[id] == nil {
		k[id] = map[string]map[string]string{}
	}
	if k[id][field] == nil {
		k[id][field] = map[string]string{}
	}
	k[id][field][name] = secret
}

// Seal moves every value typed into servers (the settings' list) to the
// secrets kept, Mask left in its place: what the store keeps after it (only
// the secrets still named, from was for a Mask), the list as sealed, and
// whether the list changed.
func Seal(was Kept, servers jsontext.Value) (Kept, jsontext.Value, bool) {
	kept := Kept{}
	var list []jsontext.Value
	if json.Unmarshal(servers, &list) != nil {
		return kept, servers, false
	}
	mask := jsontext.Value(`"` + Mask + `"`)
	changed := false
	for i, raw := range list {
		var server map[string]jsontext.Value
		var id string
		if json.Unmarshal(raw, &server) != nil || server["id"].Kind() != '"' || json.Unmarshal(server["id"], &id) != nil {
			continue
		}
		serverChanged := false
		for _, field := range secretFields {
			var values map[string]jsontext.Value
			if json.Unmarshal(server[field], &values) != nil || values == nil {
				continue
			}
			fieldChanged := false
			for name, value := range values {
				var text string
				isText := value.Kind() == '"' && json.Unmarshal(value, &text) == nil
				masked := isText && text == Mask
				secret, ok := "", false
				switch {
				case masked:
					secret, ok = was.lookup(id, field, name)
				case isText && text != "":
					secret, ok = text, true
				}
				if ok {
					kept.put(id, field, name, secret)
					if !masked {
						values[name], fieldChanged = mask, true
					}
				} else if masked {
					// A mask with nothing behind it (its name changed): empty,
					// to be typed again, never the dots sent as the value.
					values[name], fieldChanged = jsontext.Value(`""`), true
				}
			}
			if fieldChanged {
				server[field], serverChanged = mustMarshal(values), true
			}
		}
		if serverChanged {
			list[i], changed = mustMarshal(server), true
		}
	}
	if !changed {
		return kept, servers, false
	}
	return kept, mustMarshal(list), true
}

func mustMarshal(v any) jsontext.Value {
	raw, err := json.Marshal(v, json.Deterministic(true))
	if err != nil {
		panic(err)
	}
	return raw
}

// Unmask is the server's Mask values as kept, for starting or trying it.
func Unmask(kept Kept, c *Config) {
	for field, values := range map[string]map[string]string{"env": c.Env, "headers": c.Headers} {
		for name, value := range values {
			if value != Mask {
				continue
			}
			if secret, ok := kept.lookup(c.ID, field, name); ok {
				values[name] = secret
			}
		}
	}
}
