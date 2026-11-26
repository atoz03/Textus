package backend

import (
	"context"
	"maps"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo"
)

// The file tree's open folders, watched for what other apps (Git, a sync
// client, Finder) change in them. On macOS and Linux each folder is watched
// alone, not what is below it: a folded or hidden folder costs nothing, and
// .git or an ignored folder is never watched unless the user opens it. On
// Windows a watched folder holds a handle that keeps every folder above it
// from being renamed, moved or trashed, so the topmost open folder is
// watched whole instead, its folded folders passed over, and watched again
// when the folders open below it change. Changes are gathered for 150 ms
// and sent to the window as the folders to read again. A phone's tree reads
// its folders again on focus.

const (
	// treeQuiet: quiet this long, the changes gathered are sent.
	treeQuiet = 150 * time.Millisecond
	// treeMost: changes that never go quiet (a checkout, a sync) are still
	// sent this often.
	treeMost = time.Second
	// ownFor is how long a file the app wrote is taken as its own change.
	ownFor = 2 * time.Second
	// treeEntries is the most entries a folder's watch keeps track of.
	treeEntries = 1 << 16

	eventTreeDirsChanged = "tree-dirs-changed"
)

// watchWhole: a folder is watched whole, its open folders' changes picked
// out, where a watch holds the folder open (Windows).
const watchWhole = runtime.GOOS == "windows"

// dirsChanged is the folders to read again; All: every open one, when the
// watch lost track. Its TypeScript side is `DirsChanged` in
// src/files/useDiskWatch.ts.
type dirsChanged struct {
	Dirs []string `json:"dirs"`
	All  bool     `json:"all"`
}

// treeWatches is each window's watch, and the newest call each window
// made, by label.
type treeWatches struct {
	mu       sync.Mutex
	byWindow map[string]*windowWatch
	asked    map[string]float64
}

// newest reports whether a window's call numbered seq is newer than any
// before it: the calls can arrive out of order.
func newest(asked map[string]float64, label string, seq float64) bool {
	if last, ok := asked[label]; ok && last >= seq {
		return false
	}
	asked[label] = seq
	return true
}

// windowWatch follows the folders open in one window's tree.
type windowWatch struct {
	ctx     context.Context
	stop    context.CancelFunc
	changes chan treeChange
	own     func(path string) bool

	mu sync.Mutex
	// known: the open folders, by their real path (as a watch reports
	// changes), to the path the tree knows each by.
	known map[string]string
	// watched: what is watched, by real path, and how.
	watched map[string]*folderWatch
}

// folderWatch is one folder watched, as plan says.
type folderWatch struct {
	plan   string
	cancel context.CancelFunc
}

// treeChange is what a batch of a folder's changes touched.
type treeChange struct {
	dirs []string
	all  bool
}

// newWindowWatch starts a window's watch, whose changes, gathered, go to
// send; own tells a file the window itself wrote.
func newWindowWatch(own func(string) bool, send func(dirsChanged)) *windowWatch {
	ctx, stop := context.WithCancel(context.Background())
	w := &windowWatch{
		ctx: ctx, stop: stop, changes: make(chan treeChange, 64), own: own,
		known: map[string]string{}, watched: map[string]*folderWatch{},
	}
	go gather(ctx, w.changes, send)
	return w
}

// close stops the window's watch.
func (w *windowWatch) close() { w.stop() }

// folderPlan is how one folder is watched: alone, or whole with the open
// folders below it (by their real paths) picked out.
type folderPlan struct {
	whole bool
	below []string
}

// key tells one plan of a folder from another.
func (p folderPlan) key() string {
	if !p.whole {
		return ""
	}
	return "whole\x00" + strings.Join(p.below, "\x00")
}

// watchPlan is what is watched for the open folders dirs: each alone, or
// with whole, the topmost ones with the open folders below them.
func watchPlan(dirs []string, whole bool) map[string]folderPlan {
	plan := map[string]folderPlan{}
	if !whole {
		for _, d := range dirs {
			plan[d] = folderPlan{}
		}
		return plan
	}
	sorted := slices.Sorted(slices.Values(dirs))
	for _, d := range sorted {
		top := true
		for _, o := range sorted {
			if _, under := pathUnder(d, o); under && o != d {
				top = false
				break
			}
		}
		if !top {
			continue
		}
		var below []string
		for _, o := range sorted {
			if _, under := pathUnder(o, d); under && o != d {
				below = append(below, o)
			}
		}
		plan[d] = folderPlan{whole: true, below: below}
	}
	return plan
}

// only has the watch follow dirs alone, as watchPlan plans them with
// whole; false when one couldn't be watched.
func (w *windowWatch) only(dirs []string, whole bool) bool {
	wanted := map[string]string{}
	for _, dir := range dirs {
		if real, err := filepath.EvalSymlinks(dir); err == nil {
			if real, err = filepath.Abs(real); err == nil {
				wanted[real] = dir
			}
		}
	}
	plan := watchPlan(slices.Collect(maps.Keys(wanted)), whole)
	w.mu.Lock()
	defer w.mu.Unlock()
	w.known = wanted
	for root, f := range w.watched {
		if p, ok := plan[root]; !ok || p.key() != f.plan {
			f.cancel()
			delete(w.watched, root)
		}
	}
	live := true
	for root, p := range plan {
		if _, ok := w.watched[root]; ok {
			continue
		}
		if !w.start(root, p) {
			live = false
		}
	}
	return live
}

// start watches the folder root as p plans; w.mu is held.
func (w *windowWatch) start(root string, p folderPlan) bool {
	opts := mygo.FileWatchOptions{Recursive: p.whole, MaxEntries: treeEntries}
	if p.whole {
		// The folders open below root, and those on the way to them: the
		// rest is passed over, never opened.
		open := map[string]bool{}
		for _, dir := range p.below {
			rel, err := filepath.Rel(root, dir)
			if err != nil {
				continue
			}
			for rel := filepath.ToSlash(rel); rel != "." && rel != "/"; rel = path.Dir(rel) {
				open[rel] = true
			}
		}
		opts.Ignore = func(rel string, isDir bool) bool { return isDir && !open[rel] }
	}
	ctx, cancel := context.WithCancel(w.ctx)
	watcher, err := mygo.WatchFiles(ctx, root, opts)
	if err != nil {
		cancel()
		return false
	}
	f := &folderWatch{plan: p.key(), cancel: cancel}
	w.watched[root] = f
	go w.follow(ctx, watcher, root, f)
	return true
}

// follow hands what changes under root to the window's gathering until the
// watch stops. One that stops by itself (the folder gone, too much in it)
// has every open folder read again, and is watched again on the tree's
// next call.
func (w *windowWatch) follow(ctx context.Context, watcher *mygo.FileWatcher, root string, f *folderWatch) {
	defer watcher.Close()
	for {
		events, err := watcher.Next(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			w.mu.Lock()
			if w.watched[root] == f {
				delete(w.watched, root)
			}
			w.mu.Unlock()
			w.send(ctx, treeChange{all: watcher.Err() != nil})
			return
		}
		w.mu.Lock()
		dirs, all := touched(root, events, w.known, w.own)
		w.mu.Unlock()
		if len(dirs) > 0 || all {
			w.send(ctx, treeChange{dirs: dirs, all: all})
		}
	}
}

func (w *windowWatch) send(ctx context.Context, c treeChange) {
	if !c.all && len(c.dirs) == 0 {
		return
	}
	select {
	case w.changes <- c:
	case <-ctx.Done():
	}
}

// isTempName reports whether path is the temp file a write renames into
// place (writeAtomic's ".<name>.tmp-<digits>").
func isTempName(p string) bool {
	name := filepath.Base(p)
	i := strings.LastIndex(name, ".tmp-")
	if !strings.HasPrefix(name, ".") || i < 1 {
		return false
	}
	digits := name[i+len(".tmp-"):]
	return digits != "" && strings.Trim(digits, "0123456789") == ""
}

// asWritten reports whether a path of an event of op can be one the app
// wrote: made or written over, never taken away. Of a rename's two paths,
// the second is where it went. A system may report the file a save renamed
// over as removed, or renamed away (Windows does): a path still there was
// written over, not taken away.
func asWritten(op mygo.FileOp, index int, path string) bool {
	switch op {
	case mygo.FileCreate, mygo.FileWrite:
		return true
	case mygo.FileRename:
		if index == 1 {
			return true
		}
	case mygo.FileHint:
		return false
	}
	_, err := os.Lstat(path)
	return err == nil
}

// touched is the open folders a batch of the watch of root changed, as the
// tree knows them: the folder each entry is in, and the entry itself when
// it is an open folder (removed, renamed). The app's own writes and its
// temp files change nothing; all: the watch lost track, and every open
// folder is read again.
func touched(root string, events []mygo.FileEvent, known map[string]string, own func(string) bool) (dirs []string, all bool) {
	for _, e := range events {
		if e.Op == mygo.FileHint {
			if given, ok := known[filepath.Join(root, filepath.FromSlash(e.Path))]; ok {
				dirs = append(dirs, given)
			} else {
				all = true
			}
			continue
		}
		paths := []string{e.Path}
		if e.Op == mygo.FileRename {
			paths = []string{e.OldPath, e.Path}
		}
		for i, rel := range paths {
			p := filepath.Join(root, filepath.FromSlash(rel))
			if isTempName(p) || (asWritten(e.Op, i, p) && own(p)) {
				continue
			}
			for _, dir := range []string{filepath.Dir(p), p} {
				if given, ok := known[dir]; ok && !slices.Contains(dirs, given) {
					dirs = append(dirs, given)
				}
			}
		}
	}
	return dirs, all
}

// gather gathers changes until treeQuiet passes with none (or treeMost in
// all), then sends them once. It ends with ctx.
func gather(ctx context.Context, changes <-chan treeChange, send func(dirsChanged)) {
	for {
		dirs := map[string]bool{}
		all := false
		take := func(c treeChange) {
			all = all || c.all
			for _, d := range c.dirs {
				dirs[d] = true
			}
		}
		for len(dirs) == 0 && !all {
			select {
			case <-ctx.Done():
				return
			case c := <-changes:
				take(c)
			}
		}
		started := time.Now()
	quiet:
		for {
			timer := time.NewTimer(min(treeQuiet, time.Until(started.Add(treeMost))))
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case c := <-changes:
				timer.Stop()
				take(c)
			case <-timer.C:
				break quiet
			}
		}
		send(dirsChanged{Dirs: slices.Sorted(maps.Keys(dirs)), All: all})
	}
}

// ownWrites is what the app wrote over lately, when, and the window whose
// save it was: only that window's tree takes it as its own; one for no
// window (""), every window's tree.
var ownWrites struct {
	sync.Mutex
	list []ownWrite
}

type ownWrite struct {
	path string
	at   time.Time
	by   string
}

// realPath is where path really is, links followed; for one not there
// yet, its folder's real path with its name.
func realPath(p string) string {
	if real, err := filepath.EvalSymlinks(p); err == nil {
		if abs, err := filepath.Abs(real); err == nil {
			return abs
		}
	}
	if dir, err := filepath.EvalSymlinks(filepath.Dir(p)); err == nil {
		if abs, err := filepath.Abs(dir); err == nil {
			return filepath.Join(abs, filepath.Base(p))
		}
	}
	return p
}

// noteOwnWrite: the app is about to write over path, for the window
// labelled by or for none (""), so its change is not news to that
// window's tree, or to any.
func noteOwnWrite(p, by string) {
	p = realPath(p)
	now := time.Now()
	ownWrites.Lock()
	defer ownWrites.Unlock()
	ownWrites.list = slices.DeleteFunc(ownWrites.list, func(w ownWrite) bool { return now.Sub(w.at) >= ownFor })
	ownWrites.list = append(ownWrites.list, ownWrite{path: p, at: now, by: by})
}

// isOwnWrite reports whether the window labelled window wrote over path
// lately, or the app did for no window.
func isOwnWrite(p, window string) bool {
	ownWrites.Lock()
	defer ownWrites.Unlock()
	for _, w := range ownWrites.list {
		if w.path == p && time.Since(w.at) < ownFor && (w.by == "" || w.by == window) {
			return true
		}
	}
	return false
}

// watch has the window labelled label follow dirs, in place of what it
// followed before; send takes what changed.
func (t *treeWatches) watch(label string, dirs []string, seq *float64, send func(dirsChanged)) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.asked == nil {
		t.asked = map[string]float64{}
		t.byWindow = map[string]*windowWatch{}
	}
	if seq != nil && !newest(t.asked, label, *seq) {
		return true
	}
	w := t.byWindow[label]
	if len(dirs) == 0 {
		if w != nil {
			w.close()
			delete(t.byWindow, label)
		}
		return true
	}
	if w == nil {
		w = newWindowWatch(func(p string) bool { return isOwnWrite(p, label) }, send)
		t.byWindow[label] = w
	}
	return w.only(dirs, watchWhole)
}

// windowGone stops a closed window's watch.
func (t *treeWatches) windowGone(label string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if w := t.byWindow[label]; w != nil {
		w.close()
		delete(t.byWindow, label)
	}
	delete(t.asked, label)
}

func init() {
	// Watches dirs, the folders open in this window's tree, in place of what
	// it watched before; none stops. seq numbers the window's calls, which
	// can arrive out of order: an older one than the last is passed over.
	// False when one couldn't be watched, or the window is gone: the tree
	// then reads its folders on focus.
	command("tree_watch", func(c *Call, a struct {
		Dirs []string `json:"dirs"`
		Seq  *float64 `json:"seq"`
	}) (bool, error) {
		label := c.Label()
		// A window closed meanwhile would keep a watch nothing ends.
		if c.app.window(label) == nil {
			return false, nil
		}
		app := c.app
		return app.trees.watch(label, a.Dirs, a.Seq, func(changed dirsChanged) {
			_ = app.emitTo(label, eventTreeDirsChanged, changed)
		}), nil
	})
}
