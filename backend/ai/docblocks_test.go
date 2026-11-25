package ai

import (
	"encoding/json/v2"
	"os"
	"strings"
	"testing"
)

// editor is a document as the editor sends it: (kind, markdown) per
// block, ids made the way the frontend makes them.
func editor(blocks ...[2]string) Document {
	texts := make([]string, len(blocks))
	for i, b := range blocks {
		texts[i] = b[1]
	}
	ids := blockIDs(texts)
	sent := make([]SentBlock, len(blocks))
	for i, b := range blocks {
		sent[i] = SentBlock{ID: ids[i], Markdown: b[1], Kind: b[0]}
	}
	return DocumentFromBlocks(sent)
}

// resolved is a propose_edit call's arguments as resolved, read back.
func resolved(t *testing.T, d *Document, args string) *Args {
	t.Helper()
	out, ok := ParseArgs(ResolveEditArguments(d, args))
	if !ok {
		t.Fatalf("not JSON: %s", ResolveEditArguments(d, args))
	}
	return out
}

func call(parts ...string) string {
	args := NewArgs()
	for i := 0; i+1 < len(parts); i += 2 {
		args.Set(parts[i], parts[i+1])
	}
	return args.String()
}

func TestTheDocumentIsTheEditorsBlocksJoined(t *testing.T) {
	// A loose list is one node, so one block - its blank lines inside it.
	d := editor([2]string{"heading", "# Title"}, [2]string{"list", "- one\n\n- two"}, [2]string{"paragraph", "After."})
	if d.Text != "# Title\n\n- one\n\n- two\n\nAfter." || len(d.Blocks) != 3 {
		t.Errorf("got %q, %d blocks", d.Text, len(d.Blocks))
	}
	if d.BlockText(d.Blocks[1]) != "- one\n\n- two" || d.Blocks[1].Kind != "list" {
		t.Errorf("got %+v", d.Blocks[1])
	}
}

func TestMarksEachBlockWithItsID(t *testing.T) {
	d := editor([2]string{"heading", "# Title"}, [2]string{"paragraph", "One."}, [2]string{"paragraph", "Two."})
	b := d.Blocks
	want := "[#" + b[0].ID + "] # Title\n\n[#" + b[1].ID + "] One.\n\n[#" + b[2].ID + "] Two."
	if got := Annotate(&d, 0, len(d.Text)); got != want {
		t.Errorf("got %q", got)
	}
}

func TestACommentOrPictureByBlockKeepsItsIDAndQuotesTheBlockOrItsPhrase(t *testing.T) {
	d := DocumentFromMarkdown("# Title\n\nThe cat sat on the mat.\n\n![Cat](cat.png)\n\nThe cat sat.")
	b := d.Blocks
	target := func(args string) *Args {
		out, ok := ParseArgs(ResolveTargetArguments(&d, args))
		if !ok {
			t.Fatal("not JSON")
		}
		return out
	}
	whole := target(call("block", "#"+b[1].ID, "comment", "Vague."))
	if whole.StrOr("anchor") != "The cat sat on the mat." || whole.StrOr("block") != b[1].ID {
		t.Errorf("got %s", whole)
	}
	// A few words in it, though they repeat elsewhere: the block says
	// where.
	if words := target(call("anchor", "The cat sat", "block", b[1].ID, "comment", "x")); words.StrOr("anchor") != "The cat sat" {
		t.Errorf("got %s", words)
	}
	// A picture to redo, by its block; strict-schema empties go.
	redo := target(call("action", "replace", "anchor", "", "block", b[2].ID, "prompt", "p"))
	if redo.StrOr("anchor") != "![Cat](cat.png)" || b[2].Kind != "picture" {
		t.Errorf("got %s, %s", redo, b[2].Kind)
	}
	unknown := target(call("block", "zzzz", "comment", "x"))
	if unknown.StrOr("block") != "zzzz" || CheckTarget(&d, unknown) == "" {
		t.Errorf("got %s", unknown)
	}
}

func TestABlockIsItsWholeTextAndKeepsItsID(t *testing.T) {
	d := DocumentFromMarkdown("# Title\n\nThe cat sat on the mat.\n\nThe end.")
	id := d.Blocks[1].ID
	v := resolved(t, &d, call("action", "replace", "block", id, "text", "A dog lay down."))
	if v.StrOr("anchor") != "The cat sat on the mat." || v.StrOr("text") != "A dog lay down." || v.StrOr("block") != id || CheckTarget(&d, v) != "" {
		t.Errorf("got %s", v)
	}
}

func TestWordsInABlockBecomeTheBlockWithThemChanged(t *testing.T) {
	d := DocumentFromMarkdown("One cat.\n\nThe cat sat on the mat.")
	id := d.Blocks[1].ID
	v := resolved(t, &d, call("action", "replace", "block", "#"+id, "anchor", "cat", "text", "dog"))
	if v.StrOr("anchor") != "The cat sat on the mat." || v.StrOr("text") != "The dog sat on the mat." || v.StrOr("block") != id {
		t.Errorf("got %s", v)
	}
	v = resolved(t, &d, call("action", "delete", "block", id, "anchor", " on the mat"))
	if v.StrOr("action") != "replace" || v.StrOr("text") != "The cat sat." {
		t.Errorf("got %s", v)
	}
}

func TestWordsNotInTheBlockOnceAreRefused(t *testing.T) {
	d := DocumentFromMarkdown("The cat and the cat.\n\nA dog.")
	for _, quote := range []string{"cat", "dog"} {
		if v := resolved(t, &d, call("action", "replace", "block", d.Blocks[0].ID, "anchor", quote, "text", "x")); CheckTarget(&d, v) == "" {
			t.Errorf("%s: passed", quote)
		}
	}
}

func TestAnInsertionNextToWordsInABlockStaysNextToThem(t *testing.T) {
	d := editor([2]string{"list", "- one\n- two\n- three"}, [2]string{"paragraph", "And two more."})
	id := d.Blocks[0].ID
	v := resolved(t, &d, call("action", "insert_after", "block", id, "anchor", "two", "text", " and a half"))
	// "two" is in the next paragraph too; the block says which is meant.
	if v.StrOr("anchor") != "two" || v.StrOr("block") != id {
		t.Errorf("got %s", v)
	}
	start, end, ok := d.TargetSpan(v)
	if !ok || d.Text[start:end] != "two" || end >= d.Blocks[0].End {
		t.Errorf("got %d-%d %v", start, end, ok)
	}
}

func TestARepeatedBlockHasAnIDOfItsOwn(t *testing.T) {
	d := DocumentFromMarkdown("First.\n\n---\n\nSecond.\n\n---")
	b := d.Blocks
	if b[1].ID == b[3].ID {
		t.Fatal("the same id twice")
	}
	v := resolved(t, &d, call("action", "delete", "block", b[3].ID))
	if start, end, ok := d.TargetSpan(v); v.StrOr("anchor") != "---" || !ok || start != b[3].Start || end != b[3].End {
		t.Errorf("got %s", v)
	}
}

func TestARunOfBlocksIsTheirTextFromFirstToLast(t *testing.T) {
	d := DocumentFromMarkdown("# T\n\nOne.\n\nTwo.\n\nThree.")
	a, b := d.Blocks[1].ID, d.Blocks[2].ID
	v := resolved(t, &d, call("action", "replace", "anchor", "", "block", a, "last_block", b, "text", "1.\n\n2."))
	if v.StrOr("anchor") != "One.\n\nTwo." || v.StrOr("last_block") != b {
		t.Errorf("got %s", v)
	}
	// A run of one is the block.
	if one := resolved(t, &d, call("action", "replace", "block", a, "last_block", a, "text", "x")); one.Has("last_block") {
		t.Errorf("got %s", one)
	}
	// The wrong way round names nothing.
	if back := resolved(t, &d, call("action", "replace", "block", b, "last_block", a, "text", "x")); CheckTarget(&d, back) == "" {
		t.Errorf("got %s", back)
	}
}

func TestACallWithoutABlockIsLeftForTheTool(t *testing.T) {
	d := DocumentFromMarkdown("One.")
	v := resolved(t, &d, call("action", "replace", "anchor", "[#ab12] One.", "text", "[#ab12] Two. [#] stays"))
	// Markers copied into the words come out all the same.
	if v.StrOr("anchor") != "One." || v.StrOr("text") != "Two. [#] stays" || CheckTarget(&d, v) != NoBlock {
		t.Errorf("got %s", v)
	}
}

func TestKeepsPicturesAsTheSharedCasesSay(t *testing.T) {
	raw, err := os.ReadFile("../../src/ai/keep-pictures-cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Name     string `json:"name"`
		Document string `json:"document"`
		Text     string `json:"text"`
		Complete bool   `json:"complete"`
		Expected string `json:"expected"`
		Swapped  int    `json:"swapped"`
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		var run []string
		for _, s := range Paragraphs(c.Document) {
			run = append(run, c.Document[s[0]:s[1]])
		}
		got, swapped := KeepPictures(run, c.Text, c.Complete)
		if got != c.Expected || swapped != c.Swapped {
			t.Errorf("%s: got %q (%d swapped), want %q (%d)", c.Name, got, swapped, c.Expected, c.Swapped)
		}
	}
	if len(cases) < 5 {
		t.Errorf("%d cases", len(cases))
	}
}

func TestARewriteOfARunKeepsThePicturesItLeftOut(t *testing.T) {
	d := editor([2]string{"paragraph", "One."}, [2]string{"picture", "![a](a.png)"}, [2]string{"paragraph", "Two."})
	v := resolved(t, &d, call("action", "replace", "block", d.Blocks[0].ID, "last_block", d.Blocks[2].ID, "text", "Uno.\n\nDos."))
	if v.StrOr("text") != "Uno.\n\n![a](a.png)\n\nDos." {
		t.Errorf("got %s", v)
	}
}

func TestARewriteOfARunThatSwapsAPictureKeepsItAndSaysSo(t *testing.T) {
	d := DocumentFromMarkdown("One.\n\n![a](a.png)\n\nTwo.")
	v := resolved(t, &d, call("action", "replace", "block", d.Blocks[0].ID, "last_block", d.Blocks[2].ID, "text", "Uno.\n\n![b](b.png)\n\nDos."))
	if n, _ := v.Int(PicturesKeptKey); v.StrOr("text") != "Uno.\n\n![a](a.png)\n\nDos." || n != 1 {
		t.Errorf("got %s", v)
	}
}

func TestARewriteOfARunNamesThePicturesItKeeps(t *testing.T) {
	d := DocumentFromMarkdown("One.\n\n![a](a.png)\n\nTwo.")
	v := resolved(t, &d, call("action", "replace", "block", d.Blocks[0].ID, "last_block", d.Blocks[2].ID, "text", "Uno.\n\n![a](a.png)\n\nDos."))
	if raw, _ := v.Raw(PicturesInRunKey); string(raw) != `["`+d.Blocks[1].ID+`"]` {
		t.Errorf("got %s", v)
	}
	// A run with no picture names none.
	plain := DocumentFromMarkdown("One.\n\nTwo.")
	if v := resolved(t, &plain, call("action", "replace", "block", plain.Blocks[0].ID, "last_block", plain.Blocks[1].ID, "text", "Uno.\n\nDos.")); v.Has(PicturesInRunKey) {
		t.Errorf("got %s", v)
	}
}

func TestAnIDWrittenAsItsWholeMarkerStillNamesItsBlock(t *testing.T) {
	d := DocumentFromMarkdown("One.\n\nTwo.")
	id := d.Blocks[1].ID
	v := resolved(t, &d, call("action", "replace", "block", "[#"+id+"]", "text", "2."))
	if v.StrOr("block") != id || v.StrOr("anchor") != "Two." || CheckTarget(&d, v) != "" {
		t.Errorf("got %s", v)
	}
}

func TestResolvedArgumentsKeepTheModelsOrder(t *testing.T) {
	d := DocumentFromMarkdown("One.\n\nTwo.")
	got := ResolveEditArguments(&d, `{"text":"2.","note":"n","block":"`+d.Blocks[1].ID+`","action":"replace"}`)
	want := `{"action":"replace","block":"` + d.Blocks[1].ID + `","anchor":"Two.","text":"2.","note":"n"}`
	if got != want {
		t.Errorf("got %s", got)
	}
	// Not JSON: as it was.
	if got := ResolveEditArguments(&d, `{"text":`); got != `{"text":` {
		t.Errorf("got %s", got)
	}
}

func TestFencesAndMathsKeepTheirBlankLines(t *testing.T) {
	text := "Intro.\n\n```\ncode\n\nmore\n```\n\n$$\nx\n\ny\n$$\n\n$$x$$\n\nEnd."
	var got []string
	for _, s := range Paragraphs(text) {
		got = append(got, text[s[0]:s[1]])
	}
	want := []string{"Intro.", "```\ncode\n\nmore\n```", "$$\nx\n\ny\n$$", "$$x$$", "End."}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("got %q", got)
	}
}

func TestALongDocumentIsAnOutlineWithItsStartAndEnd(t *testing.T) {
	var b strings.Builder
	b.WriteString("# Book\n\n")
	for i := range 40 {
		b.WriteString("## Chapter " + string(rune('A'+i%26)) + "\n\n" + strings.Repeat("Words go here. ", 100) + "\n\n")
	}
	d := DocumentFromMarkdown(b.String())
	section := DocumentSection(&d, LeastReading(true), "Chapter C")
	for _, part := range []string{"---document (truncated", "[outline]\nL1 [#", "## Chapter B", "[the section the user is working in, from line", "[start]\n[#", "[end]\n", "read_document"} {
		if !strings.Contains(section, part) {
			t.Errorf("missing %q", part)
		}
	}
	short := DocumentFromMarkdown("# T\n\nOne.")
	if got := DocumentSection(&short, LeastReading(false), ""); got != "---document---\n[#"+short.Blocks[0].ID+"] # T\n\n[#"+short.Blocks[1].ID+"] One." {
		t.Errorf("got %q", got)
	}
}
