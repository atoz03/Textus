package backend

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"testing"
)

// Every command the frontend's bindings call has a handler here.
func TestEveryBoundCommandIsHandled(t *testing.T) {
	files, err := filepath.Glob("../src/platform/bindings/*.ts")
	if err != nil || len(files) == 0 {
		t.Fatalf("no bindings: %v", err)
	}
	call := regexp.MustCompile(`INVOKE(?:<.*?>)?\("([a-z0-9_]+)"`)
	var missing []string
	seen := 0
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range call.FindAllStringSubmatch(string(raw), -1) {
			seen++
			if _, ok := commands[m[1]]; !ok {
				missing = append(missing, m[1])
			}
		}
	}
	sort.Strings(missing)
	if seen < 300 || len(missing) > 0 {
		t.Errorf("%d calls; no handler for %v", seen, missing)
	}
}
