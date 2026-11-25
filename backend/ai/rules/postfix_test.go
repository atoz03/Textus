package rules

import (
	"encoding/json/v2"
	"slices"
	"testing"
)

const zhDoc = "# 周报\n\n本周我们完成了三项工作，进展顺利。下周继续推进，争取月底上线。团队讨论了预算问题，没有分歧。\n"

func TestProposalsMatchADocumentThatFollowsTheConvention(t *testing.T) {
	conv := defaultConv()
	fixed, rules := ProposalText(zhDoc, "我们还需要测试,然后发布...", conv)
	eqStr(t, fixed, "我们还需要测试，然后发布……")
	if !slices.Equal(rules, []string{"zh.punct.halfwidth", "zh.punct.ellipsis"}) {
		t.Errorf("%v", rules)
	}
	// A document that writes half-width punctuation on purpose keeps it.
	hwDoc := "本周我们完成了三项工作,进展顺利.下周继续推进,争取月底上线.团队讨论了预算问题,没有分歧."
	fixed, rules = ProposalText(hwDoc, "我们还需要测试,然后发布.", conv)
	eqStr(t, fixed, "我们还需要测试,然后发布.")
	if len(rules) != 0 {
		t.Errorf("%v", rules)
	}
	// Only P0 safe rules: a P1 suggestion (straight quotes) is not applied.
	fixed, _ = ProposalText(zhDoc, "他说\"好\"。", conv)
	eqStr(t, fixed, "他说\"好\"。")
}

func TestChosenFrenchSpacingReachesProposals(t *testing.T) {
	doc := "Bonjour! Le programme commence à neuf heures, avec l'accueil des participants et une présentation du projet."
	text := "Bonjour ! A-t-il confirmé sa venue ? La séance dure une heure ; voici le plan : 85 % viennent."
	conv := defaultConv()
	// Automatic: French spacing isn't forced on a document without it.
	fixed, _ := ProposalText(doc, text, conv)
	eqStr(t, fixed, text)
	conv.Project.Profiles = append(conv.Project.Profiles, "fr-FR")
	fixed, rules := ProposalText(doc, text, conv)
	eqStr(t, fixed, "Bonjour\u202F! A-t-il confirmé sa venue\u202F? La séance dure une heure\u202F; voici le plan\u00A0: 85\u00A0% viennent.")
	if !slices.Equal(rules, []string{"fr.space.high-punct", "fr.num.symbol-space"}) {
		t.Errorf("%v", rules)
	}
}

func TestProposalArgumentsRewriteOnlyText(t *testing.T) {
	conv := defaultConv()
	args := `{"action":"replace","anchor":"进展顺利。","text":"进展顺利,没有延期。"}`
	newArgs, rules, ok := ProposalArguments(args, zhDoc, conv)
	if !ok {
		t.Fatal("unchanged")
	}
	var v map[string]string
	if err := json.Unmarshal([]byte(newArgs), &v); err != nil {
		t.Fatal(err)
	}
	eqStr(t, v["text"], "进展顺利，没有延期。")
	eqStr(t, v["anchor"], "进展顺利。")
	if !slices.Equal(rules, []string{"zh.punct.halfwidth"}) {
		t.Errorf("%v", rules)
	}
	if _, _, ok := ProposalArguments(`{"action":"delete","anchor":"x"}`, zhDoc, conv); ok {
		t.Error("no text")
	}
	if _, _, ok := ProposalArguments(`{"action":"append","text":"没有问题。"}`, zhDoc, conv); ok {
		t.Error("nothing to fix")
	}
	// Korean and Japanese documents.
	koDoc := "이번 주에는 API를 정리하고 문서를 작성했습니다. 다음 주에는 테스트를 진행할 예정입니다."
	fixed, _ := ProposalText(koDoc, "v2 를 배포합니다。", conv)
	eqStr(t, fixed, "v2를 배포합니다.")
	jaDoc := "今週は三つの作業を終えました。来週は試験を行い、月末に公開します。"
	fixed, _ = ProposalText(jaDoc, "ｶﾞｲﾄﾞを更新,公開します.", conv)
	eqStr(t, fixed, "ガイドを更新、公開します。")
}

func TestCompletionsMatchTheTextBeforeTheCursor(t *testing.T) {
	for _, c := range [][3]string{
		{"今天我们开会，讨论了预算。然后", "大家都同意,没有异议...", "大家都同意，没有异议……"},
		// Half-width before the cursor: punctuation left alone.
		{"今天我们开会,讨论了预算.然后", "大家都同意,没有异议", "大家都同意,没有异议"},
		// Spacing matched: spaced before, so the completion gets spaces -
		// including across the cursor.
		{"我们用 React 写了", "Vue组件", " Vue 组件"},
		{"我们用React写了", " Vue 组件", "Vue组件"},
		// Korean particles attach; Japanese never gets a space.
		{"이번 주에는 ", "API 를 정리했다", "API를 정리했다"},
		{"新しい", "APIを使う", "APIを使う"},
		// French spacing matched.
		{"Vraiment\u202F? Oui\u202F! Et", " alors?", " alors\u202F?"},
		// Quotes matched.
		{"He said “yes” and “no”, then", " \"maybe\" twice", " “maybe” twice"},
		{"It's what they're", " doing’s fine", " doing's fine"},
		// Nothing to do.
		{"Hello", " world", " world"},
	} {
		eqStr(t, Completion(c[0], c[1]), c[2], c[0]+"|"+c[1])
	}
	eqStr(t, tail("一二三四", 2), "三四")
	eqStr(t, tail("ab", 5), "ab")
}
