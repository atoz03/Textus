package ai

import (
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

// A rewrite's pictures: a text edit never changes a picture, so
// KeepPictures puts back the ones a rewrite left out or swapped.

// IsPicture reports whether block is a picture and nothing else:
// `![alt](path)` or an `<img>`. Keep in step with the frontend's isPicture
// (shared cases file).
func IsPicture(block string) bool {
	b := strings.TrimSpace(block)
	if strings.Contains(b, "\n") {
		return false
	}
	if rest, ok := strings.CutPrefix(b, "!["); ok {
		// `![alt](path)`: no `]` in the alt, no `)` in the path.
		close := strings.IndexByte(rest, ']')
		if close < 0 {
			return false
		}
		inner, ok := strings.CutPrefix(rest[close+1:], "(")
		if !ok {
			return false
		}
		path, ok := strings.CutSuffix(inner, ")")
		return ok && !strings.Contains(path, ")")
	}
	attrs, ok := strings.CutPrefix(b, "<img")
	if !ok {
		return false
	}
	attrs, ok = strings.CutSuffix(attrs, ">")
	if !ok || attrs == "" {
		return false
	}
	first, _ := utf8.DecodeRuneInString(attrs)
	return unicode.IsSpace(first) && !strings.Contains(attrs, ">")
}

var (
	pictureMarkdown = regexp.MustCompile(`^!\[[^\]\n]*\]\(\s*(<[^>\n]*>|[^)\s]*)`)
	pictureImg      = regexp.MustCompile(`(?i)^<img\s[^>]*\bsrc\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s"'>]+))`)
	pictureAlt      = regexp.MustCompile(`^!\[([^\]\n]*)\]\(`)
	pictureRest     = regexp.MustCompile(`^!\[[^\]\n]*\](\(.*\))$`)
	pictureInText   = regexp.MustCompile(`!\[([^\]]*)\]\((<[^>]*>|[^)\s]*)(?:\s+"[^"]*")?\)`)
)

// pictureSrc is a picture's address: what makes it the same picture
// whatever its description says. `![alt](<a b.png> "title")` is `a b.png`;
// an `<img>`'s src may be unquoted, and one with none is known by its
// text. Keep in step with the frontend's pictureSrc
// (src/editor/markdown/doc-blocks.ts).
func pictureSrc(picture string) string {
	t := strings.TrimSpace(picture)
	if m := pictureMarkdown.FindStringSubmatch(t); m != nil {
		return unbracket(m[1])
	}
	if m := pictureImg.FindStringSubmatch(t); m != nil {
		for _, g := range m[1:] {
			if g != "" {
				return g
			}
		}
		return ""
	}
	return t
}

// unbracket is an address as written in `<>`, without them; a lone `<` or
// `>` is part of it. The frontend's unbracket.
func unbracket(src string) string {
	if len(src) >= 2 && src[0] == '<' && src[len(src)-1] == '>' {
		return src[1 : len(src)-1]
	}
	return src
}

// withDescription is original with the description rewritten gives it:
// the writer's address and title stay. An `<img>` stays as it is.
func withDescription(original, rewritten string) string {
	alt := pictureAlt.FindStringSubmatch(strings.TrimSpace(rewritten))
	rest := pictureRest.FindStringSubmatch(strings.TrimSpace(original))
	if alt == nil || rest == nil {
		return strings.TrimSpace(original)
	}
	return "![" + alt[1] + "]" + rest[1]
}

// picturesApart is text with each line that is one of srcs' pictures in a
// paragraph of other lines put in a paragraph of its own, as the writer's
// picture is: "![a](a.png)\n*Figure 1*" would read as one paragraph, the
// picture beside its caption. A line in a fence or `$$` block, or in an
// HTML block, is that block's and stays. Keep in step with the frontend's
// picturesApart (src/editor/markdown/doc-blocks.ts).
func picturesApart(text string, srcs []string) string {
	out := text
	spans := Paragraphs(text)
	for k := len(spans) - 1; k >= 0; k-- {
		from, to := spans[k][0], spans[k][1]
		lines := strings.Split(text[from:to], "\n")
		if len(lines) < 2 || opensHTML(lines[0]) {
			continue
		}
		code := codeLines(lines)
		apart := make([]bool, len(lines))
		any := false
		for i, line := range lines {
			apart[i] = !code[i] && IsPicture(line) && slices.Contains(srcs, pictureSrc(line))
			any = any || apart[i]
		}
		if !any {
			continue
		}
		var groups [][]string
		lastApart := false
		for i, line := range lines {
			if len(groups) > 0 && !apart[i] && !lastApart {
				groups[len(groups)-1] = append(groups[len(groups)-1], line)
			} else {
				groups = append(groups, []string{line})
			}
			lastApart = apart[i]
		}
		parts := make([]string, len(groups))
		for i, g := range groups {
			parts[i] = strings.Join(g, "\n")
		}
		out = out[:from] + strings.Join(parts, "\n\n") + out[to:]
	}
	return out
}

// opensHTML reports whether a paragraph whose first line is line is an
// HTML block: it opens with a tag, and isn't one of the pictures.
func opensHTML(line string) bool {
	rest := strings.TrimLeft(line, " ")
	if len(line)-len(rest) > 3 || len(rest) < 2 || rest[0] != '<' {
		return false
	}
	c := rest[1]
	tag := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '/' || c == '!' || c == '?'
	return tag && !IsPicture(line)
}

// codeLines is, for each of a paragraph's lines, whether it is in a fence
// or `$$` block (its fences too), as Paragraphs finds them.
func codeLines(lines []string) []bool {
	fence := ""
	out := make([]bool, len(lines))
	for i, line := range lines {
		if fence != "" {
			if strings.HasPrefix(strings.TrimLeftFunc(line, unicode.IsSpace), fence) {
				fence = ""
			}
			out[i] = true
			continue
		}
		if fenceOpening(line) == "" {
			continue
		}
		fence = opensFence(line)
		out[i] = true
	}
	return out
}

// KeepPictures is text, a rewrite of run, with the run's pictures as they
// were: a picture is the same by its address, so the rewrite may change its
// description but never what it shows; a dropped picture goes back by the
// paragraph it stood by, and a swapped one is taken out and the original
// put back (the swap is generate_image's to propose). complete: all of
// text is there; while streaming, a picture whose place isn't reached yet
// is left out. It answers the text and how many were taken out. Keep in
// step with the frontend's keepPictures (src/editor/markdown/doc-blocks.ts;
// shared cases file).
func KeepPictures(run []string, text string, complete bool) (string, int) {
	type original struct {
		at      int
		md, src string
	}
	blocks := make([]string, len(run))
	var originals []original
	var srcs []string
	for i, b := range run {
		blocks[i] = strings.TrimSpace(b)
		if IsPicture(blocks[i]) {
			originals = append(originals, original{i, blocks[i], pictureSrc(blocks[i])})
			srcs = append(srcs, pictureSrc(blocks[i]))
		}
	}
	if len(originals) == 0 {
		return text, 0
	}
	var shown []int
	// take is the writer's picture the rewrite's src is, not taken yet.
	take := func(src string) (string, bool) {
		for _, o := range originals {
			if o.src == src && !slices.Contains(shown, o.at) {
				shown = append(shown, o.at)
				return o.md, true
			}
		}
		return "", false
	}
	spaced := picturesApart(text, srcs)
	spans := Paragraphs(spaced)
	type part struct {
		text string
		ours bool
	}
	parts := make([]part, len(spans))
	for i, s := range spans {
		p := spaced[s[0]:s[1]]
		if IsPicture(p) {
			if md, ok := take(pictureSrc(p)); ok {
				parts[i] = part{withDescription(md, p), true}
			} else {
				parts[i] = part{p, false}
			}
			continue
		}
		// A picture inside the words is shown there.
		for _, m := range pictureInText.FindAllStringSubmatch(p, -1) {
			take(unbracket(m[2]))
		}
		parts[i] = part{p, false}
	}
	missing := len(originals) - len(shown)
	swapped := 0
	var kept []string
	rewritten := spaced != text
	for i, p := range parts {
		rewritten = rewritten || p.text != spaced[spans[i][0]:spans[i][1]]
		isNew := !p.ours && IsPicture(p.text) && !slices.Contains(srcs, pictureSrc(p.text))
		if isNew && swapped < missing {
			swapped++
			continue
		}
		kept = append(kept, p.text)
	}
	out := text
	if rewritten || swapped > 0 {
		out = strings.Join(kept, "\n\n")
	}
	wordsBefore := 0
	// Pictures in the new text so far: each moves the later places on by
	// one.
	put := 0
	for i, picture := range blocks {
		if !slices.ContainsFunc(originals, func(o original) bool { return o.at == i }) {
			wordsBefore++
			continue
		}
		// Kept where the rewrite has it, it takes a place all the same.
		if slices.Contains(shown, i) {
			put++
			continue
		}
		spans := Paragraphs(out)
		at := wordsBefore + put
		switch {
		case at == 0:
			out = picture + "\n\n" + strings.TrimLeftFunc(out, unicode.IsSpace)
		case at < len(spans):
			end := spans[at-1][1]
			out = out[:end] + "\n\n" + picture + "\n\n" + strings.TrimLeftFunc(out[end:], unicode.IsSpace)
		case complete && len(spans) > 0:
			out = strings.TrimRightFunc(out, unicode.IsSpace) + "\n\n" + picture
		default:
			continue
		}
		put++
	}
	return out, swapped
}
