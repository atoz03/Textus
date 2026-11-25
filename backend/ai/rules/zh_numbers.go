package rules

import "strings"

// Numbers in Chinese text: decimals, approximate counts, dates, units and
// ranges.

var (
	zhDecimalMaruRe = MustRe(`\d。\d`)
	zhDecimalBareRe = MustRe(`\.\d+`)
)

// zhDecimal is zh.num.decimal: 0。46 and .46 -> 0.46.
func zhDecimal(t *Text, c *ctx, o *out) {
	msg := zhm(c, "小数点用“.”，整数部分的 0 不能省略。", "小數點用「.」，整數部分的 0 不能省略。")
	for _, m := range t.find(zhDecimalMaruRe) {
		o.fix("zh.num.decimal", m[0]+1, m[0]+2, ".", msg)
	}
	for _, m := range t.find(zhDecimalBareRe) {
		a, b := m[0], m[1]
		beforeOK := t.beforeNoneOr(a, func(r rune) bool { return IsCJK(r) || strings.ContainsRune(" （，：", r) })
		afterOK := t.atNoneOr(b, func(r rune) bool { return !(r == '.' || isASCIIAlnum(r)) })
		if beforeOK && afterOK && t.beforeIs(a, IsCJK) {
			o.fix("zh.num.decimal", a, b, "0"+t.slice(a, b), msg)
		}
	}
}

// zhMeasure are measure words: what a count is of.
const zhMeasure = "个天日月年人次岁小时分钟周名位本件元块斤里米倍成层家项条辆台张页章节句只头匹"

var (
	zhApproxRe = MustRe(`([1-9])、([1-9])`)
	zhJiRe     = MustRe(`几(\d+)`)
)

// zhApprox is zh.num.approx (mainland): 3、4个 -> 三四个, 几1000 -> 几千.
// Only adjacent numbers before a measure word, never after 第 or in a
// list.
func zhApprox(t *Text, _ *ctx, o *out) {
	digits := []string{"零", "一", "二", "三", "四", "五", "六", "七", "八", "九"}
	for _, g := range t.captures(zhApproxRe) {
		if !g[0].OK || !g[1].OK || !g[2].OK {
			continue
		}
		a, b := g[0].A, g[0].B
		p, q := int(t.Chars[g[1].A]-'0'), int(t.Chars[g[2].A]-'0')
		measure := t.atIs(b, func(r rune) bool { return strings.ContainsRune(zhMeasure, r) })
		listed := t.beforeIs(a, func(r rune) bool { return r == '第' || r == '、' || isASCIIDigit(r) }) || t.atEq(b, '、')
		if q == p+1 && measure && !listed {
			o.fix("zh.num.approx", a, b, digits[p]+digits[q], "相邻数字表示概数时用汉字，不加顿号：“三四个”。")
		}
	}
	for _, g := range t.captures(zhJiRe) {
		if !g[0].OK || !g[1].OK {
			continue
		}
		a, b := g[0].A, g[0].B
		unit := map[string]string{"10": "十", "100": "百", "1000": "千", "10000": "万"}[t.slice(g[1].A, g[1].B)]
		msg := "“几”后用汉字数字：“几千”。"
		if unit != "" {
			o.fix("zh.num.approx", a, b, "几"+unit, msg)
		} else {
			o.flag("zh.num.approx", a, b, msg)
		}
	}
}

var zhMixedDateRe = MustRe(`[〇○零一二三四五六七八九]{4}年\d{1,2}月|\d{4}年[一二三四五六七八九十]{1,3}月`)

// zhMixedDate is zh.num.mixed-date: 二〇二六年10月 - one date in two
// numeral systems.
func zhMixedDate(t *Text, c *ctx, o *out) {
	for _, m := range t.find(zhMixedDateRe) {
		o.flag("zh.num.mixed-date", m[0], m[1],
			zhm(c, "同一个日期不要混用汉字数字和阿拉伯数字。", "同一個日期不要混用中文數字和阿拉伯數字。"))
	}
}

var zhShortYearRe = MustRe(`\d{2,4}年\d{1,2}月`)

// zhShortYear is zh.num.short-year: 98年5月 - a year shortened to two
// digits (90年代 is usage, and 活了90年 is a duration: only year-month
// dates are taken).
func zhShortYear(t *Text, c *ctx, o *out) {
	if c.lang == ZhHant {
		return // 民國 years are two- and three-digit.
	}
	for _, m := range t.find(zhShortYearRe) {
		a := m[0]
		digits := 0
		for _, r := range t.Chars[a:m[1]] {
			if !isASCIIDigit(r) {
				break
			}
			digits++
		}
		if digits == 2 && !t.beforeIs(a, isASCIIDigit) {
			o.flag("zh.num.short-year", a, a+3, "年份应写全，如“1998年”。")
		}
	}
}

var zhMixedUnitRe = MustRe(`(\d+)([百千])([零〇]?\d+)?|[一二三四五六七八九十百千]+[零〇]?\d+`)

// zhMixedUnit is zh.num.mixed-unit (mainland): 4千, 1百零8, 一百08 - Arabic
// digits mix only with 万 and 亿. 千米, 千克, 千瓦, 百分 are units, not
// numbers.
func zhMixedUnit(t *Text, c *ctx, o *out) {
	if c.profile.is("gongwen") && c.lang == ZhHant {
		return
	}
	msg := zhm(c, "阿拉伯数字只与“万”“亿”连用：写“4000”，不写“4千”。", "阿拉伯數字只與「萬」「億」連用：寫「4000」，不寫「4千」。")
	for _, g := range t.captures(zhMixedUnitRe) {
		if !g[0].OK {
			continue
		}
		a, b := g[0].A, g[0].B
		if t.atIs(b, func(r rune) bool { return strings.ContainsRune("米克瓦卡兆赫分字", r) }) ||
			t.beforeIs(a, func(r rune) bool { return isASCIIDigit(r) || isHan(r) && strings.ContainsRune("第周", r) }) {
			continue
		}
		if g[1].OK && g[2].OK && !g[3].OK {
			digits := t.slice(g[1].A, g[1].B)
			zeros := "00"
			if t.Chars[g[2].A] == '千' {
				zeros = "000"
			}
			// A byte count, as the Rust had it: three ASCII digits, or one
			// full-width one.
			if len(digits) <= 3 {
				o.fix("zh.num.mixed-unit", a, b, digits+zeros, msg)
			}
		} else {
			o.flag("zh.num.mixed-unit", a, b, msg)
		}
	}
}

var zhHyphenDateRe = MustRe(asciiB + `(\d{4})-(\d)` + asciiB)

// zhDateDunhao is zh.date.dunhao: an incomplete hyphen date (2008-8).
func zhDateDunhao(t *Text, c *ctx, o *out) {
	for _, g := range t.captures(zhHyphenDateRe) {
		if !g[0].OK || !g[1].OK || !g[2].OK {
			continue
		}
		a, b := g[0].A, g[0].B
		if t.atEq(b, '-') || t.atIs(b, isASCIIDigit) {
			continue
		}
		o.fix("zh.date.dunhao", a, b, t.slice(g[1].A, g[1].B)+"-0"+string(t.Chars[g[2].A]),
			zhm(c, "用连接号的日期，月、日写两位，如“2008-08”。", "用連接號的日期，月、日寫兩位，如「2008-08」。"))
	}
}

var zhUnitSpaceRe = MustRe(`\d[ \u00A0]+[%°℃‰′″]`)

// zhUnitSpace is zh.num.unit-space: no space between a number and % ° ′ ″.
func zhUnitSpace(t *Text, c *ctx, o *out) {
	for _, m := range t.find(zhUnitSpaceRe) {
		a, b := m[0], m[1]
		o.fix("zh.num.unit-space", a, b, string(t.Chars[a])+string(t.Chars[b-1]),
			zhm(c, "数字与 %、° 等符号之间不加空格。", "數字與 %、° 等符號之間不加空格。"))
	}
}

var zhASCIIRangeRe = MustRe(asciiB + `(\d{1,4})-(\d{1,4})` + asciiB)

// zhASCIIRange is zh.num.ascii-range: 3-5天 -> 3～5天. Not full dates,
// codes, phone or model numbers: only a short range before a Chinese
// measure word.
func zhASCIIRange(t *Text, c *ctx, o *out) {
	for _, g := range t.captures(zhASCIIRangeRe) {
		if !g[0].OK || !g[1].OK || !g[2].OK {
			continue
		}
		a, b := g[0].A, g[0].B
		x1, x2, y1, y2 := g[1].A, g[1].B, g[2].A, g[2].B
		beforeOK := t.beforeNoneOr(a, func(r rune) bool { return !(isASCIIAlnum(r) || strings.ContainsRune("-./", r)) })
		afterOK := t.atIs(b, func(r rune) bool {
			return isHan(r) && (strings.ContainsRune(zhMeasure, r) || strings.ContainsRune("℃度分秒期岁届", r))
		})
		dateLike := x2-x1 == 4 && y2-y1 == 2
		if beforeOK && afterOK && !dateLike && !t.atEq(b, '-') {
			o.fix("zh.num.ascii-range", a, b, t.slice(x1, x2)+"～"+t.slice(y1, y2),
				zhm(c, "表示数值范围用“～”或“—”，不用“-”。", "表示數值範圍用「～」，不用「-」。"))
		}
	}
}
