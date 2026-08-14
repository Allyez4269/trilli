// Package sharing implements outbound shares (Sh1): a unified primitive that
// covers both internal member shares and public "anyone-with-link" shares of a
// file or folder. grantee_type is the only thing that varies. A public link is
// a share with grantee_type='public' and a token; an internal share names a
// member. Access is gated by token + optional password + optional expiry
// (capped by the plan's max_share_expiry_days), and a share can be revoked
// without losing its history.
package sharing

import (
	"errors"
	"time"

	"trilli/system/auth"
	"trilli/system/database/postgres"
	"trilli/system/files"
	"trilli/system/mailer"
	"trilli/system/metering"
	"trilli/system/storage"
)

const packageName = "sharing"

var (
	ErrNotFound         = errors.New("sharing: share not found")
	ErrResourceNotFound = errors.New("sharing: resource not found in this account")
	ErrRevoked          = errors.New("sharing: this link was turned off")
	ErrExpired          = errors.New("sharing: this link has expired")
	ErrUnavailable      = errors.New("sharing: this link is currently unavailable")
	ErrPasswordRequired = errors.New("sharing: this link requires a password")
	ErrWrongPassword    = errors.New("sharing: incorrect password")
	ErrInvalidInput     = errors.New("sharing: invalid request")
	ErrForbidden        = errors.New("sharing: not allowed")
	ErrPasswordTooShort = errors.New("sharing: a link password must be at least 8 characters")
)

// Service holds the sharing dependencies: DB, blob store (public download
// streaming), and metering (egress on public download).
type Service struct {
	client *postgres.Client
	store  storage.Store
	meter  *metering.Service
	mailer *mailer.Mailer // may be nil; only used to email magic links
	files  *files.Service // used by drop portals to land uploads (reuses guards)
}

func NewService(c *postgres.Client, store storage.Store, meter *metering.Service, m *mailer.Mailer, f *files.Service) *Service {
	return &Service{client: c, store: store, meter: meter, mailer: m, files: f}
}

// Recipient is an account member who can receive a member share (for the picker).
type Recipient struct {
	UserID int64  `json:"user_id"`
	Name   string `json:"name"`
	Email  string `json:"email"`
}

// Share is the full row for the hub + management views (with joined names).
type Share struct {
	ID             int64      `json:"id"`
	Token          string     `json:"token"`
	URL            string     `json:"url"` // /s/<token> (relative)
	ResourceType   string     `json:"resource_type"` // file | folder
	FileID         *int64     `json:"file_id,omitempty"`
	FolderID       *int64     `json:"folder_id,omitempty"`
	ResourceName   string     `json:"resource_name"`
	GranteeType    string     `json:"grantee_type"` // member | email | public
	GranteeUserID  *int64     `json:"grantee_user_id,omitempty"`
	GranteeEmail   string     `json:"grantee_email,omitempty"`
	GranteeName    string     `json:"grantee_name,omitempty"` // member display name or email
	Capability     string     `json:"capability"`             // view | download
	HasPassword    bool       `json:"has_password"`
	ExpiresAt      *time.Time `json:"expires_at,omitempty"`
	CreatedByID    int64      `json:"created_by_id"`
	CreatedByName  string     `json:"created_by_name,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	RevokedAt      *time.Time `json:"revoked_at,omitempty"`
	LastAccessedAt *time.Time `json:"last_accessed_at,omitempty"`
	AccessCount    int64      `json:"access_count"`
	Direction      string     `json:"direction"` // out (I shared) | in (shared with me)
}

// CreateInput is what Create needs. Validated before persist.
type CreateInput struct {
	TenantID int64
	ActorID  int64
	// Access is the actor's resolved folder access (identity.Access). Creating
	// a share requires READ access to the resource's location plus a non-viewer
	// role (a link extends access outside the workspace). nil skips the gate —
	// reserved for trusted internal callers; HTTP handlers always set it.
	Access        *auth.FolderAccess
	ResourceType  string // "file" | "folder"
	FileID        *int64
	FolderID      *int64
	GranteeType   string // "member" | "email" | "public"
	GranteeUserID *int64 // required for member
	GranteeEmail  string // required for email
	Capability    string // "view" | "download" (default download)
	ExpiresInDays *int   // optional; clamped to plan max_share_expiry_days
	Password      string // optional
	// SharerName personalizes the magic-link email; Notify also emails the
	// link to a member grantee (email grantees are always emailed).
	SharerName string
	Notify     bool
}
