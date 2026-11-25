package rules

import (
	"strings"
	"testing"
)

const J = Ja

func TestJapaneseWidthsAndPunctuation(t *testing.T) {
	eqStr(t, fixed("ja.width.kana", J, "", "ｶﾞｲﾄﾞを読む"), "ガイドを読む")
	eqStr(t, fixed("ja.punct.halfwidth", J, "", "今日は雨,明日は晴れ."), "今日は雨、明日は晴れ。")
	none(t, hits("ja.punct.halfwidth", J, "", "バージョン 3.14 と URL の example.com"))
	// A document that writes ，． keeps them; a stray 、 is flagged.
	eqPairs(t, hits("ja.punct.halfwidth", J, "", "これは，本当に，そうです，ね．しかし、違う．"), one("、", "，"))
	eqStr(t, fixed("ja.width.alnum", J, "", "第１０章のＡＩ"), "第10章のAI")
	eqStr(t, fixed("ja.width.alnum", J, "ja/koyobun", "第１０章のＱＯＬ"), "第10章のＱＯＬ")
}

func TestJapaneseSpacesAreRemovedNeverAdded(t *testing.T) {
	// Automatic: a stray space in an unspaced document goes...
	eqStr(t, fixed("ja.space.typed", J, "", "新しいAPIを使う。古いURLとJSONも消す。新しい API を使う"),
		"新しいAPIを使う。古いURLとJSONも消す。新しいAPIを使う")
	// ...but a document that spaces its Latin terms keeps them.
	none(t, hits("ja.space.typed", J, "", "新しい API を使う"))
	// A chosen profile removes them; a space inside a Latin phrase is not a
	// boundary.
	eqStr(t, fixed("ja.space.typed", J, "ja/jtf", "Visual Studio Code を使う"), "Visual Studio Codeを使う")
	// A missing space is never flagged.
	none(t, hits("ja.space.typed", J, "", "新しいAPIを使う"))
	eqStr(t, fixed("ja.space.fullwidth", J, "", "これは 本です。「 はい 」"), "これは本です。「はい」")
	none(t, hits("ja.space.fullwidth", J, "", "変更は 設定 → ショートカット の設定が優先されます"))
	// The full-width space after ！ stays.
	none(t, hits("ja.space.fullwidth", J, "", "本当ですか！\u3000すごい"))
	eqStr(t, fixed("ja.punct.after-exclaim", J, "", "本当！すごい"), "本当！\u3000すごい")
	none(t, hits("ja.punct.after-exclaim", J, "", "「本当！」と言った。やった！って"))
	none(t, hits("ja.punct.after-exclaim", J, "ja/fiction", "本当！すごい"))
}

func TestJapaneseNumbersTildesAndLeaders(t *testing.T) {
	eqStr(t, fixed("ja.punct.tilde", J, "", "10~20人"), "10～20人")
	eqStr(t, fixed("ja.num.counter", J, "", "3ヶ月と5ヵ所"), "3か月と5か所")
	none(t, hits("ja.num.counter", J, "", "霞ケ関に行く"))
	eqStr(t, fixed("ja.punct.dots-dash", J, "", "そして...彼の言葉ーー去った"), "そして……彼の言葉――去った")
	none(t, hits("ja.punct.dots-dash", J, "", "えーーと、そうですね"))
	eqStr(t, fixed("ja.punct.single-leader", J, "", "待って…それで"), "待って……それで")
	none(t, hits("ja.punct.single-leader", J, "", "メニューの「編集…」を選ぶ"))
	none(t, hits("ja.punct.single-leader", J, "ja/koyobun", "待って…それで"))
	eqStr(t, fixed("ja.num.kanji-digit", J, "", "5千人と3百円、1万円"), "5,000人と300円、1万円")
}

func TestJapaneseRegisterWordsAndStyle(t *testing.T) {
	mixed := "雨が降ります。風が強いです。寒いです。明日は晴れである。"
	eqPairs(t, hits("ja.style.register-mix", J, "", mixed), flagged("である"))
	none(t, hitsIn("ja.style.register-mix", J, "", mixed, blockItem))
	eqStr(t, fixed("ja.word.ranuki", J, "", "ここから海が見れる。"), "ここから海が見られる。")
	none(t, hits("ja.word.ranuki", J, "", "見れば分かる。"))
	none(t, hits("ja.word.ranuki", J, "ja/fiction", "海が見れる。"))
	count(t, hits("ja.style.no-repeat", J, "", "東京の会社の社長の娘。"), 1)
	none(t, hits("ja.style.no-repeat", J, "", "そのプロジェクトのものがこの方法のもの。"))
	count(t, hits("ja.style.double-negative", J, "", "行かないわけではない。"), 1)
	eqStr(t, fixed("ja.word.redundant", J, "", "参加することができる。第3回目の会議。"), "参加できる。第3回の会議。")
	count(t, hits("ja.keigo.patterns", J, "", "社長がお帰りになられました。"), 1)
	none(t, hits("ja.keigo.patterns", J, "", "とんでもございません。"))
	eqStr(t, fixed("ja.punct.period-bracket", J, "ja/jtf", "「はい。」と答えた"), "「はい」と答えた")
	none(t, hits("ja.punct.period-bracket", J, "", "「はい。」と答えた"))
}

func TestJapaneseDocumentConsistency(t *testing.T) {
	text := "コンピューターとコンピューターとコンピュータ。子どもと子どもと子供。取り扱いと取り扱いと取扱い。"
	eqPairs(t, hits("ja.word.choon", J, "", text), one("コンピュータ", "コンピューター"))
	eqPairs(t, hits("ja.word.kodomo", J, "", text), one("子供", "子ども"))
	eqPairs(t, hits("ja.word.okurigana", J, "", text), one("取扱い", "取り扱い"))
	long := strings.Repeat("あ", 120) + "。"
	count(t, hits("ja.style.sentence-length", J, "", long), 1)
	none(t, hits("ja.style.sentence-length", J, "ja/fiction", long))
}
