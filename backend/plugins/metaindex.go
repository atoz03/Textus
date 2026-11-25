package plugins

import (
	"os"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"unicode/utf8"
)

// What Textus knows of the open folder's documents, for plugins: each
// document's metadata, re-read only when its size or time changes, and
// what changed by generation, so a window is sent only what changed since
// it last asked. Built on the first request.

// MaxDocumentBytes: a document larger than this is left out; no notes file
// is.
const MaxDocumentBytes = 4 * 1024 * 1024

const (
	// removalsKept are the removals kept for callers catching up; past it,
	// they get the whole index.
	removalsKept = 4096
	// rootsKept are the folders kept indexed at once: a window's folder,
	// and another window's.
	rootsKept = 2
)

// generation counts across folders: one dropped and indexed again never
// repeats a number a window holds, which then gets the whole index.
var generation atomic.Uint64

type cached struct {
	modified *float64
	size     int64
	meta     DocumentMeta
	// generation is the one it was last read in.
	generation uint64
}

type removal struct {
	generation uint64
	path       string
}

// rootIndex is one folder's index.
type rootIndex struct {
	docs       map[string]*cached
	generation uint64
	// removed are paths gone, each with the generation it went in; the
	// oldest let go.
	removed []removal
	// floor: a caller behind this has missed removals and is sent the
	// whole index.
	floor uint64
}

// Index is the index of the folders plugins ask about, by root.
type Index struct {
	mu    sync.Mutex
	roots map[string]*rootIndex
}

// IndexFile is a document a folder's walk found.
type IndexFile struct {
	Path string
	// Rel is its path from the folder, `/`-separated.
	Rel string
}

// MetadataAll is the folder's documents and what they hold (blocks left
// out): every one when Whole, else those changed since the caller's
// generation, with the paths gone since (MetadataAll in
// src/platform/bindings/plugin-access.ts).
type MetadataAll struct {
	Generation uint64         `json:"generation"`
	Whole      bool           `json:"whole"`
	Documents  []DocumentMeta `json:"documents"`
	Gone       []string       `json:"gone"`
}

// ModifiedMs is a file's modification time in ms since 1970, nil when it
// has none.
func ModifiedMs(info os.FileInfo) *float64 {
	t := info.ModTime()
	if t.IsZero() {
		return nil
	}
	ms := float64(t.UnixMilli())
	return &ms
}

func sameTime(a, b *float64) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// Refresh brings root's index up to date from the files its walk found
// (cutShort: the walk stopped before the end): what was added, changed or
// gone gets the next generation. It answers a caller at after under the
// same lock, so another folder can't push this one out in between.
func (ix *Index) Refresh(root string, files []IndexFile, cutShort bool, after *uint64) MetadataAll {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	if ix.roots == nil {
		ix.roots = map[string]*rootIndex{}
	}
	if _, ok := ix.roots[root]; !ok && len(ix.roots) >= rootsKept {
		clear(ix.roots)
	}
	known := ix.roots[root]
	if known == nil {
		now := generation.Load()
		known = &rootIndex{docs: map[string]*cached{}, generation: now, floor: now + 1}
		ix.roots[root] = known
	}
	next := generation.Add(1)
	changed := false
	present := map[string]bool{}
	for _, file := range files {
		present[file.Path] = true
		info, err := os.Stat(file.Path)
		if err != nil || info.Size() > MaxDocumentBytes {
			continue
		}
		modified := ModifiedMs(info)
		if c, ok := known.docs[file.Path]; ok && sameTime(c.modified, modified) && c.size == info.Size() {
			continue
		}
		raw, err := os.ReadFile(file.Path)
		// Not UTF-8: not a document of ours.
		if err != nil || !utf8.Valid(raw) {
			continue
		}
		meta := ParseDocument(file.Path, file.Rel, string(raw))
		meta.ModifiedMs = modified
		known.docs[file.Path] = &cached{modified: modified, size: info.Size(), meta: meta, generation: next}
		changed = true
	}
	var gone []string
	for path := range known.docs {
		if present[path] {
			continue
		}
		// A walk cut short (a slow drive, a huge folder) reached only part
		// of the folder: what it missed is gone only if its file is.
		if cutShort && Exists(path) {
			continue
		}
		gone = append(gone, path)
	}
	slices.Sort(gone)
	for _, path := range gone {
		delete(known.docs, path)
		known.removed = append(known.removed, removal{next, path})
		changed = true
	}
	if len(known.removed) > removalsKept {
		cut := len(known.removed) - removalsKept/2
		known.floor = known.removed[cut-1].generation
		known.removed = slices.Clone(known.removed[cut:])
	}
	if changed {
		known.generation = next
	}
	return since(known, after)
}

// since is what a caller at generation after hasn't seen: everything when
// it has seen nothing of this index, or missed removals no longer kept.
func since(known *rootIndex, after *uint64) MetadataAll {
	whole := after == nil || *after < known.floor || *after > known.generation
	var from uint64
	if !whole {
		from = *after
	}
	out := MetadataAll{Generation: known.generation, Whole: whole, Documents: []DocumentMeta{}, Gone: []string{}}
	for _, c := range known.docs {
		if c.generation > from {
			out.Documents = append(out.Documents, c.meta)
		}
	}
	slices.SortStableFunc(out.Documents, func(a, b DocumentMeta) int {
		return strings.Compare(strings.ToLower(a.Rel), strings.ToLower(b.Rel))
	})
	if !whole {
		for _, r := range known.removed {
			if r.generation > from {
				out.Gone = append(out.Gone, r.path)
			}
		}
	}
	return out
}
