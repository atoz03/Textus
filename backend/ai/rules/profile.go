package rules

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"textus/backend/frontmatter"
)

// Writing conventions profiles (`zh-Hans-CN/fiction`, `en-GB/govuk`): which
// norm or house style a document follows, per language. From style.md front
// matter, then the `writingConventions` setting, then Automatic, which
// follows the national norm where there is one and the document's own
// majority.

// SettingKey is the settings.json key of the global default.
const SettingKey = "writingConventions"

// Automatic is its default value, and the per-language value meaning "no
// choice".
const Automatic = "automatic"

// LanguageProfiles are, per language, the profile Automatic uses ("":
// universal rules and consistency checks only) and the choosable profiles.
// Keep in step with the frontend's WRITING_CONVENTIONS (a test checks).
var LanguageProfiles = []struct {
	Lang      Lang
	Automatic string
	Profiles  []string
}{
	{ZhHans, "zh-Hans-CN", []string{"zh-Hans-CN", "zh-Hans-CN/tech", "zh-Hans-CN/gongwen", "zh-Hans-CN/academic", "zh-Hans-CN/fiction", "zh-Hans-CN/newmedia", "zh-Hant-CN"}},
	{ZhHant, "zh-Hant-TW", []string{"zh-Hant-TW", "zh-Hant-TW/gongwen", "zh-Hant-HK"}},
	{En, "", []string{"en-US/general", "en-US/news", "en-US/tech-google", "en-US/tech-microsoft", "en-US/apa", "en-US/mla", "en-GB/govuk", "en-GB/oxford", "en-US/army", "en-AU"}},
	{Ja, "ja/jtf", []string{"ja/jtf", "ja/koyobun", "ja/fiction", "ja/tech"}},
	{Ko, "ko", []string{"ko", "ko/official"}},
	{Es, "", []string{"es-ES", "es-419", "es/eu"}},
	{Fr, "", []string{"fr-FR", "fr-CA", "fr-CH", "fr/eu"}},
	{De, "", []string{"de-DE", "de-DE/din5008", "de-AT", "de-CH", "de/eu"}},
	{Ru, "ru", []string{"ru", "ru/gost"}},
	{Pt, "pt-BR", []string{"pt-BR", "pt-BR/official", "pt-PT", "pt/eu"}},
}

// Options a profile can override, with their values (the first is the
// default), keyed as in style.md front matter and the setting's `options`.
var Options = []struct {
	Key    string
	Values []string
}{
	{"cjk-latin-space", []string{"auto", "on", "off", "layout"}},
	{"stacked-marks", []string{"auto", "1", "3"}},
	{"quotes", []string{"auto", "curly", "corner"}},
	{"number-grouping", []string{"auto", "comma", "space", "thin-space", "none"}},
	{"unit-space", []string{"auto", "on", "off"}},
	{"percent-space", []string{"auto", "on", "off"}},
	{"fr.spacing", []string{"auto", "fr-FR", "fr-CA", "fr-CH", "eu"}},
	{"de.dateZeroPad", []string{"off", "on"}},
	{"de.genderChars", []string{"allow", "flag"}},
	{"es.soloAccent", []string{"off", "flag"}},
	{"pt.ao90", []string{"on", "off"}},
	{"ru.yo", []string{"selective", "always", "never"}},
}

func isOption(key string) bool {
	return slices.ContainsFunc(Options, func(o struct {
		Key    string
		Values []string
	}) bool {
		return o.Key == key
	})
}

// Source is where the effective profile came from.
type Source string

const (
	SourceAutomatic Source = "automatic"
	SourceSettings  Source = "settings"
	SourceProject   Source = "project"
)

// Profile is the conventions one document follows, as the rules read them.
type Profile struct {
	// ID is the profile value (`zh-Hans-CN/fiction`), or the language key
	// when Automatic has no national default (`en`).
	ID   string `json:"id"`
	Lang Lang   `json:"lang"`
	// Region is `CN`, `TW`, `HK`, `US`, `GB`, `AU`, `ES`, `419`, `FR`, `CA`,
	// `CH`, `DE`, `AT`, `BR`, `PT`; "" for none.
	Region string `json:"region"`
	// Preset is the house-style preset, e.g. `fiction`, `govuk`, `jtf`.
	Preset string `json:"preset"`
	// Explicit: chosen by the user (settings or project) rather than
	// Automatic.
	Explicit bool              `json:"explicit"`
	Source   Source            `json:"source"`
	Options  map[string]string `json:"options"`
}

// ParseProfile reads a profile value; false for a value that isn't one.
func ParseProfile(value string) (Profile, bool) {
	value = strings.TrimSpace(value)
	base, preset, _ := strings.Cut(value, "/")
	parts := strings.Split(base, "-")
	var lang Lang
	var region string
	switch {
	case len(parts) == 3 && parts[0] == "zh" && parts[1] == "Hans":
		lang, region = ZhHans, parts[2]
	case len(parts) == 3 && parts[0] == "zh" && parts[1] == "Hant":
		lang, region = ZhHant, parts[2]
	case len(parts) == 2 && parts[0] == "zh" && parts[1] == "Hans":
		lang, region = ZhHans, "CN"
	case len(parts) == 2 && parts[0] == "zh" && parts[1] == "Hant":
		lang, region = ZhHant, "TW"
	case len(parts) == 1 || len(parts) == 2:
		l, ok := LangFromCode(parts[0])
		if !ok {
			return Profile{}, false
		}
		lang = l
		if len(parts) == 2 {
			region = parts[1]
		}
	default:
		return Profile{}, false
	}
	return Profile{ID: value, Lang: lang, Region: region, Preset: preset, Source: SourceAutomatic, Options: map[string]string{}}, true
}

// AutomaticProfile is the Automatic profile for a language.
func AutomaticProfile(lang Lang) Profile {
	for _, lp := range LanguageProfiles {
		if lp.Lang == lang && lp.Automatic != "" {
			if p, ok := ParseProfile(lp.Automatic); ok {
				return p
			}
		}
	}
	return Profile{ID: string(lang), Lang: lang, Source: SourceAutomatic, Options: map[string]string{}}
}

func (p *Profile) is(preset string) bool       { return p.Preset == preset }
func (p *Profile) regionIs(region string) bool { return p.Region == region }

// opt is an option's value, "" when it isn't set or is `auto`.
func (p *Profile) opt(key string) string {
	if v := p.Options[key]; v != "auto" {
		return v
	}
	return ""
}

// mainland is mainland conventions for Chinese: zh-Hans-CN and zh-Hant-CN.
func (p *Profile) mainland() bool {
	return p.Lang.IsChinese() && (p.Region == "" || p.Region == "CN")
}

// Describe is one line for prompts and tool results: the profile, where it
// came from, and the options that are set.
func (p *Profile) Describe() string {
	source := "Automatic: follows the document's own majority where conventions conflict"
	switch p.Source {
	case SourceSettings:
		source = "chosen in Settings"
	case SourceProject:
		source = "from the project's style profile"
	}
	line := fmt.Sprintf("%s (%s)", p.ID, source)
	if len(p.Options) > 0 {
		var opts []string
		for _, k := range slices.Sorted(maps.Keys(p.Options)) {
			opts = append(opts, k+"="+p.Options[k])
		}
		line += ". " + strings.Join(opts, ", ")
	}
	return line
}

// ProjectConventions are what `.textus/style.md` says about conventions.
type ProjectConventions struct {
	// Profiles are the `conventions:` values, in order.
	Profiles []string `json:"profiles"`
	// Options are the option keys set in the front matter.
	Options map[string]string `json:"options"`
}

// ProjectConventionsFromStyle reads the front matter of a style profile: a
// `---` block at the very top, `key: value` lines at column 0. Unknown keys
// are ignored.
func ProjectConventionsFromStyle(style string) ProjectConventions {
	pc := ProjectConventions{Profiles: []string{}, Options: map[string]string{}}
	front, ok := frontmatter.Split(style)
	if !ok {
		return pc
	}
	unquote := func(s string) string { return strings.Trim(s, `"'`) }
	for _, line := range front.Lines {
		if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		value = strings.TrimSpace(unquote(strings.TrimSpace(value)))
		key = strings.TrimSpace(key)
		if value == "" {
			continue
		}
		if key == "conventions" {
			pc.Profiles = []string{}
			for _, v := range strings.FieldsFunc(strings.Trim(value, "[]"), func(r rune) bool { return r == ',' || r == '，' || r == '、' }) {
				if v = unquote(strings.TrimSpace(v)); v != "" {
					pc.Profiles = append(pc.Profiles, v)
				}
			}
		} else if isOption(key) {
			pc.Options[key] = value
		}
	}
	return pc
}

// Conventions are everything that decides a document's profile: the
// global setting and the project's style profile.
type Conventions struct {
	// Setting is the `writingConventions` setting as stored, decoded from
	// JSON (nil: never set).
	Setting any
	Project ProjectConventions
}

// ProfileFor is the profile a document (or block) in lang follows.
func (c *Conventions) ProfileFor(lang Lang) Profile {
	var profile Profile
	found := false
	for _, v := range c.Project.Profiles {
		if p, ok := ParseProfile(v); ok && p.Lang == lang {
			profile, found = p, true
			profile.Explicit, profile.Source = true, SourceProject
			break
		}
	}
	if !found {
		if p, ok := c.settingProfile(lang); ok {
			profile, found = p, true
			profile.Explicit, profile.Source = true, SourceSettings
		}
	}
	if !found {
		profile = AutomaticProfile(lang)
	}
	// Options: the setting's, then the project's over them.
	if setting, ok := c.Setting.(map[string]any); ok {
		if opts, ok := setting["options"].(map[string]any); ok {
			for k, v := range opts {
				if s, ok := v.(string); ok && isOption(k) {
					profile.Options[k] = s
				}
			}
		}
	}
	for k, v := range c.Project.Options {
		profile.Options[k] = v
	}
	return profile
}

func (c *Conventions) settingProfile(lang Lang) (Profile, bool) {
	// The Settings row stores one profile as a string: it applies to
	// documents in its own language, the others stay Automatic. Commas
	// work too.
	if s, ok := c.Setting.(string); ok {
		for _, v := range strings.Split(s, ",") {
			if v = strings.TrimSpace(v); v == Automatic {
				continue
			}
			if p, ok := ParseProfile(v); ok && p.Lang == lang {
				return p, true
			}
		}
		return Profile{}, false
	}
	m, ok := c.Setting.(map[string]any)
	if !ok {
		return Profile{}, false
	}
	parse := func(v any) (Profile, bool) {
		s, ok := v.(string)
		if !ok || s == Automatic {
			return Profile{}, false
		}
		p, ok := ParseProfile(s)
		return p, ok && p.Lang == lang
	}
	// The language's own key, "zh" for either Chinese script, then any
	// value naming this language (zh-Hant-CN is offered under zh-Hans).
	if v, ok := m[string(lang)]; ok {
		if v == Automatic {
			return Profile{}, false
		}
		if p, ok := parse(v); ok {
			return p, true
		}
	}
	if lang.IsChinese() {
		if p, ok := parse(m["zh"]); ok {
			return p, true
		}
	}
	for _, k := range slices.Sorted(maps.Keys(m)) {
		if k == "options" {
			continue
		}
		if p, ok := parse(m[k]); ok {
			return p, true
		}
	}
	return Profile{}, false
}
