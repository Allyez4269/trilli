// Package azureblob is the production storage backend, talking to Azure Blob
// Storage via the official azure-sdk-for-go. Conforms to storage.Store so it's
// a drop-in replacement for the local backend.
package azureblob

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/streaming"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/blob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/bloberror"

	"trilli/system/logging"
	"trilli/system/storage"
)

const packageName = "storage/azureblob"

// Credentials come from the environment: AZURE_STORAGE_ACCOUNT and
// AZURE_STORAGE_KEY select the storage account; AZURE_STORAGE_CONTAINER
// overrides the container name (default "trilli-files").
const defaultContainerName = "trilli-files"

// Upload tuning for block-blob staging. Azure caps a block blob at 50,000
// blocks, so the largest single file ≈ blockUploadSize × 50,000. At 32 MiB that
// is ~1.5 TiB — comfortably into "really large file" territory. The default
// (nil options) uses ~4 MiB blocks, which silently caps blobs at ~200 GiB.
// Raising blockUploadSize lifts the ceiling (e.g. 100 MiB → ~4.7 TiB) but costs
// more buffered memory per upload, since UploadStream holds up to
// blockUploadSize × uploadConcurrency bytes in flight at once.
const (
	blockUploadSize   = 32 * 1024 * 1024 // 32 MiB per staged block
	uploadConcurrency = 4                // parallel block PUTs per file (≈128 MiB buffered/upload)
)

// uploadStreamOpts is shared by every UploadStream call (the SDK treats it as
// read-only) so large uploads aren't capped by the default ~4 MiB block size.
var uploadStreamOpts = &azblob.UploadStreamOptions{
	BlockSize:   blockUploadSize,
	Concurrency: uploadConcurrency,
}

// Config holds the Azure Blob connection parameters.
type Config struct {
	AccountName   string
	AccountKey    string
	ContainerName string
}

// DefaultConfig returns a Config built from the AZURE_STORAGE_* environment.
func DefaultConfig() Config {
	container := os.Getenv("AZURE_STORAGE_CONTAINER")
	if container == "" {
		container = defaultContainerName
	}
	return Config{
		AccountName:   os.Getenv("AZURE_STORAGE_ACCOUNT"),
		AccountKey:    os.Getenv("AZURE_STORAGE_KEY"),
		ContainerName: container,
	}
}

// Store implements storage.Store backed by Azure Blob.
type Store struct {
	cfg    Config
	client *azblob.Client
}

// New constructs an Azure Blob store and ensures the target container exists.
func New(cfg Config) (*Store, error) {
	if cfg.AccountName == "" {
		cfg = DefaultConfig()
	}

	cred, err := azblob.NewSharedKeyCredential(cfg.AccountName, cfg.AccountKey)
	if err != nil {
		return nil, fmt.Errorf("azureblob: shared key credential: %w", err)
	}

	serviceURL := fmt.Sprintf("https://%s.blob.core.windows.net/", cfg.AccountName)
	client, err := azblob.NewClientWithSharedKeyCredential(serviceURL, cred, nil)
	if err != nil {
		return nil, fmt.Errorf("azureblob: client init: %w", err)
	}

	s := &Store{cfg: cfg, client: client}
	if err := s.ensureContainer(context.Background()); err != nil {
		return nil, err
	}
	logging.Info(packageName, "Azure Blob store ready at https://%s.blob.core.windows.net/%s",
		cfg.AccountName, cfg.ContainerName)
	return s, nil
}

// ensureContainer creates the configured container if it doesn't already exist.
func (s *Store) ensureContainer(ctx context.Context) error {
	_, err := s.client.CreateContainer(ctx, s.cfg.ContainerName, nil)
	if err == nil {
		logging.Info(packageName, "Created container %s", s.cfg.ContainerName)
		return nil
	}
	if bloberror.HasCode(err, bloberror.ContainerAlreadyExists) {
		return nil
	}
	return fmt.Errorf("azureblob: ensure container %q: %w", s.cfg.ContainerName, err)
}

// Put streams the reader to a tenant-scoped blob and returns its path + size.
func (s *Store) Put(ctx context.Context, tenantID int64, r io.Reader) (storage.PutResult, error) {
	key, err := randomKey()
	if err != nil {
		return storage.PutResult{}, err
	}
	blobName := fmt.Sprintf("tenants/%d/%s", tenantID, key)

	counter := &countingReader{r: r}
	if _, err := s.client.UploadStream(ctx, s.cfg.ContainerName, blobName, counter, uploadStreamOpts); err != nil {
		// A mid-stream failure (client disconnect, idle timeout) leaves the blocks
		// staged so far UNCOMMITTED — invisible, but occupying storage until Azure's
		// ~7-day GC. Discard them now so a failed large upload doesn't leave a
		// ~partial-size orphan behind.
		s.discardPartial(blobName)
		return storage.PutResult{}, fmt.Errorf("azureblob: upload %s: %w", blobName, err)
	}

	logging.Debug(packageName, "Uploaded %d bytes to %s", counter.n, blobName)
	return storage.PutResult{BlobPath: blobName, Size: counter.n}, nil
}

// discardPartial best-effort clears any staged-but-uncommitted blocks left by a
// failed UploadStream. Committing an EMPTY block list discards uncommitted blocks
// and leaves a 0-byte blob, which we then delete — so nothing lingers until the
// ~7-day uncommitted-block GC. Uses a fresh context because the request context
// that failed the upload is already cancelled. Errors are non-fatal (Azure's GC
// remains the backstop).
func (s *Store) discardPartial(blobName string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := s.AbortBlocks(ctx, blobName); err != nil {
		logging.Debug(packageName, "discardPartial %s: %v", blobName, err)
	}
}

// ----- Block staging (resumable chunked uploads) — storage.BlockStore ---------

// StageBlock uploads one uncommitted block of blobPath. Re-staging the same
// blockID overwrites it, so a retried chunk is safe. The chunk is buffered
// (bounded by the caller's chunk size) since the SDK needs a seekable body.
func (s *Store) StageBlock(ctx context.Context, blobPath, blockID string, r io.Reader) (int64, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return 0, fmt.Errorf("azureblob: read block %s: %w", blobPath, err)
	}
	bbc := s.client.ServiceClient().NewContainerClient(s.cfg.ContainerName).NewBlockBlobClient(blobPath)
	if _, err := bbc.StageBlock(ctx, blockID, streaming.NopCloser(bytes.NewReader(data)), nil); err != nil {
		return 0, fmt.Errorf("azureblob: stage block %s/%s: %w", blobPath, blockID, err)
	}
	return int64(len(data)), nil
}

// CommitBlocks assembles the final blob from blockIDs in order.
func (s *Store) CommitBlocks(ctx context.Context, blobPath string, blockIDs []string) error {
	bbc := s.client.ServiceClient().NewContainerClient(s.cfg.ContainerName).NewBlockBlobClient(blobPath)
	if _, err := bbc.CommitBlockList(ctx, blockIDs, nil); err != nil {
		return fmt.Errorf("azureblob: commit blocks %s: %w", blobPath, err)
	}
	return nil
}

// AbortBlocks discards staged-but-uncommitted blocks: committing an empty block
// list drops them (leaving a 0-byte blob) which we then delete.
func (s *Store) AbortBlocks(ctx context.Context, blobPath string) error {
	bbc := s.client.ServiceClient().NewContainerClient(s.cfg.ContainerName).NewBlockBlobClient(blobPath)
	if _, err := bbc.CommitBlockList(ctx, []string{}, nil); err != nil {
		return err
	}
	_, err := bbc.Delete(ctx, nil)
	return err
}

// PutAt streams the reader to an explicit, caller-chosen key (overwriting any
// existing blob) and returns the bytes written. Used for derived artifacts like
// the preview cache, whose key is deterministic — see storage.KeyedStore.
func (s *Store) PutAt(ctx context.Context, key string, r io.Reader) (int64, error) {
	counter := &countingReader{r: r}
	if _, err := s.client.UploadStream(ctx, s.cfg.ContainerName, key, counter, uploadStreamOpts); err != nil {
		s.discardPartial(key) // clear staged-but-uncommitted blocks on failure
		return 0, fmt.Errorf("azureblob: upload %s: %w", key, err)
	}
	logging.Debug(packageName, "Uploaded %d bytes to %s", counter.n, key)
	return counter.n, nil
}

// Get opens the blob for reading. Caller must Close.
func (s *Store) Get(ctx context.Context, blobPath string) (io.ReadCloser, error) {
	resp, err := s.client.DownloadStream(ctx, s.cfg.ContainerName, blobPath, nil)
	if err != nil {
		if bloberror.HasCode(err, bloberror.BlobNotFound) {
			return nil, storage.ErrNotFound
		}
		return nil, fmt.Errorf("azureblob: download %s: %w", blobPath, err)
	}
	return resp.Body, nil
}

// Delete removes the blob. Returns nil if it didn't exist.
func (s *Store) Delete(ctx context.Context, blobPath string) error {
	_, err := s.client.DeleteBlob(ctx, s.cfg.ContainerName, blobPath, nil)
	if err == nil {
		logging.Debug(packageName, "Deleted %s", blobPath)
		return nil
	}
	if bloberror.HasCode(err, bloberror.BlobNotFound) {
		return nil
	}
	return fmt.Errorf("azureblob: delete %s: %w", blobPath, err)
}

// SetTier moves a blob between access tiers (hot/cool/cold). Hot/Cool/Cold are
// all online, so this never affects availability — only cost. Idempotent:
// setting the tier a blob is already in is a no-op on Azure's side.
func (s *Store) SetTier(ctx context.Context, blobPath, tier string) error {
	// AccessTier is a string type and SetTier transmits it as the
	// x-ms-access-tier header, so casting the Azure casing works regardless of
	// whether the pinned SDK version exposes a named "Cold" constant.
	var t blob.AccessTier
	switch tier {
	case storage.TierHot:
		t = blob.AccessTier("Hot")
	case storage.TierCool:
		t = blob.AccessTier("Cool")
	case storage.TierCold:
		t = blob.AccessTier("Cold")
	default:
		return fmt.Errorf("azureblob: unknown tier %q", tier)
	}
	blobClient := s.client.ServiceClient().NewContainerClient(s.cfg.ContainerName).NewBlobClient(blobPath)
	if _, err := blobClient.SetTier(ctx, t, nil); err != nil {
		if bloberror.HasCode(err, bloberror.BlobNotFound) {
			return storage.ErrNotFound
		}
		return fmt.Errorf("azureblob: set tier %s on %s: %w", tier, blobPath, err)
	}
	logging.Debug(packageName, "SetTier %s -> %s", blobPath, tier)
	return nil
}

// Ensure compile-time interface conformance.
var _ storage.Store = (*Store)(nil)
var _ storage.KeyedStore = (*Store)(nil)
var _ storage.TieredStore = (*Store)(nil)

// countingReader wraps an io.Reader and tracks the bytes that have flowed
// through it. UploadStream calls Read until EOF; n is the actual byte total.
type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

func randomKey() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("azureblob: random key: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

// errBoundary documents that ErrNotConfigured is no longer returned now that
// the package has real credentials. Kept exported for downstream code that
// referenced it during the stub phase.
var ErrNotConfigured = errors.New("azureblob: configuration missing")
