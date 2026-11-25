package rules

import (
	"fmt"
	"strings"

	"github.com/rivo/uniseg"
	"golang.org/x/text/unicode/norm"
)

// Exact counts for platform limits (the measure_text tool), because models
// can't count characters. X weighs CJK as 2 and every URL as 23; Bluesky
// counts graphemes; the rest count characters.

// Metrics are the metrics measure_text knows, for its schema and error
// message.
var Metrics = []string{
	"x_weighted", "bluesky", "threads", "instagram", "mastodon", "linkedin", "chars", "cjk_chars", "words",
	"utf8_bytes", "wechat_title", "wechat_digest", "wechat_content", "xhs_title", "xhs_body", "weibo",
	"govuk_title", "govuk_summary", "zh_academic_title",
}

// Measure is one measurement.
type Measure struct {
	Metric string `json:"metric"`
	Count  int    `json:"count"`
	// Limit is the platform's limit, nil for none.
	Limit *int `json:"limit"`
	// Unit is what is counted ("weighted characters", "graphemes", "字").
	Unit   string `json:"unit"`
	Within bool   `json:"within"`
	// Notes are counting rules and warnings worth saying.
	Notes []string `json:"notes"`
}

var (
	measureURLRe = MustRe(`(?i)` + asciiB + `(?:https?://[^\s<>\x22]+|(?:[a-z0-9-]+\.)+(?:com|org|net|io|app|dev|ai|co|cn|jp|kr|de|fr|es|ru|br|pt|uk|us|me|info|edu|gov|tv|ly|xyz|tw|hk|so|gg|eu|ca|au|in)` + asciiB + `(?:/[^\s<>\x22]*)?)`)
	mentionRe    = MustRe(`(@[A-Za-z0-9_]+)@[A-Za-z0-9.-]+\.[A-Za-z]{2,}`)
	hashtagRe    = MustRe(`(?:^|\s)[#＃][\p{L}\p{N}_]+`)
)

func isEmojiGrapheme(g string) bool {
	for _, c := range g {
		if c >= 0x1F000 && c <= 0x1FAFF || c >= 0x2600 && c <= 0x27BF || c >= 0x2B00 && c <= 0x2BFF ||
			c >= 0x231A && c <= 0x231B || c >= 0x23E9 && c <= 0x23FA || c == '\uFE0F' || c == '\u200D' {
			return true
		}
	}
	return false
}

func xWeight(c rune) int {
	if c <= 0x10FF || c >= 0x2000 && c <= 0x200D || c >= 0x2010 && c <= 0x201F || c >= 0x2032 && c <= 0x2037 {
		return 1
	}
	return 2
}

// XWeighted is X's weighted length.
func XWeighted(text string) int {
	text = norm.NFC.String(text)
	total := measureURLRe.Count(text) * 23
	gs := uniseg.NewGraphemes(measureURLRe.ReplaceAll(text, ""))
	for gs.Next() {
		g := gs.Str()
		if isEmojiGrapheme(g) {
			total += 2
			continue
		}
		for _, c := range g {
			total += xWeight(c)
		}
	}
	return total
}

// Graphemes is the number of user-perceived characters in text.
func Graphemes(text string) int { return uniseg.GraphemeClusterCount(text) }

// CJKChars is the number of Han, kana and Hangul characters.
func CJKChars(text string) int {
	n := 0
	for _, c := range text {
		if IsCJK(c) || IsHangul(c) {
			n++
		}
	}
	return n
}

// Words are the words as the status bar counts them (Unicode word
// boundaries): Latin, Cyrillic and Hangul words, each Chinese or Japanese
// character on its own. An apostrophe or a full stop between letters or
// digits stays inside the word ("don't", "U.S.", "3.14"), a comma between
// digits too ("1,000"); a hyphen splits ("well-known" is two).
func Words(text string) int {
	chars := []rune(text)
	word := func(c rune) bool { return IsAlphanumeric(c) && !IsCJK(c) }
	n := 0
	inWord := false
	for i, c := range chars {
		switch {
		case IsCJK(c):
			n++
			inWord = false
		case word(c):
			if !inWord {
				n++
			}
			inWord = true
		case inWord:
			prev, next := chars[i-1], ' '
			if i+1 < len(chars) {
				next = chars[i+1]
			}
			switch c {
			case '\'', '’', '.':
				inWord = word(next)
			case ',', ';':
				inWord = isNumeric(prev) && isNumeric(next)
			default:
				inWord = false
			}
		}
	}
	return n
}

func mastodonCount(text string) int {
	urls := measureURLRe.Count(text)
	rest := mentionRe.ReplaceAll(measureURLRe.ReplaceAll(text, ""), "$1")
	return urls*23 + Graphemes(rest)
}

// MeasureText measures text by metric; false for an unknown metric.
func MeasureText(text, metric string) (Measure, bool) {
	g := Graphemes(text)
	limit := func(n int) *int { return &n }
	var m Measure
	switch metric {
	case "x_weighted":
		m = Measure{Count: XWeighted(text), Limit: limit(280), Unit: "weighted characters",
			Notes: []string{"URLs count 23 each; CJK characters and emoji count 2."}}
	case "bluesky":
		bytes := len(text)
		notes := []string{fmt.Sprintf("%d of 3,000 UTF-8 bytes.", bytes)}
		if bytes > 3000 {
			notes = append(notes, "Over the byte limit.")
		}
		m = Measure{Count: g, Limit: limit(300), Unit: "graphemes", Notes: notes}
	case "threads":
		m = Measure{Count: g, Limit: limit(500), Unit: "characters"}
	case "instagram":
		tags := hashtagRe.Count(text)
		notes := []string{fmt.Sprintf("%d hashtag(s); at most 5 per post since December 2025.", tags)}
		if tags > 5 {
			notes = append(notes, "Too many hashtags.")
		}
		m = Measure{Count: g, Limit: limit(2200), Unit: "characters", Notes: notes}
	case "mastodon":
		m = Measure{Count: mastodonCount(text), Limit: limit(500), Unit: "characters",
			Notes: []string{"500 is the default; servers can set their own limit. Links count 23; a mention counts only its username."}}
	case "linkedin":
		fold := []rune(text)
		fold = fold[:min(len(fold), 140)]
		m = Measure{Count: g, Limit: limit(3000), Unit: "characters",
			Notes: []string{fmt.Sprintf("Readers see about the first 140 (mobile) to 210 (desktop) characters before “see more”: “%s”.", string(fold))}}
	case "chars":
		m = Measure{Count: g, Unit: "characters"}
	case "cjk_chars":
		m = Measure{Count: CJKChars(text), Unit: "CJK characters"}
	case "words":
		m = Measure{Count: Words(text), Unit: "words", Notes: []string{"Chinese and Japanese characters count one each."}}
	case "utf8_bytes":
		m = Measure{Count: len(text), Unit: "bytes"}
	case "wechat_title":
		m = Measure{Count: g, Limit: limit(32), Unit: "characters"}
	case "wechat_digest":
		m = Measure{Count: g, Limit: limit(120), Unit: "characters",
			Notes: []string{"An empty digest shows the first 54 characters of the article."}}
	case "wechat_content":
		m = Measure{Count: g, Limit: limit(19_999), Unit: "characters", Notes: []string{"Under 20,000 characters and under 1 MB."}}
	case "xhs_title":
		m = Measure{Count: g, Limit: limit(20), Unit: "characters",
			Notes: []string{"Unverified limit (third-party figure): a warning, not a rule."}}
	case "xhs_body":
		m = Measure{Count: g, Limit: limit(1000), Unit: "characters",
			Notes: []string{"Unverified limit (third-party figure): a warning, not a rule."}}
	case "weibo":
		m = Measure{Count: g, Limit: limit(2000), Unit: "characters",
			Notes: []string{"Folded after about 140 characters (2016 reports): a warning, not a rule."}}
	case "govuk_title":
		m = Measure{Count: g, Limit: limit(65), Unit: "characters"}
	case "govuk_summary":
		var notes []string
		if !strings.HasSuffix(trimEnd(text), ".") {
			notes = append(notes, "A GOV.UK summary ends with a full stop.")
		}
		m = Measure{Count: g, Limit: limit(160), Unit: "characters", Notes: notes}
	case "zh_academic_title":
		// 字 as a Chinese title counts them: each CJK character, and each
		// Latin word or number as one.
		m = Measure{Count: Words(text), Limit: limit(25), Unit: "字", Notes: []string{"Latin words and numbers count one each."}}
	default:
		return Measure{}, false
	}
	m.Metric = metric
	if m.Notes == nil {
		m.Notes = []string{}
	}
	m.Within = (m.Limit == nil || m.Count <= *m.Limit) &&
		!(metric == "bluesky" && len(text) > 3000) &&
		!(metric == "instagram" && hashtagRe.Count(text) > 5)
	return m, true
}

// Describe is one line for the model: the count, the limit and the
// verdict.
func (m Measure) Describe() string {
	var line string
	switch {
	case m.Limit != nil && m.Within:
		line = fmt.Sprintf("%s: %d of %d %s - within the limit (%d to spare).", m.Metric, m.Count, *m.Limit, m.Unit, *m.Limit-min(m.Count, *m.Limit))
	case m.Limit != nil:
		line = fmt.Sprintf("%s: %d of %d %s - OVER the limit by %d.", m.Metric, m.Count, *m.Limit, m.Unit, max(m.Count-*m.Limit, 1))
	default:
		line = fmt.Sprintf("%s: %d %s.", m.Metric, m.Count, m.Unit)
	}
	for _, note := range m.Notes {
		line += " " + note
	}
	return line
}
