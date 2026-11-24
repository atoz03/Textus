package extract

import (
	"strconv"
	"strings"
)

// A Word file's paragraph and character styles, followed through their
// basedOn chain (which are headings, quotes, bold or italic), and its list
// numbering (which numbered paragraphs are ordered lists, which bullets).

type style struct {
	// name is lower case: the built-in names ("heading 1") are stored in
	// English whatever Word's language, while style ids are localized.
	name    string
	basedOn string
	outline int
	list    listRef
}

type styles map[string]style

func parseStyles(data []byte) styles {
	out := styles{}
	x := newPull(data)
	for n := x.next(); n.kind != nodeEnd; n = x.next() {
		if n.kind == nodeOpen && n.tag.name == "style" {
			out[n.tag.val("styleId")] = readStyle(x)
		}
	}
	return out
}

func readStyle(x *pull) style {
	var s style
	level, depth := 0, 0
	for {
		n := x.next()
		switch n.kind {
		case nodeOpen:
			switch t := n.tag; t.name {
			case "name":
				s.name = strings.ToLower(t.val("val"))
			case "basedOn":
				s.basedOn = t.val("val")
			case "outlineLvl":
				s.outline = headingOfOutline(t)
			case "ilvl":
				level, _ = count(t, "val")
			case "numId":
				if id, ok := t.attr("val"); ok {
					s.list = listRef{level, id, true}
				}
			}
			depth++
		case nodeClose:
			if depth == 0 {
				return s
			}
			depth--
		case nodeEnd:
			return s
		}
	}
}

func (s styles) name(id string) string { return s[id].name }

// chain is the styles id is, from itself down the basedOn chain - bounded,
// since nothing stops a file from basing two styles on each other.
func (s styles) chain(id string) []style {
	var out []style
	for len(out) < 8 {
		st, ok := s[id]
		if !ok {
			break
		}
		out = append(out, st)
		id = st.basedOn
	}
	return out
}

// heading is the level of a built-in heading style, or one based on it;
// 0 for none. "Title" only as itself: pandoc's "Author" and "Date" are
// based on it.
func (s styles) heading(id string) int {
	if s.name(id) == "title" {
		return 1
	}
	for _, st := range s.chain(id) {
		if named, ok := strings.CutPrefix(st.name, "heading "); ok {
			if level, err := strconv.Atoi(strings.TrimSpace(named)); err == nil && level >= 1 && level <= 9 {
				return level
			}
		}
		if st.outline > 0 {
			return st.outline
		}
	}
	return 0
}

// isQuote is Word's "Quote" and "Intense Quote", and the "Block Text"
// pandoc and older templates use.
func (s styles) isQuote(id string) bool {
	for _, st := range s.chain(id) {
		switch st.name {
		case "quote", "intense quote", "block text":
			return true
		}
	}
	return false
}

func (s styles) numbering(id string) listRef {
	for _, st := range s.chain(id) {
		if st.list.ok {
			return st.list
		}
	}
	return listRef{}
}

type levelKey struct {
	id    string
	level int
}

// numbering says which list levels are bullets, and what number each
// numbered item shows, with the numId's own start overrides on top.
type numbering struct {
	abstractOf map[string]string
	formats    map[levelKey]string // by abstractNumId and level: w:numFmt
	starts     map[levelKey]int    // by abstractNumId and level: w:start
	// startOverrides, by numId and level: Word's "Restart at 1" is a new
	// numId on the same definition, with this.
	startOverrides map[levelKey]int
	// counters are the number last shown at each level of each numId (-1:
	// none yet), counted as the body is read.
	counters map[string][]int
}

func parseNumbering(data []byte) *numbering {
	n := &numbering{
		abstractOf: map[string]string{}, formats: map[levelKey]string{}, starts: map[levelKey]int{},
		startOverrides: map[levelKey]int{}, counters: map[string][]int{},
	}
	x := newPull(data)
	abstractID, numID, level := "", "", 0
	for node := x.next(); node.kind != nodeEnd; node = x.next() {
		if node.kind != nodeOpen {
			continue
		}
		switch t := node.tag; t.name {
		case "abstractNum":
			abstractID = t.val("abstractNumId")
		case "lvl":
			level, _ = count(t, "ilvl")
		case "numFmt":
			n.formats[levelKey{abstractID, level}] = t.val("val")
		case "start":
			if start, ok := count(t, "val"); ok {
				n.starts[levelKey{abstractID, level}] = start
			}
		case "num":
			numID = t.val("numId")
		case "abstractNumId":
			n.abstractOf[numID] = t.val("val")
		case "lvlOverride":
			// A level override may restate a level's format too; the
			// abstract definition is close enough to say "bullet".
			over, _ := count(t, "ilvl")
			for child := x.next(); child.kind == nodeOpen || child.kind == nodeText; child = x.next() {
				if child.kind != nodeOpen {
					continue
				}
				if child.tag.name == "startOverride" {
					if start, ok := count(child.tag, "val"); ok {
						n.startOverrides[levelKey{numID, over}] = start
					}
				}
				x.skip()
			}
		}
	}
	return n
}

// isBullet: without a numbering part, or for a level it doesn't define, a
// list is taken to be bulleted - "1." would claim an order the file never
// gave.
func (n *numbering) isBullet(numID string, level int) bool {
	format, ok := "", false
	if abstract, found := n.abstractOf[numID]; found {
		format, ok = n.formats[levelKey{abstract, level}]
	}
	return !ok || format == "bullet" || format == "none"
}

// nextNumber is the number the next item at level of list numID shows,
// counted as Word counts, so a list starting at 5 or resuming reads as in
// Word.
func (n *numbering) nextNumber(numID string, level int) int {
	// Word has nine levels; a file saying more is held to them.
	level = min(level, 8)
	start, ok := n.startOverrides[levelKey{numID, level}]
	if !ok {
		start, ok = n.starts[levelKey{n.abstractOf[numID], level}]
		if _, defined := n.abstractOf[numID]; !defined || !ok {
			start = 1
		}
	}
	// The levels below this one start again.
	counters := n.counters[numID]
	for len(counters) < level+1 {
		counters = append(counters, -1)
	}
	counters = counters[:level+1]
	number := start
	if counters[level] >= 0 {
		number = counters[level] + 1
	}
	counters[level] = number
	n.counters[numID] = counters
	return number
}
