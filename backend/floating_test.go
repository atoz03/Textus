package backend

import (
	"runtime"
	"strings"
	"testing"

	"github.com/egoist/mygo"
)

func TestAViewIDBecomesAFloatingWindowsLabel(t *testing.T) {
	if got := floatingLabel("catvinci.desk-pet.pet"); got != "floating-catvinci_desk-pet_pet" {
		t.Errorf("label: %s", got)
	}
	// Never another kind's label, whatever the id.
	for _, view := range []string{"a.b.c", "../x", "main", "window-1", "é"} {
		if kindOf(floatingLabel(view)) != kindFloating {
			t.Errorf("%q: %q isn't a floating window's", view, floatingLabel(view))
		}
	}
}

func TestAFloatingWindowOpensByItsDisplaysCorner(t *testing.T) {
	area := mygo.Rectangle{X: 0, Y: 25, Width: 1440, Height: 875}
	if got := cornerIn(area, 200, 100); got != [2]float64{1440 - 200 - 32, 900 - 100 - 32} {
		t.Errorf("corner: %v", got)
	}
	if got := cornerIn(mygo.Rectangle{}, 200, 100); got != [2]float64{40, 40} {
		t.Errorf("no display: %v", got)
	}
}

func TestAResizedFloatingWindowKeepsTheRightCorner(t *testing.T) {
	b := mygo.Rectangle{X: 100, Y: 200, Width: 300, Height: 400}
	// Folded or unfolded: the top-left corner stays.
	if got := resizedBounds(b, 120, 80, nil); got != (mygo.Rectangle{X: 100, Y: 200, Width: 120, Height: 80}) {
		t.Errorf("folded: %+v", got)
	}
	// Resized by the user: the bottom stays, and the outer edge.
	right, left := true, false
	if got := resizedBounds(b, 200, 300, &right); got != (mygo.Rectangle{X: 200, Y: 300, Width: 200, Height: 300}) {
		t.Errorf("by its left edge: %+v", got)
	}
	if got := resizedBounds(b, 200, 300, &left); got != (mygo.Rectangle{X: 100, Y: 300, Width: 200, Height: 300}) {
		t.Errorf("by its right edge: %+v", got)
	}
}

func TestAFloatingTabDropsOnTheFrontWindowUnderIt(t *testing.T) {
	placed := []placedWindow{
		{label: "window-2", bounds: mygo.Rectangle{X: 0, Y: 0, Width: 800, Height: 600}, rank: 1},
		{label: "main", bounds: mygo.Rectangle{X: 400, Y: 300, Width: 800, Height: 600}, rank: 0},
	}
	// Where they overlap, the one focused last.
	if hit := hitAmong(placed, 500, 350, 80); hit == nil || hit.label != "main" || !hit.row {
		t.Errorf("overlap: %+v", hit)
	}
	// Below its row: its documents.
	if hit := hitAmong(placed, 100, 100, 80); hit == nil || hit.label != "window-2" || hit.row {
		t.Errorf("documents: %+v", hit)
	}
	if hit := hitAmong(placed, 2000, 2000, 80); hit != nil {
		t.Errorf("outside: %+v", hit)
	}
}

func TestWindowBoundsAreInTheirDisplaysPixels(t *testing.T) {
	b := mygo.Rectangle{X: 10, Y: 20, Width: 800, Height: 600}
	got := inPixels("main", b, 2)
	want := windowBounds{Label: "main", X: 20, Y: 40, Width: 1600, Height: 1200, ScaleFactor: 2}
	if got != want {
		t.Errorf("at 2x: %+v", got)
	}
	// A display that gives no scale is taken as 1x: the page divides by it.
	if got := inPixels("main", b, 0); got.ScaleFactor != 1 || got.Width != 800 {
		t.Errorf("no scale: %+v", got)
	}
}

func TestFloatingAndDragCommandsWithNoWindow(t *testing.T) {
	a := pluginApp(t)
	want := map[string]string{
		"floating_desktop_supported": "false",
		"list_window_bounds":         "[]",
		"start_window_drag_tracking": "false",
		"floating_window_position":   "null",
		"close_floating_window":      "null",
		"resize_floating_window":     "null",
		"show_floating_owner":        "null",
	}
	if runtime.GOOS == "darwin" || runtime.GOOS == "windows" {
		want["floating_desktop_supported"] = "true"
	}
	args := map[string]string{
		"floating_window_position": `{"view":"a.b.c"}`,
		"close_floating_window":    `{"view":"a.b.c"}`,
		"resize_floating_window":   `{"view":"a.b.c","width":10,"height":10,"keepRight":null}`,
		"show_floating_owner":      `{"owner":"floating-x"}`,
	}
	for name, answer := range want {
		arg := args[name]
		if arg == "" {
			arg = "{}"
		}
		got, err := run(a, name, arg)
		if err != nil || got != answer {
			t.Errorf("%s: %s %v", name, got, err)
		}
	}
	// A floating view needs an editor window to belong to.
	if _, err := run(a, "open_floating_window", `{"view":"a.b.c","title":"Pet","at":null,"width":100,"height":100}`); err == nil {
		t.Error("opened with no window")
	}
	// Where nothing follows the pointer, a tab stays in its window.
	if !pointerTracked {
		tab := `{"tab":{"path":null,"content":"x","savedContent":""},"title":"x","dirty":false,"destroySource":false,"rowHeight":80,"look":""}`
		_, err := run(a, "start_floating_tab_drag", tab)
		if err == nil || !strings.Contains(err.Error(), "global cursor tracking") {
			t.Errorf("dragged without the pointer: %v", err)
		}
	}
}
