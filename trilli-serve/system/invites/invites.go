// Package invites is the magic-link onboarding path: an Owner or Member
// creates an invite tied to a tenant + role, the system hands back a
// /invite/<token> URL, and the invitee accepts it to register and join
// the tenant. SendGrid (or any other email delivery) is intentionally
// not part of this package — the link is returned to the caller for
// out-of-band delivery (copy-link in the UI, Slack, etc.). Email
// delivery will be layered on later as a side-effect of Create.
package invites

import (
	"errors"
	"time"
)

const packageName = "invites"

// Invite mirrors the invites table row.
type Invite struct {
	ID                int64      `json:"id"`
	TenantID          int64      `json:"tenant_id"`
	Email             string     `json:"email"`
	Role              string     `json:"role"`             // "admin" | "member" | "viewer"
	Token             string     `json:"token"`            // unguessable 32-byte hex
	CreatedByUserID   int64      `json:"created_by_user_id"`
	// WorkspaceIDs is the set of workspaces a Member/Viewer invitee is
	// assigned to whole on accept. FolderIDs is the set of folder-scoped
	// grants (a "folder depth" inside a workspace). A workspace is either
	// whole (in WorkspaceIDs) or folder-scoped (its folders in FolderIDs),
	// not both. Both empty for Admin invites (admins reach everything).
	WorkspaceIDs      []int64    `json:"workspace_ids,omitempty"`
	FolderIDs         []int64    `json:"folder_ids,omitempty"`
	Status            string     `json:"status"`           // pending | accepted | revoked | expired
	ExpiresAt         time.Time  `json:"expires_at"`
	CreatedAt         time.Time  `json:"created_at"`
	AcceptedAt        *time.Time `json:"accepted_at,omitempty"`
	AcceptedByUserID  *int64     `json:"accepted_by_user_id,omitempty"`
}

// LookupResult is what the accept page reads to render its banner and
// decide whether to show a fresh-signup form or a sign-in branch.
type LookupResult struct {
	Token         string `json:"token"`
	TenantID      int64  `json:"tenant_id"`
	TenantName    string `json:"tenant_name"`
	Email         string `json:"email"`
	Role          string `json:"role"`
	InviterEmail  string `json:"inviter_email"`
	ExpiresAt     time.Time `json:"expires_at"`
	// EmailHasAccount is true when the invitee's email already exists in
	// users — the accept page should then offer a sign-in branch instead
	// of asking for a fresh password.
	EmailHasAccount bool `json:"email_has_account"`
}

// Sentinel errors mapped to HTTP statuses by the handlers.
var (
	ErrInvalidEmail     = errors.New("invites: invalid email")
	ErrInvalidRole      = errors.New("invites: role must be admin, member, or viewer")
	ErrFolderRequired   = errors.New("invites: member and viewer invites require a folder grant")
	ErrInvalidFolder    = errors.New("invites: folder not found in this workspace")
	ErrWorkspaceRequired = errors.New("invites: member and viewer invites require at least one workspace")
	ErrInvalidWorkspace  = errors.New("invites: workspace not found in this account")
	ErrNotFound         = errors.New("invites: not found")
	ErrExpired          = errors.New("invites: expired")
	ErrAlreadyAccepted  = errors.New("invites: already accepted")
	ErrRevoked          = errors.New("invites: revoked")
	ErrAlreadyMember    = errors.New("invites: email is already a member of this tenant")
	ErrPasswordRequired = errors.New("invites: password required for new accounts")
	ErrWrongPassword    = errors.New("invites: existing account password did not match")
	ErrTokenForbidden   = errors.New("invites: caller is not authorized to revoke this invite")
	// Stage B — workspace invite policy enforcement.
	ErrAdminInvitesDisabled = errors.New("invites: only the workspace owner can invite people")
	ErrDomainNotAllowed     = errors.New("invites: that email domain isn't allowed in this workspace")
	// Plan seat enforcement (S5 grace). Blocks new invites once active members
	// plus outstanding pending invites would exceed the plan's max_users.
	ErrSeatLimit = errors.New("invites: your plan's seat limit is full — upgrade or remove a member to invite more people")
)
