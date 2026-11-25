package rules

import (
	"maps"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/text/unicode/norm"
)

// Japanese notation: character width, spaces, marks after ! and ?, the
// wave dash, counters, leaders, brackets, the long vowel mark and digits
// with kanji.

// jaWidthKana is ja.width.kana: half-width katakana -> full-width (voiced
// marks joined).
func jaWidthKana(t *Text, _ *ctx, o *out) {
	n := t.Len()
	for i := 0; i < n; {
		if !isHalfwidthKana(t.Chars[i]) {
			i++
			continue
		}
		a := i
		for i < n && isHalfwidthKana(t.Chars[i]) {
			i++
		}
		o.fix("ja.width.kana", a, i, norm.NFKC.String(t.slice(a, i)), "半角カタカナは全角にします。")
	}
}

// jaHalfwidth is ja.punct.halfwidth: half-width , . ! ? in Japanese -> 、。！？
// (or ，． when the document writes those), and 、。 / ，． mixed -> the
// majority.
func jaHalfwidth(t *Text, c *ctx, o *out) {
	s := c.stats
	comma, period := '、', '。'
	if minority(s.jaTouten, s.jaComma) {
		comma = '，'
	}
	if minority(s.jaKuten, s.jaPeriod) {
		period = '．'
	}
	msg := "日本語の文では全角の句読点を使います。"
	for i := range t.Len() {
		ch := t.Chars[i]
		prev, ok := t.before(i)
		if !ok {
			continue
		}
		prevJa := IsCJK(prev) || strings.ContainsRune("」』）ー", prev)
		switch {
		case strings.ContainsRune(",.!?", ch) && prevJa:
			if ch == '.' && t.atEq(i+1, '.') {
				continue
			}
			j := i + 1
			for j < t.Len() && t.Chars[j] == ' ' {
				j++
			}
			after, hasAfter := t.At(j)
			if hasAfter && !(isJa(after) || strings.ContainsRune("「『（", after)) {
				continue
			}
			full := '？'
			switch ch {
			case ',':
				full = comma
			case '.':
				full = period
			case '!':
				full = '！'
			}
			end := i + 1
			if hasAfter {
				end = j
			}
			o.fix("ja.punct.halfwidth", i, end, string(full), msg)
		case ch == '、' && comma == '，':
			o.fix("ja.punct.halfwidth", i, i+1, "，", "読点の書き方が混在しています（本文の多くは「，」）。")
		case ch == '，' && comma == '、' && minority(s.jaComma, s.jaTouten):
			o.fix("ja.punct.halfwidth", i, i+1, "、", "読点の書き方が混在しています（本文の多くは「、」）。")
		case ch == '。' && period == '．':
			o.fix("ja.punct.halfwidth", i, i+1, "．", "句点の書き方が混在しています（本文の多くは「．」）。")
		case ch == '．' && period == '。' && minority(s.jaPeriod, s.jaKuten):
			o.fix("ja.punct.halfwidth", i, i+1, "。", "句点の書き方が混在しています（本文の多くは「。」）。")
		}
	}
}

// jaWidthAlnum is ja.width.alnum: full-width digits and letters ->
// half-width (公用文 keeps full-width initialisms such as ＱＯＬ).
func jaWidthAlnum(t *Text, c *ctx, o *out) {
	koyobun := c.profile.is("koyobun")
	fw := func(r rune) bool {
		return r >= '０' && r <= '９' || !koyobun && (r >= 'Ａ' && r <= 'Ｚ' || r >= 'ａ' && r <= 'ｚ')
	}
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
		o.fix("ja.width.alnum", a, i, string(half), "全角の英数字は半角にします。")
	}
}

// jaSpaceTyped is ja.space.typed: a typed space between Japanese and Latin
// text -> none, unless the document clearly spaces them throughout.
func jaSpaceTyped(t *Text, c *ctx, o *out) {
	s := c.stats
	// A chosen profile removes the spaces; Automatic only where the
	// document's majority is unspaced, as many technical texts space Latin
	// terms on purpose.
	if !c.profile.Explicit && !minority(s.cjkLatinSpaced, s.cjkLatinTight) {
		return
	}
	for i := 1; i+1 < t.Len(); i++ {
		if t.Chars[i] != ' ' {
			continue
		}
		p, q := t.Chars[i-1], t.Chars[i+1]
		if IsCJK(p) && isLatinOrDigit(q) || isLatinOrDigit(p) && IsCJK(q) {
			o.fix("ja.space.typed", i-1, i+2, string(p)+string(q), "和文と欧文の間にスペースは入れません。")
		}
	}
}

// jaSpaceFullwidth is ja.space.fullwidth: a space between Japanese
// characters or beside a bracket -> none; the full-width space after ？！
// stays.
func jaSpaceFullwidth(t *Text, _ *ctx, o *out) {
	n := t.Len()
	space := func(r rune) bool { return r == ' ' || r == '\u3000' }
	for i := 1; i < n; {
		if !space(t.Chars[i]) {
			i++
			continue
		}
		a := i
		for i < n && space(t.Chars[i]) {
			i++
		}
		if i >= n {
			break
		}
		p, q := t.Chars[a-1], t.Chars[i]
		afterExclaim := (p == '？' || p == '！') && i-a == 1 && t.Chars[a] == '\u3000'
		// Spaces that set off a menu path ("設定 → ショートカット の") are
		// the writer's way of quoting UI labels.
		window := string(t.Chars[max(a-12, 0):min(i+12, n)])
		if strings.Contains(window, " → ") || strings.Contains(window, " > ") || strings.Contains(window, " ＞ ") {
			continue
		}
		bracket := strings.ContainsRune("「『（【", p) || strings.ContainsRune("」』）】", q)
		if !afterExclaim && (isJa(p) && isJa(q) || bracket && (isJa(p) || isJa(q))) {
			o.fix("ja.space.fullwidth", a-1, i+1, string(p)+string(q), "全角文字の間にスペースは入れません。")
		}
	}
}

// jaAfterExclaim is ja.punct.after-exclaim: a sentence-final ？/！ followed
// by more text -> a full-width space after it. Not before a closing
// bracket, と, って or か, not in fiction or headings.
func jaAfterExclaim(t *Text, c *ctx, o *out) {
	if c.profile.is("fiction") || c.block.isHeading() {
		return
	}
	for i := 0; i+1 < t.Len(); i++ {
		if t.Chars[i] != '？' && t.Chars[i] != '！' {
			continue
		}
		if q := t.Chars[i+1]; IsCJK(q) && !strings.ContainsRune("とっかなの", q) {
			o.fix("ja.punct.after-exclaim", i, i+1, string(t.Chars[i])+"\u3000", "文中の「！」「？」のあとには全角スペースを入れます。")
		}
	}
}

// jaTilde is ja.punct.tilde: 10~20 -> 10～20.
func jaTilde(t *Text, _ *ctx, o *out) {
	for i := 1; i+1 < t.Len(); i++ {
		if t.Chars[i] == '~' && isASCIIDigit(t.Chars[i-1]) && isASCIIDigit(t.Chars[i+1]) {
			o.fix("ja.punct.tilde", i, i+1, "～", "範囲を示すには「～」を使います。")
		}
	}
}

var jaCounterRe = MustRe(`\d\s?([ヶヵケ])(月|所|国|年)`)

// jaCounter is ja.num.counter: 3ヶ月 -> 3か月 (霞ケ関 has no digit before
// it).
func jaCounter(t *Text, _ *ctx, o *out) {
	for _, g := range t.captures(jaCounterRe) {
		if g[1].OK {
			o.fix("ja.num.counter", g[1].A, g[1].B, "か", "「3か月」のように「か」と書きます（「カ」も使われます）。")
		}
	}
}

var jaDotsRe = MustRe(`\.{3,}|・{3,}|。{3,}|ーー+|−−+|--+`)

// jaDotsDash is ja.punct.dots-dash: ... ・・・ -> ……; ーー −− -- used as a
// dash -> ――. ーー after kana is a drawn-out sound (えーー), not a dash.
func jaDotsDash(t *Text, _ *ctx, o *out) {
	for _, m := range t.find(jaDotsRe) {
		a, b := m[0], m[1]
		run := t.slice(a, b)
		if !t.beforeIs(a, isJa) && !t.atIs(b, isJa) {
			continue
		}
		switch {
		case strings.HasPrefix(run, "ー"):
			if t.beforeIs(a, func(r rune) bool { return isKana(r) || r == 'ー' }) {
				continue
			}
			o.fix("ja.punct.dots-dash", a, b, "――", "ダッシュは「――」と二つ重ねます。")
		case strings.HasPrefix(run, "−") || strings.HasPrefix(run, "-"):
			o.fix("ja.punct.dots-dash", a, b, "――", "ダッシュは「――」と二つ重ねます。")
		default:
			o.fix("ja.punct.dots-dash", a, b, "……", "三点リーダーは「……」と二つ重ねます。")
		}
	}
}

// jaSingleLeader is ja.punct.single-leader: a single … or ― -> two (公用文
// allows one).
func jaSingleLeader(t *Text, c *ctx, o *out) {
	if c.profile.is("koyobun") {
		return
	}
	for i, ch := range t.Chars {
		// Only inside a sentence: a lone … after a menu command ("編集…")
		// is the UI's label.
		inside := t.beforeIs(i, IsCJK) && t.atIs(i+1, IsCJK)
		if (ch == '…' || ch == '―') && !t.beforeEq(i, ch) && !t.atEq(i+1, ch) && inside {
			o.fix("ja.punct.single-leader", i, i+1, string(ch)+string(ch), "「…」「―」は二つ重ねて使います。")
		}
	}
}

// jaPeriodBracket is ja.punct.period-bracket (a chosen JTF or technical
// profile): 。」 -> 」.
func jaPeriodBracket(t *Text, c *ctx, o *out) {
	if !(c.profile.Explicit && (c.profile.is("jtf") || c.profile.is("tech"))) {
		return
	}
	for _, m := range findStr(t, "。」") {
		o.fix("ja.punct.period-bracket", m[0], m[0]+2, "」", "かぎかっこの閉じの前に句点は付けません。")
	}
}

// jaChoon is ja.word.choon: コンピュータ and コンピューター in one document -
// the minority form flagged.
func jaChoon(t *Text, c *ctx, o *out) {
	k := c.stats.katakana
	katakana := func(r rune) bool { return isKatakanaLetter(r) || r == 'ー' }
	for _, word := range slices.Sorted(maps.Keys(k)) {
		n := k[word]
		short, ok := strings.CutSuffix(word, "ー")
		if !ok {
			continue
		}
		shortCount, ok := k[short]
		if !ok {
			continue
		}
		var minor, major string
		switch {
		case shortCount < n:
			minor, major = short, word
		case n < shortCount:
			minor, major = word, short
		default:
			continue
		}
		for _, m := range findStr(t, minor) {
			// Whole katakana words only.
			if !t.atIs(m[1], katakana) && !t.beforeIs(m[0], katakana) {
				o.fix("ja.word.choon", m[0], m[1], major, "長音の表記が混在しています（「"+major+"」と「"+minor+"」）。")
			}
		}
	}
}

var jaKanjiDigitRe = MustRe(`(\d{1,3})([千百])`)

// jaKanjiDigit is ja.num.kanji-digit: 5千 -> 5,000, 3百 -> 300 (万 and 億
// are fine).
func jaKanjiDigit(t *Text, _ *ctx, o *out) {
	for _, g := range t.captures(jaKanjiDigitRe) {
		if !g[0].OK || !g[1].OK || !g[2].OK {
			continue
		}
		a, b := g[0].A, g[0].B
		if t.beforeIs(a, func(r rune) bool { return isASCIIDigit(r) || r == ',' }) ||
			t.atIs(b, func(r rune) bool { return strings.ContainsRune("葉代歳", r) }) {
			continue
		}
		n, _ := strconv.ParseUint(t.slice(g[1].A, g[1].B), 10, 64)
		value := n * 100
		if t.Chars[g[2].A] == '千' {
			value = n * 1000
		}
		s := strconv.FormatUint(value, 10)
		if value >= 1000 {
			k := len(s) - 3
			s = s[:k] + "," + s[k:]
		}
		o.fix("ja.num.kanji-digit", a, b, s, "算用数字と「千」「百」は混ぜずに書きます（「万」「億」は可）。")
	}
}
