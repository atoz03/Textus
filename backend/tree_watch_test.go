package backend

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/egoist/mygo"
)

func treeKnown() map[string]string {
	return map[string]string{
		filepath.FromSlash("/real/notes"):        "/link/notes",
		filepath.FromSlash("/real/notes/drafts"): "/link/notes/drafts",
	}
}

var noneOwn = func(string) bool { return false }

func TestAChangeNamesTheOpenFolderItIsInAsTheTreeKnowsIt(t *testing.T) {
	root := filepath.FromSlash("/real/notes")
	made := []mygo.FileEvent{{Op: mygo.FileCreate, Path: "a.md"}}
	if dirs, all := touched(root, made, treeKnown(), noneOwn); all || !slices.Equal(dirs, []string{"/link/notes"}) {
		t.Errorf("made: %v %v", dirs, all)
	}
	// A watched folder removed: the folder it was in, and it.
	gone := []mygo.FileEvent{{Op: mygo.FileRemove, Path: "drafts", IsDir: true}}
	if dirs, _ := touched(root, gone, treeKnown(), noneOwn); !slices.Equal(dirs, []string{"/link/notes", "/link/notes/drafts"}) {
		t.Errorf("gone: %v", dirs)
	}
	// The watched folder itself gone: it, and its folder when open.
	self := []mygo.FileEvent{{Op: mygo.FileRemove, Path: ".", IsDir: true}}
	if dirs, _ := touched(filepath.FromSlash("/real/notes/drafts"), self, treeKnown(), noneOwn); !slices.Equal(dirs, []string{"/link/notes", "/link/notes/drafts"}) {
		t.Errorf("itself: %v", dirs)
	}
}

func TestTempFilesOwnWritesAndUnwatchedFoldersChangeNothing(t *testing.T) {
	root := filepath.FromSlash("/real/notes")
	for _, e := range []mygo.FileEvent{
		{Op: mygo.FileCreate, Path: ".a.md.tmp-123456"},
		{Op: mygo.FileCreate, Path: ".git/index"},
	} {
		if dirs, all := touched(root, []mygo.FileEvent{e}, treeKnown(), noneOwn); len(dirs) != 0 || all {
			t.Errorf("%+v: %v %v", e, dirs, all)
		}
	}
	saved := []mygo.FileEvent{{Op: mygo.FileWrite, Path: "a.md"}}
	own := func(p string) bool { return filepath.Base(p) == "a.md" }
	if dirs, _ := touched(root, saved, treeKnown(), own); len(dirs) != 0 {
		t.Errorf("own save: %v", dirs)
	}
	if !isTempName(".a.md.tmp-1") || isTempName("a.md.tmp-1") || isTempName(".a.md.tmp-x") || isTempName(".tmp-1") {
		t.Error("temp names")
	}
}

func TestOnlyWhatMakesOrWritesAFileCanBeTheAppsOwn(t *testing.T) {
	root := filepath.FromSlash("/real/notes")
	all := func(string) bool { return true }
	for _, e := range []mygo.FileEvent{
		{Op: mygo.FileRemove, Path: "a.md"},
		{Op: mygo.FileRename, OldPath: "a.md", Path: "drafts/a.md"},
	} {
		if dirs, _ := touched(root, []mygo.FileEvent{e}, treeKnown(), all); !slices.Equal(dirs, []string{"/link/notes"}) {
			t.Errorf("%+v: %v", e, dirs)
		}
	}
	written := []mygo.FileEvent{{Op: mygo.FileCreate, Path: "a.md"}}
	if dirs, _ := touched(root, written, treeKnown(), all); len(dirs) != 0 {
		t.Errorf("written: %v", dirs)
	}
	// What the watch can't name has every open folder read again.
	lost := []mygo.FileEvent{{Op: mygo.FileHint, Path: "elsewhere"}}
	if _, everything := touched(root, lost, treeKnown(), all); !everything {
		t.Error("a hint at an unknown folder")
	}
}

// A system that reports the file a save renamed over as removed (Windows):
// while the file is still there, it was the app's own write; once gone, it
// is news.
func TestAnOwnFileReportedRemovedButStillThereWasWrittenOver(t *testing.T) {
	dir := t.TempDir()
	known := map[string]string{dir: "/link/notes"}
	file := filepath.Join(dir, "own.md")
	write := func() {
		if err := os.WriteFile(file, []byte("# Own"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write()
	own := func(string) bool { return true }
	for _, e := range []mygo.FileEvent{
		{Op: mygo.FileRemove, Path: "own.md"},
		{Op: mygo.FileRename, OldPath: "own.md", Path: "away.md"},
	} {
		if dirs, _ := touched(dir, []mygo.FileEvent{e}, known, own); len(dirs) != 0 {
			t.Errorf("%s while there: %v", e.Op, dirs)
		}
		if err := os.Remove(file); err != nil {
			t.Fatal(err)
		}
		if dirs, _ := touched(dir, []mygo.FileEvent{e}, known, own); !slices.Equal(dirs, []string{"/link/notes"}) {
			t.Errorf("%s once gone: %v", e.Op, dirs)
		}
		write()
	}
}

// A save is its own window's alone; a write for no window is every
// window's.
func TestASaveIsItsOwnWindowsAloneAndOneForNoWindowIsEveryWindows(t *testing.T) {
	dir := t.TempDir()
	saved, written := filepath.Join(dir, "saved.md"), filepath.Join(dir, "written.md")
	noteOwnWrite(saved, "a")
	noteOwnWrite(written, "")
	saved, written = realPath(saved), realPath(written)
	if !isOwnWrite(saved, "a") || isOwnWrite(saved, "b") {
		t.Error("a save is its own window's alone")
	}
	if !isOwnWrite(written, "a") || !isOwnWrite(written, "b") {
		t.Error("a write for no window is every window's")
	}
}

func TestWatchedWholeOnlyTheTopmostOpenFoldersAreWatched(t *testing.T) {
	at := filepath.FromSlash
	dirs := []string{at("/n"), at("/n/a"), at("/n/a/b"), at("/other"), at("/nb")}
	whole := watchPlan(dirs, true)
	roots := slices.Sorted(func(yield func(string) bool) {
		for r := range whole {
			if !yield(r) {
				return
			}
		}
	})
	if !slices.Equal(roots, []string{at("/n"), at("/nb"), at("/other")}) {
		t.Errorf("roots %v", roots)
	}
	if p := whole[at("/n")]; !p.whole || !slices.Equal(p.below, []string{at("/n/a"), at("/n/a/b")}) {
		t.Errorf("/n %+v", p)
	}
	alone := watchPlan(dirs, false)
	if len(alone) != len(dirs) {
		t.Errorf("alone %v", alone)
	}
	for _, p := range alone {
		if p.whole {
			t.Error("a folder alone watched whole")
		}
	}
}

func TestAnOlderCallThanTheLastIsPassedOver(t *testing.T) {
	asked := map[string]float64{}
	for _, c := range []struct {
		label string
		seq   float64
		want  bool
	}{{"main", 2, true}, {"main", 1, false}, {"main", 2, false}, {"window-2", 1, true}, {"main", 3, true}} {
		if got := newest(asked, c.label, c.seq); got != c.want {
			t.Errorf("%s %v: %v", c.label, c.seq, got)
		}
	}
}

// gathered runs gather; what it sends comes back on the channel.
func gathered(t *testing.T) (chan<- treeChange, <-chan dirsChanged) {
	changes := make(chan treeChange, 64)
	sent := make(chan dirsChanged, 64)
	ctx := t.Context()
	go gather(ctx, changes, func(c dirsChanged) { sent <- c })
	return changes, sent
}

func TestABurstOfChangesIsSentOnceAfterAQuiet150ms(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		changes, sent := gathered(t)
		for range 3 {
			changes <- treeChange{dirs: []string{"/link/notes"}}
			time.Sleep(100 * time.Millisecond)
		}
		changes <- treeChange{dirs: []string{"/link/notes/drafts"}}
		time.Sleep(140 * time.Millisecond)
		synctest.Wait()
		if len(sent) != 0 {
			t.Fatal("sent before it went quiet")
		}
		time.Sleep(20 * time.Millisecond)
		synctest.Wait()
		if got := <-sent; !slices.Equal(got.Dirs, []string{"/link/notes", "/link/notes/drafts"}) || got.All {
			t.Errorf("sent %+v", got)
		}
		if len(sent) != 0 {
			t.Error("sent twice")
		}
	})
}

func TestChangesThatNeverGoQuietAreStillSentEachSecond(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		changes, sent := gathered(t)
		for range 25 {
			changes <- treeChange{dirs: []string{"/link/notes"}}
			time.Sleep(100 * time.Millisecond)
		}
		synctest.Wait()
		// 2.5 s of changes every 100 ms: sent at 1 s and 2 s.
		if len(sent) != 2 {
			t.Errorf("sent %d times", len(sent))
		}
	})
}

// next is what the watch sends next, within a few seconds.
func next(t *testing.T, sent <-chan dirsChanged) dirsChanged {
	t.Helper()
	select {
	case c := <-sent:
		return c
	case <-time.After(10 * time.Second):
		t.Fatal("nothing sent")
		return dirsChanged{}
	}
}

// quietFor reports whether the watch sends nothing for d.
func quietFor(sent <-chan dirsChanged, d time.Duration) bool {
	select {
	case <-sent:
		return false
	case <-time.After(d):
		return true
	}
}

func drain(sent <-chan dirsChanged) {
	time.Sleep(300 * time.Millisecond)
	for len(sent) > 0 {
		<-sent
	}
}

// The system's own watch, in a real folder: a file made, renamed and
// removed outside the app each reach the tree, by the path it gave.
func TestARealFolderReportsFilesMadeRenamedAndRemoved(t *testing.T) {
	dir := t.TempDir()
	sent := make(chan dirsChanged, 64)
	w := newWindowWatch(func(p string) bool { return isOwnWrite(p, "main") }, func(c dirsChanged) { sent <- c })
	defer w.close()
	if !w.only([]string{dir}, false) {
		t.Fatal("not watched")
	}
	drain(sent)
	expect := func(what string) {
		t.Helper()
		if got := next(t, sent); !slices.Equal(got.Dirs, []string{dir}) {
			t.Errorf("%s: %+v", what, got)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "a.md"), []byte("# A"), 0o644); err != nil {
		t.Fatal(err)
	}
	expect("made")
	if err := os.Rename(filepath.Join(dir, "a.md"), filepath.Join(dir, "b.md")); err != nil {
		t.Fatal(err)
	}
	expect("renamed")
	if err := os.Remove(filepath.Join(dir, "b.md")); err != nil {
		t.Fatal(err)
	}
	expect("removed")

	// A new file the app wrote is news; the same file saved again is not.
	own := filepath.Join(dir, "own.md")
	if err := writeAtomic(own, []byte("# Own")); err != nil {
		t.Fatal(err)
	}
	expect("made by the app")
	drain(sent)
	if err := writeAtomic(own, []byte("# Own, again")); err != nil {
		t.Fatal(err)
	}
	if !quietFor(sent, time.Second) {
		t.Error("an own save was news")
	}
	// Taken away by another app just after: news, however new the write.
	if err := os.Remove(own); err != nil {
		t.Fatal(err)
	}
	expect("removed after a save")

	// Saved from another window: news to this one's tree; from this one, not.
	theirs := filepath.Join(dir, "theirs.md")
	if err := os.WriteFile(theirs, []byte("# Theirs"), 0o644); err != nil {
		t.Fatal(err)
	}
	expect("made by another app")
	drain(sent)
	if err := writeAtomicBy(theirs, []byte("# Theirs, again"), "other"); err != nil {
		t.Fatal(err)
	}
	expect("saved from another window")
	drain(sent)
	if err := writeAtomicBy(theirs, []byte("# Theirs, mine now"), "main"); err != nil {
		t.Fatal(err)
	}
	if !quietFor(sent, time.Second) {
		t.Error("this window's own save was news")
	}

	// The tree reading the folder is not a change: no loop.
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		_, _ = e.Info()
	}
	if !quietFor(sent, time.Second) {
		t.Error("reading the folder was news")
	}
}

// A folder watched whole: what changes in an open folder below reaches the
// tree by that folder; a folded one is never named.
func TestAFolderWatchedWholeReportsItsOpenFoldersAlone(t *testing.T) {
	dir := t.TempDir()
	open, folded := filepath.Join(dir, "open"), filepath.Join(dir, "folded")
	for _, d := range []string{open, folded} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	sent := make(chan dirsChanged, 64)
	w := newWindowWatch(func(p string) bool { return isOwnWrite(p, "main") }, func(c dirsChanged) { sent <- c })
	defer w.close()
	if !w.only([]string{dir, open}, true) {
		t.Fatal("not watched")
	}
	drain(sent)
	if err := os.WriteFile(filepath.Join(folded, "x.md"), []byte("# X"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(open, "a.md"), []byte("# A"), 0o644); err != nil {
		t.Fatal(err)
	}
	var dirs []string
	for !slices.Contains(dirs, open) {
		dirs = append(dirs, next(t, sent).Dirs...)
	}
	for _, d := range dirs {
		if d == folded {
			t.Errorf("a folded folder named: %v", dirs)
		}
	}
	// Opened later, it is watched too.
	if !w.only([]string{dir, open, folded}, true) {
		t.Fatal("not watched again")
	}
	drain(sent)
	if err := os.WriteFile(filepath.Join(folded, "y.md"), []byte("# Y"), 0o644); err != nil {
		t.Fatal(err)
	}
	dirs = nil
	for !slices.Contains(dirs, folded) {
		dirs = append(dirs, next(t, sent).Dirs...)
	}
}
