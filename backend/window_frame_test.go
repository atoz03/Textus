package backend

import (
	"encoding/json/v2"
	"reflect"
	"runtime"
	"testing"
)

var (
	frameLeast = [2]float64{480, 320}
	laptop     = screenArea{x: 0, y: 25, width: 1440, height: 850}
	monitor    = screenArea{x: 1440, y: 0, width: 2560, height: 1415}
)

func pt(x, y float64) *[2]float64 { return &[2]float64{x, y} }

func expectPlace(t *testing.T, gotAt *[2]float64, gotSize [2]float64, wantAt *[2]float64, wantSize [2]float64) {
	t.Helper()
	if !reflect.DeepEqual(gotAt, wantAt) || gotSize != wantSize {
		t.Errorf("placed at %v, %v; want %v, %v", gotAt, gotSize, wantAt, wantSize)
	}
}

func TestAWindowOpensWhereTheLastOneClosedOnAScreenStillThere(t *testing.T) {
	pos, size := place(pt(300, 100), [2]float64{1000, 700}, []screenArea{laptop}, frameLeast)
	expectPlace(t, pos, size, pt(300, 100), [2]float64{1000, 700})
	// On the second screen it was left on.
	pos, _ = place(pt(1800, 300), [2]float64{1200, 800}, []screenArea{laptop, monitor}, frameLeast)
	if *pos != [2]float64{1800, 300} {
		t.Errorf("second screen %v", pos)
	}
	// That screen gone: centred on the main one.
	pos, size = place(pt(1800, 300), [2]float64{1200, 800}, []screenArea{laptop}, frameLeast)
	expectPlace(t, pos, size, pt(120, 50), [2]float64{1200, 800})
}

func TestAWindowFitsItsScreen(t *testing.T) {
	// 2000x1200 from the monitor, on the laptop's 1440x850.
	pos, size := place(nil, [2]float64{2000, 1200}, []screenArea{laptop}, frameLeast)
	expectPlace(t, pos, size, pt(0, 25), [2]float64{1440, 850})
	// The first run's size on a smaller screen, centred.
	small := screenArea{width: 1280, height: 680}
	pos, size = place(nil, [2]float64{firstRunWidth, firstRunHeight}, []screenArea{small}, frameLeast)
	expectPlace(t, pos, size, pt(40, 0), [2]float64{1200, 680})
	// On a roomy screen it keeps its size, centred.
	pos, size = place(nil, [2]float64{firstRunWidth, firstRunHeight}, []screenArea{monitor}, frameLeast)
	expectPlace(t, pos, size, pt(2120, 308), [2]float64{1200, 800})
	// Half off the edge, moved back in.
	pos, size = place(pt(1200, 600), [2]float64{1000, 700}, []screenArea{laptop}, frameLeast)
	expectPlace(t, pos, size, pt(440, 175), [2]float64{1000, 700})
	// Only a sliver left on screen counts as gone.
	pos, _ = place(pt(1380, 200), [2]float64{1000, 700}, []screenArea{laptop}, frameLeast)
	if *pos != [2]float64{220, 100} {
		t.Errorf("sliver %v", pos)
	}
	// Never below the least size.
	tiny := laptop
	tiny.width, tiny.height = 400, 250
	if _, size := place(nil, [2]float64{1000, 700}, []screenArea{tiny}, frameLeast); size != frameLeast {
		t.Errorf("tiny %v", size)
	}
	// No screens known: as it was.
	pos, size = place(pt(10, 20), [2]float64{900, 600}, nil, frameLeast)
	expectPlace(t, pos, size, pt(10, 20), [2]float64{900, 600})
}

func TestAWindowFillingAMacScreenKeepsItsOwnFrame(t *testing.T) {
	if modeOf(true, true) != modeFullscreen || modeOf(false, false) != modeNormal {
		t.Error("full screen or normal")
	}
	filled := modeMaximized
	if runtime.GOOS == "darwin" {
		filled = modeNormal
	}
	if got := modeOf(false, true); got != filled {
		t.Errorf("filled %s", got)
	}
}

var normalFrame = keptFrame{Size: [2]float64{1000, 700}, Position: pt(300, 100), Mode: modeNormal}

func live(size [2]float64, pos *[2]float64, mode windowMode, minimized bool) liveFrame {
	return liveFrame{frame: keptFrame{Size: size, Position: pos, Mode: mode}, minimized: minimized}
}

func TestAWindowClosedMaximizedKeepsItsNormalBoundsAndReopensMaximized(t *testing.T) {
	filled := live([2]float64{1440, 850}, pt(0, 25), modeMaximized, false)
	// While maximized its bounds are the screen's, not ones to go back to.
	if normalBounds(filled) != nil {
		t.Error("maximized bounds kept as normal")
	}
	normal := normalFrame
	kept := onClose(filled, &normal)
	want := normalFrame
	want.Mode = modeMaximized
	if kept == nil || !reflect.DeepEqual(*kept, want) {
		t.Fatalf("kept %+v", kept)
	}
	if got := reopen(*kept, []screenArea{laptop}, frameLeast); !reflect.DeepEqual(got, *kept) {
		t.Errorf("reopened %+v", got)
	}
}

func TestAWindowClosedFullScreenReopensFullScreen(t *testing.T) {
	full := live([2]float64{1440, 900}, pt(0, 0), modeFullscreen, false)
	normal := normalFrame
	if kept := onClose(full, &normal); kept.Mode != modeFullscreen {
		t.Errorf("mode %s", kept.Mode)
	}
	// Never seen otherwise: what it has now, which reopening fits.
	kept := onClose(full, nil)
	if kept.Size != [2]float64{1440, 900} {
		t.Errorf("size %v", kept.Size)
	}
	if got := reopen(*kept, []screenArea{laptop}, frameLeast); got.Size != [2]float64{1440, 850} {
		t.Errorf("reopened %v", got.Size)
	}
}

func TestAWindowClosedNormalKeepsWhatItHasAndMinimizedWhatItHad(t *testing.T) {
	now := live([2]float64{900, 600}, pt(50, 60), modeNormal, false)
	normal := normalFrame
	if b := normalBounds(now); b == nil || !reflect.DeepEqual(*b, now.frame) {
		t.Errorf("normal bounds %+v", b)
	}
	if kept := onClose(now, &normal); !reflect.DeepEqual(*kept, now.frame) {
		t.Errorf("closed normal %+v", kept)
	}
	away := live([2]float64{160, 30}, pt(-32000, -32000), modeNormal, true)
	if normalBounds(away) != nil {
		t.Error("minimized bounds kept")
	}
	if kept := onClose(away, &normal); !reflect.DeepEqual(*kept, normalFrame) {
		t.Errorf("closed minimized %+v", kept)
	}
	if onClose(away, nil) != nil {
		t.Error("a minimized window never seen kept something")
	}
}

func TestAMaximizedWindowWhoseScreenIsGoneMaximizesOnTheMainOne(t *testing.T) {
	kept := keptFrame{Mode: modeMaximized, Size: [2]float64{1200, 800}, Position: pt(1800, 300)}
	opening := reopen(kept, []screenArea{laptop}, frameLeast)
	if opening.Mode != modeMaximized || *opening.Position != [2]float64{120, 50} {
		t.Errorf("opening %+v %v", opening, *opening.Position)
	}
}

func TestTheKeptFrameReadsBackAsWritten(t *testing.T) {
	kept := normalFrame
	kept.Mode = modeFullscreen
	raw, err := json.Marshal(kept)
	if err != nil || string(raw) != `{"size":[1000,700],"position":[300,100],"mode":"fullscreen"}` {
		t.Fatalf("%s %v", raw, err)
	}
	var back keptFrame
	if err := json.Unmarshal(raw, &back); err != nil || !reflect.DeepEqual(back, kept) {
		t.Errorf("%+v %v", back, err)
	}
	// Wayland's, with no place; and one an older build wrote.
	var sized keptFrame
	if err := json.Unmarshal([]byte(`{"size":[800,600]}`), &sized); err != nil || sized.Position != nil || sized.mode() != modeNormal {
		t.Errorf("%+v %v", sized, err)
	}
	var rust keptFrame
	if err := json.Unmarshal([]byte(`{"size":[1000.0,700.0],"position":[300.0,100.0],"mode":"maximized"}`), &rust); err != nil || rust.mode() != modeMaximized || rust.Size != [2]float64{1000, 700} {
		t.Errorf("%+v %v", rust, err)
	}
}

func TestEachWindowsFrameSurvivesThePagesUpdatesAndTheFile(t *testing.T) {
	frame := keptFrame{Size: [2]float64{1000, 700}, Position: pt(40, 60), Mode: modeMaximized}
	sessions := map[string]WindowSession{
		"main":     {Paths: []string{"one.md"}},
		"window-2": {Paths: []string{"two.md"}, Frame: &frame},
	}
	// The page never sends a frame; the one the backend kept stays.
	sent := WindowSession{Paths: []string{"two.md", "three.md"}}
	session := withKeptFrame(sessions, "window-2", sent)
	if session.Frame == nil || !reflect.DeepEqual(*session.Frame, frame) {
		t.Fatalf("frame %+v", session.Frame)
	}
	sessions["window-2"] = session
	raw, err := json.Marshal(mergeWindows(sessions))
	if err != nil {
		t.Fatal(err)
	}
	var read []WindowSession
	if err := json.Unmarshal(raw, &read); err != nil {
		t.Fatal(err)
	}
	if len(read) != 2 || read[0].Frame != nil || read[1].Frame == nil || !reflect.DeepEqual(*read[1].Frame, frame) {
		t.Errorf("read %s", raw)
	}
	// A frame kept as the window closes: written only when it changed.
	if setFrame(sessions, "window-2", &frame) {
		t.Error("the same frame changed something")
	}
	if !setFrame(sessions, "main", &frame) || sessions["main"].Frame == nil {
		t.Error("main's frame not kept")
	}
	if setFrame(sessions, "window-9", &frame) {
		t.Error("a window without a session kept a frame")
	}
}

func TestEachRestoredWindowTakesTheSessionBuiltForIt(t *testing.T) {
	a := &App{pendingSessions: map[string]WindowSession{
		"window-s1": sessionOfPaths([]string{"/a.md"}),
		"window-s2": sessionOfPaths([]string{"/b.md"}),
	}}
	// Mounted out of order, each still gets its own documents.
	if s, ok := a.takeSessionFor("window-s2"); !ok || s.Paths[0] != "/b.md" {
		t.Errorf("s2 %+v", s)
	}
	if s, ok := a.takeSessionFor("window-s1"); !ok || s.Paths[0] != "/a.md" {
		t.Errorf("s1 %+v", s)
	}
	if _, ok := a.takeSessionFor("window-s1"); ok {
		t.Error("taken twice")
	}
}
