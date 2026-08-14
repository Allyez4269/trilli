// Package thumbs renders small JPEG thumbnails of stored files for the list
// views — real previews for images, PDFs, and videos; everything else keeps
// its type icon. It mirrors the docpreview pattern: each thumbnail is a cached
// derivative, content-addressed by (tenant, file, version), generated once and
// served from the blob cache thereafter.
//
// Generators:
//   - images  — pure Go decode (jpeg/png/gif + webp/bmp/tiff via x/image),
//     downscaled with a proper resampling kernel.
//   - PDFs    — first page via pdftoppm (poppler-utils).
//   - videos  — a representative frame via ffmpeg.
//
// External tools are detected at startup; when one is missing its file class
// simply reports ErrUnsupported and the UI falls back to icons. Generation is
// capped (source size, pixel count, wall time, concurrency) so a hostile or
// enormous file can never wedge a node.
package thumbs

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/image/bmp"
	"golang.org/x/image/draw"
	"golang.org/x/image/tiff"
	"golang.org/x/image/webp"

	"trilli/system/logging"
	"trilli/system/storage"
)

const packageName = "thumbs"

// thumbPrefix is the reserved, hidden segment under a tenant's blob namespace
// for cached thumbnails — same convention as docpreview's ".previews": the
// leading dot keeps it disjoint from user content, and it's derived data that
// never counts against quota.
const thumbPrefix = ".thumbs"

// maxEdge is the longest side of a generated thumbnail, sized for crisp
// rendering of the ~36px list-row tile on a 2-3x display with room to spare
// for a hover/preview card later.
const maxEdge = 256

const (
	maxImageSourceBytes = 64 << 20  // decode cap for raster images
	maxDocSourceBytes   = 64 << 20  // pdftoppm input cap
	maxVideoSourceBytes = 512 << 20 // ffmpeg input cap (frame grab needs the file on disk)
	maxSourcePixels     = 12000 * 12000
	genTimeout          = 20 * time.Second
)

// ErrUnsupported means this file class never gets a thumbnail (or its
// external tool is unavailable) — callers should fall back to the type icon.
var ErrUnsupported = errors.New("thumbs: no thumbnail for this file type")

// ErrTooLarge means the source exceeds the generation caps.
var ErrTooLarge = errors.New("thumbs: source too large to thumbnail")

// ErrGenerationFailed wraps tool/decode failures (corrupt files etc.).
var ErrGenerationFailed = errors.New("thumbs: generation failed")

type kind int

const (
	kindNone kind = iota
	kindImage
	kindPDF
	kindVideo
)

var imageExts = map[string]bool{
	"jpg": true, "jpeg": true, "png": true, "gif": true,
	"webp": true, "bmp": true, "tif": true, "tiff": true,
}

var videoExts = map[string]bool{
	"mp4": true, "mov": true, "m4v": true, "webm": true,
	"mkv": true, "avi": true, "mpg": true, "mpeg": true,
}

// extOf returns the lowercase extension without the dot.
func extOf(name string) string {
	return strings.ToLower(strings.TrimPrefix(filepath.Ext(name), "."))
}

func classify(name string) kind {
	switch ext := extOf(name); {
	case imageExts[ext]:
		return kindImage
	case ext == "pdf":
		return kindPDF
	case videoExts[ext]:
		return kindVideo
	default:
		return kindNone
	}
}

// Service generates and caches thumbnails. Concurrency is bounded by a small
// semaphore — generation is bursty (a folder of 50 photos renders its rows at
// once) and each job can hold an image decode or an external process.
type Service struct {
	cache    storage.KeyedStore
	sem      chan struct{}
	pdftoppm string // resolved binary path, "" = PDF thumbs disabled
	ffmpeg   string // resolved binary path, "" = video thumbs disabled
}

// NewService wires the thumbnail generator to a keyed blob cache and probes
// for the optional external tools. Concurrency is sized per node via
// TRILLI_THUMB_CONCURRENCY (default 4 — thumbnail jobs are short, so a few
// lanes keep folder-of-photos bursts snappy without starving the CPU).
func NewService(cache storage.KeyedStore) *Service {
	lanes := envInt("TRILLI_THUMB_CONCURRENCY", 4, 1, 64)
	s := &Service{cache: cache, sem: make(chan struct{}, lanes)}
	logging.Info(packageName, "thumbnail engine ready (%d concurrent)", lanes)
	if p, err := exec.LookPath("pdftoppm"); err == nil {
		s.pdftoppm = p
	} else {
		logging.Info(packageName, "pdftoppm not found — PDF thumbnails disabled")
	}
	if p, err := exec.LookPath("ffmpeg"); err == nil {
		s.ffmpeg = p
	} else {
		logging.Info(packageName, "ffmpeg not found — video thumbnails disabled")
	}
	return s
}

// Supported reports whether this filename can get a thumbnail at all — the
// cheap pre-check handlers use before doing any work.
func (s *Service) Supported(name string) bool {
	switch classify(name) {
	case kindImage:
		return true
	case kindPDF:
		return s.pdftoppm != ""
	case kindVideo:
		return s.ffmpeg != ""
	default:
		return false
	}
}

// Thumb returns a JPEG thumbnail for the file, serving the cached copy when
// present and generating on a miss. openOriginal is invoked only on a miss.
// version must change whenever the source bytes do (file updated-at works).
// The caller owns access control.
func (s *Service) Thumb(
	ctx context.Context,
	tenantID, fileID int64,
	version, name string,
	sizeBytes int64,
	openOriginal func() (io.ReadCloser, error),
) (io.ReadCloser, error) {
	k := classify(name)
	if !s.Supported(name) {
		return nil, ErrUnsupported
	}
	switch k {
	case kindImage:
		if sizeBytes > maxImageSourceBytes {
			return nil, ErrTooLarge
		}
	case kindPDF:
		if sizeBytes > maxDocSourceBytes {
			return nil, ErrTooLarge
		}
	case kindVideo:
		if sizeBytes > maxVideoSourceBytes {
			return nil, ErrTooLarge
		}
	}

	key := thumbKey(tenantID, fileID, version)
	if rc, err := s.cache.Get(ctx, key); err == nil {
		return rc, nil
	} else if !errors.Is(err, storage.ErrNotFound) {
		return nil, fmt.Errorf("thumbs: cache get: %w", err)
	}

	// Bound concurrent generation; a queue burst waits its turn.
	select {
	case s.sem <- struct{}{}:
		defer func() { <-s.sem }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	// Re-check the cache after acquiring the slot — a concurrent request for
	// the same file may have just generated it.
	if rc, err := s.cache.Get(ctx, key); err == nil {
		return rc, nil
	}

	gctx, cancel := context.WithTimeout(ctx, genTimeout)
	defer cancel()

	src, err := openOriginal()
	if err != nil {
		return nil, fmt.Errorf("thumbs: open source: %w", err)
	}
	defer src.Close()

	var jpg []byte
	switch k {
	case kindImage:
		jpg, err = s.fromImage(src, extOf(name))
	case kindPDF:
		jpg, err = s.fromPDF(gctx, src)
	case kindVideo:
		jpg, err = s.fromVideo(gctx, src, extOf(name))
	}
	if err != nil {
		return nil, err
	}

	// Best-effort cache write — a failure just costs a regeneration.
	if _, err := s.cache.PutAt(ctx, key, bytes.NewReader(jpg)); err != nil {
		logging.Error(packageName, "cache write failed (tenant=%d file=%d): %v", tenantID, fileID, err)
	}
	return io.NopCloser(bytes.NewReader(jpg)), nil
}

func thumbKey(tenantID, fileID int64, version string) string {
	return fmt.Sprintf("tenants/%d/%s/%d-%s.jpg", tenantID, thumbPrefix, fileID, version)
}

// ----- generators -----------------------------------------------------------

// fromImage decodes a raster image in-process and downscales it.
func (s *Service) fromImage(src io.Reader, ext string) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(src, maxImageSourceBytes+1))
	if err != nil {
		return nil, fmt.Errorf("%w: read: %v", ErrGenerationFailed, err)
	}
	if int64(len(data)) > maxImageSourceBytes {
		return nil, ErrTooLarge
	}

	// Reject decompression bombs before allocating pixels.
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err == nil && cfg.Width*cfg.Height > maxSourcePixels {
		return nil, ErrTooLarge
	}

	img, err := decodeImage(data, ext)
	if err != nil {
		return nil, fmt.Errorf("%w: decode: %v", ErrGenerationFailed, err)
	}
	return encodeThumb(img)
}

// decodeImage picks the decoder by extension first (cheap, reliable for our
// allow-listed set), falling back to sniffing via image.Decode.
func decodeImage(data []byte, ext string) (image.Image, error) {
	r := bytes.NewReader(data)
	switch ext {
	case "jpg", "jpeg":
		return jpeg.Decode(r)
	case "png":
		return png.Decode(r)
	case "gif":
		return gif.Decode(r) // first frame
	case "webp":
		return webp.Decode(r)
	case "bmp":
		return bmp.Decode(r)
	case "tif", "tiff":
		return tiff.Decode(r)
	}
	img, _, err := image.Decode(r)
	return img, err
}

// encodeThumb downscales to fit maxEdge and encodes a quality-80 JPEG.
// CatmullRom gives clean results on photos and screenshots alike.
func encodeThumb(img image.Image) ([]byte, error) {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= 0 || h <= 0 {
		return nil, fmt.Errorf("%w: empty image", ErrGenerationFailed)
	}
	if w > maxEdge || h > maxEdge {
		scale := float64(maxEdge) / float64(max(w, h))
		nw, nh := max(1, int(float64(w)*scale)), max(1, int(float64(h)*scale))
		dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
		draw.CatmullRom.Scale(dst, dst.Bounds(), img, b, draw.Over, nil)
		img = dst
	}
	var out bytes.Buffer
	if err := jpeg.Encode(&out, img, &jpeg.Options{Quality: 80}); err != nil {
		return nil, fmt.Errorf("%w: encode: %v", ErrGenerationFailed, err)
	}
	return out.Bytes(), nil
}

// fromPDF renders page 1 via pdftoppm. The tool wants a file path, so the
// source is spooled to a capped temp file first.
func (s *Service) fromPDF(ctx context.Context, src io.Reader) ([]byte, error) {
	tmp, cleanup, err := spool(src, maxDocSourceBytes, "thumb-*.pdf")
	if err != nil {
		return nil, err
	}
	defer cleanup()

	// -singlefile writes exactly <prefix>.jpg; -scale-to bounds the long edge.
	prefix := strings.TrimSuffix(tmp, ".pdf")
	cmd := exec.CommandContext(ctx, s.pdftoppm,
		"-jpeg", "-f", "1", "-l", "1", "-singlefile",
		"-scale-to", fmt.Sprint(maxEdge), tmp, prefix)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("%w: pdftoppm: %v (%s)", ErrGenerationFailed, err, trim(out))
	}
	jpg, err := os.ReadFile(prefix + ".jpg")
	if err != nil {
		return nil, fmt.Errorf("%w: pdftoppm output: %v", ErrGenerationFailed, err)
	}
	os.Remove(prefix + ".jpg")
	return jpg, nil
}

// fromVideo grabs a representative frame via ffmpeg. Seeking needs a real
// file (the container index may live at the end), so spool first.
func (s *Service) fromVideo(ctx context.Context, src io.Reader, ext string) ([]byte, error) {
	tmp, cleanup, err := spool(src, maxVideoSourceBytes, "thumb-*."+ext)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	out := strings.TrimSuffix(tmp, "."+ext) + ".jpg"
	defer os.Remove(out)

	// Seek 1s in (skips black lead-ins; clamps to start on shorter clips),
	// take one frame, fit inside maxEdge² preserving aspect.
	vf := fmt.Sprintf("scale='min(%d,iw)':'min(%d,ih)':force_original_aspect_ratio=decrease", maxEdge, maxEdge)
	cmd := exec.CommandContext(ctx, s.ffmpeg,
		"-hide_banner", "-loglevel", "error", "-noaccurate_seek",
		"-ss", "1", "-i", tmp, "-frames:v", "1", "-vf", vf, "-q:v", "4", "-y", out)
	if cmbOut, err := cmd.CombinedOutput(); err != nil {
		// Very short clips can have nothing at t=1s — retry from the start.
		cmd = exec.CommandContext(ctx, s.ffmpeg,
			"-hide_banner", "-loglevel", "error",
			"-i", tmp, "-frames:v", "1", "-vf", vf, "-q:v", "4", "-y", out)
		if cmbOut2, err2 := cmd.CombinedOutput(); err2 != nil {
			return nil, fmt.Errorf("%w: ffmpeg: %v (%s | %s)", ErrGenerationFailed, err2, trim(cmbOut), trim(cmbOut2))
		}
	}
	jpg, err := os.ReadFile(out)
	if err != nil {
		return nil, fmt.Errorf("%w: ffmpeg output: %v", ErrGenerationFailed, err)
	}
	return jpg, nil
}

// envInt reads an integer env var, clamped to [min, max]; def when unset/bad.
func envInt(key string, def, min, max int) int {
	raw := os.Getenv(key)
	if raw == "" {
		return def
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		logging.Error(packageName, "%s=%q is not a number — using default %d", key, raw, def)
		return def
	}
	if n < min {
		return min
	}
	if n > max {
		return max
	}
	return n
}

// spool copies src to a temp file (capped), returning its path + cleanup.
func spool(src io.Reader, limit int64, pattern string) (string, func(), error) {
	f, err := os.CreateTemp("", pattern)
	if err != nil {
		return "", nil, fmt.Errorf("thumbs: temp: %w", err)
	}
	n, err := io.Copy(f, io.LimitReader(src, limit+1))
	cerr := f.Close()
	cleanup := func() { os.Remove(f.Name()) }
	if err != nil || cerr != nil {
		cleanup()
		return "", nil, fmt.Errorf("%w: spool: %v", ErrGenerationFailed, errors.Join(err, cerr))
	}
	if n > limit {
		cleanup()
		return "", nil, ErrTooLarge
	}
	return f.Name(), cleanup, nil
}

func trim(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	return s
}
