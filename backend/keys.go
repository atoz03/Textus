package backend

import (
	"encoding/json/v2"
	"errors"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"sync"
)

// The keys Textus keeps for the services it calls (AI providers, image
// hosts): one JSON file in the config folder, provider id to key, which
// only its owner can read (provider_keys.json, as the Tauri builds kept
// it). No command hands a key back to a page.

const providerKeysFile = "provider_keys.json"

// keysMu makes each change one step: two saves at once would each drop
// the other's key.
var keysMu sync.Mutex

// readJSONStore reads a store that is changed and written back whole: the
// zero value when it isn't there, or doesn't parse (then moved aside for
// recovery by hand), but an error when it can't be read, so a save never
// writes over what it holds.
func readJSONStore[T any](path string) (T, error) {
	var v T
	raw, err := os.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return v, nil
	case err != nil:
		return v, err
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		log.Printf("textus: %s is not valid JSON (%v); moving it aside", path, err)
		moveAside(path)
		var zero T
		return zero, nil
	}
	return v, nil
}

// writeSecretJSON writes v to path whole, readable by its owner only.
func writeSecretJSON(path string, v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if err := writeFileAtomic(path, raw); err != nil {
		return err
	}
	return os.Chmod(path, 0o600)
}

func (a *App) keysPath() string { return filepath.Join(a.dirs.Config, providerKeysFile) }

// providerKey is the key kept for id, "" for none.
func (a *App) providerKey(id string) (string, error) {
	keys, err := readJSONStore[map[string]string](a.keysPath())
	return keys[id], err
}

// changeKeys reads the keys, changes them and writes them back, as one step.
func (a *App) changeKeys(change func(keys map[string]string) bool) error {
	keysMu.Lock()
	defer keysMu.Unlock()
	keys, err := readJSONStore[map[string]string](a.keysPath())
	if err != nil {
		return err
	}
	if keys == nil {
		keys = map[string]string{}
	}
	if !change(keys) {
		return nil
	}
	return writeSecretJSON(a.keysPath(), keys)
}

func init() {
	command("set_provider_api_key", func(c *Call, a struct {
		Provider string `json:"provider"`
		Key      string `json:"key"`
	}) (nothing, error) {
		return nil, c.app.changeKeys(func(keys map[string]string) bool {
			keys[a.Provider] = a.Key
			return true
		})
	})
	command("provider_api_key_status", func(c *Call, a struct {
		Provider string `json:"provider"`
	}) (bool, error) {
		keys, err := readJSONStore[map[string]string](c.app.keysPath())
		_, ok := keys[a.Provider]
		return ok, err
	})
	command("clear_provider_api_key", func(c *Call, a struct {
		Provider string `json:"provider"`
	}) (nothing, error) {
		return nil, c.app.changeKeys(func(keys map[string]string) bool {
			_, ok := keys[a.Provider]
			delete(keys, a.Provider)
			return ok
		})
	})
}
