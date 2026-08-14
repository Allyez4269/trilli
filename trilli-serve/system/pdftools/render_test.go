package pdftools

import (
	"archive/zip"
	"bytes"
	"testing"
)

// TestRenderPages rasterizes the 4-page sample PDF via the embedded-WASM PDFium
// engine — proves no-cgo rendering works end to end.
func TestRenderPages(t *testing.T) {
	src := samplePDF(t)

	imgs, err := RenderPages(src, 100)
	if err != nil {
		t.Fatalf("RenderPages: %v", err)
	}
	if len(imgs) != 4 {
		t.Fatalf("rendered %d pages, want 4", len(imgs))
	}
	for i, im := range imgs {
		if b := im.Bounds(); b.Dx() <= 0 || b.Dy() <= 0 {
			t.Errorf("page %d: empty image %v", i+1, b)
		}
	}

	var buf bytes.Buffer
	if err := PDFToImagesZip(bytes.NewReader(src), 100, &buf); err != nil {
		t.Fatalf("PDFToImagesZip: %v", err)
	}
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("zip unreadable: %v", err)
	}
	if len(zr.File) != 4 {
		t.Errorf("zip has %d images, want 4", len(zr.File))
	}
}
