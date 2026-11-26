package backend

import (
	"slices"
	"testing"
)

// A second launch while Textus starts reached the app before it was
// ready, and opening its window then panicked ("NewWindow called before
// the application is ready"): it waits for ready now, in order.
func TestLaunchesBeforeReadyWaitForIt(t *testing.T) {
	var u untilReady
	var ran []string
	u.run(func() { ran = append(ran, "first") })
	u.run(func() { ran = append(ran, "second") })
	if len(ran) != 0 {
		t.Fatalf("ran before ready: %v", ran)
	}
	u.ready()
	u.run(func() { ran = append(ran, "after") })
	if want := []string{"first", "second", "after"}; !slices.Equal(ran, want) {
		t.Fatalf("ran %v, want %v", ran, want)
	}
	u.ready()
	if len(ran) != 3 {
		t.Fatalf("a second ready ran something again: %v", ran)
	}
}
