package ai

import (
	"fmt"
	"slices"
	"strings"
)

// What this request's proposals have done so far: the text proposed, the
// pictures in it, and the passages a later proposal may not overlap.

// ProposalLedger is what this request's propose_edit calls have done.
type ProposalLedger struct {
	// passages are the passages proposals already replace or delete.
	passages []ProposedPassage
	// refusedShort are replacements refused once as looking cut off, as
	// `anchor\x00text`: the same call sent again is meant, and goes
	// through.
	refusedShort []string
	// texts are the new text of each proposal made: a picture may go next
	// to a paragraph of it, though it isn't in the document yet.
	texts []string
	// pictures are the pictures this request wrote into its text, by id.
	pictures []string
	// unmade are why pictures written into the text were left out of it,
	// for propose_edit's result to say.
	unmade []string
}

func (l *ProposalLedger) proposedAny() bool { return len(l.passages) > 0 || len(l.texts) > 0 }

// noteUnmade records that a picture written into the text was left out,
// and why.
func (l *ProposalLedger) noteUnmade(why string) {
	if !slices.Contains(l.unmade, why) {
		l.unmade = append(l.unmade, why)
	}
}

// picturesPlaced is what becomes of the picture lines in text, for its
// result: each now reads `textus-generated:ID`. Unexplained, models took
// that for an invented id and spent the request trying to fix it.
func (l *ProposalLedger) picturesPlaced(text string) string {
	placed := 0
	for _, id := range l.pictures {
		if strings.Contains(text, PlaceholderSrc(id)) {
			placed++
		}
	}
	switch placed {
	case 0:
		return ""
	case 1:
		return " Its picture line is being made into its picture: in the text it now reads `textus-generated:` and the picture's id, which Textus wrote - nothing to fix."
	}
	return fmt.Sprintf(" Its %d picture lines are being made into their pictures: in the text each now reads `textus-generated:` and its picture's id, which Textus wrote - nothing to fix.", placed)
}

// takeUnmade is what the pictures left out of the text since last asked
// came to, "" for none.
func (l *ProposalLedger) takeUnmade() string {
	if len(l.unmade) == 0 {
		return ""
	}
	why := strings.Join(l.unmade, " ")
	l.unmade = nil
	return " A picture line was left out of the text: " + why
}

// picturesProblem is why text's pictures can't stand: one put in twice, or
// one this request never asked for - a long id copied wrong drew one
// picture in two places. "" when they can, or none were asked for.
func (l *ProposalLedger) picturesProblem(text string) string {
	if len(l.pictures) == 0 {
		return ""
	}
	const marker = "textus-generated:"
	var used []string
	rest := text
	for {
		at := strings.Index(rest, marker)
		if at < 0 {
			return ""
		}
		rest = rest[at+len(marker):]
		end := strings.IndexFunc(rest, func(c rune) bool {
			return !(c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z')
		})
		if end < 0 {
			end = len(rest)
		}
		id := rest[:end]
		if !slices.Contains(l.pictures, id) {
			return fmt.Sprintf("Not proposed: `textus-generated:%s` is no picture of this request. Its pictures are: %s.", id, strings.Join(l.pictures, ", "))
		}
		if slices.Contains(used, id) {
			var left []string
			for _, p := range l.pictures {
				if !slices.Contains(used, p) && p != id {
					left = append(left, p)
				}
			}
			notYet := ""
			if len(left) > 0 {
				notYet = "; not placed yet: " + strings.Join(left, ", ")
			}
			return fmt.Sprintf("Not proposed: the picture `%s` is in the text twice. Each picture goes in once%s - send the text again with each in its place.", id, notYet)
		}
		used = append(used, id)
	}
}

// ProposedPassage is a passage one of this request's proposals replaces or
// deletes: its span when the quote is found verbatim, and the quoted text.
// Two overlap when their spans intersect or, unplaced, when one quote
// contains the other.
type ProposedPassage struct {
	// span is the byte range in the document; placed when it is known.
	span   [2]int
	placed bool
	// quote is the context if it pins the anchor, else the anchor; "" for
	// the selection.
	quote string
}

// passageAt is the passage at from-to (TargetSpan; placed false when
// unknown), which reads quote.
func passageAt(from, to int, placed bool, quote string) ProposedPassage {
	return ProposedPassage{span: [2]int{from, to}, placed: placed, quote: quote}
}

func (p ProposedPassage) overlaps(o ProposedPassage) bool {
	if p.placed && o.placed {
		return p.span[0] < o.span[1] && o.span[0] < p.span[1]
	}
	a, b := p.quote, o.quote
	return (a == "" && b == "") || (a != "" && b != "" && (strings.Contains(a, b) || strings.Contains(b, a)))
}
