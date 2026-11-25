package rules

import (
	"os"
	"path/filepath"
	"testing"
)

// TestSweepRealProse prints what the engine finds in the app's own prose,
// the help guides in ten languages: a false-positive check to run by hand
// after changing a rule (TEXTUS_SWEEP=1 go test -run Sweep -v
// ./backend/ai/rules; TEXTUS_SWEEP=strict for the P2 rules too).
func TestSweepRealProse(t *testing.T) {
	mode := os.Getenv("TEXTUS_SWEEP")
	if mode == "" {
		t.Skip("a manual false-positive sweep: set TEXTUS_SWEEP")
	}
	files, err := filepath.Glob("../../../src/help/*.md")
	if err != nil || len(files) == 0 {
		t.Fatal("no help guides", err)
	}
	level := LevelStandard
	if mode == "strict" {
		level = LevelStrict
	}
	for _, f := range files {
		doc, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, x := range CheckDocument(string(doc), defaultConv(), level, nil).Findings {
			t.Logf("%s L%d [%s] %q -> %v | %s", filepath.Base(f), *x.Line, x.Rule, x.Original, deref(x.Suggestion), x.Message)
		}
	}
}
