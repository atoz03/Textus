package backend

import (
	"encoding/json/v2"
	"errors"
	"fmt"
	"log"
	"strings"
)

// errorTable holds the English text of each error code the frontend
// explains (src/i18n/errors.json). The frontend reads the code from the
// "[code]" at the end of the message.
var errorTable map[string]struct {
	En string `json:"en"`
}

func loadErrorTable(raw []byte) {
	if err := json.Unmarshal(raw, &errorTable); err != nil {
		log.Printf("textus: errors.json: %v", err)
	}
}

// codeError is the error the frontend knows by its code: "<English> [code]".
func codeError(code string) error {
	if e, ok := errorTable[code]; ok {
		return fmt.Errorf("%s [%s]", e.En, code)
	}
	return errors.New(code)
}

// isCode reports whether err is the error of code.
func isCode(err error, code string) bool {
	return err != nil && strings.HasSuffix(err.Error(), "["+code+"]")
}

// errUnsupported is what a command of a subsystem this build does not have
// answers: the frontend shows it as the command failing.
func errUnsupported(command string) error {
	return fmt.Errorf("%s is not available in this build of Textus [unsupported]", command)
}

// errorf is a plain error, shown to the user as it is.
func errorf(format string, args ...any) error { return fmt.Errorf(format, args...) }
