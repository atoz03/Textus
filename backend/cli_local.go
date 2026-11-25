package backend

import (
	"encoding/json/v2"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

// The `textus` commands that need no running app: they read and write files
// with the same walk, search and extraction Textus AI's project tools use.

// cliSearchTime is longer than the agent's: a person at a terminal waits
// for a big folder.
const cliSearchTime = 30 * time.Second

// runHere runs one of the commands that need no app.
func runHere(name string, ctx cliCtx, args []string, o *cliIO) (int, error) {
	switch name {
	case "list":
		return cliList(ctx, args, o)
	case "read":
		return cliRead(ctx, args, o)
	case "search":
		return cliSearch(ctx, args, o)
	case "new":
		return cliNew(ctx, args, o)
	case "move":
		return cliMove(ctx, args, o)
	}
	return 0, fmt.Errorf("'textus %s' has nothing to run.", name)
}

func cliFolder(ctx cliCtx, args []string) (string, error) {
	path := ctx.cwd
	if len(args) > 0 {
		path = resolveAgainst(ctx.cwd, args[0])
	}
	if info, err := os.Stat(path); err != nil || !info.IsDir() {
		return "", fmt.Errorf("%s isn't a folder.", path)
	}
	return path, nil
}

func atMost(args []string, n int, usage string) ([]string, error) {
	if len(args) > n {
		return nil, fmt.Errorf("Too many arguments. Usage: %s", usage)
	}
	return args, nil
}

// printJSON prints v as one line of JSON.
func printJSON(o *cliIO, v any) {
	out, err := json.Marshal(v, json.Deterministic(true))
	if err != nil {
		o.warn(err.Error())
		return
	}
	o.line(string(out))
}

func cliList(ctx cliCtx, args []string, o *cliIO) (int, error) {
	args, err := atMost(args, 1, "textus list [<folder>]")
	if err != nil {
		return 0, err
	}
	root, err := cliFolder(ctx, args)
	if err != nil {
		return 0, err
	}
	walked := walkReadableFiles(root, false)
	if ctx.json {
		printJSON(o, walked.files)
	} else {
		for _, file := range walked.files {
			o.line(file)
		}
	}
	if walked.truncated {
		o.warn("The folder is too large to go through completely; some files are missing.")
	}
	return 0, nil
}

func cliRead(ctx cliCtx, args []string, o *cliIO) (int, error) {
	if len(args) != 1 {
		return 0, errors.New("Usage: textus read <file>")
	}
	path := resolveAgainst(ctx.cwd, args[0])
	text, err := markdownOf(path)
	if err != nil {
		return 0, err
	}
	if ctx.json {
		printJSON(o, struct {
			Markdown string `json:"markdown"`
			Path     string `json:"path"`
		}{text, path})
	} else {
		o.text(text)
	}
	return 0, nil
}

// markdownOf is a file's text, or a document's as Markdown.
func markdownOf(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("%s can't be read: %v", path, err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("%s is a folder, not a file.", path)
	}
	if slices.Contains(documentExtensions, fileExt(path)) {
		text, err := documentText(path)
		if err != nil {
			return "", fmt.Errorf("%s can't be read: %v", path, err)
		}
		return text, nil
	}
	if fileKindOf(path) == kindImage {
		return "", fmt.Errorf("%s is an image, which has no text to print.", path)
	}
	if info.Size() > maxTextBytes {
		return "", fmt.Errorf("%s is too large to read as text.", path)
	}
	data, err := os.ReadFile(path)
	if err != nil || slices.Contains(data, 0) {
		return "", fmt.Errorf("%s isn't a text file or a document Textus reads.", path)
	}
	return string(data), nil
}

// searchHit is one match, from the search's `path:line: text`.
type searchHit struct {
	Line int    `json:"line"`
	Path string `json:"path"`
	Text string `json:"text"`
}

func parseHit(hit string) (searchHit, bool) {
	head, text, ok := strings.Cut(hit, ": ")
	if !ok {
		return searchHit{}, false
	}
	at := strings.LastIndexByte(head, ':')
	if at < 0 {
		return searchHit{}, false
	}
	line, err := strconv.Atoi(head[at+1:])
	if err != nil {
		return searchHit{}, false
	}
	return searchHit{Line: line, Path: head[:at], Text: text}, true
}

func cliSearch(ctx cliCtx, args []string, o *cliIO) (int, error) {
	usage := "textus search <query> [<folder>]"
	if len(args) == 0 {
		return 0, errors.New("Usage: " + usage)
	}
	rest, err := atMost(args[1:], 1, usage)
	if err != nil {
		return 0, err
	}
	root, err := cliFolder(ctx, rest)
	if err != nil {
		return 0, err
	}
	needle := strings.ToLower(strings.TrimSpace(args[0]))
	if needle == "" {
		return 0, errors.New("Give some words to search for.")
	}
	walked := walkReadableFiles(root, false)
	found := searchInOrder(root, walked.files, needle, time.Now().Add(cliSearchTime))
	hits := []searchHit{}
	for _, h := range found.hits {
		if hit, ok := parseHit(h); ok {
			hits = append(hits, hit)
		}
	}
	more := len(hits) > maxSearchMatches
	if more {
		hits = hits[:maxSearchMatches]
	}
	if ctx.json {
		printJSON(o, hits)
	} else {
		for _, hit := range hits {
			o.line(fmt.Sprintf("%s:%d: %s", hit.Path, hit.Line, hit.Text))
		}
	}
	switch {
	case more:
		o.warn(fmt.Sprintf("Stopped at %d matches: a more specific query finds the rest.", maxSearchMatches))
	case found.read < len(walked.files):
		o.warn(fmt.Sprintf("Stopped after %d of %d files: the search took too long.", found.read, len(walked.files)))
	case walked.truncated:
		o.warn(strings.TrimPrefix(walkTruncatedNote, "... "))
	}
	if len(hits) == 0 {
		return 1, nil
	}
	return 0, nil
}

func cliNew(ctx cliCtx, args []string, o *cliIO) (int, error) {
	title, err := takeOption(&args, "--title")
	if err != nil {
		return 0, err
	}
	if len(args) != 1 {
		return 0, errors.New("Usage: textus new <file> [--title <title>]")
	}
	path := resolveAgainst(ctx.cwd, args[0])
	if filepath.Ext(path) == "" {
		path += ".md"
	}
	content := ""
	if title != nil {
		content = "# " + *title + "\n"
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if errors.Is(err, fs.ErrExist) {
		return 0, fmt.Errorf("%s is already there; nothing was changed.", path)
	}
	if err != nil {
		return 0, fmt.Errorf("%s can't be created: %v", path, err)
	}
	_, err = f.WriteString(content)
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return 0, fmt.Errorf("%s can't be written: %v", path, err)
	}
	o.line(path)
	return 0, nil
}

func cliMove(ctx cliCtx, args []string, o *cliIO) (int, error) {
	if len(args) != 2 {
		return 0, errors.New("Usage: textus move <from> <to>")
	}
	from := resolveAgainst(ctx.cwd, args[0])
	if _, err := os.Lstat(from); err != nil {
		return 0, fmt.Errorf("%s isn't there.", from)
	}
	to := resolveAgainst(ctx.cwd, args[1])
	if info, err := os.Stat(to); err == nil && info.IsDir() {
		to = filepath.Join(to, filepath.Base(from))
	}
	if _, err := os.Lstat(to); err == nil {
		return 0, fmt.Errorf("%s is already there; nothing was moved.", to)
	}
	if err := moveEntry(from, to); err != nil {
		return 0, fmt.Errorf("%s can't be moved to %s: %v", from, to, err)
	}
	o.line(to)
	return 0, nil
}
