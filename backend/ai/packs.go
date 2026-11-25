package ai

import (
	"fmt"
	"slices"
	"strings"
	"sync"
	"unicode"
)

// Reference packs - the writing standards a skill can pull in, per
// language - and the sections of them a model is sent.

// Pack is one reference pack: its name and its `## sections`, in file
// order.
type Pack struct {
	Name     string
	Sections []PackSection
}

// PackSection is one `## name` section and its text, trimmed.
type PackSection struct{ Name, Text string }

// Section is the text of the section called name.
func (p *Pack) Section(name string) (string, bool) {
	for _, s := range p.Sections {
		if s.Name == name {
			return s.Text, true
		}
	}
	return "", false
}

// Packs are the reference packs, parsed once.
var Packs = sync.OnceValue(func() []Pack {
	packs := make([]Pack, len(packFiles))
	for i, f := range packFiles {
		skill := ParseSkillFile(f.stem, f.text)
		packs[i] = Pack{Name: skill.Name, Sections: splitSections(skill.Prompt)}
	}
	return packs
})

// FindPack is the pack called name.
func FindPack(name string) (*Pack, bool) {
	packs := Packs()
	for i := range packs {
		if packs[i].Name == name {
			return &packs[i], true
		}
	}
	return nil, false
}

// splitSections is a pack body's `## name` sections.
func splitSections(body string) []PackSection {
	var sections []PackSection
	var texts []*strings.Builder
	for _, line := range lines(body) {
		if name, ok := strings.CutPrefix(line, "## "); ok {
			sections = append(sections, PackSection{Name: strings.TrimSpace(name)})
			texts = append(texts, &strings.Builder{})
		} else if len(texts) > 0 {
			texts[len(texts)-1].WriteString(line + "\n")
		}
	}
	for i := range sections {
		sections[i].Text = strings.TrimSpace(texts[i].String())
	}
	return sections
}

// PackLanguage is a document language with a standards pack.
type PackLanguage struct{ Code, Name, UI string }

// PackLanguages are the document languages with a standards pack: pack
// code, English name, UI language code.
var PackLanguages = []PackLanguage{
	{"en", "English", "en"}, {"zh-hans", "Simplified Chinese", "zh"}, {"zh-hant", "Traditional Chinese", "zh-Hant"},
	{"ja", "Japanese", "ja"}, {"ko", "Korean", "ko"}, {"es", "Spanish", "es"}, {"fr", "French", "fr"},
	{"de", "German", "de"}, {"ru", "Russian", "ru"}, {"pt", "Portuguese", "pt"},
}

func languageName(code string) string {
	for _, l := range PackLanguages {
		if l.Code == code {
			return l.Name
		}
	}
	return "an unknown language"
}

// leadingTags is the `[tag]` or `[tag, tag]` at the start of a rule line: a
// profile, a profile part or a setting (`quotes=corner`). Anything with
// spaces inside is not a tag.
func leadingTags(line string) ([]string, bool) {
	rest := strings.TrimLeftFunc(line, unicode.IsSpace)
	rest = strings.TrimPrefix(rest, "- ")
	inner, ok := strings.CutPrefix(rest, "[")
	if !ok {
		return nil, false
	}
	end := strings.Index(inner, "]")
	if end < 0 {
		return nil, false
	}
	tags := strings.Split(inner[:end], ",")
	for i, t := range tags {
		t = strings.TrimSpace(t)
		tags[i] = t
		if t == "" || strings.ContainsFunc(t, func(c rune) bool {
			return !(c < 0x80 && (unicode.IsLetter(c) || unicode.IsDigit(c)) || strings.ContainsRune("-/._=", c))
		}) {
			return nil, false
		}
	}
	return tags, true
}

// tagApplies is whether a variant tag applies under a writing-conventions
// profile such as `zh-Hans-CN/fiction` or `en-GB/govuk`.
func tagApplies(tag, profile string) bool {
	if strings.Contains(tag, "=") {
		// A setting: the line says what it depends on.
		return true
	}
	base, variant, _ := strings.Cut(profile, "/")
	t, b := strings.ToLower(tag), strings.ToLower(base)
	return t == strings.ToLower(profile) || t == b || strings.HasPrefix(b, t+"-") ||
		variant != "" && t == strings.ToLower(variant)
}

// stripBookkeeping drops the maintainer-only bookkeeping at the end of a
// rule line: the `(M)`/`(S)` marks and the bracketed source keys.
func stripBookkeeping(line string) string {
	line = strings.TrimRightFunc(line, unicode.IsSpace)
	if strings.HasSuffix(line, "]") {
		if at := strings.LastIndex(line, " ["); at >= 0 {
			if _, tags := leadingTags(line[at+1:]); !tags || at > 2 {
				line = line[:at]
			}
		}
	}
	trimmed := strings.TrimRightFunc(line, unicode.IsSpace)
	if strings.HasSuffix(trimmed, ")") {
		if open := strings.LastIndex(trimmed, "("); open >= 0 {
			inner := []rune(trimmed[open+1 : len(trimmed)-1])
			mark := len(inner) > 0 && (inner[0] == 'M' || inner[0] == 'S') &&
				(len(inner) == 1 || strings.ContainsRune(" /,;", inner[1]))
			if mark || slices.Contains([]string{"guard", "setting", "renderer"}, string(inner)) {
				line = line[:open]
			}
		}
	}
	return strings.TrimRightFunc(line, unicode.IsSpace)
}

// sectionForModel is a pack section as the model reads it: rule lines
// tagged for another profile dropped (all kept with no profile), source
// lines and bookkeeping removed.
func sectionForModel(text, profile string) string {
	var out []string
	for _, line := range lines(text) {
		if strings.HasPrefix(strings.TrimLeftFunc(line, unicode.IsSpace), "- Sources:") {
			continue
		}
		if tags, ok := leadingTags(line); ok && profile != "" {
			applies := false
			for _, t := range tags {
				applies = applies || tagApplies(t, profile)
			}
			if !applies {
				continue
			}
		}
		out = append(out, stripBookkeeping(line))
	}
	return strings.Join(out, "\n")
}

// LanguageSource is where the document's language came from.
type LanguageSource int

const (
	// Detected is read from the document's own text.
	Detected LanguageSource = iota
	// FromUILanguage: the document has too little text to tell.
	FromUILanguage
)

// DocLanguage is a document's pack language and where it came from; Code
// "" for none.
type DocLanguage struct {
	Code   string
	Source LanguageSource
}

// conventionsHeader is the line that opens the attached conventions:
// which language (and profile) they are for and how variants are decided.
func conventionsHeader(language DocLanguage, profile string) string {
	switch {
	case profile != "":
		return fmt.Sprintf("Conventions for this document: %s (the writing conventions chosen for this project or in Settings). Rules tagged for other profiles are left out.", profile)
	case language.Code != "":
		how := "detected from the document"
		if language.Source == FromUILanguage {
			how = "assumed from the app's language; the document has too little text to tell"
		}
		return fmt.Sprintf("Conventions for this document: %s (%s). No writing-conventions profile is set: where the rules give variants (tags in square brackets) or name a setting, follow the form the document already uses most and flag only inconsistency.", languageName(language.Code), how)
	}
	return "Conventions for this document: the language couldn't be determined; apply only the universal rules and the document's own habits."
}

// packRef is one resolved reference: its label and text.
type packRef struct{ label, text string }

// resolvePack resolves one pack reference, e.g. `craft#plain` or
// `standards#dialogue` (the document language's pack); none when the pack
// or section doesn't exist.
func resolvePack(reference, language string) []packRef {
	name, section, hasSection := strings.Cut(reference, "#")
	if name == "standards" {
		if language == "" {
			return nil
		}
		name = "standards-" + language
	}
	pack, ok := FindPack(name)
	if !ok {
		return nil
	}
	switch {
	case hasSection:
		if text, ok := pack.Section(section); ok {
			return []packRef{{name + "#" + section, text}}
		}
		return nil
	case len(pack.Sections) == 1:
		return []packRef{{name, pack.Sections[0].Text}}
	}
	var refs []packRef
	for _, s := range pack.Sections {
		refs = append(refs, packRef{name + "#" + s.Name, s.Text})
	}
	return refs
}

// renderReferences is the attached references as one block: the
// conventions header (when a language's own pack is among them) and each
// section under its label.
func renderReferences(refs []packRef, language DocLanguage, profile, target string) string {
	var out []string
	// The header describes the document's own conventions: shown with its
	// language's pack, not with another language's (a translation target).
	if language.Code != "" {
		own := "standards-" + language.Code + "#"
		for _, r := range refs {
			if strings.HasPrefix(r.label, own) {
				out = append(out, conventionsHeader(language, profile))
				break
			}
		}
	}
	if target != "" {
		out = append(out, fmt.Sprintf("Target language: %s - its conventions are attached as standards-%s.", languageName(target), target))
	}
	for _, r := range refs {
		out = append(out, fmt.Sprintf("### %s\n%s", r.label, sectionForModel(r.text, profile)))
	}
	return strings.Join(out, "\n\n")
}

// LoadReference is what use_skill returns for a pack name such as
// `standards-fr#dialogue`; false when there is no such pack or section.
func LoadReference(name, document, uiLanguage string) (string, bool) {
	language := DocumentLanguage(document, uiLanguage)
	refs := resolvePack(strings.TrimSpace(name), language.Code)
	if len(refs) == 0 {
		return "", false
	}
	return renderReferences(refs, language, "", ""), true
}

// ReferencePackNames are the pack names the skill index lists for the
// model.
func ReferencePackNames() []string {
	var names []string
	for _, f := range packFiles {
		if f.stem != "contract" {
			names = append(names, f.stem)
		}
	}
	return names
}
