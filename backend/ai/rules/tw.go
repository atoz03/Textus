package rules

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// Traditional Chinese rules: script purity for any Traditional text, and
// the Taiwan and Hong Kong conventions by region. The region-neutral
// Chinese rules run from zh.go.

func twRun(t *Text, c *ctx, o *out) {
	twScriptSimplified(t, c, o)
	region := c.profile.Region
	if region == "" {
		region = "TW"
	}
	if region == "TW" || region == "HK" {
		twQuoteCorner(t, c, o)
		twHKMix(t, c, o, region)
		twDash(t, c, o)
	}
	if region == "TW" {
		twMiddot(t, c, o)
		twMainlandWords(t, c, o)
		if c.profile.is("gongwen") {
			twGongwen(t, c, o)
		}
	}
}

// twScriptSimplified is tw.script.simplified: a stray Simplified character
// in clearly Traditional text, never inside a quotation. One with several
// Traditional forms (发: 發/髮) gets no single suggestion.
func twScriptSimplified(t *Text, _ *ctx, o *out) {
	hans, hant := hanScripts(t)
	if hant < 2*max(hans, 1) {
		return
	}
	for i, ch := range t.Chars {
		forms := toTraditional(ch)
		if len(forms) == 0 || t.inQuotes(i, i+1) {
			continue
		}
		if len(forms) == 1 {
			o.fix("tw.script.simplified", i, i+1, string(forms[0]),
				fmt.Sprintf("繁體文本中混入了簡體字「%c」，應為「%c」。", ch, forms[0]))
		} else {
			options := make([]string, len(forms))
			for k, f := range forms {
				options[k] = "「" + string(f) + "」"
			}
			o.flag("tw.script.simplified", i, i+1,
				fmt.Sprintf("繁體文本中混入了簡體字「%c」，應為%s之一。", ch, strings.Join(options, "、")))
		}
	}
}

// twQuoteCorner is tw.quote.corner: “” -> 「」 and ‘’ -> 『』 around Chinese
// text (and straight quotes too). Some Taiwanese publishers use “”:
// quotes=curly.
func twQuoteCorner(t *Text, c *ctx, o *out) {
	if c.profile.opt("quotes") == "curly" {
		return
	}
	msg := "臺灣的引號用「」，引號內再用『』。"
	for _, q := range []struct {
		open, close     rune
		toOpen, toClose string
	}{{'“', '”', "「", "」"}, {'‘', '’', "『", "』"}} {
		for i := 0; i < t.Len(); {
			if t.Chars[i] != q.open {
				i++
				continue
			}
			j := slices.Index(t.Chars[i+1:], q.close)
			if j < 0 {
				break
			}
			j += i + 1
			if slices.ContainsFunc(t.Chars[i+1:j], IsCJK) {
				o.fix("tw.quote.corner", i, i+1, q.toOpen, msg)
				o.fix("tw.quote.corner", j, j+1, q.toClose, msg)
			}
			i = j + 1
		}
	}
	straightQuotePairs(t, o, "tw.quote.corner", "「", "」", msg, IsCJK)
}

// twHKMix is tw.script.hk-mix: Taiwanese and Hong Kong forms (裡/裏, 著/着,
// 線/綫, 衛/衞) mixed. A chosen profile flags the other region's forms;
// Automatic only flags them when the text also has the region's own.
func twHKMix(t *Text, c *ctx, o *out, region string) {
	hk := region == "HK"
	var own, other []rune
	for _, p := range twHK {
		if hk {
			own, other = append(own, p[1]), append(other, p[0])
		} else {
			own, other = append(own, p[0]), append(other, p[1])
		}
	}
	hasOwn := slices.ContainsFunc(t.Chars, func(r rune) bool { return slices.Contains(own, r) })
	if !(c.profile.Explicit || hasOwn) {
		return
	}
	for i, ch := range t.Chars {
		if k := slices.Index(other, ch); k >= 0 {
			msg := "臺灣用字作「" + string(own[k]) + "」。"
			if hk {
				msg = "香港用字作「" + string(own[k]) + "」。"
			}
			o.fix("tw.script.hk-mix", i, i+1, string(own[k]), msg)
		}
	}
}

// twDash is tw.punct.dash: —— and ── mixed in one document (Taiwan uses
// ──).
func twDash(t *Text, c *ctx, o *out) {
	s := c.stats
	var from, to string
	switch {
	case minority(s.zhEmDash, s.zhBoxDash):
		from, to = "——", "──"
	case minority(s.zhBoxDash, s.zhEmDash):
		from, to = "──", "——"
	default:
		return
	}
	for _, m := range findStr(t, from) {
		o.fix("tw.punct.dash", m[0], m[1], to, "破折號寫法不一致：本文多用「"+to+"」。")
	}
}

// twMiddot is tw.punct.middot (a chosen zh-Hant-TW profile): · -> ．
// between the parts of a name and in 《書．篇》.
func twMiddot(t *Text, c *ctx, o *out) {
	if !c.profile.Explicit {
		return
	}
	for i := 1; i+1 < t.Len(); i++ {
		if t.Chars[i] == '·' && isHan(t.Chars[i-1]) && isHan(t.Chars[i+1]) {
			o.fix("tw.punct.middot", i, i+1, "．", "教育部標點符號手冊的間隔號是「．」。")
		}
	}
}

// twMainlandWords is tw.word.mainland: mainland vocabulary (軟件 -> 軟體) -
// suggestions.
func twMainlandWords(t *Text, _ *ctx, o *out) {
	for _, p := range twMainland {
		for _, m := range findStr(t, p[0]) {
			if !t.inQuotes(m[0], m[1]) {
				o.fix("tw.word.mainland", m[0], m[1], p[1], "臺灣慣用「"+p[1]+"」。")
			}
		}
	}
}

var (
	twWesternYearRe = MustRe(`(19[2-9]\d|20\d\d)年`)
	twMarkerRe      = MustRe(`^(?:(\d)、|[(（](\d)[)）]|（([１-９])）)`)
)

// twGongwen is the Taiwanese 公文 profile: legal characters, 民國 years,
// item markers, 臺.
func twGongwen(t *Text, _ *ctx, o *out) {
	for _, p := range twLegal {
		for _, m := range findStr(t, p[0]) {
			o.fix("tw.gongwen.legal-chars", m[0], m[1], p[1], "法律統一用字作「"+p[1]+"」。")
		}
	}
	for _, g := range t.captures(twWesternYearRe) {
		if !g[0].OK || !g[1].OK || t.beforeIs(g[0].A, isASCIIDigit) {
			continue
		}
		year, err := strconv.Atoi(t.slice(g[1].A, g[1].B))
		if err != nil {
			continue
		}
		o.fix("tw.gongwen.year", g[0].A, g[0].B, strconv.Itoa(year-1911)+"年", "公文年份以國曆（民國紀年）為準。")
	}
	fw := func(d rune) rune { return d + 0xFEE0 }
	msg := "公文項目符號依序為「一、」「(一)」「１、」「(１)」：全形數字、半形括號。"
	for _, g := range t.captures(twMarkerRe) {
		a, b := g[0].A, g[0].B
		switch {
		case g[1].OK:
			o.fix("tw.gongwen.markers", a, b, string(fw(t.Chars[g[1].A]))+"、", msg)
		case g[2].OK:
			o.fix("tw.gongwen.markers", a, b, "("+string(fw(t.Chars[g[2].A]))+")", msg)
		case g[3].OK:
			o.fix("tw.gongwen.markers", a, b, "("+string(t.Chars[g[3].A])+")", msg)
		}
	}
	names := []string{"台積電", "台塑", "台達電", "電台", "台幣", "台灣大哥大"}
	for _, m := range findStr(t, "台") {
		a, b := m[0], m[1]
		inName := slices.ContainsFunc(names, func(name string) bool {
			return slices.ContainsFunc(findStr(t, name), func(n [2]int) bool { return n[0] <= a && b <= n[1] })
		})
		if !inName {
			o.fix("tw.gongwen.tai", a, b, "臺", "教育部標準字作「臺」（公文建議）。")
		}
	}
}
