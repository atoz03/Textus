package rules

import (
	"slices"
	"strings"
	"unicode"
)

// Width and spacing in Chinese text: half-width punctuation among Chinese
// characters, spaces next to full-width marks, between Chinese and Latin,
// full-width letters and digits.

// cjkBefore is CJK text or a closing CJK mark: what a Chinese sentence's
// punctuation follows.
func cjkBefore(c rune) bool { return IsCJK(c) || strings.ContainsRune("”’」』）》】〕", c) }

// nextNonSpace is the next non-space character from i, and where it is.
func nextNonSpace(t *Text, i int) (rune, bool, int) {
	j := i
	for j < t.Len() && t.Chars[j] == ' ' {
		j++
	}
	c, ok := t.At(j)
	return c, ok, j
}

func opensCJK(c rune) bool { return IsCJK(c) || strings.ContainsRune("“‘「『（《【〔", c) }

// zhHalfwidth is zh.punct.halfwidth: , . ; : ? ! ( ) typed with an English
// input method inside a Chinese sentence -> ，。；：？！（）. Not decimals
// (3.14), times (8:00), list markers ("1."), file names, or English
// segments.
func zhHalfwidth(t *Text, c *ctx, o *out) {
	msg := zhm(c, "中文句子里应使用全角标点。", "中文句子裡應使用全形標點。")
	n := t.Len()
	for i := range n {
		ch := t.Chars[i]
		var full rune
		switch ch {
		case ',':
			full = '，'
		case '.':
			full = '。'
		case ';':
			full = '；'
		case ':':
			full = '：'
		case '?':
			full = '？'
		case '!':
			full = '！'
		default:
			continue
		}
		prev, ok := t.before(i)
		if !ok || !cjkBefore(prev) {
			continue
		}
		if ch == '.' && t.atEq(i+1, '.') {
			continue // an ellipsis: zh.punct.ellipsis
		}
		after, hasAfter, j := nextNonSpace(t, i+1)
		ends := !hasAfter
		good := !hasAfter || opensCJK(after) ||
			(ch == '!' || ch == '?') && strings.ContainsRune("!?！？”」", after)
		if !(good || ends) {
			continue
		}
		// Keep spaces after it only when they lead nowhere (end of text).
		end := max(j, i+1)
		if ends {
			end = i + 1
		}
		if end > i+1 && slices.ContainsFunc(t.Chars[i+1:end], func(r rune) bool { return r != ' ' }) {
			end = i + 1
		}
		o.fix("zh.punct.halfwidth", i, end, string(full), msg)
	}
	// Parentheses around Chinese text.
	var stack []int
	for i := range n {
		switch t.Chars[i] {
		case '(':
			stack = append(stack, i)
		case ')':
			if len(stack) > 0 {
				a := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				innerCJK := slices.ContainsFunc(t.Chars[a+1:i], IsCJK)
				outerCJK := t.beforeIs(a, IsCJK) || t.atIs(i+1, IsCJK)
				if innerCJK && outerCJK && !t.guarded(a, i+1) {
					o.fix("zh.punct.halfwidth", a, a+1, "（", msg)
					o.fix("zh.punct.halfwidth", i, i+1, "）", msg)
				}
			}
		}
	}
}

// zhFullwidthAdjacent is zh.space.fullwidth-adjacent: full-width marks
// take no space beside them; the curly quotes, dashes and ellipsis only
// when Chinese text is on the other side.
func zhFullwidthAdjacent(t *Text, c *ctx, o *out) {
	msg := zhm(c, "全角标点前后不加空格。", "全形標點前後不加空格。")
	n := t.Len()
	for i := 0; i < n; {
		if t.Chars[i] != ' ' && t.Chars[i] != '\u3000' {
			i++
			continue
		}
		a := i
		for i < n && (t.Chars[i] == ' ' || t.Chars[i] == '\u3000') {
			i++
		}
		b := i
		if a == 0 || b >= n {
			continue
		}
		l, r := t.Chars[a-1], t.Chars[b]
		leftMark := isFullwidthPunct(l) ||
			strings.ContainsRune("”’…—", l) && isCJKText(r) ||
			strings.ContainsRune("“‘", l) && isCJKText(r)
		rightMark := isFullwidthPunct(r) ||
			strings.ContainsRune("“‘…—", r) && isCJKText(l) ||
			strings.ContainsRune("”’", r) && isCJKText(l)
		// An ideographic space after ？！ is a Japanese convention, and a
		// leading one is an indent: neither is this rule's.
		if leftMark || rightMark {
			o.fix("zh.space.fullwidth-adjacent", a-1, b+1, string(l)+string(r), msg)
		}
	}
}

// zhCJKLatin is zh.space.cjk-latin: spaces between Chinese and Latin
// letters or digits by `cjk-latin-space` (`on` adds, `off`/`layout`
// remove); Automatic flags only a clear minority. Never next to
// punctuation, %, or °.
func zhCJKLatin(t *Text, c *ctx, o *out) {
	mode := c.profile.opt("cjk-latin-space")
	if mode == "" {
		mode = "auto"
		if c.profile.is("tech") {
			mode = "on"
		}
	}
	s := c.stats
	var add, remove bool
	switch mode {
	case "on":
		add = true
	case "off", "layout":
		remove = true
	default:
		add = minority(s.cjkLatinTight, s.cjkLatinSpaced)
		remove = minority(s.cjkLatinSpaced, s.cjkLatinTight)
	}
	auto := mode == "auto"
	n := t.Len()
	for i := 0; i+1 < n; i++ {
		ch, d := t.Chars[i], t.Chars[i+1]
		if add && (IsCJK(ch) && isLatinOrDigit(d) || isLatinOrDigit(ch) && IsCJK(d)) {
			msg := zhm(c, "按所选规范，中文与英文、数字之间加空格。", "按所選規範，中文與英文、數字之間加空格。")
			if auto {
				msg = zhm(c, "中文与英文、数字之间加空格（与本文多数写法一致）。", "中文與英文、數字之間加空格（與本文多數寫法一致）。")
			}
			o.fix("zh.space.cjk-latin", i, i+2, string(ch)+" "+string(d), msg)
		}
		if remove && ch == ' ' && i > 0 {
			p := t.Chars[i-1]
			if IsCJK(p) && isLatinOrDigit(d) || isLatinOrDigit(p) && IsCJK(d) {
				msg := zhm(c, "按所选规范，中文与英文、数字之间不加空格。", "按所選規範，中文與英文、數字之間不加空格。")
				if auto {
					msg = zhm(c, "中文与英文、数字之间不加空格（与本文多数写法一致）。", "中文與英文、數字之間不加空格（與本文多數寫法一致）。")
				}
				o.fix("zh.space.cjk-latin", i-1, i+2, string(p)+string(d), msg)
			}
		}
	}
}

// zhWidthAlnum is zh.width.alnum: full-width letters and digits (Ａ１) ->
// half-width. Taiwanese 公文 numbers its items with full-width digits
// (１、).
func zhWidthAlnum(t *Text, c *ctx, o *out) {
	twGongwen := c.lang == ZhHant && c.profile.is("gongwen")
	fw := func(r rune) bool {
		return (r >= '０' && r <= '９' || r >= 'Ａ' && r <= 'Ｚ' || r >= 'ａ' && r <= 'ｚ') &&
			!(twGongwen && unicode.IsNumber(r))
	}
	msg := zhm(c, "全角字母和数字应改为半角。", "全形字母和數字應改為半形。")
	n := t.Len()
	for i := 0; i < n; {
		if !fw(t.Chars[i]) {
			i++
			continue
		}
		a := i
		for i < n && fw(t.Chars[i]) {
			i++
		}
		half := make([]rune, 0, i-a)
		for _, r := range t.Chars[a:i] {
			half = append(half, r-0xFEE0)
		}
		o.fix("zh.width.alnum", a, i, string(half), msg)
	}
}
