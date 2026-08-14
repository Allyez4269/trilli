package invites

import (
	"context"
	"crypto/rand"
	"database/sql"
	"database/sql/driver"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"trilli/system/auth"
	"trilli/system/database/postgres"
	"trilli/system/logging"
	"trilli/system/session"
	"trilli/system/tenantsettings"
)

// SettingsReader supplies the workspace invite policy enforced at Create/Resend
// time. tenantsettings.Service satisfies it; kept as an interface so invites
// doesn't hard-depend on that concrete type.
type SettingsReader interface {
	Get(ctx context.Context, tenantID int64) (*tenantsettings.Settings, error)
}

// Service is the invite business logic. Composes over the postgres client
// and the session store so Accept can issue a session in one transaction.
type Service struct {
	client       *postgres.Client
	sessionStore session.Store
	settings     SettingsReader
}

// NewService wires an invites Service. settings supplies the per-workspace
// invite policy (may be nil to disable enforcement).
func NewService(c *postgres.Client, s session.Store, settings SettingsReader) *Service {
	return &Service{client: c, sessionStore: s, settings: settings}
}

// ----- Create --------------------------------------------------------------

// CreateInput is what Create needs.
type CreateInput struct {
	TenantID    int64
	InviterID   int64
	InviterRole string // caller's tenant role — gates owner-only invite policy
	Email       string
	Role        string // "admin" | "member" | "viewer" (empty → workspace default)
	// WorkspaceIDs is the set of workspaces a Member/Viewer invitee is
	// assigned to whole on accept. FolderIDs scopes access to a folder depth
	// inside a workspace instead. At least one workspace OR folder is required
	// for those roles; both ignored for Admin. Ids are validated before persist.
	WorkspaceIDs []int64
	FolderIDs    []int64
}

// Create issues an invite. Replaces any existing pending invite for the
// same (tenant, email) — re-inviting the same address invalidates the
// previous link rather than stacking duplicates.
func (s *Service) Create(ctx context.Context, in CreateInput) (*Invite, error) {
	email := normalizeEmail(in.Email)
	if email == "" || !strings.Contains(email, "@") {
		return nil, ErrInvalidEmail
	}

	// Workspace invite policy (default role, owner-only gate, domain allowlist).
	var policy *tenantsettings.Settings
	if s.settings != nil {
		p, perr := s.settings.Get(ctx, in.TenantID)
		if perr != nil {
			return nil, fmt.Errorf("invites: load settings: %w", perr)
		}
		policy = p
	}

	// Default role from policy when the request omits one.
	roleInput := strings.TrimSpace(in.Role)
	if roleInput == "" && policy != nil {
		roleInput = policy.DefaultInviteRole
	}
	role := strings.ToLower(roleInput)
	if role != "admin" && role != "member" && role != "viewer" {
		return nil, ErrInvalidRole
	}

	// When admin-invites are off, only the owner may invite.
	if policy != nil && !policy.AllowAdminInvites &&
		!strings.EqualFold(strings.TrimSpace(in.InviterRole), "owner") {
		return nil, ErrAdminInvitesDisabled
	}

	// Domain allowlist (exact match) when configured.
	if policy != nil && len(policy.InviteDomains) > 0 && !domainAllowed(email, policy.InviteDomains) {
		return nil, ErrDomainNotAllowed
	}

	// Member/Viewer invites need at least one grant — a whole workspace and/or
	// a folder depth inside one. Admin invites reach everything, so any supplied
	// ids are ignored.
	var workspaceIDs, folderIDs []int64
	if role != "admin" {
		if len(in.WorkspaceIDs) == 0 && len(in.FolderIDs) == 0 {
			return nil, ErrWorkspaceRequired
		}
		if len(in.WorkspaceIDs) > 0 {
			dedup, err := s.validateTenantWorkspaces(ctx, in.TenantID, in.WorkspaceIDs)
			if err != nil {
				return nil, err
			}
			workspaceIDs = dedup
		}
		if len(in.FolderIDs) > 0 {
			if err := s.validateTenantFolders(ctx, in.TenantID, in.FolderIDs); err != nil {
				return nil, err
			}
			dedup, err := s.dedupeFolderSet(ctx, in.FolderIDs)
			if err != nil {
				return nil, err
			}
			folderIDs = dedup
		}
	}

	// Refuse if this email is already an active member of the tenant.
	var already bool
	if err := s.client.QueryRowContext(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM tenant_members tm
			  JOIN users u ON u.id = tm.user_id
			 WHERE tm.tenant_id = $1 AND tm.status = 'active'
			   AND lower(u.email) = $2
		)`, in.TenantID, email).Scan(&already); err != nil {
		return nil, fmt.Errorf("invites: membership check: %w", err)
	}
	if already {
		return nil, ErrAlreadyMember
	}

	// Plan seat limit (S5 grace). A downgrade can leave a tenant over its new
	// plan's seat cap; never auto-remove anyone, but block growth — refuse a
	// new invite once active members + outstanding pending invites would meet
	// or exceed max_users. NULL max_users = unlimited (no check).
	if err := s.checkSeatLimit(ctx, in.TenantID); err != nil {
		return nil, err
	}

	tok, err := newToken()
	if err != nil {
		return nil, fmt.Errorf("invites: token: %w", err)
	}

	// The partial unique index (tenant_id, lower(email)) WHERE status='pending'
	// means we can't INSERT a second pending row. Soft-revoke any existing
	// pending invite for this email first so the new one slots in cleanly.
	if _, err := s.client.ExecContext(ctx, `
		UPDATE invites SET status = 'revoked'
		 WHERE tenant_id = $1 AND lower(email) = $2 AND status = 'pending'`,
		in.TenantID, email,
	); err != nil {
		return nil, fmt.Errorf("invites: revoke prior pending: %w", err)
	}

	var inv Invite
	err = s.client.QueryRowContext(ctx, `
		INSERT INTO invites (tenant_id, email, role, token, created_by_user_id)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, tenant_id, email, role, token, created_by_user_id, status,
		          expires_at, created_at, accepted_at, accepted_by_user_id`,
		in.TenantID, email, role, tok, in.InviterID,
	).Scan(&inv.ID, &inv.TenantID, &inv.Email, &inv.Role, &inv.Token,
		&inv.CreatedByUserID, &inv.Status, &inv.ExpiresAt, &inv.CreatedAt,
		&inv.AcceptedAt, &inv.AcceptedByUserID)
	if err != nil {
		return nil, fmt.Errorf("invites: insert: %w", err)
	}

	// Store whole-workspace assignments and folder-scoped grants. Both empty
	// for admin.
	for _, wid := range workspaceIDs {
		if _, err := s.client.ExecContext(ctx,
			`INSERT INTO invite_workspaces (invite_id, workspace_id) VALUES ($1, $2)
			 ON CONFLICT DO NOTHING`,
			inv.ID, wid,
		); err != nil {
			return nil, fmt.Errorf("invites: insert workspace assignment: %w", err)
		}
	}
	for _, fid := range folderIDs {
		if _, err := s.client.ExecContext(ctx,
			`INSERT INTO invite_folders (invite_id, folder_id) VALUES ($1, $2)
			 ON CONFLICT DO NOTHING`,
			inv.ID, fid,
		); err != nil {
			return nil, fmt.Errorf("invites: insert folder grant: %w", err)
		}
	}
	inv.WorkspaceIDs = workspaceIDs
	inv.FolderIDs = folderIDs

	logging.Info(packageName, "Create: tenant=%d inviter=%d email=%s role=%s workspaces=%v folders=%v",
		in.TenantID, in.InviterID, email, role, workspaceIDs, folderIDs)
	return &inv, nil
}

// checkSeatLimit refuses a new invite when the tenant is at or over its plan's
// seat cap. Seats consumed = active members + outstanding pending invites, so
// a burst of invites can't collectively overshoot the cap. A NULL max_users
// means unlimited (the check is skipped). Returns ErrSeatLimit when full.
func (s *Service) checkSeatLimit(ctx context.Context, tenantID int64) error {
	// Effective cap = seats included with the plan + any purchased extra seats.
	var maxUsers sql.NullInt64
	var extraSeats int64
	if err := s.client.QueryRowContext(ctx, `
		SELECT p.max_users, COALESCE(t.extra_seats, 0)
		  FROM tenants t JOIN plans p ON p.id = t.plan_id
		 WHERE t.id = $1`, tenantID).Scan(&maxUsers, &extraSeats); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil // no plan resolved — don't block
		}
		return fmt.Errorf("invites: load seat cap: %w", err)
	}
	if !maxUsers.Valid {
		return nil // unlimited
	}
	seatCap := maxUsers.Int64 + extraSeats
	var used int64
	if err := s.client.QueryRowContext(ctx, `
		SELECT
		  (SELECT count(*) FROM tenant_members WHERE tenant_id = $1 AND status = 'active')
		+ (SELECT count(*) FROM invites        WHERE tenant_id = $1 AND status = 'pending')`,
		tenantID).Scan(&used); err != nil {
		return fmt.Errorf("invites: count seats: %w", err)
	}
	if used >= seatCap {
		return ErrSeatLimit
	}
	return nil
}

// ----- Lookup --------------------------------------------------------------

// Lookup reads an invite by token. Side-effect: if the invite is pending
// but past its expiry, status is flipped to 'expired' opportunistically
// so the next call returns the right state.
func (s *Service) Lookup(ctx context.Context, token string) (*LookupResult, error) {
	if strings.TrimSpace(token) == "" {
		return nil, ErrNotFound
	}

	var (
		out          LookupResult
		status       string
		emailHas     bool
	)
	err := s.client.QueryRowContext(ctx, `
		SELECT i.token, i.tenant_id, t.name, i.email, i.role,
		       u_inv.email, i.expires_at, i.status,
		       EXISTS(SELECT 1 FROM users u2 WHERE lower(u2.email) = lower(i.email))
		  FROM invites i
		  JOIN tenants t ON t.id = i.tenant_id
		  JOIN users u_inv ON u_inv.id = i.created_by_user_id
		 WHERE i.token = $1`, token,
	).Scan(&out.Token, &out.TenantID, &out.TenantName, &out.Email, &out.Role,
		&out.InviterEmail, &out.ExpiresAt, &status, &emailHas)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("invites: lookup: %w", err)
	}
	out.EmailHasAccount = emailHas

	switch status {
	case "accepted":
		return nil, ErrAlreadyAccepted
	case "revoked":
		return nil, ErrRevoked
	}
	if time.Now().After(out.ExpiresAt) {
		_, _ = s.client.ExecContext(ctx,
			`UPDATE invites SET status = 'expired' WHERE token = $1 AND status = 'pending'`,
			token)
		return nil, ErrExpired
	}
	return &out, nil
}

// ----- Accept --------------------------------------------------------------

// AcceptInput is what Accept needs. First/LastName + Password are required
// only for new accounts; an existing-account branch supplies only Password.
type AcceptInput struct {
	Token     string
	FirstName string
	LastName  string
	Password  string
	IP        string
	UserAgent string
}

// AcceptResult is what the handler turns into a session cookie + redirect.
type AcceptResult struct {
	// Session is nil when TwoFARequired — the membership was created but the
	// caller must sign in through the normal login flow (which enforces the
	// second factor) instead of receiving a password-only session here.
	Session       *session.Session
	UserID        int64
	TenantID      int64
	Role          string
	IsNewUser     bool
	TwoFARequired bool
}

// Accept consumes an invite token: creates the user row if the email is
// new (or verifies the existing password if it's an existing user),
// inserts the tenant_members row at the invite's role, marks the invite
// accepted, and issues a tenant-scoped session. All in one transaction so
// a partial failure (e.g., session create) doesn't leave the user joined
// to a tenant without a cookie.
func (s *Service) Accept(ctx context.Context, in AcceptInput) (*AcceptResult, error) {
	if strings.TrimSpace(in.Password) == "" {
		return nil, ErrPasswordRequired
	}

	tx, err := s.client.GetDB().BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("invites: begin tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	// Lock the invite row so two concurrent accepts can't both succeed.
	var (
		invID, tenantID, inviterID int64
		email, role, status        string
		expiresAt                  time.Time
	)
	err = tx.QueryRowContext(ctx, `
		SELECT id, tenant_id, email, role, status, expires_at, created_by_user_id
		  FROM invites
		 WHERE token = $1
		 FOR UPDATE`, in.Token,
	).Scan(&invID, &tenantID, &email, &role, &status, &expiresAt, &inviterID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("invites: load for accept: %w", err)
	}
	switch status {
	case "accepted":
		return nil, ErrAlreadyAccepted
	case "revoked":
		return nil, ErrRevoked
	}
	if time.Now().After(expiresAt) {
		return nil, ErrExpired
	}

	// Resolve the user — create if new, verify password if existing.
	var (
		userID        int64
		isNewUser     bool
		twoFARequired bool
	)
	var existingHash string
	err = tx.QueryRowContext(ctx,
		`SELECT id, password_hash FROM users WHERE lower(email) = lower($1)`,
		email,
	).Scan(&userID, &existingHash)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		// New user — create with the supplied password.
		hash, err := auth.HashPassword(in.Password)
		if err != nil {
			return nil, fmt.Errorf("invites: hash: %w", err)
		}
		first := strings.TrimSpace(in.FirstName)
		last := strings.TrimSpace(in.LastName)
		full := strings.TrimSpace(first + " " + last)
		newID, err := auth.GenerateUserID()
		if err != nil {
			return nil, fmt.Errorf("invites: generate user id: %w", err)
		}
		// full_name is stored too (NULLIF for the empty case) so audit /
		// mailer / Members keep reading a single name field.
		if err := tx.QueryRowContext(ctx, `
			INSERT INTO users (id, email, password_hash, password_algo, first_name, last_name, full_name, status)
			VALUES ($1, $2, $3, 'argon2id', NULLIF($4, ''), NULLIF($5, ''), NULLIF($6, ''), 'active')
			RETURNING id`, newID, email, hash, first, last, full,
		).Scan(&userID); err != nil {
			return nil, fmt.Errorf("invites: insert user: %w", err)
		}
		isNewUser = true

	case err != nil:
		return nil, fmt.Errorf("invites: lookup user: %w", err)

	default:
		// Existing user — verify password (sign-in branch).
		ok, vErr := auth.VerifyPassword(existingHash, in.Password)
		if vErr != nil || !ok {
			return nil, ErrWrongPassword
		}
		// If the account has a second factor enrolled (TOTP or a passkey),
		// accepting an invite must NOT mint a password-only session — that
		// would bypass the 2FA gate every other sign-in path enforces. The
		// membership is still created below; the user signs in normally
		// (with their second factor) afterwards.
		if enrolled, eErr := s.userHasSecondFactor(ctx, tx, userID); eErr != nil {
			return nil, eErr
		} else if enrolled {
			twoFARequired = true
		}
	}

	// Refuse if already an active member of this tenant (race / duplicate).
	var already bool
	if err := tx.QueryRowContext(ctx, `
		SELECT EXISTS(SELECT 1 FROM tenant_members
		               WHERE tenant_id = $1 AND user_id = $2 AND status = 'active')`,
		tenantID, userID).Scan(&already); err != nil {
		return nil, fmt.Errorf("invites: membership check: %w", err)
	}
	if already {
		return nil, ErrAlreadyMember
	}

	// Insert membership at the invite's role and capture the new row id
	// so we can attach folder grants in the junction table.
	var membershipID int64
	if err := tx.QueryRowContext(ctx, `
		INSERT INTO tenant_members (tenant_id, user_id, role, status, joined_at, invited_by_user_id, invited_at)
		VALUES ($1, $2, $3, 'active', NOW(), $4, NOW())
		RETURNING id`,
		tenantID, userID, role, inviterID,
	).Scan(&membershipID); err != nil {
		return nil, fmt.Errorf("invites: insert membership: %w", err)
	}

	// Assign the invitee to the invite's whole workspaces (keyed by user).
	// Admins have no rows in invite_workspaces, so this does nothing for them.
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO workspace_members (workspace_id, user_id, assigned_by_user_id)
		SELECT workspace_id, $1, $2 FROM invite_workspaces WHERE invite_id = $3
		ON CONFLICT (workspace_id, user_id) DO NOTHING`,
		userID, inviterID, invID,
	); err != nil {
		return nil, fmt.Errorf("invites: assign workspaces: %w", err)
	}

	// Copy folder-scoped grants into the member's set (keyed by membership).
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO tenant_member_folders (tenant_member_id, folder_id)
		SELECT $1, folder_id FROM invite_folders WHERE invite_id = $2`,
		membershipID, invID,
	); err != nil {
		return nil, fmt.Errorf("invites: copy folder grants: %w", err)
	}

	// Mark invite consumed.
	if _, err := tx.ExecContext(ctx, `
		UPDATE invites
		   SET status = 'accepted', accepted_at = NOW(), accepted_by_user_id = $1
		 WHERE id = $2`,
		userID, invID,
	); err != nil {
		return nil, fmt.Errorf("invites: mark accepted: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("invites: commit: %w", err)
	}
	committed = true

	// 2FA-enrolled accounts don't get a session here — the membership is in
	// place; the user signs in through /login where the second factor (and
	// trusted-device handling) is enforced.
	if twoFARequired {
		logging.Info(packageName, "Accept: tenant=%d user=%d role=%s — 2FA enrolled, deferring to login",
			tenantID, userID, role)
		return &AcceptResult{
			UserID:        userID,
			TenantID:      tenantID,
			Role:          role,
			IsNewUser:     false,
			TwoFARequired: true,
		}, nil
	}

	// Update last_login_at outside the tx — non-critical.
	_, _ = s.client.ExecContext(ctx,
		`UPDATE users SET last_login_at = NOW() WHERE id = $1`, userID)

	// Issue a tenant-scoped session.
	tIDCopy := tenantID
	sess, err := s.sessionStore.Create(ctx, session.CreateInput{
		UserID: userID, TenantID: &tIDCopy, Scope: session.ScopeTenant,
		IP: in.IP, UserAgent: in.UserAgent, TTL: session.DefaultTTL,
	})
	if err != nil {
		return nil, fmt.Errorf("invites: create session: %w", err)
	}

	logging.Info(packageName, "Accept: tenant=%d user=%d role=%s new=%v",
		tenantID, userID, role, isNewUser)
	return &AcceptResult{
		Session:   sess,
		UserID:    userID,
		TenantID:  tenantID,
		Role:      role,
		IsNewUser: isNewUser,
	}, nil
}

// userHasSecondFactor reports whether the user has confirmed TOTP or at least
// one passkey. Queried directly (mirroring auth's userHasPasskey) so invites
// doesn't need the twofactor/passkeys services.
func (s *Service) userHasSecondFactor(ctx context.Context, tx *sql.Tx, userID int64) (bool, error) {
	var enrolled bool
	err := tx.QueryRowContext(ctx, `
		SELECT EXISTS(SELECT 1 FROM user_totp WHERE user_id = $1 AND confirmed_at IS NOT NULL)
		    OR EXISTS(SELECT 1 FROM user_passkeys WHERE user_id = $1)`,
		userID).Scan(&enrolled)
	if err != nil {
		return false, fmt.Errorf("invites: check second factor: %w", err)
	}
	return enrolled, nil
}

// ----- ListPending ---------------------------------------------------------

// ListPending returns every still-pending invite for the tenant, newest
// first. Excludes accepted/revoked/expired rows — those are noise for
// the "Pending Invites" UI panel.
func (s *Service) ListPending(ctx context.Context, tenantID int64) ([]*Invite, error) {
	rows, err := s.client.QueryContext(ctx, `
		SELECT id, tenant_id, email, role, token, created_by_user_id, status,
		       expires_at, created_at, accepted_at, accepted_by_user_id
		  FROM invites
		 WHERE tenant_id = $1 AND status = 'pending' AND expires_at > NOW()
		 ORDER BY created_at DESC, id DESC`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("invites: list pending: %w", err)
	}
	defer rows.Close()

	out := make([]*Invite, 0)
	for rows.Next() {
		var inv Invite
		if err := rows.Scan(&inv.ID, &inv.TenantID, &inv.Email, &inv.Role, &inv.Token,
			&inv.CreatedByUserID, &inv.Status, &inv.ExpiresAt, &inv.CreatedAt,
			&inv.AcceptedAt, &inv.AcceptedByUserID); err != nil {
			return nil, fmt.Errorf("invites: scan pending: %w", err)
		}
		out = append(out, &inv)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("invites: pending rows: %w", err)
	}
	// Hydrate workspace assignments per invite. One round trip per invite is
	// fine at expected sizes (pending lists are short); collapse to a
	// single JOIN later if it ever becomes a hotspot.
	for _, inv := range out {
		wids, err := s.loadInviteWorkspaceIDs(ctx, inv.ID)
		if err != nil {
			return nil, err
		}
		inv.WorkspaceIDs = wids
		fids, err := s.loadInviteFolderIDs(ctx, inv.ID)
		if err != nil {
			return nil, err
		}
		inv.FolderIDs = fids
	}
	return out, nil
}

// loadInviteWorkspaceIDs reads the whole-workspace junction rows for one invite.
func (s *Service) loadInviteWorkspaceIDs(ctx context.Context, inviteID int64) ([]int64, error) {
	return s.loadInviteJunction(ctx,
		`SELECT workspace_id FROM invite_workspaces WHERE invite_id = $1 ORDER BY workspace_id`,
		inviteID)
}

// loadInviteFolderIDs reads the folder-grant junction rows for one invite.
func (s *Service) loadInviteFolderIDs(ctx context.Context, inviteID int64) ([]int64, error) {
	return s.loadInviteJunction(ctx,
		`SELECT folder_id FROM invite_folders WHERE invite_id = $1 ORDER BY folder_id`,
		inviteID)
}

func (s *Service) loadInviteJunction(ctx context.Context, query string, inviteID int64) ([]int64, error) {
	rows, err := s.client.QueryContext(ctx, query, inviteID)
	if err != nil {
		return nil, fmt.Errorf("invites: load junction: %w", err)
	}
	defer rows.Close()
	out := []int64{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("invites: scan junction id: %w", err)
		}
		out = append(out, id)
	}
	return out, nil
}

// ----- Resend --------------------------------------------------------------

// Resend bumps the expires_at on a still-pending invite so the Owner
// can re-fire the email (which the handler does after this call
// returns). Refuses to "resend" an invite that's already accepted or
// revoked — caller would need to issue a fresh invite for those cases.
// Resending an EXPIRED invite is allowed: we flip it back to pending
// and extend the window, since the typical case is "I forgot to send
// this last week, let me try again".
//
// Returns the updated invite so the handler can rebuild the link and
// fire SendGrid.
func (s *Service) Resend(ctx context.Context, tenantID, inviteID int64, inviterRole string) (*Invite, error) {
	// Same owner-only gate as Create: a non-owner admin can't resend when
	// admin-invites are disabled.
	if s.settings != nil {
		if policy, err := s.settings.Get(ctx, tenantID); err != nil {
			return nil, fmt.Errorf("invites: load settings: %w", err)
		} else if !policy.AllowAdminInvites && !strings.EqualFold(strings.TrimSpace(inviterRole), "owner") {
			return nil, ErrAdminInvitesDisabled
		}
	}

	var inv Invite
	err := s.client.QueryRowContext(ctx, `
		UPDATE invites
		   SET status = 'pending',
		       expires_at = NOW() + INTERVAL '7 days'
		 WHERE id = $1 AND tenant_id = $2
		   AND status IN ('pending', 'expired')
		RETURNING id, tenant_id, email, role, token, created_by_user_id, status,
		          expires_at, created_at, accepted_at, accepted_by_user_id`,
		inviteID, tenantID,
	).Scan(&inv.ID, &inv.TenantID, &inv.Email, &inv.Role, &inv.Token,
		&inv.CreatedByUserID, &inv.Status, &inv.ExpiresAt, &inv.CreatedAt,
		&inv.AcceptedAt, &inv.AcceptedByUserID)
	if errors.Is(err, sql.ErrNoRows) {
		// Either the row doesn't exist or it's accepted/revoked — both
		// are "can't resend" from the user's perspective.
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("invites: resend: %w", err)
	}
	wids, lerr := s.loadInviteWorkspaceIDs(ctx, inv.ID)
	if lerr != nil {
		return nil, lerr
	}
	inv.WorkspaceIDs = wids
	if fids, ferr := s.loadInviteFolderIDs(ctx, inv.ID); ferr != nil {
		return nil, ferr
	} else {
		inv.FolderIDs = fids
	}
	logging.Info(packageName, "Resend: tenant=%d invite=%d email=%s",
		tenantID, inviteID, inv.Email)
	return &inv, nil
}

// ----- Revoke --------------------------------------------------------------

// Revoke marks a pending invite as revoked. Caller must belong to the
// invite's tenant.
func (s *Service) Revoke(ctx context.Context, callerTenantID, inviteID int64) error {
	res, err := s.client.ExecContext(ctx, `
		UPDATE invites SET status = 'revoked'
		 WHERE id = $1 AND tenant_id = $2 AND status = 'pending'`,
		inviteID, callerTenantID,
	)
	if err != nil {
		return fmt.Errorf("invites: revoke: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ----- helpers -------------------------------------------------------------

// validateTenantFolders returns an error if any id in the slice is
// not an active folder in the given tenant. Empty slice is allowed
// (caller decides whether that's an error).
func (s *Service) validateTenantFolders(ctx context.Context, tenantID int64, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	rows, err := s.client.QueryContext(ctx,
		`SELECT id FROM folders WHERE tenant_id = $1 AND status = 'active' AND id = ANY($2)`,
		tenantID, int64Slice(ids),
	)
	if err != nil {
		return fmt.Errorf("invites: validate folders: %w", err)
	}
	defer rows.Close()
	found := make(map[int64]bool, len(ids))
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return fmt.Errorf("invites: scan folder id: %w", err)
		}
		found[id] = true
	}
	for _, id := range ids {
		if !found[id] {
			return ErrInvalidFolder
		}
	}
	return nil
}

// validateTenantWorkspaces checks every id is an active workspace in the
// account and returns the de-duplicated set. Errors with ErrInvalidWorkspace
// if any id doesn't belong.
func (s *Service) validateTenantWorkspaces(ctx context.Context, tenantID int64, ids []int64) ([]int64, error) {
	uniq := make([]int64, 0, len(ids))
	seen := make(map[int64]bool, len(ids))
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			uniq = append(uniq, id)
		}
	}
	rows, err := s.client.QueryContext(ctx,
		`SELECT id FROM workspaces WHERE tenant_id = $1 AND status = 'active' AND id = ANY($2)`,
		tenantID, int64Slice(uniq),
	)
	if err != nil {
		return nil, fmt.Errorf("invites: validate workspaces: %w", err)
	}
	defer rows.Close()
	found := make(map[int64]bool, len(uniq))
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("invites: scan workspace id: %w", err)
		}
		found[id] = true
	}
	for _, id := range uniq {
		if !found[id] {
			return nil, ErrInvalidWorkspace
		}
	}
	return uniq, nil
}

// dedupeFolderSet removes any id whose ancestor is also in the
// selected set, so the returned slice carries only the topmost
// folder per branch. Walks parents via a recursive CTE per id, which
// is O(N * depth) — fine for the small selection sizes we expect.
func (s *Service) dedupeFolderSet(ctx context.Context, ids []int64) ([]int64, error) {
	if len(ids) <= 1 {
		return ids, nil
	}
	selected := make(map[int64]bool, len(ids))
	for _, id := range ids {
		selected[id] = true
	}
	out := make([]int64, 0, len(ids))
	for _, id := range ids {
		// Walk up the parent chain; if any ancestor is also selected,
		// this id is redundant.
		rows, err := s.client.QueryContext(ctx, `
			WITH RECURSIVE chain AS (
				SELECT parent_folder_id FROM folders WHERE id = $1
				UNION ALL
				SELECT f.parent_folder_id FROM folders f
				  JOIN chain c ON f.id = c.parent_folder_id
				 WHERE c.parent_folder_id IS NOT NULL
			)
			SELECT parent_folder_id FROM chain WHERE parent_folder_id IS NOT NULL`,
			id,
		)
		if err != nil {
			return nil, fmt.Errorf("invites: walk ancestors: %w", err)
		}
		redundant := false
		for rows.Next() {
			var parent int64
			if err := rows.Scan(&parent); err != nil {
				rows.Close()
				return nil, fmt.Errorf("invites: scan ancestor: %w", err)
			}
			if selected[parent] {
				redundant = true
				break
			}
		}
		rows.Close()
		if !redundant {
			out = append(out, id)
		}
	}
	return out, nil
}

// int64Slice converts []int64 to a pq-friendly slice for ANY($N).
// The pgx stdlib driver accepts a plain []int64 but go's database/sql
// requires us to be explicit; this helper exists so the call site
// stays readable.
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

func normalizeEmail(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// domainAllowed reports whether the email's domain is in the allowlist. Exact,
// case-insensitive match — "acme.com" does NOT cover "eng.acme.com".
func domainAllowed(email string, allowed []string) bool {
	at := strings.LastIndex(email, "@")
	if at < 0 {
		return false
	}
	dom := strings.ToLower(strings.TrimSpace(email[at+1:]))
	for _, a := range allowed {
		if strings.EqualFold(strings.TrimSpace(a), dom) {
			return true
		}
	}
	return false
}

func newToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
