package rules

import (
	"slices"
	"strings"
	"unicode"
)

// Chinese punctuation marks: ellipses, dashes, stacked marks, quotes, the
// end of a heading, the enumeration comma and the middle dot.

var zhDotsRe = MustRe(`\.{3,}|。{3,}|…+|⋯+|·{3,}`)

// zhEllipsis is zh.punct.ellipsis: ... 。。。 a lone … ⋯⋯ and runs longer
// than two pairs -> …… (two pairs, the twelve dots for omitted
// paragraphs, stay).
func zhEllipsis(t *Text, c *ctx, o *out) {
	msg := zhm(c, "中文省略号用“……”（两个“…”）。", "中文刪節號用「……」（兩個「…」）。")
	for _, m := range t.find(zhDotsRe) {
		a, b := m[0], m[1]
		run := t.slice(a, b)
		if run == "……" || run == "…………" {
			continue
		}
		// A lone … after a menu command ("导入技能…") is the UI's label: only
		// one with CJK letters on both sides is taken for an ellipsis.
		if run == "…" && !(t.beforeIs(a, IsCJK) && t.atIs(b, IsCJK)) {
			continue
		}
		nearCJK := t.beforeIs(a, cjkBefore) || t.atIs(b, opensCJK) || t.atIs(b, isFullwidthPunct)
		chineseRun := strings.HasPrefix(run, "。") || strings.HasPrefix(run, "⋯") || strings.HasPrefix(run, "…")
		if !nearCJK && !(chineseRun && (t.beforeIs(a, isCJKText) || t.atIs(b, isCJKText))) {
			continue
		}
		// "……等" is zh.punct.ellipsis-deng's.
		if t.atEq(b, '等') || slices.ContainsFunc(findStr(t, "什么的"), func(m [2]int) bool { return m[0] == b }) {
			continue
		}
		o.fix("zh.punct.ellipsis", a, b, "……", msg)
	}
}

// zhEllipsisDeng is zh.punct.ellipsis-deng: an ellipsis and 等/等等/什么的
// say the same thing.
func zhEllipsisDeng(t *Text, c *ctx, o *out) {
	msg := zhm(c, "省略号和“等”“等等”“什么的”不同时使用。", "刪節號和「等」「等等」不同時使用。")
	for _, m := range t.find(zhDotsRe) {
		a, b := m[0], m[1]
		if !t.beforeIs(a, cjkBefore) {
			continue
		}
		for _, word := range []string{"等等", "等", "什么的", "什麼的"} {
			w := []rune(word)
			if b+len(w) <= t.Len() && slices.Equal(t.Chars[b:b+len(w)], w) {
				o.fix("zh.punct.ellipsis-deng", a, b+len(w), word, msg)
				break
			}
		}
	}
}

var zhDashRe = MustRe(`-{2,}|－{2,}|─{2}|―{2}|—`)

// cjkRunLeft is the length of the run of CJK letters ending at i
// (exclusive); cjkRunRight of the one starting at i.
func cjkRunLeft(t *Text, i int) int {
	n := 0
	for j := i - 1; j >= 0 && IsCJK(t.Chars[j]); j-- {
		n++
	}
	return n
}

func cjkRunRight(t *Text, i int) int {
	n := 0
	for j := i; j < t.Len() && IsCJK(t.Chars[j]); j++ {
		n++
	}
	return n
}

// zhDash is zh.punct.dash (mainland): -- －－ ── ―― and a single — between
// Chinese clauses -> ——. A single — between numbers, dates or places is
// the range connector (北京—上海), so only one between long clauses is
// taken.
func zhDash(t *Text, c *ctx, o *out) {
	msg := zhm(c, "中文破折号用“——”（两个“—”）。", "中文破折號用「——」。")
	for _, m := range t.find(zhDashRe) {
		a, b := m[0], m[1]
		run := t.slice(a, b)
		cjkSide := t.beforeIs(a, cjkBefore) || t.atIs(b, opensCJK)
		if !cjkSide {
			continue
		}
		if run == "—" {
			if t.atEq(b, '—') || t.beforeEq(a, '—') {
				continue
			}
			if t.beforeIs(a, IsCJK) && t.atIs(b, IsCJK) && cjkRunLeft(t, a) >= 5 && cjkRunRight(t, b) >= 5 {
				o.fix("zh.punct.dash", a, b, "——", msg)
			}
			continue
		}
		if strings.HasPrefix(run, "-") && t.beforeIs(a, isASCIIDigit) && t.atIs(b, isASCIIDigit) {
			continue
		}
		o.fix("zh.punct.dash", a, b, "——", msg)
	}
}

// zhStacked is zh.punct.stacked: more than `stacked-marks` ？ or ！ in a
// row (three in fiction, one elsewhere); one of each, ？！, is always fine.
func zhStacked(t *Text, c *ctx, o *out) {
	limit := 3
	switch opt := c.profile.opt("stacked-marks"); {
	case opt == "3":
		limit = 3
	case opt == "1":
		limit = 1
	case c.profile.is("fiction"):
		limit = 3
	// Automatic: the GB limit; the tech and official profiles allow one.
	case c.profile.Explicit:
		limit = 1
	}
	msg := zhm(c, "问号、叹号不宜连用过多。", "問號、驚嘆號不宜連用過多。")
	n := t.Len()
	mark := func(r rune) bool { return r == '！' || r == '？' }
	for i := 0; i < n; {
		if !mark(t.Chars[i]) {
			i++
			continue
		}
		a := i
		for i < n && mark(t.Chars[i]) {
			i++
		}
		run := t.Chars[a:i]
		mixedPair := len(run) == 2 && run[0] != run[1]
		if len(run) <= limit || mixedPair {
			continue
		}
		var fixed string
		switch {
		case limit != 1:
			fixed = string(run[:limit])
		case slices.Contains(run, '？') && slices.Contains(run, '！'):
			fixed = "？！"
		default:
			fixed = string(run[0])
		}
		o.fix("zh.punct.stacked", a, i, fixed, msg)
	}
}

// zhHeadingEnd is zh.heading.end-punct: a heading ending in 。，、；：.
func zhHeadingEnd(t *Text, c *ctx, o *out) {
	if !c.block.isHeading() {
		return
	}
	trimmed := runeCount(strings.TrimRightFunc(t.S, unicode.IsSpace))
	if trimmed > 0 && strings.ContainsRune("。，、；：", t.Chars[trimmed-1]) {
		o.fix("zh.heading.end-punct", trimmed-1, trimmed, "",
			zhm(c, "标题末尾不用句号、逗号等点号。", "標題末尾不用句號、逗號等標點。"))
	}
}

// zhQuoteStraight is zh.quote.straight (mainland): straight quotes around
// Chinese -> “” ‘’ (or 「」『』 with quotes=corner). Code, English
// quotations and unmatched quotes are left alone.
func zhQuoteStraight(t *Text, c *ctx, o *out) {
	corner := c.profile.opt("quotes") == "corner"
	msg := zhm(c, "中文引号用“”，内层用‘’。", "中文引號用“”，內層用‘’。")
	dOpen, dClose, sOpen, sClose := "“", "”", "‘", "’"
	if corner {
		msg = zhm(c, "中文引号用「」，内层用『』。", "中文引號用「」，內層用『』。")
		dOpen, dClose, sOpen, sClose = "「", "」", "『", "』"
	}
	for _, q := range []struct {
		mark        rune
		open, close string
	}{{'"', dOpen, dClose}, {'\'', sOpen, sClose}} {
		var marks []int
		for i := range t.Len() {
			if t.Chars[i] != q.mark || t.Protected[i] {
				continue
			}
			// An apostrophe between letters isn't a quote.
			if q.mark == '\'' && t.beforeIs(i, IsAlphanumeric) && t.atIs(i+1, IsAlphanumeric) && !t.beforeIs(i, IsCJK) {
				continue
			}
			marks = append(marks, i)
		}
		if len(marks)%2 != 0 {
			continue
		}
		for k := 0; k < len(marks); k += 2 {
			a, b := marks[k], marks[k+1]
			cjk, latin := 0, 0
			for _, r := range t.Chars[a+1 : b] {
				if IsCJK(r) {
					cjk++
				}
				if isLatinLetter(r) {
					latin++
				}
			}
			outside := t.beforeIs(a, isCJKText) || t.atIs(b+1, isCJKText)
			if cjk > 0 && cjk*3 >= latin && (outside || cjk >= latin) {
				o.fix("zh.quote.straight", a, a+1, q.open, msg)
				o.fix("zh.quote.straight", b, b+1, q.close, msg)
			}
		}
	}
}

// zhTitleDunhao is zh.title.dunhao (mainland): 《A》、《B》 -> 《A》《B》.
func zhTitleDunhao(t *Text, _ *ctx, o *out) {
	for _, m := range findStr(t, "》、《") {
		o.fix("zh.title.dunhao", m[0], m[0]+3, "》《", "并列的书名号之间一般不用顿号。")
	}
}

// zhMiddot is zh.punct.middot (mainland): • ・ ‧ between the parts of a
// name -> ·.
func zhMiddot(t *Text, _ *ctx, o *out) {
	for i := 1; i+1 < t.Len(); i++ {
		if strings.ContainsRune("•・‧", t.Chars[i]) && isHan(t.Chars[i-1]) && isHan(t.Chars[i+1]) {
			o.fix("zh.punct.middot", i, i+1, "·", "外国人名等各部分之间用间隔号“·”。")
		}
	}
}
