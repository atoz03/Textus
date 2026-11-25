package ai

import (
	"embed"
	"strings"
	"sync"
)

// The skills and writing-standards reference packs bundled with the app.
// The user's own skill of the same name overrides a built-in; a skill's
// `textus-packs` sections come with its body.

//go:embed skills/*.md skills/packs/*.md
var bundled embed.FS

// namedFile is one bundled file: its stem (also the skill's or pack's
// name) and its text.
type namedFile struct{ stem, text string }

func readBundled(dir string, stems ...string) []namedFile {
	files := make([]namedFile, len(stems))
	for i, stem := range stems {
		raw, err := bundled.ReadFile(dir + stem + ".md")
		if err != nil {
			panic("ai: bundled " + dir + stem + ".md: " + err.Error())
		}
		files[i] = namedFile{stem, string(raw)}
	}
	return files
}

// skillFiles are the built-in skills, Write's, Review's and Create's, in
// that order.
var skillFiles = readBundled("skills/", "proofread", "theme", "plugin", "skill")

// packFiles are the reference packs, by the name use_skill loads them by.
var packFiles = readBundled("skills/packs/",
	"contract", "craft", "standards-en", "standards-zh-hans", "standards-zh-hant", "standards-ja",
	"standards-ko", "standards-es", "standards-fr", "standards-de", "standards-ru", "standards-pt")

// MovedToMarket are the skills that were built in and are in the market
// now: an older profile gets the ones it had installed from there. The
// first twelve moved with 1.3.0-beta.1, the rest with beta.3.
var MovedToMarket = []string{
	"continue-story", "describe", "dialogue", "imitate-style", "change-tone", "de-ai", "email", "notice",
	"minutes", "academic-polish", "social-post", "headline", "expand", "tighten", "translate", "summarize",
	"continuity",
}

// CategoryOrder is the `/` menu's groups, in menu order (`textus-category`).
var CategoryOrder = []string{"write", "revise", "check", "format", "create"}

// BuiltinSkills are the built-in skills, parsed once. Callers copy what
// they change (AgentSkill.Clone).
var BuiltinSkills = sync.OnceValue(func() []AgentSkill {
	skills := make([]AgentSkill, len(skillFiles))
	for i, f := range skillFiles {
		skills[i] = ParseSkillFile(f.stem, f.text)
		skills[i].Source = SourceBuiltin
	}
	return skills
})

// FindBuiltin is the built-in skill called name.
func FindBuiltin(name string) (AgentSkill, bool) {
	for _, s := range BuiltinSkills() {
		if s.Name == name {
			return s, true
		}
	}
	return AgentSkill{}, false
}

// RawBuiltin is a built-in skill's file as bundled (what Customize…
// copies).
func RawBuiltin(name string) (string, bool) {
	for _, f := range skillFiles {
		if f.stem == name {
			return f.text, true
		}
	}
	return "", false
}

// WithBasedOn is a built-in skill's file with `textus-based-on` added
// under `metadata:`, for the copy Customize… writes.
func WithBasedOn(raw, basedOn string) string {
	line := `  textus-based-on: "` + basedOn + `"`
	var out []string
	added := false
	for _, l := range lines(raw) {
		out = append(out, l)
		if !added && strings.TrimRight(l, " \t") == "metadata:" {
			out = append(out, line)
			added = true
		}
	}
	text := strings.Join(out, "\n")
	if strings.HasSuffix(raw, "\n") {
		text += "\n"
	}
	return text
}
