package backend

import (
	"encoding/json/v2"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
)

const draftsRestoring = "restoring"

// draftStore keeps the unsaved text of each tab, to offer it back after a
// crash.
type draftStore struct {
	dir      string
	claimed  atomic.Bool
	mu       sync.Mutex
	pageTabs string
}

func loadDrafts(dirs Dirs) *draftStore {
	return &draftStore{dir: filepath.Join(dirs.Data, dirName("drafts"))}
}

func (d *draftStore) flush() {}

// DraftSnapshot is one tab's unsaved text.
type DraftSnapshot struct {
	TabID   string  `json:"tabId"`
	Path    *string `json:"path"`
	Content string  `json:"content"`
	// DiskMtime is the file's time when its edits began: a restored draft
	// whose file has changed since asks before it is saved over it.
	DiskMtime *float64 `json:"diskMtime"`
}

func validComponent(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-') {
			return false
		}
	}
	return true
}

func hasJSON(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".json") {
			return true
		}
	}
	return false
}

// draftsWaiting says whether drafts are left from the last session, for
// the main window to restore.
func (a *App) draftsWaiting() bool {
	d := a.drafts
	if d == nil || d.claimed.Load() {
		return false
	}
	return hasJSON(filepath.Join(d.dir, draftsRestoring)) || hasJSON(d.dir)
}

// removeJSONFiles deletes the .json files of dir, then dir itself if that
// leaves it empty: never a recursive delete.
func removeJSONFiles(dir string) error {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.Type().IsRegular() && strings.HasSuffix(e.Name(), ".json") {
			if err := os.Remove(filepath.Join(dir, e.Name())); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return err
			}
		}
	}
	_ = os.Remove(dir)
	return nil
}

func init() {
	command("save_draft_snapshot", func(c *Call, a struct {
		TabID     string   `json:"tabId"`
		Path      *string  `json:"path"`
		Content   string   `json:"content"`
		DiskMtime *float64 `json:"diskMtime"`
	}) (nothing, error) {
		if !validComponent(a.TabID) {
			return nil, errors.New("invalid tab id")
		}
		raw, err := json.Marshal(DraftSnapshot{a.TabID, a.Path, a.Content, a.DiskMtime})
		if err != nil {
			return nil, err
		}
		return nil, writeFileAtomic(filepath.Join(c.app.drafts.dir, a.TabID+".json"), raw)
	})
	command("take_draft_snapshots", func(c *Call, _ none) ([]DraftSnapshot, error) {
		d := c.app.drafts
		snapshots := []DraftSnapshot{}
		if c.Label() != "main" || d.claimed.Swap(true) {
			return snapshots, nil
		}
		restoring := filepath.Join(d.dir, draftsRestoring)
		for _, from := range []struct {
			dir   string
			aside bool
		}{{restoring, false}, {d.dir, true}} {
			entries, err := os.ReadDir(from.dir)
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			if err != nil {
				return nil, err
			}
			for _, e := range entries {
				if !strings.HasSuffix(e.Name(), ".json") || e.IsDir() {
					continue
				}
				path := filepath.Join(from.dir, e.Name())
				var snap DraftSnapshot
				raw, err := os.ReadFile(path)
				if err == nil && json.Unmarshal(raw, &snap) == nil {
					snapshots = append(snapshots, snap)
					if from.aside {
						if err := os.MkdirAll(restoring, 0o755); err != nil {
							return nil, err
						}
						_ = os.Rename(path, filepath.Join(restoring, e.Name()))
					}
					continue
				}
				// Cut off by a crash, or another version's: kept apart.
				unreadable := filepath.Join(d.dir, "unreadable")
				if err := os.MkdirAll(unreadable, 0o755); err != nil {
					return nil, err
				}
				_ = os.Rename(path, filepath.Join(unreadable, e.Name()))
			}
		}
		return snapshots, nil
	})
	command("finish_draft_restore", func(c *Call, _ none) (nothing, error) {
		return nil, removeJSONFiles(filepath.Join(c.app.drafts.dir, draftsRestoring))
	})
	command("clear_draft_snapshot", func(c *Call, a struct {
		TabID string `json:"tabId"`
	}) (nothing, error) {
		if !validComponent(a.TabID) {
			return nil, errors.New("invalid tab id")
		}
		err := os.Remove(filepath.Join(c.app.drafts.dir, a.TabID+".json"))
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
		return nil, nil
	})
	command("clear_all_drafts", func(c *Call, _ none) (nothing, error) {
		d := c.app.drafts.dir
		for _, dir := range []string{filepath.Join(d, draftsRestoring), filepath.Join(d, "unreadable"), d} {
			if err := removeJSONFiles(dir); err != nil {
				return nil, fmt.Errorf("cannot clear the drafts: %w", err)
			}
		}
		return nil, nil
	})
	command("keep_page_tabs", func(c *Call, a struct {
		Text string `json:"text"`
	}) (nothing, error) {
		c.app.drafts.mu.Lock()
		c.app.drafts.pageTabs = a.Text
		c.app.drafts.mu.Unlock()
		return nil, nil
	})
	command("kept_page_tabs", func(c *Call, _ none) (string, error) {
		c.app.drafts.mu.Lock()
		defer c.app.drafts.mu.Unlock()
		return c.app.drafts.pageTabs, nil
	})
}
