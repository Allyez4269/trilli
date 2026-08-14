package sharing

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"trilli/system/auth"
	"trilli/system/logging"
	"trilli/system/mailer"
)

// newToken returns a URL-safe random share token (32 bytes → 64 hex chars).
func newToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// Create issues a share after validating the resource belongs to the tenant,
// the grantee is valid, and clamping the expiry to the plan's
// max_share_expiry_days. Returns the created share (with joined display names).
func (s *Service) Create(ctx context.Context, in CreateInput) (*Share, error) {
	if in.ResourceType != "file" && in.ResourceType != "folder" {
		return nil, ErrInvalidInput
	}
	if (in.ResourceType == "file") != (in.FileID != nil) || (in.ResourceType == "folder") != (in.FolderID != nil) {
		return nil, ErrInvalidInput
	}
	capability := in.Capability
	if capability != "view" && capability != "download" {
		capability = "download"
	}

	// Resource must exist, be active, and belong to the tenant.
	resourceName, location, err := s.resourceNameAndLocation(ctx, in.TenantID, in.ResourceType, in.FileID, in.FolderID)
	if err != nil {
		return nil, err
	}

	// Folder-access gate: the actor must be able to READ the resource's
	// location, and viewers (read-only role) may not mint links at all —
	// a share extends access beyond the workspace, which is a write-level
	// act. Without this, a folder-scoped member could exfiltrate folders
	// outside their grant via a public link. Access == nil means a trusted
	// internal caller (none today); handlers always pass identity.Access.
	if in.Access != nil {
		if !in.Access.Write || !in.Access.CanRead(location) {
			return nil, ErrForbidden
		}
	}

	// Grantee validation.
	switch in.GranteeType {
	case "public":
		in.GranteeUserID = nil
		in.GranteeEmail = ""
	case "member":
		if in.GranteeUserID == nil {
			return nil, ErrInvalidInput
		}
		var ok bool
		if err := s.client.QueryRowContext(ctx, `
			SELECT EXISTS(SELECT 1 FROM tenant_members
			               WHERE tenant_id = $1 AND user_id = $2 AND status = 'active')`,
			in.TenantID, *in.GranteeUserID).Scan(&ok); err != nil {
			return nil, fmt.Errorf("sharing: check member: %w", err)
		}
		if !ok {
			return nil, ErrInvalidInput
		}
		in.GranteeEmail = ""
	case "email":
		in.GranteeEmail = strings.TrimSpace(strings.ToLower(in.GranteeEmail))
		if in.GranteeEmail == "" || !strings.Contains(in.GranteeEmail, "@") {
			return nil, ErrInvalidInput
		}
		in.GranteeUserID = nil
	default:
		return nil, ErrInvalidInput
	}

	// Expiry: clamp to the plan cap (NULL plan cap = unlimited). If the plan
	// caps expiry, a share can't outlive that, and an "unlimited" request is
	// forced down to the cap.
	expiresAt, err := s.clampedExpiry(ctx, in.TenantID, in.ExpiresInDays)
	if err != nil {
		return nil, err
	}

	var pwHash any
	if strings.TrimSpace(in.Password) != "" {
		if len(in.Password) < auth.MinPasswordLen {
			return nil, ErrPasswordTooShort
		}
		h, hErr := auth.HashPassword(in.Password)
		if hErr != nil {
			return nil, fmt.Errorf("sharing: hash password: %w", hErr)
		}
		pwHash = h
	}

	// Insert with a fresh token, retrying once on the (vanishingly rare) collision.
	var id int64
	var token string
	for attempt := 0; attempt < 5; attempt++ {
		tok, tErr := newToken()
		if tErr != nil {
			return nil, fmt.Errorf("sharing: token: %w", tErr)
		}
		var granteeEmail any
		if in.GranteeEmail != "" {
			granteeEmail = in.GranteeEmail
		}
		err = s.client.QueryRowContext(ctx, `
			INSERT INTO shares (tenant_id, token, resource_type, file_id, folder_id,
			                    grantee_type, grantee_user_id, grantee_email, capability, password_hash,
			                    expires_at, created_by_user_id)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
			ON CONFLICT (token) DO NOTHING
			RETURNING id`,
			in.TenantID, tok, in.ResourceType, in.FileID, in.FolderID,
			in.GranteeType, in.GranteeUserID, granteeEmail, capability, pwHash, expiresAt, in.ActorID,
		).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			continue // token collision — try again
		}
		if err != nil {
			return nil, fmt.Errorf("sharing: insert: %w", err)
		}
		token = tok
		break
	}
	if token == "" {
		return nil, fmt.Errorf("sharing: could not allocate token")
	}

	logging.Info(packageName, "Create: tenant=%d actor=%d %s grantee=%s id=%d", in.TenantID, in.ActorID, in.ResourceType, in.GranteeType, id)

	share, err := s.byID(ctx, in.TenantID, id)
	if err != nil {
		return nil, err
	}
	// Email the magic link: always for email grantees, and for member grantees
	// when the sharer asked to notify. Best-effort — never fail the create.
	s.maybeEmailLink(ctx, share, in, resourceName)
	return share, nil
}

// maybeEmailLink sends the share's magic link to the grantee when appropriate.
func (s *Service) maybeEmailLink(ctx context.Context, share *Share, in CreateInput, resourceName string) {
	if s.mailer == nil {
		return
	}
	var to string
	switch in.GranteeType {
	case "email":
		to = in.GranteeEmail
	case "member":
		if !in.Notify || in.GranteeUserID == nil {
			return
		}
		_ = s.client.QueryRowContext(ctx, `SELECT email FROM users WHERE id = $1`, *in.GranteeUserID).Scan(&to)
	default:
		return // public link — nothing to email
	}
	if strings.TrimSpace(to) == "" {
		return
	}
	if err := s.mailer.SendShareLink(ctx, mailer.ShareLinkEmail{
		To:           to,
		SharerName:   in.SharerName,
		ResourceName: resourceName,
		IsFolder:     in.ResourceType == "folder",
		URL:          "https://app.trilli.com" + share.URL,
		Capability:   share.Capability,
		HasPassword:  share.HasPassword,
		ExpiresAt:    share.ExpiresAt,
	}); err != nil {
		logging.Error(packageName, "maybeEmailLink: send to %s failed: %v", to, err)
	}
}

// ListRecipients returns active members of the tenant (minus the caller) for
// the share recipient picker.
func (s *Service) ListRecipients(ctx context.Context, tenantID, excludeUserID int64) ([]Recipient, error) {
	rows, err := s.client.QueryContext(ctx, `
		SELECT u.id,
		       COALESCE(NULLIF(TRIM(COALESCE(u.first_name,'') || ' ' || COALESCE(u.last_name,'')), ''), u.email),
		       u.email
		  FROM tenant_members tm JOIN users u ON u.id = tm.user_id
		 WHERE tm.tenant_id = $1 AND tm.status = 'active' AND u.status = 'active' AND u.id <> $2
		 ORDER BY 2`, tenantID, excludeUserID)
	if err != nil {
		return nil, fmt.Errorf("sharing: list recipients: %w", err)
	}
	defer rows.Close()
	out := make([]Recipient, 0)
	for rows.Next() {
		var r Recipient
		if err := rows.Scan(&r.UserID, &r.Name, &r.Email); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// clampedExpiry resolves the requested expiry against the plan cap.
func (s *Service) clampedExpiry(ctx context.Context, tenantID int64, requestedDays *int) (any, error) {
	var maxDays sql.NullInt64
	if err := s.client.QueryRowContext(ctx, `
		SELECT p.max_share_expiry_days FROM tenants t JOIN plans p ON p.id = t.plan_id WHERE t.id = $1`,
		tenantID).Scan(&maxDays); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("sharing: load expiry cap: %w", err)
	}
	days := 0
	if requestedDays != nil && *requestedDays > 0 {
		days = *requestedDays
	}
	if maxDays.Valid {
		cap := int(maxDays.Int64)
		if days == 0 || days > cap { // unlimited request or over cap → clamp
			days = cap
		}
	}
	if days <= 0 {
		return nil, nil // no expiry (unlimited plan + no request)
	}
	return time.Now().UTC().AddDate(0, 0, days), nil
}

// resourceName returns the active resource's display name, validating tenancy.
// resourceNameAndLocation loads the resource's display name plus the folder
// that governs access to it: a file's parent folder (nil = tenant root), or
// the shared folder itself. The location feeds auth.FolderAccess.CanRead.
func (s *Service) resourceNameAndLocation(ctx context.Context, tenantID int64, kind string, fileID, folderID *int64) (string, *int64, error) {
	var name string
	var location *int64
	var err error
	if kind == "file" {
		err = s.client.QueryRowContext(ctx,
			`SELECT name, parent_folder_id FROM files WHERE id = $1 AND tenant_id = $2 AND status = 'active'`,
			*fileID, tenantID).Scan(&name, &location)
	} else {
		err = s.client.QueryRowContext(ctx,
			`SELECT name FROM folders WHERE id = $1 AND tenant_id = $2 AND status = 'active'`,
			*folderID, tenantID).Scan(&name)
		location = folderID
	}
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil, ErrResourceNotFound
	}
	if err != nil {
		return "", nil, fmt.Errorf("sharing: load resource: %w", err)
	}
	return name, location, nil
}

const shareSelect = `
	SELECT s.id, s.token, s.resource_type, s.file_id, s.folder_id,
	       COALESCE(f.name, fo.name, '') AS resource_name,
	       s.grantee_type, s.grantee_user_id, COALESCE(s.grantee_email, ''),
	       COALESCE(NULLIF(TRIM(COALESCE(gu.first_name,'') || ' ' || COALESCE(gu.last_name,'')), ''), gu.email, s.grantee_email, '') AS grantee_name,
	       s.capability, (s.password_hash IS NOT NULL) AS has_password, s.expires_at,
	       s.created_by_user_id,
	       COALESCE(NULLIF(TRIM(COALESCE(cu.first_name,'') || ' ' || COALESCE(cu.last_name,'')), ''), cu.email, '') AS created_by_name,
	       s.created_at, s.revoked_at, s.last_accessed_at, s.access_count
	  FROM shares s
	  LEFT JOIN files   f  ON f.id  = s.file_id
	  LEFT JOIN folders fo ON fo.id = s.folder_id
	  LEFT JOIN users   gu ON gu.id = s.grantee_user_id
	  LEFT JOIN users   cu ON cu.id = s.created_by_user_id`

func scanShare(rows interface{ Scan(...any) error }, viewerID int64) (*Share, error) {
	var sh Share
	var fileID, folderID, granteeUserID sql.NullInt64
	var expiresAt, revokedAt, lastAccessed sql.NullTime
	if err := rows.Scan(&sh.ID, &sh.Token, &sh.ResourceType, &fileID, &folderID,
		&sh.ResourceName, &sh.GranteeType, &granteeUserID, &sh.GranteeEmail, &sh.GranteeName,
		&sh.Capability, &sh.HasPassword, &expiresAt, &sh.CreatedByID, &sh.CreatedByName,
		&sh.CreatedAt, &revokedAt, &lastAccessed, &sh.AccessCount); err != nil {
		return nil, err
	}
	if fileID.Valid {
		sh.FileID = &fileID.Int64
	}
	if folderID.Valid {
		sh.FolderID = &folderID.Int64
	}
	if granteeUserID.Valid {
		sh.GranteeUserID = &granteeUserID.Int64
	}
	if expiresAt.Valid {
		sh.ExpiresAt = &expiresAt.Time
	}
	if revokedAt.Valid {
		sh.RevokedAt = &revokedAt.Time
	}
	if lastAccessed.Valid {
		sh.LastAccessedAt = &lastAccessed.Time
	}
	sh.URL = "/s/" + sh.Token
	if sh.CreatedByID == viewerID {
		sh.Direction = "out"
	} else {
		sh.Direction = "in"
	}
	return &sh, nil
}

func (s *Service) byID(ctx context.Context, tenantID, id int64) (*Share, error) {
	row := s.client.QueryRowContext(ctx, shareSelect+` WHERE s.id = $1 AND s.tenant_id = $2`, id, tenantID)
	sh, err := scanShare(row, 0)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("sharing: load share: %w", err)
	}
	return sh, nil
}

// ListForUser returns the caller's shares in both directions: ones they created
// (Direction "out") and ones shared with them as a member (Direction "in").
// Includes recently-revoked rows so the hub can show history.
func (s *Service) ListForUser(ctx context.Context, tenantID, userID int64) ([]Share, error) {
	rows, err := s.client.QueryContext(ctx,
		shareSelect+` WHERE s.tenant_id = $1 AND (s.created_by_user_id = $2 OR s.grantee_user_id = $2)
		              ORDER BY s.created_at DESC`, tenantID, userID)
	if err != nil {
		return nil, fmt.Errorf("sharing: list: %w", err)
	}
	defer rows.Close()
	out := make([]Share, 0)
	for rows.Next() {
		sh, sErr := scanShare(rows, userID)
		if sErr != nil {
			return nil, fmt.Errorf("sharing: scan: %w", sErr)
		}
		out = append(out, *sh)
	}
	return out, rows.Err()
}

// Revoke turns off a share (soft). Only the creator, or an owner/admin of the
// tenant, may revoke. Idempotent.
func (s *Service) Revoke(ctx context.Context, tenantID, shareID, callerID int64, callerIsAdmin bool) error {
	var createdBy int64
	if err := s.client.QueryRowContext(ctx,
		`SELECT created_by_user_id FROM shares WHERE id = $1 AND tenant_id = $2`,
		shareID, tenantID).Scan(&createdBy); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("sharing: load for revoke: %w", err)
	}
	if !callerIsAdmin && createdBy != callerID {
		return ErrForbidden
	}
	if _, err := s.client.ExecContext(ctx,
		`UPDATE shares SET revoked_at = NOW() WHERE id = $1 AND tenant_id = $2 AND revoked_at IS NULL`,
		shareID, tenantID); err != nil {
		return fmt.Errorf("sharing: revoke: %w", err)
	}
	return nil
}

// Delete permanently removes a share row (no soft-revoke). Same authorization
// as Revoke: only the creator or a tenant owner/admin may delete.
func (s *Service) Delete(ctx context.Context, tenantID, shareID, callerID int64, callerIsAdmin bool) error {
	var createdBy int64
	if err := s.client.QueryRowContext(ctx,
		`SELECT created_by_user_id FROM shares WHERE id = $1 AND tenant_id = $2`,
		shareID, tenantID).Scan(&createdBy); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("sharing: load for delete: %w", err)
	}
	if !callerIsAdmin && createdBy != callerID {
		return ErrForbidden
	}
	if _, err := s.client.ExecContext(ctx,
		`DELETE FROM shares WHERE id = $1 AND tenant_id = $2`,
		shareID, tenantID); err != nil {
		return fmt.Errorf("sharing: delete: %w", err)
	}
	return nil
}

// Resolved is the public view of a share once its token is looked up.
type Resolved struct {
	ID           int64
	TenantID     int64
	ResourceType string
	FileID       *int64
	FolderID     *int64
	ResourceName string
	Capability   string
	HasPassword  bool
	pwHash       string
}

// ByToken resolves a public token to a live share, enforcing revoke, expiry,
// and owner availability (a suspended/lapsed account's links go dark). It does
// NOT verify the password — callers check HasPassword and call Authorized.
func (s *Service) ByToken(ctx context.Context, token string) (*Resolved, error) {
	var r Resolved
	var fileID, folderID sql.NullInt64
	var expiresAt sql.NullTime
	var pwHash sql.NullString
	var tenantStatus, lifecycleState string
	err := s.client.QueryRowContext(ctx, `
		SELECT s.id, s.tenant_id, s.resource_type, s.file_id, s.folder_id,
		       COALESCE(f.name, fo.name, ''), s.capability, s.password_hash, s.expires_at,
		       s.revoked_at, t.status, t.lifecycle_state
		  FROM shares s
		  JOIN tenants t  ON t.id = s.tenant_id
		  LEFT JOIN files   f  ON f.id  = s.file_id
		  LEFT JOIN folders fo ON fo.id = s.folder_id
		 WHERE s.token = $1`, token,
	).Scan(&r.ID, &r.TenantID, &r.ResourceType, &fileID, &folderID, &r.ResourceName,
		&r.Capability, &pwHash, &expiresAt, new(sql.NullTime), &tenantStatus, &lifecycleState)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("sharing: resolve token: %w", err)
	}
	// Re-scan revoked separately (the new(sql.NullTime) above discards it); do a
	// targeted check instead for clarity.
	var revoked sql.NullTime
	if err := s.client.QueryRowContext(ctx, `SELECT revoked_at FROM shares WHERE id = $1`, r.ID).Scan(&revoked); err == nil && revoked.Valid {
		return nil, ErrRevoked
	}
	if expiresAt.Valid && expiresAt.Time.Before(time.Now()) {
		return nil, ErrExpired
	}
	if tenantStatus != "active" || lifecycleState == "lapsed" {
		return nil, ErrUnavailable
	}
	if fileID.Valid {
		r.FileID = &fileID.Int64
	}
	if folderID.Valid {
		r.FolderID = &folderID.Int64
	}
	r.HasPassword = pwHash.Valid && pwHash.String != ""
	r.pwHash = pwHash.String
	return &r, nil
}

// Authorized checks the supplied password against a protected share. Returns
// nil when the share has no password or the password matches.
func (s *Service) Authorized(r *Resolved, password string) error {
	if !r.HasPassword {
		return nil
	}
	if strings.TrimSpace(password) == "" {
		return ErrPasswordRequired
	}
	ok, err := auth.VerifyPassword(r.pwHash, password)
	if err != nil || !ok {
		return ErrWrongPassword
	}
	return nil
}

// SharedFile is a file reachable through a share, ready to stream.
type SharedFile struct {
	Name        string
	ContentType string
	SizeBytes   int64
	blobPath    string
}

// OpenForDownload returns an open reader for a file reachable through the share.
// For a file share, fileID must be the shared file (or 0). For a folder share,
// fileID must be a file within the shared folder's subtree. Egress is metered.
func (s *Service) OpenForDownload(ctx context.Context, r *Resolved, fileID int64) (*SharedFile, io.ReadCloser, error) {
	var targetID int64
	switch r.ResourceType {
	case "file":
		targetID = *r.FileID
		if fileID != 0 && fileID != targetID {
			return nil, nil, ErrForbidden
		}
	case "folder":
		if fileID == 0 {
			return nil, nil, ErrInvalidInput
		}
		within, err := s.fileWithinFolder(ctx, r.TenantID, *r.FolderID, fileID)
		if err != nil {
			return nil, nil, err
		}
		if !within {
			return nil, nil, ErrForbidden
		}
		targetID = fileID
	default:
		return nil, nil, ErrInvalidInput
	}

	var sf SharedFile
	if err := s.client.QueryRowContext(ctx,
		`SELECT name, content_type, size_bytes, blob_path FROM files
		  WHERE id = $1 AND tenant_id = $2 AND status = 'active'`,
		targetID, r.TenantID).Scan(&sf.Name, &sf.ContentType, &sf.SizeBytes, &sf.blobPath); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil, ErrResourceNotFound
		}
		return nil, nil, fmt.Errorf("sharing: load file: %w", err)
	}
	rc, err := s.store.Get(ctx, sf.blobPath)
	if err != nil {
		return nil, nil, fmt.Errorf("sharing: open blob: %w", err)
	}
	return &sf, rc, nil
}

// MeterEgress records bytes served through a public download (best-effort,
// detached from request cancellation by the caller).
func (s *Service) MeterEgress(ctx context.Context, tenantID, bytes int64) {
	if s.meter == nil {
		return
	}
	if err := s.meter.RecordOut(ctx, tenantID, nil, bytes); err != nil {
		logging.Error(packageName, "MeterEgress failed (tenant=%d): %v", tenantID, err)
	}
}

// RecordAccess bumps the access counter + last-accessed timestamp.
func (s *Service) RecordAccess(ctx context.Context, shareID int64) {
	if _, err := s.client.ExecContext(ctx,
		`UPDATE shares SET access_count = access_count + 1, last_accessed_at = NOW() WHERE id = $1`,
		shareID); err != nil {
		logging.Error(packageName, "RecordAccess failed (id=%d): %v", shareID, err)
	}
}

// BrowseEntry is one item when listing a shared folder.
type BrowseEntry struct {
	ID       int64  `json:"id"`
	Kind     string `json:"kind"` // file | folder
	Name     string `json:"name"`
	Size     int64  `json:"size_bytes"`
	Modified string `json:"modified"`
}

// Browse lists the contents of a folder reachable through a folder share.
// folderID==0 means the shared root; otherwise it must be within the subtree.
func (s *Service) Browse(ctx context.Context, r *Resolved, folderID int64) ([]BrowseEntry, error) {
	if r.ResourceType != "folder" {
		return nil, ErrInvalidInput
	}
	root := *r.FolderID
	target := root
	if folderID != 0 {
		if folderID != root {
			within, err := s.folderWithinFolder(ctx, r.TenantID, root, folderID)
			if err != nil {
				return nil, err
			}
			if !within {
				return nil, ErrForbidden
			}
		}
		target = folderID
	}

	out := make([]BrowseEntry, 0)
	frows, err := s.client.QueryContext(ctx,
		`SELECT id, name, updated_at FROM folders
		  WHERE tenant_id = $1 AND parent_folder_id = $2 AND status = 'active' ORDER BY lower(name)`,
		r.TenantID, target)
	if err != nil {
		return nil, fmt.Errorf("sharing: browse folders: %w", err)
	}
	for frows.Next() {
		var e BrowseEntry
		var mod time.Time
		if err := frows.Scan(&e.ID, &e.Name, &mod); err != nil {
			frows.Close()
			return nil, err
		}
		e.Kind = "folder"
		e.Modified = mod.Format(time.RFC3339)
		out = append(out, e)
	}
	frows.Close()

	drows, err := s.client.QueryContext(ctx,
		`SELECT id, name, size_bytes, updated_at FROM files
		  WHERE tenant_id = $1 AND parent_folder_id = $2 AND status = 'active' ORDER BY lower(name)`,
		r.TenantID, target)
	if err != nil {
		return nil, fmt.Errorf("sharing: browse files: %w", err)
	}
	defer drows.Close()
	for drows.Next() {
		var e BrowseEntry
		var mod time.Time
		if err := drows.Scan(&e.ID, &e.Name, &e.Size, &mod); err != nil {
			return nil, err
		}
		e.Kind = "file"
		e.Modified = mod.Format(time.RFC3339)
		out = append(out, e)
	}
	return out, drows.Err()
}

// folderWithinFolder reports whether candidate descends from (or equals) root.
func (s *Service) folderWithinFolder(ctx context.Context, tenantID, root, candidate int64) (bool, error) {
	var within bool
	err := s.client.QueryRowContext(ctx, `
		WITH RECURSIVE up AS (
			SELECT id, parent_folder_id FROM folders WHERE id = $1 AND tenant_id = $3
			UNION ALL
			SELECT f.id, f.parent_folder_id FROM folders f
			  JOIN up ON up.parent_folder_id = f.id WHERE f.tenant_id = $3
		)
		SELECT EXISTS(SELECT 1 FROM up WHERE id = $2)`, candidate, root, tenantID).Scan(&within)
	if err != nil {
		return false, fmt.Errorf("sharing: ancestry check: %w", err)
	}
	return within, nil
}

// fileWithinFolder reports whether a file lives anywhere under root.
func (s *Service) fileWithinFolder(ctx context.Context, tenantID, root, fileID int64) (bool, error) {
	var parent sql.NullInt64
	err := s.client.QueryRowContext(ctx,
		`SELECT parent_folder_id FROM files WHERE id = $1 AND tenant_id = $2 AND status = 'active'`,
		fileID, tenantID).Scan(&parent)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("sharing: file parent: %w", err)
	}
	if !parent.Valid {
		return false, nil // file at tenant root, not inside any folder
	}
	if parent.Int64 == root {
		return true, nil
	}
	return s.folderWithinFolder(ctx, tenantID, root, parent.Int64)
}
