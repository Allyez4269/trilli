package pdftools

import (
	"archive/zip"
	"bytes"
	"image"
	"image/color"
	"image/png"
	"io"
	"strings"
	"testing"

	"github.com/pdfcpu/pdfcpu/pkg/api"
)

func samplePNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 120, 90))
	for y := 0; y < 90; y++ {
		for x := 0; x < 120; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x * 2), G: 80, B: uint8(y * 2), A: 255})
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return b.Bytes()
}

// sampleJSON describes a 4-page PDF using only the Helvetica corefont (no font
// files needed), so the smoke test is self-contained.
const sampleJSON = `{
  "paper": "A4P",
  "fonts": {"h": {"name": "Helvetica", "size": 20}},
  "pages": {
    "1": {"content": {"text": [{"value": "Trilli PDF - page 1", "pos": [50, 700], "font": {"name": "$h"}}]}},
    "2": {"content": {"text": [{"value": "Trilli PDF - page 2", "pos": [50, 700], "font": {"name": "$h"}}]}},
    "3": {"content": {"text": [{"value": "Trilli PDF - page 3", "pos": [50, 700], "font": {"name": "$h"}}]}},
    "4": {"content": {"text": [{"value": "Trilli PDF - page 4", "pos": [50, 700], "font": {"name": "$h"}}]}}
  }
}`

func samplePDF(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := api.Create(nil, strings.NewReader(sampleJSON), &buf, conf()); err != nil {
		t.Fatalf("create sample pdf: %v", err)
	}
	return buf.Bytes()
}

// TestOps exercises every engine op against a real generated PDF, asserting no
// error and non-empty output. This is what catches malformed watermark/stamp
// descriptors and bad pdfcpu config, which the compiler can't see.
func TestOps(t *testing.T) {
	src := samplePDF(t)
	rs := func() *bytes.Reader { return bytes.NewReader(src) }
	nonEmpty := func(t *testing.T, name string, b *bytes.Buffer, err error) {
		if err != nil {
			t.Errorf("%s: %v", name, err)
			return
		}
		if b.Len() == 0 {
			t.Errorf("%s: produced empty output", name)
		}
	}

	var out bytes.Buffer

	out.Reset()
	nonEmpty(t, "Compress", &out, Compress(rs(), &out))

	out.Reset()
	nonEmpty(t, "Watermark", &out, Watermark(rs(), &out, "CONFIDENTIAL"))

	out.Reset()
	nonEmpty(t, "PageNumbers", &out, PageNumbers(rs(), &out))

	out.Reset()
	nonEmpty(t, "Rotate", &out, Rotate(rs(), &out, 90))

	out.Reset()
	nonEmpty(t, "NUp", &out, NUp(rs(), &out, 4))

	out.Reset()
	nonEmpty(t, "Merge", &out, Merge([]io.ReadSeeker{rs(), rs()}, &out))

	// Protect → Unlock round-trip.
	var enc bytes.Buffer
	if err := Protect(rs(), &enc, "secret", ""); err != nil {
		t.Fatalf("Protect: %v", err)
	}
	var dec bytes.Buffer
	nonEmpty(t, "Unlock", &dec, Unlock(bytes.NewReader(enc.Bytes()), &dec, "secret"))

	info, err := Info(rs())
	if err != nil {
		t.Errorf("Info: %v", err)
	} else if info.PageCount != 4 {
		t.Errorf("Info: PageCount=%d, want 4", info.PageCount)
	}

	// page ops
	out.Reset()
	nonEmpty(t, "DeletePages", &out, DeletePages(rs(), &out, "1"))

	out.Reset()
	nonEmpty(t, "ExtractPages", &out, ExtractPages(rs(), &out, "2-3"))

	// split a 4-page PDF into 4 single-page PDFs → a 4-entry zip
	out.Reset()
	if err := SplitZip(rs(), 1, &out); err != nil {
		t.Errorf("SplitZip: %v", err)
	} else if zr, err := zip.NewReader(bytes.NewReader(out.Bytes()), int64(out.Len())); err != nil {
		t.Errorf("SplitZip: zip unreadable: %v", err)
	} else if len(zr.File) != 4 {
		t.Errorf("SplitZip: %d parts, want 4", len(zr.File))
	}

	// image → 1-page PDF
	out.Reset()
	if err := ImageToPDF([]io.Reader{bytes.NewReader(samplePNG(t))}, &out); err != nil {
		t.Errorf("ImageToPDF: %v", err)
	} else if info, err := Info(bytes.NewReader(out.Bytes())); err != nil {
		t.Errorf("ImageToPDF: result not a valid PDF: %v", err)
	} else if info.PageCount != 1 {
		t.Errorf("ImageToPDF: PageCount=%d, want 1", info.PageCount)
	}
}
