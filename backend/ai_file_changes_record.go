package backend

import (
	"encoding/json/v2"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/egoist/mygo"
)

// The record of a conversation's changes to the folder's files: each
// change kept in the app's data (the file before and after), and the
// reply's changes undone and redone together from it.

var changeCount atomic.Uint32

func changeID() string {
	return fmt.Sprintf("%x-%x", time.Now().UnixMilli(), changeCount.Add(1)-1)
}

func writeChanged(file, text string) error {
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return err
	}
	return writeAtomic(file, []byte(text))
}

func saveChange(record string, c FileChange) error {
	raw, err := json.Marshal(c)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(record, 0o755); err != nil {
		return err
	}
	return writeAtomic(filepath.Join(record, c.ID+".json"), raw)
}

func loadChange(record, id string) (FileChange, error) {
	var c FileChange
	if !validScratchID(id) {
		return c, errors.New("Unknown change.")
	}
	raw, err := os.ReadFile(filepath.Join(record, id+".json"))
	if err != nil {
		return c, errors.New("That change is no longer kept.")
	}
	return c, json.Unmarshal(raw, &c)
}

// changeRecord is where conversation's changes are kept: the app's data,
// which the system doesn't clear as it does a temporary folder (they undo
// changes to the user's files, and hold a file taken away where there is
// no Trash). Those an older Textus kept in the conversation's scratch
// folder move there the first time.
func (a *App) changeRecord(conversation string) (string, error) {
	dir, err := conversationDir(conversation)
	if err != nil {
		return "", err
	}
	scratch := filepath.Join(dir, "changes")
	if a.dirs.Data == "" {
		return scratch, nil
	}
	record := filepath.Join(a.dirs.Data, "ai-changes", conversation)
	if info, err := os.Stat(scratch); err == nil && info.IsDir() && !exists(record) {
		_ = os.MkdirAll(filepath.Dir(record), 0o755)
		if os.Rename(scratch, record) != nil {
			return scratch, nil
		}
	}
	return record, nil
}

// forgetOldChangeRecords lets go of change records no conversation has
// touched for changesKeptFor: to the Trash where there is one (a record
// may hold a file taken away), and only records - never the user's own
// files.
func forgetOldChangeRecords(data string) {
	entries, err := os.ReadDir(filepath.Join(data, "ai-changes"))
	if err != nil {
		return
	}
	for _, e := range entries {
		info, err := e.Info()
		if err == nil && e.IsDir() && time.Since(info.ModTime()) > changesKeptFor {
			_ = moveToTrash(filepath.Join(data, "ai-changes", e.Name()))
		}
	}
}

// tryTrash moves path to the Trash; false where there is none. Tests
// replace it.
var tryTrash = func(path string) bool { return mygo.Shell.TrashItem(path) == nil }

// setChange puts c on disk (on) or takes it off: written, or the file back
// as it was - one the change made moved to the Trash, never deleted.
// Refused where the file no longer reads as the change left it.
func setChange(record string, c *FileChange, on bool) error {
	if c.Applied == on {
		return nil
	}
	var now *string
	if raw, err := os.ReadFile(c.File); err == nil && utf8.Valid(raw) {
		text := string(raw)
		now = &text
	}
	expected := &c.After
	if on {
		expected = c.Before
	}
	if (now == nil) != (expected == nil) || now != nil && *now != *expected {
		return fmt.Errorf("`%s` was changed since, so it is left as it is.", c.Path)
	}
	switch {
	case on:
		if err := writeChanged(c.File, c.After); err != nil {
			return err
		}
	case c.Before != nil:
		if err := writeChanged(c.File, *c.Before); err != nil {
			return err
		}
	// A file it made, discarded: to the Trash, where the user can get it
	// back; set aside in the record where there is none.
	case !tryTrash(c.File):
		if err := moveOrCopy(c.File, filepath.Join(record, c.ID+".removed")); err != nil {
			return err
		}
	}
	c.Applied = on
	return nil
}

// crossesDevices reports whether a rename failed for being between
// drives.
func crossesDevices(err error) bool {
	var errno syscall.Errno
	// ERROR_NOT_SAME_DEVICE on Windows.
	return errors.As(err, &errno) && (errno == syscall.EXDEV || runtime.GOOS == "windows" && errno == 17)
}

// moveOrCopy moves from to to, copying where they are on different
// drives.
func moveOrCopy(from, to string) error {
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		return err
	}
	err := os.Rename(from, to)
	if err == nil {
		return nil
	}
	if _, statErr := os.Lstat(to); statErr == nil || !crossesDevices(err) {
		return err
	}
	info, err := os.Lstat(from)
	if err != nil {
		return err
	}
	if info.IsDir() {
		if err := copyWhole(from, to); err != nil {
			return err
		}
		return os.RemoveAll(from)
	}
	if err := copyFile(from, to); err != nil {
		return err
	}
	return os.Remove(from)
}

func init() {
	// The changes ids of conversation, as kept.
	command("ai_file_changes", func(c *Call, a struct {
		Conversation string   `json:"conversation"`
		IDs          []string `json:"ids"`
	}) ([]FileChange, error) {
		record, err := c.app.changeRecord(a.Conversation)
		if err != nil {
			return nil, err
		}
		changes := []FileChange{}
		for _, id := range a.IDs {
			if change, err := loadChange(record, id); err == nil {
				changes = append(changes, change)
			}
		}
		return changes, nil
	})
	// The changes ids put on disk (on: apply, redo) or taken off (undo),
	// the later first when undoing. Each that can't be is left, and the
	// answer says why; the changes come back as they now are.
	command("ai_set_file_changes", func(c *Call, a struct {
		Conversation string   `json:"conversation"`
		IDs          []string `json:"ids"`
		On           bool     `json:"on"`
	}) ([2]any, error) {
		changes, problems, err := c.app.setFileChanges(a.Conversation, a.IDs, a.On)
		return [2]any{changes, problems}, err
	})
}

// setFileChanges is ai_set_file_changes.
func (a *App) setFileChanges(conversation string, ids []string, on bool) ([]FileChange, []string, error) {
	record, err := a.changeRecord(conversation)
	if err != nil {
		return nil, nil, err
	}
	changes := []FileChange{}
	for _, id := range ids {
		if change, err := loadChange(record, id); err == nil {
			changes = append(changes, change)
		}
	}
	problems := []string{}
	for k := range changes {
		i := k
		if !on {
			i = len(changes) - 1 - k
		}
		if err := setChange(record, &changes[i], on); err != nil {
			problems = append(problems, err.Error())
			continue
		}
		if err := saveChange(record, changes[i]); err != nil {
			return nil, nil, err
		}
	}
	return changes, problems, nil
}
