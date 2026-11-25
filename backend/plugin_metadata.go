package backend

import (
	"os"
	"strings"

	"textus/backend/plugins"
)

// What a folder's documents hold, for textus.metadata: their front matter,
// tags, tasks, links and headings, kept per folder so a window asks only
// for what changed since it last asked.

// metadataOf brings root's index up to date and answers a caller at
// generation after.
func (a *App) metadataOf(root string, after *uint64) plugins.MetadataAll {
	walked := walk(root, walkLimits, isMarkdown)
	files := make([]plugins.IndexFile, len(walked.files))
	for i, f := range walked.files {
		files[i] = plugins.IndexFile{Path: f.path, Rel: f.rel}
	}
	return a.plug.metadata.Refresh(root, files, walked.truncated, after)
}

// documentMeta is what one document holds now, its blocks with it - read
// afresh; nil when it can't be read.
func documentMeta(root, path string) (*plugins.DocumentMeta, error) {
	file, err := plugins.InFolder(root, path)
	if err != nil {
		return nil, err
	}
	text, err := readUTF8(file)
	if err != nil {
		return nil, nil
	}
	real, err := plugins.Canonical(root)
	if err != nil {
		return nil, err
	}
	rel := path
	if under, ok := pathUnder(file, real); ok {
		rel = strings.Join(under, "/")
	}
	meta := plugins.ParseDocument(file, rel, text)
	blocks := plugins.BlocksOf(text)
	meta.Blocks = &blocks
	if info, err := os.Stat(file); err == nil {
		meta.ModifiedMs = plugins.ModifiedMs(info)
	}
	return &meta, nil
}

func init() {
	// The folder's documents and what they hold (blocks left out): every
	// one when whole, else those changed since the caller's generation,
	// with the paths gone since.
	command("plugin_metadata_all", func(c *Call, a struct {
		ID    string  `json:"id"`
		Root  string  `json:"root"`
		After *uint64 `json:"after"`
	}) (plugins.MetadataAll, error) {
		if err := c.app.checkedRoot(a.ID, a.Root); err != nil {
			return plugins.MetadataAll{}, err
		}
		return c.app.metadataOf(a.Root, a.After), nil
	})
	command("plugin_metadata_get", func(c *Call, a struct {
		ID   string `json:"id"`
		Root string `json:"root"`
		Path string `json:"path"`
	}) (*plugins.DocumentMeta, error) {
		if err := c.app.checkedRoot(a.ID, a.Root); err != nil {
			return nil, err
		}
		return documentMeta(a.Root, a.Path)
	})
	// Reads no file: the window hands the texts in.
	command("plugin_metadata_of_texts", func(c *Call, a struct {
		Documents []plugins.DocumentText `json:"documents"`
		Blocks    bool                   `json:"blocks"`
	}) ([]plugins.DocumentMeta, error) {
		return plugins.OfTexts(a.Documents, a.Blocks), nil
	})
}
