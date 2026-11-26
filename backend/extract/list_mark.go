package extract

import (
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// listMark is a list item's Markdown mark from the marker a word processor
// wrote: 3., (3), 3、, 1.3., c), iii. and 三、 are item 3, anything else (a
// bullet character, a word) a bullet.
func listMark(marker string) string {
	marker = strings.TrimSpace(marker)
	stem := strings.TrimRight(strings.TrimLeft(marker, "(（"), ".)）、")
	if n, ok := itemNumber(stem); ok && len(stem) < len(marker) {
		return strconv.Itoa(n) + "."
	}
	return "-"
}

// itemNumber is what a list marker's stem counts to: digits (the last of
// 1.3), a Roman numeral, a letter or a run of one (Word's aa after z), or
// a Chinese numeral below a hundred.
func itemNumber(stem string) (int, bool) {
	parts := strings.Split(stem, ".")
	numbers := true
	for _, p := range parts {
		if _, ok := wholeNumber(p); !ok {
			numbers = false
		}
	}
	if numbers {
		return wholeNumber(parts[len(parts)-1])
	}
	if n, ok := chineseNumber(stem); ok {
		return n, true
	}
	// i, ii, iv, xi... are Roman far more often than letters past h.
	if strings.Trim(stem, "ivxIVX") == "" {
		return roman(stem)
	}
	first, _ := utf8.DecodeRuneInString(stem)
	if isASCIILetter(first) && strings.Trim(stem, string(first)) == "" {
		return (len(stem)-1)*26 + int(unicode.ToLower(first)-'a') + 1, true
	}
	return roman(stem)
}

// wholeNumber is a run of digits, with the + a number may carry.
func wholeNumber(s string) (int, bool) {
	n, err := strconv.ParseUint(strings.TrimPrefix(s, "+"), 10, 32)
	return int(n), err == nil
}

func isASCIILetter(c rune) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }

// chineseNumber: 三 is 3, 十一 is 11, 二十 is 20, 九十九 is 99.
func chineseNumber(stem string) (int, bool) {
	digit := func(c rune) (int, bool) {
		for i, d := range []rune("一二三四五六七八九") {
			if d == c {
				return i + 1, true
			}
		}
		return 0, false
	}
	c := []rune(stem)
	switch {
	case len(c) == 1 && c[0] == '十':
		return 10, true
	case len(c) == 1:
		return digit(c[0])
	case len(c) == 2 && c[0] == '十':
		one, ok := digit(c[1])
		return 10 + one, ok
	case len(c) == 2 && c[1] == '十':
		tens, ok := digit(c[0])
		return 10 * tens, ok
	case len(c) == 3 && c[1] == '十':
		tens, ok1 := digit(c[0])
		one, ok2 := digit(c[2])
		return 10*tens + one, ok1 && ok2
	}
	return 0, false
}

// roman is a Roman numeral as a word processor writes it, in either case.
// iiii, vx and ic add up to 4, 5 and 99 but aren't numerals, so they count
// as nothing, as 十十 does.
func roman(stem string) (int, bool) {
	values := map[byte]int{'i': 1, 'v': 5, 'x': 10, 'l': 50, 'c': 100, 'd': 500, 'm': 1000}
	digits := make([]int, 0, len(stem))
	for i := 0; i < len(stem); i++ {
		c := stem[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		v, ok := values[c]
		if !ok {
			return 0, false
		}
		digits = append(digits, v)
	}
	total := 0
	for k, d := range digits {
		if k+1 < len(digits) && digits[k+1] > d {
			total -= d
		} else {
			total += d
		}
	}
	if total <= 0 || !strings.EqualFold(toRoman(total), stem) {
		return 0, false
	}
	return total, true
}

// toRoman: 4 is iv, 14 xiv, 40 xl.
func toRoman(n int) string {
	numerals := []struct {
		value   int
		numeral string
	}{
		{1000, "m"}, {900, "cm"}, {500, "d"}, {400, "cd"}, {100, "c"}, {90, "xc"},
		{50, "l"}, {40, "xl"}, {10, "x"}, {9, "ix"}, {5, "v"}, {4, "iv"}, {1, "i"},
	}
	var out strings.Builder
	for _, r := range numerals {
		for n >= r.value {
			out.WriteString(r.numeral)
			n -= r.value
		}
	}
	return out.String()
}
