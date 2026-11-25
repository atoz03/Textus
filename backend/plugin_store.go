package backend

import (
	"context"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"textus/backend/plugins"
)

// A plugin's own data, in a global place and a project one (with
// project.data; with no folder open, a hidden one of the app's), each with
// keys and files held to a quota; and its requests to the hosts it was
// granted.

// pluginHTTPTimeout bounds one request of a plugin's.
const pluginHTTPTimeout = 60 * time.Second

// storeArgs name one of a plugin's places: Root is the folder the window
// has open, for the project's.
type storeArgs struct {
	ID    string             `json:"id"`
	Scope plugins.StoreScope `json:"scope"`
	Root  *string            `json:"root"`
}

// looseRoot is root when the window has a folder open.
func looseRoot(root *string) (string, bool) {
	if root == nil || *root == "" {
		return "", false
	}
	return *root, true
}

// storePlace is the folder of a plugin's scope. With no folder open the
// project's is a hidden folder of the app's, so a plugin works without
// asking the user to open one.
func (a *App) storePlace(s storeArgs) (string, error) {
	state := a.pluginState()
	if err := state.Running(s.ID); err != nil {
		return "", err
	}
	global := a.globalAgentPath()
	switch s.Scope {
	case plugins.ScopeGlobal:
		return plugins.GlobalPlace(global, s.ID), nil
	case plugins.ScopeProject:
		if !state.Allows(s.ID, "project.data") {
			return "", plugins.Refused(s.ID, "project.data")
		}
		root, ok := looseRoot(s.Root)
		if !ok {
			return plugins.LooseDir(global, s.ID), nil
		}
		place := plugins.ProjectPlace(root, s.ID)
		return place, plugins.Unlinked(root, place)
	}
	return "", fmt.Errorf("unknown variant `%s`, expected `global` or `project`", s.Scope)
}

// storeFile is a file of the plugin's in its place.
func (a *App) storeFile(s storeArgs, path string) (place, file string, err error) {
	if place, err = a.storePlace(s); err != nil {
		return "", "", err
	}
	file, err = plugins.FileIn(place, path)
	return place, file, err
}

func init() {
	// What the plugin kept under key, or null.
	command("plugin_store_get", func(c *Call, a struct {
		storeArgs
		Key string `json:"key"`
	}) (jsontext.Value, error) {
		place, err := c.app.storePlace(a.storeArgs)
		if err != nil {
			return nil, err
		}
		v, err := plugins.StoreGet(place, a.Key)
		if v == nil && err == nil {
			v = jsontext.Value("null")
		}
		return v, err
	})
	// Keeps value under key; null takes the key away.
	command("plugin_store_set", func(c *Call, a struct {
		storeArgs
		Key   string         `json:"key"`
		Value jsontext.Value `json:"value"`
	}) (nothing, error) {
		place, err := c.app.storePlace(a.storeArgs)
		if err != nil {
			return nil, err
		}
		return nil, plugins.StoreSet(place, a.Key, a.Value, writeAtomic)
	})
	// Every key the plugin keeps there, in order.
	command("plugin_store_keys", func(c *Call, a storeArgs) ([]string, error) {
		place, err := c.app.storePlace(a)
		if err != nil {
			return nil, err
		}
		return plugins.StoreKeys(place), nil
	})
	// One of the plugin's own files there, as text.
	command("plugin_store_read", func(c *Call, a struct {
		storeArgs
		Path string `json:"path"`
	}) (string, error) {
		_, file, err := c.app.storeFile(a.storeArgs, a.Path)
		if err != nil {
			return "", err
		}
		return readUTF8(file)
	})
	// Writes one of the plugin's own files there.
	command("plugin_store_write", func(c *Call, a struct {
		storeArgs
		Path string `json:"path"`
		Text string `json:"text"`
	}) (nothing, error) {
		place, file, err := c.app.storeFile(a.storeArgs, a.Path)
		if err != nil {
			return nil, err
		}
		return nil, plugins.WriteWithin(place, file, []byte(a.Text), writeAtomic)
	})
	// The plugin's own files there, as paths under its files/.
	command("plugin_store_files", func(c *Call, a storeArgs) ([]string, error) {
		place, err := c.app.storePlace(a)
		if err != nil {
			return nil, err
		}
		return plugins.StoreFiles(filepath.Join(place, "files")), nil
	})
	// Deletes one of the plugin's own files there.
	command("plugin_store_delete", func(c *Call, a struct {
		storeArgs
		Path string `json:"path"`
	}) (nothing, error) {
		_, file, err := c.app.storeFile(a.storeArgs, a.Path)
		if err != nil {
			return nil, err
		}
		if err := os.Remove(file); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
		return nil, nil
	})
	// How much a plugin keeps in each place, for Settings. With no folder
	// open, what it kept without one counts as its data for every
	// document: it isn't any project's.
	command("plugin_store_usage", func(c *Call, a struct {
		ID   string  `json:"id"`
		Root *string `json:"root"`
	}) ([2]int64, error) {
		if err := plugins.RequireID(a.ID); err != nil {
			return [2]int64{}, err
		}
		global := c.app.globalAgentPath()
		used := plugins.SizeOf(plugins.GlobalPlace(global, a.ID))
		if root, ok := looseRoot(a.Root); ok {
			return [2]int64{used, plugins.SizeOf(plugins.ProjectPlace(root, a.ID))}, nil
		}
		return [2]int64{used + plugins.SizeOf(plugins.LooseDir(global, a.ID)), 0}, nil
	})
	// Empties one of a plugin's places (Clear data), to the Trash: it may
	// hold the user's notes. Asked by the user, not the plugin, which may
	// be off.
	command("plugin_store_clear", func(c *Call, a storeArgs) (nothing, error) {
		if err := plugins.RequireID(a.ID); err != nil {
			return nil, err
		}
		global := c.app.globalAgentPath()
		var dir string
		switch root, ok := looseRoot(a.Root); {
		case a.Scope == plugins.ScopeGlobal:
			dir = plugins.GlobalPlace(global, a.ID)
		case a.Scope != plugins.ScopeProject:
			return nil, fmt.Errorf("unknown variant `%s`, expected `global` or `project`", a.Scope)
		case ok:
			dir = plugins.ProjectPlace(root, a.ID)
		default:
			dir = plugins.LooseDir(global, a.ID)
		}
		return nil, moveToTrash(dir)
	})

	// A request to a host the plugin was granted (network:<host>); one
	// given a cancelId stops when the plugin cancels it.
	command("plugin_http", func(c *Call, a struct {
		ID      string              `json:"id"`
		Request plugins.HTTPRequest `json:"request"`
	}) (plugins.HTTPResponse, error) {
		state := c.app.pluginState()
		if err := state.Running(a.ID); err != nil {
			return plugins.HTTPResponse{}, err
		}
		granted := state.Granted(a.ID)
		return plugins.Cancellable(c.ctx, a.ID, a.Request.CancelID, func(ctx context.Context) (plugins.HTTPResponse, error) {
			return plugins.Send(ctx, netClient(pluginHTTPTimeout), a.ID, granted, a.Request)
		})
	})
	command("plugin_http_cancel", func(c *Call, a struct {
		ID       string `json:"id"`
		CancelID string `json:"cancelId"`
	}) (nothing, error) {
		plugins.Cancel(a.ID, a.CancelID)
		return nil, nil
	})
}
