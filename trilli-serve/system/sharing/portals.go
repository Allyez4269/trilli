package sharing

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"trilli/system/auth"
	"trilli/system/files"
	"trilli/system/logging"
	"trilli/system/mailer"
)

// Portal is a drop portal (file request) for the hub + management views.
type Portal struct {
	ID            int64      `json:"id"`
	Token         string     `json:"token"`
	URL           string     `json:"url"` // /p/<token>
	FolderID      int64      `json:"folder_id"`
	FolderName    string     `json:"folder_name"`
	Title          string     `json:"title"`
	HasPassword    bool       `json:"has_password"`
	NotifyOnUpload bool       `json:"notify_on_upload"`
	ExpiresAt      *time.Time `json:"expires_at,omitempty"`
	CreatedByID    int64      `json:"created_by_id"`
	CreatedByName string     `json:"created_by_name,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	RevokedAt     *time.Time `json:"revoked_at,omitempty"`
	UploadCount   int64      `json:"upload_count"`
	LastUploadAt  *time.Time `json:"last_upload_at,omitempty"`
}

type CreatePortalInput struct {
	TenantID int64
	ActorID  int64
	// Access is the actor's resolved folder access (identity.Access). Creating
	// a portal requires WRITE access to the destination folder — portal uploads
	// land there as the creator, so a read-only viewer or an out-of-scope
	// member must not be able to mint one. nil skips the gate (internal only).
	Access         *auth.FolderAccess
	FolderID       int64
	Title          string
	ExpiresInDays  *int
	Password       string
	NotifyOnUpload bool
}

// UpdatePortalInput carries the editable portal fields. Nil pointers are left
// unchanged. Password: nil = keep, "" = remove, non-empty = set. ExpiresInDays:
// nil = keep, 0 = no expiry, >0 = days. Revoked toggles terminate/reactivate.
type UpdatePortalInput struct {
	Title          *string
	ExpiresInDays  *int
	Password       *string
	NotifyOnUpload *bool
	Revoked        *bool
}

// ResolvedPortal is the public view of a portal once its token is looked up.
type ResolvedPortal struct {
	ID             int64
	TenantID       int64
	FolderID       int64
	CreatedBy      int64
	Title          string
	FolderName     string
	HasPassword    bool
	NotifyOnUpload bool
	pwHash         string
}

// CreatePortal makes a drop portal targeting an active folder in the tenant.
func (s *Service) CreatePortal(ctx context.Context, in CreatePortalInput) (*Portal, error) {
	var folderName string
	err := s.client.QueryRowContext(ctx,
		`SELECT name FROM folders WHERE id = $1 AND tenant_id = $2 AND status = 'active'`,
		in.FolderID, in.TenantID).Scan(&folderName)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrResourceNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("sharing: load portal folder: %w", err)
	}

	// Folder-access gate: portals write into the folder on the creator's
	// behalf, so require write access to it (blocks viewers and members
	// scoped to other folders).
	if in.Access != nil && !in.Access.CanWrite(&in.FolderID) {
		return nil, ErrForbidden
	}

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
			return nil, fmt.Errorf("sharing: hash portal password: %w", hErr)
		}
		pwHash = h
	}

	var id int64
	for attempt := 0; attempt < 5; attempt++ {
		tok, tErr := newToken()
		if tErr != nil {
			return nil, fmt.Errorf("sharing: portal token: %w", tErr)
		}
		err = s.client.QueryRowContext(ctx, `
			INSERT INTO drop_portals (tenant_id, token, folder_id, title, password_hash, expires_at, created_by_user_id, notify_on_upload)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
			ON CONFLICT (token) DO NOTHING
			RETURNING id`,
			in.TenantID, tok, in.FolderID, strings.TrimSpace(in.Title), pwHash, expiresAt, in.ActorID, in.NotifyOnUpload,
		).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("sharing: insert portal: %w", err)
		}
		break
	}
	if id == 0 {
		return nil, fmt.Errorf("sharing: could not allocate portal token")
	}
	logging.Info(packageName, "CreatePortal: tenant=%d actor=%d folder=%d id=%d", in.TenantID, in.ActorID, in.FolderID, id)
	return s.portalByID(ctx, in.TenantID, id)
}

const portalSelect = `
	SELECT p.id, p.token, p.folder_id, COALESCE(fo.name,''), p.title,
	       (p.password_hash IS NOT NULL), p.notify_on_upload, p.expires_at, p.created_by_user_id,
	       COALESCE(NULLIF(TRIM(COALESCE(cu.first_name,'') || ' ' || COALESCE(cu.last_name,'')), ''), cu.email, ''),
	       p.created_at, p.revoked_at, p.upload_count, p.last_upload_at
	  FROM drop_portals p
	  LEFT JOIN folders fo ON fo.id = p.folder_id
	  LEFT JOIN users   cu ON cu.id = p.created_by_user_id`

func scanPortal(row interface{ Scan(...any) error }) (*Portal, error) {
	var p Portal
	var expiresAt, revokedAt, lastUpload sql.NullTime
	if err := row.Scan(&p.ID, &p.Token, &p.FolderID, &p.FolderName, &p.Title,
		&p.HasPassword, &p.NotifyOnUpload, &expiresAt, &p.CreatedByID, &p.CreatedByName,
		&p.CreatedAt, &revokedAt, &p.UploadCount, &lastUpload); err != nil {
		return nil, err
	}
	if expiresAt.Valid {
		p.ExpiresAt = &expiresAt.Time
	}
	if revokedAt.Valid {
		p.RevokedAt = &revokedAt.Time
	}
	if lastUpload.Valid {
		p.LastUploadAt = &lastUpload.Time
	}
	p.URL = "/p/" + p.Token
	return &p, nil
}

func (s *Service) portalByID(ctx context.Context, tenantID, id int64) (*Portal, error) {
	row := s.client.QueryRowContext(ctx, portalSelect+` WHERE p.id = $1 AND p.tenant_id = $2`, id, tenantID)
	p, err := scanPortal(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("sharing: load portal: %w", err)
	}
	return p, nil
}

// ListPortals returns the tenant's drop portals (incl. revoked, for history).
func (s *Service) ListPortals(ctx context.Context, tenantID int64) ([]Portal, error) {
	rows, err := s.client.QueryContext(ctx, portalSelect+` WHERE p.tenant_id = $1 ORDER BY p.created_at DESC`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("sharing: list portals: %w", err)
	}
	defer rows.Close()
	out := make([]Portal, 0)
	for rows.Next() {
		p, sErr := scanPortal(rows)
		if sErr != nil {
			return nil, sErr
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}

// PortalByToken resolves a public portal token, enforcing revoke/expiry and
// owner availability (suspended/lapsed accounts can't receive uploads).
func (s *Service) PortalByToken(ctx context.Context, token string) (*ResolvedPortal, error) {
	var r ResolvedPortal
	var expiresAt, revokedAt sql.NullTime
	var pwHash sql.NullString
	var tenantStatus, lifecycleState string
	err := s.client.QueryRowContext(ctx, `
		SELECT p.id, p.tenant_id, p.folder_id, p.created_by_user_id, p.title,
		       COALESCE(fo.name,''), p.password_hash, p.notify_on_upload, p.expires_at, p.revoked_at,
		       t.status, t.lifecycle_state
		  FROM drop_portals p
		  JOIN tenants t  ON t.id = p.tenant_id
		  LEFT JOIN folders fo ON fo.id = p.folder_id
		 WHERE p.token = $1`, token,
	).Scan(&r.ID, &r.TenantID, &r.FolderID, &r.CreatedBy, &r.Title, &r.FolderName,
		&pwHash, &r.NotifyOnUpload, &expiresAt, &revokedAt, &tenantStatus, &lifecycleState)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("sharing: resolve portal token: %w", err)
	}
	if revokedAt.Valid {
		return nil, ErrRevoked
	}
	if expiresAt.Valid && expiresAt.Time.Before(time.Now()) {
		return nil, ErrExpired
	}
	if tenantStatus != "active" || lifecycleState == "lapsed" {
		return nil, ErrUnavailable
	}
	r.HasPassword = pwHash.Valid && pwHash.String != ""
	r.pwHash = pwHash.String
	return &r, nil
}

// AuthorizedPortal checks the supplied password for a protected portal.
func (s *Service) AuthorizedPortal(r *ResolvedPortal, password string) error {
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

// AcceptUpload streams an incoming portal upload into the destination folder
// via the normal files path — so storage quota, per-file size, workspace
// allocation, and transfer-ingress metering all apply. The file is attributed
// to the portal's creator (the account "receives" it).
func (s *Service) AcceptUpload(ctx context.Context, r *ResolvedPortal, name, contentType string, reader io.Reader) (*files.File, error) {
	if s.files == nil {
		return nil, fmt.Errorf("sharing: uploads not configured")
	}
	folderID := r.FolderID
	f, err := s.files.Upload(ctx, files.UploadInput{
		TenantID:       r.TenantID,
		UploaderID:     r.CreatedBy,
		Name:           name,
		ContentType:    contentType,
		Reader:         reader,
		ParentFolderID: &folderID,
	})
	if err != nil {
		return nil, err
	}
	if _, err := s.client.ExecContext(ctx,
		`UPDATE drop_portals SET upload_count = upload_count + 1, last_upload_at = NOW() WHERE id = $1`,
		r.ID); err != nil {
		logging.Error(packageName, "AcceptUpload: bump count failed (portal=%d): %v", r.ID, err)
	}
	// Upload alert to the portal owner (best-effort), if enabled.
	if r.NotifyOnUpload && s.mailer != nil {
		var ownerEmail, ownerName string
		if err := s.client.QueryRowContext(ctx,
			`SELECT email, COALESCE(NULLIF(TRIM(COALESCE(first_name,'') || ' ' || COALESCE(last_name,'')), ''), email)
			   FROM users WHERE id = $1`, r.CreatedBy).Scan(&ownerEmail, &ownerName); err == nil && ownerEmail != "" {
			if err := s.mailer.SendDropAlert(ctx, mailer.DropAlertEmail{
				To:         ownerEmail,
				OwnerName:  ownerName,
				FileName:   f.Name,
				FolderName: r.FolderName,
				PortalName: r.Title,
			}); err != nil {
				logging.Error(packageName, "AcceptUpload: drop alert failed (portal=%d): %v", r.ID, err)
			}
		}
	}
	return f, nil
}

// UpdatePortal edits an existing portal's properties (creator or owner/admin).
func (s *Service) UpdatePortal(ctx context.Context, tenantID, portalID, callerID int64, callerIsAdmin bool, in UpdatePortalInput) (*Portal, error) {
	var createdBy int64
	if err := s.client.QueryRowContext(ctx,
		`SELECT created_by_user_id FROM drop_portals WHERE id = $1 AND tenant_id = $2`,
		portalID, tenantID).Scan(&createdBy); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("sharing: load portal for update: %w", err)
	}
	if !callerIsAdmin && createdBy != callerID {
		return nil, ErrForbidden
	}

	sets := []string{}
	args := []any{}
	add := func(expr string, val any) {
		args = append(args, val)
		sets = append(sets, fmt.Sprintf("%s = $%d", expr, len(args)))
	}
	if in.Title != nil {
		add("title", strings.TrimSpace(*in.Title))
	}
	if in.NotifyOnUpload != nil {
		add("notify_on_upload", *in.NotifyOnUpload)
	}
	if in.ExpiresInDays != nil {
		exp, err := s.clampedExpiry(ctx, tenantID, in.ExpiresInDays)
		if err != nil {
			return nil, err
		}
		add("expires_at", exp)
	}
	if in.Password != nil {
		if *in.Password == "" {
			add("password_hash", nil)
		} else {
			if len(*in.Password) < auth.MinPasswordLen {
				return nil, ErrPasswordTooShort
			}
			h, hErr := auth.HashPassword(*in.Password)
			if hErr != nil {
				return nil, fmt.Errorf("sharing: hash portal password: %w", hErr)
			}
			add("password_hash", h)
		}
	}
	if in.Revoked != nil {
		if *in.Revoked {
			sets = append(sets, "revoked_at = NOW()")
		} else {
			sets = append(sets, "revoked_at = NULL")
		}
	}
	if len(sets) > 0 {
		args = append(args, portalID, tenantID)
		q := fmt.Sprintf("UPDATE drop_portals SET %s WHERE id = $%d AND tenant_id = $%d",
			strings.Join(sets, ", "), len(args)-1, len(args))
		if _, err := s.client.ExecContext(ctx, q, args...); err != nil {
			return nil, fmt.Errorf("sharing: update portal: %w", err)
		}
	}
	return s.portalByID(ctx, tenantID, portalID)
}

// DeletePortal permanently removes a portal row (creator or owner/admin).
func (s *Service) DeletePortal(ctx context.Context, tenantID, portalID, callerID int64, callerIsAdmin bool) error {
	var createdBy int64
	if err := s.client.QueryRowContext(ctx,
		`SELECT created_by_user_id FROM drop_portals WHERE id = $1 AND tenant_id = $2`,
		portalID, tenantID).Scan(&createdBy); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("sharing: load portal for delete: %w", err)
	}
	if !callerIsAdmin && createdBy != callerID {
		return ErrForbidden
	}
	if _, err := s.client.ExecContext(ctx,
		`DELETE FROM drop_portals WHERE id = $1 AND tenant_id = $2`, portalID, tenantID); err != nil {
		return fmt.Errorf("sharing: delete portal: %w", err)
	}
	return nil
}

// RevokePortal turns off a portal (soft). Creator or owner/admin only.
func (s *Service) RevokePortal(ctx context.Context, tenantID, portalID, callerID int64, callerIsAdmin bool) error {
	var createdBy int64
	if err := s.client.QueryRowContext(ctx,
		`SELECT created_by_user_id FROM drop_portals WHERE id = $1 AND tenant_id = $2`,
		portalID, tenantID).Scan(&createdBy); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("sharing: load portal for revoke: %w", err)
	}
	if !callerIsAdmin && createdBy != callerID {
		return ErrForbidden
	}
	if _, err := s.client.ExecContext(ctx,
		`UPDATE drop_portals SET revoked_at = NOW() WHERE id = $1 AND tenant_id = $2 AND revoked_at IS NULL`,
		portalID, tenantID); err != nil {
		return fmt.Errorf("sharing: revoke portal: %w", err)
	}
	return nil
}
