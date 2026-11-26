package backend

import (
	"maps"
	"slices"
)

// Which open window documents handed over from outside (a file manager, a
// second launch, Open Recent) go to, as VS Code picks one: the window that
// has them open already, else the one whose folder holds them all.

// windowFor is the window for paths among windows (label → its session),
// or "" when no window has them or holds them: then the focused one takes
// them. focused wins a tie, so a second window on the same folder keeps
// them; past that, the first label in order.
func windowFor(windows map[string]WindowSession, paths []string, focused string) string {
	hasOpen := func(w WindowSession) (int, bool) {
		for _, p := range paths {
			if !slices.Contains(w.Paths, p) {
				return 0, false
			}
		}
		return 0, true
	}
	// The innermost folder holding them all: its depth.
	holds := func(w WindowSession) (int, bool) {
		if w.Folder == nil {
			return 0, false
		}
		for _, p := range paths {
			if _, under := pathUnder(p, *w.Folder); !under {
				return 0, false
			}
		}
		return len(splitPath(*w.Folder)), true
	}
	pick := func(rank func(WindowSession) (int, bool)) string {
		best, bestRank, bestFocused := "", 0, false
		for _, label := range slices.Sorted(maps.Keys(windows)) {
			r, ok := rank(windows[label])
			if !ok {
				continue
			}
			mine := label == focused
			if best == "" || r > bestRank || (r == bestRank && mine && !bestFocused) {
				best, bestRank, bestFocused = label, r, mine
			}
		}
		return best
	}
	if to := pick(hasOpen); to != "" {
		return to
	}
	return pick(holds)
}

// liveEditorSessions is the sessions of the editor windows open now, by
// label.
func (a *App) liveEditorSessions() map[string]WindowSession {
	a.mu.Lock()
	defer a.mu.Unlock()
	live := map[string]WindowSession{}
	for label, s := range a.sessions {
		if isEditorWindow(label) && a.windows[label] != nil {
			live[label] = s
		}
	}
	return live
}
