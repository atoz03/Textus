package ai

import (
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"

	"textus/backend/ai/rules"
)

func names(entries []*LibraryEntry) []string {
	out := []string{}
	for _, e := range entries {
		out = append(out, e.Name)
	}
	return out
}

func TestLibraryEntriesParseFrontMatterListsInBothScripts(t *testing.T) {
	entry := ParseLibraryEntry(".textus/library/lin.md", "lin", "---\nname: 林晚\naliases: [晚晚, 林老师]\ninclude: always\n---\n女主角。")
	if entry.Name != "林晚" || !slices.Equal(entry.Aliases, []string{"晚晚", "林老师"}) || entry.Include != IncludeAlways || entry.Body != "女主角。" {
		t.Errorf("got %+v", entry)
	}
	block := ParseLibraryEntry("f", "lin", "---\naliases:\n  - Lin\n  - \"Dr. Lin\"\ninclude: always\n---\n")
	if !slices.Equal(block.Aliases, []string{"Lin", "Dr. Lin"}) || block.Include != IncludeAlways {
		t.Errorf("got %+v", block)
	}
	plain := ParseLibraryEntry("f", "Harbour", "A port city.")
	if plain.Name != "Harbour" || plain.Include != IncludeDetected {
		t.Errorf("got %+v", plain)
	}
}

func TestCommentsInLibraryEntriesNeverReachTheModel(t *testing.T) {
	// The "New library entry…" template explains itself in a comment; left
	// untouched, the entry has no facts and is not an entry.
	if fresh := ParseLibraryEntry("f", "lin", "---\nname: Lin Wan\ninclude: detected\n---\n\n<!-- Facts to keep consistent. -->\n"); fresh.Body != "" {
		t.Errorf("got %q", fresh.Body)
	}
	if noted := ParseLibraryEntry("f", "lin", "---\nname: Lin Wan\n---\n<!-- note to self -->\nTeaches Chinese."); noted.Body != "Teaches Chinese." {
		t.Errorf("got %q", noted.Body)
	}
}

func TestOnlyMentionedOrAlwaysEntriesAreRelevant(t *testing.T) {
	library := []LibraryEntry{
		ParseLibraryEntry("a", "a", "---\nname: 林晚\naliases: [晚晚]\n---\n女主角。"),
		ParseLibraryEntry("b", "b", "---\nname: Harbour\n---\nA port."),
		ParseLibraryEntry("c", "c", "---\nname: Rules\ninclude: always\n---\nNo magic."),
		ParseLibraryEntry("d", "d", "---\nname: Secret\ninclude: never\n---\nHidden."),
	}
	if got := names(PickLibrary(library, "晚晚走进教室，Secret 不该出现").Included); !slices.Equal(got, []string{"林晚", "Rules"}) {
		t.Errorf("got %v", got)
	}
	// An entry with nothing written in it yet is never sent.
	empty := append(library, ParseLibraryEntry("e", "e", "---\nname: New\n---\n<!-- what to know about New -->\n"))
	if got := names(PickLibrary(empty, "New and 林晚").Included); slices.Contains(got, "New") {
		t.Errorf("got %v", got)
	}
}

func TestTheLibraryCapCountsCharactersAndNamesWhatItLeftOut(t *testing.T) {
	// 3,000 Chinese characters are 9,000 bytes: two such entries fit in
	// 8,000 characters, the third doesn't.
	body := strings.Repeat("林", 3_000)
	var library []LibraryEntry
	for _, n := range []string{"甲", "乙", "丙"} {
		library = append(library, ParseLibraryEntry(n, n, fmt.Sprintf("---\nname: %s%s\ninclude: always\n---\n%s", n, n, body)))
	}
	pick := PickLibrary(library, "")
	if len(pick.Included) != 2 || len(pick.LeftOut) != 1 || pick.LeftOut[0].Name != "丙丙" {
		t.Errorf("included %v, left out %v", names(pick.Included), names(pick.LeftOut))
	}
	// The Memory tab marks entries by file.
	use := UseOf(pick, false)
	if !slices.Equal(use.LeftOut, []string{"丙"}) || !slices.Equal(use.Included, []string{"甲", "乙"}) || use.Cap != MaxLibraryChars {
		t.Errorf("got %+v", use)
	}
}

func TestTheWritingContextAddsTheProfileAndTheEntriesMentioned(t *testing.T) {
	style := "Short sentences."
	ws := &AgentWorkspace{Style: &style, Library: []LibraryEntry{
		ParseLibraryEntry("a", "a", "---\nname: Lin\n---\nTeaches Chinese."),
		ParseLibraryEntry("b", "b", "---\nname: Harbour\n---\nA port."),
	}}
	got := WritingContext("Complete it.", ws, "Lin walked", "")
	for _, part := range []string{"Short sentences.", "Teaches Chinese.", "Write the way the style profile describes.", "Output only the words to insert."} {
		if !strings.Contains(got, part) {
			t.Errorf("missing %q in %q", part, got)
		}
	}
	if strings.Contains(got, "A port.") {
		t.Error("an entry the text doesn't mention was sent")
	}
	if !strings.HasPrefix(got, "Complete it.\n\n") || !strings.HasSuffix(got, "Output only the words to insert.") {
		t.Errorf("got %q", got)
	}
	// Without a profile, the entries alone and no word about a profile.
	ws.Style = nil
	if got := WritingContext("Complete it.", ws, "Lin", ""); strings.Contains(got, "style profile describes") || !strings.Contains(got, "Teaches Chinese.") {
		t.Errorf("got %q", got)
	}
	// With neither, the instructions as they were.
	if got := WritingContext("Complete it.", &AgentWorkspace{}, "Lin", ""); got != "Complete it." {
		t.Errorf("got %q", got)
	}
}

func TestLearnedProfilesUseTheTemplateHeadings(t *testing.T) {
	// "Create style profile" writes projectStyleTemplate; "Learn my style"
	// must head its profile the same way in each UI language.
	instructions := StyleInstructions()
	checked := 0
	for _, language := range rules.UILanguages {
		raw, err := os.ReadFile("../../src/i18n/locales/" + language.Code + ".ts")
		if err != nil {
			t.Fatal(err)
		}
		template := ""
		for _, l := range lines(string(raw)) {
			if strings.HasPrefix(strings.TrimLeft(l, " \t"), "projectStyleTemplate:") {
				template = l
				break
			}
		}
		if template == "" {
			// Not translated yet: the English starter, English headings.
			continue
		}
		headings, ok := styleHeadings(language.Code)
		if !ok {
			t.Fatalf("style-headings/%s.txt is empty", language.Code)
		}
		parts := strings.Split(headings, " / ")
		if len(parts) != 8 {
			t.Errorf("%s: %d headings, not a title and seven sections", language.Code, len(parts))
		}
		if !strings.Contains(instructions, language.English+": "+headings+"\n") && !strings.HasSuffix(instructions, language.English+": "+headings) {
			t.Errorf("%s: the headings are not in the instructions", language.Code)
		}
		for _, part := range strings.Split(template, `\n`) {
			if at := strings.Index(part, "#"); at >= 0 {
				part = part[at:]
			} else {
				part = ""
			}
			if strings.HasPrefix(part, "# ") || strings.HasPrefix(part, "## ") {
				checked++
				heading := strings.TrimRight(part, "`,")
				if !slices.Contains(parts, heading) {
					t.Errorf("%s: %s is not in style-headings/%s.txt", language.Code, heading, language.Code)
				}
			}
		}
	}
	if checked < 15 {
		t.Errorf("found %d headings", checked)
	}
}

func memory(kind MemoryKind, text, updated string) Memory {
	return Memory{
		ID: fmt.Sprintf("project/memory/%d", len(text)), Scope: ScopeProject, Kind: kind, Text: text,
		Remembered: Remembered{Updated: updated},
	}
}

func TestUnderTheBudgetEveryMemoryGoes(t *testing.T) {
	m := Memories{Items: []Memory{memory(Preference, "Short replies.", "2026-01-01")}, Cap: MaxMemoryChars}
	m.SelectFor("anything")
	if !m.Items[0].Included {
		t.Error("left out under the budget")
	}
}

func TestOverItTheRulesGoThenWhatBearsOnTheRequestOldOrNew(t *testing.T) {
	// 200 newer preferences that fill the budget, one old memory about the
	// lighthouse story, and one old requirement.
	var items []Memory
	for i := range 200 {
		items = append(items, memory(Preference, fmt.Sprintf("偏好%d：段落之间空一行，标题不加句号，引用用直角引号。%s", i, strings.Repeat("。", i%3)), "2026-10-20"))
	}
	items = append(items, memory(StyleMemory, "灯塔看守人林晚说话简短，从不叫陌生人的名字。", "2025-01-01"),
		memory(Requirement, "Never use em dashes.", "2025-01-01"))
	m := Memories{Items: items, Cap: MaxMemoryChars}
	m.SelectFor("帮我续写灯塔看守人那一章，林晚开门")
	included := func(text string) bool {
		for _, item := range m.Items {
			if strings.Contains(item.Text, text) {
				return item.Included
			}
		}
		t.Fatalf("no memory %q", text)
		return false
	}
	if !included("Never use em dashes.") || !included("灯塔看守人林晚") {
		t.Error("the rule or the memory about the request was left out")
	}
	if m.Chars > MaxMemoryChars || !slices.ContainsFunc(m.Items, func(i Memory) bool { return !i.Included }) {
		t.Errorf("%d characters, everything included", m.Chars)
	}
}
