package backend

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/egoist/mygo"

	"textus/backend/ai"
)

// The project folder as writing material: its files for @-mentions, the
// style profile "Learn my style" writes, the references imported, and
// what of the library the last request carried.

const (
	// styleSampleChars is the most of one writing sample "Learn my style"
	// reads, and styleTotalChars the most of all of them.
	styleSampleChars = 20_000
	styleTotalChars  = 60_000
	// styleSampleFiles are the most samples read.
	styleSampleFiles = 8
)

// textDocumentExtensions are the documents Textus opens as text.
var textDocumentExtensions = []string{"md", "markdown", "mdx", "txt", "text"}

// projectArgs name a document or open folder, whose project a command is
// about.
type projectArgs struct {
	DocPath       *string `json:"docPath"`
	WorkspaceRoot *string `json:"workspaceRoot"`
}

// root is the project folder, or noProject.
func (p projectArgs) root() (string, error) {
	root, ok := projectRoot(deref(p.DocPath), deref(p.WorkspaceRoot))
	if !ok {
		return "", codeError("noProject")
	}
	return root, nil
}

// lastSentLibrary is the last request's library use per document path, so
// the Memory tab's marks are what was actually sent.
var lastSentLibrary struct {
	sync.Mutex
	byDoc map[string]ai.LibraryUse
}

// recordLibraryUse keeps what a request for docPath carried of the
// library.
func recordLibraryUse(docPath string, pick ai.LibraryPick) {
	lastSentLibrary.Lock()
	defer lastSentLibrary.Unlock()
	if lastSentLibrary.byDoc == nil {
		lastSentLibrary.byDoc = map[string]ai.LibraryUse{}
	}
	lastSentLibrary.byDoc[docPath] = ai.UseOf(pick, true)
}

func sentLibraryUse(docPath string) (ai.LibraryUse, bool) {
	lastSentLibrary.Lock()
	defer lastSentLibrary.Unlock()
	use, ok := lastSentLibrary.byDoc[docPath]
	return use, ok
}

// styleSamples is the user turn of a "Learn my style" request: up to eight
// picked files, each cut to styleSampleChars, until styleTotalChars.
func styleSamples(files []string) (string, error) {
	var samples strings.Builder
	for _, file := range files[:min(len(files), styleSampleFiles)] {
		if utf8.RuneCountInString(samples.String()) >= styleTotalChars {
			break
		}
		text, ok := sampleText(file)
		if !ok {
			continue
		}
		fmt.Fprintf(&samples, "<sample name=\"%s\">\n%s\n</sample>\n\n", filepath.Base(file), charPrefixOf(text, styleSampleChars))
	}
	if strings.TrimSpace(samples.String()) == "" {
		return "", codeError("noSamples")
	}
	return samples.String(), nil
}

// sampleText is a writing sample's text: a plain-text file of the
// writer's, none of its folders hidden, read no further than its first
// styleSampleChars need.
func sampleText(path string) (string, bool) {
	for _, part := range strings.Split(filepath.ToSlash(path), "/") {
		if strings.HasPrefix(part, ".") && part != "." && part != ".." {
			return "", false
		}
	}
	if !isTextFile(path) && fileExt(path) != "rst" {
		return "", false
	}
	f, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, styleSampleChars*4))
	if err != nil {
		return "", false
	}
	return strings.ToValidUTF8(string(raw), "\uFFFD"), true
}

// learnStyle writes the profile drafted from samples to root's
// .textus/style.md, keeping the one it replaces as style.previous.md, and
// answers its path.
func learnStyle(root, profile string) (string, error) {
	profile = strings.TrimSpace(profile)
	if profile == "" {
		return "", errors.New("The model returned an empty profile.")
	}
	path := projectFileIn(root, "style.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
		previous := filepath.Join(filepath.Dir(path), "style.previous.md")
		if raw, err := os.ReadFile(path); err == nil {
			_ = writeAtomic(previous, raw)
		}
	}
	return path, writeAtomic(path, []byte(profile+"\n"))
}

// ensureProjectFile makes .textus/<rel> with template unless it is there,
// and answers its path.
func ensureProjectFile(root, rel, template string) (string, error) {
	rel = strings.TrimLeft(rel, `/\`)
	for _, part := range strings.Split(strings.ReplaceAll(rel, `\`, "/"), "/") {
		if part == ".." || part == "" {
			return "", codeError("badName")
		}
	}
	path := filepath.Join(projectDir(root), rel)
	if !exists(path) {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return "", err
		}
		if err := writeAtomic(path, []byte(template)); err != nil {
			return "", err
		}
	}
	return path, nil
}

// libraryUse is the Memory tab's "used" marks: what the last request for
// the document carried, or before one, what the document's text brings in
// by itself.
func libraryUse(p projectArgs, document string) ai.LibraryUse {
	if sent, ok := sentLibraryUse(deref(p.DocPath)); ok {
		return sent
	}
	root, ok := projectRoot(deref(p.DocPath), deref(p.WorkspaceRoot))
	if !ok {
		return ai.LibraryUse{}
	}
	doc := ai.DocumentFromMarkdown(document)
	context := ai.DocumentSection(&doc, ai.LeastReading(true), "")
	return ai.UseOf(ai.PickLibrary(readLibrary(root), context), false)
}

func init() {
	// The project's text files, for the composer's @-mention picker.
	command("list_project_files", func(c *Call, a projectArgs) ([]string, error) {
		root, err := a.root()
		if err != nil {
			return []string{}, nil
		}
		return walkTextFiles(root).files, nil
	})
	// One project file's text, for an @-mention, under the same safety
	// rules as the agent's read_file.
	command("read_project_file", func(c *Call, a struct {
		projectArgs
		Path string `json:"path"`
	}) (string, error) {
		root, err := a.root()
		if err != nil {
			return "", err
		}
		return readMention(root, a.Path)
	})
	command("library_use", func(c *Call, a struct {
		projectArgs
		Document string `json:"document"`
	}) (ai.LibraryUse, error) {
		return libraryUse(a.projectArgs, a.Document), nil
	})
	// .textus/<rel> made with template unless it is there; its path.
	command("ensure_project_file", func(c *Call, a struct {
		projectArgs
		Rel      string `json:"rel"`
		Template string `json:"template"`
	}) (string, error) {
		root, err := a.root()
		if err != nil {
			return "", err
		}
		return ensureProjectFile(root, a.Rel, a.Template)
	})
	// Several files picked, the writing samples "Learn my style" reads.
	command("pick_text_files", func(c *Call, _ none) ([]string, error) {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
			Parent:   c.parent(),
			Multiple: true,
			Filters:  []mygo.FileFilter{{Name: "Text", Extensions: textDocumentExtensions}},
		})
		if err != nil || paths == nil {
			return []string{}, nil
		}
		return paths, nil
	})
	// "Import references…" and "Import a reference folder…": the picked
	// files (zips unpacked) or folder copied into the project's references/
	// folder, then indexed in the background for search_references; null
	// when the user cancels.
	command("import_references_picked", func(c *Call, a struct {
		projectArgs
		Folder bool `json:"folder"`
	}) (*importedReferences, error) {
		root, err := a.root()
		if err != nil {
			return nil, err
		}
		var picked []string
		if a.Folder {
			if one, err := pickFolder(c); err == nil && one != nil {
				picked = []string{*one}
			}
		} else {
			kinds := append([]string{"zip"}, textDocumentExtensions...)
			kinds = append(kinds, agentTextExtensions...)
			picked, _ = mygo.Dialog.Open(mygo.OpenDialogOptions{
				Parent:   c.parent(),
				Multiple: true,
				Filters:  []mygo.FileFilter{{Name: "References", Extensions: kinds}},
			})
		}
		if len(picked) == 0 {
			return nil, nil
		}
		done, err := importReferences(root, picked)
		if err != nil {
			return nil, err
		}
		go warmReferences(root)
		return &done, nil
	})
	// "Learn my style": the picked samples analysed into .textus/style.md
	// (the one before kept as style.previous.md); its path, which the
	// frontend opens for review.
	command("learn_style", func(c *Call, a struct {
		Provider string  `json:"provider"`
		Model    *string `json:"model"`
		projectArgs
		Files []string `json:"files"`
	}) (string, error) {
		if err := c.app.ensureAIOn(); err != nil {
			return "", err
		}
		root, err := a.root()
		if err != nil {
			return "", err
		}
		samples, err := styleSamples(a.Files)
		if err != nil {
			return "", err
		}
		profile, err := c.app.complete(c.ctx, a.Provider, deref(a.Model), ai.StyleInstructions(), samples)
		if err != nil {
			return "", err
		}
		return learnStyle(root, profile)
	})
}
