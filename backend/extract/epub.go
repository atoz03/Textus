package extract

import (
	"archive/zip"
	"errors"
	"strconv"
	"strings"
)

// EPUB to Markdown: the book's title and authors, then every chapter in
// reading order through the HTML converter. A manifest href is tried both
// percent-decoded and as written, since producers disagree.

// EpubMarkdown is the Markdown of an .epub; "" when no chapter has text.
func EpubMarkdown(path string) (string, error) {
	unreadable := errors.New("This .epub is not a readable e-book.")
	z, err := zip.OpenReader(path)
	if err != nil {
		return "", unreadable
	}
	defer z.Close()
	container, ok := zipEntry(&z.Reader, "META-INF/container.xml")
	if !ok {
		return "", unreadable
	}
	opfPath, ok := packagePath(container)
	if !ok {
		return "", unreadable
	}
	opf, ok := zipEntry(&z.Reader, opfPath)
	if !ok {
		return "", unreadable
	}
	book := parsePackage(opf)
	base := ""
	if i := strings.LastIndex(opfPath, "/"); i >= 0 {
		base = opfPath[:i]
	}

	var about, out []string
	if book.title != "" {
		about = append(about, "**Title:** "+book.title)
	}
	if len(book.authors) > 0 {
		about = append(about, "**Authors:** "+strings.Join(book.authors, ", "))
	}
	if len(about) > 0 {
		out = append(out, strings.Join(about, "\n"))
	}
	chapters := 0
	for _, id := range book.spine {
		href, ok := book.href(id)
		if !ok {
			continue
		}
		for _, h := range []string{percentDecode(href), href} {
			if chapter, ok := zipEntry(&z.Reader, resolvePart(base, h)); ok {
				if md := HTMLMarkdown(strings.ToValidUTF8(string(chapter), "\uFFFD")); md != "" {
					out = append(out, md)
					chapters++
				}
				break
			}
		}
	}
	if chapters == 0 {
		return "", nil
	}
	return strings.Join(out, "\n\n"), nil
}

// packagePath is the full-path of the container's first <rootfile>.
func packagePath(container []byte) (string, bool) {
	x := newPull(container)
	for n := x.next(); n.kind != nodeEnd; n = x.next() {
		if n.kind == nodeOpen && n.tag.name == "rootfile" {
			return n.tag.attr("full-path")
		}
	}
	return "", false
}

// epubPackage is what the package document (.opf) says.
type epubPackage struct {
	title    string
	authors  []string
	manifest [][2]string // id, href
	spine    []string    // manifest ids in reading order
}

func parsePackage(opf []byte) epubPackage {
	var book epubPackage
	x := newPull(opf)
	for n := x.next(); n.kind != nodeEnd; n = x.next() {
		if n.kind != nodeOpen {
			continue
		}
		switch t := n.tag; t.name {
		case "title":
			if title := strings.TrimSpace(x.text()); book.title == "" {
				book.title = title
			}
		case "creator":
			if author := strings.TrimSpace(x.text()); author != "" {
				book.authors = append(book.authors, author)
			}
		case "item":
			id, hasID := t.attr("id")
			href, hasHref := t.attr("href")
			if hasID && hasHref {
				book.manifest = append(book.manifest, [2]string{id, href})
			}
		case "itemref":
			if id, ok := t.attr("idref"); ok {
				book.spine = append(book.spine, id)
			}
		}
	}
	return book
}

// href is the first manifest item's href with id.
func (b epubPackage) href(id string) (string, bool) {
	for _, item := range b.manifest {
		if item[0] == id {
			return item[1], true
		}
	}
	return "", false
}

// percentDecode is Chapter%201.xhtml as Chapter 1.xhtml; a malformed
// escape is kept as written.
func percentDecode(text string) string {
	var out []byte
	for i := 0; i < len(text); i++ {
		if text[i] == '%' && i+3 <= len(text) {
			if b, err := strconv.ParseUint(text[i+1:i+3], 16, 8); err == nil {
				out = append(out, byte(b))
				i += 2
				continue
			}
		}
		out = append(out, text[i])
	}
	return strings.ToValidUTF8(string(out), "\uFFFD")
}
