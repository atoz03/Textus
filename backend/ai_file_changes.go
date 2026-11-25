package backend

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"textus/backend/ai"
)

// General mode's changes to the folder's other files: write_file and
// edit_file write straight to disk, each change kept (the file before and
// after) in the app's data, so a reply's changes undo and redo together.
// With changes as revisions (askFirst), a change to a file is only kept -
// the editor shows it as revisions in that file - and a file made is made,
// marked as the AI's until the user keeps it.

const (
	writeFileToolName = "write_file"
	editFileToolName  = "edit_file"
	// maxWrittenBytes is the most a written file may hold.
	maxWrittenBytes = 2 << 20
	// changesKeptFor is how long a conversation's change records are kept
	// after its last change.
	changesKeptFor = 90 * 24 * time.Hour
)

// FileChange is one change to one file (FileChange in
// src/platform/bindings/ai-project.ts).
type FileChange struct {
	ID string `json:"id"`
	// Path is the file, from the folder.
	Path string `json:"path"`
	// File is the file's whole path.
	File string `json:"file"`
	// Before is the file as it was; nil where the change made it.
	Before *string `json:"before"`
	After  string  `json:"after"`
	// Applied: on disk now; false while waiting to be applied, or once
	// undone.
	Applied bool `json:"applied"`
	// Revision: made while changes land as revisions - kept for review, a
	// file made waiting to be kept.
	Revision bool `json:"revision"`
}

// fileEdits is where one request's file changes go.
type fileEdits struct {
	// root is the folder, canonical: what "inside it" is checked against.
	root string
	// named is the folder as the editor names it: the files' paths are
	// written from it, so the tree, its tabs and the stores find them.
	named  string
	record string
	data   string
	// openDocument is the document open in the editor: its changes are
	// proposals.
	openDocument string
	askFirst     bool
	// made are this request's changes, in order: a file changed twice
	// reads its own earlier change.
	mu   sync.Mutex
	made []FileChange
}

// newFileEdits is the changes for a request in root, kept for
// conversation; nil without either.
func (a *App) newFileEdits(root, conversation, openDocument string, askFirst bool) *fileEdits {
	if root == "" || conversation == "" {
		return nil
	}
	real, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil
	}
	real, _ = filepath.Abs(real)
	record, err := a.changeRecord(conversation)
	if err != nil {
		return nil
	}
	open := ""
	if openDocument != "" {
		if p, err := filepath.EvalSymlinks(openDocument); err == nil {
			open, _ = filepath.Abs(p)
		}
	}
	return &fileEdits{root: real, named: root, record: record, data: a.dirs.Data, openDocument: open, askFirst: askFirst}
}

// fileChangeFunc is a change's new text, given its arguments and the
// file's text now (nil for a file not there yet).
type fileChangeFunc func(args *ai.Args, now *string) (string, error)

// run makes one change and answers what the model is told and, when it
// was made, the call's arguments with the change's id as `change`.
func (e *fileEdits) run(arguments string, change fileChangeFunc) (string, *string) {
	args, ok := ai.ParseArgs(arguments)
	if !ok {
		args = ai.NewArgs()
	}
	rel := args.StrOr("path")
	made, err := e.make(args, rel, change)
	if err != nil {
		return err.Error(), nil
	}
	args.Set("change", made.ID)
	recorded := args.String()
	var text string
	switch {
	case !made.Applied:
		text = fmt.Sprintf("Kept `%s` as a revision in that file for the user to accept or reject: it isn't on disk until they accept it. Later changes to it in this reply build on this one; from your next reply on, read_file shows it with your revision.", made.Path)
	case made.Before == nil && e.askFirst:
		text = fmt.Sprintf("Created `%s` - marked as yours until the user keeps or discards it.", made.Path)
	case made.Before == nil:
		text = fmt.Sprintf("Created `%s` - the user can undo it.", made.Path)
	default:
		text = fmt.Sprintf("Changed `%s` - the user can undo it.", made.Path)
	}
	return text, &recorded
}

func (e *fileEdits) make(args *ai.Args, rel string, change fileChangeFunc) (FileChange, error) {
	file, err := e.target(rel)
	if err != nil {
		return FileChange{}, err
	}
	now, err := e.current(file)
	if err != nil {
		return FileChange{}, err
	}
	// Made in this reply: it waits as a whole to be kept, its later changes
	// written in with it.
	e.mu.Lock()
	madeNow := false
	for _, c := range e.made {
		madeNow = madeNow || c.File == file && c.Before == nil
	}
	e.mu.Unlock()
	after, err := change(args, now)
	if err != nil {
		return FileChange{}, err
	}
	if len(after) > maxWrittenBytes {
		return FileChange{}, errors.New("That is too long for one file (2 MB at most).")
	}
	if now != nil && *now == after {
		return FileChange{}, fmt.Errorf("`%s` already reads that way: nothing changed.", rel)
	}
	c := FileChange{
		ID:   changeID(),
		Path: strings.TrimLeft(strings.TrimSpace(rel), "@/"),
		File: file,
		// A file it makes is made even as a revision: it waits, marked as
		// the AI's, for the user to keep or discard it.
		Applied:  !e.askFirst || now == nil || madeNow,
		Before:   now,
		After:    after,
		Revision: e.askFirst,
	}
	if c.Applied {
		if err := writeChanged(file, c.After); err != nil {
			return FileChange{}, err
		}
	}
	// Unrecorded, it couldn't be undone: put the file back.
	if err := saveChange(e.record, c); err != nil {
		if c.Applied {
			if c.Before != nil {
				_ = writeChanged(file, *c.Before)
			} else {
				_ = os.Remove(file)
			}
		}
		return FileChange{}, err
	}
	e.mu.Lock()
	e.made = append(e.made, c)
	e.mu.Unlock()
	return c, nil
}

const hiddenRefused = "Files and folders whose names start with \".\" (.git, .textus) aren't changed."

// target is the file rel names: inside the folder, not hidden, a text
// file, and not the open document.
func (e *fileEdits) target(rel string) (string, error) {
	rel = strings.TrimLeft(strings.TrimSpace(rel), "@/")
	if rel == "" {
		return "", errors.New("`path` is required: the file, from the folder.")
	}
	var inside []string
	for _, part := range strings.Split(filepath.ToSlash(rel), "/") {
		switch {
		case part == "" || part == ".":
		case part == "..":
			return "", errors.New("Give the path from the folder, without `..`.")
		case strings.HasPrefix(part, "."):
			return "", errors.New(hiddenRefused)
		default:
			inside = append(inside, part)
		}
	}
	if filepath.IsAbs(rel) || filepath.VolumeName(rel) != "" {
		return "", errors.New("Give the path from the folder, without `..`.")
	}
	if fileKindOf(rel) != kindText {
		return "", fmt.Errorf("`%s` isn't a text file; only text files (Markdown, plain text and the like) are written.", rel)
	}
	file := filepath.Join(append([]string{e.named}, inside...)...)
	// A link inside the folder may point out of it.
	existing := file
	for !exists(existing) && existing != e.named && filepath.Dir(existing) != existing {
		existing = filepath.Dir(existing)
	}
	real, err := filepath.EvalSymlinks(existing)
	if err != nil {
		return "", err
	}
	real, _ = filepath.Abs(real)
	parts, ok := pathUnder(real, e.root)
	if !ok {
		return "", errors.New("That path leads outside the folder - only files inside it are changed.")
	}
	// A link inside the folder may also lead into a hidden one.
	for _, part := range parts {
		if strings.HasPrefix(part, ".") {
			return "", errors.New(hiddenRefused)
		}
	}
	realFile := file
	if p, err := filepath.EvalSymlinks(file); err == nil {
		realFile, _ = filepath.Abs(p)
	}
	if e.openDocument != "" && realFile == e.openDocument {
		return "", errors.New("That is the open document: change it with propose_edit.")
	}
	return file, nil
}

// current is the file's text as this request left it: its last kept
// change, else a revision kept for it earlier, else the disk; nil for a
// file not there yet.
func (e *fileEdits) current(file string) (*string, error) {
	e.mu.Lock()
	for i := len(e.made) - 1; i >= 0; i-- {
		if e.made[i].File == file {
			after := e.made[i].After
			e.mu.Unlock()
			return &after, nil
		}
	}
	e.mu.Unlock()
	// A change kept as a revision before, not yet placed: this builds on
	// it. Written straight in, a revision left waiting is the user's to
	// decide, never written along with this.
	if waiting, ok := pendingText(e.data, file); ok {
		if e.askFirst {
			return &waiting, nil
		}
		return nil, fmt.Errorf("A revision of yours to `%s` still waits for the user to accept or reject it; tell them what you would change instead of changing it now.", file)
	}
	// Its revisions placed in the editor and still undecided: a change now
	// would land under them.
	if hasUndecided(e.data, file) {
		return nil, errors.New("The user is still deciding on your revisions in that file; tell them what else you would change instead of changing it now.")
	}
	info, err := os.Stat(file)
	if err != nil {
		return nil, nil
	}
	if info.IsDir() {
		return nil, errors.New("That is a folder, not a file.")
	}
	raw, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	if len(raw) > maxWrittenBytes {
		return nil, errors.New("That file is too large to change (2 MB at most).")
	}
	if !utf8.Valid(raw) {
		return nil, errors.New("That file isn't UTF-8 text, so it isn't changed.")
	}
	text := string(raw)
	return &text, nil
}

// writeFileChange is write_file's change: the whole new text.
func writeFileChange(args *ai.Args, now *string) (string, error) {
	content, ok := args.Str("content")
	if !ok {
		return "", errors.New("`content` is required: the file's whole new text.")
	}
	return inEndingsOf(now, strings.ReplaceAll(content, "\r\n", "\n")), nil
}

// inEndingsOf is text (`\n` lines) with the line endings now has: a
// Windows file read to the model as `\n` keeps its `\r\n` when written
// back.
func inEndingsOf(now *string, text string) string {
	if now != nil && strings.Contains(*now, "\r\n") {
		return strings.ReplaceAll(text, "\n", "\r\n")
	}
	return text
}

// editFileChange is edit_file's change: `old`, there once, becomes `new`.
func editFileChange(args *ai.Args, now *string) (string, error) {
	if now == nil {
		return "", errors.New("That file doesn't exist yet: create it with write_file.")
	}
	old := args.StrOr("old")
	replacement, ok := args.Str("new")
	if !ok {
		return "", errors.New("`new` is required: what the text becomes (\"\" to delete it).")
	}
	if old == "" {
		return "", errors.New("`old` is required: the text to replace, verbatim.")
	}
	// Matched as the model read it, with `\n` lines; written back with the
	// file's own.
	lf := func(text string) string { return strings.ReplaceAll(text, "\r\n", "\n") }
	text, old, replacement := lf(*now), lf(old), lf(replacement)
	switch n := strings.Count(text, old); n {
	case 1:
		return inEndingsOf(now, strings.Replace(text, old, replacement, 1)), nil
	case 0:
		return "", errors.New("`old` isn't in the file as written - read_file it and copy the text exactly.")
	default:
		return "", fmt.Errorf("`old` appears %d times; include more of the text around it so it is unique.", n)
	}
}
