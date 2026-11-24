package backend

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo"
)

// File › Export as PDF: the themed, paginated document the page made
// (src/export/exporters.ts) is loaded into a hidden window and printed
// with the webview's own PDF backend (MyGo's PrintToPDF: WebKit's print
// operation on macOS and Linux, Chromium's printToPDF on Windows), with no
// print dialog.

// PdfSheet is the paper and the band left at its top and bottom, in points
// (src/export/pdf-page.ts pdfSheet; bindings/app.ts PdfSheet).
type PdfSheet struct {
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
	// Margin is read on Linux only: elsewhere the page laid the band out
	// itself (paginate.ts).
	Margin float64 `json:"margin"`
}

// pdfTimeout bounds the render, so one that never reports back can't
// leave the page's progress overlay up forever.
const pdfTimeout = 60 * time.Second

// cssPxPerPt: the page is laid out at 96 CSS px an inch, a sheet at 72 pt.
const cssPxPerPt = 4.0 / 3

var pdfWindowCounter atomic.Uint32

// pdfOptions is how MyGo prints the sheet on goos. The frontend lays the
// top and bottom band into the page itself on macOS and Windows; WebKitGTK
// leaves a CSS margin out, so Linux's comes from the page setup.
func pdfOptions(sheet PdfSheet, goos string) mygo.PDFOptions {
	const ptPerInch = 72
	opts := mygo.PDFOptions{
		PageSize:   mygo.PageSize{Width: sheet.Width / ptPerInch, Height: sheet.Height / ptPerInch},
		Margins:    &mygo.Margins{},
		Background: true,
	}
	if goos == "linux" {
		opts.Margins.Top = sheet.Margin / ptPerInch
		opts.Margins.Bottom = sheet.Margin / ptPerInch
	}
	return opts
}

// settleScript waits for the fonts and pictures, or the PDF captures
// fallback glyphs and empty boxes. On macOS it pins the page to the
// sheet's width first: WebKit's print layout shrinks a page wider than the
// paper to fit it, which lands at 3/4 pt a CSS px, the scale the page was
// paginated at (pdf-page.ts pageGeometry "mac").
func settleScript(sheet PdfSheet, goos string) string {
	pin := ""
	if goos == "darwin" {
		pin = "document.documentElement.style.width = '" +
			strconv.FormatFloat(sheet.Width*cssPxPerPt, 'f', 3, 64) + "px';"
	}
	return pin + `await document.fonts.ready;
await Promise.all(Array.from(document.images).map(i => i.complete ? null : i.decode().catch(() => null)));
return true;`
}

// exportPDF prints html to a PDF at outputPath, numbering its pages when
// asked (Settings › "Page numbers in PDF").
func exportPDF(ctx context.Context, html, outputPath string, pageNumbers bool, sheet PdfSheet) error {
	if sheet.Width <= 0 || sheet.Height <= 0 {
		return errors.New("the paper has no size")
	}
	// A file, not a string: WebView2 caps a page loaded from a string, and
	// inline pictures can pass the cap.
	page, err := os.CreateTemp("", "textus-pdf-export-*.html")
	if err != nil {
		return err
	}
	defer os.Remove(page.Name())
	_, err = page.WriteString(html)
	if cerr := page.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	pdf, err := printPage(ctx, page.Name(), sheet)
	if err != nil {
		return err
	}
	if pageNumbers {
		// A PDF it can't read is kept unnumbered: the export itself worked.
		if numbered, nerr := numberPages(pdf); nerr == nil {
			pdf = numbered
		}
	}
	return writeAtomic(outputPath, pdf)
}

// printPage loads the file at path into a hidden window and prints it.
func printPage(ctx context.Context, path string, sheet PdfSheet) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, pdfTimeout)
	defer cancel()
	win := mygo.NewWindow(mygo.WindowOptions{
		Title:  "PDF",
		Hidden: true,
		// The sheet in CSS px, so the page lays out as it was paginated.
		Width:       int(sheet.Width * cssPxPerPt),
		Height:      int(sheet.Height * cssPxPerPt),
		SkipTaskbar: true,
	})
	defer win.Destroy()
	page := win.Page()
	loaded := make(chan error, 1)
	page.OnDidFinishLoad(func() { deliverOnce(loaded, nil) })
	page.OnDidFailLoad(func(e *mygo.LoadError) { deliverOnce(loaded, fmt.Errorf("the page didn't load: %v", e)) })
	if err := page.LoadFile(path); err != nil {
		return nil, err
	}
	select {
	case err := <-loaded:
		if err != nil {
			return nil, err
		}
	case <-ctx.Done():
		return nil, codeError("pdfTimeout")
	}
	if _, err := page.EvalContext(ctx, settleScript(sheet, runtime.GOOS)); err != nil {
		if ctx.Err() != nil {
			return nil, codeError("pdfTimeout")
		}
		return nil, err
	}
	type printed struct {
		pdf []byte
		err error
	}
	done := make(chan printed, 1)
	go func() {
		pdf, err := page.PrintToPDF(pdfOptions(sheet, runtime.GOOS))
		done <- printed{pdf, err}
	}()
	select {
	case r := <-done:
		if r.err != nil {
			return nil, r.err
		}
		if len(r.pdf) == 0 {
			return nil, codeError("pdfFailed")
		}
		return r.pdf, nil
	case <-ctx.Done():
		return nil, codeError("pdfTimeout")
	}
}

// deliverOnce sends v unless one was sent already: a page can report its
// load more than once.
func deliverOnce[T any](ch chan T, v T) {
	select {
	case ch <- v:
	default:
	}
}

func init() {
	command("export_pdf_native", func(c *Call, a struct {
		HTML        string   `json:"html"`
		BaseDir     *string  `json:"baseDir"`
		OutputPath  string   `json:"outputPath"`
		PageNumbers bool     `json:"pageNumbers"`
		Sheet       PdfSheet `json:"sheet"`
	}) (nothing, error) {
		return nil, exportPDF(c.ctx, a.HTML, a.OutputPath, a.PageNumbers, a.Sheet)
	})
}
