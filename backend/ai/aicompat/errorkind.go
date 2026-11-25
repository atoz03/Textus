package aicompat

import (
	"encoding/json/v2"
	"fmt"
	"regexp"
	"sync"
)

// What kind of failure a provider's error is, from the list the frontend
// reads too (src/ai/ai-error-kinds.json, which the main package embeds and
// the backend hands to LoadErrorKinds): first match wins.

type errorKind struct {
	kind    string
	pattern *regexp.Regexp
}

var errorKinds struct {
	sync.RWMutex
	list []errorKind
}

// LoadErrorKinds reads the list, replacing any read before.
func LoadErrorKinds(raw []byte) error {
	var entries []struct {
		Kind    string `json:"kind"`
		Pattern string `json:"pattern"`
	}
	if err := json.Unmarshal(raw, &entries); err != nil {
		return err
	}
	list := make([]errorKind, 0, len(entries))
	for _, e := range entries {
		pattern, err := regexp.Compile("(?i)" + e.Pattern)
		if err != nil {
			return fmt.Errorf("%s: %w", e.Kind, err)
		}
		list = append(list, errorKind{e.Kind, pattern})
	}
	errorKinds.Lock()
	errorKinds.list = list
	errorKinds.Unlock()
	return nil
}

// ErrorKind is the kind text reads as ("rateLimit", "auth", ...), "" for
// none.
func ErrorKind(text string) string {
	errorKinds.RLock()
	defer errorKinds.RUnlock()
	for _, k := range errorKinds.list {
		if k.pattern.MatchString(text) {
			return k.kind
		}
	}
	return ""
}
