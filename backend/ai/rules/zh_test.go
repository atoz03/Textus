package rules

import (
	"strings"
	"testing"
)

const H = ZhHans

func TestHalfWidthPunctuationInChinese(t *testing.T) {
	eqStr(t, fixed("zh.punct.halfwidth", H, "", "今天下午开会,大家讨论了三个问题.第一个是预算:钱不够!"),
		"今天下午开会，大家讨论了三个问题。第一个是预算：钱不够！")
	// The space after the comma goes with it.
	eqStr(t, fixed("zh.punct.halfwidth", H, "", "开会, 大家来了"), "开会，大家来了")
	eqStr(t, fixed("zh.punct.halfwidth", H, "", "这是测试(内部版本)的结果"), "这是测试（内部版本）的结果")
	for _, ok := range []string{
		"版本 3.14 和 8:00 开会",
		"备份 MySQL 8.0, 保留 30 天。Done, ok.",
		"1. 第一项",
		"打开文件.txt 看看",
		"使用 Node.js 和 (API) 接口",
		"见 https://a.cn/x,y 页面",
	} {
		none(t, hits("zh.punct.halfwidth", H, "", ok), ok)
	}
}

func TestNoSpaceBesideFullWidthMarks(t *testing.T) {
	eqStr(t, fixed("zh.space.fullwidth-adjacent", H, "", "你好 ，世界。 再见"), "你好，世界。再见")
	eqStr(t, fixed("zh.space.fullwidth-adjacent", H, "", "他说 “好” 然后走了"), "他说“好”然后走了")
	// Curly quotes in English text are not full-width marks.
	none(t, hits("zh.space.fullwidth-adjacent", H, "", "He said “Hello” to me."))
}

func TestEllipses(t *testing.T) {
	eqStr(t, fixed("zh.punct.ellipsis", H, "", "他想了想...然后走了。。。还有…以及⋯⋯和………"),
		"他想了想……然后走了……还有……以及……和……")
	// Two pairs (omitted paragraphs) and English segments stay.
	none(t, hits("zh.punct.ellipsis", H, "", "前文…………后文"))
	none(t, hits("zh.punct.ellipsis", H, "", "他说 wait... ok 吧"))
	// Menu labels keep their single ellipsis.
	none(t, hits("zh.punct.ellipsis", H, "", "右键 > 询问 AI…）打开，或选择导入技能… 即可"))
	eqStr(t, fixed("zh.punct.ellipsis-deng", H, "", "苹果、香蕉……等水果，桃子...等等"), "苹果、香蕉等水果，桃子等等")
}

func TestDashesButNotRanges(t *testing.T) {
	eqStr(t, fixed("zh.punct.dash", H, "", "我们必须承认--这件事很难"), "我们必须承认——这件事很难")
	eqStr(t, fixed("zh.punct.dash", H, "", "他停了一下──然后说"), "他停了一下——然后说")
	eqStr(t, fixed("zh.punct.dash", H, "", "我们必须要承认—这件事情真的很难"), "我们必须要承认——这件事情真的很难")
	for _, ok := range []string{"北京—上海特快", "1031—1095年", "2023--2024 赛季"} {
		none(t, hits("zh.punct.dash", H, "", ok), ok)
	}
	// Taiwan writes ──: not a mainland rule there.
	none(t, hits("zh.punct.dash", ZhHant, "", "他停了一下──然後說"))
}

func TestStackedMarksByProfile(t *testing.T) {
	none(t, hits("zh.punct.stacked", H, "zh-Hans-CN/fiction", "什么？！你说什么！！！"))
	eqPairs(t, hits("zh.punct.stacked", H, "zh-Hans-CN/fiction", "天哪！！！！"), one("！！！！", "！！！"))
	eqPairs(t, hits("zh.punct.stacked", H, "zh-Hans-CN/tech", "成功了！！"), one("！！", "！"))
	eqPairs(t, hits("zh.punct.stacked", H, "zh-Hans-CN/tech", "真的吗？？！"), one("？？！", "？！"))
	// ？！ is always fine.
	none(t, hits("zh.punct.stacked", H, "zh-Hans-CN/tech", "真的？！"))
}

func TestHeadingPunctuation(t *testing.T) {
	eqPairs(t, hitsIn("zh.heading.end-punct", H, "", "第一章 开始。", headingBlock(2)), one("。", ""))
	none(t, hitsIn("zh.heading.end-punct", H, "", "为什么？", headingBlock(2)))
	none(t, hitsIn("zh.heading.end-punct", H, "", "他走了。", blockPara))
}

func TestStraightQuotesAroundChinese(t *testing.T) {
	eqStr(t, fixed("zh.quote.straight", H, "", "他说\"你好\"就走了"), "他说“你好”就走了")
	eqStr(t, fixed("zh.quote.straight", H, "zh-Hans-CN", "他说\"你好\"就走了"), "他说“你好”就走了")
	if all := findings(H, "", "x", blockUnkno); len(all) != 0 {
		t.Errorf("findings on x: %v", all)
	}
	for _, ok := range []string{"他说 \"hello world\" 吧", "只有一个\"引号", "代码 `\"a\"` 里"} {
		none(t, hits("zh.quote.straight", H, "", ok), ok)
	}
}

func TestCJKLatinSpacingFollowsTheMajorityOrTheSetting(t *testing.T) {
	// Automatic: two spaced, one tight -> the tight one is flagged.
	eqPairs(t, hits("zh.space.cjk-latin", H, "", "我用 React 写了一个Vue 组件"), one("个V", "个 V"))
	// A tie or a single example: nothing.
	none(t, hits("zh.space.cjk-latin", H, "", "我用React和 Vue"))
	none(t, hits("zh.space.cjk-latin", H, "", "我用React写的"))
	// The tech profile wants spaces.
	eqStr(t, fixed("zh.space.cjk-latin", H, "zh-Hans-CN/tech", "我用React写的"), "我用 React 写的")
	// Never before % or punctuation.
	for _, h := range hits("zh.space.cjk-latin", H, "zh-Hans-CN/tech", "增长了50%。") {
		if strings.Contains(h.orig, "%") {
			t.Errorf("spaced before %%: %v", h)
		}
	}
}

func TestWidthsDecimalsAndNumbers(t *testing.T) {
	eqStr(t, fixed("zh.width.alnum", H, "", "第Ａ１号文件"), "第A1号文件")
	none(t, hits("zh.width.alnum", ZhHant, "zh-Hant-TW/gongwen", "１、說明"))
	eqStr(t, fixed("zh.num.decimal", H, "", "比例是0。46，或者是.46"), "比例是0.46，或者是0.46")
	eqStr(t, fixed("zh.num.approx", H, "", "过了3、4个月"), "过了三四个月")
	eqStr(t, fixed("zh.num.approx", H, "", "来了几1000人"), "来了几千人")
	for _, ok := range []string{"第3、4个", "1、2、3个", "3、5个", "二、三产业"} {
		none(t, hits("zh.num.approx", H, "", ok), ok)
	}
	none(t, hits("zh.num.approx", ZhHant, "", "約3、4天"))
	eqStr(t, fixed("zh.title.dunhao", H, "", "读了《红楼梦》、《水浒传》"), "读了《红楼梦》《水浒传》")
	if n := len(hits("zh.num.mixed-date", H, "", "二〇二六年10月")); n != 1 {
		t.Errorf("mixed-date: %d hits", n)
	}
	if n := len(hits("zh.num.short-year", H, "", "98年5月出生")); n != 1 {
		t.Errorf("short-year: %d hits", n)
	}
	none(t, hits("zh.num.short-year", H, "", "90年代，活了90年"))
	eqStr(t, fixed("zh.num.mixed-unit", H, "", "有4千人"), "有4000人")
	if n := len(hits("zh.num.mixed-unit", H, "", "共1百零8将")); n != 1 {
		t.Errorf("mixed-unit: %d hits", n)
	}
	for _, ok := range []string{"跑了5千米", "重3千克", "10万人", "3亿元"} {
		none(t, hits("zh.num.mixed-unit", H, "", ok), ok)
	}
	eqStr(t, fixed("zh.date.dunhao", H, "", "于2008-8发布"), "于2008-08发布")
	none(t, hits("zh.date.dunhao", H, "", "于2008-08-08发布"))
}

func TestNeverFlagIdentifiersAndEstablishedTerms(t *testing.T) {
	for _, ok := range []string{"2026年的3G网络和MP3", "G8峰会讨论维生素B12", "电话 010-12345678", "型号 A3-200 的价格", "第1990号"} {
		for _, f := range findings(H, "", ok, blockUnkno) {
			if strings.HasPrefix(f.Rule, "zh.num") {
				t.Errorf("%s: %v", ok, f)
			}
		}
	}
	eqStr(t, fixed("zh.num.ascii-range", H, "", "需要3-5天"), "需要3～5天")
	eqStr(t, fixed("zh.num.unit-space", H, "", "增长 50 %"), "增长 50%")
}

func TestWordsScriptAndIdeographs(t *testing.T) {
	eqStr(t, fixed("zh.word.typo", H, "", "我们要再接再励"), "我们要再接再厉")
	none(t, hits("zh.word.typo", H, "", "“再接再励”是常见错误"))
	eqStr(t, fixed("zh.word.typo", ZhHant, "", "我們要再接再勵"), "我們要再接再厲")
	eqStr(t, fixed("zh.word.variant", H, "", "这是惟一的成份"), "这是唯一的成分")
	// 蹚水 and 标识 are standard.
	none(t, hits("zh.word.variant", H, "", "蹚水过河的标识"))
	eqPairs(t, hits("zh.script.mixed", H, "", "我们这个问题很重要，我們明天再讨论这个问题。"), one("們", "们"))
	// Mostly Traditional text isn't "Simplified with strays".
	none(t, hits("zh.script.mixed", H, "", "這個問題"))
	eqStr(t, fixed("zh.compat-ideograph", H, "", "\uF99A續"), "連續")
	none(t, hits("zh.compat-ideograph", H, "", "\uFA11"))
}

func TestMiddotAndLongClauses(t *testing.T) {
	eqStr(t, fixed("zh.punct.middot", H, "", "让・雅克•卢梭"), "让·雅克·卢梭")
	long := "这是一个非常非常长的没有任何标点符号的句子它一直写下去写下去写下去写下去一直到超过了四十个字为止"
	if n := len(hits("zh.style.clause-length", H, "", long)); n != 1 {
		t.Errorf("clause-length: %d hits", n)
	}
	none(t, hits("zh.style.clause-length", H, "zh-Hans-CN/fiction", long))
}

func TestGongwenAndNewmediaProfilesOnly(t *testing.T) {
	g := "zh-Hans-CN/gongwen"
	eqStr(t, fixed("zh.gongwen.docnum", H, g, "国办发[2012]第09号"), "国办发〔2012〕9号")
	none(t, hits("zh.gongwen.docnum", H, g, "国办发〔2012〕9号"))
	none(t, hits("zh.gongwen.docnum", H, "", "国办发[2012]第09号"))
	eqStr(t, fixed("zh.gongwen.date", H, g, "2026年10月09日"), "2026年10月9日")
	if n := len(hitsIn("zh.gongwen.title", H, g, "关于经费的请示报告", headingBlock(1))); n != 1 {
		t.Errorf("gongwen.title: %d hits", n)
	}
	n := "zh-Hans-CN/newmedia"
	if k := len(hits("zh.ad.absolute", H, n, "全网最低，销量第一的最佳选择")); k != 3 {
		t.Errorf("ad.absolute: %d hits", k)
	}
	none(t, hits("zh.ad.absolute", H, n, "这是第一次见面"))
	none(t, hits("zh.ad.absolute", H, "", "最佳选择"))
	if k := len(hits("zh.ad.unsourced", H, n, "数据显示，90%的用户喜欢")); k != 2 {
		t.Errorf("ad.unsourced: %d hits", k)
	}
	none(t, hits("zh.ad.unsourced", H, n, "据《年报》数据显示"))
}

const T = ZhHant

func TestSimplifiedCharactersInTraditionalText(t *testing.T) {
	eqPairs(t, hits("tw.script.simplified", T, "", "這個問題很重要，我們明天再讨論。"), one("讨", "討"))
	if h := hits("tw.script.simplified", T, "", "這個問題很重要，我們明天再發言，头发很長。"); len(h) < 2 || h[1].fix {
		t.Errorf("one-to-many: %v", h)
	}
	// Characters both scripts share (里, 后, 台) are not "simplified".
	none(t, hits("tw.script.simplified", T, "", "這個問題在公里和皇后之間。"))
}

func TestCornerQuotesInTaiwanButNotWithCurlyOrOnTheMainland(t *testing.T) {
	eqStr(t, fixed("tw.quote.corner", T, "", "他說：“我們‘明天’見。”"), "他說：「我們『明天』見。」")
	eqStr(t, fixed("tw.quote.corner", T, "", "他說\"好的\"就走了"), "他說「好的」就走了")
	none(t, hits("tw.quote.corner", T, "zh-Hant-CN", "他說：“我們見。”"))
	none(t, hits("tw.quote.corner", T, "", "He said “hi” 然後"))
}

func TestTaiwanAndHongKongForms(t *testing.T) {
	eqPairs(t, hits("tw.script.hk-mix", T, "", "這裡有人，那裏沒有。"), one("裏", "裡"))
	// Automatic, with no Taiwanese form beside it: left alone.
	none(t, hits("tw.script.hk-mix", T, "", "那裏沒有人。"))
	eqPairs(t, hits("tw.script.hk-mix", T, "zh-Hant-HK", "這裡沒有人。"), one("裡", "裏"))
}

func TestMainlandWordsAndMiddot(t *testing.T) {
	eqStr(t, fixed("tw.word.mainland", T, "", "這個軟件的信息"), "這個軟體的資訊")
	none(t, hits("tw.word.mainland", T, "zh-Hant-HK", "這個軟件"))
	// 質量 (mass) and 程序 (procedure) alone are fine.
	none(t, hits("tw.word.mainland", T, "", "物體的質量與法律程序"))
	eqStr(t, fixed("tw.punct.middot", T, "zh-Hant-TW", "馬克·吐溫"), "馬克．吐溫")
	none(t, hits("tw.punct.middot", T, "", "馬克·吐溫"))
}

func TestTaiwaneseGongwen(t *testing.T) {
	g := "zh-Hant-TW/gongwen"
	eqStr(t, fixed("tw.gongwen.legal-chars", T, g, "請盡量公佈"), "請儘量公布")
	eqStr(t, fixed("tw.gongwen.year", T, g, "2026年10月14日"), "115年10月14日")
	eqStr(t, fixed("tw.gongwen.markers", T, g, "1、說明"), "１、說明")
	eqStr(t, fixed("tw.gongwen.markers", T, g, "(2)辦法"), "(２)辦法")
	eqStr(t, fixed("tw.gongwen.tai", T, g, "台北市的台積電"), "臺北市的台積電")
	none(t, hits("tw.gongwen.year", T, "", "2026年"))
	// Never flagged: 頓號 between titled items, 約三、四天.
	if all := findings(T, g, "閱讀《水滸傳》、《三國演義》約三、四天。", blockUnkno); len(all) != 0 {
		t.Errorf("%v", all)
	}
}
