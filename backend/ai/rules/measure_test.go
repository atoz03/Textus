package rules

import (
	"slices"
	"strings"
	"testing"
)

func TestXWeighsCJKURLsAndEmoji(t *testing.T) {
	for _, c := range []struct {
		text string
		n    int
	}{
		{"hello", 5},
		{"你好", 4},
		{"こんにちは", 10},
		{"안녕", 4},
		// Any URL is 23, with or without a scheme.
		{"see https://example.com/a/very/long/path?x=1", 4 + 23},
		{"textus.app", 23},
		// In Chinese text too, with no space around the address.
		{"访问textus.app了解", 4 + 23 + 4},
		// Abbreviations are not domains.
		{"e.g. ok", 7},
		// An emoji is 2, ZWJ family and flags included; curly quotes are 1.
		{"👍", 2},
		{"\U0001F469\u200D\U0001F469\u200D\U0001F467", 2},
		{"🇯🇵", 2},
		{"“ok”", 4},
		// NFC first: a decomposed é is one character.
		{"e\u0301", 1},
	} {
		if got := XWeighted(c.text); got != c.n {
			t.Errorf("%q: got %d, want %d", c.text, got, c.n)
		}
	}
}

func TestBlueskyGraphemesAndBytes(t *testing.T) {
	m, _ := MeasureText("\U0001F469\u200D\U0001F469\u200D\U0001F467 hi", "bluesky")
	if m.Count != 4 || !m.Within {
		t.Errorf("%+v", m)
	}
	m, _ = MeasureText(strings.Repeat("字", 1001), "bluesky") // 1001 graphemes, 3003 bytes
	if m.Within || !strings.Contains(m.Describe(), "OVER") {
		t.Errorf("%+v", m)
	}
}

func TestCountsForOtherPlatforms(t *testing.T) {
	if n := CJKChars("我用 React 写了 한글 と"); n != 7 {
		t.Errorf("cjk %d", n)
	}
	if n := Words("我用 React 写的 code-review tool"); n != 8 {
		t.Errorf("words %d", n)
	}
	// As the status bar's count (Intl.Segmenter) has them.
	for _, c := range []struct {
		text string
		n    int
	}{{"well-known", 2}, {"don't", 1}, {"3.14", 1}, {"U.S.", 1}, {"1,000 people", 2}, {"한국어 문장", 2}, {"end. Next", 2}} {
		if got := Words(c.text); got != c.n {
			t.Errorf("%q: got %d, want %d", c.text, got, c.n)
		}
	}
	if m, _ := MeasureText("关于 AI 写作的研究", "zh_academic_title"); m.Count != 8 {
		t.Errorf("%+v", m)
	}
	if m, _ := MeasureText("Apply for a passport", "govuk_summary"); !slices.ContainsFunc(m.Notes, func(n string) bool { return strings.Contains(n, "full stop") }) {
		t.Errorf("%+v", m)
	}
	if m, _ := MeasureText("#a #b #c #d #e #f text", "instagram"); m.Within {
		t.Errorf("%+v", m)
	}
	if m, _ := MeasureText("Hi @ann@mastodon.social see https://x.org/long/path", "mastodon"); m.Count != len([]rune("Hi @ann see "))+23 {
		t.Errorf("%+v", m)
	}
	if _, ok := MeasureText("", "nonsense"); ok {
		t.Error("unknown metric")
	}
	for _, metric := range Metrics {
		if _, ok := MeasureText("text", metric); !ok {
			t.Error(metric)
		}
	}
	m, _ := MeasureText("一二三四五六七八九十一二三四五六七八九十一", "xhs_title")
	if m.Within || !strings.Contains(m.Describe(), "Unverified") {
		t.Errorf("%+v", m)
	}
}
