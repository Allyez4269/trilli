package pdftools

import (
	"archive/zip"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"io"
	"sync"
	"time"

	"github.com/klippa-app/go-pdfium"
	"github.com/klippa-app/go-pdfium/requests"
	"github.com/klippa-app/go-pdfium/webassembly"
)

// PDF→Image rasterization runs PDFium compiled to WebAssembly inside a pure-Go
// runtime (wazero) — no cgo, no external binary, no subprocess. The engine is
// embedded in the binary and runs in-process; a small instance pool caps
// concurrency so renders stay bounded. First use pays a one-time WASM compile.
var (
	poolOnce sync.Once
	pdfPool  pdfium.Pool
	poolErr  error
)

func renderPool() (pdfium.Pool, error) {
	poolOnce.Do(func() {
		pdfPool, poolErr = webassembly.Init(webassembly.Config{MinIdle: 1, MaxIdle: 2, MaxTotal: 4})
	})
	return pdfPool, poolErr
}

// RenderPages rasterizes every page of the PDF to an RGBA image at the given DPI.
// Images are copied out of the WASM-backed buffers so they remain valid after the
// per-page cleanup.
func RenderPages(pdf []byte, dpi int) ([]image.Image, error) {
	if dpi < 36 {
		dpi = 150
	}
	p, err := renderPool()
	if err != nil {
		return nil, fmt.Errorf("pdf renderer init: %w", err)
	}
	inst, err := p.GetInstance(30 * time.Second)
	if err != nil {
		return nil, err
	}
	defer inst.Close()

	doc, err := inst.OpenDocument(&requests.OpenDocument{File: &pdf})
	if err != nil {
		return nil, err
	}
	defer inst.FPDF_CloseDocument(&requests.FPDF_CloseDocument{Document: doc.Document})

	pc, err := inst.FPDF_GetPageCount(&requests.FPDF_GetPageCount{Document: doc.Document})
	if err != nil {
		return nil, err
	}

	imgs := make([]image.Image, 0, pc.PageCount)
	for i := 0; i < pc.PageCount; i++ {
		res, err := inst.RenderPageInDPI(&requests.RenderPageInDPI{
			Page: requests.Page{ByIndex: &requests.PageByIndex{Document: doc.Document, Index: i}},
			DPI:  dpi,
		})
		if err != nil {
			return nil, err
		}
		src := res.Result.Image
		cp := image.NewRGBA(src.Bounds())
		draw.Draw(cp, cp.Bounds(), src, src.Bounds().Min, draw.Src)
		res.Cleanup() // frees the WASM-backed image; we keep our copy
		imgs = append(imgs, cp)
	}
	return imgs, nil
}

// PDFToImagesZip renders every page to a PNG and writes a ZIP of them.
func PDFToImagesZip(rs io.ReadSeeker, dpi int, w io.Writer) error {
	data, err := io.ReadAll(rs)
	if err != nil {
		return err
	}
	imgs, err := RenderPages(data, dpi)
	if err != nil {
		return err
	}
	zw := zip.NewWriter(w)
	for i, img := range imgs {
		f, err := zw.Create(fmt.Sprintf("page_%02d.png", i+1))
		if err != nil {
			return err
		}
		if err := png.Encode(f, img); err != nil {
			return err
		}
	}
	return zw.Close()
}
