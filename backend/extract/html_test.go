package extract

import "testing"

func TestHTMLStructureBecomesMarkdown(t *testing.T) {
	source := `<!doctype html><html><head><title>T</title><style>p{}</style></head><body>
            <h1>Title</h1>
            <p>Some <b>bold</b> and <em>italic</em>
               text with a <a href="https://example.com/x">link</a>.</p>
            <script>var a = 1 < 2;</script>
            <ul><li>one</li><li>two<ol start="3"><li>three</li></ol></li></ul>
            <blockquote><p>Quoted.</p><p>Twice.</p></blockquote>
            <pre><code class="language-rust">fn main() {
    println!("hi");
}</code></pre>
            <p>Line<br>break and <code>x &lt; y</code> <code>a` + "``" + `b</code></p>
        </body></html>`
	expect(t, HTMLMarkdown(source), "# Title\n\n"+
		"Some **bold** and *italic* text with a [link](https://example.com/x).\n\n"+
		"- one\n- two\n  3. three\n\n"+
		"> Quoted.\n>\n> Twice.\n\n"+
		"```rust\nfn main() {\n    println!(\"hi\");\n}\n```\n\n"+
		"Line\nbreak and `x < y` ``` a``b ```")
}

func TestHTMLTagSoupReadsAsABrowserShowsIt(t *testing.T) {
	source := "<p>first<p>second<ul><li>a<li>b</ul><table><tr><th>H<th>I<tr><td colspan=2>wide</table>"
	expect(t, HTMLMarkdown(source), "first\n\nsecond\n\n- a\n- b\n\n| H | I |\n| --- | --- |\n| wide |  |")
}

func TestHTMLLinksAndImagesFollowMarkItDown(t *testing.T) {
	source := `<a href="javascript:void(0)">Click</a> <a href="notes.html#top">notes</a>
            <img alt="A chart" src="data:image/png;base64,iVBORw0KGgo=">
            <img alt="Lazy" src="data:image/gif;base64,R0lG" data-src="/big.png">
            <input type="checkbox" checked> done <input type="checkbox"> todo`
	expect(t, HTMLMarkdown(source),
		"Click [notes](notes.html#top) ![A chart](data:image/png;base64...) ![Lazy](/big.png) [x] done [ ] todo")
}

func TestHTMLEmphasisSplitByABlockIsLeftAlone(t *testing.T) {
	expect(t, HTMLMarkdown("<a href='/x'>a<div>b</div>c</a>"), "a\n\nb\n\nc")
}

func TestHTMLWhitespaceCollapsesOnlyWhenASCII(t *testing.T) {
	// A no-break space is the author's: kept, as a browser shows it.
	expect(t, HTMLMarkdown("<p>a \u00a0 b\n\tc</p><p><code>x\u00a0 \n y</code></p>"), "a \u00a0 b c\n\n`x\u00a0 y`")
}
