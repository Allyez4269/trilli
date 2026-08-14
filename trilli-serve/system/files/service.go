package files

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"trilli/system/database/postgres"
	"trilli/system/docpreview"
	"trilli/system/logging"
	"trilli/system/metering"
	"trilli/system/storage"
	"trilli/system/thumbs"
)

// copySuffixRe matches a trailing "_copy_NN" suffix on a file/folder base
// name. We strip it before generating new candidates so renaming a file
// that's already "<name>_copy_01" lands as "_copy_02" rather than
// "_copy_01_copy_01".
var copySuffixRe = regexp.MustCompile(`_copy_\d+$`)

// Service orchestrates file uploads, listing, downloads, and soft-delete
// against the metadata DB and a storage.Store backend.
type Service struct {
	client  *postgres.Client
	store   storage.Store
	meter   *metering.Service
	preview *docpreview.Service // optional Office→PDF preview engine; nil = disabled
	thumbs  *thumbs.Service     // optional list-row thumbnail engine; nil = disabled
	warmCh  chan warmJob        // eager preview/thumbnail queue; nil = warming off
}

// NewService returns a wired files Service. meter records monthly transfer
// (bandwidth) usage and gates uploads when an account is over its plan's
// transfer cap; it may be nil (metering disabled).
func NewService(c *postgres.Client, s storage.Store, m *metering.Service) *Service {
	return &Service{client: c, store: s, meter: m}
}

// SetPreview enables Office-document preview by attaching a docpreview Service.
// Left unset, PreviewPDF returns ErrPreviewUnavailable for every file.
func (s *Service) SetPreview(p *docpreview.Service) { s.preview = p }

// SetThumbs enables list-row thumbnails by attaching a thumbs Service.
// Left unset, Thumb returns thumbs.ErrUnsupported for every file.
func (s *Service) SetThumbs(t *thumbs.Service) { s.thumbs = t }

// ----- Eager preview/thumbnail warming --------------------------------------
//
// After an upload commits, the file is queued for derivative generation
// (thumbnail + Office→PDF preview) so that by the time anyone opens the
// folder, the work is already done — no "Preparing preview…" spinner on
// first view.
//
// Cluster design — collision-free BY CONSTRUCTION, no coordination needed:
//   - each node warms only the uploads IT received (the queue is in-process),
//     so two nodes never race to convert the same fresh upload;
//   - results land in the shared blob cache, content-addressed by
//     (tenant, file, version) — so every node serves every node's warm work;
//   - generation is idempotent: in the rare overlap (eager here + first-view
//     on another node) both produce identical bytes for the same key, and
//     the underlying converters re-check the cache before working.
//
// Strictly best-effort: the queue is bounded and DROPS when full, workers
// share the same semaphores as interactive requests (an upload burst can
// never starve a user clicking Preview — it just queues behind them), and
// anything skipped is covered by the lazy on-demand path exactly as before.

// warmJob identifies one freshly-uploaded file to pre-render.
type warmJob struct {
	tenantID    int64
	fileID      int64
	name        string
	contentType string
	sizeBytes   int64
}

// eagerPreviewMaxBytes gates eager Office conversion: huge documents are left
// to the lazy path so a single 90 MB deck can't monopolize the warm lanes.
// (Thumbnails enforce their own per-class caps in the thumbs package.)
const eagerPreviewMaxBytes = 20 << 20 // 20 MiB

// warmQueueDepth bounds the in-process queue. At ~3s per Office conversion a
// full queue represents minutes of background work — beyond that, dropping to
// the lazy path beats hoarding memory.
const warmQueueDepth = 256

// StartWarmers launches the background warm workers. Call once at startup;
// returns immediately. Set TRILLI_PREVIEW_WARM=off to run without eager
// warming (the lazy path is unaffected).
func (s *Service) StartWarmers(ctx context.Context) {
	if strings.EqualFold(os.Getenv("TRILLI_PREVIEW_WARM"), "off") {
		logging.Info(packageName, "preview warming DISABLED (TRILLI_PREVIEW_WARM=off)")
		return
	}
	s.warmCh = make(chan warmJob, warmQueueDepth)
	const workers = 2 // real parallelism is bounded by the converter/thumbs semaphores
	for i := 0; i < workers; i++ {
		go s.warmWorker(ctx)
	}
	logging.Info(packageName, "preview warming ON (%d workers, queue %d, office gate %d MB)",
		workers, warmQueueDepth, eagerPreviewMaxBytes>>20)
}

// enqueueWarm hands a committed upload to the warm queue. Non-blocking: if
// the queue is full the job is simply skipped (lazy path covers it).
func (s *Service) enqueueWarm(f *File, tenantID int64) {
	if s.warmCh == nil {
		return
	}
	job := warmJob{
		tenantID:    tenantID,
		fileID:      f.ID,
		name:        f.Name,
		contentType: f.ContentType,
		sizeBytes:   f.SizeBytes,
	}
	select {
	case s.warmCh <- job:
	default:
		logging.Debug(packageName, "warm queue full — skipping file=%d (lazy path will cover)", f.ID)
	}
}

func (s *Service) warmWorker(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case job := <-s.warmCh:
			s.warmOne(ctx, job)
		}
	}
}

// warmOne generates whatever derivatives apply to this file, through the
// same cached code paths the interactive requests use.
func (s *Service) warmOne(ctx context.Context, job warmJob) {
	wctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()

	// Thumbnail (images / PDFs / videos) — cheap, capped in the thumbs pkg.
	if s.thumbs != nil && s.thumbs.Supported(job.name) {
		if rc, _, err := s.Thumb(wctx, job.tenantID, job.fileID); err == nil {
			rc.Close()
			logging.Debug(packageName, "warmed thumbnail file=%d", job.fileID)
		} else if !errors.Is(err, thumbs.ErrUnsupported) && !errors.Is(err, thumbs.ErrTooLarge) {
			logging.Debug(packageName, "thumbnail warm failed file=%d: %v", job.fileID, err)
		}
	}

	// Office→PDF preview — the expensive one, size-gated for eager work.
	if s.preview != nil && docpreview.IsConvertible(job.contentType, job.name) &&
		job.sizeBytes <= eagerPreviewMaxBytes {
		if rc, err := s.PreviewPDF(wctx, job.tenantID, job.fileID); err == nil {
			rc.Close()
			logging.Debug(packageName, "warmed preview file=%d", job.fileID)
		} else {
			logging.Debug(packageName, "preview warm failed file=%d: %v", job.fileID, err)
		}
	}
}

// Thumb returns a small JPEG thumbnail of an image/PDF/video file for the
// list views, generating + caching on first request (keyed by the file's
// updated-at, like PreviewPDF). Caller must have already gated read access.
func (s *Service) Thumb(ctx context.Context, tenantID, fileID int64) (io.ReadCloser, string, error) {
	if s.thumbs == nil {
		return nil, "", thumbs.ErrUnsupported
	}
	f, blobPath, err := s.loadByID(ctx, tenantID, fileID)
	if err != nil {
		return nil, "", err
	}
	version := strconv.FormatInt(f.UpdatedAt.UnixNano(), 10)
	rc, err := s.thumbs.Thumb(ctx, tenantID, fileID, version, f.Name, f.SizeBytes,
		func() (io.ReadCloser, error) { return s.store.Get(ctx, blobPath) })
	return rc, version, err
}

// ErrPreviewUnavailable means this file can't be previewed as a PDF: either the
// preview engine isn't configured, or the file isn't a convertible Office
// document.
var ErrPreviewUnavailable = errors.New("files: preview not available for this file")

// PreviewPDF returns a readable PDF rendering of an Office document for in-app
// preview, converting + caching on first request and serving the cached copy
// thereafter. The cache is keyed by the file's updated-at, so an edited file
// re-renders automatically. Caller must have already gated read access.
func (s *Service) PreviewPDF(ctx context.Context, tenantID, fileID int64) (io.ReadCloser, error) {
	if s.preview == nil {
		return nil, ErrPreviewUnavailable
	}
	f, blobPath, err := s.loadByID(ctx, tenantID, fileID)
	if err != nil {
		return nil, err
	}
	if !docpreview.IsConvertible(f.ContentType, f.Name) {
		return nil, ErrPreviewUnavailable
	}
	version := strconv.FormatInt(f.UpdatedAt.UnixNano(), 10)
	return s.preview.PDF(ctx, tenantID, fileID, version, docpreview.ExtOf(f.Name),
		func() (io.ReadCloser, error) { return s.store.Get(ctx, blobPath) })
}

// FolderScope optionally restricts a tenant-wide listing (recent, starred,
// search, trash, stats) to a set of folder ids. A nil scope means
// unrestricted — used for owner/admin. A non-nil scope restricts to exactly
// those folders; an empty scope therefore matches nothing, which is the
// correct result for a scoped member with no grants. Root files
// (parent_folder_id IS NULL) never match a scope, so scoped members never
// see root-level files in these views.
type FolderScope struct {
	IDs []int64
}

// scopeClause returns an AND fragment binding parameter $idx plus the arg to
// append, or ("", nil) when scope is nil. The ::bigint[] cast lets an empty
// id set ('{}') match nothing without a separate branch. col is the
// (optionally aliased) parent_folder_id column for the query.
func scopeClause(scope *FolderScope, col string, idx int) (string, []any) {
	if scope == nil {
		return "", nil
	}
	return fmt.Sprintf(" AND %s = ANY($%d::bigint[])", col, idx), []any{int64Slice(scope.IDs)}
}

// int64Slice renders []int64 as a Postgres array literal for ANY($N).
type int64Slice []int64

func (s int64Slice) Value() (driver.Value, error) {
	if len(s) == 0 {
		return "{}", nil
	}
	var b strings.Builder
	b.WriteByte('{')
	for i, v := range s {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, "%d", v)
	}
	b.WriteByte('}')
	return b.String(), nil
}

// Quota returns the tenant's current usage and plan limits.
func (s *Service) Quota(ctx context.Context, tenantID int64) (*Quota, error) {
	var q Quota
	err := s.client.QueryRowContext(ctx, `
		SELECT p.name,
		       t.storage_bytes_used,
		       COALESCE(t.quota_override_bytes, p.max_storage_bytes, 9223372036854775807),
		       COALESCE(p.max_file_size_bytes, 9223372036854775807)
		  FROM tenants t JOIN plans p ON p.id = t.plan_id
		 WHERE t.id = $1`, tenantID,
	).Scan(&q.PlanName, &q.StorageBytesUsed, &q.MaxStorageBytes, &q.MaxFileSizeBytes)
	if err != nil {
		return nil, fmt.Errorf("files: load quota: %w", err)
	}
	// Current-month transfer usage (best-effort — a metering hiccup shouldn't
	// break the storage quota the UI depends on).
	if s.meter != nil {
		if u, mErr := s.meter.Usage(ctx, tenantID); mErr != nil {
			logging.Error(packageName, "Quota: transfer usage failed (tenant=%d): %v", tenantID, mErr)
		} else {
			q.TransferUsedBytes = u.UsedBytes
			q.TransferCapBytes = u.CapBytes
		}
	}
	return &q, nil
}

// ----- Upload --------------------------------------------------------------

// UploadInput carries everything Upload needs.
type UploadInput struct {
	TenantID       int64
	UploaderID     int64
	Name           string
	ContentType    string
	Reader         io.Reader
	ParentFolderID *int64 // optional; nil = upload to workspace root
	WorkspaceID    *int64 // optional; nil = inherit parent's workspace, else account default
	// ProtectedSource marks a system-managed file (e.g. "trilli-sign"): it
	// cannot be trashed from Files, only via its managing feature.
	ProtectedSource string
}

// Upload streams the reader into the storage backend, then atomically inserts
// the files row and bumps tenants.storage_bytes_used under a row lock so
// concurrent uploads can't both squeeze past the storage cap.
func (s *Service) Upload(ctx context.Context, in UploadInput) (*File, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" || strings.ContainsAny(name, "/\\") || len(name) > 255 {
		return nil, ErrInvalidName
	}

	if in.ParentFolderID != nil {
		if err := s.assertFolderBelongsToTenant(ctx, in.TenantID, *in.ParentFolderID); err != nil {
			return nil, err
		}
		// The Trilli Sign directory only accepts files staged BY Trilli Sign.
		if in.ProtectedSource == "" {
			var inside bool
			_ = s.client.QueryRowContext(ctx, `
				WITH RECURSIVE up AS (
					SELECT id, parent_folder_id, COALESCE(protected_source, '') AS ps
					  FROM folders WHERE id = $1 AND tenant_id = $2
					UNION ALL
					SELECT p.id, p.parent_folder_id, COALESCE(p.protected_source, '')
					  FROM folders p JOIN up ON up.parent_folder_id = p.id
				)
				SELECT bool_or(ps <> '') FROM up`, *in.ParentFolderID, in.TenantID).Scan(&inside)
			if inside {
				return nil, ErrFolderProtectedUploads
			}
		}
	}

	// Which workspace this file lands in: a child inherits its parent folder's
	// workspace; a root upload uses the explicit workspace or the account default.
	workspaceID, err := s.resolveWorkspaceID(ctx, in.TenantID, in.ParentFolderID, in.WorkspaceID)
	if err != nil {
		return nil, err
	}

	// Monthly transfer cap (bandwidth). Over-cap blocks NEW uploads (downloads
	// stay open — see metering pkg). Checked before we stream anything in.
	if s.meter != nil {
		if over, mErr := s.meter.OverCap(ctx, in.TenantID); mErr != nil {
			logging.Error(packageName, "Upload: transfer cap check failed (allowing): %v", mErr)
		} else if over {
			return nil, ErrTransferExceeded
		}
	}

	q, err := s.Quota(ctx, in.TenantID)
	if err != nil {
		return nil, err
	}

	// Hard-cap per-file size by wrapping the reader. Reading one past the limit
	// means the file is over-cap and is rejected after the fact. Guard the +1
	// against int64 overflow: on unlimited plans MaxFileSizeBytes is max int64,
	// and MaxInt64+1 wraps negative — which makes io.LimitReader return EOF
	// immediately (zero bytes uploaded). When already at the ceiling, just read
	// to MaxInt64 (effectively unbounded; the over-cap check can never trip).
	readLimit := q.MaxFileSizeBytes
	if readLimit < math.MaxInt64 {
		readLimit++
	}
	limited := io.LimitReader(in.Reader, readLimit)

	put, err := s.store.Put(ctx, in.TenantID, limited)
	if err != nil {
		return nil, fmt.Errorf("files: store put: %w", err)
	}
	if put.Size > q.MaxFileSizeBytes {
		_ = s.store.Delete(ctx, put.BlobPath)
		return nil, ErrFileTooLarge
	}

	f, err := s.persistBlob(ctx, in.TenantID, in.ParentFolderID, workspaceID, name, in.ContentType, in.UploaderID, put, in.ProtectedSource)
	if err != nil {
		return nil, err
	}

	// Meter the ingress (best-effort — never fail a committed upload over this).
	if s.meter != nil {
		if mErr := s.meter.RecordIn(ctx, in.TenantID, &in.UploaderID, put.Size); mErr != nil {
			logging.Error(packageName, "Upload: meter ingress failed (tenant=%d): %v", in.TenantID, mErr)
		}
	}

	logging.Info(packageName, "Upload: tenant=%d user=%d file=%d size=%d folder=%v",
		in.TenantID, in.UploaderID, f.ID, f.SizeBytes, in.ParentFolderID)

	// Hand the committed file to the eager preview/thumbnail warmer so its
	// derivatives are usually ready before anyone opens the folder.
	s.enqueueWarm(f, in.TenantID)

	return f, nil
}

// persistBlob inserts the files row for an already-stored blob and bumps the
// tenant + workspace storage counters under a row lock, so concurrent writes
// can't both slip past a quota cap. The blob is rolled back (deleted) on any
// failure. Shared by Upload (a user upload) and CopyToTenant (a cross-account
// copy): both stream the bytes into the store first, then call this to commit
// the metadata atomically. The name auto-dedupes to "<base>_copy_NN<ext>" when
// the destination folder already holds a file by that name.
func (s *Service) persistBlob(ctx context.Context, tenantID int64, parentFolderID *int64, workspaceID int64, name, contentType string, uploaderID int64, put storage.PutResult, protectedSource string) (*File, error) {
	tx, err := s.client.GetDB().BeginTx(ctx, nil)
	if err != nil {
		_ = s.store.Delete(ctx, put.BlobPath)
		return nil, fmt.Errorf("files: begin tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	var currentUsed, maxStorage int64
	err = tx.QueryRowContext(ctx, `
		SELECT t.storage_bytes_used, COALESCE(t.quota_override_bytes, p.max_storage_bytes, 9223372036854775807)
		  FROM tenants t JOIN plans p ON p.id = t.plan_id
		 WHERE t.id = $1
		 FOR UPDATE OF t`, tenantID,
	).Scan(&currentUsed, &maxStorage)
	if err != nil {
		_ = s.store.Delete(ctx, put.BlobPath)
		return nil, fmt.Errorf("files: lock tenant: %w", err)
	}
	if currentUsed+put.Size > maxStorage {
		_ = s.store.Delete(ctx, put.BlobPath)
		return nil, ErrQuotaExceeded
	}

	// Per-workspace allocation, locked alongside the tenant row so concurrent
	// writes into the same workspace can't both slip past its cap.
	var wsUsed, wsAlloc int64
	if err := tx.QueryRowContext(ctx,
		`SELECT storage_bytes_used, disk_allocation_bytes FROM workspaces WHERE id = $1 FOR UPDATE`,
		workspaceID,
	).Scan(&wsUsed, &wsAlloc); err != nil {
		_ = s.store.Delete(ctx, put.BlobPath)
		return nil, fmt.Errorf("files: lock workspace: %w", err)
	}
	if wsUsed+put.Size > wsAlloc {
		_ = s.store.Delete(ctx, put.BlobPath)
		return nil, ErrWorkspaceQuotaExceeded
	}

	// If another active file in this folder already owns this name, auto-pick
	// the next available "<base>_copy_NN<ext>" variant. Done inside the tx so
	// two concurrent writes of the same name can't both land on the same suffix.
	finalName, err := s.resolveAvailableName(ctx, tx, tenantID, parentFolderID, name, 0)
	if err != nil {
		_ = s.store.Delete(ctx, put.BlobPath)
		return nil, err
	}

	var f File
	err = tx.QueryRowContext(ctx, `
		INSERT INTO files (tenant_id, parent_folder_id, name, size_bytes, content_type,
		                   blob_path, uploaded_by_user_id, status, workspace_id, protected_source)
		VALUES ($1, $2, $3, $4, $5, $6, $7, 'active', $8, $9)
		RETURNING id, tenant_id, parent_folder_id, name, size_bytes, content_type, COALESCE(protected_source, ''),
		          uploaded_by_user_id, status, created_at, updated_at, deleted_at, starred_at`,
		tenantID, parentFolderID, finalName, put.Size, contentType, put.BlobPath, uploaderID, workspaceID, protectedSource,
	).Scan(&f.ID, &f.TenantID, &f.ParentFolderID, &f.Name, &f.SizeBytes, &f.ContentType, &f.ProtectedSource,
		&f.UploadedByUserID, &f.Status, &f.CreatedAt, &f.UpdatedAt, &f.DeletedAt, &f.StarredAt)
	if err != nil {
		_ = s.store.Delete(ctx, put.BlobPath)
		return nil, fmt.Errorf("files: insert: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE tenants
		   SET storage_bytes_used = storage_bytes_used + $1, updated_at = NOW()
		 WHERE id = $2`, put.Size, tenantID); err != nil {
		_ = s.store.Delete(ctx, put.BlobPath)
		return nil, fmt.Errorf("files: bump usage: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE workspaces
		   SET storage_bytes_used = storage_bytes_used + $1, updated_at = NOW()
		 WHERE id = $2`, put.Size, workspaceID); err != nil {
		_ = s.store.Delete(ctx, put.BlobPath)
		return nil, fmt.Errorf("files: bump workspace usage: %w", err)
	}

	if err := tx.Commit(); err != nil {
		_ = s.store.Delete(ctx, put.BlobPath)
		return nil, fmt.Errorf("files: commit: %w", err)
	}
	committed = true
	return &f, nil
}

// CrossCopyInput carries a single cross-account (cross-tenant) file copy.
type CrossCopyInput struct {
	SourceTenantID  int64
	SourceFileID    int64
	DestTenantID    int64
	DestFolderID    *int64 // nil = destination workspace root
	DestWorkspaceID *int64 // consulted only for root copies (DestFolderID == nil)
	ActorUserID     int64  // recorded as the destination file's uploader
}

// CopyToTenant copies one file from a source tenant into a DIFFERENT tenant the
// caller is authorized for. Authorization (the actor may read the source and
// write the destination) is the handler's responsibility — this method trusts
// it. Tenants are isolated and per-tenant AES-encrypted, so this is a real byte
// copy: the source blob is streamed out (decrypted under the source tenant's
// key, which the store derives from the blob path) and back in under the
// destination tenant (re-encrypted with its key). It counts against the
// DESTINATION account + workspace quota and is metered as egress on the source
// and ingress on the destination. The source file is never touched —
// cross-account transfers are always copies, never moves.
func (s *Service) CopyToTenant(ctx context.Context, in CrossCopyInput) (*File, error) {
	if in.DestTenantID == in.SourceTenantID {
		return nil, ErrInvalidFolder // same-tenant relocations go through Move
	}

	src, blobPath, err := s.loadByID(ctx, in.SourceTenantID, in.SourceFileID)
	if err != nil {
		return nil, err
	}

	if in.DestFolderID != nil {
		if err := s.assertFolderBelongsToTenant(ctx, in.DestTenantID, *in.DestFolderID); err != nil {
			return nil, err
		}
	}
	workspaceID, err := s.resolveWorkspaceID(ctx, in.DestTenantID, in.DestFolderID, in.DestWorkspaceID)
	if err != nil {
		return nil, err
	}

	// Stream source bytes → destination. The encrypted store decrypts with the
	// source tenant's key (keyed off blobPath) and re-encrypts with the dest
	// tenant's key (keyed off the DestTenantID arg). No plaintext is persisted.
	rc, err := s.store.Get(ctx, blobPath)
	if err != nil {
		return nil, fmt.Errorf("files: open source blob: %w", err)
	}
	defer rc.Close()

	put, err := s.store.Put(ctx, in.DestTenantID, rc)
	if err != nil {
		return nil, fmt.Errorf("files: copy put: %w", err)
	}

	f, err := s.persistBlob(ctx, in.DestTenantID, in.DestFolderID, workspaceID, src.Name, src.ContentType, in.ActorUserID, put, "")
	if err != nil {
		return nil, err
	}

	// Meter the transfer: egress on the source account, ingress on the dest.
	if s.meter != nil {
		if mErr := s.meter.RecordOut(ctx, in.SourceTenantID, &in.ActorUserID, put.Size); mErr != nil {
			logging.Error(packageName, "CopyToTenant: meter egress failed (tenant=%d): %v", in.SourceTenantID, mErr)
		}
		if mErr := s.meter.RecordIn(ctx, in.DestTenantID, &in.ActorUserID, put.Size); mErr != nil {
			logging.Error(packageName, "CopyToTenant: meter ingress failed (tenant=%d): %v", in.DestTenantID, mErr)
		}
	}

	logging.Info(packageName, "CopyToTenant: src=%d/%d -> dest=%d file=%d size=%d by=%d",
		in.SourceTenantID, in.SourceFileID, in.DestTenantID, f.ID, f.SizeBytes, in.ActorUserID)

	s.enqueueWarm(f, in.DestTenantID)
	return f, nil
}

// CopyInput carries a single same-account (same-tenant) file copy.
type CopyInput struct {
	TenantID        int64
	SourceFileID    int64
	DestFolderID    *int64 // nil = destination workspace root
	DestWorkspaceID *int64 // consulted only for root copies (DestFolderID == nil)
	ActorUserID     int64  // recorded as the new file's uploader
	Name            string // "" = keep the source name (then auto-dedupe on clash)
}

// Copy duplicates one file within the SAME account. Unlike CopyToTenant
// (cross-account), the source and destination share the tenant's encryption key,
// so it's a real byte copy that counts against THIS account's quota. The name
// defaults to the source name; persistBlob then keeps it when free in the
// destination and auto-renames to "<base>_copy_NN<ext>" only on a clash — so a
// paste into another folder keeps the name, while a duplicate-in-place renames.
// An explicit Name (the Duplicate dialog) overrides the default.
func (s *Service) Copy(ctx context.Context, in CopyInput) (*File, error) {
	src, blobPath, err := s.loadByID(ctx, in.TenantID, in.SourceFileID)
	if err != nil {
		return nil, err
	}
	if in.DestFolderID != nil {
		if err := s.assertFolderBelongsToTenant(ctx, in.TenantID, *in.DestFolderID); err != nil {
			return nil, err
		}
	}
	workspaceID, err := s.resolveWorkspaceID(ctx, in.TenantID, in.DestFolderID, in.DestWorkspaceID)
	if err != nil {
		return nil, err
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		name = src.Name
	}

	rc, err := s.store.Get(ctx, blobPath)
	if err != nil {
		return nil, fmt.Errorf("files: open source blob: %w", err)
	}
	defer rc.Close()
	put, err := s.store.Put(ctx, in.TenantID, rc)
	if err != nil {
		return nil, fmt.Errorf("files: copy put: %w", err)
	}

	f, err := s.persistBlob(ctx, in.TenantID, in.DestFolderID, workspaceID, name, src.ContentType, in.ActorUserID, put, "")
	if err != nil {
		return nil, err
	}

	if s.meter != nil {
		if mErr := s.meter.RecordIn(ctx, in.TenantID, &in.ActorUserID, put.Size); mErr != nil {
			logging.Error(packageName, "Copy: meter ingress failed (tenant=%d): %v", in.TenantID, mErr)
		}
	}
	logging.Info(packageName, "Copy: tenant=%d src=%d -> file=%d size=%d by=%d",
		in.TenantID, in.SourceFileID, f.ID, f.SizeBytes, in.ActorUserID)
	s.enqueueWarm(f, in.TenantID)
	return f, nil
}

// RecordDownloadBytes meters egress (a successful download) for the account.
// Best-effort and never blocks — over-cap accounts can always retrieve data.
func (s *Service) RecordDownloadBytes(ctx context.Context, tenantID int64, userID *int64, bytes int64) {
	if s.meter == nil {
		return
	}
	if err := s.meter.RecordOut(ctx, tenantID, userID, bytes); err != nil {
		logging.Error(packageName, "RecordDownloadBytes failed (tenant=%d): %v", tenantID, err)
	}
}

// resolveWorkspaceID determines which workspace a new file belongs to: a file
// in a folder inherits that folder's workspace; a root upload uses the explicit
// workspace (validated against the tenant) or the account's default workspace.
func (s *Service) resolveWorkspaceID(ctx context.Context, tenantID int64, parentFolderID, explicit *int64) (int64, error) {
	if parentFolderID != nil {
		var wsID sql.NullInt64
		err := s.client.QueryRowContext(ctx,
			`SELECT workspace_id FROM folders WHERE id = $1 AND tenant_id = $2`,
			*parentFolderID, tenantID,
		).Scan(&wsID)
		if errors.Is(err, sql.ErrNoRows) {
			return 0, ErrInvalidFolder
		}
		if err != nil {
			return 0, fmt.Errorf("files: resolve parent workspace: %w", err)
		}
		if wsID.Valid {
			return wsID.Int64, nil
		}
		// Parent created in the migration gap with no workspace yet — fall back.
	}
	if explicit != nil {
		var ok bool
		if err := s.client.QueryRowContext(ctx,
			`SELECT EXISTS(SELECT 1 FROM workspaces WHERE id = $1 AND tenant_id = $2 AND status = 'active')`,
			*explicit, tenantID,
		).Scan(&ok); err != nil {
			return 0, fmt.Errorf("files: validate workspace: %w", err)
		}
		if !ok {
			return 0, ErrInvalidFolder
		}
		return *explicit, nil
	}
	return s.defaultWorkspaceID(ctx, tenantID)
}

// defaultWorkspaceID returns the account's oldest active workspace (the Default
// Workspace from the migration, or the first one created).
func (s *Service) defaultWorkspaceID(ctx context.Context, tenantID int64) (int64, error) {
	var wsID int64
	err := s.client.QueryRowContext(ctx,
		`SELECT id FROM workspaces WHERE tenant_id = $1 AND status = 'active' ORDER BY created_at, id LIMIT 1`,
		tenantID,
	).Scan(&wsID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("files: account %d has no active workspace", tenantID)
	}
	if err != nil {
		return 0, fmt.Errorf("files: default workspace: %w", err)
	}
	return wsID, nil
}

// ----- Read ---------------------------------------------------------------

// ListInFolder returns active files in a specific folder. parentFolderID nil
// means "files at tenant root" (no parent folder).
func (s *Service) ListInFolder(ctx context.Context, tenantID int64, parentFolderID *int64) ([]*File, error) {
	var rows *sql.Rows
	var err error
	if parentFolderID == nil {
		rows, err = s.client.QueryContext(ctx, listFilesSQL+` AND parent_folder_id IS NULL ORDER BY created_at DESC, id DESC`, tenantID)
	} else {
		rows, err = s.client.QueryContext(ctx, listFilesSQL+` AND parent_folder_id = $2 ORDER BY created_at DESC, id DESC`, tenantID, *parentFolderID)
	}
	if err != nil {
		return nil, fmt.Errorf("files: list in folder: %w", err)
	}
	return scanFiles(rows)
}

// ListWorkspaceRootFiles returns active files at a workspace's root (no parent
// folder), scoped to that workspace.
func (s *Service) ListWorkspaceRootFiles(ctx context.Context, tenantID, workspaceID int64) ([]*File, error) {
	rows, err := s.client.QueryContext(ctx,
		listFilesSQL+` AND parent_folder_id IS NULL AND workspace_id = $2 ORDER BY created_at DESC, id DESC`,
		tenantID, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("files: list workspace root files: %w", err)
	}
	return scanFiles(rows)
}

// ListRecent returns the N most recently created active files across all
// folders for the tenant. Each row's Path is its folder location, e.g.
// "/Documents/Reports/" or "/" when the file lives at tenant root —
// computed in-query via a recursive walk of the folder tree.
func (s *Service) ListRecent(ctx context.Context, tenantID int64, limit int, scope *FolderScope) ([]*File, error) {
	if limit <= 0 || limit > 200 {
		limit = 20
	}
	clause, extra := scopeClause(scope, "f.parent_folder_id", 3)
	args := append([]any{tenantID, limit}, extra...)
	rows, err := s.client.QueryContext(ctx, recentWithPathSQL+clause+`
		 ORDER BY created_at DESC, id DESC
		 LIMIT $2`, args...)
	if err != nil {
		return nil, fmt.Errorf("files: list recent: %w", err)
	}
	return scanFilesWithPath(rows)
}

// ListRecentPage is the paginated recent listing. Limit is clamped to
// [1,100]; offset starts at 0. Sort is one of name/modified/type/size
// (default modified), dir is asc/desc (default desc). Sorting runs
// against the full active file set, not the visible page, so the user
// always sees the global top-N for the chosen column.
func (s *Service) ListRecentPage(ctx context.Context, tenantID int64, offset, limit int, sort, dir, ext string, exclude []string, scope *FolderScope) (*RecentPage, error) {
	if limit <= 0 || limit > 100 {
		limit = 10
	}
	if offset < 0 {
		offset = 0
	}

	var total int64
	totalClause, totalExtra := scopeClause(scope, "parent_folder_id", 2)
	totalArgs := append([]any{tenantID}, totalExtra...)
	extTotalClause, extTotalArgs := extFilterClause(ext, exclude, "name", 2+len(totalExtra))
	totalArgs = append(totalArgs, extTotalArgs...)
	if err := s.client.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM files WHERE tenant_id = $1 AND status = 'active'`+totalClause+extTotalClause,
		totalArgs...).Scan(&total); err != nil {
		return nil, fmt.Errorf("files: recent total: %w", err)
	}

	clause, extra := scopeClause(scope, "f.parent_folder_id", 4)
	args := append([]any{tenantID, limit, offset}, extra...)
	extPageClause, extPageArgs := extFilterClause(ext, exclude, "f.name", 4+len(extra))
	args = append(args, extPageArgs...)
	rows, err := s.client.QueryContext(ctx, recentWithPathSQL+clause+extPageClause+`
		 ORDER BY `+recentOrderBy(sort, dir)+`
		 LIMIT $2 OFFSET $3`, args...)
	if err != nil {
		return nil, fmt.Errorf("files: list recent page: %w", err)
	}
	files, err := scanFilesWithPath(rows)
	if err != nil {
		return nil, err
	}
	return &RecentPage{Files: files, Total: total}, nil
}

// extFilterClause builds a WHERE fragment that filters files by extension,
// matching the same lowercase-extension definition the stats breakdown uses.
//
//	ext == ""          -> no filter
//	ext == "__other__" -> files whose extension is NOT in `exclude` (the chips
//	                      shown individually); the catch-all bucket
//	otherwise          -> exact (case-insensitive) extension match
//
// nameCol is the SQL column holding the filename ("name" or "f.name").
// Placeholders are numbered starting at startIdx; the returned args line up.
func extFilterClause(ext string, exclude []string, nameCol string, startIdx int) (string, []any) {
	expr := "LOWER(SUBSTRING(" + nameCol + " FROM '\\.([^.]+)$'))"
	switch {
	case ext == "":
		return "", nil
	case ext == "__other__":
		if len(exclude) == 0 {
			return "", nil
		}
		ph := make([]string, len(exclude))
		args := make([]any, len(exclude))
		for i, e := range exclude {
			ph[i] = fmt.Sprintf("$%d", startIdx+i)
			args[i] = strings.ToLower(e)
		}
		return fmt.Sprintf(" AND COALESCE(%s, '') NOT IN (%s)", expr, strings.Join(ph, ", ")), args
	default:
		return fmt.Sprintf(" AND %s = $%d", expr, startIdx), []any{strings.ToLower(ext)}
	}
}

// recentOrderBy returns an ORDER BY clause from a whitelisted (sort, dir)
// pair. Concatenated into SQL — never accept user-controlled values
// outside the switch.
func recentOrderBy(sort, dir string) string {
	desc := dir != "asc"
	dirSQL := "DESC"
	if !desc {
		dirSQL = "ASC"
	}
	switch sort {
	case "name":
		return "LOWER(f.name) " + dirSQL + ", f.id " + dirSQL
	case "type":
		return "LOWER(SUBSTRING(f.name FROM '\\.([^.]+)$')) " + dirSQL + " NULLS LAST, f.id " + dirSQL
	case "size":
		return "f.size_bytes " + dirSQL + ", f.id " + dirSQL
	case "modified":
		fallthrough
	default:
		return "f.created_at " + dirSQL + ", f.id " + dirSQL
	}
}

// Stats returns tenant-wide active-file counts grouped by lowercase
// extension (no dot). Files without an extension fall under "" so the UI
// can decide how to label them. Used by the Recent page summary tile.
func (s *Service) Stats(ctx context.Context, tenantID int64, scope *FolderScope) (*FileStats, error) {
	clause, extra := scopeClause(scope, "parent_folder_id", 2)
	args := append([]any{tenantID}, extra...)
	rows, err := s.client.QueryContext(ctx, `
		SELECT COALESCE(LOWER(SUBSTRING(name FROM '\.([^.]+)$')), '') AS ext, COUNT(*) AS n
		  FROM files
		 WHERE tenant_id = $1 AND status = 'active'`+clause+`
		 GROUP BY ext`, args...)
	if err != nil {
		return nil, fmt.Errorf("files: stats: %w", err)
	}
	defer rows.Close()

	out := &FileStats{ByExtension: map[string]int64{}}
	for rows.Next() {
		var ext string
		var n int64
		if err := rows.Scan(&ext, &n); err != nil {
			return nil, fmt.Errorf("files: stats scan: %w", err)
		}
		out.ByExtension[ext] = n
		out.Total += n
	}
	return out, rows.Err()
}

// ListTrashed returns the tenant's trashed (soft-deleted) files for the trash
// page. Phase 5 will add restore + permanent-delete actions over this list.
func (s *Service) ListTrashed(ctx context.Context, tenantID int64, scope *FolderScope) ([]*File, error) {
	clause, extra := scopeClause(scope, "parent_folder_id", 2)
	args := append([]any{tenantID}, extra...)
	rows, err := s.client.QueryContext(ctx, `
		SELECT id, tenant_id, parent_folder_id, name, size_bytes, COALESCE(content_type, ''), COALESCE(protected_source, ''),
		       uploaded_by_user_id, status, created_at, updated_at, deleted_at, starred_at
		  FROM files
		 WHERE tenant_id = $1 AND status = 'trashed'`+clause+`
		 ORDER BY deleted_at DESC NULLS LAST, id DESC`, args...)
	if err != nil {
		return nil, fmt.Errorf("files: list trashed: %w", err)
	}
	return scanFiles(rows)
}

// Search returns files whose name matches the query (case-insensitive substring),
// across the entire tenant. Limited to 200 results for now.
func (s *Service) Search(ctx context.Context, tenantID int64, query string, scope *FolderScope) ([]*File, error) {
	q := strings.TrimSpace(query)
	if q == "" {
		return []*File{}, nil
	}
	pattern := "%" + escapeLikeArg(q) + "%"
	clause, extra := scopeClause(scope, "f.parent_folder_id", 3)
	args := append([]any{tenantID, pattern}, extra...)
	rows, err := s.client.QueryContext(ctx, recentWithPathSQL+`
		 AND f.name ILIKE $2 ESCAPE '\'`+clause+`
		 ORDER BY f.created_at DESC, f.id DESC
		 LIMIT 200`, args...)
	if err != nil {
		return nil, fmt.Errorf("files: search: %w", err)
	}
	return scanFilesWithPath(rows)
}

// Get returns a single active file's metadata, scoped to the tenant.
// Returns ErrFileNotFound if missing or trashed.
func (s *Service) Get(ctx context.Context, tenantID, fileID int64) (*File, error) {
	f, _, err := s.loadByID(ctx, tenantID, fileID)
	return f, err
}

// loadByID returns a single file row scoped to the tenant.
func (s *Service) loadByID(ctx context.Context, tenantID, fileID int64) (*File, string, error) {
	var f File
	var blobPath string
	err := s.client.QueryRowContext(ctx, `
		SELECT id, tenant_id, parent_folder_id, name, size_bytes, COALESCE(content_type, ''), COALESCE(protected_source, ''),
		       blob_path, uploaded_by_user_id, status, created_at, updated_at, deleted_at, starred_at
		  FROM files
		 WHERE id = $1 AND tenant_id = $2`, fileID, tenantID,
	).Scan(&f.ID, &f.TenantID, &f.ParentFolderID, &f.Name, &f.SizeBytes, &f.ContentType, &f.ProtectedSource,
		&blobPath, &f.UploadedByUserID, &f.Status, &f.CreatedAt, &f.UpdatedAt, &f.DeletedAt, &f.StarredAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, "", ErrFileNotFound
		}
		return nil, "", fmt.Errorf("files: load: %w", err)
	}
	if f.Status != "active" {
		return nil, "", ErrFileNotFound
	}
	return &f, blobPath, nil
}

// FileMeta returns the name + parent folder id of an active file (parent
// nil = tenant root). Used by handlers to gate folder-access and to snapshot
// the name/path for the audit log, without a full metadata + blob load.
// Returns ErrFileNotFound if the file is missing or trashed in this tenant.
func (s *Service) FileMeta(ctx context.Context, tenantID, fileID int64) (name string, parentID *int64, err error) {
	var status string
	err = s.client.QueryRowContext(ctx,
		`SELECT name, parent_folder_id, status FROM files WHERE id = $1 AND tenant_id = $2`,
		fileID, tenantID,
	).Scan(&name, &parentID, &status)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil, ErrFileNotFound
		}
		return "", nil, fmt.Errorf("files: file meta: %w", err)
	}
	if status != "active" {
		return "", nil, ErrFileNotFound
	}
	return name, parentID, nil
}

// Download returns metadata + an open reader for the blob. Caller must Close.
func (s *Service) Download(ctx context.Context, tenantID, fileID int64) (*File, io.ReadCloser, error) {
	f, blobPath, err := s.loadByID(ctx, tenantID, fileID)
	if err != nil {
		return nil, nil, err
	}
	rc, err := s.store.Get(ctx, blobPath)
	if err != nil {
		return nil, nil, err
	}
	return f, rc, nil
}

// TouchAccess stamps a file's last_accessed_at = NOW(), the "this file is hot"
// signal the storage-tiering analysis reads. Best-effort and fire-and-forget:
// it must never slow or fail a download. Detached from the request context so
// it isn't cancelled when the client closes the stream.
func (s *Service) TouchAccess(tenantID, fileID int64) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := s.client.ExecContext(ctx,
			`UPDATE files SET last_accessed_at = NOW(), access_count = access_count + 1
			  WHERE id = $1 AND tenant_id = $2 AND status = 'active'`,
			fileID, tenantID); err != nil {
			logging.Debug(packageName, "TouchAccess(%d) failed: %v", fileID, err)
		}
	}()
}

// ----- Mutate -------------------------------------------------------------

// Rename changes a file's name (keeps it in the same folder).
func (s *Service) Rename(ctx context.Context, tenantID, fileID int64, newName string) (*File, error) {
	name := strings.TrimSpace(newName)
	if name == "" || strings.ContainsAny(name, "/\\") || len(name) > 255 {
		return nil, ErrInvalidName
	}
	// Reject a rename that would collide with another active file in the same
	// folder + workspace (case-insensitive). Uploads auto-dedupe with _copy_NN,
	// but an explicit rename should surface the conflict rather than create a twin.
	var dup bool
	if err := s.client.QueryRowContext(ctx, `
		SELECT EXISTS (
		  SELECT 1
		    FROM files sib
		    JOIN files self ON self.id = $2 AND self.tenant_id = $1
		   WHERE sib.tenant_id = $1 AND sib.status = 'active' AND sib.id <> $2
		     AND LOWER(sib.name) = LOWER($3)
		     AND sib.parent_folder_id IS NOT DISTINCT FROM self.parent_folder_id
		     AND sib.workspace_id = self.workspace_id
		)`, tenantID, fileID, name).Scan(&dup); err != nil {
		return nil, fmt.Errorf("files: rename dup check: %w", err)
	}
	if dup {
		return nil, ErrNameTaken
	}
	var f File
	err := s.client.QueryRowContext(ctx, `
		UPDATE files SET name = $1, updated_at = NOW()
		 WHERE id = $2 AND tenant_id = $3 AND status = 'active'
		RETURNING id, tenant_id, parent_folder_id, name, size_bytes, COALESCE(content_type, ''), COALESCE(protected_source, ''),
		          uploaded_by_user_id, status, created_at, updated_at, deleted_at, starred_at`,
		name, fileID, tenantID,
	).Scan(&f.ID, &f.TenantID, &f.ParentFolderID, &f.Name, &f.SizeBytes, &f.ContentType, &f.ProtectedSource,
		&f.UploadedByUserID, &f.Status, &f.CreatedAt, &f.UpdatedAt, &f.DeletedAt, &f.StarredAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrFileNotFound
		}
		return nil, fmt.Errorf("files: rename: %w", err)
	}
	return &f, nil
}

// SetStarred toggles a file's starred state. starred=true sets starred_at
// to NOW() if not already set; starred=false clears it. Idempotent.
func (s *Service) SetStarred(ctx context.Context, tenantID, fileID int64, starred bool) (*File, error) {
	var f File
	var query string
	if starred {
		query = `
			UPDATE files
			   SET starred_at = COALESCE(starred_at, NOW()), updated_at = NOW()
			 WHERE id = $1 AND tenant_id = $2 AND status = 'active'
			RETURNING id, tenant_id, parent_folder_id, name, size_bytes, COALESCE(content_type, ''), COALESCE(protected_source, ''),
			          uploaded_by_user_id, status, created_at, updated_at, deleted_at, starred_at`
	} else {
		query = `
			UPDATE files
			   SET starred_at = NULL, updated_at = NOW()
			 WHERE id = $1 AND tenant_id = $2 AND status = 'active'
			RETURNING id, tenant_id, parent_folder_id, name, size_bytes, COALESCE(content_type, ''), COALESCE(protected_source, ''),
			          uploaded_by_user_id, status, created_at, updated_at, deleted_at, starred_at`
	}
	err := s.client.QueryRowContext(ctx, query, fileID, tenantID).
		Scan(&f.ID, &f.TenantID, &f.ParentFolderID, &f.Name, &f.SizeBytes, &f.ContentType, &f.ProtectedSource,
			&f.UploadedByUserID, &f.Status, &f.CreatedAt, &f.UpdatedAt, &f.DeletedAt, &f.StarredAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrFileNotFound
		}
		return nil, fmt.Errorf("files: set starred: %w", err)
	}
	return &f, nil
}

// ListStarredPage is the paginated starred listing. Same contract as
// ListRecentPage but with WHERE starred_at IS NOT NULL appended and a
// default sort of starred_at DESC.
func (s *Service) ListStarredPage(ctx context.Context, tenantID int64, offset, limit int, sort, dir, ext string, exclude []string, scope *FolderScope) (*RecentPage, error) {
	if limit <= 0 || limit > 100 {
		limit = 10
	}
	if offset < 0 {
		offset = 0
	}

	var total int64
	totalClause, totalExtra := scopeClause(scope, "parent_folder_id", 2)
	totalArgs := append([]any{tenantID}, totalExtra...)
	extTotalClause, extTotalArgs := extFilterClause(ext, exclude, "name", 2+len(totalExtra))
	totalArgs = append(totalArgs, extTotalArgs...)
	if err := s.client.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM files
		 WHERE tenant_id = $1 AND status = 'active' AND starred_at IS NOT NULL`+totalClause+extTotalClause,
		totalArgs...).Scan(&total); err != nil {
		return nil, fmt.Errorf("files: starred total: %w", err)
	}

	orderBy := starredOrderBy(sort, dir)
	clause, extra := scopeClause(scope, "f.parent_folder_id", 4)
	args := append([]any{tenantID, limit, offset}, extra...)
	extPageClause, extPageArgs := extFilterClause(ext, exclude, "f.name", 4+len(extra))
	args = append(args, extPageArgs...)
	rows, err := s.client.QueryContext(ctx, recentWithPathSQL+`
		   AND f.starred_at IS NOT NULL`+clause+extPageClause+`
		 ORDER BY `+orderBy+`
		 LIMIT $2 OFFSET $3`, args...)
	if err != nil {
		return nil, fmt.Errorf("files: list starred: %w", err)
	}
	files, err := scanFilesWithPath(rows)
	if err != nil {
		return nil, err
	}
	return &RecentPage{Files: files, Total: total}, nil
}

// starredOrderBy mirrors recentOrderBy but defaults to starred_at DESC so
// the most recently starred files lead the list.
func starredOrderBy(sort, dir string) string {
	desc := dir != "asc"
	dirSQL := "DESC"
	if !desc {
		dirSQL = "ASC"
	}
	switch sort {
	case "name":
		return "LOWER(f.name) " + dirSQL + ", f.id " + dirSQL
	case "type":
		return "LOWER(SUBSTRING(f.name FROM '\\.([^.]+)$')) " + dirSQL + " NULLS LAST, f.id " + dirSQL
	case "size":
		return "f.size_bytes " + dirSQL + ", f.id " + dirSQL
	case "modified":
		return "f.created_at " + dirSQL + ", f.id " + dirSQL
	default:
		return "f.starred_at " + dirSQL + ", f.id " + dirSQL
	}
}

// StatsStarred is the same breakdown shape as Stats but limited to
// starred files. Powers the summary tile on the Starred page.
func (s *Service) StatsStarred(ctx context.Context, tenantID int64, scope *FolderScope) (*FileStats, error) {
	clause, extra := scopeClause(scope, "parent_folder_id", 2)
	args := append([]any{tenantID}, extra...)
	rows, err := s.client.QueryContext(ctx, `
		SELECT COALESCE(LOWER(SUBSTRING(name FROM '\.([^.]+)$')), '') AS ext, COUNT(*) AS n
		  FROM files
		 WHERE tenant_id = $1 AND status = 'active' AND starred_at IS NOT NULL`+clause+`
		 GROUP BY ext`, args...)
	if err != nil {
		return nil, fmt.Errorf("files: starred stats: %w", err)
	}
	defer rows.Close()
	out := &FileStats{ByExtension: map[string]int64{}}
	for rows.Next() {
		var ext string
		var n int64
		if err := rows.Scan(&ext, &n); err != nil {
			return nil, fmt.Errorf("files: starred stats scan: %w", err)
		}
		out.ByExtension[ext] = n
		out.Total += n
	}
	return out, rows.Err()
}

// Move changes a file's parent folder. newFolderID nil means move to root.
// If a file with the same (case-insensitive) name already lives in the
// destination, the moved file is auto-renamed "<base>_copy_NN<ext>" — same
// rule as Upload. Wrapped in a tx so the name resolution and the UPDATE
// see a consistent snapshot of the destination.
// Move relocates a file. The destination is either a folder (newFolderID) or a
// workspace root. When the destination lives in a DIFFERENT workspace than the
// file does, the file's workspace_id is reassigned and both workspaces' storage
// counters are reconciled — so files can be transferred across workspaces.
// targetWorkspaceID is only consulted for root moves (newFolderID == nil); when
// moving into a folder the target workspace is the folder's own.
func (s *Service) Move(ctx context.Context, tenantID, fileID int64, newFolderID *int64, targetWorkspaceID *int64) (*File, error) {
	if newFolderID != nil {
		if err := s.assertFolderBelongsToTenant(ctx, tenantID, *newFolderID); err != nil {
			return nil, err
		}
	}

	tx, err := s.client.GetDB().BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("files: begin tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	var currentName string
	var currentWS int64
	err = tx.QueryRowContext(ctx, `
		SELECT name, workspace_id FROM files
		 WHERE id = $1 AND tenant_id = $2 AND status = 'active'
		 FOR UPDATE`, fileID, tenantID).Scan(&currentName, &currentWS)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrFileNotFound
		}
		return nil, fmt.Errorf("files: load for move: %w", err)
	}

	// Resolve the destination workspace: the target folder's workspace, an
	// explicit workspace for root moves, or unchanged.
	targetWS := currentWS
	if newFolderID != nil {
		if err := tx.QueryRowContext(ctx,
			`SELECT workspace_id FROM folders WHERE id = $1 AND tenant_id = $2`,
			*newFolderID, tenantID).Scan(&targetWS); err != nil {
			return nil, fmt.Errorf("files: resolve destination workspace: %w", err)
		}
	} else if targetWorkspaceID != nil {
		var ok bool
		if err := tx.QueryRowContext(ctx,
			`SELECT EXISTS(SELECT 1 FROM workspaces WHERE id = $1 AND tenant_id = $2 AND status = 'active')`,
			*targetWorkspaceID, tenantID).Scan(&ok); err != nil {
			return nil, fmt.Errorf("files: check target workspace: %w", err)
		}
		if !ok {
			return nil, ErrWorkspaceNotFound
		}
		targetWS = *targetWorkspaceID
	}

	finalName, err := s.resolveAvailableName(ctx, tx, tenantID, newFolderID, currentName, fileID)
	if err != nil {
		return nil, err
	}

	// Protection is scoped to the Trilli Sign directory: moving a protected
	// file OUT of that tree releases it (it becomes an ordinary, deletable
	// file); moving within the tree keeps it protected.
	insideProtected := false
	if newFolderID != nil {
		_ = tx.QueryRowContext(ctx, `
			WITH RECURSIVE up AS (
				SELECT id, parent_folder_id, COALESCE(protected_source, '') AS ps
				  FROM folders WHERE id = $1 AND tenant_id = $2
				UNION ALL
				SELECT p.id, p.parent_folder_id, COALESCE(p.protected_source, '')
				  FROM folders p JOIN up ON up.parent_folder_id = p.id
			)
			SELECT bool_or(ps <> '') FROM up`, *newFolderID, tenantID).Scan(&insideProtected)
	}

	var f File
	err = tx.QueryRowContext(ctx, `
		UPDATE files SET parent_folder_id = $1, name = $2, workspace_id = $3, updated_at = NOW(),
		       protected_source = CASE WHEN $6 THEN protected_source ELSE '' END
		 WHERE id = $4 AND tenant_id = $5 AND status = 'active'
		RETURNING id, tenant_id, parent_folder_id, name, size_bytes, COALESCE(content_type, ''), COALESCE(protected_source, ''),
		          uploaded_by_user_id, status, created_at, updated_at, deleted_at, starred_at`,
		newFolderID, finalName, targetWS, fileID, tenantID, insideProtected,
	).Scan(&f.ID, &f.TenantID, &f.ParentFolderID, &f.Name, &f.SizeBytes, &f.ContentType, &f.ProtectedSource,
		&f.UploadedByUserID, &f.Status, &f.CreatedAt, &f.UpdatedAt, &f.DeletedAt, &f.StarredAt)
	if err != nil {
		return nil, fmt.Errorf("files: move: %w", err)
	}

	if targetWS != currentWS {
		if err := reconcileWorkspaceStorage(ctx, tx, tenantID, currentWS); err != nil {
			return nil, err
		}
		if err := reconcileWorkspaceStorage(ctx, tx, tenantID, targetWS); err != nil {
			return nil, err
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("files: commit move: %w", err)
	}
	committed = true
	return &f, nil
}

// reconcileWorkspaceStorage recomputes a workspace's storage_bytes_used as the
// true SUM of its files' sizes — idempotent, so cross-workspace moves can't
// drift the per-workspace meters.
func reconcileWorkspaceStorage(ctx context.Context, tx *sql.Tx, tenantID, workspaceID int64) error {
	_, err := tx.ExecContext(ctx, `
		UPDATE workspaces
		   SET storage_bytes_used = COALESCE(
		         (SELECT SUM(size_bytes) FROM files WHERE tenant_id = $1 AND workspace_id = $2), 0),
		       updated_at = NOW()
		 WHERE id = $2 AND tenant_id = $1`, tenantID, workspaceID)
	if err != nil {
		return fmt.Errorf("files: reconcile workspace storage: %w", err)
	}
	return nil
}

// Delete moves a file to trash (status='trashed') as its own one-item batch,
// stamping who deleted it and a purge_at 7 days out. The blob is KEPT (so the
// file can be restored) and the tenant's storage is NOT decremented — trashed
// bytes still occupy quota until the janitor purges them. Permanent removal
// (blob delete + quota decrement + row delete) happens at purge time.
func (s *Service) Delete(ctx context.Context, tenantID, fileID, deletedByUserID int64) error {
	// System-managed files (Trilli Sign deposits) never trash from Files —
	// deleting the envelope in Trilli Sign is the one path that removes them,
	// so the file and its envelope metadata can't drift apart.
	var prot string
	if err := s.client.QueryRowContext(ctx,
		`SELECT COALESCE(protected_source, '') FROM files WHERE id = $1 AND tenant_id = $2`,
		fileID, tenantID).Scan(&prot); err == nil && prot != "" {
		return ErrFileProtected
	}
	res, err := s.client.ExecContext(ctx, `
		UPDATE files
		   SET status = 'trashed', deleted_at = NOW(), updated_at = NOW(),
		       deleted_by_user_id = $3,
		       purge_at = NOW() + INTERVAL '7 days',
		       trash_batch = nextval('trash_batch_seq'),
		       trash_root = true
		 WHERE id = $1 AND tenant_id = $2 AND status = 'active'`,
		fileID, tenantID, deletedByUserID)
	if err != nil {
		return fmt.Errorf("files: trash: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrFileNotFound
	}
	logging.Info(packageName, "Trash: tenant=%d file=%d by=%d", tenantID, fileID, deletedByUserID)
	return nil
}

// RemoveSystemFile hard-deletes a system-managed (protected) file on behalf of
// its managing feature: row + blob gone, tenant/workspace usage decremented.
func (s *Service) RemoveSystemFile(ctx context.Context, tenantID, fileID int64) error {
	var blobPath string
	var size, wsID int64
	err := s.client.QueryRowContext(ctx, `
		DELETE FROM files WHERE id = $1 AND tenant_id = $2 AND COALESCE(protected_source, '') <> ''
		RETURNING blob_path, size_bytes, workspace_id`, fileID, tenantID,
	).Scan(&blobPath, &size, &wsID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil // already gone (or not a protected file) — nothing to unhook
	}
	if err != nil {
		return fmt.Errorf("files: remove system file: %w", err)
	}
	_ = s.store.Delete(ctx, blobPath)
	_, _ = s.client.ExecContext(ctx,
		`UPDATE tenants SET storage_bytes_used = GREATEST(0, storage_bytes_used - $1), updated_at = NOW() WHERE id = $2`, size, tenantID)
	_, _ = s.client.ExecContext(ctx,
		`UPDATE workspaces SET storage_bytes_used = GREATEST(0, storage_bytes_used - $1), updated_at = NOW() WHERE id = $2`, size, wsID)
	logging.Info(packageName, "RemoveSystemFile: tenant=%d file=%d", tenantID, fileID)
	return nil
}

// ----- internals -----------------------------------------------------------

const listFilesSQL = `
	SELECT id, tenant_id, parent_folder_id, name, size_bytes, COALESCE(content_type, ''), COALESCE(protected_source, ''),
	       uploaded_by_user_id, status, created_at, updated_at, deleted_at, starred_at
	  FROM files
	 WHERE tenant_id = $1 AND status = 'active'`

// recentWithPathSQL is the file-list SQL plus a recursive folder_walk that
// builds the absolute path for each file ("/Documents/Reports/" or "/" at
// root). One round-trip — no per-row breadcrumb fetch.
const recentWithPathSQL = `
	WITH RECURSIVE folder_walk AS (
		SELECT id, parent_folder_id, name::text AS full_path
		  FROM folders
		 WHERE tenant_id = $1 AND status = 'active' AND parent_folder_id IS NULL
		UNION ALL
		SELECT f.id, f.parent_folder_id, w.full_path || '/' || f.name
		  FROM folders f
		  JOIN folder_walk w ON w.id = f.parent_folder_id
		 WHERE f.tenant_id = $1 AND f.status = 'active'
	)
	SELECT f.id, f.tenant_id, f.parent_folder_id, f.name, f.size_bytes,
	       COALESCE(f.content_type, ''), f.uploaded_by_user_id, f.status,
	       f.created_at, f.updated_at, f.deleted_at, f.starred_at,
	       CASE WHEN w.full_path IS NULL THEN '/' ELSE '/' || w.full_path || '/' END AS path
	  FROM files f
	  LEFT JOIN folder_walk w ON w.id = f.parent_folder_id
	 WHERE f.tenant_id = $1 AND f.status = 'active'`

func scanFiles(rows *sql.Rows) ([]*File, error) {
	defer rows.Close()
	out := make([]*File, 0, 32)
	for rows.Next() {
		var f File
		if err := rows.Scan(&f.ID, &f.TenantID, &f.ParentFolderID, &f.Name, &f.SizeBytes,
			&f.ContentType, &f.ProtectedSource, &f.UploadedByUserID, &f.Status,
			&f.CreatedAt, &f.UpdatedAt, &f.DeletedAt, &f.StarredAt); err != nil {
			return nil, fmt.Errorf("files: scan: %w", err)
		}
		out = append(out, &f)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("files: rows: %w", err)
	}
	return out, nil
}

// scanFilesWithPath is scanFiles plus the trailing path column from
// recentWithPathSQL.
func scanFilesWithPath(rows *sql.Rows) ([]*File, error) {
	defer rows.Close()
	out := make([]*File, 0, 32)
	for rows.Next() {
		var f File
		if err := rows.Scan(&f.ID, &f.TenantID, &f.ParentFolderID, &f.Name, &f.SizeBytes,
			&f.ContentType, &f.ProtectedSource, &f.UploadedByUserID, &f.Status,
			&f.CreatedAt, &f.UpdatedAt, &f.DeletedAt, &f.StarredAt, &f.Path); err != nil {
			return nil, fmt.Errorf("files: scan with path: %w", err)
		}
		out = append(out, &f)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("files: rows: %w", err)
	}
	return out, nil
}

func (s *Service) assertFolderBelongsToTenant(ctx context.Context, tenantID, folderID int64) error {
	var exists bool
	if err := s.client.QueryRowContext(ctx, `
		SELECT EXISTS(SELECT 1 FROM folders
		               WHERE id = $1 AND tenant_id = $2 AND status = 'active')`,
		folderID, tenantID).Scan(&exists); err != nil {
		return fmt.Errorf("files: folder check: %w", err)
	}
	if !exists {
		return ErrInvalidFolder
	}
	return nil
}

// resolveAvailableName returns desired unchanged if no active file in the
// same folder already uses that name (case-insensitive, ignoring trailing
// underscores/whitespace before the extension). Otherwise it walks
// "<base>_copy_01<ext>", "_copy_02", … until one is free, up to 99 attempts.
// Runs inside the caller's tx so concurrent uploads/moves see each other's
// choices. Pass excludeFileID > 0 when resolving for a move so the file
// being moved doesn't count as its own collision (no-op same-folder move).
func (s *Service) resolveAvailableName(ctx context.Context, tx *sql.Tx, tenantID int64, parentFolderID *int64, desired string, excludeFileID int64) (string, error) {
	free, err := nameIsFree(ctx, tx, tenantID, parentFolderID, desired, excludeFileID)
	if err != nil {
		return "", err
	}
	if free {
		return desired, nil
	}
	base, ext := splitNameExt(desired)
	base = copySuffixRe.ReplaceAllString(base, "")
	base = strings.TrimRight(base, "_ \t")
	for i := 1; i <= 99; i++ {
		candidate := fmt.Sprintf("%s_copy_%02d%s", base, i, ext)
		free, err := nameIsFree(ctx, tx, tenantID, parentFolderID, candidate, excludeFileID)
		if err != nil {
			return "", err
		}
		if free {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("files: too many copies of %q in this folder", desired)
}

// nameIsFree reports whether no active file with this name exists in the
// given folder. Comparison is case-insensitive AND ignores trailing
// underscores/whitespace before the extension, so "report_.pdf" and
// "report.pdf" are treated as the same name (browsers and operating
// systems sometimes produce the trailing-underscore form during duplicate
// downloads). NULL parent_folder_id means tenant root. excludeFileID > 0
// ignores the row with that id (used by Move so the file being moved
// doesn't collide with itself).
// rowQuerier is satisfied by both *sql.Tx and the postgres client, so the
// name-collision check can run inside a move's tx OR as a standalone read.
type rowQuerier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func nameIsFree(ctx context.Context, tx rowQuerier, tenantID int64, parentFolderID *int64, name string, excludeFileID int64) (bool, error) {
	normTarget := normalizeNameForCompare(name)
	var exists bool
	var err error
	// regexp_replace strips trailing underscores/whitespace right before
	// the extension (or end of name, if no ext) so DB-side names normalize
	// the same way Go's normalizeNameForCompare does.
	const stripPattern = `[_[:space:]]+(\.[^.]+)?$`
	if parentFolderID == nil {
		err = tx.QueryRowContext(ctx, `
			SELECT EXISTS(SELECT 1 FROM files
			               WHERE tenant_id = $1 AND parent_folder_id IS NULL
			                 AND status = 'active'
			                 AND regexp_replace(lower(name), $4, '\1') = $2
			                 AND ($3 = 0 OR id <> $3))`,
			tenantID, normTarget, excludeFileID, stripPattern).Scan(&exists)
	} else {
		err = tx.QueryRowContext(ctx, `
			SELECT EXISTS(SELECT 1 FROM files
			               WHERE tenant_id = $1 AND parent_folder_id = $2
			                 AND status = 'active'
			                 AND regexp_replace(lower(name), $5, '\1') = $3
			                 AND ($4 = 0 OR id <> $4))`,
			tenantID, *parentFolderID, normTarget, excludeFileID, stripPattern).Scan(&exists)
	}
	if err != nil {
		return false, fmt.Errorf("files: name check: %w", err)
	}
	return !exists, nil
}

// NameStatus reports whether `name` is free in the destination folder
// (parentFolderID; nil = workspace root) and, when taken, the next available
// "<base>_copy_NN<ext>" suggestion — exactly what a move would auto-assign.
// Read-only; powers the pre-move "a file with this name already exists" prompt.
// ContentLocation returns what the content-save endpoint needs to authorize
// an in-place text edit: the file's folder, workspace, name, content type,
// and protection mark. Active files only.
func (s *Service) ContentLocation(ctx context.Context, tenantID, fileID int64) (folderID *int64, workspaceID int64, name, contentType, protectedSource string, err error) {
	err = s.client.QueryRowContext(ctx, `
		SELECT parent_folder_id, workspace_id, name, COALESCE(content_type, ''), COALESCE(protected_source, '')
		  FROM files WHERE id = $1 AND tenant_id = $2 AND status = 'active'`,
		fileID, tenantID).Scan(&folderID, &workspaceID, &name, &contentType, &protectedSource)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrFileNotFound
	}
	return
}

// OverwriteFile replaces an existing file's blob + metadata in-place (used by
// Productivity Save As when the user chooses Overwrite). The existing file's
// blob is deleted, the new blob is uploaded, and the file row is updated with
// the new size, content type, blob path, and updated_at. The file ID stays the
// same so shares, stars, etc. are preserved.
func (s *Service) OverwriteFile(ctx context.Context, tenantID, fileID int64, name, contentType string, reader io.Reader, uploaderID int64) (*File, error) {
	// Load the existing file for its current blob_path, folder, and OLD size
	// (captured here, before the update — needed for the storage delta).
	f, oldBlob, err := s.loadByID(ctx, tenantID, fileID)
	if err != nil {
		return nil, err
	}
	oldSize := f.SizeBytes

	// Monthly transfer cap blocks new writes (downloads stay open), same as
	// Upload — an overwrite streams fresh bytes in just like a new upload.
	if s.meter != nil {
		if over, mErr := s.meter.OverCap(ctx, tenantID); mErr != nil {
			logging.Error(packageName, "OverwriteFile: transfer cap check failed (allowing): %v", mErr)
		} else if over {
			return nil, ErrTransferExceeded
		}
	}

	// Per-file size cap: wrap the reader so an over-cap overwrite is rejected
	// after the fact, exactly as Upload does (guarding the +1 against overflow
	// on unlimited plans).
	q, err := s.Quota(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	readLimit := q.MaxFileSizeBytes
	if readLimit < math.MaxInt64 {
		readLimit++
	}
	limited := io.LimitReader(reader, readLimit)

	put, err := s.store.Put(ctx, tenantID, limited)
	if err != nil {
		return nil, fmt.Errorf("files: overwrite upload: %w", err)
	}
	if put.Size > q.MaxFileSizeBytes {
		_ = s.store.Delete(ctx, put.BlobPath)
		return nil, ErrFileTooLarge
	}

	// The storage delta this overwrite adds (or frees). Only a positive delta
	// needs to clear the quota/allocation caps; shrinking always fits.
	delta := put.Size - oldSize

	tx, err := s.client.GetDB().BeginTx(ctx, nil)
	if err != nil {
		_ = s.store.Delete(ctx, put.BlobPath)
		return nil, fmt.Errorf("files: begin overwrite tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	// Lock the tenant row and enforce the storage quota on growth (same locked,
	// cap-checked path as persistBlob, so concurrent writes can't slip past).
	var currentUsed, maxStorage int64
	if err := tx.QueryRowContext(ctx, `
		SELECT t.storage_bytes_used, COALESCE(t.quota_override_bytes, p.max_storage_bytes, 9223372036854775807)
		  FROM tenants t JOIN plans p ON p.id = t.plan_id
		 WHERE t.id = $1
		 FOR UPDATE OF t`, tenantID,
	).Scan(&currentUsed, &maxStorage); err != nil {
		_ = s.store.Delete(ctx, put.BlobPath)
		return nil, fmt.Errorf("files: overwrite lock tenant: %w", err)
	}
	if delta > 0 && currentUsed+delta > maxStorage {
		_ = s.store.Delete(ctx, put.BlobPath)
		return nil, ErrQuotaExceeded
	}

	// Lock the file's workspace and enforce its allocation on growth, then keep
	// its counter in sync — the old code never touched it, so it silently drifted.
	var workspaceID int64
	if err := tx.QueryRowContext(ctx,
		`SELECT workspace_id FROM files WHERE id = $1 AND tenant_id = $2`, fileID, tenantID,
	).Scan(&workspaceID); err != nil {
		_ = s.store.Delete(ctx, put.BlobPath)
		return nil, fmt.Errorf("files: overwrite load workspace: %w", err)
	}
	var wsUsed, wsAlloc int64
	if err := tx.QueryRowContext(ctx,
		`SELECT storage_bytes_used, disk_allocation_bytes FROM workspaces WHERE id = $1 FOR UPDATE`, workspaceID,
	).Scan(&wsUsed, &wsAlloc); err != nil {
		_ = s.store.Delete(ctx, put.BlobPath)
		return nil, fmt.Errorf("files: overwrite lock workspace: %w", err)
	}
	if delta > 0 && wsUsed+delta > wsAlloc {
		_ = s.store.Delete(ctx, put.BlobPath)
		return nil, ErrWorkspaceQuotaExceeded
	}

	// Update the file row in place — the id is preserved so shares/stars survive.
	if err := tx.QueryRowContext(ctx, `
		UPDATE files
		   SET blob_path = $1, size_bytes = $2, content_type = $3,
		       name = $4, updated_at = NOW(), uploaded_by_user_id = $5
		 WHERE id = $6 AND tenant_id = $7 AND status = 'active'
		 RETURNING id, tenant_id, parent_folder_id, name, size_bytes, content_type, COALESCE(protected_source, ''),
		           uploaded_by_user_id, status, created_at, updated_at, deleted_at, starred_at`,
		put.BlobPath, put.Size, contentType, name, uploaderID, fileID, tenantID,
	).Scan(&f.ID, &f.TenantID, &f.ParentFolderID, &f.Name, &f.SizeBytes, &f.ContentType, &f.ProtectedSource,
		&f.UploadedByUserID, &f.Status, &f.CreatedAt, &f.UpdatedAt, &f.DeletedAt, &f.StarredAt); err != nil {
		_ = s.store.Delete(ctx, put.BlobPath)
		return nil, fmt.Errorf("files: overwrite update: %w", err)
	}

	if delta != 0 {
		if _, err := tx.ExecContext(ctx,
			`UPDATE tenants SET storage_bytes_used = storage_bytes_used + $1, updated_at = NOW() WHERE id = $2`,
			delta, tenantID); err != nil {
			_ = s.store.Delete(ctx, put.BlobPath)
			return nil, fmt.Errorf("files: overwrite tenant usage: %w", err)
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE workspaces SET storage_bytes_used = storage_bytes_used + $1, updated_at = NOW() WHERE id = $2`,
			delta, workspaceID); err != nil {
			_ = s.store.Delete(ctx, put.BlobPath)
			return nil, fmt.Errorf("files: overwrite workspace usage: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		_ = s.store.Delete(ctx, put.BlobPath)
		return nil, fmt.Errorf("files: overwrite commit: %w", err)
	}
	committed = true

	// Meter the ingress (best-effort — never fail a committed write over this).
	if s.meter != nil {
		if mErr := s.meter.RecordIn(ctx, tenantID, &uploaderID, put.Size); mErr != nil {
			logging.Error(packageName, "OverwriteFile: meter ingress failed (tenant=%d): %v", tenantID, mErr)
		}
	}

	// Delete the old blob (best-effort).
	if oldBlob != "" && oldBlob != put.BlobPath {
		_ = s.store.Delete(ctx, oldBlob)
	}
	logging.Info(packageName, "OverwriteFile: file=%d tenant=%d oldBlob=%s newBlob=%s oldSize=%d newSize=%d delta=%d",
		fileID, tenantID, oldBlob, put.BlobPath, oldSize, put.Size, delta)

	// Eagerly regenerate the derived PDF preview (and any thumbnail) for the
	// just-saved content, exactly as Upload does for a new file. The preview
	// cache is keyed by the file's updated-at, which this overwrite advanced, so
	// the stale preview is already orphaned — this warms the FRESH version ahead
	// of the next open instead of leaving it to the lazy "Preparing preview…"
	// path. Non-blocking (best-effort queue), so it never delays the save.
	s.enqueueWarm(f, tenantID)

	return f, nil
}

func (s *Service) NameStatus(ctx context.Context, tenantID int64, parentFolderID *int64, name string) (available bool, suggested string, err error) {
	free, err := nameIsFree(ctx, s.client, tenantID, parentFolderID, name, 0)
	if err != nil {
		return false, "", err
	}
	if free {
		return true, name, nil
	}
	base, ext := splitNameExt(name)
	base = copySuffixRe.ReplaceAllString(base, "")
	base = strings.TrimRight(base, "_ \t")
	for i := 1; i <= 99; i++ {
		candidate := fmt.Sprintf("%s_copy_%02d%s", base, i, ext)
		f, e := nameIsFree(ctx, s.client, tenantID, parentFolderID, candidate, 0)
		if e != nil {
			return false, "", e
		}
		if f {
			return false, candidate, nil
		}
	}
	return false, name, nil
}

// normalizeNameForCompare lowercases and strips trailing whitespace + a
// run of underscores/whitespace from the base (the part before the
// extension) so comparison ignores cosmetic differences.
func normalizeNameForCompare(name string) string {
	base, ext := splitNameExt(name)
	base = strings.TrimRight(base, "_ \t")
	return strings.ToLower(base + ext)
}

// splitNameExt returns ("report", ".pdf") for "report.pdf". For dotfiles
// like ".env" or names with no extension, ext is empty so the copy suffix
// goes at the end of the whole name.
func splitNameExt(name string) (base, ext string) {
	dot := strings.LastIndex(name, ".")
	if dot <= 0 {
		return name, ""
	}
	return name[:dot], name[dot:]
}

// escapeLikeArg escapes %, _, and \ for a LIKE/ILIKE pattern so a user search
// for "100%" doesn't act as a wildcard.
func escapeLikeArg(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}

// pgUniqueViolation classifies a unique-constraint error.
func pgUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "23505"
	}
	return false
}

var _ = pgUniqueViolation // reserved for future per-folder file uniqueness

// FileStatus returns a file's lifecycle status ('active'|'trashed'|...) or
// ErrFileNotFound if the row is gone. Used to detect a source file deleted out
// from under a live office session.
func (s *Service) FileStatus(ctx context.Context, tenantID, fileID int64) (string, error) {
	var status string
	err := s.client.QueryRowContext(ctx,
		`SELECT status FROM files WHERE id = $1 AND tenant_id = $2`, fileID, tenantID,
	).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrFileNotFound
	}
	if err != nil {
		return "", err
	}
	return status, nil
}
