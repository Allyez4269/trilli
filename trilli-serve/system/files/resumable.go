package files

import (
	"context"
	crand "crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"

	"trilli/system/logging"
	"trilli/system/storage"
)

// Resumable chunked uploads. A large file is uploaded as a sequence of fixed-size
// plaintext chunks; each is encrypted and staged as a block (system/storage
// BlockStore) under a stable blob path, and the file is finalized by committing
// the blocks in order. A dropped connection resumes from the last staged chunk
// (the client asks UploadStatus for the received set) instead of restarting — and
// nothing enters the user's file list or quota until CompleteUpload succeeds.

const (
	// 16 MiB plaintext per chunk — a whole multiple of the 64 KiB encryption frame
	// (required so every non-final chunk contains only full frames).
	uploadChunkSize = 16 << 20
	maxUploadChunks = 200_000 // ~3 TiB ceiling; also a runaway guard
)

var (
	ErrUploadNotFound       = errors.New("files: upload session not found")
	ErrUploadIncomplete     = errors.New("files: upload incomplete — missing chunks")
	ErrUploadCompleted      = errors.New("files: upload already completed")
	ErrChunkOutOfRange      = errors.New("files: chunk index out of range")
	ErrChunkSize            = errors.New("files: unexpected chunk size")
	ErrResumableUnsupported = errors.New("files: resumable uploads unavailable")
)

// chunkStore is the encrypting block-staging surface (system/storage/encryptedstore).
// A non-encrypting store won't implement it, in which case resumable uploads are
// unavailable and the client falls back to a single POST.
type chunkStore interface {
	StageChunk(ctx context.Context, tenantID int64, blobPath, blockID string, first bool, plain io.Reader) (int64, error)
	CommitChunks(ctx context.Context, blobPath string, blockIDs []string) error
	AbortChunks(ctx context.Context, blobPath string) error
}

func (s *Service) chunks() (chunkStore, bool) {
	cs, ok := s.store.(chunkStore)
	return cs, ok
}

// ResumableEnabled reports whether the backing store supports resumable uploads.
func (s *Service) ResumableEnabled() bool { _, ok := s.chunks(); return ok }

// UploadSession is the resumable-upload state returned to the client.
type UploadSession struct {
	Token          string `json:"upload_id"`
	Name           string `json:"name"`
	TotalSize      int64  `json:"total_size_bytes"`
	ChunkSize      int64  `json:"chunk_size_bytes"`
	TotalChunks    int    `json:"total_chunks"`
	ReceivedChunks []int  `json:"received_chunks"`
	Completed      bool   `json:"completed"`
}

// InitUploadInput starts a resumable upload. Access (CanWrite on the folder) is
// checked by the caller before this runs, like the single-shot Upload path.
type InitUploadInput struct {
	TenantID       int64
	UserID         int64
	Name           string
	ContentType    string
	TotalSize      int64
	ParentFolderID *int64
	WorkspaceID    *int64
}

func blockID(index int) string {
	// Azure block IDs must be equal-length base64 strings; a zero-padded index
	// keeps them fixed-width and their natural order == chunk order.
	return base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf("blk-%09d", index)))
}

func randHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := crand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// InitUpload validates the request, reserves a session + blob path, and returns
// the chunking plan. Quota is pre-checked here and re-checked under lock at
// completion, so a session can't reserve capacity it later exceeds.
func (s *Service) InitUpload(ctx context.Context, in InitUploadInput) (*UploadSession, error) {
	if _, ok := s.chunks(); !ok {
		return nil, ErrResumableUnsupported
	}
	name := strings.TrimSpace(in.Name)
	if name == "" || strings.ContainsAny(name, "/\\") || len(name) > 255 {
		return nil, ErrInvalidName
	}
	if in.TotalSize <= 0 {
		return nil, fmt.Errorf("files: invalid total size")
	}
	if in.ParentFolderID != nil {
		if err := s.assertFolderBelongsToTenant(ctx, in.TenantID, *in.ParentFolderID); err != nil {
			return nil, err
		}
	}
	workspaceID, err := s.resolveWorkspaceID(ctx, in.TenantID, in.ParentFolderID, in.WorkspaceID)
	if err != nil {
		return nil, err
	}
	if s.meter != nil {
		if over, mErr := s.meter.OverCap(ctx, in.TenantID); mErr == nil && over {
			return nil, ErrTransferExceeded
		}
	}
	q, err := s.Quota(ctx, in.TenantID)
	if err != nil {
		return nil, err
	}
	if in.TotalSize > q.MaxFileSizeBytes {
		return nil, ErrFileTooLarge
	}
	if q.StorageBytesUsed+in.TotalSize > q.MaxStorageBytes {
		return nil, ErrQuotaExceeded
	}

	chunkSize := int64(uploadChunkSize)
	totalChunks := int((in.TotalSize + chunkSize - 1) / chunkSize)
	if totalChunks < 1 {
		totalChunks = 1
	}
	if totalChunks > maxUploadChunks {
		return nil, ErrFileTooLarge
	}

	token, err := randHex(24)
	if err != nil {
		return nil, err
	}
	key, err := randHex(16)
	if err != nil {
		return nil, err
	}
	blobPath := fmt.Sprintf("tenants/%d/%s", in.TenantID, key)

	if _, err := s.client.ExecContext(ctx, `
		INSERT INTO upload_sessions
		  (token, tenant_id, user_id, parent_folder_id, workspace_id, name, content_type,
		   total_size_bytes, chunk_size_bytes, total_chunks, blob_path, expires_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11, now() + interval '1 day')`,
		token, in.TenantID, in.UserID, in.ParentFolderID, workspaceID, name, in.ContentType,
		in.TotalSize, chunkSize, totalChunks, blobPath,
	); err != nil {
		return nil, fmt.Errorf("files: init upload: %w", err)
	}

	return &UploadSession{
		Token: token, Name: name, TotalSize: in.TotalSize, ChunkSize: chunkSize,
		TotalChunks: totalChunks, ReceivedChunks: []int{},
	}, nil
}

type sessionRow struct {
	id             int64
	parentFolderID *int64
	workspaceID    int64
	name           string
	contentType    string
	totalSize      int64
	chunkSize      int64
	totalChunks    int
	blobPath       string
	completed      bool
}

func (s *Service) loadSession(ctx context.Context, tenantID int64, token string) (*sessionRow, error) {
	var r sessionRow
	var completedAt sql.NullTime
	err := s.client.QueryRowContext(ctx, `
		SELECT id, parent_folder_id, workspace_id, name, content_type,
		       total_size_bytes, chunk_size_bytes, total_chunks, blob_path, completed_at
		  FROM upload_sessions WHERE token = $1 AND tenant_id = $2`,
		token, tenantID,
	).Scan(&r.id, &r.parentFolderID, &r.workspaceID, &r.name, &r.contentType,
		&r.totalSize, &r.chunkSize, &r.totalChunks, &r.blobPath, &completedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrUploadNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("files: load session: %w", err)
	}
	r.completed = completedAt.Valid
	return &r, nil
}

// StageUploadChunk encrypts + stages one chunk. Idempotent: re-staging an index
// overwrites its block, so a retried chunk is safe. The chunk's plaintext size
// must match the plan (chunk_size, or the remainder for the final chunk).
func (s *Service) StageUploadChunk(ctx context.Context, tenantID int64, token string, index int, r io.Reader) error {
	cs, ok := s.chunks()
	if !ok {
		return ErrResumableUnsupported
	}
	sess, err := s.loadSession(ctx, tenantID, token)
	if err != nil {
		return err
	}
	if sess.completed {
		return ErrUploadCompleted
	}
	if index < 0 || index >= sess.totalChunks {
		return ErrChunkOutOfRange
	}
	expected := sess.chunkSize
	if index == sess.totalChunks-1 {
		expected = sess.totalSize - int64(index)*sess.chunkSize
	}

	// Cap the read at expected+1 so an over-long chunk is detected, not streamed
	// unbounded.
	n, err := cs.StageChunk(ctx, tenantID, sess.blobPath, blockID(index), index == 0, io.LimitReader(r, expected+1))
	if err != nil {
		return fmt.Errorf("files: stage chunk: %w", err)
	}
	if n != expected {
		// Wrong size — leave it unmarked; the bad block is overwritten on resend.
		return ErrChunkSize
	}

	if _, err := s.client.ExecContext(ctx, `
		INSERT INTO upload_session_chunks (session_id, chunk_index, staged_bytes)
		VALUES ($1, $2, $3)
		ON CONFLICT (session_id, chunk_index) DO UPDATE SET staged_bytes = EXCLUDED.staged_bytes`,
		sess.id, index, n,
	); err != nil {
		return fmt.Errorf("files: mark chunk: %w", err)
	}
	_, _ = s.client.ExecContext(ctx, `UPDATE upload_sessions SET updated_at = now() WHERE id = $1`, sess.id)
	return nil
}

// UploadStatus returns which chunks have landed (for resume).
func (s *Service) UploadStatus(ctx context.Context, tenantID int64, token string) (*UploadSession, error) {
	sess, err := s.loadSession(ctx, tenantID, token)
	if err != nil {
		return nil, err
	}
	rows, err := s.client.QueryContext(ctx,
		`SELECT chunk_index FROM upload_session_chunks WHERE session_id = $1 ORDER BY chunk_index`, sess.id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	received := []int{}
	for rows.Next() {
		var i int
		if err := rows.Scan(&i); err != nil {
			return nil, err
		}
		received = append(received, i)
	}
	return &UploadSession{
		Token: token, Name: sess.name, TotalSize: sess.totalSize, ChunkSize: sess.chunkSize,
		TotalChunks: sess.totalChunks, ReceivedChunks: received, Completed: sess.completed,
	}, rows.Err()
}

// CompleteUpload commits the staged blocks into the final file (row + quota) once
// every chunk has landed.
func (s *Service) CompleteUpload(ctx context.Context, tenantID, userID int64, token string) (*File, error) {
	cs, ok := s.chunks()
	if !ok {
		return nil, ErrResumableUnsupported
	}
	sess, err := s.loadSession(ctx, tenantID, token)
	if err != nil {
		return nil, err
	}
	if sess.completed {
		return nil, ErrUploadCompleted
	}

	var count int
	var sumBytes int64
	if err := s.client.QueryRowContext(ctx,
		`SELECT count(*), COALESCE(sum(staged_bytes), 0) FROM upload_session_chunks WHERE session_id = $1`, sess.id,
	).Scan(&count, &sumBytes); err != nil {
		return nil, err
	}
	if count != sess.totalChunks {
		return nil, ErrUploadIncomplete
	}

	blockIDs := make([]string, sess.totalChunks)
	for i := range blockIDs {
		blockIDs[i] = blockID(i)
	}
	if err := cs.CommitChunks(ctx, sess.blobPath, blockIDs); err != nil {
		return nil, fmt.Errorf("files: commit chunks: %w", err)
	}

	// persistBlob creates the row + charges quota under lock, and deletes the blob
	// on any failure (so a quota-at-commit rejection leaves no orphan).
	f, err := s.persistBlob(ctx, tenantID, sess.parentFolderID, sess.workspaceID,
		sess.name, sess.contentType, userID, storage.PutResult{BlobPath: sess.blobPath, Size: sumBytes}, "")
	if err != nil {
		return nil, err
	}

	if s.meter != nil {
		if mErr := s.meter.RecordIn(ctx, tenantID, &userID, sumBytes); mErr != nil {
			logging.Error(packageName, "CompleteUpload: meter ingress failed (tenant=%d): %v", tenantID, mErr)
		}
	}
	logging.Info(packageName, "CompleteUpload: tenant=%d user=%d file=%d size=%d chunks=%d",
		tenantID, userID, f.ID, f.SizeBytes, sess.totalChunks)
	s.enqueueWarm(f, tenantID)

	_, _ = s.client.ExecContext(ctx, `DELETE FROM upload_sessions WHERE id = $1`, sess.id) // cascades chunks
	return f, nil
}

// AbortUpload discards a session and its staged blocks.
func (s *Service) AbortUpload(ctx context.Context, tenantID int64, token string) error {
	cs, ok := s.chunks()
	if !ok {
		return ErrResumableUnsupported
	}
	sess, err := s.loadSession(ctx, tenantID, token)
	if err != nil {
		return err
	}
	_ = cs.AbortChunks(ctx, sess.blobPath)
	_, err = s.client.ExecContext(ctx, `DELETE FROM upload_sessions WHERE id = $1`, sess.id)
	return err
}

// SweepUploadSessions discards expired, incomplete sessions (and their staged
// blocks). Meant to be run periodically by the jobs runner.
func (s *Service) SweepUploadSessions(ctx context.Context) (int, error) {
	cs, ok := s.chunks()
	rows, err := s.client.QueryContext(ctx,
		`SELECT id, blob_path FROM upload_sessions WHERE completed_at IS NULL AND expires_at < now() LIMIT 200`)
	if err != nil {
		return 0, err
	}
	type dead struct {
		id       int64
		blobPath string
	}
	var deads []dead
	for rows.Next() {
		var d dead
		if err := rows.Scan(&d.id, &d.blobPath); err != nil {
			rows.Close()
			return 0, err
		}
		deads = append(deads, d)
	}
	rows.Close()
	for _, d := range deads {
		if ok {
			_ = cs.AbortChunks(ctx, d.blobPath)
		}
		_, _ = s.client.ExecContext(ctx, `DELETE FROM upload_sessions WHERE id = $1`, d.id)
	}
	return len(deads), nil
}
