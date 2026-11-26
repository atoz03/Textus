package extract

import (
	"errors"
	"testing"
)

func TestTextEditRTFReadsAsMarkdown(t *testing.T) {
	// What TextEdit saves: a font table, \uc0 with \u for anything past
	// ASCII, and list markers in \listtext groups.
	rtf := `{\rtf1\ansi\ansicpg1252\cocoartf2761
{\fonttbl\f0\fswiss\fcharset0 Helvetica;}
{\colortbl;\red255\green255\blue255;}
\pard\f0\fs24 Plain, {\b bold} and \i italic\i0  \'93quoted\'94.\par
\pard\outlinelevel0 Chapter One\par
\pard{\listtext\uc0\u8226 	}First item\par
{\listtext	\uc0\u8226 	}Second \uc0\u20320 \u22909 \par
\pard After {\field{\*\fldinst{HYPERLINK "https://example.com/"}}{\fldrslt the link}}.\par
\pard See {\field{\*\fldinst{HYPERLINK \\l "_Toc1"}}{\fldrslt Contents}}.\par
\pard{\listtext 3.\tab}Third\par
\pard{\listtext a)\tab}Lettered\par
}`
	expect(t, rtfToMarkdown([]byte(rtf)),
		"Plain, **bold** and *italic* “quoted”.\n\n# Chapter One\n\n- First item\n- Second 你好\n\nAfter [the link](https://example.com/).\n\nSee Contents.\n\n3. Third\n1. Lettered")
}

func TestUnicodeFallbacksAreSkippedAndCodePagesDecoded(t *testing.T) {
	// Word writes \u with a one-character fallback (\uc1) for old readers;
	// reading both would double every character. \'c4\'e3 is GBK for 你 in
	// a document declaring code page 936.
	rtf := `{\rtf1\ansi\ansicpg936 caf\u233?, \'c4\'e3, \u-10179?\u-8704?\par}`
	expect(t, rtfToMarkdown([]byte(rtf)), "café, 你, \U0001f600")
}

func TestFootnotesGoToTheEnd(t *testing.T) {
	// Word's form and pandoc's, which marks the group ignorable.
	rtf := `{\rtf1 Claim{\super\chftn}{\footnote\pard\plain{\super\chftn} Smith, 1999.} and{\*\footnote\chftn\pard Ibid.}.\par}`
	expect(t, rtfToMarkdown([]byte(rtf)), "Claim[^1] and[^2].\n\n[^1]: Smith, 1999.\n\n[^2]: Ibid.")
}

func TestHeadingStylesAndTablesAreKept(t *testing.T) {
	// LibreOffice's RTF: headings by style number, cells ended by \cell and
	// rows by \row.
	rtf := `{\rtf1{\stylesheet{\s0 Normal;}{\s1\sbasedon0\snext2 heading 1;}{\*\cs15 Strong;}}
\pard\plain\s1 Chapter {\b One}\par
\pard\plain\s0 Text.\par
\trowd\cellx1000\cellx2000
\pard\intbl Name\cell\pard\intbl Role\cell\row
\pard\intbl Maria\cell\pard\intbl Volunteer\par\pard\intbl weekends\cell\row
\pard After.\par}`
	expect(t, rtfToMarkdown([]byte(rtf)),
		"# Chapter One\n\nText.\n\n| Name | Role |\n| --- | --- |\n| Maria | Volunteer<br>weekends |\n\nAfter.")
}

func TestBinaryDataAndSkippedGroupsAreNoText(t *testing.T) {
	// \bin's bytes may hold braces; a picture's hex is no text either.
	rtf := `{\rtf1 Before {\pict\pngblip 89504e47}\bin3 {}}x after.\par}`
	expect(t, rtfToMarkdown([]byte(rtf)), "Before x after.")
}

func TestAFileThatIsNotRTFSaysSo(t *testing.T) {
	_, err := RtfMarkdown(writeFile(t, "x.rtf", []byte("plain text")))
	if !errors.Is(err, ErrUnreadable) {
		t.Errorf("%v", err)
	}
}

func TestListMarkersCountAsWhatTheyStandFor(t *testing.T) {
	for marker, want := range map[string]string{
		"3.": "3.", "(3)": "3.", "3)": "3.", "3、": "3.", "（3）": "3.", "1.3.": "3.", "c.": "3.",
		"C)": "3.", "iii.": "3.", "IV)": "4.", "cc.": "29.", "三、": "3.", "•": "-", "o": "-",
		"-": "-", "Note": "-", "ab.": "-", "十、": "10.", "十一、": "11.", "二十、": "20.",
		"九十九、": "99.", "十十、": "-",
		// Roman markers count in either case.
		"i.": "1.", "iv.": "4.", "ix.": "9.", "xiv.": "14.", "xl.": "40.", "I)": "1.", "IX)": "9.",
		"XIV)": "14.", "XL)": "40.",
		// A malformed Roman marker is a bullet.
		"iiii.": "-", "vx.": "-", "ic.": "-", "IIII)": "-", "VX)": "-", "IC)": "-",
	} {
		if got := listMark(marker); got != want {
			t.Errorf("%q: %q, want %q", marker, got, want)
		}
	}
}
