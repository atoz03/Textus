package backend

import (
	"encoding/json/v2"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf16"

	"textus/backend/plugins"
)

// Links between notes and to pictures, resolved the way the app that keeps
// a vault does: a wikilink by name anywhere in the vault, nearest first.

const (
	vaultMaxFiles     = 100_000
	vaultMaxEntries   = 500_000
	vaultMaxDepth     = 24
	vaultIndexTTL     = 60 * time.Second
	vaultMissRebuild  = 2 * time.Second
	vaultCachedVaults = 4
)

// imageTypes are the picture types a vault link may name, by extension
// (src/editor/images/image-types.json).
var imageTypes = map[string]bool{}

func loadImageTypes(raw []byte) {
	var types map[string]string
	if json.Unmarshal(raw, &types) != nil {
		return
	}
	for ext := range types {
		imageTypes[strings.ToLower(ext)] = true
	}
}

type linkKind int

const (
	linkImage linkKind = iota
	linkNote
)

func vaultRoot(docPath, folder string) string {
	docDir := filepath.Dir(docPath)
	if root := conventionsOfFolder(docDir).notesRoot(); root != "" {
		return root
	}
	if folder != "" {
		if _, ok := pathUnder(docDir, folder); ok {
			return folder
		}
	}
	return docDir
}

func isVaultImage(path string) bool { return imageTypes[fileExt(path)] }

// joinWithin joins a relative link to base, never leaving root.
func joinWithin(root, base, link string) (string, bool) {
	if filepath.IsAbs(link) || filepath.VolumeName(link) != "" {
		return "", false
	}
	out := base
	for _, part := range strings.FieldsFunc(link, func(r rune) bool { return r == '/' || r == filepath.Separator }) {
		switch part {
		case ".":
		case "..":
			parent := filepath.Dir(out)
			if parent == out {
				return "", false
			}
			out = parent
		default:
			out = filepath.Join(out, part)
		}
	}
	if _, ok := pathUnder(out, root); !ok {
		return "", false
	}
	return out, true
}

// indexVault lists a vault's files, relative and with forward slashes;
// cut when it stopped at a limit.
func indexVault(root string, maxFiles, maxEntries int) (found []string, cut bool) {
	type dir struct {
		path  string
		depth int
	}
	visited := 0
	stack := []dir{{root, 0}}
	for len(stack) > 0 {
		d := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		entries, err := os.ReadDir(d.path)
		if err != nil {
			continue
		}
		for _, e := range entries {
			visited++
			if visited > maxEntries || len(found) >= maxFiles {
				return found, true
			}
			name := e.Name()
			if strings.HasPrefix(name, ".") {
				continue
			}
			path := filepath.Join(d.path, name)
			switch t := e.Type(); {
			case t.IsDir():
				if name != "node_modules" && d.depth+1 < vaultMaxDepth {
					stack = append(stack, dir{path, d.depth + 1})
				}
			case t.IsRegular():
				if rel, err := filepath.Rel(root, path); err == nil {
					found = append(found, filepath.ToSlash(rel))
				}
			}
		}
	}
	return found, false
}

func folderDistance(from, to []string) int {
	common := 0
	for common < len(from) && common < len(to) && from[common] == to[common] {
		common++
	}
	return (len(from) - common) + (len(to) - common)
}

// fileNames are the names a link may mean: a note's link without an
// extension names any of its spellings (a plugin's document types last),
// then the name as written.
func fileNames(link string, kind linkKind) []string {
	if kind == linkNote && !isMarkdown(link) {
		names := []string{link + ".md", link + ".markdown", link + ".mdx"}
		for _, ext := range plugins.DocumentTypes() {
			names = append(names, link+"."+ext)
		}
		return append(names, link)
	}
	return []string{link}
}

func joinRel(dir, link string) (string, bool) {
	var parts []string
	for _, p := range strings.Split(dir, "/") {
		if p != "" {
			parts = append(parts, p)
		}
	}
	for _, part := range strings.FieldsFunc(link, func(r rune) bool { return r == '/' || r == '\\' }) {
		switch part {
		case ".":
		case "..":
			if len(parts) == 0 {
				return "", false
			}
			parts = parts[:len(parts)-1]
		default:
			parts = append(parts, part)
		}
	}
	return strings.Join(parts, "/"), true
}

func folderOf(rel string) string {
	if i := strings.LastIndexByte(rel, '/'); i >= 0 {
		return rel[:i]
	}
	return ""
}

func folders(rel string) []string {
	var out []string
	for _, p := range strings.Split(rel, "/") {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func direct(index []string, rel string, ok bool, kind linkKind) (string, bool) {
	if !ok {
		return "", false
	}
	for _, name := range fileNames(rel, kind) {
		lower := strings.ToLower(name)
		for _, p := range index {
			if strings.ToLower(p) == lower {
				return p, true
			}
		}
	}
	return "", false
}

func utf16Len(s string) int { return len(utf16.Encode([]rune(s))) }

func byName(index []string, link, fromDir string, kind linkKind) (string, bool) {
	names := fileNames(link, kind)
	// A note's spellings (.md, .markdown) compete as one; the name as
	// written is only tried after them.
	groups := [][]string{names}
	if len(names) > 1 {
		groups = [][]string{names[:len(names)-1], names[len(names)-1:]}
	}
	from := folders(fromDir)
	for _, group := range groups {
		type want struct{ lower, tail string }
		var wanted []want
		for _, name := range group {
			for strings.HasPrefix(name, "./") {
				name = name[2:]
			}
			lower := strings.ToLower(name)
			wanted = append(wanted, want{lower, "/" + lower})
		}
		best, found := "", false
		var bestKey [2]int
		for _, rel := range index {
			lr := strings.ToLower(rel)
			if !slices.ContainsFunc(wanted, func(w want) bool { return lr == w.lower || strings.HasSuffix(lr, w.tail) }) {
				continue
			}
			key := [2]int{folderDistance(from, folders(folderOf(rel))), utf16Len(rel)}
			if !found || key[0] < bestKey[0] || (key[0] == bestKey[0] && (key[1] < bestKey[1] || (key[1] == bestKey[1] && rel < best))) {
				best, bestKey, found = rel, key, true
			}
		}
		if found {
			return best, true
		}
	}
	return "", false
}

func pickFromIndex(index []string, link string, wiki bool, from string, kind linkKind) (string, bool) {
	link = strings.ReplaceAll(link, "\\", "/")
	fromDir := folderOf(from)
	relative := strings.HasPrefix(link, "./") || strings.HasPrefix(link, "../")
	if !wiki || relative {
		rel, ok := joinRel(fromDir, link)
		found, hit := direct(index, rel, ok, kind)
		if hit || relative || strings.Contains(link, "/") {
			return found, hit
		}
		return byName(index, link, fromDir, kind)
	}
	if strings.Contains(link, "/") {
		if rel, ok := joinRel("", link); ok {
			if found, hit := direct(index, rel, true, kind); hit {
				return found, true
			}
		}
		if rel, ok := joinRel(fromDir, link); ok {
			if found, hit := direct(index, rel, true, kind); hit {
				return found, true
			}
		}
		return byName(index, link, fromDir, kind)
	}
	return byName(index, link, fromDir, kind)
}

func isFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

func resolveWith(docPath, folder, link string, kind linkKind, wiki bool, index func(root string, fresh bool) []string) (string, bool) {
	link = strings.TrimSpace(link)
	if link == "" || (filepath.IsAbs(link) && !strings.HasPrefix(link, "/")) || filepath.VolumeName(link) != "" {
		return "", false
	}
	fits := func(path string) bool { return kind == linkNote || isVaultImage(path) }
	if fromRoot, ok := strings.CutPrefix(link, "/"); ok {
		root := linkRoot(docPath, folder)
		if root == "" {
			return "", false
		}
		for _, name := range fileNames(fromRoot, kind) {
			if p, ok := joinWithin(root, root, name); ok && isFile(p) && fits(p) {
				return p, true
			}
		}
		return "", false
	}
	root := vaultRoot(docPath, folder)
	inside := func(path string) (string, bool) {
		// A symlink inside the vault can still point out of it.
		real, err := filepath.EvalSymlinks(path)
		if err != nil {
			return "", false
		}
		realRoot, err := filepath.EvalSymlinks(root)
		if err != nil {
			return "", false
		}
		if _, ok := pathUnder(real, realRoot); ok && isFile(real) && fits(path) {
			return path, true
		}
		return "", false
	}
	relFrom, ok := pathUnder(docPath, root)
	if !ok {
		return "", false
	}
	from := strings.Join(relFrom, "/")
	for _, fresh := range []bool{false, true} {
		if rel, ok := pickFromIndex(index(root, fresh), link, wiki, from, kind); ok {
			p, ok := joinWithin(root, root, rel)
			if !ok {
				return "", false
			}
			return inside(p)
		}
	}
	return "", false
}

type cachedIndex struct {
	root  string
	built time.Time
	files []string
	cut   bool // the walk stopped at a limit: a file not listed may exist
}

var vaultIndexes struct {
	sync.Mutex
	list []cachedIndex
}

func touchCached(root string, fresh *cachedIndex) {
	i := slices.IndexFunc(vaultIndexes.list, func(c cachedIndex) bool { return c.root == root })
	var entry cachedIndex
	switch {
	case fresh != nil:
		if i >= 0 {
			vaultIndexes.list = slices.Delete(vaultIndexes.list, i, i+1)
		}
		entry = *fresh
	case i >= 0:
		entry = vaultIndexes.list[i]
		vaultIndexes.list = slices.Delete(vaultIndexes.list, i, i+1)
	default:
		return
	}
	vaultIndexes.list = slices.Insert(vaultIndexes.list, 0, entry)
	if len(vaultIndexes.list) > vaultCachedVaults {
		vaultIndexes.list = vaultIndexes.list[:vaultCachedVaults]
	}
}

func vaultIndex(root string, fresh bool) []string {
	limit := vaultIndexTTL
	if fresh {
		limit = vaultMissRebuild
	}
	vaultIndexes.Lock()
	for _, c := range vaultIndexes.list {
		if c.root == root && time.Since(c.built) < limit {
			files := c.files
			touchCached(root, nil)
			vaultIndexes.Unlock()
			return files
		}
	}
	vaultIndexes.Unlock()
	files, cut := indexVault(root, vaultMaxFiles, vaultMaxEntries)
	vaultIndexes.Lock()
	touchCached(root, &cachedIndex{root: root, built: time.Now(), files: files, cut: cut})
	vaultIndexes.Unlock()
	return files
}

func indexCut(root string) bool {
	vaultIndexes.Lock()
	defer vaultIndexes.Unlock()
	return slices.ContainsFunc(vaultIndexes.list, func(c cachedIndex) bool { return c.root == root && c.cut })
}

func newNoteFolder(root string) (string, bool) {
	found := conventionsOfFolder(root)
	setting := found.NewNotes
	if setting == nil && found.Detected != nil {
		setting = found.Detected.NewNotes
	}
	if setting == nil {
		return "", false
	}
	switch setting.Value {
	case "./":
		return "", false
	case "/", "":
		return root, true
	default:
		return joinWithin(root, root, strings.TrimLeft(setting.Value, "/"))
	}
}

func newNotePath(docPath, folder, link string, wiki bool) (string, bool) {
	root := vaultRoot(docPath, folder)
	docDir := filepath.Dir(docPath)
	name := fileNames(strings.TrimSpace(link), linkNote)[0]
	relative := strings.HasPrefix(name, "./") || strings.HasPrefix(name, "../")
	base := docDir
	switch {
	case !wiki || relative:
	case strings.Contains(name, "/"):
		base = root
	default:
		if f, ok := newNoteFolder(root); ok {
			base = f
		}
	}
	return joinWithin(root, base, name)
}

// linkRoot is the folder a link starting with / starts from.
func linkRoot(docPath, folder string) string {
	c := conventionsOfDocument(docPath)
	if c.LinkRoot != nil {
		return c.LinkRoot.Value
	}
	if root := c.notesRoot(); root != "" {
		return root
	}
	if folder != "" {
		if _, ok := pathUnder(docPath, folder); ok {
			return folder
		}
	}
	return ""
}

type NoteLinkTarget struct {
	Path   string `json:"path"`
	Exists bool   `json:"exists"`
}

func init() {
	command("resolve_vault_image", func(c *Call, a struct {
		DocPath string  `json:"docPath"`
		Link    string  `json:"link"`
		Folder  *string `json:"folder"`
	}) (*string, error) {
		if err := requireNonEmpty(a.DocPath); err != nil {
			return nil, err
		}
		if p, ok := resolveWith(a.DocPath, or(a.Folder, ""), a.Link, linkImage, true, vaultIndex); ok {
			return &p, nil
		}
		return nil, nil
	})
	command("resolve_note_link", func(c *Call, a struct {
		DocPath string  `json:"docPath"`
		Link    string  `json:"link"`
		Folder  *string `json:"folder"`
		Wiki    bool    `json:"wiki"`
	}) (*NoteLinkTarget, error) {
		if err := requireNonEmpty(a.DocPath); err != nil {
			return nil, err
		}
		folder := or(a.Folder, "")
		if p, ok := resolveWith(a.DocPath, folder, a.Link, linkNote, a.Wiki, vaultIndex); ok {
			return &NoteLinkTarget{Path: p, Exists: true}, nil
		}
		// The vault is larger than what was looked through: the note may
		// well be there, and making it here would make a second one.
		if indexCut(vaultRoot(a.DocPath, folder)) {
			return nil, nil
		}
		if p, ok := newNotePath(a.DocPath, folder, a.Link, a.Wiki); ok {
			return &NoteLinkTarget{Path: p, Exists: false}, nil
		}
		return nil, nil
	})
	command("document_link_root", func(c *Call, a struct {
		DocPath string  `json:"docPath"`
		Folder  *string `json:"folder"`
	}) (*string, error) {
		if err := requireNonEmpty(a.DocPath); err != nil {
			return nil, err
		}
		if root := linkRoot(a.DocPath, or(a.Folder, "")); root != "" {
			return &root, nil
		}
		return nil, nil
	})
}
