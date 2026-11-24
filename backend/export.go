package backend

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Export through Pandoc, when the user has it. PDF export, which the Tauri
// build made with the system's webview, is not in this build.

func pandocCandidates() []string {
	list := []string{"pandoc", "/opt/homebrew/bin/pandoc", "/usr/local/bin/pandoc", "/opt/local/bin/pandoc", "/usr/bin/pandoc"}
	if home := os.Getenv("HOME"); home != "" {
		list = append(list, filepath.Join(home, ".local", "bin", "pandoc"))
	}
	for _, base := range []string{"LOCALAPPDATA", "ProgramFiles"} {
		if dir := os.Getenv(base); dir != "" {
			list = append(list, filepath.Join(dir, "Pandoc", "pandoc.exe"))
		}
	}
	return list
}

// pandocPlaces are where Pandoc is looked for: the usual places, then the
// PATH the app was started with.
func pandocPlaces() []string {
	list := pandocCandidates()
	if found, err := exec.LookPath("pandoc"); err == nil && !containsString(list, found) {
		list = append(list, found)
	}
	return list
}

func detectPandoc() *string {
	for _, candidate := range pandocPlaces() {
		cmd := exec.Command(candidate, "--version")
		if cmd.Run() == nil {
			return &candidate
		}
	}
	return nil
}

var (
	ownedLong      = []string{"from", "read", "to", "write", "output"}
	ownedShort     = "frtwo"
	shortWithValue = "MdVHBAFLTcD"
	shortOptional  = "sNpi"
	longWithValue  = strings.Fields(`abbreviations base-header-level bibliography chunk-template citation-abbreviations
		columns csl css data-dir defaults dpi epub-chapter-level epub-cover-image epub-embed-font epub-metadata
		epub-subdirectory extract-media filter highlight-style id-prefix include-after-body include-before-body
		include-in-header indented-code-classes log lua-filter math-method metadata metadata-file number-offset
		pdf-engine pdf-engine-opt reference-doc request-header resource-path shift-heading-level-by slide-level
		split-level syntax-definition tab-stop template title-prefix toc-depth variable variable-json`)
)

func anyHasPrefix(list []string, prefix string) bool {
	for _, s := range list {
		if strings.HasPrefix(s, prefix) {
			return true
		}
	}
	return false
}

// ownedArg is the first of the user's Pandoc arguments that Textus sets
// itself (what is read and written, and where), or a stray value.
func ownedArg(args []string) (string, bool) {
	valueNext := false
	for _, arg := range args {
		if valueNext {
			valueNext = false
			continue
		}
		if long, ok := strings.CutPrefix(arg, "--"); ok {
			name, _, hasValue := strings.Cut(long, "=")
			if name == "" || anyHasPrefix(ownedLong, name) {
				return arg, true
			}
			valueNext = !hasValue && anyHasPrefix(longWithValue, name)
		} else if short, ok := strings.CutPrefix(arg, "-"); ok && short != "" {
			for i, c := range short {
				if strings.ContainsRune(ownedShort, c) {
					return arg, true
				}
				if strings.ContainsRune(shortWithValue, c) {
					valueNext = i+len(string(c)) == len(short)
					break
				}
				if strings.ContainsRune(shortOptional, c) {
					break
				}
			}
		} else {
			return arg, true
		}
	}
	return "", false
}

const luaFilter = `function Span(s)
  if s.classes:includes("textus-sub") then return pandoc.Subscript(s.content) end
  if s.classes:includes("textus-sup") then return pandoc.Superscript(s.content) end
end
function RawInline(r)
  if r.format == "textuscite" then
    local blocks = pandoc.read(r.text, "markdown+citations").blocks
    if blocks[1] then return blocks[1].content end
  end
end
`

type PandocOptions struct {
	Toc            bool     `json:"toc"`
	NumberSections bool     `json:"numberSections"`
	Bibliography   *string  `json:"bibliography"`
	Csl            *string  `json:"csl"`
	ExtraArgs      []string `json:"extraArgs"`
	HardLineBreaks bool     `json:"hardLineBreaks"`
}

func pandocArgs(format, title string, hardBreaks bool) []string {
	titleKey := "pagetitle"
	if format == "epub" {
		titleKey = "title"
	}
	from := "commonmark_x-smart-subscript-superscript-fancy_lists-definition_lists-fenced_divs-implicit_header_references+autolink_bare_uris"
	if hardBreaks {
		from += "+hard_line_breaks"
	}
	return []string{"--from", from, "--to", format, "--standalone", "--metadata", titleKey + "=" + title}
}

func settingsArgs(o PandocOptions) []string {
	var args []string
	if o.Toc {
		args = append(args, "--toc")
	}
	if o.NumberSections {
		args = append(args, "--number-sections")
	}
	if o.Bibliography != nil && *o.Bibliography != "" {
		args = append(args, "--citeproc", "--bibliography="+*o.Bibliography)
		if o.Csl != nil && *o.Csl != "" {
			args = append(args, "--csl="+*o.Csl)
		}
	}
	return args
}

type pandocExport struct {
	PandocPath   string        `json:"pandocPath"`
	Markdown     string        `json:"markdown"`
	OutputPath   string        `json:"outputPath"`
	Format       string        `json:"format"`
	ResourceDir  *string       `json:"resourceDir"`
	Title        string        `json:"title"`
	ReferenceDoc *string       `json:"referenceDoc"`
	Options      PandocOptions `json:"options"`
}

func exportViaPandoc(e pandocExport) error {
	// Only a Pandoc found where detect_pandoc looks: the page names which,
	// never a program of its own.
	if !containsString(pandocPlaces(), e.PandocPath) {
		return errorf("%s isn't a Pandoc Textus found.", e.PandocPath)
	}
	if arg, ok := ownedArg(e.Options.ExtraArgs); ok {
		return errorf("The Pandoc argument %s is set by Textus.", arg)
	}
	filter, err := os.CreateTemp("", "textus-pandoc-*.lua")
	if err != nil {
		return err
	}
	defer os.Remove(filter.Name())
	if _, err := filter.WriteString(luaFilter); err != nil {
		filter.Close()
		return err
	}
	filter.Close()
	args := []string{"--lua-filter", filter.Name()}
	args = append(args, e.Options.ExtraArgs...)
	args = append(args, pandocArgs(e.Format, e.Title, e.Options.HardLineBreaks)...)
	args = append(args, settingsArgs(e.Options)...)
	args = append(args, "--output", e.OutputPath)
	cmd := exec.Command(e.PandocPath)
	if e.ResourceDir != nil {
		args = append(args, "--resource-path", *e.ResourceDir)
		cmd.Dir = *e.ResourceDir
	}
	if e.ReferenceDoc != nil {
		if info, err := os.Stat(*e.ReferenceDoc); err == nil && info.Mode().IsRegular() {
			args = append(args, "--reference-doc", *e.ReferenceDoc)
		}
	}
	cmd.Args = append(cmd.Args, args...)
	cmd.Stdin = strings.NewReader(e.Markdown)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return errors.New(strings.TrimSpace(stderr.String()))
		}
		return err
	}
	return nil
}

func init() {
	command("detect_pandoc", func(c *Call, _ none) (*string, error) { return detectPandoc(), nil })
	command("check_pandoc_args", func(c *Call, a struct {
		Args []string `json:"args"`
	}) (*string, error) {
		if arg, ok := ownedArg(a.Args); ok {
			return &arg, nil
		}
		return nil, nil
	})
	command("export_via_pandoc", func(c *Call, a pandocExport) (nothing, error) {
		return nil, exportViaPandoc(a)
	})
	command("export_pdf_native", func(c *Call, _ none) (nothing, error) {
		return nil, codeError("pdfUnsupported")
	})
}
