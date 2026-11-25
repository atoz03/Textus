package rules

import (
	"fmt"
	"slices"
	"strings"

	"golang.org/x/text/unicode/norm"
)

// Chinese wording: typos, variant and compatibility characters, mixed
// scripts, long clauses, official documents and advertising law.

// zhTypo is zh.word.typo: the wrong-character list; not inside a quotation
// (which may be quoting the error).
func zhTypo(t *Text, c *ctx, o *out) {
	for _, p := range zhTypos {
		wrong, right := p[0], p[1]
		for _, m := range findStr(t, wrong) {
			if t.inQuotes(m[0], m[1]) {
				continue
			}
			msg := "错别字：应为“" + right + "”。"
			if c.lang == ZhHant {
				msg = "錯別字：應為「" + right + "」。"
			}
			o.fix("zh.word.typo", m[0], m[1], right, msg)
		}
	}
}

// zhVariant is zh.word.variant: GF 1001 variant words -> the recommended
// form.
func zhVariant(t *Text, _ *ctx, o *out) {
	for _, p := range zhVariants {
		for _, m := range findStr(t, p[0]) {
			if !t.inQuotes(m[0], m[1]) {
				o.fix("zh.word.variant", m[0], m[1], p[1], "异形词：推荐写作“"+p[1]+"”。")
			}
		}
	}
}

// zhScriptMixed is zh.script.mixed: a Traditional character in Simplified
// text - only a stray one (the text is clearly Simplified), never inside a
// quotation.
func zhScriptMixed(t *Text, _ *ctx, o *out) {
	hans, hant := 0, 0
	for _, p := range scriptPairs {
		for _, ch := range t.Chars {
			if ch == p.s && p.kind != pairTradOnly {
				hans++
			}
			if ch == p.t && p.kind != pairSimpOnly {
				hant++
			}
		}
	}
	if hans < 2*max(hant, 1) {
		return
	}
	for i, ch := range t.Chars {
		if s, ok := toSimplified(ch); ok && !t.inQuotes(i, i+1) {
			o.fix("zh.script.mixed", i, i+1, string(s), fmt.Sprintf("简体文本中混入了繁体字“%c”，应为“%c”。", ch, s))
		}
	}
}

// zhCompatIdeograph is zh.compat-ideograph: CJK compatibility ideographs
// (pasted from PDFs) -> the unified ones, except the twelve that are
// unified themselves.
func zhCompatIdeograph(t *Text, c *ctx, o *out) {
	unifiedOnes := []rune{0xFA0E, 0xFA0F, 0xFA11, 0xFA13, 0xFA14, 0xFA1F, 0xFA21, 0xFA23, 0xFA24, 0xFA27, 0xFA28, 0xFA29}
	for i, ch := range t.Chars {
		if isCompatIdeograph(ch) && !slices.Contains(unifiedOnes, ch) {
			if unified := norm.NFC.String(string(ch)); unified != string(ch) {
				o.fix("zh.compat-ideograph", i, i+1, unified,
					zhm(c, "兼容汉字（常见于从 PDF 复制的文字）：应改为统一汉字。", "相容漢字（常見於從 PDF 複製的文字）：應改為統一漢字。"))
			}
		}
	}
}

// zhClauseLength is zh.style.clause-length: a clause of more than 40
// characters with no punctuation (not in fiction, not in quotations).
func zhClauseLength(t *Text, c *ctx, o *out) {
	if c.profile.is("fiction") || c.block.isHeading() {
		return
	}
	start := 0
	for i := 0; i <= t.Len(); i++ {
		if i < t.Len() && !strings.ContainsRune("，。；：！？、,.;:!?…—\n“”「」（）", t.Chars[i]) {
			continue
		}
		n := 0
		for _, ch := range t.Chars[start:i] {
			if IsCJK(ch) {
				n++
			}
		}
		if n > 40 && !t.inQuotes(start, i) {
			o.flag("zh.style.clause-length", start, i, zhm(c,
				fmt.Sprintf("这一分句很长（%d 字没有标点），可以考虑拆开。", n),
				fmt.Sprintf("這一分句很長（%d 字沒有標點），可以考慮拆開。", n)))
		}
		start = i + 1
	}
}

var (
	zhDocnumRe      = MustRe(`([〔\[\(（【［])(\d{4})([〕\]\)）】］])(第?)(0*)(\d+)号`)
	zhGongwenDateRe = MustRe(`\d{4}年(0?\d{1,2})月(0?\d{1,2})日`)
)

// zhGongwen is the 公文 profile (GB/T 9704, 条例): document numbers,
// dates, titles.
func zhGongwen(t *Text, c *ctx, o *out) {
	if !c.profile.is("gongwen") {
		return
	}
	for _, g := range t.captures(zhDocnumRe) {
		if slices.ContainsFunc(g[:7], func(s Span) bool { return !s.OK }) {
			continue
		}
		a, b := g[0].A, g[0].B
		open, close, di, zero := g[1].A, g[3].A, g[4], g[5]
		if t.Chars[open] != '〔' || t.Chars[close] != '〕' || di.B > di.A || zero.B > zero.A {
			o.fix("zh.gongwen.docnum", a, b, "〔"+t.slice(g[2].A, g[2].B)+"〕"+t.slice(g[6].A, g[6].B)+"号",
				"发文字号写作“代字〔年份〕序号号”：六角括号，序号不加“第”、不编虚位。")
		}
	}
	for _, g := range t.captures(zhGongwenDateRe) {
		for _, s := range g[1:3] {
			if s.OK && s.B-s.A == 2 && t.Chars[s.A] == '0' {
				o.fix("zh.gongwen.date", s.A, s.B, string(t.Chars[s.A+1]), "成文日期的月、日不编虚位：“2026年10月9日”。")
			}
		}
	}
	if c.block.isHeading() {
		for _, word := range []string{"请示报告", "报告请示"} {
			for _, m := range findStr(t, word) {
				o.flag("zh.gongwen.title", m[0], m[1], "公文标题不要并用“请示”和“报告”：请示用于请求批准，报告不得夹带请示事项。")
			}
		}
		for i, ch := range t.Chars {
			if strings.ContainsRune("，。；：！？“”（）", ch) {
				o.flag("zh.gongwen.title", i, i+1, "公文标题中除书名号外，一般不用标点符号。")
			}
		}
	}
}

var zhAdAbsolute = []string{"国家级", "最高级", "最佳", "最好", "顶级", "首选", "史上最", "全网最", "最低价", "第一品牌"}

var (
	zhAdFirstRe  = MustRe(`(?:销量|全国|全球|行业|世界|全网|排名)第一`)
	zhAdHealthRe = MustRe(`治疗|治愈|疗效|根治`)
	zhAdDataRe   = MustRe(`数据显示|调查显示|研究表明|研究显示|\d+(?:\.\d+)?%的(?:用户|消费者|人|网友)`)
)

// zhAd is the newmedia profile: 广告法 absolute terms (warnings, with the
// exemptions in the message), treatment claims and unsourced data.
func zhAd(t *Text, c *ctx, o *out) {
	if !c.profile.is("newmedia") {
		return
	}
	msg := "广告法禁用绝对化用语；商标、官方奖项或有时间地点限定的真实数据可除外。"
	for _, word := range zhAdAbsolute {
		for _, m := range findStr(t, word) {
			o.flag("zh.ad.absolute", m[0], m[1], msg)
		}
	}
	for _, m := range t.find(zhAdFirstRe) {
		o.flag("zh.ad.absolute", m[0], m[1], msg)
	}
	for _, m := range t.find(zhAdHealthRe) {
		o.flag("zh.ad.health", m[0], m[1], "非医疗广告不得涉及疾病治疗功能。")
	}
	sourced := slices.ContainsFunc([]string{"来源", "出处", "《", "引自", "据悉", "根据", "据统计"}, func(w string) bool {
		return strings.Contains(t.S, w)
	})
	if !sourced {
		for _, m := range t.find(zhAdDataRe) {
			o.flag("zh.ad.unsourced", m[0], m[1], "引用数据、调查结果请注明出处。")
		}
	}
}
