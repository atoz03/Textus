//go:build !linux && !windows && (!darwin || ios)

package backend

import "errors"

func cliCommandStatus() cliStatus { return cliMissing }

func quietCLIInstall() {}

func installCLICommand() error {
	return errors.New("The 'textus' command isn't available here.")
}
