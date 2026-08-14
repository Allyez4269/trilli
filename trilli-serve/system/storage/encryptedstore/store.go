package encryptedstore

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"

	"trilli/system/database/postgres"
	"trilli/system/logging"
	"trilli/system/storage"
)

const packageName = "encryptedstore"

// backend is what we decorate: a store that supports random-key writes (PutAt,
// for the preview cache and re-encryption overwrites) and tiering (SetTier).
// The Azure store satisfies it.
type backend interface {
	storage.KeyedStore
	SetTier(ctx context.Context, blobPath, tier string) error
}

// Store decorates a TieredStore so blob content is encrypted per tenant on the
// way out and decrypted on the way back — transparently to the rest of the app,
// which keeps using the storage.Store interface. Reads auto-detect legacy
// plaintext blobs (written before encryption was enabled) by the absence of the
// TLE1 header and pass them through untouched, so deployment is zero-downtime.
type Store struct {
	inner backend
	keys  *KeyService
}

// New wraps inner with transparent per-tenant encryption.
func New(inner backend, keys *KeyService) *Store {
	return &Store{inner: inner, keys: keys}
}

// Put encrypts the stream with the tenant's DEK, then stores it. The returned
// Size is the PLAINTEXT byte count (what the user uploaded / what quota meters),
// not the slightly larger ciphertext.
func (s *Store) Put(ctx context.Context, tenantID int64, r io.Reader) (storage.PutResult, error) {
	dek, err := s.keys.DEK(ctx, tenantID)
	if err != nil {
		return storage.PutResult{}, err
	}
	enc, err := EncryptReader(r, dek)
	if err != nil {
		return storage.PutResult{}, err
	}
	res, err := s.inner.Put(ctx, tenantID, enc)
	if err != nil {
		return storage.PutResult{}, err
	}
	res.Size = enc.PlaintextBytes()
	return res, nil
}

// PutAt encrypts to an explicit key (derived artifacts like the preview cache).
// The tenant is parsed from the key's tenants/{id}/ prefix.
func (s *Store) PutAt(ctx context.Context, key string, r io.Reader) (int64, error) {
	tenantID, ok := tenantFromPath(key)
	if !ok {
		return 0, fmt.Errorf("encryptedstore: cannot derive tenant from key %q", key)
	}
	dek, err := s.keys.DEK(ctx, tenantID)
	if err != nil {
		return 0, err
	}
	enc, err := EncryptReader(r, dek)
	if err != nil {
		return 0, err
	}
	if _, err := s.inner.PutAt(ctx, key, enc); err != nil {
		return 0, err
	}
	return enc.PlaintextBytes(), nil
}

// Get opens the blob and decrypts it if it's TLE1-encrypted; legacy plaintext
// blobs stream through unchanged.
func (s *Store) Get(ctx context.Context, blobPath string) (io.ReadCloser, error) {
	rc, err := s.inner.Get(ctx, blobPath)
	if err != nil {
		return nil, err
	}
	hdr := make([]byte, headerLen)
	n, _ := io.ReadFull(rc, hdr)
	if n == headerLen && HasMagic(hdr) {
		tenantID, ok := tenantFromPath(blobPath)
		if !ok {
			rc.Close()
			return nil, fmt.Errorf("encryptedstore: cannot derive tenant from path %q", blobPath)
		}
		dek, err := s.keys.DEK(ctx, tenantID)
		if err != nil {
			rc.Close()
			return nil, err
		}
		dec, err := DecryptReader(rc, dek, hdr)
		if err != nil {
			rc.Close()
			return nil, err
		}
		return &readCloser{r: dec, c: rc}, nil
	}
	// Legacy plaintext (or a blob shorter than the header): re-prepend whatever
	// we read and stream the rest verbatim.
	return &readCloser{r: io.MultiReader(bytes.NewReader(hdr[:n]), rc), c: rc}, nil
}

// Delete and SetTier operate on the blob path only — no content involved.
func (s *Store) Delete(ctx context.Context, blobPath string) error {
	return s.inner.Delete(ctx, blobPath)
}

func (s *Store) SetTier(ctx context.Context, blobPath, tier string) error {
	return s.inner.SetTier(ctx, blobPath, tier)
}

// Inner exposes the underlying store for the migration (which must read RAW
// bytes to tell encrypted blobs from legacy plaintext ones).
func (s *Store) Inner() backend { return s.inner }

// Keys exposes the key service for the migration.
func (s *Store) Keys() *KeyService { return s.keys }

// ----- resumable chunked writes: encrypt each chunk, stage as a block ----------

// StageChunk encrypts one PLAINTEXT chunk of a resumable upload with the tenant's
// DEK and stages it as a block under blobPath. `first` must be true only for the
// first chunk (index 0): it emits the 5-byte TLE1 header; later chunks are
// frames-only. Every chunk except the last must be a whole multiple of
// FrameChunkSize plaintext, so committing the blocks in order yields a blob
// byte-compatible with a straight-through EncryptReader upload. Returns the
// plaintext bytes staged (for quota); idempotent per blockID.
func (s *Store) StageChunk(ctx context.Context, tenantID int64, blobPath, blockID string, first bool, plain io.Reader) (int64, error) {
	bs, ok := s.inner.(storage.BlockStore)
	if !ok {
		return 0, fmt.Errorf("encryptedstore: inner store has no block staging")
	}
	dek, err := s.keys.DEK(ctx, tenantID)
	if err != nil {
		return 0, err
	}
	var enc *encReader
	if first {
		enc, err = EncryptReader(plain, dek)
	} else {
		enc, err = EncryptReaderNoHeader(plain, dek)
	}
	if err != nil {
		return 0, err
	}
	if _, err := bs.StageBlock(ctx, blobPath, blockID, enc); err != nil {
		return 0, err
	}
	return enc.PlaintextBytes(), nil
}

// CommitChunks finalizes blobPath from the ordered staged blocks.
func (s *Store) CommitChunks(ctx context.Context, blobPath string, blockIDs []string) error {
	bs, ok := s.inner.(storage.BlockStore)
	if !ok {
		return fmt.Errorf("encryptedstore: inner store has no block staging")
	}
	return bs.CommitBlocks(ctx, blobPath, blockIDs)
}

// AbortChunks discards the staged blocks of an abandoned/failed resumable upload.
func (s *Store) AbortChunks(ctx context.Context, blobPath string) error {
	bs, ok := s.inner.(storage.BlockStore)
	if !ok {
		return fmt.Errorf("encryptedstore: inner store has no block staging")
	}
	return bs.AbortBlocks(ctx, blobPath)
}

var (
	_ storage.Store       = (*Store)(nil)
	_ storage.KeyedStore  = (*Store)(nil)
	_ storage.TieredStore = (*Store)(nil)
)

// tenantFromPath parses the tenant id out of a "tenants/{id}/..." blob key.
func tenantFromPath(p string) (int64, bool) {
	parts := strings.SplitN(p, "/", 3)
	if len(parts) < 2 || parts[0] != "tenants" {
		return 0, false
	}
	id, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return 0, false
	}
	return id, true
}

// readCloser pairs a (possibly decrypting) reader with the underlying blob's
// Closer so callers' Close releases the network stream.
type readCloser struct {
	r io.Reader
	c io.Closer
}

func (rc *readCloser) Read(p []byte) (int, error) { return rc.r.Read(p) }
func (rc *readCloser) Close() error               { return rc.c.Close() }

// --- migration of legacy plaintext blobs ---

// Migrator re-encrypts blobs written before encryption was enabled. It reads
// RAW bytes from the inner store, so it can distinguish already-encrypted blobs
// (just flip the flag) from legacy plaintext (read → encrypt → overwrite).
type Migrator struct {
	client *postgres.Client
	store  *Store
}

// NewMigrator builds the migration helper.
func NewMigrator(c *postgres.Client, store *Store) *Migrator {
	return &Migrator{client: c, store: store}
}

// MigrateBatch processes up to limit not-yet-encrypted active files. Returns the
// number of files newly marked encrypted this pass (0 means nothing left).
func (m *Migrator) MigrateBatch(ctx context.Context, limit int) (int, error) {
	rows, err := m.client.QueryContext(ctx, `
		SELECT id, tenant_id, blob_path FROM files
		 WHERE encrypted = FALSE AND status = 'active'
		 ORDER BY id
		 LIMIT $1`, limit)
	if err != nil {
		return 0, fmt.Errorf("encryptedstore: migrate scan: %w", err)
	}
	type job struct {
		id, tenant int64
		blob       string
	}
	var jobs []job
	for rows.Next() {
		var j job
		if err := rows.Scan(&j.id, &j.tenant, &j.blob); err != nil {
			rows.Close()
			return 0, err
		}
		jobs = append(jobs, j)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}

	done := 0
	for _, j := range jobs {
		if err := m.migrateOne(ctx, j.tenant, j.blob); err != nil {
			logging.Error(packageName, "migrate file=%d: %v", j.id, err)
			continue
		}
		if _, err := m.client.ExecContext(ctx,
			`UPDATE files SET encrypted = TRUE WHERE id = $1`, j.id); err != nil {
			logging.Error(packageName, "mark encrypted file=%d: %v", j.id, err)
			continue
		}
		done++
	}
	return done, nil
}

// migrateOne ensures a single blob is encrypted at rest. Already-encrypted
// blobs (new uploads) are left as-is; legacy plaintext is read, encrypted, and
// overwritten in place (Azure commits the new blob atomically, so an interrupted
// run leaves the original intact).
func (m *Migrator) migrateOne(ctx context.Context, tenantID int64, blobPath string) error {
	rc, err := m.store.Inner().Get(ctx, blobPath)
	if err != nil {
		return err
	}
	defer rc.Close()

	hdr := make([]byte, headerLen)
	n, _ := io.ReadFull(rc, hdr)
	if n == headerLen && HasMagic(hdr) {
		return nil // already encrypted — just needs the flag flipped by caller
	}

	// Legacy plaintext: reconstruct the full stream, encrypt, overwrite.
	dek, err := m.store.Keys().DEK(ctx, tenantID)
	if err != nil {
		return err
	}
	plain := io.MultiReader(bytes.NewReader(hdr[:n]), rc)
	enc, err := EncryptReader(plain, dek)
	if err != nil {
		return err
	}
	if _, err := m.store.Inner().PutAt(ctx, blobPath, enc); err != nil {
		return fmt.Errorf("encryptedstore: overwrite %s: %w", blobPath, err)
	}
	return nil
}
