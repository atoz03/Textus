package ai

import (
	"encoding/json/v2"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"

	"textus/backend/ai/aicompat"
)

// The ports of src-tauri/src/ai/tools/tests.rs.

func execute(tools *ToolRegistry, tc *ToolContext, name, arguments string) aicompat.ToolOutput {
	return tools.Execute(tc, aicompat.ToolCall{Name: name, Arguments: arguments})
}

func testContext(document string) *ToolContext {
	d := DocumentFromMarkdown(document)
	return NewToolContext(&d)
}

func jsonArgs(t *testing.T, v any) string {
	t.Helper()
	raw, err := json.Marshal(v, json.Deterministic(true))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// named is a call as the model sends it, naming the block its `anchor` is
// in, resolved as the agent does before the tool sees it. Words in no
// block stay a quote.
func named(d *Document, arguments string) string {
	args, _ := ParseArgs(arguments)
	if anchor, ok := args.Str("anchor"); ok && !args.Has("block") {
		var holding []Block
		for _, b := range d.Blocks {
			if strings.Contains(d.BlockText(b), anchor) {
				holding = append(holding, b)
			}
		}
		if len(holding) == 1 {
			args.Set("block", holding[0].ID)
		}
	}
	return ResolveEditArguments(d, args.String())
}

func edit(tc *ToolContext, arguments string) string {
	return proposeEdit(tc, named(tc.Document, arguments))
}

func toolNames(r *ToolRegistry) []string {
	var names []string
	for _, s := range r.Specs() {
		names = append(names, s.Name)
	}
	return names
}

func runtimeTool(name string, access Access, modes []AgentMode) Tool {
	return Tool{
		Spec:   aicompat.ToolSpec{Name: name, Description: name + ", from a plugin", Parameters: map[string]any{"type": "object"}},
		Access: access, Modes: modes,
		Run: func(_ *ToolContext, call aicompat.ToolCall) aicompat.ToolOutput {
			return aicompat.Text(call.Name + " ran with " + call.Arguments)
		},
	}
}

func TestARuntimeToolIsOfferedAndRunThroughTheRegistry(t *testing.T) {
	tools := BuiltinTools(false, ModeWrite)
	tools.Add(runtimeTool("zotero_search", ReadOnly, EveryMode))
	if !slices.Contains(toolNames(tools), "zotero_search") {
		t.Fatal(toolNames(tools))
	}
	if out := execute(tools, testContext("Doc."), "zotero_search", `{"q":"x"}`).Text; out != `zotero_search ran with {"q":"x"}` {
		t.Fatal(out)
	}
	if out := execute(tools, testContext("Doc."), "no_such_tool", "{}").Text; out != "Unknown tool: no_such_tool" {
		t.Fatal(out)
	}
}

func TestValidProposalsAndCommentsAreSettledAndRefusalsAreNot(t *testing.T) {
	tools := BuiltinTools(false, ModeWrite)
	tc := testContext("The cat sat on the mat.")
	settled := func(name, args string) bool { return execute(tools, tc, name, named(tc.Document, args)).Settled }
	if !settled(ProposeEditToolName, `{"action":"append","text":"The dog ran."}`) ||
		settled(ProposeEditToolName, `{"action":"replace","anchor":"not in the document","text":"x"}`) ||
		!settled(AddCommentToolName, `{"anchor":"The cat sat","comment":"Name the cat."}`) ||
		settled(ReadDocumentToolName, "{}") {
		t.Fatal("settled wrong")
	}
}

func TestAToolIsOfferedOnlyInTheModesItNames(t *testing.T) {
	for mode, offered := range map[AgentMode]bool{ModeReview: false, ModeWrite: true} {
		tools := NewToolRegistry(mode)
		tools.Add(runtimeTool("make_figure", Proposes, EditingModes))
		if (len(tools.Specs()) == 1) != offered {
			t.Fatal(mode)
		}
	}
}

func TestReadsRunTogetherAndProposalsInOrder(t *testing.T) {
	tools := BuiltinTools(false, ModeWrite)
	tools.Add(runtimeTool("make_figure", ProposesInItsCall, EditingModes))
	for name, want := range map[string]aicompat.ToolRun{
		ReadDocumentToolName: aicompat.Concurrent, ProposeEditToolName: aicompat.InOrder,
		AddCommentToolName: aicompat.InOrder, "make_figure": aicompat.Amending, "no_such_tool": aicompat.InOrder,
	} {
		if got := tools.HowToRun(name); got != want {
			t.Errorf("%s: %v", name, got)
		}
	}
}

func TestASectionRunsToTheNextHeadingAtItsLevel(t *testing.T) {
	doc := lines("# Intro\na\n## Intro details\nb\n# Methods\nc\n```sh\n# not a heading\n```")
	for heading, want := range map[string][2]int{"Intro": {0, 4}, "## intro DETAILS": {2, 4}, "methods": {4, 9}} {
		if a, b, ok := sectionRange(doc, heading); !ok || [2]int{a, b} != want {
			t.Errorf("%s: %d-%d", heading, a, b)
		}
	}
	for _, heading := range []string{"Results", "  "} {
		if _, _, ok := sectionRange(doc, heading); ok {
			t.Error(heading)
		}
	}
}

func TestUseSkillExpandsSkillsAndLoadsPacks(t *testing.T) {
	tc := testContext("雨はまだ降っていた。美咲は改札の前で、何度も時計を見た。")
	tc.Skills = BuiltinSkills()
	call := func(name string) string { return useSkill(tc, jsonArgs(t, map[string]string{"name": name})) }
	proofread := call("proofread")
	if !strings.HasPrefix(proofread, "Instructions for skill `proofread`") ||
		!strings.Contains(proofread, "### contract") || !strings.Contains(proofread, "### standards-ja#typography") {
		t.Fatal(proofread[:min(len(proofread), 300)])
	}
	for _, moved := range []string{"describe", "tighten"} {
		if !strings.HasPrefix(call(moved), "No skill named `"+moved+"`") {
			t.Error(moved, "is in the market, not built in")
		}
	}
	if fr := call("standards-fr#dialogue"); !strings.HasPrefix(fr, "Reference `standards-fr#dialogue`") || !strings.Contains(fr, "guillemets") {
		t.Error(fr)
	}
	if missing := call("nope"); !strings.Contains(missing, "Reference packs: craft, standards-en") {
		t.Error(missing)
	}
	if guide := call(AppGuide); !strings.HasPrefix(guide, "The app guide - answer from it:") {
		t.Error(guide)
	}
}

func TestCheckToolsStopAnsweringOnceTheirBudgetIsSpent(t *testing.T) {
	tools := BuiltinTools(false, ModeWrite)
	tc := testContext("草稿")
	run := func(name, args string) string { return execute(tools, tc, name, args).Text }
	measure := func(metric string) string {
		return run(MeasureTextToolName, jsonArgs(t, map[string]string{"text": "草稿", "metric": metric}))
	}
	for _, metric := range []string{"", "chars", "cjk_chars"} {
		if out := measure(metric); strings.HasPrefix(out, "Not run") {
			t.Fatal(metric, out)
		}
	}
	if out := measure("words"); !strings.HasPrefix(out, "Not run: measure_text was already called 3 times") {
		t.Fatal(out)
	}
	// A platform's limit is measured as often as it takes.
	for range 10 {
		if out := measure("x_weighted"); !strings.HasPrefix(out, "x_weighted: 4 of 280") {
			t.Fatal(out)
		}
	}
	// Each check has its own budget, and other tools have none.
	for range 4 {
		if out := run(CheckConventionsToolName, "{}"); strings.HasPrefix(out, "Not run") {
			t.Fatal(out)
		}
	}
	if out := run(CheckConventionsToolName, "{}"); !strings.HasPrefix(out, "Not run") {
		t.Fatal(out)
	}
	for range 5 {
		if out := run(SearchToolName, `{"query":"草"}`); strings.HasPrefix(out, "Not run") {
			t.Fatal(out)
		}
	}
}

func TestALongDraftOfNewTextIsProposedNotChecked(t *testing.T) {
	tools := BuiltinTools(false, ModeWrite)
	paragraph := strings.Repeat("港口不是海的终点，是陆地和浪涛握手的地方。", 12)
	tc := testContext(paragraph)
	run := func(name, text, metric string) string {
		return execute(tools, tc, name, jsonArgs(t, map[string]string{"text": text, "metric": metric})).Text
	}
	draft := paragraph + "\n\n" + paragraph
	for _, name := range []string{MeasureTextToolName, CheckConventionsToolName} {
		if out := run(name, draft, "cjk_chars"); !strings.Contains(out, "doesn't take a draft") {
			t.Fatal(name, out)
		}
	}
	for _, c := range [][2]string{{paragraph, "cjk_chars"}, {"一句话。", "cjk_chars"}, {draft, "weibo"}} {
		if out := run(MeasureTextToolName, c[0], c[1]); strings.HasPrefix(out, "Not run") {
			t.Fatal(out)
		}
	}
}

func TestCheckConventionsReportsByRuleWithLines(t *testing.T) {
	doc := "# 周报\n\n本周完成了三项工作，进展顺利。\n\n下周继续推进,争取月底上线...\n"
	out := checkConventions(testContext(doc), "{}")
	for _, want := range []string{"zh.punct.halfwidth (1): 中文句子里应使用全角标点。", `L5 fix: "," -> "，"`, "zh.punct.ellipsis (1)"} {
		if !strings.Contains(out, want) {
			t.Fatalf("%q not in %s", want, out)
		}
	}
	if !strings.HasPrefix(out, "Writing conventions: zh-Hans-CN (Automatic") {
		t.Fatal(out)
	}
	if out := checkConventions(testContext(doc), `{"start_line":1,"end_line":3}`); !strings.Contains(out, "No convention issues found in lines 1-3.") {
		t.Fatal(out)
	}
	if out := checkConventions(testContext(doc), `{"heading":"没有"}`); !strings.HasPrefix(out, "No heading matching") {
		t.Fatal(out)
	}
	if out := checkConventions(testContext(doc), `{"text":"我们还要测试,然后发布。"}`); !strings.Contains(out, "in the text you gave") || !strings.Contains(out, "L1 fix") {
		t.Fatal(out)
	}
	out = checkConventions(testContext("Vraiment ? Nous avons fini le travail dans la maison avec les enfants."), "{}")
	if !strings.Contains(out, "[narrow no-break space]?") || !strings.Contains(out, `write it as \u00a0 or \u202f`) {
		t.Fatal(out)
	}
}

func TestMeasureTextCountsExactly(t *testing.T) {
	out := measureText(nil, `{"text":"你好 https://textus.app","metric":"x_weighted"}`)
	if out != "x_weighted: 28 of 280 weighted characters - within the limit (252 to spare). URLs count 23 each; CJK characters and emoji count 2." {
		t.Fatal(out)
	}
	if out := measureText(nil, `{"text":"hi"}`); !strings.Contains(out, "chars: 2 characters.") || !strings.Contains(out, "bluesky: 2 of 300") {
		t.Fatal(out)
	}
	if !strings.HasPrefix(measureText(nil, `{"text":"hi","metric":"tiktok"}`), "Unknown metric") || !strings.HasPrefix(measureText(nil, "{}"), "No `text`") {
		t.Fatal("measure_text refusals")
	}
}

func TestEachModeIsOfferedItsTools(t *testing.T) {
	for _, mode := range []AgentMode{ModeReview, ModeWrite} {
		names := toolNames(BuiltinTools(false, mode))
		if !slices.Contains(names, CheckConventionsToolName) || !slices.Contains(names, MeasureTextToolName) {
			t.Fatal(mode, names)
		}
		if slices.Contains(names, ProposeEditToolName) != (mode == ModeWrite) {
			t.Fatal("Review mode does not receive the edit tool", names)
		}
	}
	if names := toolNames(BuiltinTools(true, ModeCreate)); !slices.Equal(names, []string{UseSkillToolName}) {
		t.Fatal("Create mode is offered skills only of the built-ins", names)
	}
	shared := SharedToolRegistry()
	for _, tool := range documentTools() {
		shared.Add(tool)
	}
	if len(shared.Specs()) != 0 {
		t.Fatal("a shared registry takes only the tools every mode offers", toolNames(shared))
	}
}

func TestReadDocumentReadsASectionOrALineRange(t *testing.T) {
	doc := "# Title\nintro\n## Methods\nm1\n### Sampling\ns1\n```\n# not a heading\n```\n## Results\nr1"
	out := readDocument(testContext(doc), `{"heading":"methods"}`)
	if !strings.HasPrefix(out, "Lines 3-9 of 11:") || !strings.Contains(out, "### Sampling") ||
		!strings.Contains(out, "# not a heading") || strings.Contains(out, "## Results") {
		t.Fatal(out)
	}
	// One block, so its id comes first.
	tc := testContext("a\nb\nc\nd")
	if out := readDocument(tc, `{"start_line":2,"end_line":3}`); out != "Lines 2-3 of 4:\nb\nc\n" {
		t.Fatalf("%q", out)
	}
}

func TestAnEditOfTextAnUndecidedRewriteReplacesGoesIntoTheRewrite(t *testing.T) {
	tc := testContext("The lighthouse stood on the rock.\n\nWaves broke below.")
	tc.Undecided = []UndecidedEdit{{Replaced: "The lighthouse stood on the rock.", Text: "The old lighthouse stood alone on its rock."}}
	tools := BuiltinTools(false, ModeWrite)
	run := func(args string) string {
		return execute(tools, tc, ProposeEditToolName, named(tc.Document, args)).Text
	}
	if out := run(`{"action":"replace","anchor":"stood alone","text":"stood tall"}`); !strings.Contains(out, "goes into that proposal") {
		t.Fatal(out)
	}
	if out := run(`{"action":"replace","anchor":"lighthouse","text":"beacon"}`); !strings.Contains(out, "takes the place of your earlier rewrite") {
		t.Fatal(out)
	}
	out := run(`{"action":"replace","anchor":"Waves broke below.","text":"Waves crashed below."}`)
	if !strings.HasPrefix(out, "Edit proposed") || strings.Contains(out, "goes into that proposal") {
		t.Fatal(out)
	}
	// The model named the block it had rewritten and quoted its own
	// rewrite.
	block := tc.Document.Blocks[0].ID
	tc = testContext("The lighthouse stood on the rock.\n\nWaves broke below.")
	tc.Undecided = []UndecidedEdit{{Replaced: "The lighthouse stood on the rock.", Text: "The old lighthouse stood alone on its rock."}}
	if out := proposeEdit(tc, jsonArgs(t, map[string]string{"action": "replace", "block": block, "anchor": "stood alone", "text": "stood tall"})); !strings.Contains(out, "goes into that proposal") {
		t.Fatal(out)
	}
	if out := proposeEdit(tc, jsonArgs(t, map[string]string{"action": "replace", "block": block, "anchor": "a dragon", "text": "x"})); !strings.Contains(out, "exactly once") {
		t.Fatal(out)
	}
}

func TestAnEditMayChangeTextProposedAndNotDecidedYet(t *testing.T) {
	tools := BuiltinTools(false, ModeWrite)
	tc := testContext("The cat sat on the mat.")
	tc.Undecided = []UndecidedEdit{{Text: "It purred all night. Then it slept."}}
	run := func(args string) string {
		return execute(tools, tc, ProposeEditToolName, named(tc.Document, args)).Text
	}
	for _, args := range []string{
		`{"action":"replace","anchor":"It purred all night.","text":"It purred."}`,
		`{"action":"delete","anchor":"Then it slept."}`,
		`{"action":"insert_after","anchor":"Then it slept.","text":"At dawn it woke."}`,
	} {
		if out := run(args); !strings.HasPrefix(out, "Edit proposed") || !strings.Contains(out, "goes into that proposal") {
			t.Fatal(args, out)
		}
	}
	if out := run(`{"action":"replace","anchor":"A dog barked.","text":"x"}`); strings.HasPrefix(out, "Edit proposed") {
		t.Fatal(out)
	}
	// This request's own proposals count too.
	tc = testContext("The cat sat on the mat.")
	if out := run(`{"action":"append","text":"A new line here."}`); !strings.HasPrefix(out, "Edit proposed") {
		t.Fatal(out)
	}
	if out := run(`{"action":"replace","anchor":"A new line here.","text":"A newer line."}`); !strings.Contains(out, "goes into that proposal") {
		t.Fatal(out)
	}
}

func TestWriteModeOffersEveryEdit(t *testing.T) {
	tools := BuiltinTools(false, ModeWrite)
	for _, args := range []string{
		`{"action":"replace","anchor":"The cat sat on the mat.","text":"A dog."}`,
		`{"action":"delete","anchor":"The cat sat on the mat."}`,
		`{"action":"insert_after","anchor":"The cat sat on the mat.","text":"It purred."}`,
		`{"action":"append","text":"It purred."}`,
	} {
		tc := testContext("The cat sat on the mat.")
		if out := execute(tools, tc, ProposeEditToolName, named(tc.Document, args)).Text; !strings.HasPrefix(out, "Edit proposed") {
			t.Fatal(args, out)
		}
	}
}

func TestProposeEditRefusesAClauseReplacedByOneLatinToken(t *testing.T) {
	doc := "会议照常。\n\n给您添麻烦了，如果周五上午不方便，请告诉我们您方便的时间。\n\n此致"
	anchor := "给您添麻烦了，如果周五上午不方便，请告诉我们您方便的时间。"
	args := func(text string) string {
		return jsonArgs(t, map[string]string{"action": "replace", "anchor": anchor, "text": text})
	}
	one := testContext(doc)
	if out := edit(one, args("due")); !strings.HasPrefix(out, "Not proposed") {
		t.Fatal(out)
	}
	// Sent again unchanged, it is what the user asked for.
	if out := edit(one, args("due")); !strings.HasPrefix(out, "Edit proposed") {
		t.Fatal(out)
	}
	for _, text := range []string{"Sorry for the trouble - if Friday morning doesn't suit you, tell us what does.", "抱歉。"} {
		if out := edit(testContext(doc), args(text)); !strings.HasPrefix(out, "Edit proposed") {
			t.Fatal(text, out)
		}
	}
	if out := edit(testContext("名称：乐维斯"), `{"action":"replace","anchor":"乐维斯","text":"Textus"}`); !strings.HasPrefix(out, "Edit proposed") {
		t.Fatal(out)
	}
}

func TestARewriteThatChangesNothingIsRefused(t *testing.T) {
	tc := testContext("Textus is great.\n\nIt ships soon.")
	if out := edit(tc, `{"action":"replace","anchor":"Textus is great.","text":"Textus is great.\n"}`); !strings.Contains(out, "nothing would change") {
		t.Fatal(out)
	}
	if out := edit(tc, `{"action":"replace","anchor":"Textus is great.","text":"Textus is good."}`); !strings.HasPrefix(out, "Edit proposed") {
		t.Fatal(out)
	}
	tc = testContext("One line.\n\nIt ships soon.")
	if out := edit(tc, `{"action":"replace","anchor":"One line.","text":"One line.  "}`); !strings.HasPrefix(out, "Edit proposed") {
		t.Fatal("a hard break is a change:", out)
	}
}

func TestASecondProposalForTheSamePassageIsRefused(t *testing.T) {
	tc := testContext("Textus is great.\n\nTextus is fast.\n\nIt ships soon.")
	first := `{"action":"replace","anchor":"Textus is great.","text":"Textus is good."}`
	if out := edit(tc, first); !strings.HasPrefix(out, "Edit proposed") {
		t.Fatal(out)
	}
	if out := edit(tc, first); !strings.HasPrefix(out, "Not proposed") {
		t.Fatal(out)
	}
	inner := jsonArgs(t, map[string]string{"action": "delete", "block": tc.Document.Blocks[0].ID, "anchor": "great"})
	if out := edit(tc, inner); !strings.HasPrefix(out, "Not proposed") {
		t.Fatal(out)
	}
	for _, args := range []string{
		`{"action":"replace","anchor":"It ships soon.","text":"It ships in May."}`,
		`{"action":"insert_after","anchor":"Textus is great.","text":" Really."}`,
	} {
		if out := edit(tc, args); !strings.HasPrefix(out, "Edit proposed") {
			t.Fatal(args, out)
		}
	}
	if out := edit(testContext("Textus is great."), `{"action":"replace","anchor":"Textus is slow.","text":"x"}`); !strings.HasPrefix(out, "Not proposed") {
		t.Fatal(out)
	}
}

func TestTheSizeNoteCountsLikeMeasureText(t *testing.T) {
	out := edit(testContext("周末带爸妈去了千岛湖，人很多。"), `{"action":"replace","anchor":"周末带爸妈去了千岛湖，人很多。","text":"周末去了千岛湖。"}`)
	if !strings.Contains(out, "replaces 15 characters with 8") {
		t.Fatal(out)
	}
}

func TestARepeatedWordIsToldApartByItsBlock(t *testing.T) {
	tc := testContext("Textus is great.\n\nTextus is fast.")
	fast := tc.Document.Blocks[1].ID
	if out := proposeEdit(tc, jsonArgs(t, map[string]string{"action": "replace", "block": fast, "anchor": "Textus", "text": "It"})); !strings.HasPrefix(out, "Edit proposed") {
		t.Fatal(out)
	}
	twice := testContext("Textus is great, Textus is fast.")
	id := twice.Document.Blocks[0].ID
	if out := proposeEdit(twice, jsonArgs(t, map[string]string{"action": "replace", "block": id, "anchor": "Textus", "text": "It"})); !strings.Contains(out, "exactly once") {
		t.Fatal(out)
	}
	if out := proposeEdit(twice, `{"action":"replace","anchor":"great","text":"good"}`); out != NoBlock {
		t.Fatal(out)
	}
}

func TestATextPutsEachPictureItWasGivenInOnce(t *testing.T) {
	var ledger ProposalLedger
	if why := ledger.picturesProblem("![a](textus-generated:zz)"); why != "" {
		t.Fatal(why)
	}
	ledger.pictures = []string{"k3f9qz", "p08mxa"}
	if why := ledger.picturesProblem("One.\n\n![a](textus-generated:k3f9qz)\n\nTwo.\n\n![b](textus-generated:p08mxa)"); why != "" {
		t.Fatal(why)
	}
	if why := ledger.picturesProblem("![a](textus-generated:k3f9qz)\n\n![b](textus-generated:k3f9qz)"); !strings.Contains(why, "twice") || !strings.Contains(why, "p08mxa") {
		t.Fatal(why)
	}
	if why := ledger.picturesProblem("![a](textus-generated:k3f9qy)"); !strings.Contains(why, "no picture of this request") {
		t.Fatal(why)
	}
	if note := ledger.picturesPlaced("One.\n\n![a](textus-generated:k3f9qz)"); !strings.Contains(note, "nothing to fix") || ledger.picturesPlaced("One.") != "" {
		t.Fatal(note)
	}
}

func TestTheEditActionsAreTheFrontendsList(t *testing.T) {
	raw, err := os.ReadFile("../../src/editor/markdown/edit-actions.json")
	if err != nil {
		t.Fatal(err)
	}
	var rules []struct {
		Action   string `json:"action"`
		Anchored bool   `json:"anchored"`
		Text     bool   `json:"text"`
	}
	if err := json.Unmarshal(raw, &rules); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, r := range editRules {
		got = append(got, fmt.Sprint(r.action, r.anchored, r.text))
	}
	var want []string
	for _, r := range rules {
		want = append(want, fmt.Sprint(r.Action, r.Anchored, r.Text))
	}
	if !slices.Equal(got, want) {
		t.Fatalf("%v\n%v", got, want)
	}
}
