package backend

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"slices"
	"strconv"
)

// Page numbers for PDF export, stamped onto the finished file: no CSS
// route numbers pages in every webview, but every export ends with an
// empty band of about 20 mm at the bottom to draw in. The numbers go in an
// incremental update, so the renderer's bytes stay as they were.

const (
	// pageNumberFont is the resource name of the numbers' font, unusual
	// enough not to collide with a renderer's own /F1, /TT2, ...
	pageNumberFont = "TextusPageNo"
	// Small, like a book's folio.
	pageNumberSize = 9.0
	// The baseline above the sheet's bottom edge: about 10 mm, the middle
	// of the bottom band.
	pageNumberBaseline = 28.0
	// Every digit of Helvetica is 556/1000 em wide, so a number is centred
	// from its length alone.
	digitWidth = 0.556
)

// pdfPage is a page and the Pages nodes above it, nearest first, which it
// inherits Resources, MediaBox and CropBox from.
type pdfPage struct {
	num, gen  int
	dict      pdfValue
	ancestors []pdfValue
}

// pages lists the document's pages in order.
func (f *pdfFile) pages() ([]pdfPage, error) {
	root, ok := f.trailer.get("Root")
	if !ok {
		return nil, errPDF
	}
	catalog, err := f.resolve(root)
	if err != nil {
		return nil, err
	}
	tree, ok := catalog.get("Pages")
	if !ok || tree.kind != pdfRef {
		return nil, errPDF
	}
	var out []pdfPage
	seen := map[int]bool{}
	var walk func(ref pdfValue, ancestors []pdfValue) error
	walk = func(ref pdfValue, ancestors []pdfValue) error {
		if ref.kind != pdfRef || seen[ref.num] || len(ancestors) > 64 || len(seen) > 1<<20 {
			return errPDF
		}
		seen[ref.num] = true
		node, err := f.object(ref.num)
		if err != nil || node.kind != pdfDict {
			return errPDF
		}
		kids, hasKids := node.get("Kids")
		if t, _ := node.get("Type"); t.named("Pages") || (hasKids && !t.named("Page")) {
			kids, err := f.resolve(kids)
			if err != nil {
				return err
			}
			inner := append([]pdfValue{node}, ancestors...)
			for _, kid := range kids.list {
				if err := walk(kid, inner); err != nil {
					return err
				}
			}
			return nil
		}
		out = append(out, pdfPage{num: ref.num, gen: ref.gen, dict: node, ancestors: ancestors})
		return nil
	}
	return out, walk(tree, nil)
}

// inherited is a page attribute looked up the tree as a reader does it.
func (f *pdfFile) inherited(p pdfPage, key string) (pdfValue, bool) {
	for _, node := range append([]pdfValue{p.dict}, p.ancestors...) {
		if v, ok := node.get(key); ok {
			v, err := f.resolve(v)
			return v, err == nil
		}
	}
	return pdfValue{}, false
}

// pageBox is the visible area of a page: its crop box, else its media box.
// On macOS these needn't start at 0,0.
func (f *pdfFile) pageBox(p pdfPage) ([4]float64, error) {
	box, ok := f.inherited(p, "CropBox")
	if !ok || box.kind != pdfArray {
		box, ok = f.inherited(p, "MediaBox")
	}
	if !ok || box.kind != pdfArray || len(box.list) != 4 {
		return [4]float64{}, fmt.Errorf("%w: a page has no size", errPDF)
	}
	var c [4]float64
	for i, v := range box.list {
		v, err := f.resolve(v)
		if err != nil {
			return c, err
		}
		if c[i], ok = v.number(); !ok {
			return c, errPDF
		}
	}
	return [4]float64{min(c[0], c[2]), min(c[1], c[3]), max(c[0], c[2]), max(c[1], c[3])}, nil
}

// pageResources is a copy of the page's resources with the numbers' font
// added: resources shared with other pages stay as they were.
func (f *pdfFile) pageResources(p pdfPage, font int) (pdfValue, error) {
	resources, ok := f.inherited(p, "Resources")
	if !ok || resources.kind != pdfDict {
		resources = pdfValue{kind: pdfDict}
	}
	fonts := pdfValue{kind: pdfDict}
	if v, ok := resources.get("Font"); ok {
		v, err := f.resolve(v)
		if err != nil {
			return pdfValue{}, err
		}
		if v.kind == pdfDict {
			fonts = v
		}
	}
	return resources.with("Font", fonts.with(pageNumberFont, pdfRefTo(font))), nil
}

// pageStamp draws number centred in the bottom band of box, after a Q
// that ends the q the page's own content was put inside, so whatever state
// the renderer left can't move or tint it.
func pageStamp(number int, box [4]float64) string {
	label := strconv.Itoa(number)
	width := digitWidth * pageNumberSize * float64(len(label))
	x := (box[0] + box[2] - width) / 2
	y := box[1] + pageNumberBaseline
	return fmt.Sprintf("Q\nq\nBT\n/%s %s Tf\n0.5 g\n1 0 0 1 %.2f %.2f Tm\n(%s) Tj\nET\nQ\n",
		pageNumberFont, strconv.FormatFloat(pageNumberSize, 'f', -1, 64), x, y, label)
}

// numberPages is pdf with each page's number, from 1, centred in its
// bottom band.
func numberPages(pdf []byte) ([]byte, error) {
	f, err := readPDF(pdf)
	if err != nil {
		return nil, err
	}
	pages, err := f.pages()
	if err != nil {
		return nil, err
	}
	if len(pages) == 0 {
		return nil, errPDF
	}
	u := &pdfUpdate{file: f, next: f.size()}
	// Helvetica is one of the standard fonts every reader has: nothing is
	// embedded, and WinAnsi covers the digits.
	font := u.add(0, "<</Type /Font /Subtype /Type1 /BaseFont /Helvetica /Encoding /WinAnsiEncoding>>")
	save := u.addStream("q\n")
	for i, page := range pages {
		box, err := f.pageBox(page)
		if err != nil {
			return nil, err
		}
		stamp := u.addStream(pageStamp(i+1, box))
		resources, err := f.pageResources(page, font)
		if err != nil {
			return nil, err
		}
		contents := pdfValue{kind: pdfArray, list: []pdfValue{pdfRefTo(save)}}
		if c, ok := page.dict.get("Contents"); ok {
			if c.kind == pdfRef {
				// An indirect array of streams is spread; a stream stays a reference.
				if resolved, err := f.object(c.num); err == nil && resolved.kind == pdfArray {
					c = resolved
				}
			}
			if c.kind == pdfArray {
				contents.list = append(contents.list, c.list...)
			} else {
				contents.list = append(contents.list, c)
			}
		}
		contents.list = append(contents.list, pdfRefTo(stamp))
		dict := page.dict.with("Contents", contents).with("Resources", resources)
		u.addAt(page.num, page.gen, dict.String())
	}
	return u.finish()
}

// pdfUpdate is an incremental update being written after a file.
type pdfUpdate struct {
	file    *pdfFile
	body    bytes.Buffer
	offsets map[int][2]int // object number: offset, generation
	next    int
}

func (u *pdfUpdate) addAt(n, gen int, value string) {
	if u.offsets == nil {
		u.offsets = map[int][2]int{}
		u.body.WriteByte('\n')
	}
	u.offsets[n] = [2]int{len(u.file.data) + u.body.Len(), gen}
	fmt.Fprintf(&u.body, "%d %d obj\n%s\nendobj\n", n, gen, value)
}

// add writes a new object and returns its number; n 0 takes the next.
func (u *pdfUpdate) add(n int, value string) int {
	if n == 0 {
		n = u.next
		u.next++
	}
	u.addAt(n, 0, value)
	return n
}

func (u *pdfUpdate) addStream(data string) int {
	return u.add(0, fmt.Sprintf("<</Length %d>>\nstream\n%s\nendstream", len(data), data))
}

// finish writes the cross-reference section, the same kind as the file's
// newest, and returns the updated file.
func (u *pdfUpdate) finish() ([]byte, error) {
	trailer := u.file.trailer.without("Prev", "XRefStm", "Size", "Type", "W", "Index", "Length", "Filter", "DecodeParms")
	var section bytes.Buffer
	if u.file.xrefStream {
		// The stream lists itself too.
		self := u.next
		u.next++
		at := len(u.file.data) + u.body.Len()
		u.offsets[self] = [2]int{at, 0}
		numbers := u.numbers()
		var data bytes.Buffer
		var index pdfValue
		index.kind = pdfArray
		for _, run := range runs(numbers) {
			index.list = append(index.list, pdfInt(run[0]), pdfInt(run[1]))
		}
		for _, n := range numbers {
			e := u.offsets[n]
			if e[0] > 0xffffffff {
				return nil, errPDF
			}
			data.WriteByte(1)
			_ = binary.Write(&data, binary.BigEndian, uint32(e[0]))
			_ = binary.Write(&data, binary.BigEndian, uint16(e[1]))
		}
		dict := trailer.with("Type", pdfValue{kind: pdfName, raw: pdfNameOf("XRef")}).
			with("Size", pdfInt(u.next)).
			with("W", pdfValue{kind: pdfArray, list: []pdfValue{pdfInt(1), pdfInt(4), pdfInt(2)}}).
			with("Index", index).
			with("Prev", pdfInt(u.file.start)).
			with("Length", pdfInt(data.Len()))
		fmt.Fprintf(&section, "%d 0 obj\n%s\nstream\n", self, dict)
		section.Write(data.Bytes())
		fmt.Fprintf(&section, "\nendstream\nendobj\nstartxref\n%d\n%%%%EOF\n", at)
	} else {
		at := len(u.file.data) + u.body.Len()
		section.WriteString("xref\n")
		numbers := u.numbers()
		i := 0
		for _, run := range runs(numbers) {
			fmt.Fprintf(&section, "%d %d\n", run[0], run[1])
			for range run[1] {
				e := u.offsets[numbers[i]]
				if e[0] > 9999999999 {
					return nil, errPDF
				}
				fmt.Fprintf(&section, "%010d %05d n \n", e[0], e[1])
				i++
			}
		}
		dict := trailer.with("Size", pdfInt(u.next)).with("Prev", pdfInt(u.file.start))
		fmt.Fprintf(&section, "trailer\n%s\nstartxref\n%d\n%%%%EOF\n", dict, at)
	}
	out := make([]byte, 0, len(u.file.data)+u.body.Len()+section.Len())
	out = append(out, u.file.data...)
	out = append(out, u.body.Bytes()...)
	return append(out, section.Bytes()...), nil
}

func (u *pdfUpdate) numbers() []int {
	numbers := make([]int, 0, len(u.offsets))
	for n := range u.offsets {
		numbers = append(numbers, n)
	}
	slices.Sort(numbers)
	return numbers
}

// runs groups sorted numbers into runs of consecutive ones: first, count.
func runs(numbers []int) [][2]int {
	var out [][2]int
	for _, n := range numbers {
		if last := len(out) - 1; last >= 0 && out[last][0]+out[last][1] == n {
			out[last][1]++
			continue
		}
		out = append(out, [2]int{n, 1})
	}
	return out
}
