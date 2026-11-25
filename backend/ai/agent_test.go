package ai

import (
	"encoding/json/v2"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	"textus/backend/ai/aicompat"
	"textus/backend/ai/rules"
)

// The ports of src-tauri/src/ai/agent/tests.rs and lists/tests.rs.

func md(text string) *Document {
	d := DocumentFromMarkdown(text)
	return &d
}

func called(id, name, arguments string) aicompat.AgentTurn {
	return aicompat.AgentTurn{Kind: aicompat.ToolCalled, CallID: id, Name: name, Arguments: arguments}
}

func result(id, output string) aicompat.AgentTurn {
	return aicompat.AgentTurn{Kind: aicompat.ToolResult, CallID: id, Output: output}
}

func TestALengthTheUserAsksForIsNoticed(t *testing.T) {
	for _, asked := range []string{"写一篇 1200 字左右的文章", "写三千字", "Write about 500 words on tides", "2,000 characters, please", "800字以内"} {
		if !asksForALength(asked) {
			t.Error(asked)
		}
	}
	for _, other := range []string{"写一篇文章，配两张图", "Fix the 3 typos", "第2段"} {
		if asksForALength(other) {
			t.Error(other)
		}
	}
}

// scripted runs a Write request on document whose steps are script, through
// the real tools, settled as the agent settles them. It answers the turns
// and how many steps ran.
func scripted(t *testing.T, document string, script []aicompat.StepResult) ([]aicompat.AgentTurn, int) {
	t.Helper()
	d := md(document)
	tools := BuiltinTools(false, ModeWrite)
	tc := NewToolContext(d)
	steps := 0
	turns, err := aicompat.Loop{
		MaxSteps: 12,
		Step: func([]aicompat.AgentTurn) (aicompat.StepResult, error) {
			step := script[steps]
			steps++
			settled, _ := SettleStep(step, d, tc.Conventions)
			return settled, nil
		},
		Execute:  func(c aicompat.ToolCall) aicompat.ToolOutput { return tools.Execute(tc, c) },
		HowToRun: tools.HowToRun,
	}.Run(nil, aicompat.UserTurn("Go"))
	if err != nil {
		t.Fatal(err)
	}
	return turns, steps
}

func outputOf(turns []aicompat.AgentTurn, id string) string {
	for _, turn := range turns {
		if turn.Kind == aicompat.ToolResult && turn.CallID == id {
			return turn.Output
		}
	}
	return ""
}

func proposedEdits(turns []aicompat.AgentTurn) int {
	n := 0
	for _, turn := range turns {
		if turn.Kind == aicompat.ToolResult && strings.HasPrefix(turn.Output, "Edit proposed") {
			n++
		}
	}
	return n
}

func TestAScriptedAgentReadsThenProposesThroughTheRealTools(t *testing.T) {
	doc := "# Notes\n\n## Methods\n\nWe sampled ten sites.\n\n## Results\n\nAll good."
	methods := md(doc).Blocks[2].ID
	bad := fmt.Sprintf(`{"edits":[{"action":"replace","block":%q,"anchor":"We sampled 10 sites.","text":"x"}]}`, methods)
	ok := fmt.Sprintf(`{"edits":[{"action":"replace","block":%q,"text":"We sampled ten coastal sites."}]}`, methods)
	turns, _ := scripted(t, doc, []aicompat.StepResult{
		aicompat.CallsWithText([]aicompat.AgentTurn{called("r", ReadDocumentToolName, `{"heading":"Methods"}`)}, ""),
		aicompat.CallsWithText([]aicompat.AgentTurn{called("bad", ProposeEditToolName, bad)}, ""),
		aicompat.CallsWithText([]aicompat.AgentTurn{called("ok", ProposeEditToolName, ok)}, ""),
		aicompat.Reply("Named the site type."),
	})
	if read := outputOf(turns, "r"); !strings.Contains(read, "We sampled ten sites.") || strings.Contains(read, "All good.") {
		t.Fatal(read)
	}
	if out := outputOf(turns, "bad#0"); !strings.Contains(out, "exactly once") {
		t.Fatal(out)
	}
	if out := outputOf(turns, "ok#0"); !strings.HasPrefix(out, "Edit proposed") {
		t.Fatal(out)
	}
}

func comment(id, document, anchor string) aicompat.AgentTurn {
	d := md(document)
	block := ""
	for _, b := range d.Blocks {
		if strings.Contains(d.BlockText(b), anchor) {
			block = b.ID
			break
		}
	}
	return called(id, AddCommentToolName, fmt.Sprintf(`{"comments":[{"block":%q,"anchor":%q,"comment":"The title is a pun; this keeps one half."}]}`, block, anchor))
}

func translation(id, document string) aicompat.AgentTurn {
	b := md(document).Blocks
	return called(id, ProposeEditToolName, fmt.Sprintf(`{"edits":[{"action":"replace","block":%q,"last_block":%q,"text":"# Borrowed Rain\n\nA rainy night."}]}`, b[0].ID, b[1].ID))
}

func TestACommentAndASentenceBeforeAnyEditIsAskedOn(t *testing.T) {
	document := "# 借雨\n\n雨夜。"
	turns, steps := scripted(t, document, []aicompat.StepResult{
		aicompat.CallsWithText([]aicompat.AgentTurn{comment("c0", document, "借雨")}, "Translated it all; one note on the title."),
		aicompat.CallsWithText([]aicompat.AgentTurn{translation("e1", document)}, "Done."),
	})
	if steps != 2 || proposedEdits(turns) != 1 {
		t.Fatal(steps, proposedEdits(turns))
	}
}

func TestEditsCommentsAndASentenceInOneStepEndTheRequest(t *testing.T) {
	document := "# 借雨\n\n雨夜。"
	turns, steps := scripted(t, document, []aicompat.StepResult{
		aicompat.CallsWithText([]aicompat.AgentTurn{comment("c0", document, "借雨"), translation("e0", document)}, "Translated it; one note on the title."),
	})
	if steps != 1 || proposedEdits(turns) != 1 {
		t.Fatal(steps, proposedEdits(turns))
	}
}

func TestAWholeDocumentRewrittenInOneReplaceEndsInOneStep(t *testing.T) {
	seven := "一。\n\n二。\n\n三。\n\n四。\n\n五。\n\n六。\n\n七。"
	b := md(seven).Blocks
	var parts []string
	for n := 1; n <= 7; n++ {
		parts = append(parts, fmt.Sprintf("Part %d.", n))
	}
	edits := fmt.Sprintf(`{"edits":[{"action":"replace","block":%q,"last_block":%q,"text":%q}]}`, b[0].ID, b[6].ID, strings.Join(parts, "\n\n"))
	turns, steps := scripted(t, seven, []aicompat.StepResult{
		aicompat.CallsWithText([]aicompat.AgentTurn{called("e0", ProposeEditToolName, edits)}, "Translated it all."),
	})
	if steps != 1 || proposedEdits(turns) != 1 {
		t.Fatal(steps, proposedEdits(turns))
	}
}

func TestHistoryKeepsNoToolImagesNorReasoning(t *testing.T) {
	shown := result("v", "Here is `map.png` (image/png).")
	shown.Images = []aicompat.ImageAttachment{{Name: "map.png", Mime: "image/png", DataBase64: "AAAA"}}
	kept := ForHistory(shown)
	if len(kept.Images) != 0 || !strings.HasSuffix(kept.Output, "call read_file again to look at it.]") {
		t.Fatal(kept.Output)
	}
	if ForHistory(result("r", "text")).Output != "text" {
		t.Fatal("a plain result changed")
	}
	thought := called("c", ReadDocumentToolName, "{}")
	thought.Reasoning = []byte(`"thought"`)
	if ForHistory(thought).Reasoning != nil {
		t.Fatal("reasoning kept")
	}
}

func TestCompactHistoryStubsOldReadsButKeepsEditResults(t *testing.T) {
	big := strings.Repeat("x", 5_000)
	out := CompactHistory([]aicompat.AgentTurn{
		aicompat.UserTurn("q"), called("r", "read_file", "{}"), result("r", big),
		called("e", ProposeEditToolName, "{}"), result("e", big),
	})
	if len(out[2].Output) >= 1_000 || !strings.Contains(out[2].Output, "omitted") || out[4].Output != big {
		t.Fatal(len(out[2].Output), len(out[4].Output))
	}
}

func TestCompactHistoryDropsWholeOldestExchangesOverBudget(t *testing.T) {
	out := CompactHistory([]aicompat.AgentTurn{
		aicompat.UserTurn(strings.Repeat("y", historyBudget)), aicompat.AssistantTurn("a1"),
		aicompat.UserTurn("recent"), aicompat.AssistantTurn("a2"),
	})
	if len(out) != 2 || out[0].Text != "recent" {
		t.Fatal(out)
	}
	if _, forgot := CompactTelling([]aicompat.AgentTurn{aicompat.UserTurn("hi"), aicompat.AssistantTurn("hello")}); forgot {
		t.Fatal("a short conversation forgot")
	}
	long := []aicompat.AgentTurn{aicompat.UserTurn(strings.Repeat("y", historyBudget)), aicompat.AssistantTurn("a1"), aicompat.UserTurn("recent")}
	if _, forgot := CompactTelling(long); !forgot {
		t.Fatal("a conversation that loses its start doesn't say so")
	}
}

// exchange is one exchange of chars characters: a question, a file read,
// a reply.
func exchange(n, chars int) []aicompat.AgentTurn {
	id := fmt.Sprintf("r%d", n)
	return []aicompat.AgentTurn{
		aicompat.UserTurn(fmt.Sprintf("question %d", n)), called(id, "read_file", "{}"),
		result(id, strings.Repeat("z", 2_000)), aicompat.AssistantTurn(strings.Repeat("a", chars)),
	}
}

func debug(turns []aicompat.AgentTurn) string { return fmt.Sprintf("%+v", turns) }

func TestCompactedHistoryIsTheSameOnEverySend(t *testing.T) {
	history := exchange(0, 100)
	first := CompactHistory(slices.Clone(history))
	if !strings.Contains(debug(first), "omitted") {
		t.Fatal("not cut")
	}
	for n := 1; n < 5; n++ {
		history = append(history, exchange(n, 100)...)
		if later := CompactHistory(slices.Clone(history)); debug(later[:len(first)]) != debug(first) {
			t.Fatal("the cut changed")
		}
	}
}

func TestOverBudgetTheOldestExchangesGoInBlocks(t *testing.T) {
	var history, previous []aicompat.AgentTurn
	sends, moves := 0, 0
	for n := range 60 {
		history = append(history, exchange(n, 9_000)...)
		out := CompactHistory(slices.Clone(history))
		kept := 0
		for _, turn := range out {
			kept += turnChars(turn)
		}
		if kept > historyBudget {
			t.Fatal(kept, "chars kept")
		}
		if previous != nil && n > 20 {
			sends++
			if len(out) < len(previous) || debug(out[:len(previous)]) != debug(previous) {
				moves++
				if kept < historyBudget-historyDropStep-10_000 {
					t.Fatal("a move kept only", kept)
				}
			}
		}
		previous = out
	}
	if moves*4 >= sends || moves == 0 {
		t.Fatal(moves, "moves in", sends, "sends")
	}
}

func TestOverBudgetALongAttachmentIsNotDroppedEarly(t *testing.T) {
	var history []aicompat.AgentTurn
	for n := range 3 {
		history = append(history, exchange(n, 10_000)...)
	}
	history = append(history, aicompat.UserTurn("<attached-file>"+strings.Repeat("p", 130_000)+"</attached-file>"), aicompat.AssistantTurn("summary"))
	history = append(history, exchange(5, 5_000)...)
	out := CompactHistory(history)
	if out[0].Text != "question 2" || !strings.Contains(debug(out), "attached-file") {
		t.Fatal(out[0].Text)
	}
}

func TestTheMessageHeaderNamesTheLanguageTheUserTyped(t *testing.T) {
	cases := map[string]string{
		"What was the most common complaint?": "English",
		"把这段翻译成英文":                            "Simplified Chinese",
		"把這段翻譯成英文":                            "Traditional Chinese",
		"この段落を短くしてください":                       "Japanese",
		"<selected-text>\n图书馆每周日下午四点就闭馆了，很多读者抱怨。\n</selected-text>\n\nMake this more formal.":                     "English",
		"<skill name=\"tighten\" typed=\"/tighten\">\nTighten the passage. Keep every fact.\n</skill>\n\n帮我精简这一段": "Simplified Chinese",
		"ok": "",
	}
	for message, want := range cases {
		if got, _ := MessageLanguage(message); got != want {
			t.Errorf("%q: %q", message, got)
		}
	}
	if text := RequestUserText("doc", "What was the most common complaint?"); !strings.HasSuffix(text, "---the user's message (written in English: reply in English, unless the user instructions or what you remember set a reply language)---\nWhat was the most common complaint?") {
		t.Fatal(text)
	}
	if !strings.HasSuffix(RequestUserText("doc", "ok"), "---the user's message---\nok") {
		t.Fatal("no header for a message too short to tell")
	}
}

func TestStyleAndMentionedLibraryEntriesReachThePrompt(t *testing.T) {
	style := "Short sentences. No adverbs."
	ws := AgentWorkspace{Style: &style, Library: []LibraryEntry{
		ParseLibraryEntry("a", "a", "---\nname: 林晚\naliases: [晚晚]\n---\n中学语文老师。"),
		ParseLibraryEntry("b", "b", "---\nname: 港城\n---\n海边城市。"),
	}}
	prompt, _ := RequestContext(&ws, md("晚晚推开门。"), LeastReading(true), "改写这句", "")
	if !strings.Contains(prompt, style) || !strings.Contains(prompt, "中学语文老师") || strings.Contains(prompt, "海边城市") {
		t.Fatal(prompt)
	}
}

func builtinWorkspace() AgentWorkspace {
	return AgentWorkspace{Skills: BuiltinSkills(), Memories: NoMemories()}
}

func TestTheAppGuideIsLoadedOnDemandWhereSkillsCanBe(t *testing.T) {
	plain := AgentWorkspace{Memories: NoMemories()}
	if !strings.Contains(workspaceSection(&plain, "en"), "What the app calls the things") {
		t.Fatal("no guide without skills")
	}
	ws := builtinWorkspace()
	section := workspaceSection(&ws, "en")
	if strings.Contains(section, "What the app calls the things") || !strings.Contains(section, "use_skill app-guide") || !strings.Contains(appGuideText, "Learn my style") {
		t.Fatal(section)
	}
}

func TestTheSkillIndexOffersModelSkillsAndPrefersTheUsers(t *testing.T) {
	ws := builtinWorkspace()
	project := NewSkill("house-headline")
	project.Description, project.Source = "Titles in the house style", SourceProject
	mine := NewSkill("my-tone")
	mine.Description = strings.Repeat("d", 700)
	resting := NewSkill("resting")
	resting.Description, resting.Off = "Off", true
	ws.Skills = append(ws.Skills, project, mine, resting)
	index := strings.Split(skillIndex(&ws, ModeWrite, "en"), "\n")
	if len(index) != 2 || !strings.HasPrefix(index[0], "- house-headline (project): ") ||
		!strings.HasPrefix(index[1], "- my-tone (yours): ") || !strings.HasSuffix(index[1], "…") || utf8.RuneCountInString(index[1]) >= 540 {
		t.Fatal(index)
	}
	review := skillIndex(&ws, ModeReview, "en")
	if !strings.Contains(review, "- proofread: Use when asked to proofread, check for typos or errors, or check against a standard.") || strings.Contains(review, "校对") {
		t.Fatal(review)
	}
	if zh := skillIndex(&ws, ModeReview, "zh"); !strings.Contains(zh, "- proofread [/校对 in their UI]: ") || strings.Contains(zh, "- house-headline") {
		t.Fatal(zh)
	}
	create := skillIndex(&ws, ModeCreate, "en")
	for _, name := range []string{"theme", "plugin", "skill"} {
		if !strings.Contains(create, "- "+name+" [create]:") {
			t.Fatal(create)
		}
	}
	if strings.Contains(create, "- proofread:") || !strings.Contains(skillIndex(&ws, ModeCreate, "zh"), "- theme [create] [/") {
		t.Fatal(create)
	}
	// The system prompt is the same whatever the document.
	a := BuildInstructions(&ws, md("# One"), true, ModeWrite, "hi", "en")
	b := BuildInstructions(&ws, md("# Two\n\nOther text."), true, ModeWrite, "other", "en")
	if a != b || !strings.Contains(a, "When a skill marked (project) or (yours) and an unmarked built-in skill both match a request, use the marked one.") ||
		!strings.Contains(a, "standards-zh-hant") || strings.Contains(a, "contract,") {
		t.Fatal(a)
	}
	none := AgentWorkspace{Memories: NoMemories()}
	if strings.Contains(BuildInstructions(&none, md(""), true, ModeWrite, "", "en"), "---available skills---") {
		t.Fatal("an index with nothing to offer")
	}
}

func TestTheWorkspaceSectionSaysWhichFolderAndWhy(t *testing.T) {
	lone := AgentWorkspace{RootSource: RootNone, Memories: NoMemories()}
	text := workspaceSection(&lone, "en")
	if !strings.Contains(text, "no folder is open") || !strings.Contains(text, "Settings > Textus AI") ||
		!strings.Contains(text, "attach it with +") || strings.Contains(text, "attached/") || strings.Contains(text, "click the folder name") {
		t.Fatal(text)
	}
	root := "/tmp/novel"
	chosen := AgentWorkspace{Root: &root, RootSource: RootChosen, Memories: NoMemories()}
	if text := workspaceSection(&chosen, "en"); !strings.Contains(text, "chosen by the user") || !strings.Contains(text, "click the folder name") || !strings.Contains(text, "Use the sidebar's folder") {
		t.Fatal(text)
	}
	withFiles := lone
	withFiles.Attached = true
	if !strings.Contains(workspaceSection(&withFiles, "en"), "under `attached/`") {
		t.Fatal("attached files not named")
	}
	tooBroad := AgentWorkspace{RootSource: RootTooBroad, Memories: NoMemories()}
	text = workspaceSection(&tooBroad, "en")
	if !strings.Contains(text, "too broad") || strings.Contains(text, "click the folder name") || !strings.Contains(text, "Learn my style") || strings.Contains(text, "English |") {
		t.Fatal(text)
	}
	chinese := workspaceSection(&tooBroad, "zh")
	if !strings.Contains(chinese, "English | 简体中文\nfolder | 文件夹\n") || !strings.Contains(chinese, `not "style profile (文风档案)", not "资料库 (library)"`) || strings.Contains(chinese, "日本語") {
		t.Fatal(chinese)
	}
	if workspaceSection(&tooBroad, "xx") != text {
		t.Fatal("an unknown UI language isn't English")
	}
	if strings.Contains(BuildInstructions(&withFiles, md("draft"), false, ModeWrite, "", "en"), "---project---") {
		t.Fatal("the tool-less fallback hears of file tools")
	}
	if review := RequestPreamble(&chosen, ModeReview, "en"); !strings.Contains(review, "Review mode:") || !strings.Contains(review, "General mode you") {
		t.Fatal(review)
	}
	if review := RequestPreamble(&chosen, ModeReview, "ja"); !strings.Contains(review, "Review mode (レビュー in their Japanese UI)") || !strings.Contains(review, "General mode (汎用)") {
		t.Fatal(review)
	}
}

// readRepo is a file of the repository, "" when it isn't there.
func readRepo(path string) string {
	raw, _ := os.ReadFile("../../" + path)
	return string(raw)
}

func TestThePromptNamesThingsExactlyAsTheUIDoes(t *testing.T) {
	// Every UI label the agent is told about must be the one the user sees,
	// in each language. Read from the frontend's files.
	names := readRepo("src/i18n/names.ts")
	if !strings.Contains(names, `export const AI_NAME = "`+AIName+`";`) {
		t.Error("AI_NAME differs from src/i18n/names.ts")
	}
	var config struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal([]byte(readRepo("mygo.json")), &config); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(names, `export const APP_NAME = "`+config.Name+`";`) || !strings.HasPrefix(AIName, config.Name+" ") {
		t.Errorf("APP_NAME in src/i18n/names.ts and AI_NAME differ from %q, mygo.json's name", config.Name)
	}
	// The same languages as the UI offers, in the same order.
	_, list, _ := strings.Cut(readRepo("src/i18n/langs.ts"), "export const LANGS = [")
	list, _, found := strings.Cut(list, "] as const")
	if !found {
		t.Fatal("no LANGS in src/i18n/langs.ts")
	}
	var codes, ours []string
	for i, part := range strings.Split(list, `"`) {
		if i%2 == 1 {
			codes = append(codes, part)
		}
	}
	for _, l := range rules.UILanguages {
		ours = append(ours, l.Code)
	}
	if !slices.Equal(codes, ours) {
		t.Errorf("LANGS is %q, the backend's %q", codes, ours)
	}

	// A chosen project folder: the case that names every label.
	root := "/tmp/novel"
	chosen := AgentWorkspace{Root: &root, RootSource: RootChosen, Memories: NoMemories()}
	english := entries(uiFile("ui-names", "en"))
	for _, language := range rules.UILanguages {
		code := language.Code
		uiStrings := readRepo("src/i18n/locales/" + code + ".ts")
		guide := strings.ToLower(readRepo("src/help/agent-guide." + code + ".md"))
		// A name is the UI's, or, for the few that are no label - "project
		// menu" - the help guide's. "Style (the chip)": the label is what
		// precedes the note.
		known := func(name string) bool {
			name, _, _ = strings.Cut(name, " (")
			return strings.Contains(uiStrings, name) || strings.Contains(guide, strings.ToLower(name))
		}
		if code == "en" {
			for _, name := range english {
				if !known(name) {
					t.Errorf("neither the UI nor the guide says %s", name)
				}
			}
			continue
		}
		lines := entries(uiFile("ui-names", code))
		if len(lines) == 0 {
			continue
		}
		var inEnglish []string
		var pairs [][2]string
		for _, line := range lines {
			en, theirs, ok := strings.Cut(line, " = ")
			if !ok {
				t.Fatalf("ui-names/%s.txt: no \" = \" in %s", code, line)
			}
			inEnglish = append(inEnglish, en)
			pairs = append(pairs, [2]string{en, theirs})
		}
		if !slices.Equal(inEnglish, english) {
			t.Errorf("ui-names/%s.txt names every line of en.txt, in its order", code)
		}
		prompt := BuildInstructions(&chosen, md("draft"), true, ModeReview, "", code)
		if !strings.Contains(prompt, "\nEnglish | "+NativeName(code)+"\n") {
			t.Errorf("%s: the prompt lacks the names' header", code)
		}
		for _, pair := range pairs {
			if !strings.Contains(prompt, pair[0]+" | "+pair[1]) {
				t.Errorf("%s: the prompt lacks %s", code, pair[1])
			}
			if !known(pair[1]) {
				t.Errorf("%s: neither the UI nor the guide says %s", code, pair[1])
			}
		}
	}

	// The labels the English description quotes, and those too short to
	// look up on their own, are exactly the strings' entries.
	prompt := BuildInstructions(&chosen, md("draft"), true, ModeReview, "", "en")
	en := readRepo("src/i18n/locales/en.ts")
	for _, label := range []string{
		"Choose folder…",
		"Use the sidebar's folder",
		"Learn my style from samples…",
		"Create style profile",
		"Open style profile",
		"New library entry…",
		"Edit the folder's writing instructions",
		"Writing Instructions (agent.md)",
		"Edit agent.md",
		"Import Skill…",
		// The Settings pages and groups its paths go through.
		"Memory & skills",
		"Chat & writing",
		"Continue & Check",
		"Memory",
	} {
		if !strings.Contains(prompt, label) {
			t.Errorf("the prompt lacks %s", label)
		}
		if !strings.Contains(en, `"`+label+`"`) {
			t.Errorf("the UI has no %s", label)
		}
	}
	zh, ja := readRepo("src/i18n/locales/zh.ts"), readRepo("src/i18n/locales/ja.ts")
	for _, c := range []struct{ strings, entry string }{
		{en, `agentWorkspaceRootLabel: "Folder"`},
		{zh, `agentWorkspaceRootLabel: "文件夹"`},
		{ja, `agentWorkspaceRootLabel: "フォルダー"`},
		{zh, `navSkills: "技能"`},
		{ja, `navSkills: "スキル"`},
		{zh, `settingsTitle: "设置"`},
		{ja, `settingsTitle: "設定"`},
		{zh, `aiCompletionLabel: "续写"`},
		{ja, `completionToneLabel: "補完のトーン"`},
		{zh, `proposalAccept: "采纳"`},
		{ja, `proposalAccept: "適用"`},
		{zh, `proposalReject: "拒绝"`},
		{ja, `proposalReject: "却下"`},
	} {
		if !strings.Contains(c.strings, c.entry) {
			t.Errorf("the UI has no %s", c.entry)
		}
	}
	if !strings.Contains(prompt, "Settings > "+AIName+" > Memory & skills > Folder") {
		t.Error("the prompt lacks the folder's Settings path")
	}
	// The backend's own pointer to Settings names the same page.
	if !strings.Contains(NotConfigured, "Settings > "+AIName+".") {
		t.Errorf("NotConfigured: %s", NotConfigured)
	}
}

func TestEachModeIsToldItsTools(t *testing.T) {
	ws := AgentWorkspace{Memories: NoMemories()}
	create := BuildInstructions(&ws, md("draft"), true, ModeCreate, "", "en")
	if !strings.Contains(create, agentCreateToolInstructions) || strings.Contains(create, rulesToolInstructions) ||
		!strings.Contains(BuildInstructions(&ws, md("draft"), true, ModeWrite, "", "en"), rulesToolInstructions) {
		t.Fatal("Create's tools")
	}
	message := RequestMessage(&ws, ModeCreate, "en", "", "Make a skill for 500-word posts.")
	if !strings.Contains(message, "interaction mode: create") || !strings.Contains(message, "switch to General mode") || strings.Contains(message, lengthTargetNote) {
		t.Fatal(message)
	}
	if !strings.Contains(RequestMessage(&ws, ModeWrite, "en", "", "Write 500 words."), lengthTargetNote) {
		t.Fatal("no length note")
	}
	review := RequestMessage(&ws, ModeReview, "en", "---document---\ndraft", "Go.")
	if !strings.Contains(review, "the <selected-text> block when there is one") || !strings.Contains(review, "`suggestion`") ||
		!strings.Contains(BuildInstructions(&ws, md("draft"), true, ModeReview, "", "en"), "No document edit tool is available") {
		t.Fatal(review)
	}
	write := RequestMessage(&ws, ModeWrite, "en", "---document---\ndraft", "Go.")
	if !strings.Contains(write, "replaces it with replace_selection") || !strings.Contains(write, "right after it") || strings.Contains(write, "Revise") ||
		strings.Index(write, "interaction mode") > strings.Index(write, "---document---") {
		t.Fatal(write)
	}
	if !strings.Contains(BuildInstructions(&ws, md("draft"), false, ModeWrite, "", "en"), "interaction mode: write") {
		t.Fatal("tool-less, the mode stays in the system prompt")
	}
}

func TestProjectWritingInstructionsFollowAndOverrideTheGlobalOnes(t *testing.T) {
	global, project, agents := "Always reply in English.", "Reply in Chinese.", "Keep British spelling."
	both := AgentWorkspace{Instructions: []string{global}, ProjectInstructions: &project, AgentsMD: &agents, Memories: NoMemories()}
	prompt := BuildInstructions(&both, md("draft"), true, ModeWrite, "", "en")
	g, p, a := strings.Index(prompt, global), strings.Index(prompt, project), strings.Index(prompt, agents)
	if g >= p || a <= p || !strings.Contains(prompt, "---user instructions for this folder (where they conflict with the ones above, follow these)---\nReply in Chinese.") {
		t.Fatal(prompt)
	}
	for _, alone := range []AgentWorkspace{{Instructions: []string{global}}, {ProjectInstructions: &global}} {
		if prompt := BuildInstructions(&alone, md("draft"), true, ModeWrite, "", "en"); !strings.Contains(prompt, "---user instructions---\nAlways reply in English.") {
			t.Fatal(prompt)
		}
	}
}

func TestTheCachedPrefixSurvivesANewMemory(t *testing.T) {
	ws := AgentWorkspace{Remember: true, Memories: NoMemories()}
	before := BuildInstructions(&ws, md("draft"), true, ModeWrite, "", "en")
	ws.Memories.Items = []Memory{{ID: "personal/memory/x", Scope: ScopePersonal, Kind: Preference, Text: "Reply briefly.", Included: true}}
	if BuildInstructions(&ws, md("draft"), true, ModeWrite, "", "en") != before {
		t.Fatal("a memory changed the system prompt")
	}
	if !strings.Contains(RequestMessage(&ws, ModeWrite, "en", "", "Go."), "Reply briefly.") {
		t.Fatal("the memory isn't sent")
	}
	if !strings.Contains(BuildInstructions(&ws, md("draft"), false, ModeReview, "", "en"), "Reply briefly.") {
		t.Fatal("without tools it is in the system prompt")
	}
}

func TestASelectedPictureIsItsSrc(t *testing.T) {
	ask := func(selection string) (string, bool) {
		return SelectedPicture("<selected-text>\n" + selection + "\n</selected-text>\n\nWhat is in it?")
	}
	for selection, want := range map[string]string{
		"![Boats](assets/boats.png)":              "assets/boats.png",
		`![Boats](<assets/my boats.png> "Boats")`: "assets/my boats.png",
		"![Boats](assets/my%20boats.png)":         "assets/my boats.png",
	} {
		if src, ok := ask(selection); !ok || src != want {
			t.Error(selection, src)
		}
	}
	for _, selection := range []string{"Look: ![Boats](assets/boats.png)", "![Boats](https://example.com/boats.png)"} {
		if _, ok := ask(selection); ok {
			t.Error(selection)
		}
	}
	if _, ok := SelectedPicture("What is in it?"); ok {
		t.Error("no selection")
	}
}

func TestAListCallIsOneCallPerItemAndJoinsBackAsTheModelWroteIt(t *testing.T) {
	edits := `{"edits":[{"action":"replace","block":"a","text":"One."},{"action":"replace","block":"b","text":"Two."}]}`
	split := SplitCalls(aicompat.CallsWithText([]aicompat.AgentTurn{called("c1", ProposeEditToolName, edits), called("c2", "read_file", `{"path":"x"}`)}, "")).Calls
	ids := func(turns []aicompat.AgentTurn) []string {
		var out []string
		for _, turn := range turns {
			out = append(out, string(turn.Kind)+" "+turn.CallID)
		}
		return out
	}
	if got := ids(split); !slices.Equal(got, []string{"ToolCall c1#0", "ToolCall c1#1", "ToolCall c2"}) {
		t.Fatal(got)
	}
	if split[1].Arguments != `{"action":"replace","block":"b","text":"Two."}` {
		t.Fatal(split[1].Arguments)
	}
	turns := append(split, result("c1#0", "Edit proposed. [accepted]"), result("c1#1", "Not proposed: no block b."), result("c2", "file"))
	joined := JoinCalls(turns)
	if got := ids(joined); !slices.Equal(got, []string{"ToolCall c1", "ToolCall c2", "ToolResult c1", "ToolResult c2"}) {
		t.Fatal(got)
	}
	if joined[0].Arguments != edits {
		t.Fatal(joined[0].Arguments)
	}
	if joined[2].Output != "1. Edit proposed. [accepted]\n2. Not proposed: no block b." {
		t.Fatal(joined[2].Output)
	}
	single := []aicompat.AgentTurn{called("c1", AddCommentToolName, `{"block":"a","comment":"Why?"}`)}
	if got := ids(JoinCalls(SplitCalls(aicompat.CallsWithText(single, "")).Calls)); !slices.Equal(got, []string{"ToolCall c1"}) {
		t.Fatal(got)
	}
}

// streamed is the events a stream of deltas for list call c1 gives.
func streamed(tool string, deltas ...string) []string {
	var stream ListStream
	var seen []string
	out := func(e aicompat.ProviderEvent) {
		switch e.Kind {
		case aicompat.EventToolStart:
			seen = append(seen, "start "+e.CallID+" "+e.Name)
		case aicompat.EventToolArgs:
			seen = append(seen, e.CallID+": "+e.Text)
		case aicompat.EventText:
			seen = append(seen, "text "+e.Text)
		}
	}
	stream.Feed(aicompat.ProviderEvent{Kind: aicompat.EventToolStart, CallID: "c1", Name: tool}, out)
	for _, d := range deltas {
		stream.Feed(aicompat.ProviderEvent{Kind: aicompat.EventToolArgs, CallID: "c1", Text: d}, out)
	}
	return seen
}

func TestEachItemStreamsAsACallOfItsOwn(t *testing.T) {
	events := streamed(AddCommentToolName, `{"comm`, `ents": [{"block": "a", `, `"comment": "Why {so} \"this\"?"}, {"blo`, `ck": "b", "comment": "Hm"}]}`)
	want := []string{
		"start c1#0 add_comment", `c1#0: {"block": "a", `, `c1#0: "comment": "Why {so} \"this\"?"}`,
		"start c1#1 add_comment", `c1#1: {"blo`, `c1#1: ck": "b", "comment": "Hm"}`,
	}
	if !slices.Equal(events, want) {
		t.Fatalf("%q", events)
	}
	var stream ListStream
	var seen []string
	out := func(e aicompat.ProviderEvent) { seen = append(seen, fmt.Sprint(e.Kind, e.CallID, e.Text)) }
	stream.Feed(aicompat.ProviderEvent{Kind: aicompat.EventText, Text: "Hi"}, out)
	stream.Feed(aicompat.ProviderEvent{Kind: aicompat.EventToolStart, CallID: "r", Name: "read_file"}, out)
	stream.Feed(aicompat.ProviderEvent{Kind: aicompat.EventToolArgs, CallID: "r", Text: "{}"}, out)
	if len(seen) != 3 {
		t.Fatal("other calls and text don't pass through", seen)
	}
}

func TestTokensGoOutMergedAndInOrder(t *testing.T) {
	var mu sync.Mutex
	var sent []string
	paced := NewPaced(func(e StreamEvent) {
		mu.Lock()
		defer mu.Unlock()
		if e.Type == "retry" {
			sent = append(sent, "|")
		} else {
			sent = append(sent, e.Text)
		}
	})
	for _, word := range []string{"a", "b", "c"} {
		paced.Send(StreamEvent{Type: "delta", Text: word})
	}
	paced.Send(StreamEvent{Type: "retry"})
	paced.Send(StreamEvent{Type: "delta", Text: "d"})
	paced.Send(StreamEvent{Type: "delta", Text: "e"})
	paced.Flush()
	if strings.Join(sent, "") != "abc|de" || len(sent) > 5 {
		t.Fatal(sent)
	}
}

func TestEventsAreTheShapeTheFrontendReads(t *testing.T) {
	for event, want := range map[*StreamEvent]string{
		{Type: "delta", Text: "Hi"}:                             `{"type":"delta","text":"Hi"}`,
		{Type: "thinking"}:                                      `{"type":"thinking","text":""}`,
		{Type: "toolStart", CallID: "c1", Name: "propose_edit"}: `{"type":"toolStart","callId":"c1","name":"propose_edit"}`,
		{Type: "toolArgsDelta", CallID: "c1", Delta: `{"a":`}:   `{"type":"toolArgsDelta","callId":"c1","delta":"{\"a\":"}`,
		{Type: "retry"}:                                         `{"type":"retry"}`,
		{Type: "forgot"}:                                        `{"type":"forgot"}`,
		{Type: "turn", Turn: aicompat.AssistantTurn("Done.")}:   `{"type":"turn","turn":{"kind":"Assistant","text":"Done."}}`,
	} {
		raw, err := event.MarshalJSON()
		if err != nil || string(raw) != want {
			t.Errorf("%s, not %s (%v)", raw, want, err)
		}
	}
}

func TestTheUILanguagesNativeNamesAreTheLanguageMenus(t *testing.T) {
	raw, err := os.ReadFile("../../src/prefs/appearance-tables.json")
	if err != nil {
		t.Fatal(err)
	}
	var tables struct {
		LangNames map[string]string `json:"langNames"`
	}
	if err := json.Unmarshal(raw, &tables); err != nil {
		t.Fatal(err)
	}
	if !maps.Equal(tables.LangNames, nativeNames) {
		t.Fatalf("%v, not %v", nativeNames, tables.LangNames)
	}
}
