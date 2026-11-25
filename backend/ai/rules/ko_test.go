package rules

import "testing"

const K = Ko

func TestKoreanParticlesAttachButUnitsAndQuotesStay(t *testing.T) {
	eqStr(t, fixed("ko.space.particle", K, "", "API 를 호출하고 v2 에서 확인했다."), "API를 호출하고 v2에서 확인했다.")
	for _, ok := range []string{"사과 3 개를 샀다", "기온이 30 도 이상", "“좋아” 하고 웃었다", "API 문서를 봤다"} {
		none(t, hits("ko.space.particle", K, "", ok), ok)
	}
}

func TestKoreanMarksAndSpellingLists(t *testing.T) {
	eqStr(t, fixed("ko.punct.cjk-marks", K, "", "좋습니다。그리고、"), "좋습니다.그리고,")
	eqStr(t, fixed("ko.word.myeochil", K, "", "몇일 걸려요?"), "며칠 걸려요?")
	eqStr(t, fixed("ko.word.dwae", K, "", "안되요. 다 됬다."), "안돼요. 다 됐다.")
	eqStr(t, fixed("ko.word.saisiot", K, "", "햇님과 갯수"), "해님과 개수")
	eqStr(t, fixed("ko.word.ryul", K, "", "실패률과 할인률"), "실패율과 할인율")
	// 합격률 is right (률 after a consonant).
	none(t, hits("ko.word.ryul", K, "", "합격률"))
}

func TestKoreanDatesColonsAndHints(t *testing.T) {
	eqStr(t, fixed("ko.date.format", K, "", "날짜: 2026.10.14 오전"), "날짜: 2026. 10. 14. 오전")
	none(t, hits("ko.date.format", K, "", "버전 2026.10.14.3과 192.168.0.1"))
	none(t, hits("ko.date.format", K, "", "2026. 10. 14. 회의"))
	eqStr(t, fixed("ko.punct.colon-space", K, "", "장소 : 광화문, 일시:내일"), "장소: 광화문, 일시: 내일")
	none(t, hits("ko.punct.colon-space", K, "", "10:20에 만나고 3:16 구절"))
	count(t, hits("ko.word.double-passive", K, "", "잊혀진 계절"), 1)
	count(t, hits("ko.honorific.thing", K, "", "주문하신 커피 나오셨습니다."), 1)
}

func TestKoreanRegisterAndOfficialProfile(t *testing.T) {
	eqPairs(t, hits("ko.style.register-mix", K, "", "확인했습니다. 진행합니다. 완료됩니다. 좋아요."), flagged("좋아요"))
	// Instructions in -세요 are normal in a 하십시오체 manual.
	none(t, hits("ko.style.register-mix", K, "", "확인했습니다. 진행합니다. 완료됩니다. 파일을 여세요."))
	eqStr(t, fixed("ko.official.format", K, "ko/official", "오후 3시 30분에 시작"), "15:30에 시작")
	none(t, hits("ko.official.format", K, "", "오후 3시"))
}

func TestNeverFlagInKorean(t *testing.T) {
	text := "비가 와서, 우리는 3 개를 샀고... 1,000원(?)이었다… “네” 하고 답했다."
	if all := findings(K, "", text, blockPara); len(all) != 0 {
		t.Errorf("%v", all)
	}
}
