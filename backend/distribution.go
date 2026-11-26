package backend

import (
	"fmt"
	"slices"
)

// distribution is the channel this build is installed through
// (src/platform/ipc-app.ts Distribution), which decides who installs its
// updates (src/platform/distribution.ts): a "direct" build installs its
// own; "homebrew" and "aur" leave them to the package manager and only
// report them. A release build sets it at link time, as the Tauri builds'
// TEXTUS_DISTRIBUTION did at compile time:
//
//	MYGO_LDFLAGS="-X textus/backend.distribution=aur" npx mygo build
var distribution = "direct"

// checkDistribution refuses a value the page doesn't know, such as a typo
// (aur spelled arch), which would ship a package that installs its own
// updates over the package manager's files; and "portable", whose data
// folder beside the app the Go backend doesn't keep yet.
func checkDistribution(d string) error {
	switch {
	case d == "portable":
		return fmt.Errorf("textus/backend.distribution is portable, which the Go backend doesn't build yet: its settings and data would not stay beside the app")
	case !slices.Contains([]string{"direct", "homebrew", "aur"}, d):
		return fmt.Errorf("textus/backend.distribution is %q: link with direct, homebrew or aur", d)
	}
	return nil
}

func init() {
	// A build linked with a wrong value fails as it starts, as the Tauri
	// builds failed to compile.
	if err := checkDistribution(distribution); err != nil {
		panic(err)
	}
	command("app_distribution", func(c *Call, _ none) (string, error) { return distribution, nil })
}
