package sign

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image/png"
	"io"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"

	"trilli/system/credentials"
	"trilli/system/database/postgres"
	"trilli/system/files"
	"trilli/system/logging"
	"trilli/system/mailer"
	"trilli/system/pdftools"
	"trilli/system/qserve"
	"trilli/system/storage"
)

const packageName = "sign"

var (
	ErrNotFound      = errors.New("sign: envelope not found")
	ErrNotDraft      = errors.New("sign: envelope is no longer editable")
	ErrNotPDF        = errors.New("sign: source file must be a PDF")
	ErrNoRecipients  = errors.New("sign: add at least one recipient before sending")
	ErrEmptySigner   = errors.New("sign: every recipient needs at least one field")
	ErrBadInput      = errors.New("sign: invalid input")
	ErrTokenNotFound = errors.New("sign: signing link not found")
)

// Service is the Trilli Sign backend: envelopes wrapping immutable encrypted
// PDF snapshots, with recipients, placed fields, and an append-only event
// trail. Storage runs through the same encrypted store as user files.
type Service struct {
	pg      *postgres.Client
	store   storage.Store
	mail    *mailer.Mailer
	conv    DocConverter  // optional Word→PDF converter (LibreOffice); nil = PDFs only
	deposit FileDepositor // optional Files integration for completed agreements
	creds   *credentials.Service
	geo     GeoLookup

	// Rendered-page cache for the editor: envelope id -> encoded PNGs (one per
	// page at a fixed DPI). Rendering is the expensive step (pdfium over the
	// whole doc), so keep the last few envelopes hot.
	mu    sync.Mutex
	pages map[int64][][]byte
}

func NewService(pg *postgres.Client, store storage.Store, mail *mailer.Mailer, creds *credentials.Service) *Service {
	return &Service{pg: pg, store: store, mail: mail, creds: creds, pages: make(map[int64][][]byte)}
}

// FileDepositor is the slice of the Files service Sign needs to deposit the
// completed agreement into the user's file tree.
type FileDepositor interface {
	Upload(ctx context.Context, in files.UploadInput) (*files.File, error)
	RemoveSystemFile(ctx context.Context, tenantID, fileID int64) error
}

// SetFileDepositor wires the Files service so completed envelopes land in the
// tenant's configured "Envelopes save to" destination.
func (s *Service) SetFileDepositor(d FileDepositor) { s.deposit = d }

// Settings is the tenant's Sign preferences (deposit destination).
type Settings struct {
	WorkspaceID   *int64 `json:"workspace_id"`
	FolderID      *int64 `json:"folder_id"`
	WorkspaceName string `json:"workspace_name,omitempty"`
	FolderName    string `json:"folder_name,omitempty"`
}

// GetSettings returns the tenant's deposit destination with display names
// resolved (deleted folders resolve back to the default root).
func (s *Service) GetSettings(ctx context.Context, tenantID int64) (*Settings, error) {
	out := &Settings{}
	var ws, fo sql.NullInt64
	err := s.pg.QueryRowContext(ctx,
		`SELECT workspace_id, folder_id FROM sign_settings WHERE tenant_id = $1`, tenantID,
	).Scan(&ws, &fo)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if ws.Valid {
		out.WorkspaceID = &ws.Int64
		_ = s.pg.QueryRowContext(ctx, `SELECT name FROM workspaces WHERE id = $1 AND tenant_id = $2`,
			ws.Int64, tenantID).Scan(&out.WorkspaceName)
	}
	if fo.Valid {
		out.FolderID = &fo.Int64
		_ = s.pg.QueryRowContext(ctx, `SELECT name FROM folders WHERE id = $1 AND tenant_id = $2`,
			fo.Int64, tenantID).Scan(&out.FolderName)
	}
	return out, nil
}

// SetSettings stores the deposit destination (upsert).
func (s *Service) SetSettings(ctx context.Context, tenantID int64, workspaceID, folderID *int64) error {
	_, err := s.pg.ExecContext(ctx, `
		INSERT INTO sign_settings (tenant_id, workspace_id, folder_id, updated_at)
		VALUES ($1, $2, $3, now())
		ON CONFLICT (tenant_id) DO UPDATE
		   SET workspace_id = EXCLUDED.workspace_id, folder_id = EXCLUDED.folder_id, updated_at = now()`,
		tenantID, workspaceID, folderID)
	return err
}

// ensureSignFolders provisions the tenant's "Trilli Sign" system directory
// ("Trilli Sign" -> "Signed Agreements" + "Drafts") in the default (or
// configured) workspace, pinning ids in sign_settings so renames survive.
// Files staged in this tree are protected; copies/moves out are ordinary.
func (s *Service) ensureSignFolders(ctx context.Context, tenantID, userID int64) (draftsID, signedID int64, err error) {
	var ws sql.NullInt64
	var root, drafts, signed sql.NullInt64
	_ = s.pg.QueryRowContext(ctx, `
		SELECT workspace_id, root_folder_id, drafts_folder_id, signed_folder_id
		  FROM sign_settings WHERE tenant_id = $1`, tenantID).Scan(&ws, &root, &drafts, &signed)

	alive := func(id sql.NullInt64) bool {
		if !id.Valid {
			return false
		}
		var ok bool
		_ = s.pg.QueryRowContext(ctx,
			`SELECT EXISTS(SELECT 1 FROM folders WHERE id = $1 AND tenant_id = $2 AND status = 'active')`,
			id.Int64, tenantID).Scan(&ok)
		return ok
	}
	if alive(drafts) && alive(signed) {
		return drafts.Int64, signed.Int64, nil
	}

	wsID := ws.Int64
	if !ws.Valid {
		if err := s.pg.QueryRowContext(ctx,
			`SELECT id FROM workspaces WHERE tenant_id = $1 AND status = 'active' ORDER BY id LIMIT 1`,
			tenantID).Scan(&wsID); err != nil {
			return 0, 0, fmt.Errorf("sign: resolve workspace: %w", err)
		}
	}

	// find-or-create, adopting a same-named user folder if one exists
	ensure := func(parent sql.NullInt64, name string) (int64, error) {
		var id int64
		var q string
		var args []any
		if parent.Valid {
			q = `SELECT id FROM folders WHERE tenant_id = $1 AND workspace_id = $2 AND parent_folder_id = $3 AND lower(name) = lower($4) AND status = 'active' LIMIT 1`
			args = []any{tenantID, wsID, parent.Int64, name}
		} else {
			q = `SELECT id FROM folders WHERE tenant_id = $1 AND workspace_id = $2 AND parent_folder_id IS NULL AND lower(name) = lower($3) AND status = 'active' LIMIT 1`
			args = []any{tenantID, wsID, name}
		}
		err := s.pg.QueryRowContext(ctx, q, args...).Scan(&id)
		if err == nil {
			_, _ = s.pg.ExecContext(ctx, `UPDATE folders SET protected_source = 'trilli-sign' WHERE id = $1`, id)
			return id, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return 0, err
		}
		var parentArg any
		if parent.Valid {
			parentArg = parent.Int64
		}
		if err := s.pg.QueryRowContext(ctx, `
			INSERT INTO folders (tenant_id, parent_folder_id, name, created_by_user_id, workspace_id, protected_source)
			VALUES ($1, $2, $3, $4, $5, 'trilli-sign') RETURNING id`,
			tenantID, parentArg, name, userID, wsID).Scan(&id); err != nil {
			return 0, err
		}
		return id, nil
	}

	rootID, err := ensure(sql.NullInt64{}, "Trilli Sign")
	if err != nil {
		return 0, 0, fmt.Errorf("sign: ensure root folder: %w", err)
	}
	rootRef := sql.NullInt64{Int64: rootID, Valid: true}
	draftsID, err = ensure(rootRef, "Drafts")
	if err != nil {
		return 0, 0, fmt.Errorf("sign: ensure drafts folder: %w", err)
	}
	signedID, err = ensure(rootRef, "Signed Agreements")
	if err != nil {
		return 0, 0, fmt.Errorf("sign: ensure signed folder: %w", err)
	}
	_, _ = s.pg.ExecContext(ctx, `
		INSERT INTO sign_settings (tenant_id, root_folder_id, drafts_folder_id, signed_folder_id, updated_at)
		VALUES ($1, $2, $3, $4, now())
		ON CONFLICT (tenant_id) DO UPDATE
		   SET root_folder_id = $2, drafts_folder_id = $3, signed_folder_id = $4, updated_at = now()`,
		tenantID, rootID, draftsID, signedID)
	return draftsID, signedID, nil
}

// AttachUploadedDocument stages raw bytes as a PROTECTED file in the Trilli
// Sign "Drafts" folder, then attaches it to the draft envelope. This is the
// desktop drag-drop path — sources never land loose in the workspace root.
func (s *Service) AttachUploadedDocument(ctx context.Context, tenantID, envelopeID, userID int64, name string, raw []byte, actor string) (*Envelope, error) {
	if s.deposit == nil {
		return nil, fmt.Errorf("sign: %w (file staging unavailable)", ErrNotPDF)
	}
	draftsID, _, err := s.ensureSignFolders(ctx, tenantID, userID)
	if err != nil {
		return nil, err
	}
	f, err := s.deposit.Upload(ctx, files.UploadInput{
		TenantID:        tenantID,
		UploaderID:      userID,
		Name:            name,
		ContentType:     "application/octet-stream",
		Reader:          bytes.NewReader(raw),
		ParentFolderID:  &draftsID,
		ProtectedSource: "trilli-sign",
	})
	if err != nil {
		return nil, err
	}
	env, err := s.AttachDocument(ctx, tenantID, envelopeID, f.ID, actor)
	if err != nil {
		_ = s.deposit.RemoveSystemFile(ctx, tenantID, f.ID) // not a usable source — unstage
		return nil, err
	}
	return env, nil
}

// DocConverter converts office documents to PDF (satisfied by
// docpreview.LibreOfficeConverter). Wired at startup when LibreOffice exists.
type DocConverter interface {
	Convert(ctx context.Context, src io.Reader, srcExt, targetExt string) ([]byte, error)
}

// SetConverter wires the Word→PDF engine so envelopes accept .docx/.doc
// sources (converted to PDF at attach time).
func (s *Service) SetConverter(c DocConverter) { s.conv = c }

// GeoLookup is the slice of qserve the disclosure needs: IP -> location.
type GeoLookup interface {
	LookupIP(ip string) (*qserve.GeoIPResponse, error)
}

// SetGeoLookup wires the in-house GeoIP service (system/qserve) so the
// signing disclosure can show the signer their resolved location.
func (s *Service) SetGeoLookup(g GeoLookup) { s.geo = g }

// ----- types ----------------------------------------------------------------

type Envelope struct {
	ID          int64      `json:"id"`
	Title       string     `json:"title"`
	Subject     string     `json:"subject"`
	Category    string     `json:"category"`
	Message     string     `json:"message"`
	Status      string     `json:"status"`
	PageCount   int        `json:"page_count"`
	SizeBytes   int64      `json:"size_bytes"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	SentAt      *time.Time `json:"sent_at,omitempty"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`

	Recipients []*Recipient `json:"recipients,omitempty"`
	Fields     []*Field     `json:"fields,omitempty"`
}

type Recipient struct {
	ID           int64      `json:"id"`
	Name         string     `json:"name"`
	Email        string     `json:"email"`
	SigningOrder int        `json:"signing_order"`
	Status       string     `json:"status"`
	SignedAt     *time.Time `json:"signed_at,omitempty"`
}

type Field struct {
	ID          int64           `json:"id"`
	RecipientID int64           `json:"recipient_id"`
	Kind        string          `json:"kind"`
	Page        int             `json:"page"`
	X           float64         `json:"x"`
	Y           float64         `json:"y"`
	W           float64         `json:"w"`
	H           float64         `json:"h"`
	Required    bool            `json:"required"`
	Meta        json.RawMessage `json:"meta"`
	Value       string          `json:"value,omitempty"` // ceremony payload only (e.g. uploaded filename)
}

type Event struct {
	Actor     string    `json:"actor"`
	Action    string    `json:"action"`
	Detail    string    `json:"detail"`
	CreatedAt time.Time `json:"created_at"`
}

var fieldKinds = map[string]bool{
	// legacy
	"signature": true, "initials": true, "date": true, "text": true, "checkbox": true,
	// DocuSign-style palette
	"date_signed": true, "name": true, "email": true, "company": true, "title": true,
	// inputs / actions / other
	"number": true, "dropdown": true, "radio": true, "note": true,
	"approve": true, "decline": true, "attachment": true, "formula": true,
}

func randToken() (string, error) {
	b := make([]byte, 20)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func (s *Service) event(ctx context.Context, envelopeID int64, actor, action, detail string) {
	if _, err := s.pg.ExecContext(ctx,
		`INSERT INTO sign_events (envelope_id, actor, action, detail) VALUES ($1,$2,$3,$4)`,
		envelopeID, actor, action, detail); err != nil {
		logging.Error(packageName, "event %s on envelope %d: %v", action, envelopeID, err)
	}
}

// ----- envelope lifecycle -----------------------------------------------------

// CreateEnvelope snapshots a tenant PDF into an envelope-owned encrypted blob.
// The snapshot is immutable: later edits or deletion of the source file can't
// change what recipients sign.
func (s *Service) CreateEnvelope(ctx context.Context, tenantID, userID int64, actor string, fileID int64) (*Envelope, error) {
	name, blobPath, size, pageCount, err := s.snapshotPDF(ctx, tenantID, fileID)
	if err != nil {
		return nil, err
	}
	title := pdfTitle(name)
	var e Envelope
	err = s.pg.QueryRowContext(ctx, `
		INSERT INTO sign_envelopes (tenant_id, created_by_user_id, title, source_file_id, blob_path, size_bytes, page_count)
		VALUES ($1,$2,$3,$4,$5,$6,$7)
		RETURNING id, title, message, status, page_count, size_bytes, created_at, updated_at`,
		tenantID, userID, title, fileID, blobPath, size, pageCount,
	).Scan(&e.ID, &e.Title, &e.Message, &e.Status, &e.PageCount, &e.SizeBytes, &e.CreatedAt, &e.UpdatedAt)
	if err != nil {
		_ = s.store.Delete(ctx, blobPath)
		return nil, fmt.Errorf("sign: insert envelope: %w", err)
	}
	s.event(ctx, e.ID, actor, "created", fmt.Sprintf("from %q", name))
	return &e, nil
}

// CreateBlankEnvelope makes a document-less DRAFT so "New envelope" can jump
// straight to setup; the PDF is attached later via AttachDocument.
func (s *Service) CreateBlankEnvelope(ctx context.Context, tenantID, userID int64, actor string) (*Envelope, error) {
	var e Envelope
	err := s.pg.QueryRowContext(ctx, `
		INSERT INTO sign_envelopes (tenant_id, created_by_user_id, title, blob_path, size_bytes, page_count)
		VALUES ($1,$2,$3,NULL,0,0)
		RETURNING id, title, subject, message, status, page_count, size_bytes, created_at, updated_at`,
		tenantID, userID, "Untitled envelope",
	).Scan(&e.ID, &e.Title, &e.Subject, &e.Message, &e.Status, &e.PageCount, &e.SizeBytes, &e.CreatedAt, &e.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("sign: insert blank envelope: %w", err)
	}
	s.event(ctx, e.ID, actor, "created", "")
	return &e, nil
}

// AttachDocument snapshots a Trilli PDF into an existing DRAFT envelope that has
// no document yet (or replaces the current one). Draft-only.
func (s *Service) AttachDocument(ctx context.Context, tenantID, envelopeID, fileID int64, actor string) (*Envelope, error) {
	var status string
	var oldBlob sql.NullString
	if err := s.pg.QueryRowContext(ctx,
		`SELECT status, blob_path FROM sign_envelopes WHERE id = $1 AND tenant_id = $2`,
		envelopeID, tenantID).Scan(&status, &oldBlob); errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	} else if err != nil {
		return nil, err
	}
	if status != "draft" {
		return nil, ErrNotDraft
	}
	name, blobPath, size, pageCount, err := s.snapshotPDF(ctx, tenantID, fileID)
	if err != nil {
		return nil, err
	}
	var e Envelope
	err = s.pg.QueryRowContext(ctx, `
		UPDATE sign_envelopes
		   SET title = $3, source_file_id = $4, blob_path = $5, size_bytes = $6, page_count = $7, updated_at = now()
		 WHERE id = $1 AND tenant_id = $2
		RETURNING id, title, subject, message, status, page_count, size_bytes, created_at, updated_at`,
		envelopeID, tenantID, pdfTitle(name), fileID, blobPath, size, pageCount,
	).Scan(&e.ID, &e.Title, &e.Subject, &e.Message, &e.Status, &e.PageCount, &e.SizeBytes, &e.CreatedAt, &e.UpdatedAt)
	if err != nil {
		_ = s.store.Delete(ctx, blobPath)
		return nil, fmt.Errorf("sign: attach document: %w", err)
	}
	if oldBlob.Valid && oldBlob.String != "" {
		_ = s.store.Delete(ctx, oldBlob.String) // replaced snapshot
	}
	// re-rendering cache is keyed by envelope id; invalidate any cached pages
	s.mu.Lock()
	delete(s.pages, e.ID)
	s.mu.Unlock()
	s.event(ctx, e.ID, actor, "updated", fmt.Sprintf("document set to %q", name))
	return &e, nil
}

// RemoveDocument clears a DRAFT envelope's document, returning it to the empty
// state (blank page count) so the setup drop zone reappears.
func (s *Service) RemoveDocument(ctx context.Context, tenantID, envelopeID int64, actor string) (*Envelope, error) {
	var status string
	var oldBlob sql.NullString
	if err := s.pg.QueryRowContext(ctx,
		`SELECT status, blob_path FROM sign_envelopes WHERE id = $1 AND tenant_id = $2`,
		envelopeID, tenantID).Scan(&status, &oldBlob); errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	} else if err != nil {
		return nil, err
	}
	if status != "draft" {
		return nil, ErrNotDraft
	}
	var e Envelope
	err := s.pg.QueryRowContext(ctx, `
		UPDATE sign_envelopes
		   SET title = 'Untitled envelope', source_file_id = NULL, blob_path = NULL,
		       size_bytes = 0, page_count = 0, updated_at = now()
		 WHERE id = $1 AND tenant_id = $2
		RETURNING id, title, subject, category, message, status, page_count, size_bytes, created_at, updated_at`,
		envelopeID, tenantID,
	).Scan(&e.ID, &e.Title, &e.Subject, &e.Category, &e.Message, &e.Status, &e.PageCount, &e.SizeBytes, &e.CreatedAt, &e.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("sign: remove document: %w", err)
	}
	if oldBlob.Valid && oldBlob.String != "" {
		_ = s.store.Delete(ctx, oldBlob.String)
	}
	s.mu.Lock()
	delete(s.pages, e.ID)
	s.mu.Unlock()
	s.event(ctx, e.ID, actor, "updated", "document removed")
	return &e, nil
}

// wordExts are source formats the envelope converter accepts besides PDF;
// they're converted to PDF at attach time via the shared LibreOffice engine.
var wordExts = map[string]bool{".docx": true, ".doc": true, ".odt": true, ".rtf": true}

func srcExt(name string) string {
	return strings.ToLower(filepath.Ext(name))
}

// removeProtectedSource unstages the envelope's SOURCE file from Drafts when
// it was uploaded through Sign (protected); files picked from the user's own
// tree are untouched (RemoveSystemFile no-ops on unprotected files).
func (s *Service) removeProtectedSource(ctx context.Context, tenantID, envelopeID int64) {
	if s.deposit == nil {
		return
	}
	var src sql.NullInt64
	_ = s.pg.QueryRowContext(ctx,
		`SELECT source_file_id FROM sign_envelopes WHERE id = $1`, envelopeID).Scan(&src)
	if src.Valid {
		_ = s.deposit.RemoveSystemFile(ctx, tenantID, src.Int64)
	}
}

// snapshotPDF validates a Trilli source file and copies it into the envelope's
// own encrypted blob as a PDF — Word documents (.docx/.doc/.odt/.rtf) are
// converted on the way in. Returns (name, blobPath, size, pageCount).
func (s *Service) snapshotPDF(ctx context.Context, tenantID, fileID int64) (string, string, int64, int, error) {
	var name, blobPath string
	err := s.pg.QueryRowContext(ctx, `
		SELECT name, blob_path FROM files
		 WHERE id = $1 AND tenant_id = $2 AND status = 'active'`, fileID, tenantID,
	).Scan(&name, &blobPath)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", 0, 0, ErrNotFound
	}
	if err != nil {
		return "", "", 0, 0, fmt.Errorf("sign: lookup source: %w", err)
	}
	ext := srcExt(name)
	if ext != ".pdf" && !wordExts[ext] {
		return "", "", 0, 0, ErrNotPDF
	}
	if wordExts[ext] && s.conv == nil {
		return "", "", 0, 0, fmt.Errorf("sign: %w (document conversion unavailable)", ErrNotPDF)
	}
	rc, err := s.store.Get(ctx, blobPath)
	if err != nil {
		return "", "", 0, 0, fmt.Errorf("sign: open source blob: %w", err)
	}
	raw, err := io.ReadAll(rc)
	rc.Close()
	if err != nil {
		return "", "", 0, 0, fmt.Errorf("sign: read source: %w", err)
	}
	if wordExts[ext] {
		converted, err := s.conv.Convert(ctx, bytes.NewReader(raw), strings.TrimPrefix(ext, "."), "pdf")
		if err != nil {
			return "", "", 0, 0, fmt.Errorf("sign: convert %s to pdf: %w", ext, err)
		}
		raw = converted
	}
	conf := model.NewDefaultConfiguration()
	conf.ValidationMode = model.ValidationRelaxed
	pageCount, err := api.PageCount(bytes.NewReader(raw), conf)
	if err != nil || pageCount < 1 {
		return "", "", 0, 0, ErrNotPDF
	}
	put, err := s.store.Put(ctx, tenantID, bytes.NewReader(raw))
	if err != nil {
		return "", "", 0, 0, fmt.Errorf("sign: snapshot: %w", err)
	}
	return name, put.BlobPath, put.Size, pageCount, nil
}

func pdfTitle(name string) string {
	ext := srcExt(name)
	if ext == ".pdf" || wordExts[ext] {
		return name[:len(name)-len(ext)]
	}
	return name
}

func (s *Service) ListEnvelopes(ctx context.Context, tenantID int64) ([]*Envelope, error) {
	// A record only exists once a document is attached: document-less drafts
	// are working state, never listed, and abandoned ones (tab closed before
	// the client could discard) are swept opportunistically.
	_, _ = s.pg.ExecContext(ctx, `
		DELETE FROM sign_envelopes
		 WHERE tenant_id = $1 AND status = 'draft' AND blob_path IS NULL
		   AND updated_at < now() - interval '1 hour'`, tenantID)
	rows, err := s.pg.QueryContext(ctx, `
		SELECT e.id, e.title, e.subject, e.category, e.message, e.status, e.page_count, e.size_bytes,
		       e.created_at, e.updated_at, e.sent_at, e.completed_at
		  FROM sign_envelopes e
		 WHERE e.tenant_id = $1
		   AND NOT (e.status = 'draft' AND e.blob_path IS NULL)
		 ORDER BY e.updated_at DESC`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Envelope{}
	for rows.Next() {
		var e Envelope
		if err := rows.Scan(&e.ID, &e.Title, &e.Subject, &e.Category, &e.Message, &e.Status, &e.PageCount, &e.SizeBytes,
			&e.CreatedAt, &e.UpdatedAt, &e.SentAt, &e.CompletedAt); err != nil {
			return nil, err
		}
		out = append(out, &e)
	}
	// recipient rollup for the dashboard list
	for _, e := range out {
		recs, err := s.recipients(ctx, e.ID)
		if err != nil {
			return nil, err
		}
		e.Recipients = recs
	}
	return out, rows.Err()
}

func (s *Service) GetEnvelope(ctx context.Context, tenantID, id int64) (*Envelope, error) {
	var e Envelope
	err := s.pg.QueryRowContext(ctx, `
		SELECT id, title, subject, category, message, status, page_count, size_bytes, created_at, updated_at, sent_at, completed_at
		  FROM sign_envelopes WHERE id = $1 AND tenant_id = $2`, id, tenantID,
	).Scan(&e.ID, &e.Title, &e.Subject, &e.Category, &e.Message, &e.Status, &e.PageCount, &e.SizeBytes,
		&e.CreatedAt, &e.UpdatedAt, &e.SentAt, &e.CompletedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if e.Recipients, err = s.recipients(ctx, e.ID); err != nil {
		return nil, err
	}
	if e.Fields, err = s.fields(ctx, e.ID); err != nil {
		return nil, err
	}
	return &e, nil
}

func (s *Service) recipients(ctx context.Context, envelopeID int64) ([]*Recipient, error) {
	rows, err := s.pg.QueryContext(ctx, `
		SELECT id, name, email, signing_order, status, signed_at
		  FROM sign_recipients WHERE envelope_id = $1 ORDER BY signing_order, id`, envelopeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Recipient{}
	for rows.Next() {
		var r Recipient
		if err := rows.Scan(&r.ID, &r.Name, &r.Email, &r.SigningOrder, &r.Status, &r.SignedAt); err != nil {
			return nil, err
		}
		out = append(out, &r)
	}
	return out, rows.Err()
}

func (s *Service) fields(ctx context.Context, envelopeID int64) ([]*Field, error) {
	rows, err := s.pg.QueryContext(ctx, `
		SELECT id, recipient_id, kind, page, x, y, w, h, required, meta
		  FROM sign_fields WHERE envelope_id = $1 ORDER BY page, id`, envelopeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Field{}
	for rows.Next() {
		var f Field
		if err := rows.Scan(&f.ID, &f.RecipientID, &f.Kind, &f.Page, &f.X, &f.Y, &f.W, &f.H, &f.Required, &f.Meta); err != nil {
			return nil, err
		}
		out = append(out, &f)
	}
	return out, rows.Err()
}

// requireDraft loads the envelope's status and blob, ensuring editability.
func (s *Service) requireDraft(ctx context.Context, tenantID, id int64) (blobPath string, err error) {
	var status string
	err = s.pg.QueryRowContext(ctx,
		`SELECT status, blob_path FROM sign_envelopes WHERE id = $1 AND tenant_id = $2`, id, tenantID,
	).Scan(&status, &blobPath)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	if status != "draft" {
		return "", ErrNotDraft
	}
	return blobPath, nil
}

func (s *Service) UpdateEnvelope(ctx context.Context, tenantID, id int64, actor string, title, subject, category, message *string) error {
	if _, err := s.requireDraft(ctx, tenantID, id); err != nil {
		return err
	}
	if title != nil {
		t := strings.TrimSpace(*title)
		if t == "" || len(t) > 200 {
			return ErrBadInput
		}
		if _, err := s.pg.ExecContext(ctx,
			`UPDATE sign_envelopes SET title = $1, updated_at = now() WHERE id = $2`, t, id); err != nil {
			return err
		}
	}
	if subject != nil {
		if len(*subject) > 200 {
			return ErrBadInput
		}
		if _, err := s.pg.ExecContext(ctx,
			`UPDATE sign_envelopes SET subject = $1, updated_at = now() WHERE id = $2`, *subject, id); err != nil {
			return err
		}
	}
	if category != nil {
		if len(*category) > 60 {
			return ErrBadInput
		}
		if _, err := s.pg.ExecContext(ctx,
			`UPDATE sign_envelopes SET category = $1, updated_at = now() WHERE id = $2`, *category, id); err != nil {
			return err
		}
	}
	if message != nil {
		if len(*message) > 2000 {
			return ErrBadInput
		}
		if _, err := s.pg.ExecContext(ctx,
			`UPDATE sign_envelopes SET message = $1, updated_at = now() WHERE id = $2`, *message, id); err != nil {
			return err
		}
	}
	s.event(ctx, id, actor, "updated", "")
	return nil
}

// DeleteEnvelope removes a draft entirely (snapshot included); a sent envelope
// is voided instead, preserving the record and its event trail.
func (s *Service) DeleteEnvelope(ctx context.Context, tenantID, id int64, actor string) error {
	var status string
	var blobPath sql.NullString // NULL for a document-less blank draft
	err := s.pg.QueryRowContext(ctx,
		`SELECT status, blob_path FROM sign_envelopes WHERE id = $1 AND tenant_id = $2`, id, tenantID,
	).Scan(&status, &blobPath)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if status == "draft" {
		s.removeProtectedSource(ctx, tenantID, id)
		s.removeProtectedSource(ctx, tenantID, id)
		if _, err := s.pg.ExecContext(ctx, `DELETE FROM sign_envelopes WHERE id = $1`, id); err != nil {
			return err
		}
		if blobPath.Valid && blobPath.String != "" {
			_ = s.store.Delete(ctx, blobPath.String)
		}
		s.dropPageCache(id)
		return nil
	}
	if status == "completed" || status == "voided" || status == "declined" {
		// Closed envelopes delete outright — the ONLY path that removes the
		// protected signed copy from Files, so file + metadata stay coupled.
		var depositID, execBlob, sealBlob sql.NullString
		_ = s.pg.QueryRowContext(ctx, `
			SELECT deposited_file_id::text, executed_blob, sealed_blob
			  FROM sign_envelopes WHERE id = $1`, id).Scan(&depositID, &execBlob, &sealBlob)
		if s.deposit != nil && depositID.Valid && depositID.String != "" {
			if fid, err := strconv.ParseInt(depositID.String, 10, 64); err == nil {
				if err := s.deposit.RemoveSystemFile(ctx, tenantID, fid); err != nil {
					logging.Error(packageName, "remove deposit for envelope %d: %v", id, err)
				}
			}
		}
		s.removeProtectedSource(ctx, tenantID, id)
		if _, err := s.pg.ExecContext(ctx, `DELETE FROM sign_envelopes WHERE id = $1`, id); err != nil {
			return err
		}
		if blobPath.Valid && blobPath.String != "" {
			_ = s.store.Delete(ctx, blobPath.String)
		}
		for _, b := range []sql.NullString{execBlob, sealBlob} {
			if b.Valid && b.String != "" {
				_ = s.store.Delete(ctx, b.String)
			}
		}
		s.dropPageCache(id)
		return nil
	}
	if _, err := s.pg.ExecContext(ctx,
		`UPDATE sign_envelopes SET status = 'voided', updated_at = now() WHERE id = $1`, id); err != nil {
		return err
	}
	s.event(ctx, id, actor, "voided", "")

	// Notify every recipient who had a signing link that the agreement is void.
	if s.mail != nil {
		var title string
		_ = s.pg.QueryRowContext(ctx, `SELECT title FROM sign_envelopes WHERE id = $1`, id).Scan(&title)
		rows, err := s.pg.QueryContext(ctx,
			`SELECT name, email FROM sign_recipients WHERE envelope_id = $1 AND status <> 'declined'`, id)
		if err == nil {
			type party struct{ name, email string }
			var parts []party
			for rows.Next() {
				var p party
				if rows.Scan(&p.name, &p.email) == nil {
					parts = append(parts, p)
				}
			}
			rows.Close()
			for _, p := range parts {
				in := mailer.SignVoidedEmail{To: p.email, Name: p.name, Title: title, Sender: actor}
				if err := s.mail.SendSignVoided(ctx, in); err != nil {
					logging.Error(packageName, "void notice to %s: %v", p.email, err)
				}
			}
		}
	}
	return nil
}

// ----- recipients -------------------------------------------------------------

func (s *Service) AddRecipient(ctx context.Context, tenantID, envelopeID int64, actor, name, email string, order int) (*Recipient, error) {
	if _, err := s.requireDraft(ctx, tenantID, envelopeID); err != nil {
		return nil, err
	}
	name = strings.TrimSpace(name)
	email = strings.ToLower(strings.TrimSpace(email))
	if name == "" || len(name) > 120 || !strings.Contains(email, "@") || len(email) > 254 {
		return nil, ErrBadInput
	}
	if order < 1 {
		order = 1
	}
	token, err := randToken()
	if err != nil {
		return nil, err
	}
	var r Recipient
	err = s.pg.QueryRowContext(ctx, `
		INSERT INTO sign_recipients (envelope_id, name, email, signing_order, token)
		VALUES ($1,$2,$3,$4,$5)
		RETURNING id, name, email, signing_order, status, signed_at`,
		envelopeID, name, email, order, token,
	).Scan(&r.ID, &r.Name, &r.Email, &r.SigningOrder, &r.Status, &r.SignedAt)
	if err != nil {
		return nil, err
	}
	s.touch(ctx, envelopeID)
	s.event(ctx, envelopeID, actor, "recipient_added", email)
	return &r, nil
}

func (s *Service) DeleteRecipient(ctx context.Context, tenantID, envelopeID, recipientID int64, actor string) error {
	if _, err := s.requireDraft(ctx, tenantID, envelopeID); err != nil {
		return err
	}
	res, err := s.pg.ExecContext(ctx,
		`DELETE FROM sign_recipients WHERE id = $1 AND envelope_id = $2`, recipientID, envelopeID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	s.touch(ctx, envelopeID)
	s.event(ctx, envelopeID, actor, "recipient_removed", "")
	return nil
}

// ----- fields -----------------------------------------------------------------

func (s *Service) AddField(ctx context.Context, tenantID, envelopeID int64, f Field) (*Field, error) {
	if _, err := s.requireDraft(ctx, tenantID, envelopeID); err != nil {
		return nil, err
	}
	if !fieldKinds[f.Kind] || f.Page < 1 || !normOK(f.X) || !normOK(f.Y) || f.W <= 0 || f.W > 1 || f.H <= 0 || f.H > 1 {
		return nil, ErrBadInput
	}
	// the recipient must belong to this envelope
	var n int
	if err := s.pg.QueryRowContext(ctx,
		`SELECT count(*) FROM sign_recipients WHERE id = $1 AND envelope_id = $2`,
		f.RecipientID, envelopeID).Scan(&n); err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, ErrBadInput
	}
	meta := normalizeMeta(f.Meta)
	err := s.pg.QueryRowContext(ctx, `
		INSERT INTO sign_fields (envelope_id, recipient_id, kind, page, x, y, w, h, meta)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING id`,
		envelopeID, f.RecipientID, f.Kind, f.Page, f.X, f.Y, f.W, f.H, string(meta),
	).Scan(&f.ID)
	f.Meta = meta
	if err != nil {
		return nil, err
	}
	f.Required = true
	s.touch(ctx, envelopeID)
	return &f, nil
}

// normalizeMeta caps field meta to a small allow-listed config object.
func normalizeMeta(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 || len(raw) > 4096 {
		return json.RawMessage(`{}`)
	}
	var in map[string]any
	if json.Unmarshal(raw, &in) != nil {
		return json.RawMessage(`{}`)
	}
	out := map[string]any{}
	if v, ok := in["options"].([]any); ok && len(v) <= 50 {
		opts := []string{}
		for _, o := range v {
			if sv, ok := o.(string); ok && strings.TrimSpace(sv) != "" && len(sv) <= 100 {
				opts = append(opts, strings.TrimSpace(sv))
			}
		}
		if len(opts) > 0 {
			out["options"] = opts
		}
	}
	if v, ok := in["group"].(string); ok && strings.TrimSpace(v) != "" && len(v) <= 60 {
		out["group"] = strings.TrimSpace(v)
	}
	if v, ok := in["formula"].(string); ok && strings.TrimSpace(v) != "" && len(v) <= 200 {
		out["formula"] = strings.TrimSpace(v)
	}
	b, err := json.Marshal(out)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return b
}

func (s *Service) UpdateField(ctx context.Context, tenantID, envelopeID, fieldID int64, x, y, w, h *float64, page *int, required *bool, recipientID *int64, meta json.RawMessage) error {
	if _, err := s.requireDraft(ctx, tenantID, envelopeID); err != nil {
		return err
	}
	set := []string{}
	args := []any{}
	add := func(col string, v any) {
		args = append(args, v)
		set = append(set, fmt.Sprintf("%s = $%d", col, len(args)))
	}
	if x != nil && normOK(*x) {
		add("x", *x)
	}
	if y != nil && normOK(*y) {
		add("y", *y)
	}
	if w != nil && *w > 0 && *w <= 1 {
		add("w", *w)
	}
	if h != nil && *h > 0 && *h <= 1 {
		add("h", *h)
	}
	if page != nil && *page >= 1 {
		add("page", *page)
	}
	if required != nil {
		add("required", *required)
	}
	if meta != nil {
		add("meta", string(normalizeMeta(meta)))
	}
	if recipientID != nil {
		// reassignment target must belong to this envelope
		var n int
		if err := s.pg.QueryRowContext(ctx,
			`SELECT count(*) FROM sign_recipients WHERE id = $1 AND envelope_id = $2`,
			*recipientID, envelopeID).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			return ErrBadInput
		}
		add("recipient_id", *recipientID)
	}
	if len(set) == 0 {
		return nil
	}
	args = append(args, fieldID, envelopeID)
	q := fmt.Sprintf(`UPDATE sign_fields SET %s WHERE id = $%d AND envelope_id = $%d`,
		strings.Join(set, ", "), len(args)-1, len(args))
	res, err := s.pg.ExecContext(ctx, q, args...)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Service) DeleteField(ctx context.Context, tenantID, envelopeID, fieldID int64) error {
	if _, err := s.requireDraft(ctx, tenantID, envelopeID); err != nil {
		return err
	}
	res, err := s.pg.ExecContext(ctx,
		`DELETE FROM sign_fields WHERE id = $1 AND envelope_id = $2`, fieldID, envelopeID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	s.touch(ctx, envelopeID)
	return nil
}

func normOK(v float64) bool { return v >= 0 && v <= 1 }

func (s *Service) touch(ctx context.Context, envelopeID int64) {
	_, _ = s.pg.ExecContext(ctx, `UPDATE sign_envelopes SET updated_at = now() WHERE id = $1`, envelopeID)
}

// ----- page rendering (editor) --------------------------------------------------

const renderDPI = 110

// RenderPage returns the PNG of a 1-based page for the field-placement editor.
// The whole document renders once and is cached, since pdfium's cost is per
// document open, and the editor requests pages one by one.
func (s *Service) RenderPage(ctx context.Context, tenantID, id int64, page int) ([]byte, error) {
	var blobPath string
	var pageCount int
	var sealed, executed sql.NullString
	err := s.pg.QueryRowContext(ctx,
		`SELECT blob_path, page_count, sealed_blob, executed_blob
		   FROM sign_envelopes WHERE id = $1 AND tenant_id = $2`, id, tenantID,
	).Scan(&blobPath, &pageCount, &sealed, &executed)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	// Completed envelopes preview the EXECUTED document (signatures + audit
	// certificate), not the blank template snapshot.
	if sealed.Valid && sealed.String != "" {
		blobPath = sealed.String
	} else if executed.Valid && executed.String != "" {
		blobPath = executed.String
	}
	if page < 1 || page > pageCount {
		return nil, ErrNotFound
	}

	s.mu.Lock()
	cached, ok := s.pages[id]
	s.mu.Unlock()
	if ok && page <= len(cached) {
		return cached[page-1], nil
	}

	rc, err := s.store.Get(ctx, blobPath)
	if err != nil {
		return nil, err
	}
	raw, err := io.ReadAll(rc)
	rc.Close()
	if err != nil {
		return nil, err
	}
	imgs, err := pdftools.RenderPages(raw, renderDPI)
	if err != nil {
		return nil, fmt.Errorf("sign: render: %w", err)
	}
	encoded := make([][]byte, len(imgs))
	for i, im := range imgs {
		var buf bytes.Buffer
		if err := png.Encode(&buf, im); err != nil {
			return nil, err
		}
		encoded[i] = buf.Bytes()
	}

	s.mu.Lock()
	if len(s.pages) >= 6 { // tiny cache: evict arbitrary entry
		for k := range s.pages {
			delete(s.pages, k)
			break
		}
	}
	s.pages[id] = encoded
	s.mu.Unlock()

	if page > len(encoded) {
		return nil, ErrNotFound
	}
	return encoded[page-1], nil
}

func (s *Service) dropPageCache(id int64) {
	s.mu.Lock()
	delete(s.pages, id)
	s.mu.Unlock()
}

// ----- send --------------------------------------------------------------------

// Send transitions a draft to sent: every recipient gets a unique ceremony
// link by email, and the event trail records the dispatch. Validation: at
// least one recipient, and every recipient has at least one field.
func (s *Service) Send(ctx context.Context, tenantID, id int64, actorName, actorEmail, baseURL string) error {
	if _, err := s.requireDraft(ctx, tenantID, id); err != nil {
		return err
	}
	var e Envelope
	if err := s.pg.QueryRowContext(ctx,
		`SELECT title, subject, message FROM sign_envelopes WHERE id = $1`, id,
	).Scan(&e.Title, &e.Subject, &e.Message); err != nil {
		return err
	}
	recs, err := s.recipients(ctx, id)
	if err != nil {
		return err
	}
	if len(recs) == 0 {
		return ErrNoRecipients
	}
	flds, err := s.fields(ctx, id)
	if err != nil {
		return err
	}
	perRecipient := map[int64]int{}
	for _, f := range flds {
		perRecipient[f.RecipientID]++
	}
	for _, r := range recs {
		if perRecipient[r.ID] == 0 {
			return ErrEmptySigner
		}
	}

	if _, err := s.pg.ExecContext(ctx, `
		UPDATE sign_envelopes SET status = 'sent', sent_at = now(), updated_at = now()
		 WHERE id = $1 AND status = 'draft'`, id); err != nil {
		return err
	}
	if _, err := s.pg.ExecContext(ctx, `
		UPDATE sign_recipients SET status = 'notified', notified_at = now()
		 WHERE envelope_id = $1 AND status = 'pending'`, id); err != nil {
		return err
	}
	s.event(ctx, id, actorEmail, "sent", fmt.Sprintf("%d recipient(s)", len(recs)))

	// Email dispatch is best-effort AFTER the state change: a bounced email
	// doesn't unsend the envelope; the link can be re-shared from the app.
	rows, err := s.pg.QueryContext(ctx,
		`SELECT name, email, token FROM sign_recipients WHERE envelope_id = $1`, id)
	if err != nil {
		return nil //nolint:nilerr — envelope is sent; email dispatch failure is logged
	}
	defer rows.Close()
	for rows.Next() {
		var name, email, token string
		if err := rows.Scan(&name, &email, &token); err != nil {
			continue
		}
		in := mailer.SignRequestEmail{
			To: email, RecipientName: name,
			SenderName: actorName, SenderEmail: actorEmail,
			Title: e.Title, Subject: e.Subject, Message: e.Message,
			Link: strings.TrimRight(baseURL, "/") + "/sign/" + token,
		}
		if err := s.mail.SendSignRequest(ctx, in); err != nil {
			logging.Error(packageName, "send email to %s: %v", email, err)
			continue
		}
		s.event(ctx, id, "system", "notified", email)
	}
	return nil
}

// Resend re-emails the signing link to every recipient who hasn't finished
// (not signed, not declined) on a SENT envelope — for "I never got it" cases.
func (s *Service) Resend(ctx context.Context, tenantID, id int64, actorName, actorEmail, baseURL string) (int, error) {
	var status, title, subject, message string
	err := s.pg.QueryRowContext(ctx,
		`SELECT status, title, subject, message FROM sign_envelopes WHERE id = $1 AND tenant_id = $2`,
		id, tenantID).Scan(&status, &title, &subject, &message)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	if err != nil {
		return 0, err
	}
	if status != "sent" {
		return 0, ErrNotDraft // only in-flight envelopes can be resent
	}
	rows, err := s.pg.QueryContext(ctx, `
		SELECT name, email, token FROM sign_recipients
		 WHERE envelope_id = $1 AND status NOT IN ('signed', 'declined')`, id)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	sent := 0
	for rows.Next() {
		var name, email, token string
		if err := rows.Scan(&name, &email, &token); err != nil {
			continue
		}
		in := mailer.SignRequestEmail{
			To: email, RecipientName: name,
			SenderName: actorName, SenderEmail: actorEmail,
			Title: title, Subject: subject, Message: message,
			Link: strings.TrimRight(baseURL, "/") + "/sign/" + token,
		}
		if err := s.mail.SendSignRequest(ctx, in); err != nil {
			logging.Error(packageName, "resend email to %s: %v", email, err)
			continue
		}
		sent++
		s.event(ctx, id, actorEmail, "notified", "resent to "+email)
	}
	return sent, nil
}

// Events returns the envelope's append-only trail (newest last).
func (s *Service) Events(ctx context.Context, tenantID, id int64) ([]*Event, error) {
	if _, err := s.GetEnvelope(ctx, tenantID, id); err != nil {
		return nil, err
	}
	rows, err := s.pg.QueryContext(ctx, `
		SELECT actor, action, detail, created_at FROM sign_events
		 WHERE envelope_id = $1 ORDER BY created_at, id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Event{}
	for rows.Next() {
		var ev Event
		if err := rows.Scan(&ev.Actor, &ev.Action, &ev.Detail, &ev.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, &ev)
	}
	return out, rows.Err()
}

func nullStr(v string) any {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	return v
}
