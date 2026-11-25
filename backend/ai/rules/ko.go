package rules

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// Korean rules, from 한글 맞춤법 and 문장 부호. Dependent-noun and
// auxiliary-verb spacing needs morphology and is left to the model.

func koRun(t *Text, c *ctx, o *out) {
	koSpaceParticle(t, c, o)
	koCJKMarks(t, c, o)
	koWords(t, c, o)
	koDateFormat(t, c, o)
	koColonSpace(t, c, o)
	koDoublePassive(t, c, o)
	koHonorificThing(t, c, o)
	koRegisterMix(t, c, o)
	koOfficial(t, c, o)
}

// koParticles are standalone particles that attach to the word before
// them.
var koParticles = []string{"에서", "에게", "으로", "까지", "부터", "처럼", "보다", "이나", "을", "를", "이", "가", "은",
	"는", "의", "에", "로", "와", "과", "도", "만", "나", "랑"}

// koSpaceParticle is ko.space.particle: Latin text or a digit, a space,
// then a particle -> no space ("API 를" -> "API를"). After a digit, 도
// (degrees) and 만 (ten thousand) are not particles.
func koSpaceParticle(t *Text, _ *ctx, o *out) {
	n := t.Len()
	for i := 1; i+1 < n; i++ {
		if t.Chars[i] != ' ' {
			continue
		}
		prev := t.Chars[i-1]
		if !(isASCIIAlnum(prev) || strings.ContainsRune(`)”’"`, prev)) {
			continue
		}
		// The particle: a run of Hangul after the space, ending the word.
		j := i + 1
		for j < n && IsHangul(t.Chars[j]) {
			j++
		}
		word := t.slice(i+1, j)
		endsWord := t.atNoneOr(j, func(r rune) bool { return isSpace(r) || strings.ContainsRune(".,?!):;", r) })
		if !endsWord || !slices.Contains(koParticles, word) {
			continue
		}
		if isASCIIDigit(prev) && (word == "도" || word == "만") {
			continue
		}
		if prev == '”' || prev == '"' {
			continue // “…” 하고, quoted speech
		}
		o.fix("ko.space.particle", i-1, j, string(prev)+word, "조사는 앞말에 붙여 씁니다.")
	}
}

// koCJKMarks is ko.punct.cjk-marks: 。 and 、 in Korean text -> . and ,
// (the 2015 문장 부호 dropped them). Quoted Japanese or Chinese keeps its
// own.
func koCJKMarks(t *Text, _ *ctx, o *out) {
	for i := 1; i < t.Len(); i++ {
		ch := t.Chars[i]
		if (ch == '。' || ch == '、') && IsHangul(t.Chars[i-1]) {
			to := ","
			if ch == '。' {
				to = "."
			}
			o.fix("ko.punct.cjk-marks", i, i+1, to, "한국어 문장에는 마침표(.)와 쉼표(,)를 씁니다.")
		}
	}
}

// koWords is ko.word.*: closed spelling lists - 며칠, 돼, 사이시옷, 율/률.
func koWords(t *Text, _ *ctx, o *out) {
	for _, m := range findStr(t, "몇일") {
		o.fix("ko.word.myeochil", m[0], m[1], "며칠", "‘몇일’이 아니라 ‘며칠’로 씁니다.")
	}
	for _, m := range findStr(t, "되요") {
		o.fix("ko.word.dwae", m[0], m[1], "돼요", "‘되어요’의 준말은 ‘돼요’입니다.")
	}
	for _, m := range findStr(t, "됬") {
		o.fix("ko.word.dwae", m[0], m[1], "됐", "‘되었-’의 준말은 ‘됐-’입니다.")
	}
	for _, p := range [][2]string{{"햇님", "해님"}, {"갯수", "개수"}, {"촛점", "초점"}, {"댓가", "대가"}, {"싯가", "시가"}} {
		for _, m := range findStr(t, p[0]) {
			o.fix("ko.word.saisiot", m[0], m[1], p[1], "사이시옷을 쓰지 않습니다: ‘"+p[1]+"’.")
		}
	}
	for _, p := range [][2]string{
		{"비률", "비율"}, {"실패률", "실패율"}, {"규률", "규율"}, {"백분률", "백분율"}, {"할인률", "할인율"}, {"이자률", "이자율"},
		{"감소률", "감소율"}, {"증가률", "증가율"}, {"운률", "운율"}, {"선률", "선율"}, {"전률", "전율"}, {"환률", "환율"},
	} {
		for _, m := range findStr(t, p[0]) {
			o.fix("ko.word.ryul", m[0], m[1], p[1], "모음이나 ㄴ 뒤에서는 ‘율’로 씁니다: ‘"+p[1]+"’.")
		}
	}
}

var koDateRe = MustRe(`((?:19|20)\d{2})\.\s?(\d{1,2})\.\s?(\d{1,2})(\.?)`)

// koDateFormat is ko.date.format: 2026.10.14 or a missing final period ->
// 2026. 10. 14. Versions and IP addresses don't have a 4-digit year with a
// valid month and day, or go on with another number.
func koDateFormat(t *Text, _ *ctx, o *out) {
	for _, g := range t.captures(koDateRe) {
		if !g[0].OK || !g[1].OK || !g[2].OK || !g[3].OK {
			continue
		}
		a, b := g[0].A, g[0].B
		if t.beforeIs(a, func(r rune) bool { return isASCIIDigit(r) || r == '.' }) || t.atIs(b, isASCIIDigit) {
			continue
		}
		month, _ := strconv.Atoi(t.slice(g[2].A, g[2].B))
		day, _ := strconv.Atoi(t.slice(g[3].A, g[3].B))
		if month < 1 || month > 12 || day < 1 || day > 31 {
			continue
		}
		o.fix("ko.date.format", a, b, fmt.Sprintf("%s. %d. %d.", t.slice(g[1].A, g[1].B), month, day),
			"날짜는 ‘2026. 10. 14.’처럼 마침표를 찍고 띄어 씁니다.")
	}
}

// koColonSpace is ko.punct.colon-space: `장소 : 광화문` / `장소:광화문` ->
// `장소: 광화문`. Times, ratios and chapter:verse (digits on both sides)
// stay.
func koColonSpace(t *Text, _ *ctx, o *out) {
	for i := range t.Len() {
		if t.Chars[i] != ':' {
			continue
		}
		a := i
		for a > 0 && t.Chars[a-1] == ' ' {
			a--
		}
		if a == 0 || !IsHangul(t.Chars[a-1]) {
			continue
		}
		before := t.Chars[a-1]
		b := i + 1
		for b < t.Len() && t.Chars[b] == ' ' {
			b++
		}
		after, ok := t.At(b)
		if !ok {
			continue
		}
		if isASCIIDigit(after) && isASCIIDigit(before) {
			continue
		}
		if a == i && b == i+2 {
			continue // already `X: Y`
		}
		if !(IsHangul(after) || IsAlphanumeric(after)) {
			continue
		}
		o.fix("ko.punct.colon-space", a-1, b+1, string(before)+": "+string(after), "쌍점(:)은 앞말에 붙이고 뒤는 띄어 씁니다.")
	}
}

var koDoublePassiveRe = MustRe(`(?:보여|잊혀|불려|되어|쓰여|읽혀|짜여|믿겨)(?:지|진|져|졌)`)

// koDoublePassive is ko.word.double-passive: 보여지다, 잊혀지다... - a hint
// (some accept -어지다 as resultative).
func koDoublePassive(t *Text, _ *ctx, o *out) {
	for _, m := range t.find(koDoublePassiveRe) {
		o.flag("ko.word.double-passive", m[0], m[1], "이중 피동 표현입니다: ‘보이다’, ‘잊히다’처럼 써 보세요.")
	}
}

var koThingHonorificRe = MustRe(`(?:커피|음료|음식|주문하신\s?\S+|메뉴|가격|사이즈|물건|상품)\S{0,3}\s(?:나오셨|있으시|없으시|되시)|원이세요|품절이세요`)

// koHonorificThing is ko.honorific.thing: honorifics for things (커피
// 나오셨습니다).
func koHonorificThing(t *Text, _ *ctx, o *out) {
	for _, m := range t.find(koThingHonorificRe) {
		o.flag("ko.honorific.thing", m[0], m[1], "사물을 높이는 표현입니다: ‘커피 나왔습니다’처럼 씁니다.")
	}
}

var (
	koFormalEndRe = MustRe(`(?:습니다|ㅂ니다|십시오|습니까|입니다|합니다|됩니다)[.?!]`)
	// koPoliteEndRe is 해요체 statements and questions. The polite
	// imperative -(으)세요 is left out: manuals in 하십시오체 use it for
	// instructions as a matter of course.
	koPoliteEndRe = MustRe(`[\p{Hangul}-[세]](?:요|죠)[.?!]`)
)

// koRegisterMix is ko.style.register-mix: 하십시오체 and 해요체 mixed in
// one document - the minority flagged; not quotations or dialogue.
func koRegisterMix(t *Text, c *ctx, o *out) {
	if c.block.isHeading() || c.block == blockItem || c.block == blockQuot {
		return
	}
	s := c.stats
	var re *Re
	var msg string
	switch {
	case minority(s.koPolite, s.koFormal):
		re, msg = koPoliteEndRe, "하십시오체와 해요체가 섞여 있습니다(본문은 대부분 하십시오체)."
	case minority(s.koFormal, s.koPolite):
		re, msg = koFormalEndRe, "하십시오체와 해요체가 섞여 있습니다(본문은 대부분 해요체)."
	default:
		return
	}
	for _, m := range t.find(re) {
		a, b := m[0], m[1]
		for a > 0 && IsHangul(t.Chars[a-1]) {
			a--
		}
		if !t.inQuotes(a, b) {
			o.flag("ko.style.register-mix", a, b-1, msg)
		}
	}
}

var koAfternoonRe = MustRe(`(오전|오후)\s?(\d{1,2})시(?:\s?(\d{1,2})분)?`)

// koOfficial is ko.official.* (ko/official): times in 24-hour form.
func koOfficial(t *Text, c *ctx, o *out) {
	if !c.profile.is("official") {
		return
	}
	for _, g := range t.captures(koAfternoonRe) {
		if !g[0].OK || !g[1].OK || !g[2].OK {
			continue
		}
		hour, _ := strconv.Atoi(t.slice(g[2].A, g[2].B))
		if t.slice(g[1].A, g[1].B) == "오후" && hour < 12 {
			hour += 12
		}
		minute := 0
		if g[3].OK {
			minute, _ = strconv.Atoi(t.slice(g[3].A, g[3].B))
		}
		o.fix("ko.official.format", g[0].A, g[0].B, fmt.Sprintf("%02d:%02d", hour, minute), "공문서의 시각은 24시각제로 ‘15:00’처럼 씁니다.")
	}
}
