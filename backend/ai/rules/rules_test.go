package rules

import (
	"slices"
	"strings"
	"testing"
	"time"
)

func TestLevelsPickRulesByPriorityAndCategory(t *testing.T) {
	for _, c := range []struct {
		rule     string
		level    Level
		docLevel bool
		want     bool
	}{
		{"zh.word.typo", LevelTypos, false, true},
		{"zh.punct.halfwidth", LevelTypos, false, false},
		{"zh.punct.halfwidth", LevelStandard, false, true},
		{"en.word.wordy", LevelStandard, false, false},
		{"en.word.wordy", LevelStrict, false, true},
		{"en.punct.dash-style", LevelStandard, true, true},
		{"en.punct.dash-style", LevelStandard, false, false},
		{"tw.gongwen.year", LevelStandard, false, true},
	} {
		if got := allowed(info(c.rule), c.level, c.docLevel); got != c.want {
			t.Errorf("%s at %d (doc %v): got %v", c.rule, c.level, c.docLevel, got)
		}
	}
}

func TestSharedMessagesFollowTheAppsLanguage(t *testing.T) {
	text := "This sentence has  two spaces in the middle of it, written in English."
	messageIn := func(ui string) string {
		for _, f := range CheckFor(text, defaultConv(), LevelStandard, nil, ui) {
			if strings.HasPrefix(f.Rule, "shared.") {
				return f.Message
			}
		}
		t.Fatal("no double space found")
		return ""
	}
	english, chinese := messageIn(""), messageIn("zh")
	if english == chinese {
		t.Error("the app's language, not the text's")
	}
	eqStr(t, chinese, message("shared.double-space", ZhHans))
}

func TestTheGrammarCheckGetsParagraphRulesOnly(t *testing.T) {
	conv := defaultConv()
	found := Check("今天下午开会,大家讨论了三个问题.", conv, LevelStandard, nil)
	var pairs [][2]string
	for _, f := range found {
		pairs = append(pairs, [2]string{f.Original, deref(f.Suggestion)})
	}
	if !slices.Equal(pairs, [][2]string{{",", "，"}, {".", "。"}}) {
		t.Fatalf("%v", pairs)
	}
	eqStr(t, found[0].Message, "中文句子里应使用全角标点。")
	// Typos level: spelling rules only.
	if f := Check("今天下午开会,大家讨论了三个问题.", conv, LevelTypos, nil); len(f) != 0 {
		t.Errorf("%v", f)
	}
	typo := Check("我们要再接再励,继续努力。", conv, LevelTypos, nil)
	if len(typo) != 1 || deref(typo[0].Suggestion) != "再接再厉" {
		t.Errorf("%v", typo)
	}
	// Too short to tell the language: nothing language-specific.
	if f := Check("ok , fine", conv, LevelStandard, nil); len(f) != 0 {
		t.Errorf("%v", f)
	}
}

func TestApplySkipsOverlapsAndIsIdempotent(t *testing.T) {
	conv := defaultConv()
	text := "我们用React写了一个工具,很快...真的。"
	once := Apply(text, Check(text, conv, LevelStandard, nil))
	eqStr(t, once, "我们用React写了一个工具，很快……真的。")
	for _, f := range Check(once, conv, LevelStandard, nil) {
		if f.Fix == FixSafe {
			t.Errorf("%+v", f)
		}
	}
}

func TestAParagraphChecksWellUnderBudget(t *testing.T) {
	para := strings.Repeat("我们用 React 和 Tauri 重写了编辑器,启动时间从 3 秒降到了 800ms。The rewrite took two months, and the the team shipped it. ", 20)
	start := time.Now()
	for range 10 {
		Check(para, defaultConv(), LevelStrict, nil)
	}
	// ~2,000 chars x 10 runs; generous for -race and CI.
	if d := time.Since(start); d > 6*time.Second {
		t.Errorf("took %v", d)
	}
}

func TestURLsPathsCodeAndMathAreProtected(t *testing.T) {
	tx := NewText("见 https://a.com/x,然后 `a,b` 和 src/main.rs 与 $x,y$ 以及 a@b.cn。")
	prot := func(needle string) bool {
		m := findStr(tx, needle)[0]
		for i := m[0]; i < m[1]; i++ {
			if !tx.Protected[i] {
				return false
			}
		}
		return true
	}
	for _, needle := range []string{"https://a.com/x", "`a,b`", "src/main.rs", "$x,y$", "a@b.cn"} {
		if !prot(needle) {
			t.Errorf("%q is not protected", needle)
		}
	}
	if tx.Protected[findStr(tx, ",然后")[0][0]] {
		t.Error("the comma after the URL is prose")
	}
	// Words with a slash and currency are not paths or math.
	tx = NewText("and/or he/she costs $5 and $10")
	if slices.Contains(tx.Protected, true) {
		t.Errorf("%v", tx.Protected)
	}
}

func TestQuotesAndSentenceStarts(t *testing.T) {
	tx := NewText("He said “the the end” and left. Then she spoke.")
	m := findStr(tx, "the the")[0]
	if !tx.inQuotes(m[0], m[1]) || tx.inQuotes(0, 2) {
		t.Error("inQuotes")
	}
	if !tx.sentenceStart(findStr(tx, "Then")[0][0]) || tx.sentenceStart(findStr(tx, "she")[0][0]) {
		t.Error("sentenceStart")
	}
	eqStr(t, matchCase("Fué", "fue"), "Fue")
	eqStr(t, matchCase("ALOT", "a lot"), "A LOT")
	if n := len(findWord(tx, "the", true)); n != 2 {
		t.Errorf("findWord: %d", n)
	}
}

func TestRuleIDsAreUniqueAndScoped(t *testing.T) {
	seen := map[string]bool{}
	scopes := []string{"shared", "zh", "tw", "en", "ja", "ko", "es", "fr", "de", "ru", "pt"}
	for _, r := range registry {
		if seen[r.id] {
			t.Errorf("%s twice", r.id)
		}
		seen[r.id] = true
		scope, _, _ := strings.Cut(r.id, ".")
		if !slices.Contains(scopes, scope) {
			t.Errorf("%s: scope", r.id)
		}
		if r.sites == 0 {
			t.Errorf("%s: no sites", r.id)
		}
		// Only P0 rules with safe fixes post-process proposals.
		if r.sites&sitePR != 0 && r.id != "shared.residue" && !(r.priority == P0 && r.fix == FixSafe) {
			t.Errorf("%s post-processes proposals", r.id)
		}
	}
}

func TestEveryLanguageHasEveryMessageWithTheSamePlaceholders(t *testing.T) {
	en := messageTables["en"]
	if len(en) < 10 {
		t.Fatalf("%d English messages", len(en))
	}
	for code, table := range messageTables {
		for key, text := range en {
			theirs, ok := table[key]
			if !ok {
				t.Errorf("%s: missing %s", code, key)
				continue
			}
			for _, ph := range []string{"{0}", "{1}", "{2}"} {
				if strings.Contains(text, ph) != strings.Contains(theirs, ph) {
					t.Errorf("%s: %s: %s", code, key, ph)
				}
			}
		}
		for key := range table {
			if _, ok := en[key]; !ok {
				t.Errorf("%s: %s is not in en.txt", code, key)
			}
		}
	}
	// Every UI language has a file.
	if len(messageTables) != len(UILanguages) || len(messageTables) != 10 {
		t.Errorf("%d tables", len(messageTables))
	}
	eqStr(t, message("shared.library-name", En, "Elizabeth"), "Possible misspelling of the library name “Elizabeth”.")
}

func TestCountsSpacingQuotesAndRegister(t *testing.T) {
	s := &Stats{}
	s.count(NewText("我们用 React 写了 2 个工具,很快。"), ZhHans)
	if s.cjkLatinSpaced != 4 || s.cjkLatinTight != 0 || s.cjkFwPunct != 1 || s.cjkHwPunct != 1 {
		t.Errorf("%+v", s)
	}
	s = &Stats{}
	s.count(NewText("Il a dit : « oui » ; puis non! Il est 13:52."), Fr)
	if s.frSpace != 2 || s.frNone != 1 {
		t.Errorf("%+v", s)
	}
	s = &Stats{}
	s.count(NewText("雨です。晴れである。曇りです。"), Ja)
	if s.jaDesu != 2 || s.jaDearu != 1 {
		t.Errorf("%+v", s)
	}
	if !(minority(1, 3) && minority(2, 4) && !minority(1, 2) && !minority(0, 5) && !minority(2, 3)) {
		t.Error("minority")
	}
}
