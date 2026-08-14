package docpreview

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"trilli/system/logging"
)

// Tunables for the in-process LibreOffice engine. These are the guardrails that
// keep a soffice subprocess from running away with the box.
const (
	// defaultMaxConcurrent caps how many soffice processes run at once. Each is
	// memory-hungry; this bounds the blast radius under a burst of previews.
	defaultMaxConcurrent = 2
	// defaultTimeout aborts a single conversion that wedges (corrupt input,
	// a macro prompt, etc.). The subprocess is killed when the ctx expires.
	defaultTimeout = 45 * time.Second
	// defaultMaxInputBytes rejects sources too large to convert interactively.
	defaultMaxInputBytes = 100 << 20 // 100 MiB
)

// ErrTooLarge is returned when the source exceeds the converter's input cap.
var ErrTooLarge = errors.New("docpreview: source document too large to preview")

// ErrConversionFailed wraps any failure of the underlying engine (missing
// binary, non-zero exit, no output produced). Callers surface it as an
// "unavailable" preview rather than a hard error.
var ErrConversionFailed = errors.New("docpreview: conversion failed")

// LibreOfficeConverter renders documents by shelling out to a headless
// LibreOffice (`soffice --headless --convert-to pdf`). It's safe for concurrent
// use: a semaphore bounds parallelism and every conversion gets its own
// throwaway UserInstallation profile so two soffice instances never fight over
// a shared profile lock.
type LibreOfficeConverter struct {
	bin           string
	sem           chan struct{}
	timeout       time.Duration
	maxInputBytes int64
}

// NewLibreOfficeConverter locates the soffice binary and returns a ready
// converter. It errors if no LibreOffice is installed, so wiring can decide to
// run with previews disabled rather than failing every request at runtime.
//
// Concurrency is sized per node via TRILLI_PREVIEW_CONCURRENCY (default 2 —
// matched to a 2-vCPU box, where soffice conversions are CPU-bound and extra
// lanes add latency, not throughput). On a bigger node, raise it to ~the core
// count and conversion throughput scales linearly.
func NewLibreOfficeConverter() (*LibreOfficeConverter, error) {
	bin := findSoffice()
	if bin == "" {
		return nil, fmt.Errorf("%w: soffice/libreoffice not found in PATH", ErrConversionFailed)
	}
	lanes := envInt("TRILLI_PREVIEW_CONCURRENCY", defaultMaxConcurrent, 1, 32)
	logging.Info(packageName, "LibreOffice converter ready (%s, %d concurrent)", bin, lanes)
	return &LibreOfficeConverter{
		bin:           bin,
		sem:           make(chan struct{}, lanes),
		timeout:       defaultTimeout,
		maxInputBytes: defaultMaxInputBytes,
	}, nil
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

// findSoffice returns the first usable LibreOffice executable, or "".
func findSoffice() string {
	for _, name := range []string{"soffice", "libreoffice"} {
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
	}
	// Common absolute fallbacks for minimal installs that don't add to PATH.
	for _, p := range []string{"/usr/bin/soffice", "/usr/bin/libreoffice", "/opt/libreoffice/program/soffice"} {
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return p
		}
	}
	return ""
}

// ToPDF converts src to PDF bytes. It buffers the source to a temp file (soffice
// can't read a pipe), runs the headless conversion into an isolated work dir,
// and reads the single PDF it produces.
func (c *LibreOfficeConverter) ToPDF(ctx context.Context, src io.Reader, ext string) ([]byte, error) {
	return c.Convert(ctx, src, ext, "pdf")
}

// Convert renders src (extension srcExt, e.g. "docx") into targetExt (e.g.
// "doc", "pdf", "xls") and returns the converted bytes. It's the general form
// of ToPDF, used by the Productivity save-as/download flow to export the
// editor's native OOXML document into a legacy Office format when the user picks
// .doc/.xls/.ppt. Same guardrails as ToPDF: bounded concurrency, a private
// soffice profile per run, and a per-conversion timeout. srcExt and targetExt
// are lowercase, no leading dot.
func (c *LibreOfficeConverter) Convert(ctx context.Context, src io.Reader, srcExt, targetExt string) ([]byte, error) {
	if srcExt == "" {
		srcExt = "bin"
	}
	if targetExt == "" {
		targetExt = "bin"
	}

	// Bound concurrency — block (cancellable) until a slot frees.
	select {
	case c.sem <- struct{}{}:
		defer func() { <-c.sem }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	// Per-conversion scratch dir: holds the input, the output, and a private
	// UserInstallation profile. Removed wholesale on return.
	work, err := os.MkdirTemp("", "trilli-loconv-*")
	if err != nil {
		return nil, fmt.Errorf("%w: temp dir: %v", ErrConversionFailed, err)
	}
	defer os.RemoveAll(work)

	inPath := filepath.Join(work, "source."+srcExt)
	if err := c.writeCapped(inPath, src); err != nil {
		return nil, err
	}

	outDir := filepath.Join(work, "out")
	if err := os.Mkdir(outDir, 0o700); err != nil {
		return nil, fmt.Errorf("%w: out dir: %v", ErrConversionFailed, err)
	}

	cctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	// A unique UserInstallation per run avoids the shared-profile lock that
	// otherwise serializes (or deadlocks) concurrent headless conversions.
	profileURL := "file://" + filepath.Join(work, "profile")
	cmd := exec.CommandContext(cctx, c.bin,
		"--headless", "--nologo", "--nofirststartwizard", "--norestore",
		"-env:UserInstallation="+profileURL,
		"--convert-to", targetExt,
		"--outdir", outDir,
		inPath,
	)
	// HOME must point somewhere writable; on locked-down service users the
	// default may not be. Pin it to the scratch dir for good measure.
	cmd.Env = append(os.Environ(), "HOME="+work)

	out, err := cmd.CombinedOutput()
	if cctx.Err() == context.DeadlineExceeded {
		logging.Error(packageName, "conversion timed out after %s (%s->%s)", c.timeout, srcExt, targetExt)
		return nil, fmt.Errorf("%w: timed out", ErrConversionFailed)
	}
	if err != nil {
		logging.Error(packageName, "soffice failed (%s->%s): %v — %s", srcExt, targetExt, err, trim(out))
		return nil, fmt.Errorf("%w: %v", ErrConversionFailed, err)
	}

	res, err := readExt(outDir, targetExt)
	if err != nil {
		logging.Error(packageName, "no .%s produced (%s->%s): %v — %s", targetExt, srcExt, targetExt, err, trim(out))
		return nil, fmt.Errorf("%w: %v", ErrConversionFailed, err)
	}
	logging.Debug(packageName, "converted %s -> %s (%d bytes)", srcExt, targetExt, len(res))
	return res, nil
}

// writeCapped copies src to path, failing with ErrTooLarge if it exceeds the
// input cap. LimitReader is set one past the cap so we can tell "exactly at the
// cap" from "over".
func (c *LibreOfficeConverter) writeCapped(path string, src io.Reader) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("%w: open input: %v", ErrConversionFailed, err)
	}
	n, copyErr := io.Copy(f, io.LimitReader(src, c.maxInputBytes+1))
	closeErr := f.Close()
	if copyErr != nil {
		return fmt.Errorf("%w: buffer input: %v", ErrConversionFailed, copyErr)
	}
	if closeErr != nil {
		return fmt.Errorf("%w: flush input: %v", ErrConversionFailed, closeErr)
	}
	if n > c.maxInputBytes {
		return ErrTooLarge
	}
	return nil
}

// readExt returns the bytes of the single .<ext> file in dir. soffice names the
// output after the input basename with the new extension ("source.doc"), so we
// just take whichever file with the target extension appears.
func readExt(dir, ext string) ([]byte, error) {
	want := "." + strings.TrimPrefix(ext, ".")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if filepath.Ext(e.Name()) == want {
			return os.ReadFile(filepath.Join(dir, e.Name()))
		}
	}
	return nil, fmt.Errorf("no %s in output dir", want)
}

// trim caps captured subprocess output so a chatty failure doesn't flood logs.
func trim(b []byte) string {
	const max = 500
	if len(b) > max {
		b = b[:max]
	}
	return string(b)
}

var _ Converter = (*LibreOfficeConverter)(nil)
