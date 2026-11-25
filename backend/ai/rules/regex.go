package rules

import (
	"time"

	"github.com/dlclark/regexp2"
)

// The rules' patterns run on regexp2 rather than the standard library:
// they were written for Rust's regex crate, whose \b, \w, \d and \s are
// Unicode-aware (a word boundary after "café", full-width digits), as
// .NET's are and Go's regexp's aren't. Two differences are written out
// in the patterns themselves: \z for the end of the text (a .NET $ also
// matches before a final line break) and asciiB for Rust's (?-u:\b).

// asciiB is an ASCII-only word boundary, Rust's (?-u:\b).
const asciiB = `(?:(?<=[0-9A-Za-z_])(?![0-9A-Za-z_])|(?<![0-9A-Za-z_])(?=[0-9A-Za-z_]))`

// matchTimeout bounds one search: the patterns are simple, but a pasted
// megabyte of one character should not hold a check.
const matchTimeout = 2 * time.Second

// Re is a compiled pattern.
type Re struct{ re *regexp2.Regexp }

// MustRe compiles pattern, panicking on a bad one: the patterns are the
// program's own.
func MustRe(pattern string) *Re {
	re := regexp2.MustCompile(pattern, regexp2.None)
	re.MatchTimeout = matchTimeout
	return &Re{re}
}

// Span is a half-open range of char (rune) offsets; ok is false for a
// group that took no part in the match.
type Span struct {
	A, B int
	OK   bool
}

// each calls fn with every non-overlapping match in text, left to right,
// as its groups' spans (group 0 the whole match).
func (r *Re) each(text []rune, fn func(groups []Span)) {
	m, err := r.re.FindRunesMatch(text)
	for err == nil && m != nil {
		gs := m.Groups()
		spans := make([]Span, len(gs))
		for i, g := range gs {
			if len(g.Captures) > 0 {
				spans[i] = Span{g.Index, g.Index + g.Length, true}
			}
		}
		fn(spans)
		m, err = r.re.FindNextMatch(m)
	}
}

// FindAll is every match in text, as char spans.
func (r *Re) FindAll(text []rune) [][2]int {
	var out [][2]int
	r.each(text, func(g []Span) { out = append(out, [2]int{g[0].A, g[0].B}) })
	return out
}

// FindAllString is FindAll on a string, in char offsets.
func (r *Re) FindAllString(s string) [][2]int { return r.FindAll([]rune(s)) }

// Count is how many matches text has.
func (r *Re) Count(s string) int { return len(r.FindAllString(s)) }

// MatchString is whether s has a match.
func (r *Re) MatchString(s string) bool {
	ok, err := r.re.MatchString(s)
	return err == nil && ok
}

// ReplaceAll is s with every match replaced by repl, in which $1 stands
// for the first group.
func (r *Re) ReplaceAll(s, repl string) string {
	out, err := r.re.Replace(s, repl, -1, -1)
	if err != nil {
		return s
	}
	return out
}
