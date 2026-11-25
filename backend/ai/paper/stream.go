package paper

import (
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

// What the stages' streams show on the card as they come: the tail of the
// thinking, and strings read out of a tool call's JSON arguments while
// they still arrive - a few times a second at most, since tokens come far
// faster than a card needs to redraw.

// lastSent is when a watcher last sent, so it sends at most once every
// so often.
type lastSent struct{ at time.Time }

// due reports whether a send now would come at least every after the last.
func (l *lastSent) due(every time.Duration) bool {
	return l.at.IsZero() || time.Since(l.at) >= every
}

// now: a send is made now.
func (l *lastSent) now() { l.at = time.Now() }

const (
	// thinkingTail is how much of the planner's stream a ThinkingEvent
	// carries: its tail.
	thinkingTail = 160
	// thinkingEvery is the least time between two ThinkingEvents.
	thinkingEvery = 250 * time.Millisecond
)

// thinkingTailOf gathers a stream's fragments and sends their tail as
// ThinkingEvents.
type thinkingTailOf struct {
	send Send
	mu   sync.Mutex
	text string
	last lastSent
}

func newThinkingTail(send Send) *thinkingTailOf { return &thinkingTailOf{send: send} }

func (t *thinkingTailOf) push(fragment string) {
	t.mu.Lock()
	t.text += fragment
	// Kept bounded: only its tail is ever sent.
	if len(t.text) > thinkingTail*8 {
		t.text = tail(t.text, thinkingTail*2)
	}
	if !t.last.due(thinkingEvery) {
		t.mu.Unlock()
		return
	}
	t.last.now()
	text := tail(t.text, thinkingTail)
	t.mu.Unlock()
	if text := strings.Join(strings.Fields(text), " "); text != "" {
		t.send(ThinkingEvent{Text: text})
	}
}

// tail is the last chars characters of text.
func tail(text string, chars int) string {
	skip := utf8.RuneCountInString(text) - chars
	if skip <= 0 {
		return text
	}
	for i := range text {
		if skip == 0 {
			return text[i:]
		}
		skip--
	}
	return text
}

// afterKey is what follows a key in JSON still arriving.
type afterKey struct {
	// pending: not enough has come to tell.
	pending bool
	// other: no string value - read on from at.
	other bool
	at    int
	// text is a string's value, unescaped as far as it has come; end, just
	// past its closing quote, once it is whole (-1 before).
	text string
	end  int
}

// readAfterKey is what follows a key, text starting right after it.
func readAfterKey(text string) afterKey {
	colon := len(text) - len(strings.TrimLeftFunc(text, unicode.IsSpace))
	value, ok := strings.CutPrefix(text[colon:], ":")
	if !ok {
		return otherOrPending(text, colon)
	}
	open := len(text) - len(strings.TrimLeftFunc(value, unicode.IsSpace))
	body, ok := strings.CutPrefix(text[open:], `"`)
	if !ok {
		return otherOrPending(text, open)
	}
	unescaped, closeAt := unescapeJSON(body)
	end := -1
	if closeAt >= 0 {
		end = open + 1 + closeAt + 1
	}
	return afterKey{text: unescaped, end: end}
}

func otherOrPending(text string, at int) afterKey {
	if at == len(text) {
		return afterKey{pending: true}
	}
	return afterKey{other: true, at: at}
}

// stringValue is the string value of key in JSON still arriving, as far as
// it has come; "" before it starts.
func stringValue(json, key string) string {
	at := strings.Index(json, key)
	if at < 0 {
		return ""
	}
	found := readAfterKey(json[at+len(key):])
	if found.pending || found.other {
		return ""
	}
	return found.text
}

// unescapeJSON is a JSON string's body, cut anywhere, unescaped as far as
// it has come, and where its closing quote is once that has come (-1
// before). An escape cut in half waits for the rest; a carriage return is
// left out, as the text is shown in lines.
func unescapeJSON(body string) (string, int) {
	var out strings.Builder
	i := 0
	for i < len(body) {
		c, size := utf8.DecodeRuneInString(body[i:])
		switch c {
		case '"':
			return out.String(), i
		case '\\':
			if i+size >= len(body) {
				return out.String(), -1
			}
			escape, escSize := utf8.DecodeRuneInString(body[i+size:])
			at := i + size + escSize
			switch escape {
			case 'n':
				out.WriteByte('\n')
			case 't':
				out.WriteByte('\t')
			case 'r':
			case 'u':
				r, used, cut := escapedChar(body[at:])
				if cut {
					return out.String(), -1
				}
				if r >= 0 {
					out.WriteRune(r)
				}
				at += used
			default:
				out.WriteRune(escape)
			}
			i = at
			continue
		}
		out.WriteRune(c)
		i += size
	}
	return out.String(), -1
}

// escapedChar is the character a `\u` escape stands for, read from rest
// after the `\u` - with the low half of a surrogate pair after its high
// half - and how much of rest it took. -1 when it stands for none; cut
// while it is.
func escapedChar(rest string) (r rune, used int, cut bool) {
	code, ok, whole := hex4At(rest)
	if !whole {
		return 0, 0, true
	}
	if !ok || code < 0xD800 || code > 0xDBFF {
		if !ok {
			return -1, charsBytes(rest, 4), false
		}
		if code >= 0xDC00 && code <= 0xDFFF {
			return -1, 4, false
		}
		return rune(code), 4, false
	}
	after := rest[4:]
	low, lowOK := uint32(0), false
	if lowHex, escaped := strings.CutPrefix(after, `\u`); escaped {
		l, ok, whole := hex4At(lowHex)
		if !whole {
			return 0, 0, true
		}
		low, lowOK = l, ok
	} else if strings.HasPrefix(`\u`, after) {
		return 0, 0, true
	}
	if lowOK && low >= 0xDC00 && low <= 0xDFFF {
		return rune(0x10000 + (code-0xD800)<<10 + (low - 0xDC00)), 10, false
	}
	// Half a pair.
	return -1, 4, false
}

// hex4At is the four hex digits s starts with: ok false when they aren't
// hex, whole false while fewer than four characters have come.
func hex4At(s string) (code uint32, ok, whole bool) {
	digits := firstRunes(s, 4)
	if utf8.RuneCountInString(digits) < 4 {
		return 0, false, false
	}
	for _, c := range digits {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
			return 0, false, true
		}
	}
	n, _ := strconv.ParseUint(digits, 16, 32)
	return uint32(n), true, true
}

// firstRunes is s's first n characters.
func firstRunes(s string, n int) string {
	for i := range s {
		if n == 0 {
			return s[:i]
		}
		n--
	}
	return s
}

// charsBytes is how many bytes s's first n characters take.
func charsBytes(s string, n int) int { return len(firstRunes(s, n)) }
