package ai

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"net"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"textus/backend/ai/aicompat"
)

// The tests run side by side and share the pictures kept, so more are kept
// here.
func init() { pendingCount = 1024 }

// runAt is a run whose pictures go to baseURL.
func runAt(baseURL string) *ImageRun {
	return &ImageRun{job: ImageJob{API: aicompat.OpenAIImages, BaseURL: baseURL, APIKey: "sk", Model: "gpt-image-2"}, limit: PerSend}
}

// offline is a run whose pictures fail: nothing listens there.
func offline() *ImageRun { return runAt("https://127.0.0.1:1") }

// silent is a server that takes a request and never answers: a picture
// sent there is still being made while a test looks.
func silent(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			t.Cleanup(func() { conn.Close() })
		}
	}()
	return "http://" + l.Addr().String()
}

func waitPicture(t *testing.T, id string) (GeneratedImageData, error) {
	t.Helper()
	return AwaitPicture(context.Background(), id, 10*time.Second)
}

// namedTarget is a call as the model sends it, naming the block its
// `anchor` is in, resolved as the agent does. Words in no block stay a
// quote.
func namedTarget(d *Document, args map[string]any) string {
	if anchor, ok := args["anchor"].(string); ok {
		var holding []Block
		for _, b := range d.Blocks {
			if strings.Contains(d.BlockText(b), anchor) {
				holding = append(holding, b)
			}
		}
		if _, has := args["block"]; !has && len(holding) == 1 {
			args["block"] = holding[0].ID
		}
	}
	raw, _ := json.Marshal(args, json.Deterministic(true))
	return ResolveTargetArguments(d, string(raw))
}

// callImage is a generate_image call on run, with proposals not decided
// yet: this request's, and earlier ones'.
func callImage(run *ImageRun, args map[string]any, document string, thisRequest []string, undecided ...UndecidedEdit) (map[string]any, bool) {
	d := DocumentFromMarkdown(document)
	tc := NewToolContext(&d)
	tc.proposed.texts = thisRequest
	tc.Undecided = undecided
	announced := false
	out := run.runCall(namedTarget(&d, args), tc, func() { announced = true })
	var got map[string]any
	json.Unmarshal([]byte(out), &got)
	return got, announced
}

const harbour = "# Harbour\n\nThe boats came in at dusk.\n\nThe nets were mended by lamplight."

func notGenerated(args map[string]any) string { s, _ := args["not_generated"].(string); return s }

func outcomeOf(args map[string]any) string {
	raw, _ := json.Marshal(args)
	return imageOutcome(string(raw))
}

func ready(id string, width uint32) {
	finishPicture(id, pictureSlot{state: slotReady, image: aicompat.GeneratedImage{Bytes: []byte{1, 2, 3}, Mime: "image/png", Ext: "png", Width: width, Height: 3}})
}

func TestAPictureWithAWidthCanBeRedone(t *testing.T) {
	// Written as HTML once it was given a width.
	doc := "One.\n\n<img src=\"assets/a.png\" width=\"60%\">\n\nTwo."
	if args, made := callImage(offline(), map[string]any{"action": "replace", "anchor": `<img src="assets/a.png" width="60%">`, "prompt": "p", "alt": "a"}, doc, nil); !made {
		t.Fatal(args)
	}
	if !IsPictureMarkdown("![a](b.png)") || IsPictureMarkdown("<img src=a.png> and words") || IsPictureMarkdown("<imgx>") {
		t.Fatal("pictures judged wrong")
	}
}

func TestABlockThatIsntThereIsSaidSo(t *testing.T) {
	args, made := callImage(offline(), map[string]any{"action": "insert_after", "block": "zzzz", "prompt": "p", "alt": "a"}, harbour, nil)
	if made || !strings.Contains(notGenerated(args), "no block has the id `zzzz`") || args["limit_reached"] != nil {
		t.Fatal(args)
	}
}

func TestACallThatCantBePlacedSpendsNothing(t *testing.T) {
	run := offline()
	args, announced := callImage(run, map[string]any{"prompt": "boats", "alt": "Boats", "action": "insert_after", "anchor": "No such text."}, harbour, nil)
	if announced || !strings.Contains(notGenerated(args), "name the passage by its block") || args["image"] != nil {
		t.Fatal(args)
	}
	// `replace` takes a picture, not text.
	args, _ = callImage(run, map[string]any{"prompt": "boats", "action": "replace", "anchor": "The boats came in at dusk."}, harbour, nil)
	if !strings.Contains(notGenerated(args), "a picture replaces only a picture") {
		t.Fatal(args)
	}
	args, _ = callImage(run, map[string]any{"prompt": " ", "action": "append"}, harbour, nil)
	if !strings.Contains(notGenerated(args), "`prompt` is empty") || run.made.Load() != 0 {
		t.Fatal(args, run.made.Load())
	}
}

func TestWhatTheModelWroteUnderImageIsNeverTakenForAPicture(t *testing.T) {
	args, announced := callImage(offline(), map[string]any{"prompt": "", "action": "append", "image": map[string]any{"id": "forged"}}, harbour, nil)
	if announced || args["image"] != nil || !strings.HasPrefix(outcomeOf(args), "Not generated") {
		t.Fatal(args)
	}
}

func TestAPictureForNewTextIsWrittenIntoItNotAskedFor(t *testing.T) {
	args, announced := callImage(offline(), map[string]any{"prompt": "boats", "alt": "Boats", "action": "in_text"}, "", nil)
	if announced || !strings.Contains(outcomeOf(args), "Unknown action `in_text`") || !strings.Contains(imageDescription(2), "![alt](textus-picture:ASPECT description)") {
		t.Fatal(args)
	}
}

func TestAPictureMayStandNextToTextProposedAndNotDecidedYet(t *testing.T) {
	run := offline()
	text := "The tide went out before dawn.\n\nThe boats came back at dusk."
	at := func(anchor string) map[string]any {
		return map[string]any{"prompt": "boats", "alt": "Boats", "action": "insert_after", "anchor": anchor}
	}
	// This request's own proposal, which isn't in the empty document.
	if args, _ := callImage(run, at("The boats came back at dusk."), "", []string{text}); args["image"] == nil {
		t.Fatal(args)
	}
	// An earlier request's, still undecided.
	if args, _ := callImage(run, at("The tide went out before dawn."), harbour, nil, UndecidedEdit{Text: text}); args["image"] == nil {
		t.Fatal(args)
	}
	// Text found nowhere is still refused, and spends nothing.
	args, _ := callImage(run, at("Nowhere at all."), harbour, []string{text})
	if !strings.Contains(notGenerated(args), "name the passage by its block") || run.made.Load() != 2 {
		t.Fatal(args, run.made.Load())
	}
}

func TestAPictureNotDecidedYetIsKnownWithItsMarkdown(t *testing.T) {
	call := func(id, image string) aicompat.AgentTurn {
		raw, _ := json.Marshal(map[string]any{"prompt": "p", "alt": "Boats", "action": "insert_after", "anchor": "A.", "image": map[string]any{"id": image, "width": 1, "height": 1}})
		return aicompat.AgentTurn{Kind: aicompat.ToolCalled, CallID: id, Name: GenerateImageToolName, Arguments: string(raw)}
	}
	result := func(id, note string) aicompat.AgentTurn {
		return aicompat.AgentTurn{Kind: aicompat.ToolResult, CallID: id, Output: "Image proposed. " + note}
	}
	history := []aicompat.AgentTurn{
		call("g1", "open1"), result("g1", "Status: the user has NOT yet accepted or rejected this proposed edit."),
		call("g2", "taken"), result("g2", "Status: the user accepted this proposed edit."),
	}
	if got := UndecidedPictures(history); !reflect.DeepEqual(got, []string{"open1"}) {
		t.Fatal(got)
	}
	text := outcomeOf(map[string]any{"alt": "Boats", "action": "insert_after", "image": map[string]any{"id": "open1", "width": 1, "height": 1}})
	if !strings.Contains(text, "![Boats](textus-generated:open1)") {
		t.Fatal(text)
	}
}

func proposal(id string, args map[string]any) aicompat.AgentTurn {
	raw, _ := json.Marshal(args)
	return aicompat.AgentTurn{Kind: aicompat.ToolCalled, CallID: id, Name: ProposeEditToolName, Arguments: string(raw)}
}

func proposalNote(id, note string) aicompat.AgentTurn {
	return aicompat.AgentTurn{Kind: aicompat.ToolResult, CallID: id, Output: "Edit proposed.\n\n[" + note + "]"}
}

func TestAnEditThatWentIntoAnUndecidedOneIsReadIntoIt(t *testing.T) {
	pending := "Status: the user has NOT yet accepted or rejected this proposed edit."
	joined := "Status: this went into the proposed text it changes or goes next to - that proposal now reads with it, and it is accepted or rejected with that proposal."
	history := []aicompat.AgentTurn{
		proposal("a", map[string]any{"action": "replace", "anchor": "The old light.", "text": "The lighthouse shone. Gulls cried."}), proposalNote("a", pending),
		proposal("b", map[string]any{"action": "delete", "anchor": " Gulls cried."}), proposalNote("b", joined),
		proposal("c", map[string]any{"action": "replace", "anchor": "lighthouse", "text": "beacon"}), proposalNote("c", joined),
	}
	if got := UndecidedEdits(history); !reflect.DeepEqual(got, []UndecidedEdit{{Replaced: "The old light.", Text: "The beacon shone."}}) {
		t.Fatal(got)
	}
}

func TestUndecidedEditsAreReadOffTheStatusNotes(t *testing.T) {
	history := []aicompat.AgentTurn{
		proposal("a", map[string]any{"action": "append", "text": "Kept."}),
		proposalNote("a", "Status: the user accepted this proposed edit - the document text in this request includes it."),
		proposal("b", map[string]any{"action": "append", "text": "Open."}),
		proposalNote("b", "Status: the user has NOT yet accepted or rejected this proposed edit. It is still shown to them as a preview."),
	}
	if got := UndecidedEdits(history); !reflect.DeepEqual(got, []UndecidedEdit{{Text: "Open."}}) {
		t.Fatal(got)
	}
}

func TestAPictureIsMadeWhileTheReplyGoesOn(t *testing.T) {
	args, announced := callImage(offline(), map[string]any{"prompt": "boats at dusk", "alt": "Boats", "action": "insert_after", "anchor": "The boats came in at dusk.", "aspect": "16:9"}, harbour, nil)
	// Returned at once, with the frame's shape, before any answer.
	image, _ := args["image"].(map[string]any)
	if !announced || image["pending"] != true || image["width"] != 1536.0 || image["height"] != 1024.0 {
		t.Fatal(args)
	}
	if text := outcomeOf(args); !strings.HasPrefix(text, "Image proposed and being made") || !strings.Contains(text, "don't wait for it") {
		t.Fatal(text)
	}
	// Nothing answers here: the frame learns why.
	if _, err := waitPicture(t, image["id"].(string)); err == nil || !strings.HasPrefix(err.Error(), "Couldn't reach the image service") {
		t.Fatal(err)
	}
}

func TestTheFrameWaitsForThePictureAndGetsIt(t *testing.T) {
	id := newPicture()
	if _, _, done := LookPicture(id); done {
		t.Fatal("made at once")
	}
	var data GeneratedImageData
	var err error
	var wg sync.WaitGroup
	wg.Go(func() { data, err = waitPicture(t, id) })
	time.Sleep(400 * time.Millisecond)
	ready(id, 4)
	wg.Wait()
	if err != nil || data.DataBase64 != "AQID" || data.Width != 4 {
		t.Fatal(data, err)
	}
}

func TestAPictureLetGoWhilePaintingStaysGone(t *testing.T) {
	id := newPicture()
	DiscardPicture(id)
	finishPicture(id, pictureSlot{state: slotFailed, why: "late"})
	if _, err := waitPicture(t, id); err == nil || !strings.Contains(err.Error(), "no longer available") {
		t.Fatal(err)
	}
}

func TestStoppingARequestStopsItsPicturesStillBeingMade(t *testing.T) {
	run := runAt(silent(t)).ForRequest("r1")
	s := run.start("a lighthouse", aicompat.Square, nil)
	time.Sleep(200 * time.Millisecond)
	StopPictures("r2")
	if _, _, done := LookPicture(s.id); done {
		t.Fatal("another request's Stop leaves it be")
	}
	StopPictures("r1")
	if _, err := waitPicture(t, s.id); err == nil || err.Error() != stoppedPicture {
		t.Fatal(err)
	}
}

func TestStoppingARequestKeepsAPictureAlreadyMade(t *testing.T) {
	run := runAt(silent(t)).ForRequest("r3")
	s := run.start("a harbour", aicompat.Square, nil)
	time.Sleep(200 * time.Millisecond)
	// In before the Stop, its task not yet tidied away.
	ready(s.id, 4)
	StopPictures("r3")
	if data, err := waitPicture(t, s.id); err != nil || data.Width != 4 {
		t.Fatal(data, err)
	}
}

func TestASendMakesAtMostTwoPictures(t *testing.T) {
	run := offline()
	args := func() map[string]any { return map[string]any{"prompt": "nets", "alt": "Nets", "action": "append"} }
	for range PerSend {
		if _, made := callImage(run, args(), harbour, nil); !made {
			t.Fatal("not made")
		}
	}
	third, announced := callImage(run, args(), harbour, nil)
	// The limit, by its number, for the chat to say in the user's language.
	if announced || !strings.Contains(notGenerated(third), "already made 2 pictures") || third["limit_reached"] != float64(PerSend) {
		t.Fatal(third)
	}
}

func TestAKeptPictureIsProposedAndCanBeLetGo(t *testing.T) {
	id := newPicture()
	ready(id, 4)
	if data, err := waitPicture(t, id); err != nil || data.Mime != "image/png" || data.DataBase64 != "AQID" {
		t.Fatal(data, err)
	}
	text := outcomeOf(map[string]any{"image": map[string]any{"id": id, "width": 4, "height": 3}})
	if !strings.HasPrefix(text, "Image proposed") || !strings.Contains(text, "4 x 3 pixels") {
		t.Fatal(text)
	}
	DiscardPicture(id)
	if _, err := waitPicture(t, id); err == nil {
		t.Fatal("still kept")
	}
}

func TestTheToolOffersInsertionsReplacingAPictureAndTheFiveAspects(t *testing.T) {
	properties := imageSpec(PerSend).Parameters.(map[string]any)["properties"].(map[string]any)
	if !reflect.DeepEqual(properties["action"].(map[string]any)["enum"], ImageActions) || !reflect.DeepEqual(properties["aspect"].(map[string]any)["enum"], aicompat.Aspects) {
		t.Fatal(properties)
	}
}

func TestAPictureInTheDocumentCanBeRedone(t *testing.T) {
	doc := harbour + "\n\n![Boats at dusk](assets/boats.png)"
	args, announced := callImage(offline(), map[string]any{"prompt": "the boats at dawn instead", "alt": "Boats at dawn", "action": "replace", "anchor": "![Boats at dusk](assets/boats.png)"}, doc, nil)
	if image, _ := args["image"].(map[string]any); !announced || image["pending"] != true {
		t.Fatal(args)
	}
	// Only one that is there, and only a whole picture.
	args, _ = callImage(offline(), map[string]any{"prompt": "x", "action": "replace", "anchor": "![Nets](assets/nets.png)"}, doc, nil)
	if !strings.Contains(notGenerated(args), "name the passage by its block") {
		t.Fatal(args)
	}
	if !IsPictureMarkdown("![a](b.png)") || !IsPictureMarkdown("![](<my file.png>)") || IsPictureMarkdown("![a](b.png) and text") || IsPictureMarkdown("The boats came in at dusk.") {
		t.Fatal("pictures judged wrong")
	}
}

func TestTheLimitIsTheSettingWithinBoundsAndTheModelIsToldIt(t *testing.T) {
	n := func(v int) *int { return &v }
	if perSend(nil) != PerSend || perSend(n(4)) != 4 || perSend(n(0)) != 1 || perSend(n(50)) != MaxPerSend {
		t.Fatal("limits")
	}
	run := offline()
	run.limit = 3
	args := func() map[string]any { return map[string]any{"prompt": "nets", "alt": "Nets", "action": "append"} }
	for range 3 {
		if _, made := callImage(run, args(), harbour, nil); !made {
			t.Fatal("not made")
		}
	}
	if fourth, _ := callImage(run, args(), harbour, nil); !strings.Contains(notGenerated(fourth), "already made 3 pictures") || !strings.Contains(imageDescription(3), "at most 3") {
		t.Fatal(fourth)
	}
}

func TestTheImageModelFollowsTheChatProvider(t *testing.T) {
	for _, c := range []struct {
		chat     string
		signedIn bool
		want     string
	}{
		// OpenAI: the sign-in's pictures while signed in, the key's else.
		{"openai", true, "openai-chatgpt"}, {"openai", false, "openai"},
		// A provider with pictures of its own, on the same key.
		{"google", false, "google"}, {"doubao", true, "doubao"},
		// One that makes none, and the compatible endpoint, which is a
		// different server from chat's: no pictures.
		{"anthropic", false, ""}, {"deepseek", false, ""}, {"custom", false, ""},
	} {
		if got, _ := FollowingChat(c.chat, c.signedIn); got != c.want {
			t.Fatal(c, got)
		}
	}
}

func TestEveryBuiltInImageProviderHasAnHTTPSAddressAndADefaultModel(t *testing.T) {
	for _, e := range ImageProviders {
		switch {
		case e.ID == "custom":
			if e.BaseURL != nil || len(e.Models) > 0 {
				t.Fatal(e)
			}
		// The sign-in has no address or key, only its one model.
		case e.API == aicompat.ChatGPTImages:
			if e.BaseURL != nil || e.KeyID != "" || len(e.Models) != 1 {
				t.Fatal(e)
			}
		case e.BaseURL == nil || !strings.HasPrefix(*e.BaseURL, "https://") || len(e.Models) == 0:
			t.Fatal(e.ID)
		}
	}
	// A chat provider that makes pictures (the capability table says which,
	// and how) is offered with its own key.
	for _, chat := range Catalog {
		api := aicompat.Provider(chat.ID).Image
		entry, offered := FindImageProvider(chat.ID)
		switch {
		case api != "" && offered:
			if entry.API != api || entry.KeyID != chat.ID {
				t.Fatal(chat.ID)
			}
		case api == "" && offered:
			if entry.ID != "custom" {
				t.Fatal(chat.ID)
			}
		case api != "":
			t.Fatalf("%s makes pictures but isn't offered", chat.ID)
		}
	}
}

func TestAPictureThatWasNotMadeIsAskedForAgainByItself(t *testing.T) {
	run := runAt(silent(t))
	failing := run.start("A red boat", aicompat.Square, nil)
	other := run.start("A blue boat", aicompat.Square, nil)
	finishPicture(failing.id, pictureSlot{state: slotFailed, why: "offline"})
	finishPicture(other.id, pictureSlot{state: slotFailed, why: "offline"})
	if err := RetryPicture(failing.id); err != nil {
		t.Fatal(err)
	}
	// Being made again: retried twice at once, the second is refused.
	if _, _, done := LookPicture(failing.id); done || RetryPicture(failing.id) == nil {
		t.Fatal("retried twice")
	}
	// The other picture was left as it was.
	if _, err, done := LookPicture(other.id); !done || err == nil {
		t.Fatal("the other changed")
	}
	if RetryPicture("gone00") == nil {
		t.Fatal("a picture that is gone")
	}
}

func TestAPictureIsDrawnAgainFromTheWritersDescription(t *testing.T) {
	run := runAt(silent(t))
	s := run.start("A red boat", aicompat.Square, nil)
	if prompt, _ := PicturePrompt(s.id); prompt != "A red boat" {
		t.Fatal(prompt)
	}
	// Still being made: not drawn again over it.
	if RedrawPicture(s.id, "A red boat at dawn") == nil {
		t.Fatal("drawn over a picture being made")
	}
	finishPicture(s.id, pictureSlot{state: slotFailed, why: "offline"})
	// An empty description draws nothing.
	if RedrawPicture(s.id, "  ") == nil {
		t.Fatal("an empty description")
	}
	if err := RedrawPicture(s.id, "A red boat at dawn"); err != nil {
		t.Fatal(err)
	}
	if prompt, _ := PicturePrompt(s.id); prompt != "A red boat at dawn" {
		t.Fatal(prompt)
	}
	if _, ok := PicturePrompt("gone00"); ok || RedrawPicture("gone00", "A boat") == nil {
		t.Fatal("a picture that is gone")
	}
}

func TestAPictureIsDrawnLikeAnEarlierOneThatIsHereNow(t *testing.T) {
	pngBytes := []byte("\x89PNG\r\n\x1a\n")
	run := offline().InFolder(func(src string) (aicompat.ImageAttachment, bool) {
		return aicompat.ImageAttachment{Name: src, Mime: "image/png", DataBase64: "iVBORw0KGgo="}, src == "assets/lin.png"
	})
	// A service that takes no picture: none sent.
	if run.like("assets/lin.png") != nil {
		t.Fatal("sent to a service that takes none")
	}
	run.job.API = aicompat.ChatGPTImages
	if r := run.like("assets/lin.png"); r == nil || !r.Like {
		t.Fatal(r)
	}
	if run.like("assets/not-there.png") != nil || run.like("") != nil {
		t.Fatal("a picture that isn't there")
	}
	// One proposed before: once made, not while it is being made.
	id := newPicture()
	src := PlaceholderSrc(id)
	if run.like(src) != nil {
		t.Fatal("waited for")
	}
	finishPicture(id, pictureSlot{state: slotReady, image: aicompat.GeneratedImage{Bytes: pngBytes, Mime: "image/png", Ext: "png", Width: 1, Height: 1}})
	reference := run.like(src)
	if reference == nil || !reference.Like || reference.Picture.Mime != "image/png" {
		t.Fatal(reference)
	}
	// The service is told it is a new picture, kept like that one.
	sent, attached := (&asked{job: run.job, prompt: "Lin asleep", reference: reference}).sent()
	if !strings.HasPrefix(sent, "A new picture, not a copy") || !strings.HasSuffix(sent, "Lin asleep") || attached == nil {
		t.Fatal(sent)
	}
}

// An edit into undecided text: a title asked for above undecided writing
// is a paragraph of its own, not joined to the writing's first line.
func TestAnEditIntoUndecidedTextIsMadeAsThePageMakesIt(t *testing.T) {
	text := "雨下到深夜，街上很静。\n\n雨没有停。"
	for _, c := range [][5]string{
		{text, "insert_before", "雨下到深夜，街上很静。", "# 雨夜", "# 雨夜\n\n雨下到深夜，街上很静。\n\n雨没有停。"},
		// Before a later paragraph: between the two.
		{text, "insert_before", "雨没有停。", "## 后来", "雨下到深夜，街上很静。\n\n## 后来\n\n雨没有停。"},
		// Words before words within a paragraph: no space after CJK.
		{text, "insert_before", "街上很静。", "整条", "雨下到深夜，整条街上很静。\n\n雨没有停。"},
		// English words: a space between them.
		{"The rain fell.", "insert_after", "The rain", "softly", "The rain softly fell."},
	} {
		if got, ok := editedText(c[0], c[1], c[2], c[3]); !ok || got != c[4] {
			t.Fatalf("%q", got)
		}
	}
}

func TestAPictureStillBeingMadeIsNotLetGoForRoom(t *testing.T) {
	failed := func(id string) pendingPicture { return pendingPicture{id, pictureSlot{state: slotFailed}} }
	list := trimPictures([]pendingPicture{{id: "making"}, failed("old"), failed("new")}, 2)
	var ids []string
	for _, p := range list {
		ids = append(ids, p.id)
	}
	if !reflect.DeepEqual(ids, []string{"making", "new"}) {
		t.Fatal(ids)
	}
	// All being made: none goes.
	if got := trimPictures([]pendingPicture{{id: "a"}, {id: "b"}}, 1); len(got) != 2 {
		t.Fatal(got)
	}
}

func TestAPictureLineHasAnAltAnAspectAndADescription(t *testing.T) {
	for line, want := range map[string]pictureLine{
		"![港口](textus-picture:16:9 A harbour at dawn (soft light))": {alt: "港口", prompt: "A harbour at dawn (soft light)", aspect: aicompat.Wide},
		// Drawn like an earlier picture: its src after the aspect, or alone.
		"![Lin](textus-picture:16:9 like:assets/lin.png Lin at the helm)": {alt: "Lin", prompt: "Lin at the helm", aspect: aicompat.Wide, like: "assets/lin.png"},
		"![Lin](textus-picture:like:textus-generated:ab12cd Lin asleep)":  {alt: "Lin", prompt: "Lin asleep", like: "textus-generated:ab12cd"},
		"  ![x](textus-picture: Boats)  ":                                 {alt: "x", prompt: "Boats"},
	} {
		if got, ok := readPictureLine(line); !ok || got != want {
			t.Fatal(line, got)
		}
	}
	for _, line := range []string{"![x](textus-picture:1:1 like:assets/lin.png)", "![x](textus-picture:)", "![x](picture.png)", "Text ![x](textus-picture:Boats) more"} {
		if got, ok := readPictureLine(line); ok {
			t.Fatal(line, got)
		}
	}
}

func TestAStringStreamingInIsReadAsFarAsItHasCome(t *testing.T) {
	if _, _, ok := partialString(`{"action":"append","te`, "text"); ok {
		t.Fatal("read before its quote")
	}
	for _, c := range []struct {
		json, want string
		closed     bool
	}{
		{`{"anchor":"say \"text\": no","text":"a\nb`, "a\nb", false},
		{`{"text":"港😀!"}`, "港😀!", true},
		{`{"text":"ab\u6e`, "ab", false},
		{`{"text":"😀 港"}`, "😀 港", true},
	} {
		if got, closed, ok := partialString(c.json, "text"); !ok || got != c.want || closed != c.closed {
			t.Fatalf("%s: %q %v", c.json, got, closed)
		}
	}
}

// streamedPictures feeds json to pictures as call callID's arguments, three
// characters at a time, as a provider might send them; the pictures
// started, as "line -> id".
func streamedPictures(pictures *InlinePictures, callID, json string) []string {
	pictures.Observe(aicompat.ProviderEvent{Kind: aicompat.EventToolStart, CallID: callID, Name: ProposeEditToolName})
	var started []string
	chars := []rune(json)
	for i := 0; i < len(chars); i += 3 {
		for _, e := range pictures.Observe(aicompat.ProviderEvent{Kind: aicompat.EventToolArgs, CallID: callID, Text: string(chars[i:min(i+3, len(chars))])}) {
			if e.Type == "picture" {
				started = append(started, e.Line+" -> "+e.ID)
			}
		}
	}
	return started
}

func appendText(text string) string {
	raw, _ := json.Marshal(map[string]any{"action": "append", "text": text}, json.Deterministic(true))
	return string(raw)
}

func TestEachPictureStartsOnceItsLineHasStreamedIn(t *testing.T) {
	d := DocumentFromMarkdown("")
	pictures := OfflineImageRun(2).Inline(NewToolContext(&d))
	args := appendText("One.\n\n![港口](textus-picture:16:9 Harbour)\n\nTwo.\n\n![船](textus-picture:Boats)")
	started := streamedPictures(pictures, "c1", args)
	if len(started) != 2 || !strings.HasPrefix(started[0], "![港口](textus-picture:16:9 Harbour) -> ") {
		t.Fatal(started)
	}
	// The finished step: the same two pictures, now in the text.
	step := pictures.Place(aicompat.StepResult{Calls: []aicompat.AgentTurn{{Kind: aicompat.ToolCalled, CallID: "c1", Name: ProposeEditToolName, Arguments: args}}})
	placed, _ := ParseArgs(step.Calls[0].Arguments)
	text := placed.StrOr("text")
	first := started[0][strings.LastIndex(started[0], " ")+1:]
	if !strings.Contains(text, fmt.Sprintf("![港口](textus-generated:%s)", first)) || strings.Count(text, "textus-generated:") != 2 || strings.Contains(text, PictureScheme) {
		t.Fatal(text)
	}
}

func TestAPicturePastTheLimitIsLeftOutAndSaidWhy(t *testing.T) {
	d := DocumentFromMarkdown("")
	tc := NewToolContext(&d)
	pictures := OfflineImageRun(1).Inline(tc)
	placed, ok := pictures.placeIn(appendText("One.\n\n![a](textus-picture:A)\n\nTwo.\n\n![b](textus-picture:B)\n\nThree."))
	args, _ := ParseArgs(placed)
	text := args.StrOr("text")
	if !ok || strings.Count(text, "textus-generated:") != 1 || !strings.HasSuffix(text, "Two.\n\nThree.") {
		t.Fatal(text)
	}
	if note := tc.proposed.takeUnmade(); !strings.Contains(note, "1 pictures") {
		t.Fatal(note)
	}
}

func TestALineAskedForAgainIsTheSamePicture(t *testing.T) {
	d := DocumentFromMarkdown("")
	tc := NewToolContext(&d)
	pictures := OfflineImageRun(2).Inline(tc)
	raw, _ := json.Marshal(map[string]any{"text": "![a](textus-picture:A)\n"})
	first := streamedPictures(pictures, "c1", string(raw))
	// A retried step writes the line again: the same picture, told again
	// for the new call, and no second one made.
	if len(first) != 1 || !slices.Equal(streamedPictures(pictures, "c2", string(raw)), first) || len(tc.proposed.pictures) != 1 {
		t.Fatal(first, tc.proposed.pictures)
	}
}

func TestPicturesPlacedInTextThatDidntStream(t *testing.T) {
	placed, unmade := PlacePictures(OfflineImageRun(1), "One.\n\n![a](textus-picture:A)\n\nTwo.\n\n![b](textus-picture:B)")
	if strings.Count(placed, "textus-generated:") != 1 || len(unmade) != 1 || strings.Contains(placed, PictureScheme) {
		t.Fatal(placed, unmade)
	}
}
