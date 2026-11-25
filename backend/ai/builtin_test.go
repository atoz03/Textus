package ai

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"textus/backend/ai/rules"
)

var uiCodes = []string{"en", "zh", "zh-Hant", "ja", "ko", "es", "fr", "de", "ru", "pt"}

var standardSections = []string{
	"typography", "numbers", "words", "dialogue", "register", "letters", "official", "academic",
	"social", "ai-tells",
}

func isKebab(name string) bool {
	if name == "" {
		return false
	}
	for _, part := range strings.Split(name, "-") {
		if part == "" || strings.ContainsFunc(part, func(c rune) bool { return !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9') }) {
			return false
		}
	}
	return true
}

// approxTokens is a rough token count: a CJK, kana or Hangul character is
// about one token, other text about four characters per token.
func approxTokens(text string) int {
	wide := 0
	for _, c := range text {
		if c >= 0x2e80 {
			wide++
		}
	}
	return wide + (utf8.RuneCountInString(text)-wide)/4
}

func mustBuiltin(t *testing.T, name string) AgentSkill {
	t.Helper()
	s, ok := FindBuiltin(name)
	if !ok {
		t.Fatalf("%s is not built in", name)
	}
	return s
}

func TestEveryModeHasItsBuiltInSkills(t *testing.T) {
	of := func(mode AgentMode) []string {
		names := []string{}
		for _, s := range BuiltinSkills() {
			if slices.Equal(s.Modes, []AgentMode{mode}) {
				names = append(names, s.Name)
			}
		}
		return names
	}
	if got := of(ModeWrite); len(got) != 0 {
		t.Errorf("Write: %v", got)
	}
	if got := of(ModeReview); !slices.Equal(got, []string{"proofread"}) {
		t.Errorf("Review: %v", got)
	}
	if got := of(ModeCreate); !slices.Equal(got, []string{"theme", "plugin", "skill"}) {
		t.Errorf("Create: %v", got)
	}
	for _, name := range MovedToMarket {
		if _, ok := FindBuiltin(name); ok {
			t.Errorf("%s is in the market now", name)
		}
	}
}

func TestEveryBundledFileIsListed(t *testing.T) {
	stems := func(dir string) []string {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, e := range entries {
			if stem, ok := strings.CutSuffix(e.Name(), ".md"); ok && !e.IsDir() && stem != "ATTRIBUTION" {
				out = append(out, stem)
			}
		}
		slices.Sort(out)
		return out
	}
	listed := func(files []namedFile) []string {
		var out []string
		for _, f := range files {
			out = append(out, f.stem)
		}
		slices.Sort(out)
		return out
	}
	if got, want := stems("skills"), listed(skillFiles); !slices.Equal(got, want) {
		t.Errorf("skills/ has %v, listed %v", got, want)
	}
	if got, want := stems(filepath.Join("skills", "packs")), listed(packFiles); !slices.Equal(got, want) {
		t.Errorf("skills/packs/ has %v, listed %v", got, want)
	}
	if len(skillFiles) != 4 || len(packFiles) != 12 {
		t.Errorf("%d skills and %d packs", len(skillFiles), len(packFiles))
	}
}

func TestEveryBuiltinSkillParsesAndFollowsTheFormat(t *testing.T) {
	for _, f := range skillFiles {
		stem := f.stem
		skill := mustBuiltin(t, stem)
		fm, body := SplitFrontmatter(f.text)
		if fm == nil {
			t.Fatalf("%s has no front matter", stem)
		}
		if skill.Name != stem || !isKebab(skill.Name) || skill.Source != SourceBuiltin {
			t.Errorf("%s: name %q, source %q", stem, skill.Name, skill.Source)
		}
		d := skill.Description
		if n := utf8.RuneCountInString(d); d == "" || n > 500 {
			t.Errorf("%s: description of %d characters", stem, n)
		}
		if strings.ContainsAny(d, "<>") || !strings.Contains(d, "Use when") {
			t.Errorf("%s: description %q", stem, d)
		}
		// The index shows `textus-index` instead of the description: the
		// description's "Use when" clause without the request words in
		// other languages, kept in step with it.
		if skill.IndexLine == nil {
			t.Errorf("%s: no textus-index", stem)
		} else if line := *skill.IndexLine; !strings.HasPrefix(line, "Use when") ||
			strings.ContainsFunc(line, func(c rune) bool { return c > 0x7f }) ||
			!strings.Contains(d, strings.TrimRight(line, ".")) {
			t.Errorf("%s: index line %q", stem, line)
		}
		if fm.Fields["license"] != "MIT" {
			t.Errorf("%s: license %q", stem, fm.Fields["license"])
		}
		for _, key := range []string{"textus-version", "textus-index", "textus-invocation", "textus-category", "textus-packs", "textus-sources"} {
			if _, ok := fm.Metadata[key]; !ok {
				t.Errorf("%s: no metadata %s", stem, key)
			}
		}
		if skill.Version == nil || *skill.Version != "1" {
			t.Errorf("%s: version %v", stem, skill.Version)
		}
		if skill.Category == nil || !slices.Contains(CategoryOrder, *skill.Category) {
			t.Errorf("%s: category %v", stem, skill.Category)
		}
		if skill.Invocation != InvokeBoth {
			t.Errorf("%s: invocation %q", stem, skill.Invocation)
		}
		// The editing contract is for skills that work on the document;
		// Create's make themes, plugins and skills and never touch it.
		creates := skill.Category != nil && *skill.Category == "create"
		if slices.Contains(skill.Packs, "contract") == creates {
			t.Errorf("%s: contract listed %v in category create %v", stem, !creates, creates)
		}
		if n := len(lines(body)); n > 120 {
			t.Errorf("%s: body has %d lines", stem, n)
		}
		for _, section := range []string{"## Scope", "## Do", "## Don't", "## Deliver"} {
			if !slices.ContainsFunc(lines(skill.Prompt), func(l string) bool { return l == section || strings.HasPrefix(l, section+" ") }) {
				t.Errorf("%s: no %s", stem, section)
			}
		}
		for _, text := range []string{f.text, skill.Prompt} {
			words := strings.FieldsFunc(text, func(c rune) bool { return !isAlphanumeric(c) })
			if strings.Contains(text, "CRITICAL") || slices.Contains(words, "MUST") {
				t.Errorf("%s: shouts", stem)
			}
			// The plugin skill's webview code calls acquireTextusApi(): an
			// API's name, not the product named to the model.
			if strings.Contains(strings.ReplaceAll(text, "acquireTextusApi", ""), "Textus") {
				t.Errorf("%s: names the product", stem)
			}
		}
		for _, code := range uiCodes {
			if len(skill.Aliases[code]) == 0 {
				t.Errorf("%s: no aliases in %s", stem, code)
			}
		}
	}
}

func TestAliasesAreUniquePerLanguageAndNeverAnotherSkillsName(t *testing.T) {
	for _, code := range uiCodes {
		seen := map[string]string{}
		for _, skill := range BuiltinSkills() {
			for _, alias := range skill.Aliases[code] {
				key := strings.ToLower(alias)
				if other, ok := seen[key]; ok {
					t.Errorf("%s: alias %s of %s is also %s's", code, alias, skill.Name, other)
				}
				if other, ok := FindBuiltin(key); ok && other.Name != skill.Name {
					t.Errorf("%s: alias %s of %s is the skill %s", code, alias, skill.Name, other.Name)
				}
				seen[key] = skill.Name
			}
		}
	}
	// The UI's own words for inline completion never label a skill.
	completion := []string{"補完", "이어 쓰기", "autocompletado", "complétion", "vervollständigung", "автодополнение", "autocompletar"}
	for _, skill := range BuiltinSkills() {
		for _, aliases := range skill.Aliases {
			for _, alias := range aliases {
				if slices.Contains(completion, strings.ToLower(alias)) {
					t.Errorf("%s: alias %s", skill.Name, alias)
				}
			}
		}
	}
}

func TestEveryPackParsesWithNamedSections(t *testing.T) {
	for _, f := range packFiles {
		stem := f.stem
		parsed := ParseSkillFile(stem, f.text)
		if parsed.Name != stem || parsed.Invocation != InvokeHidden {
			t.Errorf("%s: name %q, invocation %q", stem, parsed.Name, parsed.Invocation)
		}
		if n := utf8.RuneCountInString(parsed.Description); n == 0 || n > 500 {
			t.Errorf("%s: description of %d characters", stem, n)
		}
		fm, _ := SplitFrontmatter(f.text)
		if fm.Fields["license"] != "MIT" {
			t.Errorf("%s: license", stem)
		}
		if _, ok := fm.Metadata["textus-sources"]; !ok {
			t.Errorf("%s: no textus-sources", stem)
		}
		pack, ok := FindPack(stem)
		if !ok || len(pack.Sections) == 0 {
			t.Fatalf("%s: no sections", stem)
		}
		for _, s := range pack.Sections {
			if s.Text == "" {
				t.Errorf("%s#%s is empty", stem, s.Name)
			}
			if strings.HasPrefix(stem, "standards-") && !slices.Contains(standardSections, s.Name) {
				t.Errorf("%s#%s is not a standards section", stem, s.Name)
			}
			model := sectionForModel(s.Text, "")
			if tokens := approxTokens(model); tokens > 2_000 {
				t.Errorf("%s#%s: about %d tokens", stem, s.Name, tokens)
			}
			if strings.Contains(model, "(M)") || strings.Contains(model, "(S)") {
				t.Errorf("%s#%s: bookkeeping left in", stem, s.Name)
			}
		}
		if strings.Contains(strings.ReplaceAll(f.text, "acquireTextusApi", ""), "Textus") {
			t.Errorf("%s: names the product", stem)
		}
	}
	if contract, _ := FindPack("contract"); len(contract.Sections) != 1 {
		t.Errorf("contract has %d sections", len(contract.Sections))
	}
	craft, _ := FindPack("craft")
	var names []string
	for _, s := range craft.Sections {
		names = append(names, s.Name)
	}
	if want := []string{"fiction", "plain", "email", "platforms"}; !slices.Equal(names, want) {
		t.Errorf("craft has %v", names)
	}
	// Every language has a standards pack.
	for _, l := range PackLanguages {
		if _, ok := FindPack("standards-" + l.Code); !ok {
			t.Errorf("no standards-%s", l.Code)
		}
	}
}

func TestEveryPackReferenceASkillMakesExists(t *testing.T) {
	for _, skill := range BuiltinSkills() {
		for _, reference := range skill.Packs {
			p, section, named := strings.Cut(reference, "#")
			if p != "standards" {
				if len(resolvePack(reference, "")) == 0 {
					t.Errorf("%s: %s resolves to nothing", skill.Name, reference)
				}
				continue
			}
			if !named {
				t.Errorf("%s: a standards reference names no section", skill.Name)
				continue
			}
			if !slices.ContainsFunc(PackLanguages, func(l PackLanguage) bool {
				pack, _ := FindPack("standards-" + l.Code)
				_, ok := pack.Section(section)
				return ok
			}) {
				t.Errorf("%s: standards#%s exists in no language", skill.Name, section)
			}
		}
		for _, section := range skill.TargetPacks {
			if !slices.Contains(standardSections, section) {
				t.Errorf("%s: target pack %s", skill.Name, section)
			}
		}
	}
}

func TestAttributionCoversEveryAdaptedSource(t *testing.T) {
	raw, err := bundled.ReadFile("skills/ATTRIBUTION.md")
	if err != nil {
		t.Fatal(err)
	}
	attribution := string(raw)
	var sources strings.Builder
	for _, f := range append(slices.Clone(skillFiles), packFiles...) {
		fm, _ := SplitFrontmatter(f.text)
		sources.WriteString(fm.Metadata["textus-sources"] + "\n")
	}
	all := sources.String()
	for _, need := range [][2]string{
		{"GOV.UK", "Open Government Licence v3.0"},
		{"Google developer documentation style guide", "Google developer documentation style guide"},
		{"Microsoft Writing Style Guide", "Microsoft Writing Style Guide"},
		{"BIPM", "SI Brochure"},
		{"JTF", "JTF日本語標準スタイルガイド"},
		{"文化庁", "文化庁ホームページ"},
		{"Amtliches Regelwerk", "doi:10.14618/s99n-fk38"},
		{"EU Interinstitutional Style Guide", "Reproduction authorised provided the source is acknowledged"},
		{"clreq", "Requirements for Chinese Text Layout"},
		{"JLReq", "Requirements for Japanese Text Layout"},
		{"academic-humanizer", "Copyright (c) 2026 AIScientists-Dev"},
	} {
		if strings.Contains(all, need[0]) && !strings.Contains(attribution, need[1]) {
			t.Errorf("ATTRIBUTION.md doesn't credit %s", need[0])
		}
	}
	if !strings.Contains(attribution, "W3C Software and Document License") ||
		!strings.Contains(attribution, "Title to copyright in this work will at all times remain with copyright holders") {
		t.Error("ATTRIBUTION.md lacks the W3C licence")
	}
	// Share-alike and non-commercial sources are only ever summarised or
	// used for ideas: their text would pull its licence into ours.
	for _, marker := range []string{"CC BY-SA", "CC BY-NC", "GPL"} {
		for rest := all; ; {
			at := strings.Index(rest, marker)
			if at < 0 {
				break
			}
			after := []rune(rest[at:])
			window := string(after[:min(len(after), 60)])
			if !strings.Contains(window, "summarised") && !strings.Contains(window, "ideas") {
				t.Errorf("%s source used for more than ideas: %s", marker, window)
			}
			rest = rest[at+len(marker):]
		}
	}
}

func TestVariantTagsFilterByProfile(t *testing.T) {
	section := "- [zh-Hant-TW] taiwan rule\n- [fiction] fiction rule\n- [en-GB/govuk] govuk rule\n- general rule (M) [P1]\n- [quotes=corner] setting rule\n- Sources: [x]"
	if got, want := sectionForModel(section, ""), "- [zh-Hant-TW] taiwan rule\n- [fiction] fiction rule\n- [en-GB/govuk] govuk rule\n- general rule\n- [quotes=corner] setting rule"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	fiction := sectionForModel(section, "zh-Hans-CN/fiction")
	if !strings.Contains(fiction, "fiction rule") || strings.Contains(fiction, "taiwan") || strings.Contains(fiction, "govuk") ||
		!strings.Contains(fiction, "general rule") || !strings.Contains(fiction, "setting rule") {
		t.Errorf("fiction: %q", fiction)
	}
	if tw := sectionForModel(section, "zh-Hant-TW/gongwen"); !strings.Contains(tw, "taiwan rule") || strings.Contains(tw, "fiction rule") {
		t.Errorf("tw: %q", tw)
	}
	if !strings.Contains(sectionForModel(section, "en-GB/govuk"), "govuk rule") {
		t.Error("govuk lost its rule")
	}
	if strings.Contains(sectionForModel(section, "en-GB/oxford"), "govuk rule") {
		t.Error("oxford got govuk's rule")
	}
	// Bookkeeping goes; brackets that are content stay.
	for in, want := range map[string]string{
		"- Keep 3G. (guard) [N20]":                                "- Keep 3G.",
		"- A rule (M for . and ,; flag only) [RU-M02; §§199–200]": "- A rule",
		"- Mastodon limits (Mastodon docs)":                       "- Mastodon limits (Mastodon docs)",
		"- [es-ES] « » first":                                     "- [es-ES] « » first",
	} {
		if got := stripBookkeeping(in); got != want {
			t.Errorf("stripBookkeeping(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDocumentLanguageDetection(t *testing.T) {
	for text, want := range map[string]string{
		"林晚推开书店的门，老周正在柜台后面看书。这是她来这座城市的第五年。":                                                       "zh-hans",
		"本校預定於十一月舉辦閱讀週開幕活動，想向貴館借用一樓演講廳。這是我們的請求。":                                                  "zh-hant",
		"雨はまだ降っていた。美咲は改札の前で、何度も時計を見た。":                                                            "ja",
		"이번 업데이트에서는 자료를 내보낼 수 있습니다.":                                                              "ko",
		"Москва — столица России, и это большой город.":                                           "ru",
		"The new reading room is open, and it has 48 seats for the students.":                     "en",
		"El programa comienza a las nueve y la sesión dura una hora con los participantes.":       "es",
		"Le programme commence à neuf heures et la session dure une heure pour les participants.": "fr",
		"Die Sitzung beginnt um neun Uhr und dauert eine Stunde für die Teilnehmer.":              "de",
		"A sessão começa às nove horas e não termina antes das dez para os participantes.":        "pt",
		"# 42": "",
	} {
		if got := detectPackLanguage(text); got != want {
			t.Errorf("%q: got %q, want %q", text, got, want)
		}
	}
	if got := DocumentLanguage("", "ja"); got != (DocLanguage{"ja", FromUILanguage}) {
		t.Errorf("ja: %+v", got)
	}
	if got := DocumentLanguage("", "zh"); got != (DocLanguage{"zh-hans", FromUILanguage}) {
		t.Errorf("zh: %+v", got)
	}
	if got := DocumentLanguage("", "it"); got.Code != "" {
		t.Errorf("it: %+v", got)
	}
}

func TestTranslationTargetsAreFoundInEveryUILanguage(t *testing.T) {
	for _, c := range [][2]string{
		{"翻译成中文", "zh-hans"},
		{"译成日语", "ja"},
		{"转成台湾繁体", "zh-hant"},
		{"翻成繁體中文", "zh-hant"},
		{"Translate this Chinese text into English", "en"},
		{"英語に翻訳して", "en"},
		{"한국어로 번역해 줘", "ko"},
		{"Tradúcelo al inglés", "en"},
		{"Traduis en allemand", "de"},
		{"Übersetze ins Französische", "fr"},
		{"Переведи на английский", "en"},
		{"Traduza para o português", "pt"},
		{"translate to Spanish", "es"},
		{"polish the translation", ""},
	} {
		if got := TargetLanguage(c[0]); got != c[1] {
			t.Errorf("%q: got %q, want %q", c[0], got, c[1])
		}
	}
}

const docZh = "# 第一章\n\n林晚推开书店的门，老周正在柜台后面看书。这是她来这座城市的第五年。"

func containsAll(t *testing.T, text string, parts ...string) {
	t.Helper()
	for _, p := range parts {
		if !strings.Contains(text, p) {
			t.Errorf("missing %q", p)
		}
	}
}

func TestExpansionAppendsTheDeclaredPacksForTheDocumentsLanguage(t *testing.T) {
	// A market skill's packs, as continue-story declares them.
	skill := ParseSkillFile("continue-story",
		"---\nname: continue-story\nmetadata:\n  textus-packs: \"contract craft#fiction standards#dialogue standards#ai-tells\"\n---\nContinue the story.")
	text := ExpandSkill(&skill, docZh, "en", "", "")
	if !strings.HasPrefix(text, skill.Prompt) {
		t.Errorf("the body doesn't come first: %q", text)
	}
	containsAll(t, text, "---references for this skill---",
		"Conventions for this document: Simplified Chinese (detected from the document)",
		"### contract", "### craft#fiction", "### standards-zh-hans#dialogue", "### standards-zh-hans#ai-tells")
	if strings.Contains(text, "standards-en#") {
		t.Error("only the document's language")
	}
	// An English document gets the English pack.
	en := ExpandSkill(&skill, "The rain had not stopped when Elena reached the station and the train was late.", "zh", "", "")
	if !strings.Contains(en, "### standards-en#dialogue") || strings.Contains(en, "zh-hans#") {
		t.Error("the English document didn't get the English pack")
	}
	// The packs' section text arrives without bookkeeping.
	if strings.Contains(text, "(M)") || strings.Contains(text, "[P17") {
		t.Error("bookkeeping left in")
	}
	// A pack a language lacks is simply left out (Korean has no numbers
	// section; notice still gets the rest).
	notice := ParseSkillFile("notice",
		"---\nname: notice\nmetadata:\n  textus-packs: \"contract standards#official standards#numbers\"\n---\nWrite the notice.")
	got := ExpandSkill(&notice, "이번 업데이트에서는 자료를 내보낼 수 있습니다. 몇일 뒤에 다시 안내해 드리겠습니다.", "ko", "", "")
	if !strings.Contains(got, "### standards-ko#official") || strings.Contains(got, "standards-ko#numbers") {
		t.Errorf("notice: %q", got)
	}
}

func TestATranslatingSkillAttachesTheTargetLanguage(t *testing.T) {
	// As the market's Translate says it: its packs follow the target.
	skill := ParseSkillFile("translate",
		"---\nname: translate\ndescription: d\nmetadata:\n  textus-packs: \"contract\"\n  textus-target-packs: \"typography numbers register\"\n---\nBody.\n")
	text := ExpandSkill(&skill, docZh, "zh", "译成日语", "")
	containsAll(t, text, "Target language: Japanese", "### standards-ja#typography", "### standards-ja#numbers", "### standards-ja#register")
	// No target named: just the contract; the body says to load one.
	if text := ExpandSkill(&skill, docZh, "zh", "", ""); !strings.Contains(text, "### contract") || strings.Contains(text, "### standards-") {
		t.Errorf("no target: %q", text)
	}
}

func TestMessagesExpandByNameAliasOrBlock(t *testing.T) {
	skills := BuiltinSkills()
	proofread := mustBuiltin(t, "proofread")
	expand := func(message string) string { return ExpandMessage(message, skills, docZh, "zh", "") }
	full := ExpandSkill(&proofread, docZh, "zh", "第二段", "")
	want := "<skill name=\"proofread\">\n" + full + "\n</skill>\n\n第二段"
	for _, message := range []string{"/proofread 第二段", "/校对 第二段", "／校对 第二段"} {
		if got := expand(message); got != want {
			t.Errorf("%q: got %q", message, got)
		}
	}
	// What the composer sends and an empty block both become the full
	// expansion, and expanding again changes nothing.
	composer := "<selected-text>\nx\n</selected-text>\n\n<skill name=\"proofread\">\n" + proofread.Prompt + "\n</skill>\n\n第二段"
	expanded := expand(composer)
	if expanded != "<selected-text>\nx\n</selected-text>\n\n"+want {
		t.Errorf("composer: got %q", expanded)
	}
	if again := expand(expanded); again != expanded {
		t.Error("expanding twice changed the message")
	}
	empty := "<skill name=\"proofread\"></skill>\n\n第二段"
	if got := expand(empty); got != want {
		t.Errorf("empty block: got %q", got)
	}
	// The composer's typed alias survives, for the transcript.
	typed := "<skill name=\"proofread\" typed=\"/校对\"></skill>\n\n第二段"
	if got := expand(typed); got != strings.Replace(want, "name=\"proofread\">", "name=\"proofread\" typed=\"/校对\">", 1) {
		t.Errorf("typed: got %q", got)
	}
	// A skill block quoted inside an attached file (which goes out first)
	// is the file's text, not a call: only the composer's expands.
	attached := "<attached-file name=\"notes.md\">\n<skill name=\"translate\"></skill>\n</attached-file>\n\n"
	if got := expand(attached + empty); got != attached+want {
		t.Errorf("quoted: got %q", got)
	}
	// A chosen profile filters the packs; Automatic keeps every variant.
	conv := &rules.Conventions{}
	if got := ChosenProfile(conv, docZh); got != "" {
		t.Errorf("Automatic chose %q", got)
	}
	conv.Setting = "zh-Hans-CN/fiction"
	if got := ChosenProfile(conv, docZh); got != "zh-Hans-CN/fiction" {
		t.Errorf("chosen: %q", got)
	}
	// Unknown, or not a skill call: unchanged.
	for _, message := range []string{"/nope x", "/home/me/file.md", "plain"} {
		if got := expand(message); got != message {
			t.Errorf("%q became %q", message, got)
		}
	}
	// A user skill without packs keeps the composer's plain block.
	user := NewSkill("润色")
	user.Prompt = "Polish it."
	users := []AgentSkill{user}
	if got := ExpandMessage("/润色 第二段", users, docZh, "zh", ""); got != "<skill name=\"润色\">\nPolish it.\n</skill>\n\n第二段" {
		t.Errorf("user skill: %q", got)
	}
	if got := ExpandMessage("/润色", users, docZh, "zh", ""); got != "<skill name=\"润色\">\nPolish it.\n</skill>" {
		t.Errorf("user skill alone: %q", got)
	}
}

func TestTheThemeSkillAnswersToItsNameInEveryUILanguage(t *testing.T) {
	skills := BuiltinSkills()
	theme := mustBuiltin(t, "theme")
	if theme.Category == nil || *theme.Category != "create" {
		t.Errorf("category %v", theme.Category)
	}
	for _, code := range uiCodes {
		for _, alias := range theme.Aliases[code] {
			if s, ok := FindInvocable(skills, alias); !ok || s.Name != "theme" {
				t.Errorf("%s: /%s", code, alias)
			}
		}
	}
	message := ExpandMessage("/theme a calm dark page", skills, "", "en", "")
	if !strings.HasPrefix(message, "<skill name=\"theme\">\n") || !strings.Contains(message, "`--doc-bg`") || !strings.HasSuffix(message, "a calm dark page") {
		t.Errorf("got %q", message)
	}
}

func TestPacksLoadByNameAndSection(t *testing.T) {
	load := func(name, document, ui string) string {
		t.Helper()
		text, ok := LoadReference(name, document, ui)
		if !ok {
			t.Fatalf("%s didn't load", name)
		}
		return text
	}
	if fr := load("standards-fr#dialogue", docZh, "zh"); !strings.HasPrefix(fr, "### standards-fr#dialogue") {
		t.Errorf("another language's pack has a document header: %q", fr[:min(len(fr), 80)])
	}
	containsAll(t, load("standards#numbers", docZh, "zh"), "Simplified Chinese", "### standards-zh-hans#numbers")
	containsAll(t, load("standards-ja", "", "en"), "### standards-ja#typography", "### standards-ja#ai-tells")
	if !strings.HasPrefix(load("contract", "", "en"), "### contract\n") {
		t.Error("contract has a header")
	}
	for _, name := range []string{"craft#nope", "standards-it"} {
		if _, ok := LoadReference(name, "", "en"); ok {
			t.Errorf("%s loaded", name)
		}
	}
	if slices.Contains(ReferencePackNames(), "contract") {
		t.Error("contract is offered to load")
	}
}

func TestCustomizedCopiesSayWhatTheyCameFrom(t *testing.T) {
	raw, ok := RawBuiltin("proofread")
	if !ok {
		t.Fatal("no proofread")
	}
	parsed := ParseSkillFile("proofread", WithBasedOn(raw, "proofread@1"))
	if parsed.BasedOn == nil || *parsed.BasedOn != "proofread@1" {
		t.Errorf("based on %v", parsed.BasedOn)
	}
	if parsed.Prompt != mustBuiltin(t, "proofread").Prompt || parsed.Invocation != InvokeBoth {
		t.Errorf("the copy reads differently: %+v", parsed)
	}
	if !reflect.DeepEqual(parsed.Aliases, mustBuiltin(t, "proofread").Aliases) {
		t.Error("the copy lost its aliases")
	}
}
