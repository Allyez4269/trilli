// Package pdftools implements Trilli PDF: a native, pure-Go suite of PDF
// utilities built on pdfcpu (Apache-2.0). Every operation runs in-process — no
// external binaries, no subprocess fleet — so it scales as plain goroutines and
// ships in the single Trilli binary. The HTTP layer (handlers.go) reads inputs
// from and writes results back to the tenant's encrypted file space via the
// shared files.Service; this file is the engine only (io.ReadSeeker in, io.Writer
// out) and has no knowledge of HTTP, tenants, or storage.
package pdftools

import (
	"archive/zip"
	"fmt"
	"io"
	"strings"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/types"
)

func init() {
	// Never read or write a config file on the server — use in-memory defaults
	// only. Without this pdfcpu tries to materialize a config under the user's
	// XDG config dir on first use.
	model.ConfigPath = "disable"
}

// conf returns a fresh in-memory configuration. Relaxed validation so the tools
// accept real-world PDFs that aren't 100% spec-compliant (the common case).
func conf() *model.Configuration {
	c := model.NewDefaultConfiguration()
	c.ValidationMode = model.ValidationRelaxed
	return c
}

// Merge concatenates the inputs, in order, into a single PDF.
func Merge(inputs []io.ReadSeeker, w io.Writer) error {
	return api.MergeRaw(inputs, w, false, conf())
}

// Compress rewrites the PDF with redundant resources removed and Flate maxed out
// (pure Go). NB: this is structural/stream compression — image-downsampling for
// scan-heavy PDFs is a Tier-B follow-up using golang.org/x/image.
func Compress(rs io.ReadSeeker, w io.Writer) error {
	return api.Optimize(rs, w, conf())
}

// Protect encrypts with AES-256. ownerPW falls back to userPW when empty.
func Protect(rs io.ReadSeeker, w io.Writer, userPW, ownerPW string) error {
	if ownerPW == "" {
		ownerPW = userPW
	}
	c := model.NewAESConfiguration(userPW, ownerPW, 256)
	c.ValidationMode = model.ValidationRelaxed
	return api.Encrypt(rs, w, c)
}

// Unlock removes encryption from a password-protected PDF.
func Unlock(rs io.ReadSeeker, w io.Writer, password string) error {
	c := model.NewAESConfiguration(password, password, 256)
	c.ValidationMode = model.ValidationRelaxed
	return api.Decrypt(rs, w, c)
}

// watermarkDesc styles a diagonal grey text watermark behind page content.
const watermarkDesc = "points:48, rotation:45, opacity:0.6, fillcolor:0.6 0.6 0.6"

// Watermark stamps grey diagonal text across every page.
func Watermark(rs io.ReadSeeker, w io.Writer, text string) error {
	wm, err := api.TextWatermark(text, watermarkDesc, false /*onTop*/, false, types.POINTS)
	if err != nil {
		return err
	}
	return api.AddWatermarks(rs, w, nil, wm, conf())
}

// pageNumDesc places a small dark number at bottom-centre, on top of content.
const pageNumDesc = "points:10, position:bc, offset:0 10, fillcolor:0.25 0.25 0.25"

// PageNumbers stamps the page number ("%p") at the bottom of every page.
func PageNumbers(rs io.ReadSeeker, w io.Writer) error {
	wm, err := api.TextWatermark("%p", pageNumDesc, true /*onTop*/, false, types.POINTS)
	if err != nil {
		return err
	}
	return api.AddWatermarks(rs, w, nil, wm, conf())
}

// Rotate turns every page clockwise by rotation degrees (90/180/270).
func Rotate(rs io.ReadSeeker, w io.Writer, rotation int) error {
	return api.Rotate(rs, w, rotation, nil, conf())
}

// NUp arranges n source pages onto each output page (2, 3, 4, 6, 8, 9, 12, 16).
func NUp(rs io.ReadSeeker, w io.Writer, n int) error {
	c := conf()
	nup, err := api.PDFNUpConfig(n, "", c)
	if err != nil {
		return err
	}
	return api.NUp(rs, w, nil, nil, nup, c)
}

// Info returns structural + metadata details (page count, dimensions, encryption,
// form/signature presence, etc.) for display — a read-only tool.
func Info(rs io.ReadSeeker) (*pdfcpu.PDFInfo, error) {
	return api.PDFInfo(rs, "", nil, false, conf())
}

// DeletePages removes the selected pages (e.g. "1-3,5,8-"). The rest are kept.
func DeletePages(rs io.ReadSeeker, w io.Writer, pages string) error {
	sel, err := api.ParsePageSelection(pages)
	if err != nil {
		return err
	}
	return api.RemovePages(rs, w, sel, conf())
}

// ExtractPages keeps ONLY the selected pages (in the order given — so it doubles
// as a reorder), e.g. "1,3-4" or "5,1,2".
func ExtractPages(rs io.ReadSeeker, w io.Writer, pages string) error {
	sel, err := api.ParsePageSelection(pages)
	if err != nil {
		return err
	}
	return api.Collect(rs, w, sel, conf())
}

// SplitZip splits the PDF into spans of `span` pages each and writes a ZIP of the
// resulting PDFs. All in memory — no temp files.
func SplitZip(rs io.ReadSeeker, span int, w io.Writer) error {
	if span < 1 {
		span = 1
	}
	spans, err := api.SplitRaw(rs, span, conf())
	if err != nil {
		return err
	}
	zw := zip.NewWriter(w)
	for _, ps := range spans {
		f, err := zw.Create(fmt.Sprintf("pages_%d-%d.pdf", ps.From, ps.Thru))
		if err != nil {
			return err
		}
		if _, err := io.Copy(f, ps.Reader); err != nil {
			return err
		}
	}
	return zw.Close()
}

// ImageToPDF builds a PDF with one image per page from the given image readers
// (JPEG/PNG/etc.).
func ImageToPDF(imgs []io.Reader, w io.Writer) error {
	imp, err := api.Import("", types.POINTS)
	if err != nil {
		return err
	}
	return api.ImportImages(nil, w, imgs, imp, conf())
}

// ExtractImagesZip pulls every embedded raster image out of the PDF and writes a
// ZIP of them. Errors if the PDF contains no images.
func ExtractImagesZip(rs io.ReadSeeker, w io.Writer) error {
	pages, err := api.ExtractImagesRaw(rs, nil, conf())
	if err != nil {
		return err
	}
	zw := zip.NewWriter(w)
	n := 0
	for _, m := range pages {
		for _, img := range m {
			n++
			ext := strings.ToLower(img.FileType)
			if ext == "" {
				ext = "png"
			}
			base := img.Name
			if base == "" {
				base = fmt.Sprintf("image_%d", n)
			}
			f, err := zw.Create(fmt.Sprintf("%s.%s", base, ext))
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, img); err != nil {
				return err
			}
		}
	}
	if n == 0 {
		return fmt.Errorf("no images found in the PDF")
	}
	return zw.Close()
}
