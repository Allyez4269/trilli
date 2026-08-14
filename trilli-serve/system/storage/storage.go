// Package storage defines the Store interface that file backends implement.
// The production backend is Azure Blob Storage (system/storage/azureblob);
// blob bytes live in Azure and Postgres holds the metadata + blob_path pointer.
package storage

import (
	"context"
	"errors"
	"io"
)

// Sentinel errors returned by Store implementations.
var (
	ErrNotFound = errors.New("storage: blob not found")
)

// Access tiers a blob can occupy. Hot/Cool/Cold are all online (instant reads);
// they trade storage cost for retrieval cost. Archive is deliberately omitted —
// it's offline (hours to rehydrate) and our files must stay instantly accessible.
const (
	TierHot  = "hot"
	TierCool = "cool"
	TierCold = "cold"
)

// PutResult is what Store.Put returns when it has successfully written bytes.
type PutResult struct {
	// BlobPath is the opaque key for the stored object — persist this in
	// files.blob_path and pass it back to Get / Delete later.
	BlobPath string
	// Size is the number of bytes actually written.
	Size int64
}

// Store is the persistence interface for file bytes. Implementations enforce
// tenant isolation by encoding tenant_id into BlobPath (so even a leaked path
// can't address another tenant's data).
//
// Put generates its own opaque BlobPath so callers don't have to allocate file
// IDs before uploading.
type Store interface {
	// Put streams bytes into a tenant-scoped blob and returns the path + size.
	// Implementations should fsync or otherwise durably commit before returning.
	Put(ctx context.Context, tenantID int64, r io.Reader) (PutResult, error)

	// Get opens the blob for reading. Caller must Close the returned reader.
	Get(ctx context.Context, blobPath string) (io.ReadCloser, error)

	// Delete removes the blob. Returns nil if it doesn't exist.
	Delete(ctx context.Context, blobPath string) error
}

// KeyedStore is a Store that additionally writes to a caller-chosen,
// deterministic key (rather than the random key Put allocates). It's the
// substrate for derived/cache artifacts — e.g. the Office→PDF preview cache —
// whose key is a pure function of the source (tenant + file + version), so the
// same input always maps to the same blob and can be looked up without a DB
// pointer. Get/Delete from Store already take an explicit path, so PutAt is the
// only addition.
type KeyedStore interface {
	Store

	// PutAt streams bytes to an explicit key, overwriting any existing blob,
	// and returns the number of bytes written. Callers own the key namespace
	// and must keep cache keys disjoint from user-file keys.
	PutAt(ctx context.Context, key string, r io.Reader) (int64, error)
}

// BlockStore is a Store that supports building a blob from independently-staged
// blocks (Azure block-blob semantics), for resumable chunked uploads: each chunk
// is staged under a blockID, a dropped/retried chunk simply re-stages the same
// blockID (idempotent), and the blob is finalized by committing the ordered
// block list. Uncommitted blocks are discarded by the backend on abort.
type BlockStore interface {
	// StageBlock uploads one uncommitted block of blobPath and returns its byte
	// count. Re-staging the same blockID overwrites it (safe to retry).
	StageBlock(ctx context.Context, blobPath, blockID string, r io.Reader) (int64, error)

	// CommitBlocks assembles the final blob from blockIDs, in the given order.
	CommitBlocks(ctx context.Context, blobPath string, blockIDs []string) error

	// AbortBlocks discards any staged-but-uncommitted blocks for blobPath.
	AbortBlocks(ctx context.Context, blobPath string) error
}

// TieredStore is a Store whose blobs can be moved between access tiers
// (TierHot/Cool/Cold). Backends without native tiering (e.g. a local-disk dev
// store) simply don't implement it, and the tiering engine no-ops against them.
type TieredStore interface {
	Store

	// SetTier moves a blob to the given tier ("hot"|"cool"|"cold"). Idempotent;
	// returns ErrNotFound if the blob is gone.
	SetTier(ctx context.Context, blobPath, tier string) error
}
