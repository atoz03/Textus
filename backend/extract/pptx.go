package extract

import (
	"archive/zip"
	"cmp"
	"errors"
	"path"
	"slices"
	"strconv"
	"strings"
)

// PowerPoint (.pptx) to Markdown: one "## Slide N: title" per slide, then
// its shapes in reading order (top to bottom, then left to right, not
// z-order) and notes under "### Notes", as MarkItDown (MIT) does.

// PptxMarkdown is the Markdown of a .pptx; "" when no slide has text.
func PptxMarkdown(file string) (string, error) {
	z, err := zip.OpenReader(file)
	if err != nil {
		return "", errors.New("This .pptx is not a readable PowerPoint file.")
	}
	defer z.Close()

	// Slides sort wrong as strings (slide10 before slide2), so by number.
	type numbered struct {
		number int
		name   string
	}
	var slides []numbered
	for _, f := range z.File {
		rest, ok := strings.CutPrefix(f.Name, "ppt/slides/slide")
		digits, xml := strings.CutSuffix(rest, ".xml")
		number, err := strconv.ParseUint(digits, 10, 31)
		if ok && xml && err == nil {
			slides = append(slides, numbered{int(number), f.Name})
		}
	}
	slices.SortFunc(slides, func(a, b numbered) int {
		return cmp.Or(cmp.Compare(a.number, b.number), cmp.Compare(a.name, b.name))
	})

	var out []string
	for _, s := range slides {
		data, ok := zipEntry(&z.Reader, s.name)
		if !ok {
			continue
		}
		relsData, _ := zipEntry(&z.Reader, "ppt/slides/_rels/"+path.Base(s.name)+".rels")
		r := parseRels(relsData)
		title, titled, body := readSlide(data, r, true)
		notes := ""
		if target, ok := r.ofKind("notesSlide"); ok {
			if data, ok := zipEntry(&z.Reader, resolvePart("ppt/slides", target)); ok {
				_, _, notes = readSlide(data, nil, false)
			}
		}
		if !titled && body == "" && notes == "" {
			continue
		}
		slide := "## Slide " + strconv.Itoa(s.number)
		if titled {
			slide += ": " + title
		}
		if body != "" {
			slide += "\n\n" + body
		}
		if notes != "" {
			slide += "\n\n### Notes\n\n" + notes
		}
		out = append(out, slide)
	}
	return strings.Join(out, "\n\n"), nil
}
