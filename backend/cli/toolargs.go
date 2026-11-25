package cli

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
)

// Plugins' AI tools as terminal commands: `textus <plugin> <tool>` with
// `--name value` options read against the tool's JSON schema.

// PluginPrefix starts a request's command for a plugin's word:
// `plugin:<word>`.
const PluginPrefix = "plugin:"

// ListTools is the request `textus help` sends for the plugins' commands.
const ListTools = "help:plugins"

// schema is what of a tool's JSON schema its options are read with.
type schema struct {
	// Properties is nil where the schema names none: then any option is
	// taken, as text.
	Properties map[string]struct {
		Type jsontext.Value `json:"type"`
	} `json:"properties"`
	Required []string `json:"required"`
}

func readSchema(raw jsontext.Value) schema {
	var s schema
	_ = json.Unmarshal(raw, &s)
	return s
}

// typeOf is a property's type, "string" unless it names one.
func (s schema) typeOf(name string) string {
	var kind string
	if json.Unmarshal(s.Properties[name].Type, &kind) != nil || kind == "" {
		return "string"
	}
	return kind
}

// Arguments are a tool's arguments from the command line: `--name value`,
// `--name=value`, a bare `--name` for a boolean, and `--json '{...}'` for
// all at once. Values are read as the schema's type says.
func Arguments(raw jsontext.Value, args []string) (jsontext.Value, error) {
	s := readSchema(raw)
	out := map[string]jsontext.Value{}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		option, ok := strings.CutPrefix(arg, "--")
		if !ok {
			return nil, fmt.Errorf("'%s' isn't an option: give arguments as --<name> <value>.", arg)
		}
		name, inline, hasInline := strings.Cut(option, "=")
		next := func() (string, bool) {
			if i+1 < len(args) {
				i++
				return args[i], true
			}
			return "", false
		}
		if name == "json" {
			text := inline
			if !hasInline {
				text, _ = next()
			}
			var all map[string]jsontext.Value
			if json.Unmarshal([]byte(text), &all) != nil || all == nil {
				return nil, errors.New("--json needs a JSON object.")
			}
			for k, v := range all {
				out[k] = v
			}
			continue
		}
		kind := "string"
		if s.Properties != nil {
			if _, ok := s.Properties[name]; !ok {
				return nil, fmt.Errorf("This tool has no option --%s.", name)
			}
			kind = s.typeOf(name)
		}
		value := inline
		if !hasInline {
			if kind == "boolean" {
				out[name] = jsontext.Value("true")
				continue
			}
			if value, ok = next(); !ok {
				return nil, fmt.Errorf("--%s needs a value.", name)
			}
		}
		typed, err := typedValue(kind, value, name)
		if err != nil {
			return nil, err
		}
		out[name] = typed
	}
	var missing []string
	for _, name := range s.Required {
		if _, ok := out[name]; !ok {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("Missing: --%s.", strings.Join(missing, ", --"))
	}
	return json.Marshal(out, json.Deterministic(true))
}

func typedValue(kind, value, name string) (jsontext.Value, error) {
	wrong := fmt.Errorf("--%s needs a %s.", name, kind)
	switch kind {
	case "integer":
		n, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return nil, wrong
		}
		return jsontext.Value(strconv.FormatInt(n, 10)), nil
	case "number":
		f, err := strconv.ParseFloat(value, 64)
		if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
			return nil, wrong
		}
		return json.Marshal(f)
	case "boolean":
		if value != "true" && value != "false" {
			return nil, wrong
		}
		return jsontext.Value(value), nil
	case "array", "object":
		v := jsontext.Value(value)
		if !v.IsValid() {
			return nil, wrong
		}
		return v, nil
	}
	return json.Marshal(value)
}

// ToolUsage is `textus <plugin> <tool> --a <string> [--b <number>]`, then
// what the tool does.
func ToolUsage(plugin, shortName, tool, description string, raw jsontext.Value) string {
	s := readSchema(raw)
	line := "Usage: textus " + shortName + " " + tool
	names := make([]string, 0, len(s.Properties))
	for name := range s.Properties {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		option := fmt.Sprintf("--%s <%s>", name, s.typeOf(name))
		if slices.Contains(s.Required, name) {
			line += " " + option
		} else {
			line += " [" + option + "]"
		}
	}
	return fmt.Sprintf("%s\n\n%s\n\nA tool of the plugin %s. Also: --json '<object>'.\n", line, strings.TrimSpace(description), plugin)
}
