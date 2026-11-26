package backend

import (
	"os"
	"strings"
	"testing"
)

func TestDistributionTakesTheChannelsThePageKnows(t *testing.T) {
	for _, d := range []string{"direct", "homebrew", "aur"} {
		if err := checkDistribution(d); err != nil {
			t.Errorf("%s: %v", d, err)
		}
	}
	for _, d := range []string{"", "arch", "Direct", "portable"} {
		if checkDistribution(d) == nil {
			t.Errorf("%q passed", d)
		}
	}
	if distribution != "direct" {
		t.Errorf("an unlinked build is %q, not direct", distribution)
	}
	// The page's type names the same channels.
	ts, err := os.ReadFile("../src/platform/ipc-app.ts")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(ts), `export type Distribution = "direct" | "homebrew" | "aur" | "portable";`) {
		t.Error("src/platform/ipc-app.ts Distribution changed: change checkDistribution with it")
	}
}
