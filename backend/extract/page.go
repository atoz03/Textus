package extract

import (
	"net/url"
	"strings"
	"unicode/utf8"

	"golang.org/x/net/html"
)

// A web page's main content as Markdown, for web_fetch: scripts,
// navigation, headers, footers, forms and hidden elements taken out,
// <main> or <article> kept when there is one.

// notContent are elements that are never content, wherever they are.
var notContent = map[string]bool{
	"script": true, "style": true, "noscript": true, "template": true, "svg": true, "iframe": true,
	"canvas": true, "object": true, "nav": true, "aside": true, "form": true, "button": true,
	"select": true, "textarea": true, "dialog": true,
}

// minMainChars: main content shorter than this is taken as a teaser, not
// the page; the whole body is used instead.
const minMainChars = 200

// PageMarkdown is a web page's title and its content as Markdown, its
// links made absolute against pageURL.
func PageMarkdown(source, pageURL string) (title, markdown string) {
	doc, err := html.Parse(strings.NewReader(source))
	if err != nil {
		return "", ""
	}
	title = pageTitle(doc)
	base, err := url.Parse(pageURL)
	if err != nil {
		base = nil
	}
	prune(doc, base, false)
	content := first(doc, func(n *html.Node) bool {
		role, _ := attr(n, "role")
		return n.Data == "main" || n.Data == "article" || role == "main"
	})
	if content == nil || utf8.RuneCountInString(strings.TrimSpace(textOf(content))) < minMainChars {
		content = first(doc, func(n *html.Node) bool { return n.Data == "body" })
	}
	if content == nil {
		return title, ""
	}
	w := &htmlWriter{}
	w.node(content)
	return title, w.finish()
}

// first is the first element in document order that is, nil for none.
func first(n *html.Node, is func(*html.Node) bool) *html.Node {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type != html.ElementNode {
			continue
		}
		if is(c) {
			return c
		}
		if found := first(c, is); found != nil {
			return found
		}
	}
	return nil
}

// pageTitle is the page's <title>, else its og:title, on one line.
func pageTitle(doc *html.Node) string {
	title := ""
	if t := first(doc, func(n *html.Node) bool { return n.Data == "title" }); t != nil {
		title = textOf(t)
	}
	if strings.TrimSpace(title) == "" {
		if meta := first(doc, func(n *html.Node) bool {
			property, _ := attr(n, "property")
			return n.Data == "meta" && property == "og:title"
		}); meta != nil {
			title, _ = attr(meta, "content")
		}
	}
	return strings.Join(strings.Fields(title), " ")
}

// hiddenOrChrome reports whether n is never content: an element that
// never is, one hidden, or the page's navigation.
func hiddenOrChrome(n *html.Node) bool {
	if notContent[n.Data] {
		return true
	}
	if _, hidden := attr(n, "hidden"); hidden {
		return true
	}
	if v, _ := attr(n, "aria-hidden"); v == "true" {
		return true
	}
	if role, _ := attr(n, "role"); role == "navigation" || role == "search" {
		return true
	}
	style, _ := attr(n, "style")
	style = strings.ToLower(strings.ReplaceAll(style, " ", ""))
	return strings.Contains(style, "display:none") || strings.Contains(style, "visibility:hidden")
}

// prune takes out of n what isn't content - a page's own header and
// footer too, though an article's (its title, byline) stay - and makes
// its links and pictures absolute.
func prune(n *html.Node, base *url.URL, inArticle bool) {
	for c := n.FirstChild; c != nil; {
		next := c.NextSibling
		if c.Type == html.ElementNode {
			if hiddenOrChrome(c) || ((c.Data == "header" || c.Data == "footer") && !inArticle) {
				n.RemoveChild(c)
				c = next
				continue
			}
			absolute(c, base)
			prune(c, base, inArticle || c.Data == "article")
		}
		c = next
	}
}

// absolute makes a link's or a picture's address absolute, when it joins
// to a web address.
func absolute(n *html.Node, base *url.URL) {
	key := map[string]string{"a": "href", "img": "src"}[n.Data]
	if key == "" || base == nil {
		return
	}
	for i, a := range n.Attr {
		if !strings.EqualFold(a.Key, key) {
			continue
		}
		if joined, err := base.Parse(a.Val); err == nil && (joined.Scheme == "http" || joined.Scheme == "https") {
			n.Attr[i].Val = joined.String()
		}
	}
}
