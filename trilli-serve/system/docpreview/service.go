package docpreview

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"

	"trilli/system/logging"
	"trilli/system/storage"
)

// previewPrefix is the reserved, hidden segment under a tenant's blob namespace
// where derived preview PDFs live. The leading dot keeps it disjoint from real
// user content (workspace/folder names can't begin with one in our paths) so a
// cache blob can never collide with — or be mistaken for — a user file.
const previewPrefix = ".previews"

// Service renders Office documents to PDF, caching each result so a given
// document version is only ever converted once. The cache is content-addressed
// by (tenant, file, version): editing a file changes its version, which yields
// a fresh key and naturally orphans the stale preview (sweepable later — it's
// pure derived data and never counts against quota).
type Service struct {
	conv  Converter
	cache storage.KeyedStore
}

// NewService wires a converter to a keyed blob cache.
func NewService(conv Converter, cache storage.KeyedStore) *Service {
	return &Service{conv: conv, cache: cache}
}

// PDF returns a readable PDF rendering of an Office document, serving a cached
// copy when present and converting on a miss. openOriginal is invoked ONLY on a
// cache miss, so a hit costs a single blob read and never touches the source.
//
//   - tenantID/fileID/version identify the cache slot. version must change
//     whenever the source bytes do (the file's updated-at works well).
//   - ext is the source extension ("docx") used to pick the import filter.
//
// The caller owns access control: PDF assumes the requester is already
// authorized to read fileID.
func (s *Service) PDF(
	ctx context.Context,
	tenantID, fileID int64,
	version, ext string,
	openOriginal func() (io.ReadCloser, error),
) (io.ReadCloser, error) {
	key := previewKey(tenantID, fileID, version)

	// Fast path: cache hit.
	if rc, err := s.cache.Get(ctx, key); err == nil {
		return rc, nil
	} else if !errors.Is(err, storage.ErrNotFound) {
		return nil, fmt.Errorf("docpreview: cache get: %w", err)
	}

	// Miss: fetch the original, convert, cache, and return the fresh bytes
	// (no second round-trip to the blob store).
	src, err := openOriginal()
	if err != nil {
		return nil, fmt.Errorf("docpreview: open source: %w", err)
	}
	defer src.Close()

	pdf, err := s.conv.ToPDF(ctx, src, ext)
	if err != nil {
		return nil, err // already a docpreview sentinel (ErrConversionFailed / ErrTooLarge)
	}

	// Best-effort cache write — a cache failure shouldn't deny the user their
	// preview, just cost a reconversion next time.
	if _, err := s.cache.PutAt(ctx, key, bytes.NewReader(pdf)); err != nil {
		logging.Error(packageName, "cache write failed (tenant=%d file=%d): %v", tenantID, fileID, err)
	}
	return io.NopCloser(bytes.NewReader(pdf)), nil
}

// previewKey is the deterministic blob key for a file's preview at a given
// version, e.g. "tenants/42/.previews/1007-1717900000.pdf".
func previewKey(tenantID, fileID int64, version string) string {
	return fmt.Sprintf("tenants/%d/%s/%d-%s.pdf", tenantID, previewPrefix, fileID, version)
}
