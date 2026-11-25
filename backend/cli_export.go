package backend

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"textus/backend/cli"
)

// `textus export`: the document opens in a window, which runs its export
// there; the export's save dialog takes the terminal's target without
// asking (export_save_dialog).

// cliExportJob is a `textus export`, checked.
type cliExportJob struct {
	file, format, out string
}

// exportExtension is the extension a `textus export --to` format's file
// takes: PDF, HTML or a pandoc writer's; false for one Textus doesn't write.
func exportExtension(formats []pandocFormat, format string) (string, bool) {
	switch format {
	case "pdf", "html":
		return format, true
	}
	for _, f := range formats {
		if f.Format == format {
			return f.Ext, true
		}
	}
	return "", false
}

// withExtension is path with its extension, if any, replaced by ext.
func withExtension(path, ext string) string {
	base := filepath.Base(path)
	if i := strings.LastIndexByte(base, '.'); i > 0 {
		base = base[:i]
	}
	return filepath.Join(filepath.Dir(path), base+"."+ext)
}

// exportJob is `export <file> --to <format> [--out <file>]`, checked.
func exportJob(r cli.Request, formats []pandocFormat) (cliExportJob, error) {
	args := slices.Clone(r.Args)
	to, err := takeOption(&args, "--to")
	if err != nil {
		return cliExportJob{}, err
	}
	out, err := takeOption(&args, "--out")
	if err != nil {
		return cliExportJob{}, err
	}
	if len(args) != 1 {
		return cliExportJob{}, errors.New("Usage: textus export <file> --to <format> [--out <file>]")
	}
	file := resolveAgainst(r.Cwd, args[0])
	if info, err := os.Stat(file); err != nil || !info.Mode().IsRegular() {
		return cliExportJob{}, fmt.Errorf("%s isn't a file.", file)
	}
	if to == nil {
		return cliExportJob{}, errors.New("Say which format with --to (pdf, html, docx, ...).")
	}
	ext, ok := exportExtension(formats, *to)
	if !ok {
		return cliExportJob{}, fmt.Errorf("Textus doesn't export to '%s'. See 'textus help export'.", *to)
	}
	target := withExtension(file, ext)
	if out != nil {
		target = resolveAgainst(r.Cwd, *out)
	}
	if _, err := os.Lstat(target); err == nil {
		return cliExportJob{}, fmt.Errorf("%s is already there; choose another with --out.", target)
	}
	return cliExportJob{file: file, format: *to, out: target}, nil
}

// cliExport opens the document in a window and runs its export there.
func (a *App) cliExport(r cli.Request) cli.Answer {
	var formats []pandocFormat
	if a.menu != nil {
		formats = a.menu.pandoc
	}
	job, err := exportJob(r, formats)
	if err != nil {
		return cli.Fail(err.Error(), 2)
	}
	if job.format != "pdf" && job.format != "html" && detectPandoc() == nil {
		return cli.Fail(fmt.Sprintf("Exporting to %s needs pandoc: https://pandoc.org/installing.html", job.format), 1)
	}
	a.cli.exporting.Lock()
	defer a.cli.exporting.Unlock()
	a.setCLIExportTarget(&job.out)
	defer a.setCLIExportTarget(nil)
	r.Command, r.Args = "export", []string{job.file, job.format}
	opened := a.inWindow(r)
	switch {
	case opened.Exit != 0:
		return opened
	case a.cliWritten(job.out):
		return cli.OK(job.out)
	}
	return cli.Fail("The export didn't finish; Textus says why in its window.", 1)
}

// cliWritten is whether the export wrote out: there, and the same size
// twice in a row. An export that never asked where to save failed before
// it began.
func (a *App) cliWritten(out string) bool {
	started := time.Now()
	last := int64(0)
	for time.Since(started) < cliWindowAnswersWithin {
		if time.Since(started) > cliExportStartsWithin && !a.cliExportTaken() {
			return false
		}
		time.Sleep(250 * time.Millisecond)
		size := int64(0)
		if info, err := os.Stat(out); err == nil {
			size = info.Size()
		}
		if size > 0 && size == last {
			return true
		}
		last = size
	}
	return false
}

// setCLIExportTarget is where the export of a `textus export` goes; nil
// once it is over.
func (a *App) setCLIExportTarget(target *string) {
	a.cli.mu.Lock()
	defer a.cli.mu.Unlock()
	a.cli.exportTarget, a.cli.exportTaken = target, false
}

// takeCLIExportTarget is the target of a `textus export` waiting for its
// save dialog, taken once.
func (a *App) takeCLIExportTarget() (string, bool) {
	a.cli.mu.Lock()
	defer a.cli.mu.Unlock()
	if a.cli.exportTarget == nil || a.cli.exportTaken {
		return "", false
	}
	a.cli.exportTaken = true
	return *a.cli.exportTarget, true
}

func (a *App) cliExportTaken() bool {
	a.cli.mu.Lock()
	defer a.cli.mu.Unlock()
	return a.cli.exportTarget != nil && a.cli.exportTaken
}

// isCLIExportTarget is whether path is where a `textus export` writes:
// the terminal prints it, and no file manager window opens.
func (a *App) isCLIExportTarget(path string) bool {
	a.cli.mu.Lock()
	defer a.cli.mu.Unlock()
	return a.cli.exportTarget != nil && *a.cli.exportTarget == path
}
