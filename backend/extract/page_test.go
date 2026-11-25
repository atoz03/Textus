package extract

import (
	"strings"
	"testing"
)

func TestAnArticleKeepsItsHeadingsListsAndLinksAndLosesTheChrome(t *testing.T) {
	page := `<html><head><title>On tides | Sea Notes</title><style>p{color:red}</style></head><body>
            <header><nav><a href="/">Home</a> <a href="/about">About</a></nav></header>
            <main><article>
              <header><h1>On tides</h1></header>
              <p>The moon pulls the sea twice a day, so the water rises and falls along every coast in a rhythm that sailors and fishermen have counted on for centuries.</p>
              <h2>Spring tides</h2>
              <p>Spring tides come at new and full moon, when the sun and the moon pull in one line. See <a href="/tables">the tide tables</a> &amp; charts.</p>
              <ul><li>Highest of the month</li><li>Twice a month</li></ul>
              <p style="display: none">hidden tracking text</p>
              <!-- a comment <p>not text</p> -->
            </article></main>
            <footer>© Sea Notes</footer><script>if (a < b) track()</script></body></html>`
	title, text := PageMarkdown(page, "https://sea.example/tides")
	if title != "On tides | Sea Notes" {
		t.Fatal(title)
	}
	if !strings.HasPrefix(text, "# On tides") || !strings.Contains(text, "## Spring tides") ||
		!strings.Contains(text, "[the tide tables](https://sea.example/tables)") || !strings.Contains(text, "Highest of the month") {
		t.Fatal(text)
	}
	for _, gone := range []string{"track()", "Home", "hidden tracking", "not text", "color:red", "© Sea"} {
		if strings.Contains(text, gone) {
			t.Errorf("%s in %s", gone, text)
		}
	}
}

func TestAPageWithoutMainContentGivesItsBody(t *testing.T) {
	title, text := PageMarkdown("<html><head><title>Links</title></head><body><ul><li>One</li><li>Two &#x4e2d;&#25991;</li></ul><script>x()</script></body></html>", "https://example.com/")
	if title != "Links" || !strings.Contains(text, "One") || !strings.Contains(text, "Two 中文") || strings.Contains(text, "x()") {
		t.Fatal(title, text)
	}
}
