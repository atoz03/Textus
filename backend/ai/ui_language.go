package ai

import (
	"embed"
	"fmt"
	"strings"

	"textus/backend/ai/rules"
)

// The app's UI languages as the AI prompts need them. Everything per
// language lives in its own files (ui-names/<code>.txt,
// style-headings/<code>.txt), so translating the app touches no code here.

//go:embed ui-names/*.txt style-headings/*.txt
var uiFiles embed.FS

func uiFile(dir, code string) string {
	raw, err := uiFiles.ReadFile(dir + "/" + code + ".txt")
	if err != nil {
		panic("ai: " + dir + "/" + code + ".txt: " + err.Error())
	}
	return string(raw)
}

// entries are a file's lines, less comments and blank ones.
func entries(text string) []string {
	var out []string
	for _, l := range lines(text) {
		if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "#") {
			out = append(out, l)
		}
	}
	return out
}

// UINames are the names the user's UI gives what the prompt describes in
// English, as (English, theirs) pairs. One language only, so the prompt
// stays short and cacheable.
type UINames struct {
	Code, English string
	Names         [][2]string
}

// UINamesOf is the names of the UI language code; false for one with none.
func UINamesOf(code string) (UINames, bool) {
	for _, l := range rules.UILanguages {
		if l.Code != code {
			continue
		}
		var names [][2]string
		for _, line := range entries(uiFile("ui-names", code)) {
			if en, theirs, ok := strings.Cut(line, " = "); ok {
				names = append(names, [2]string{en, theirs})
			}
		}
		return UINames{l.Code, l.English, names}, len(names) > 0
	}
	return UINames{}, false
}

// nativeNames are the UI languages' names in themselves, as the language
// menu shows them (src/prefs/appearance-tables.json langNames; a test
// keeps them the same).
var nativeNames = map[string]string{
	"en": "English", "zh": "简体中文", "zh-Hant": "繁體中文", "ja": "日本語", "ko": "한국어",
	"es": "Español", "fr": "Français", "de": "Deutsch", "ru": "Русский", "pt": "Português",
}

// NativeName is a UI language's name in itself; its English name for one
// the table lacks.
func NativeName(code string) string {
	if name, ok := nativeNames[code]; ok {
		return name
	}
	return rules.EnglishName(code)
}

// OfEnglish is this UI's name for what the English UI calls english.
func (n UINames) OfEnglish(english string) string {
	for _, pair := range n.Names {
		if pair[0] == english {
			return pair[1]
		}
	}
	return english
}

// styleHeadings is "# Style profile / ## Voice and tone / …" of one
// language: its first entry as Markdown headings.
func styleHeadings(code string) (string, bool) {
	list := entries(uiFile("style-headings", code))
	if len(list) == 0 {
		return "", false
	}
	var headings []string
	for i, h := range strings.Split(list[0], " / ") {
		mark := "##"
		if i == 0 {
			mark = "#"
		}
		headings = append(headings, mark+" "+strings.TrimSpace(h))
	}
	return strings.Join(headings, " / "), true
}

// styleHeadingLines is "English: # Style profile / …", one line for each
// language whose headings are translated.
func styleHeadingLines() string {
	var out []string
	for _, l := range rules.UILanguages {
		if h, ok := styleHeadings(l.Code); ok {
			out = append(out, fmt.Sprintf("%s: %s", l.English, h))
		}
	}
	return strings.Join(out, "\n")
}

// StyleInstructions is the prompt that drafts the style profile from
// samples, written in the samples' language with that language's
// headings, since the author edits it right afterwards.
func StyleInstructions() string {
	return fmt.Sprintf(`You analyse an author's writing so that an AI writing assistant can continue and revise their text in their own voice. You are given samples the author picked as representative. Write their style profile in Markdown.

Write the whole profile in the language the samples are written in - headings, bullets and all: Chinese samples get a Chinese profile, Japanese samples a Japanese one, English samples an English one. The author reads and edits this file, and the assistant follows it. Use exactly these seven sections, in this order, with the headings in that language:

%s

Under each heading give 3-6 short, concrete, checkable bullet points drawn from the samples - "opens paragraphs with a concrete scene, then the claim", "句子很短，多数不超过十五个字", "uses full-width Chinese punctuation, no em dashes" - not generic praise or advice. Every bullet must hold for the samples as given: before writing one, check it against every sample - count instead of estimating, name a habit as "every sample" or "always" only if each sample shows it, and call something absent (metaphors, exclamation marks, a topic) only if no sample uses it. A habit of the samples' content - who they address, what they are about, their tense - is described as a habit, never as a rule for all writing ("the pieces speak to the reader as 'you'", not "never uses I"). The Never section lists only what no sample does and the author clearly avoids, and contradicts no other bullet. The Examples section quotes 2-3 short representative passages (1-3 sentences each) verbatim from the samples, without adding quotation marks around a passage that already contains them. Output only the profile, in the samples' language.`, styleHeadingLines())
}

// NearbySection names the files beside the document for the request; ""
// without any.
func NearbySection(files []string) string {
	if len(files) == 0 {
		return ""
	}
	var list []string
	for _, f := range files {
		list = append(list, "- "+f)
	}
	return "---files beside this document (the same series, templates): read_file one when it bears on the request - an earlier chapter for names and continuity, a template for the shape to follow, every one of its sections kept (one with nothing to say left empty or marked so, never dropped); don't read them otherwise---\n" + strings.Join(list, "\n")
}
