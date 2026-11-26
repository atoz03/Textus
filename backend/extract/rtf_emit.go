package extract

import (
	"strconv"
	"strings"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/encoding/korean"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/encoding/traditionalchinese"
	"golang.org/x/text/encoding/unicode"
)

// What the RTF reader writes out: text in its group's marks, paragraphs as
// their style says (headings, list items), and tables row by row.

func (r *rtfReader) flushBytes() {
	if len(r.bytes) == 0 {
		return
	}
	text, err := r.encoding.NewDecoder().Bytes(r.bytes)
	if err != nil {
		text = r.bytes
	}
	r.bytes = r.bytes[:0]
	r.emit(strings.ToValidUTF8(string(text), "\uFFFD"))
}

func (r *rtfReader) emit(text string) {
	g := r.group()
	if g.skip {
		return
	}
	switch g.dest {
	case destBody:
		r.paragraph.push(text, g.marks, g.link)
	case destFootnote:
		if len(r.notes) > 0 {
			r.notes[len(r.notes)-1].push(text, g.marks, g.link)
		}
	case destFieldInstruction:
		r.instruction += text
	case destListText:
		r.listText += text
	case destStylesheet:
		r.styleName += text
	}
}

func (r *rtfReader) endParagraph() {
	g := r.group()
	if g.dest == destFootnote {
		r.emit(" ")
		return
	}
	line, marker := r.paragraph, strings.TrimSpace(r.listText)
	r.paragraph, r.listText = inline{}, ""
	if g.para.inTable {
		r.cell += line.render() + "\n"
		return
	}
	r.endTable()
	heading := g.para.heading
	if name, ok := r.styles[g.para.style]; heading == 0 && ok && g.para.hasStyle {
		heading = headingOfStyle(name)
	}
	switch {
	case heading > 0 && line.renderPlain() != "":
		r.out.push(strings.Repeat("#", heading) + " " + line.renderPlain())
	case marker != "":
		r.out.pushItem(g.para.listLevel, listMark(marker), line.render())
	default:
		r.out.push(line.render())
	}
}

func (r *rtfReader) endCell() {
	r.row = append(r.row, strings.TrimSpace(r.cell+r.paragraph.render()))
	r.cell, r.paragraph = "", inline{}
}

func (r *rtfReader) endRow() {
	if len(r.row) > 0 {
		r.table = append(r.table, r.row)
	}
	r.row = nil
}

func (r *rtfReader) endTable() {
	r.endRow()
	if len(r.table) > 0 {
		r.out.push(table(r.table))
		r.table = nil
	}
}

// headingOfStyle is "heading 2" as 2, as Word and LibreOffice name their
// heading styles; 0 for a style that is no heading.
func headingOfStyle(name string) int {
	if name == "title" {
		return 1
	}
	rest, ok := strings.CutPrefix(name, "heading ")
	level, err := strconv.Atoi(strings.TrimSpace(rest))
	if !ok || err != nil || level < 1 || level > 6 {
		return 0
	}
	return level
}

// hyperlink is the URL of a HYPERLINK "https://..." field instruction.
func hyperlink(instruction string) (string, bool) {
	rest, ok := strings.CutPrefix(strings.TrimSpace(instruction), "HYPERLINK")
	rest = strings.TrimSpace(rest)
	// HYPERLINK \l "_Toc1": a place in the document itself, no URL.
	if !ok || strings.HasPrefix(rest, `\`) {
		return "", false
	}
	url, _, _ := strings.Cut(strings.TrimLeft(rest, `"`), `"`)
	url = strings.TrimSpace(url)
	return url, url != ""
}

// codePage is the encoding of a Windows code page (\ansicpgN), nil for one
// Textus doesn't know: the ones the Rust reader's codepage crate knew.
func codePage(n int) encoding.Encoding {
	if e, ok := windowsCodePages[n]; ok {
		return e
	}
	if n >= 28592 && n <= 28606 {
		return isoCodePages[n-28590]
	}
	return nil
}

var windowsCodePages = map[int]encoding.Encoding{
	65001: unicode.UTF8,
	866:   charmap.CodePage866,
	874:   charmap.Windows874,
	1250:  charmap.Windows1250,
	1251:  charmap.Windows1251,
	1252:  charmap.Windows1252,
	1253:  charmap.Windows1253,
	1254:  charmap.Windows1254,
	1255:  charmap.Windows1255,
	1256:  charmap.Windows1256,
	1257:  charmap.Windows1257,
	1258:  charmap.Windows1258,
	10000: charmap.Macintosh,
	10017: charmap.MacintoshCyrillic,
	20866: charmap.KOI8R,
	21866: charmap.KOI8U,
	// ISO-8859-1 as browsers read it.
	28591: charmap.Windows1252,
	38598: charmap.ISO8859_8I,
	932:   japanese.ShiftJIS,
	20932: japanese.EUCJP,
	50220: japanese.ISO2022JP,
	936:   simplifiedchinese.GBK,
	54936: simplifiedchinese.GB18030,
	950:   traditionalchinese.Big5,
	949:   korean.EUCKR,
	1200:  unicode.UTF16(unicode.LittleEndian, unicode.IgnoreBOM),
	1201:  unicode.UTF16(unicode.BigEndian, unicode.IgnoreBOM),
}

// isoCodePages are ISO-8859-2 to -16 by their number, as code pages
// 28592 to 28606 name them; nil for the ones there are none of.
var isoCodePages = [17]encoding.Encoding{
	2: charmap.ISO8859_2, 3: charmap.ISO8859_3, 4: charmap.ISO8859_4, 5: charmap.ISO8859_5,
	6: charmap.ISO8859_6, 7: charmap.ISO8859_7, 8: charmap.ISO8859_8, 9: charmap.ISO8859_9,
	10: charmap.ISO8859_10, 13: charmap.ISO8859_13, 14: charmap.ISO8859_14,
	15: charmap.ISO8859_15, 16: charmap.ISO8859_16,
}
