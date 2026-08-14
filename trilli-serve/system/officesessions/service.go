// Package officesessions manages the Trilli Productivity editor's per-session
// working files.
//
// Phase 2 moved the editor's live document OUT of a single shared file on the
// app server's local disk (which crossed tenants, never cleaned up, and couldn't
// scale) and INTO a hidden, per-tenant, per-session area of the tenant's OWN
// encrypted blob store. The working blob lives at:
//
//	tenants/{tenantID}/.office/work/{sessionKey}/{app}.{nativeExt}
//
// The leading ".office" segment keeps it disjoint from real user content
// (workspace/folder names can't begin with a dot in our paths), so a working
// blob can never collide with — or be mistaken for — a user file. It has NO
// files row, never counts against quota, and never appears in the file
// browser. It is pure scratch: the engine edits it, Save As commits it into a
// real Trilli file, and a janitor deletes it once the session expires.
//
// This package owns:
//   - the office_sessions DB registry (one row per live edit session),
//   - the working-blob CRUD (PutAt/Get/Delete in the tenant's .office prefix,
//     transparently encrypted by the blob store's encryption decorator),
//   - session lifecycle: Create (mint a session + seed the working blob from a
//     Trilli file or a blank template), Touch (renew the idle TTL on engine
//     activity), End (delete the working blob + row), and the Janitor sweep
//     that reaps sessions past their hard TTL — catching browser closes and
//     crashes that a purely client-driven cleanup can't.
//
// trilli-serve holds the per-tenant decryption key; the Collabora engine NEVER
// touches the encrypted bytes — it fetches plaintext from / commits plaintext
// to the WOPI endpoints on trilli-serve (loopback only).
package officesessions

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"trilli/system/database/postgres"
	"trilli/system/logging"
	"trilli/system/storage"
)

const packageName = "officesessions"

// Sentinel errors.
var (
	ErrNotFound   = errors.New("officesessions: session not found")
	ErrUnknownApp = errors.New("officesessions: unknown app")
	ErrTemplate   = errors.New("officesessions: blank template missing")
)

// SessionTTL is how long an edit session stays live after its last engine
// activity. WOPI Get/Put calls Touch(), renewing it. A browser close / crash
// stops touching; the janitor reaps the session (and deletes its working blob)
// once this elapses. Long enough for a thoughtful edit, short enough that
// abandoned scratch doesn't accumulate.
const SessionTTL = 4 * time.Hour

// workPrefix is the reserved, hidden segment under a tenant's blob namespace
// where per-session working files live. The leading dot keeps it disjoint from
// real user content (same convention as .previews / .thumbs).
const workPrefix = ".office/work"

// AppDef describes one Productivity app's native document: the blank template
// (seeded into the working blob on New) + the file extension the engine edits.
type AppDef struct {
	Key       string // "docs" | "sheets" | "slides"
	NativeExt string // "docx" | "xlsx" | "pptx" (no dot)
	Template  string // on-disk pristine blank template, copied into the working blob on New
}

// templatesDir is where the pristine blank documents live. Override with
// TRILLI_OFFICE_TEMPLATES; defaults to templates/office under the working
// directory.
var templatesDir = func() string {
	if v := os.Getenv("TRILLI_OFFICE_TEMPLATES"); v != "" {
		return v
	}
	return "templates/office"
}()

// Apps is the registry the handlers + WOPI endpoint share. Keys match the
// app keys used by the React suite (docs/sheets/slides) and the file-extension
// mapping in files-meta. Templates are read once from disk on first use.
var Apps = map[string]AppDef{
	"docs":   {Key: "docs", NativeExt: "docx", Template: filepath.Join(templatesDir, "word.docx")},
	"sheets": {Key: "sheets", NativeExt: "xlsx", Template: filepath.Join(templatesDir, "excel.xlsx")},
	"slides": {Key: "slides", NativeExt: "pptx", Template: filepath.Join(templatesDir, "impress.pptx")},
}

// Session is one live edit session's registry row.
type Session struct {
	SessionKey   string
	TenantID     int64
	UserID       int64
	App          string
	SourceFileID *int64
	WorkingPath  string
	FileName     string
	SizeBytes    int64
	PutSeq       int64 // monotonic; +1 on every WOPI PutFile — the flush signal the save handshake waits on
	CreatedAt    time.Time
	LastSeenAt   time.Time
	ExpiresAt    time.Time
}

// Service manages office edit sessions and their working blobs.
type Service struct {
	client *postgres.Client
	store  storage.KeyedStore // the encrypted blob store (per-tenant DEK applied transparently)
}

// NewService wires the session service to the DB + the (encryption-wrapped) blob
// store. store MUST be the same store the rest of the app uses so working blobs
// inherit per-tenant encryption at rest.
func NewService(c *postgres.Client, store storage.KeyedStore) *Service {
	return &Service{client: c, store: store}
}

// CreateInput seeds a new session's working blob.
type CreateInput struct {
	TenantID int64
	UserID   int64
	App      string
	FileName string  // display name (Save As default + download filename)
	Source   *Source // when set, seed the working blob from this Trilli file; when nil, seed from the blank template
}

// Source seeds a working blob from an existing Trilli file (Open / Edit). The
// caller resolves the decrypted bytes (files.Service.Download) and hands them
// over as a reader — trilli-serve holds the key, the session service just
// stores the (re-encrypted) bytes into the working blob.
type Source struct {
	FileID int64
	Reader io.ReadCloser // plaintext bytes of the Trilli file, already access-checked + decrypted by the caller
}

// Create mints a new edit session: allocates a session key + working-blob path,
// seeds the working blob (from a Trilli file on Open/Edit, or the blank
// template on New), and inserts the registry row. Returns the session (its key
// is the only credential the engine sees via WOPISrc). The caller owns closing
// Source.Reader.
func (s *Service) Create(ctx context.Context, in CreateInput) (*Session, error) {
	app, ok := Apps[in.App]
	if !ok {
		return nil, ErrUnknownApp
	}
	if in.FileName == "" {
		// Per-app default name for a brand-new file. This is the AUTHORITATIVE
		// name returned on New (it overrides the client's placeholder), so the
		// app-specific wording must live here.
		switch in.App {
		case "sheets":
			in.FileName = "New spreadsheet"
		case "slides":
			in.FileName = "New presentation"
		default:
			in.FileName = "New document"
		}
	}

	key, err := newSessionKey()
	if err != nil {
		return nil, err
	}
	workingPath := workPath(in.TenantID, key, app.NativeExt)

	// Seed the working blob.
	var seededSize int64
	if in.Source != nil {
		// Open/Edit: store the (decrypted-by-caller) Trilli file's bytes. The
		// blob store re-encrypts them with the tenant DEK. Count the plaintext
		// bytes so CheckFileInfo can report an accurate Size (the engine skips
		// GetFile when Size is 0 → blank canvas).
		counter := &countingReader{r: in.Source.Reader}
		if _, err := s.store.PutAt(ctx, workingPath, counter); err != nil {
			return nil, fmt.Errorf("officesessions: seed working blob from file: %w", err)
		}
		seededSize = counter.n
	} else {
		// New: seed from the blank template.
		n, err := s.seedFromTemplate(ctx, workingPath, app)
		if err != nil {
			return nil, err
		}
		seededSize = n
	}

	now := time.Now().UTC()
	sess := &Session{
		SessionKey:  key,
		TenantID:    in.TenantID,
		UserID:      in.UserID,
		App:         in.App,
		WorkingPath: workingPath,
		FileName:    in.FileName,
		SizeBytes:   seededSize,
		CreatedAt:   now,
		LastSeenAt:  now,
		ExpiresAt:   now.Add(SessionTTL),
	}
	if in.Source != nil {
		fid := in.Source.FileID
		sess.SourceFileID = &fid
	}

	if err := s.insert(ctx, sess); err != nil {
		// Best-effort: don't leak the just-written working blob.
		_ = s.store.Delete(ctx, workingPath)
		return nil, err
	}
	logging.Info(packageName, "Create: tenant=%d user=%d app=%s key=%s sourceFile=%v",
		in.TenantID, in.UserID, in.App, key, sess.SourceFileID)
	return sess, nil
}

// seedFromTemplate copies the app's blank template into the working blob.
func (s *Service) seedFromTemplate(ctx context.Context, workingPath string, app AppDef) (int64, error) {
	f, err := os.Open(app.Template)
	if err != nil {
		return 0, fmt.Errorf("%w: %s: %v", ErrTemplate, app.Key, err)
	}
	defer f.Close()
	counter := &countingReader{r: f}
	if _, err := s.store.PutAt(ctx, workingPath, counter); err != nil {
		return 0, fmt.Errorf("officesessions: seed working blob from template: %w", err)
	}
	return counter.n, nil
}

// Get loads a session by its key. Used by the WOPI handler to serve
// CheckFileInfo / GetFile / PutFile for the engine.
func (s *Service) Get(ctx context.Context, key string) (*Session, error) {
	var sess Session
	var src sql.NullInt64
	err := s.client.QueryRowContext(ctx, `
		SELECT session_key, tenant_id, user_id, app, source_file_id,
		       working_path, file_name, size_bytes, put_seq, created_at, last_seen_at, expires_at
		  FROM office_sessions
		 WHERE session_key = $1`, key,
	).Scan(&sess.SessionKey, &sess.TenantID, &sess.UserID, &sess.App, &src,
		&sess.WorkingPath, &sess.FileName, &sess.SizeBytes, &sess.PutSeq, &sess.CreatedAt, &sess.LastSeenAt, &sess.ExpiresAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("officesessions: get: %w", err)
	}
	if src.Valid {
		v := src.Int64
		sess.SourceFileID = &v
	}
	return &sess, nil
}

// GetActiveUserApp returns the user's most recent non-expired session for an
// app, or ErrNotFound if none. Used by the editor to RESUME an existing
// session on refresh instead of creating a new one (which would seed from the
// blank template and discard the auto-saved working blob — losing the user's
// edits + inserted images). The engine auto-saves to the working blob every
// ~30s via WOPI PutFile, so on refresh the working blob holds the latest state.
func (s *Service) GetActiveUserApp(ctx context.Context, userID int64, app string) (*Session, error) {
	var sess Session
	var src sql.NullInt64
	err := s.client.QueryRowContext(ctx, `
		SELECT session_key, tenant_id, user_id, app, source_file_id,
		       working_path, file_name, size_bytes, put_seq, created_at, last_seen_at, expires_at
		  FROM office_sessions
		 WHERE user_id = $1 AND app = $2 AND expires_at > NOW()
		 ORDER BY last_seen_at DESC
		 LIMIT 1`, userID, app,
	).Scan(&sess.SessionKey, &sess.TenantID, &sess.UserID, &sess.App, &src,
		&sess.WorkingPath, &sess.FileName, &sess.SizeBytes, &sess.PutSeq, &sess.CreatedAt, &sess.LastSeenAt, &sess.ExpiresAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("officesessions: get active user+app: %w", err)
	}
	if src.Valid {
		v := src.Int64
		sess.SourceFileID = &v
	}
	return &sess, nil
}

// Touch renews a session's idle TTL (called on every WOPI Get/Put) and records
// the current display name if it changed. Best-effort: a failure here MUST NOT
// fail the engine's WOPI request — the session is still valid until its hard
// expires_at, and the janitor will reap it eventually if updates stop.
func (s *Service) Touch(ctx context.Context, key, fileName string) {
	_, _ = s.client.ExecContext(ctx, `
		UPDATE office_sessions
		   SET last_seen_at = NOW(), expires_at = NOW() + make_interval(secs => $2),
		       file_name = COALESCE(NULLIF($3,''), file_name)
		 WHERE session_key = $1`,
		key, int(SessionTTL.Seconds()), fileName)
}

// PutWorking writes the engine's latest bytes to the working blob (WOPI
// PutFile). The blob store re-encrypts transparently.
func (s *Service) PutWorking(ctx context.Context, sess *Session, r io.Reader) (int64, error) {
	n, err := s.store.PutAt(ctx, sess.WorkingPath, r)
	if err != nil {
		return 0, fmt.Errorf("officesessions: put working blob: %w", err)
	}
	// Keep the row's size in sync so the next CheckFileInfo reports an accurate
	// Size (the engine SKIPS GetFile when Size is 0 → blank canvas). The
	// encrypted-store byte count is slightly larger than plaintext, which is
	// fine — the engine only needs Size > 0 to fetch contents.
	//
	// Bump put_seq in the same statement: it's the flush signal the save
	// handshake polls on. Watching size_bytes alone missed a flush whenever the
	// re-saved document had the same byte length (common for small edits, since
	// the ciphertext length tracks the plaintext length) — put_seq advances on
	// EVERY PutFile, so the flush is always observable. RETURNING keeps the
	// in-memory session's counter authoritative across nodes.
	sess.SizeBytes = n
	if err := s.client.QueryRowContext(ctx,
		`UPDATE office_sessions SET size_bytes = $2, put_seq = put_seq + 1 WHERE session_key = $1 RETURNING put_seq`,
		sess.SessionKey, n,
	).Scan(&sess.PutSeq); err != nil {
		// Non-fatal: the blob is written; a stale counter only costs the save
		// handshake a fallback wait, never correctness.
		logging.Error(packageName, "PutWorking: bump put_seq: %v", err)
	}
	return n, nil
}

// GetWorking opens the working blob for reading (WOPI GetFile). The blob store
// decrypts transparently. Caller must Close the reader.
func (s *Service) GetWorking(ctx context.Context, sess *Session) (io.ReadCloser, error) {
	rc, err := s.store.Get(ctx, sess.WorkingPath)
	if err != nil {
		return nil, err
	}
	return rc, nil
}

// PutAsset stores a transient asset (an image the host picked for Insert
// Image) in the session's hidden working area and returns the blob path. The
// engine fetches assets server-side by HTTP URL (LOKit's insertfile can't read
// a data: URL), so the host uploads the image here, gets back a fetchable URL,
// and hands that URL to Action_InsertGraphic. Assets are swept with the session
// (End/janitor delete the whole session prefix implicitly via the row's
// working_path — assets live alongside it under the same .office/work/{key}/).
func (s *Service) PutAsset(ctx context.Context, sess *Session, r io.Reader) (string, error) {
	assetID, err := newSessionKey() // reuse the crypto-random generator for an unguessable asset id
	if err != nil {
		return "", err
	}
	path := fmt.Sprintf("tenants/%d/%s/%s/assets/%s", sess.TenantID, workPrefix, sess.SessionKey, assetID)
	if _, err := s.store.PutAt(ctx, path, r); err != nil {
		return "", fmt.Errorf("officesessions: put asset: %w", err)
	}
	return path, nil
}

// GetAsset opens a session asset for reading (the engine's server-side fetch).
// Caller must Close the reader.
func (s *Service) GetAsset(ctx context.Context, sess *Session, assetPath string) (io.ReadCloser, error) {
	// Guard: the asset must live under THIS session's working prefix, so one
	// session's key can't address another's assets.
	expectedPrefix := fmt.Sprintf("tenants/%d/%s/%s/assets/", sess.TenantID, workPrefix, sess.SessionKey)
	if !strings.HasPrefix(assetPath, expectedPrefix) {
		return nil, fmt.Errorf("officesessions: asset path outside session scope")
	}
	return s.store.Get(ctx, assetPath)
}

// End deletes a session's working blob + registry row (on New/Open replacing
// it, or on explicit end). Idempotent — safe to call for a session that's
// already gone. The working blob is deleted first; if that fails we still
// remove the row so the janitor doesn't keep a dangling session, and the blob
// becomes an orphan the janitor's prefix-sweep (Phase 3) will catch.
func (s *Service) End(ctx context.Context, key string) error {
	sess, err := s.Get(ctx, key)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil // already gone — idempotent
		}
		return err
	}
	if err := s.store.Delete(ctx, sess.WorkingPath); err != nil {
		logging.Error(packageName, "End: delete working blob %s: %v", sess.WorkingPath, err)
	}
	_, err = s.client.ExecContext(ctx, `DELETE FROM office_sessions WHERE session_key = $1`, key)
	if err != nil {
		return fmt.Errorf("officesessions: end: delete row: %w", err)
	}
	logging.Info(packageName, "End: key=%s tenant=%d app=%s", key, sess.TenantID, sess.App)
	return nil
}

// EndUserApp ends the user's active session for an app (when they start a New
// doc or open a different file in the same app — the prior session is
// discarded so only one working file per user+app lives at a time). Returns the
// count ended.
func (s *Service) EndUserApp(ctx context.Context, userID int64, app string) (int, error) {
	rows, err := s.client.QueryContext(ctx,
		`SELECT session_key, working_path FROM office_sessions WHERE user_id = $1 AND app = $2`, userID, app)
	if err != nil {
		return 0, fmt.Errorf("officesessions: end user+app scan: %w", err)
	}
	type row struct{ key, path string }
	var rs []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.key, &r.path); err != nil {
			rows.Close()
			return 0, err
		}
		rs = append(rs, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	n := 0
	for _, r := range rs {
		// Collaborative guard: if OTHER participants still hold tokens on this
		// session, the document must survive — only the leaver's token goes.
		// The janitor reaps the session at TTL once everyone is gone.
		if s.OtherParticipants(ctx, r.key, userID) > 0 {
			s.LeaveSession(ctx, r.key, userID)
			continue
		}
		if err := s.store.Delete(ctx, r.path); err != nil {
			logging.Error(packageName, "EndUserApp: delete %s: %v", r.path, err)
		}
		if _, err := s.client.ExecContext(ctx, `DELETE FROM office_sessions WHERE session_key = $1`, r.key); err != nil {
			logging.Error(packageName, "EndUserApp: delete row %s: %v", r.key, err)
			continue
		}
		n++
	}
	return n, nil
}

// EndAllForUser ends ALL of a user's office sessions (every app) — used on
// logout so the scratch area is wiped and the next login starts fresh, not
// resuming a stale working blob the user abandoned without saving. Returns the
// count ended.
func (s *Service) EndAllForUser(ctx context.Context, userID int64) (int, error) {
	rows, err := s.client.QueryContext(ctx,
		`SELECT session_key, working_path FROM office_sessions WHERE user_id = $1`, userID)
	if err != nil {
		return 0, fmt.Errorf("officesessions: end all for user scan: %w", err)
	}
	type row struct{ key, path string }
	var rs []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.key, &r.path); err != nil {
			rows.Close()
			return 0, err
		}
		rs = append(rs, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	n := 0
	for _, r := range rs {
		if err := s.store.Delete(ctx, r.path); err != nil {
			logging.Error(packageName, "EndAllForUser: delete %s: %v", r.path, err)
		}
		if _, err := s.client.ExecContext(ctx, `DELETE FROM office_sessions WHERE session_key = $1`, r.key); err != nil {
			logging.Error(packageName, "EndAllForUser: delete row %s: %v", r.key, err)
			continue
		}
		n++
	}
	logging.Info(packageName, "EndAllForUser: user=%d ended=%d", userID, n)
	return n, nil
}

// oldest first. limit caps the batch.
func (s *Service) Expired(ctx context.Context, limit int) ([]*Session, error) {
	rows, err := s.client.QueryContext(ctx, `
		SELECT session_key, tenant_id, user_id, app, source_file_id,
		       working_path, file_name, size_bytes, put_seq, created_at, last_seen_at, expires_at
		  FROM office_sessions
		 WHERE expires_at < NOW()
		 ORDER BY expires_at ASC
		 LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("officesessions: expired scan: %w", err)
	}
	defer rows.Close()
	var out []*Session
	for rows.Next() {
		var sess Session
		var src sql.NullInt64
		if err := rows.Scan(&sess.SessionKey, &sess.TenantID, &sess.UserID, &sess.App, &src,
			&sess.WorkingPath, &sess.FileName, &sess.SizeBytes, &sess.PutSeq, &sess.CreatedAt, &sess.LastSeenAt, &sess.ExpiresAt); err != nil {
			return nil, err
		}
		if src.Valid {
			v := src.Int64
			sess.SourceFileID = &v
		}
		out = append(out, &sess)
	}
	return out, rows.Err()
}

// Sweep reaps one batch of expired sessions + deletes their working blobs. The
// coordinated janitor calls this on its interval. Returns the count reaped.
func (s *Service) Sweep(ctx context.Context, batch int) (int, error) {
	expired, err := s.Expired(ctx, batch)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, sess := range expired {
		if err := s.store.Delete(ctx, sess.WorkingPath); err != nil {
			logging.Error(packageName, "Sweep: delete %s: %v", sess.WorkingPath, err)
		}
		if _, err := s.client.ExecContext(ctx, `DELETE FROM office_sessions WHERE session_key = $1`, sess.SessionKey); err != nil {
			logging.Error(packageName, "Sweep: delete row %s: %v", sess.SessionKey, err)
			continue
		}
		n++
		logging.Info(packageName, "Sweep: reaped key=%s tenant=%d app=%s", sess.SessionKey, sess.TenantID, sess.App)
	}
	return n, nil
}

// SetFileName updates a session's display name (after Save As / Rename in the
// editor), so subsequent Save As / Download default to the new name.
func (s *Service) SetFileName(ctx context.Context, key, name string) error {
	_, err := s.client.ExecContext(ctx,
		`UPDATE office_sessions SET file_name = $2 WHERE session_key = $1`, key, name)
	return err
}

// SetSourceFileID records the Trilli file ID a session was saved as, so a
// subsequent resume (refresh) knows the doc was previously saved and the client
// can silently overwrite on subsequent Save clicks.
func (s *Service) SetSourceFileID(ctx context.Context, key string, fileID int64) error {
	_, err := s.client.ExecContext(ctx,
		`UPDATE office_sessions SET source_file_id = $2 WHERE session_key = $1`, key, fileID)
	return err
}

// UserFriendlyName looks up a user's display name (first + last, full_name, or
// email fallback) for WOPI CheckFileInfo. The WOPI handler authenticates via
// session key (not app cookie), so it can't use auth.Identity — it queries the
// users table directly.
func (s *Service) UserFriendlyName(ctx context.Context, userID int64) string {
	var firstName, lastName, fullName, email sql.NullString
	err := s.client.QueryRowContext(ctx,
		`SELECT first_name, last_name, full_name, email FROM users WHERE id = $1`, userID,
	).Scan(&firstName, &lastName, &fullName, &email)
	if err != nil {
		return "Trilli User"
	}
	if firstName.Valid && lastName.Valid && firstName.String != "" && lastName.String != "" {
		return firstName.String + " " + lastName.String
	}
	if fullName.Valid && fullName.String != "" {
		return fullName.String
	}
	if email.Valid && email.String != "" {
		return email.String
	}
	return "Trilli User"
}

func (s *Service) insert(ctx context.Context, sess *Session) error {
	var src any
	if sess.SourceFileID != nil {
		src = *sess.SourceFileID
	}
	_, err := s.client.ExecContext(ctx, `
		INSERT INTO office_sessions
		    (session_key, tenant_id, user_id, app, source_file_id, working_path, file_name, size_bytes, created_at, last_seen_at, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
		sess.SessionKey, sess.TenantID, sess.UserID, sess.App, src,
		sess.WorkingPath, sess.FileName, sess.SizeBytes, sess.CreatedAt, sess.LastSeenAt, sess.ExpiresAt)
	if err != nil {
		return fmt.Errorf("officesessions: insert: %w", err)
	}
	return nil
}

// workPath builds the working-blob key for a session. The .office/work segment
// keeps it hidden + disjoint from real user content.
func workPath(tenantID int64, sessionKey, nativeExt string) string {
	return fmt.Sprintf("tenants/%d/%s/%s/%s", tenantID, workPrefix, sessionKey, "doc."+nativeExt)
}

// newSessionKey mints a 22-char URL-safe crypto-random session key. It's the
// only credential the engine sees (it rides in the WOPISrc URL the engine
// resolves server-side; the browser never fetches the WOPI host).
func newSessionKey() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("officesessions: session key: %w", err)
	}
	return strings.TrimRight(base64.URLEncoding.EncodeToString(b), "="), nil
}

// countingReader wraps an io.Reader and counts the bytes that flow through it,
// so Create can capture the seeded working-blob's plaintext size for
// CheckFileInfo (the engine skips GetFile when Size is 0 → blank canvas).
type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

// ---- collaborative sessions -------------------------------------------------

// scanSession scans the canonical session column list from a QueryRow result.
func scanSession(row interface{ Scan(...any) error }) (*Session, error) {
	var sess Session
	var src sql.NullInt64
	err := row.Scan(&sess.SessionKey, &sess.TenantID, &sess.UserID, &sess.App, &src,
		&sess.WorkingPath, &sess.FileName, &sess.SizeBytes, &sess.PutSeq,
		&sess.CreatedAt, &sess.LastSeenAt, &sess.ExpiresAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if src.Valid {
		v := src.Int64
		sess.SourceFileID = &v
	}
	return &sess, nil
}

// FindActiveByFile returns the newest ACTIVE session editing the given source
// file in this tenant+app — the join point for collaborative editing: a second
// user opening the same file attaches to this session instead of forking a
// private working copy.
func (s *Service) FindActiveByFile(ctx context.Context, tenantID, fileID int64, app string) (*Session, error) {
	row := s.client.QueryRowContext(ctx, `
		SELECT session_key, tenant_id, user_id, app, source_file_id, working_path,
		       file_name, size_bytes, put_seq, created_at, last_seen_at, expires_at
		  FROM office_sessions
		 WHERE tenant_id = $1 AND source_file_id = $2 AND app = $3 AND expires_at > NOW()
		 ORDER BY created_at DESC LIMIT 1`, tenantID, fileID, app)
	return scanSession(row)
}

// GetActiveJoined returns the newest active session the user PARTICIPATES in
// (via a token) for the app — lets a joiner's refresh resume the shared doc.
func (s *Service) GetActiveJoined(ctx context.Context, userID int64, app string) (*Session, error) {
	row := s.client.QueryRowContext(ctx, `
		SELECT s.session_key, s.tenant_id, s.user_id, s.app, s.source_file_id, s.working_path,
		       s.file_name, s.size_bytes, s.put_seq, s.created_at, s.last_seen_at, s.expires_at
		  FROM office_sessions s
		  JOIN office_session_tokens t ON t.session_key = s.session_key
		 WHERE t.user_id = $1 AND s.app = $2 AND s.expires_at > NOW()
		 ORDER BY s.created_at DESC LIMIT 1`, userID, app)
	return scanSession(row)
}

// MintToken returns the user's participant token for a session, creating it on
// first join (idempotent per session+user).
func (s *Service) MintToken(ctx context.Context, sessionKey string, userID int64) (string, error) {
	tok, err := newSessionKey()
	if err != nil {
		return "", err
	}
	var out string
	err = s.client.QueryRowContext(ctx, `
		INSERT INTO office_session_tokens (token, session_key, user_id)
		VALUES ($1, $2, $3)
		ON CONFLICT (session_key, user_id) DO UPDATE SET user_id = EXCLUDED.user_id
		RETURNING token`, tok, sessionKey, userID).Scan(&out)
	return out, err
}

// ResolveToken maps a WOPI access token to (session, acting user). Two forms:
// a participant token from office_session_tokens, or — back-compat for
// sessions started before collaborative tokens — the raw session key, which
// acts as the session creator.
func (s *Service) ResolveToken(ctx context.Context, token string) (*Session, int64, error) {
	var key string
	var actor int64
	err := s.client.QueryRowContext(ctx, `
		SELECT session_key, user_id FROM office_session_tokens WHERE token = $1`, token,
	).Scan(&key, &actor)
	if err == nil {
		sess, gerr := s.Get(ctx, key)
		if gerr != nil {
			return nil, 0, ErrNotFound
		}
		return sess, actor, nil
	}
	// raw session key fallback (pre-token sessions)
	legacy, lerr := s.Get(ctx, token)
	if lerr != nil {
		return nil, 0, ErrNotFound
	}
	return legacy, legacy.UserID, nil
}

// OtherParticipants reports how many participants besides the given user hold
// a token on the session — the "is anyone else here?" guard for teardown.
func (s *Service) OtherParticipants(ctx context.Context, sessionKey string, userID int64) int {
	var n int
	_ = s.client.QueryRowContext(ctx, `
		SELECT count(*) FROM office_session_tokens
		 WHERE session_key = $1 AND user_id <> $2`, sessionKey, userID).Scan(&n)
	return n
}

// IsParticipant reports whether the user is the creator of, or holds a token
// on, the session — i.e. may act on it (poll size, save, download).
func (s *Service) IsParticipant(ctx context.Context, sessionKey string, userID int64) bool {
	var n int
	_ = s.client.QueryRowContext(ctx, `
		SELECT count(*) FROM office_sessions
		 WHERE session_key = $1 AND user_id = $2`, sessionKey, userID).Scan(&n)
	if n > 0 {
		return true
	}
	_ = s.client.QueryRowContext(ctx, `
		SELECT count(*) FROM office_session_tokens
		 WHERE session_key = $1 AND user_id = $2`, sessionKey, userID).Scan(&n)
	return n > 0
}

// LeaveSession removes the user's participant token without touching the doc.
func (s *Service) LeaveSession(ctx context.Context, sessionKey string, userID int64) {
	_, _ = s.client.ExecContext(ctx, `
		DELETE FROM office_session_tokens WHERE session_key = $1 AND user_id = $2`,
		sessionKey, userID)
}

// SharesActiveSession reports whether users a and b are BOTH participants
// (creator or token-holder) in the same currently-active office session. This
// is the collaboration trust context: co-editing a document right now grants
// each the right to see the other's presence attributes (e.g. avatar), even
// across tenants. No blob copying — the live session row IS the shared context,
// and it's reaped by the janitor / leave logic when everyone's gone.
func (s *Service) SharesActiveSession(ctx context.Context, a, b int64) bool {
	if a == b {
		return true
	}
	var n int
	_ = s.client.QueryRowContext(ctx, `
		WITH parts AS (
			SELECT session_key, user_id FROM office_sessions WHERE expires_at > NOW()
			UNION
			SELECT s.session_key, t.user_id
			  FROM office_session_tokens t
			  JOIN office_sessions s ON s.session_key = t.session_key
			 WHERE s.expires_at > NOW()
		)
		SELECT count(*) FROM parts pa JOIN parts pb ON pa.session_key = pb.session_key
		 WHERE pa.user_id = $1 AND pb.user_id = $2`, a, b).Scan(&n)
	return n > 0
}
