package backend

import (
	"bytes"
	"fmt"
)

// pdfEntry is where an object is: at an offset of the file, or in an
// object stream.
type pdfEntry struct {
	offset int
	inside int // the object stream holding it; 0 for none
	index  int
	gen    int
	free   bool
}

// pdfFile is a PDF read for an incremental update: its objects by number,
// as the newest cross-reference section says, and the newest trailer.
type pdfFile struct {
	data       []byte
	xref       map[int]pdfEntry
	trailer    pdfValue
	start      int  // the newest section's offset (startxref)
	xrefStream bool // the newest section is a cross-reference stream
	streams    map[int]objStream
}

// objStream is an object stream's decoded data, and where its objects
// start after the header.
type objStream struct {
	data  []byte
	first int
}

func readPDF(data []byte) (*pdfFile, error) {
	at := bytes.LastIndex(data, []byte("startxref"))
	if at < 0 || !bytes.HasPrefix(data, []byte("%PDF-")) {
		return nil, errPDF
	}
	p := &pdfParser{data: data, pos: at + len("startxref")}
	start, err := p.int()
	if err != nil {
		return nil, err
	}
	f := &pdfFile{data: data, xref: map[int]pdfEntry{}, start: start, streams: map[int]objStream{}}
	seen := map[int]bool{}
	for next, first := start, true; next > 0; first = false {
		if seen[next] || next >= len(data) || len(seen) > 64 {
			return nil, errPDF
		}
		seen[next] = true
		trailer, isStream, err := f.section(next)
		if err != nil {
			return nil, err
		}
		if first {
			f.trailer, f.xrefStream = trailer, isStream
		}
		// A hybrid file's table names a stream for its compressed objects.
		if stm, ok := trailer.get("XRefStm"); ok {
			if at, ok := stm.integer(); ok && !seen[at] {
				seen[at] = true
				if _, _, err := f.section(at); err != nil {
					return nil, err
				}
			}
		}
		prev, ok := trailer.get("Prev")
		if !ok {
			break
		}
		if next, ok = prev.integer(); !ok {
			return nil, errPDF
		}
	}
	if _, ok := f.trailer.get("Encrypt"); ok {
		return nil, errPDF
	}
	return f, nil
}

// add records an entry unless a newer section had the object already.
func (f *pdfFile) add(n int, e pdfEntry) {
	if _, ok := f.xref[n]; !ok {
		f.xref[n] = e
	}
}

// section reads the cross-reference section at offset: a table and its
// trailer, or a stream, whose dictionary is the trailer.
func (f *pdfFile) section(offset int) (pdfValue, bool, error) {
	p := &pdfParser{data: f.data, pos: offset}
	back := p.pos
	if p.word() != "xref" {
		p.pos = back
		return f.xrefStreamAt(offset)
	}
	for {
		back = p.pos
		w := p.word()
		if w == "trailer" {
			break
		}
		p.pos = back
		first, err := p.int()
		if err != nil {
			return pdfValue{}, false, err
		}
		count, err := p.int()
		if err != nil || count < 0 || count > 1<<24 {
			return pdfValue{}, false, errPDF
		}
		for i := range count {
			offset, err1 := p.int()
			gen, err2 := p.int()
			kind := p.word()
			if err1 != nil || err2 != nil || (kind != "n" && kind != "f") {
				return pdfValue{}, false, errPDF
			}
			f.add(first+i, pdfEntry{offset: offset, gen: gen, free: kind == "f"})
		}
	}
	trailer, err := p.value(0)
	if err != nil || trailer.kind != pdfDict {
		return pdfValue{}, false, errPDF
	}
	return trailer, false, nil
}

func (f *pdfFile) xrefStreamAt(offset int) (pdfValue, bool, error) {
	_, dict, data, err := f.objectAt(f.data, offset, true)
	if err != nil {
		return pdfValue{}, false, err
	}
	if t, _ := dict.get("Type"); !t.named("XRef") {
		return pdfValue{}, false, errPDF
	}
	widths, _ := dict.get("W")
	if len(widths.list) != 3 {
		return pdfValue{}, false, errPDF
	}
	var w [3]int
	for i, v := range widths.list {
		if w[i], _ = v.integer(); w[i] < 0 || w[i] > 8 {
			return pdfValue{}, false, errPDF
		}
	}
	index := []int{0, 0}
	if size, ok := dict.get("Size"); ok {
		index[1], _ = size.integer()
	}
	if idx, ok := dict.get("Index"); ok {
		index = index[:0]
		for _, v := range idx.list {
			n, _ := v.integer()
			index = append(index, n)
		}
	}
	row := w[0] + w[1] + w[2]
	field := func(b []byte) int {
		n := 0
		for _, c := range b {
			n = n<<8 | int(c)
		}
		return n
	}
	pos := 0
	for i := 0; i+1 < len(index); i += 2 {
		for n := index[i]; n < index[i]+index[i+1]; n++ {
			if pos+row > len(data) {
				return pdfValue{}, false, errPDF
			}
			kind := 1
			if w[0] > 0 {
				kind = field(data[pos : pos+w[0]])
			}
			a, b := field(data[pos+w[0]:pos+w[0]+w[1]]), field(data[pos+w[0]+w[1]:pos+row])
			pos += row
			switch kind {
			case 0:
				f.add(n, pdfEntry{free: true})
			case 1:
				f.add(n, pdfEntry{offset: a, gen: b})
			case 2:
				f.add(n, pdfEntry{inside: a, index: b})
			}
		}
	}
	return dict, true, nil
}

// objectAt parses "n g obj" at offset of data: its value, and with
// stream its decoded stream.
func (f *pdfFile) objectAt(data []byte, offset int, stream bool) (int, pdfValue, []byte, error) {
	p := &pdfParser{data: data, pos: offset}
	n, err := p.int()
	if err != nil {
		return 0, pdfValue{}, nil, err
	}
	if _, err := p.int(); err != nil {
		return 0, pdfValue{}, nil, err
	}
	if err := p.expect("obj"); err != nil {
		return 0, pdfValue{}, nil, err
	}
	v, err := p.value(0)
	if err != nil || !stream {
		return n, v, nil, err
	}
	if v.kind != pdfDict || p.word() != "stream" {
		return 0, pdfValue{}, nil, errPDF
	}
	// The data starts after the end of the line: CRLF or LF.
	if p.pos < len(data) && data[p.pos] == '\r' {
		p.pos++
	}
	if p.pos < len(data) && data[p.pos] == '\n' {
		p.pos++
	}
	length := -1
	if l, ok := v.get("Length"); ok {
		if l.kind == pdfRef {
			l, _ = f.object(l.num)
		}
		length, _ = l.integer()
	}
	if length < 0 || p.pos+length > len(data) {
		end := bytes.Index(data[p.pos:], []byte("endstream"))
		if end < 0 {
			return 0, pdfValue{}, nil, errPDF
		}
		length = end
	}
	decoded, err := pdfDecode(v, data[p.pos:p.pos+length])
	return n, v, decoded, err
}

// object is object n as the file now has it; null for a free or missing
// one, as a reader takes it.
func (f *pdfFile) object(n int) (pdfValue, error) {
	e, ok := f.xref[n]
	switch {
	case !ok || e.free:
		return pdfValue{kind: pdfNull}, nil
	case e.inside == 0:
		got, v, _, err := f.objectAt(f.data, e.offset, false)
		if err == nil && got != n {
			return pdfValue{}, fmt.Errorf("%w: object %d isn't at its offset", errPDF, n)
		}
		return v, err
	}
	stm, ok := f.streams[e.inside]
	if !ok {
		var err error
		if stm, err = f.objStreamOf(e.inside); err != nil {
			return pdfValue{}, err
		}
		f.streams[e.inside] = stm
	}
	p := &pdfParser{data: stm.data}
	// The header: pairs of object number and offset after First.
	for i := 0; i <= e.index; i++ {
		num, err1 := p.int()
		at, err2 := p.int()
		if err1 != nil || err2 != nil {
			return pdfValue{}, errPDF
		}
		if i == e.index {
			if num != n || stm.first+at > len(stm.data) {
				return pdfValue{}, errPDF
			}
			q := &pdfParser{data: stm.data, pos: stm.first + at}
			return q.value(0)
		}
	}
	return pdfValue{}, errPDF
}

// objStreamOf reads object stream n.
func (f *pdfFile) objStreamOf(n int) (objStream, error) {
	holder, found := f.xref[n]
	if !found || holder.inside != 0 || holder.free {
		return objStream{}, errPDF
	}
	_, dict, decoded, err := f.objectAt(f.data, holder.offset, true)
	if err != nil {
		return objStream{}, err
	}
	if t, _ := dict.get("Type"); !t.named("ObjStm") {
		return objStream{}, errPDF
	}
	first, _ := dict.get("First")
	at, ok := first.integer()
	if !ok || at < 0 || at > len(decoded) {
		return objStream{}, errPDF
	}
	return objStream{data: decoded, first: at}, nil
}

// resolve follows a reference.
func (f *pdfFile) resolve(v pdfValue) (pdfValue, error) {
	for range 16 {
		if v.kind != pdfRef {
			return v, nil
		}
		var err error
		if v, err = f.object(v.num); err != nil {
			return pdfValue{}, err
		}
	}
	return pdfValue{}, errPDF
}

// size is the first object number no object has.
func (f *pdfFile) size() int {
	size := 0
	if s, ok := f.trailer.get("Size"); ok {
		size, _ = s.integer()
	}
	for n := range f.xref {
		size = max(size, n+1)
	}
	return size
}
