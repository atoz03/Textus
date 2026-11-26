package plugins

import (
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestTokensCountCJKByCharacterAndOtherTextByFour(t *testing.T) {
	for text, want := range map[string]uint64{"": 0, "abcd": 1, "abcde": 2, "春天的花园": 5, "春天 spring": 2 + 2} {
		if got := Tokens(text); got != want {
			t.Errorf("%q: %d", text, got)
		}
	}
}

func TestANewMonthStartsCountingAgain(t *testing.T) {
	usage := func() AIUsage {
		return AIUsage{Month: "2026-10", Plugins: map[string]*PluginAIUsage{"a.b": {Used: 90_000}}}
	}
	if now := usage().Fresh("2026-10"); !reflect.DeepEqual(now, usage()) {
		t.Errorf("the same month: %+v", now)
	}
	next := usage().Fresh("2026-11")
	if e := next.Plugins["a.b"]; e.Used != 0 || next.Month != "2026-11" {
		t.Errorf("a new month: %+v", e)
	}
}

func TestTheStatusIsWhetherAIIsOnAndWhatThePluginUsed(t *testing.T) {
	// No yes to wait for and no allowance to run out: a plugin that is on
	// with ai.model just asks.
	if got := StatusOf(nil, true); got != (AIStatus{On: true}) {
		t.Errorf("none used: %+v", got)
	}
	if got := StatusOf(&PluginAIUsage{Used: 250_000}, false); got != (AIStatus{Used: 250_000}) {
		t.Errorf("%+v", got)
	}
}

func TestACancelledCallCountsWhatWasWritten(t *testing.T) {
	if got, ok := Counted("whole", nil, "wh"); !ok || got != "whole" {
		t.Errorf("done: %q", got)
	}
	if got, ok := Counted("", errorString(Cancelled), "wh"); !ok || got != "wh" {
		t.Errorf("cancelled: %q", got)
	}
	if _, ok := Counted("", errorString("offline"), "wh"); ok {
		t.Error("a failure is counted")
	}
}

type errorString string

func (e errorString) Error() string { return string(e) }

func TestAToolIsNamedForItsPluginInANameAModelTakes(t *testing.T) {
	if got := ModelName("catvinci.word-count", "count words"); got != "plugin_word-count_count_words" {
		t.Errorf("%s", got)
	}
	if got := ModelName("a.b", strings.Repeat("x", 100)); len(got) > 64 {
		t.Errorf("%d characters", len(got))
	}
}

func TestARequestIsOfferedItsOwnWindowsToolsOnce(t *testing.T) {
	tool := func(plugin, name string) AITool { return AITool{Plugin: plugin, Name: name} }
	var names []string
	for _, t := range WindowTools([]AITool{tool("a.count", "words")}) {
		names = append(names, t.Name)
	}
	if !slices.Equal(names, []string{"plugin_count_words"}) {
		t.Errorf("%q", names)
	}
	// Two plugins' tools of one name stay apart.
	names = nil
	for _, t := range WindowTools([]AITool{tool("a.count", "words"), tool("b.count", "words")}) {
		names = append(names, t.Name)
	}
	if !slices.Equal(names, []string{"plugin_count_words", "plugin_count_words_"}) {
		t.Errorf("%q", names)
	}
}

func TestADescriptionNamesOtherToolsAsTheModelSeesThem(t *testing.T) {
	names := [][2]string{{"list_reminders", "plugin_reminders_list_reminders"}, {"read", "plugin_x_read"}}
	got := ModelNamesIn("Timed ones are list_reminders; read it, not list_reminders_2.", names)
	if want := "Timed ones are plugin_reminders_list_reminders; read it, not list_reminders_2."; got != want {
		t.Errorf("%s", got)
	}
}

func TestAnAnswerIsCutToWhatTheModelIsShown(t *testing.T) {
	if got := Trimmed("  "); got != "(The tool returned nothing.)" {
		t.Errorf("%q", got)
	}
	if got := Trimmed(strings.Repeat("x", ToolResultChars+10)); !strings.HasSuffix(got, "[cut off: the rest was too long]") {
		t.Errorf("%q", got[len(got)-40:])
	}
}

func TestReadsTheExtensionsAManifestNames(t *testing.T) {
	manifest := `{"contributes":{"documentTypes":[
            {"extensions":["qmd", ".RMD"]},{"extensions":[]},{"keep":{}}]}}`
	if got := ExtensionsIn(manifest); !slices.Equal(got, []string{"qmd", "rmd"}) {
		t.Errorf("%q", got)
	}
	for _, none := range []string{"{}", "not json"} {
		if got := ExtensionsIn(none); len(got) != 0 {
			t.Errorf("%s: %q", none, got)
		}
	}
}

func TestAListedExtensionIsADocument(t *testing.T) {
	SetDocumentTypes([]string{`{"contributes":{"documentTypes":[{"extensions":["qmd"]}]}}`})
	defer SetDocumentTypes(nil)
	if !IsDocumentType("qmd") || IsDocumentType("md") || IsDocumentType("") {
		t.Errorf("%q", DocumentTypes())
	}
}
