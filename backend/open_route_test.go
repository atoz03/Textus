package backend

import (
	"path/filepath"
	"testing"
)

func routeWindow(folder string, paths ...string) WindowSession {
	w := WindowSession{}
	for _, p := range paths {
		w.Paths = append(w.Paths, filepath.FromSlash(p))
	}
	if folder != "" {
		f := filepath.FromSlash(folder)
		w.Folder = &f
	}
	return w
}

func routePaths(paths ...string) []string {
	out := make([]string, len(paths))
	for i, p := range paths {
		out[i] = filepath.FromSlash(p)
	}
	return out
}

func TestADocumentGoesToTheWindowWhoseFolderHoldsIt(t *testing.T) {
	windows := map[string]WindowSession{
		"a": routeWindow("/work"),
		"b": routeWindow("/notes"),
	}
	if to := windowFor(windows, routePaths("/notes/x.md"), "a"); to != "b" {
		t.Errorf("went to %q", to)
	}
}

func TestOutsideEveryFolderNoneAndTheFocusedWindowTakesIt(t *testing.T) {
	windows := map[string]WindowSession{"a": routeWindow("/work")}
	if to := windowFor(windows, routePaths("/elsewhere/x.md"), "a"); to != "" {
		t.Errorf("went to %q", to)
	}
	// A window without a folder holds nothing.
	windows = map[string]WindowSession{"a": routeWindow("")}
	if to := windowFor(windows, routePaths("/x.md"), ""); to != "" {
		t.Errorf("went to %q", to)
	}
}

func TestTheWindowThatHasItOpenComesFirst(t *testing.T) {
	windows := map[string]WindowSession{
		"a": routeWindow("/notes"),
		"b": routeWindow("", "/notes/x.md"),
	}
	if to := windowFor(windows, routePaths("/notes/x.md"), "a"); to != "b" {
		t.Errorf("went to %q", to)
	}
}

func TestTheInnermostFolderWinsThenTheFocusedWindow(t *testing.T) {
	windows := map[string]WindowSession{
		"a": routeWindow("/notes"),
		"b": routeWindow("/notes/daily"),
		"c": routeWindow("/notes/daily"),
	}
	paths := routePaths("/notes/daily/x.md")
	if to := windowFor(windows, paths, "c"); to != "c" {
		t.Errorf("focused c: went to %q", to)
	}
	if to := windowFor(windows, paths, "a"); to != "b" {
		t.Errorf("focused a: went to %q", to)
	}
}

func TestABatchGoesWhereAllOfItBelongs(t *testing.T) {
	windows := map[string]WindowSession{"a": routeWindow("/notes")}
	if to := windowFor(windows, routePaths("/notes/x.md", "/work/y.md"), ""); to != "" {
		t.Errorf("a split batch went to %q", to)
	}
	// A folder name that only starts the same is not the folder.
	if to := windowFor(windows, routePaths("/notes-old/x.md"), ""); to != "" {
		t.Errorf("went to %q", to)
	}
}
