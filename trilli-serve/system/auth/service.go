package auth

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"math/big"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	appcrypto "trilli/system/crypto"
	"trilli/system/database/postgres"
	"trilli/system/logging"
	"trilli/system/session"
)

// Tenant IDs: 12-digit decimal random integers, range [100B, 1T).
// User IDs:   15-digit decimal random integers, range [100T, 1Q). 15 digits
//
//	still fit safely in JS Number.MAX_SAFE_INTEGER (~9 * 10^15), so
//	no string-encoding is needed on the JSON wire.
//
// In both cases the DB no longer auto-increments — Go generates the value
// via crypto/rand and retries on unique-constraint violation.
const (
	tenantIDMin            int64 = 100_000_000_000 // smallest 12-digit value
	tenantIDRange          int64 = 900_000_000_000 // 999_999_999_999 - 100_000_000_000 + 1
	tenantInsertMaxRetries       = 10

	userIDMin            int64 = 100_000_000_000_000 // smallest 15-digit value
	userIDRange          int64 = 900_000_000_000_000 // 999_999_999_999_999 - 100_000_000_000_000 + 1
	userInsertMaxRetries       = 10
)

// GenerateTenantID returns a fresh 12-digit tenant id. Collision-checking is
// the caller's responsibility — typically by attempting the INSERT and retrying
// on a unique-constraint violation.
func GenerateTenantID() (int64, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(tenantIDRange))
	if err != nil {
		return 0, fmt.Errorf("auth: random tenant id: %w", err)
	}
	return n.Int64() + tenantIDMin, nil
}

// GenerateUserID returns a fresh 15-digit user id.
func GenerateUserID() (int64, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(userIDRange))
	if err != nil {
		return 0, fmt.Errorf("auth: random user id: %w", err)
	}
	return n.Int64() + userIDMin, nil
}

const packageName = "auth"

// Sentinel errors. Handlers map these to HTTP statuses.
var (
	ErrInvalidCredentials = errors.New("auth: invalid credentials")
	ErrEmailTaken         = errors.New("auth: email already in use")
	ErrEmailInvalid       = errors.New("auth: invalid email")
	ErrPasswordTooShort   = errors.New("auth: password too short")
	ErrUnauthorized       = errors.New("auth: unauthorized")
	ErrUserSuspended      = errors.New("auth: account suspended")
	// Federated-login binding failures (ResolveOAuthLogin).
	ErrOAuthIdentityMismatch  = errors.New("auth: this provider identity doesn't match the account on this email")
	ErrOAuthDifferentProvider = errors.New("auth: this account signs in with a different method")
	ErrOAuthUnverified        = errors.New("auth: provider didn't verify this email, and the account isn't linked to it")
	ErrInvalidPendingToken    = errors.New("auth: invalid or expired 2FA session")
	ErrInvalidStepUpToken     = errors.New("auth: invalid or expired step-up token")
	ErrTenantSuspended        = errors.New("auth: tenant suspended")
	ErrNoActiveTenant         = errors.New("auth: user has no active tenant membership")
	ErrPlanNotFound           = errors.New("auth: plan not found")
	ErrPlanUnavailable        = errors.New("auth: plan is not available")
	ErrMemberNotFound         = errors.New("auth: member not found in tenant")
	ErrCannotRemoveOwner      = errors.New("auth: cannot remove the workspace owner")
	ErrCannotRemoveSelf       = errors.New("auth: cannot remove yourself")
	ErrInvalidStatus          = errors.New("auth: invalid status")
	ErrInvalidFolder          = errors.New("auth: folder not found in this workspace")
	ErrCannotDisableOwner     = errors.New("auth: cannot disable the workspace owner")
	ErrCannotChangeSelfStatus = errors.New("auth: cannot change your own status")
	ErrAccountDisabled        = errors.New("auth: your account has been disabled — please contact your workspace owner")
	ErrInvalidName            = errors.New("auth: first and last name are required")
	ErrAvatarNotFound         = errors.New("auth: no avatar set")
	ErrWorkspaceNameRequired  = errors.New("auth: workspace name is required")
	ErrSlugInvalid            = errors.New("auth: that workspace URL isn't valid — use letters, numbers, and hyphens")
	ErrSlugReserved           = errors.New("auth: that workspace URL is reserved — please choose another")
	ErrSlugTaken              = errors.New("auth: that workspace URL is already taken")
)

// reservedSlugs are workspace-URL namespaces we never hand out: app route
// prefixes and common system names. Keeping tenant slugs out of this set means
// a workspace URL can never shadow (or be shadowed by) an application path when
// the per-workspace web service is built later.
var reservedSlugs = map[string]bool{
	"api": true, "app": true, "www": true, "admin": true, "dashboard": true,
	"login": true, "logout": true, "signup": true, "signin": true, "register": true,
	"invite": true, "confirm-email": true, "settings": true, "profile": true,
	"files": true, "recent": true, "starred": true, "shared": true, "trash": true,
	"members": true, "activity": true, "usage": true, "billing": true, "plan": true,
	"help": true, "support": true, "about": true, "status": true, "mail": true,
	"static": true, "assets": true, "public": true, "auth": true, "workspace": true,
}

// User mirrors the users table (omitting password fields from the public type).
type User struct {
	ID          int64      `json:"id"`
	Email       string     `json:"email"`
	FirstName   *string    `json:"first_name,omitempty"` // captured at invite acceptance
	LastName    *string    `json:"last_name,omitempty"`
	FullName    *string    `json:"full_name,omitempty"`  // derived "first last"; kept for back-compat
	AvatarURL   *string    `json:"avatar_url,omitempty"` // serving URL (with ?v= cache-bust) when an avatar is set
	IsSuperuser bool       `json:"is_superuser"`
	Status      string     `json:"status"`
	CreatedAt   time.Time  `json:"created_at"`
	LastLoginAt *time.Time `json:"last_login_at,omitempty"`
}

// Tenant is the minimal tenant info attached to sessions.
type Tenant struct {
	ID                  int64      `json:"id"`
	Name                string     `json:"name"`
	Slug                string     `json:"slug"`
	PlanID              int64      `json:"plan_id"`
	PlanName            string     `json:"plan_name"`         // joined from plans; lets the UI show the tier without a separate quota fetch
	MaxStorageBytes     int64      `json:"max_storage_bytes"` // plan storage cap (max int64 = unlimited)
	APIAccess           bool       `json:"api_access"`        // plan includes programmatic API access
	Status              string     `json:"status"`
	StorageBytesUsed    int64      `json:"storage_bytes_used"`
	UserCount           int        `json:"user_count"`
	CreatedAt           time.Time  `json:"created_at"`
	LockedPriceCents    *int       `json:"locked_price_cents,omitempty"`    // snapshot-locked at plan assignment (hybrid model)
	LockedBillingPeriod string     `json:"locked_billing_period,omitempty"` // 'monthly' | 'annual'
	LifecycleState      string     `json:"lifecycle_state"`                 // 'current' | 'lapsed' (read-only before purge)
	PurgeAt             *time.Time `json:"purge_at,omitempty"`              // when a lapsed account's data is deleted
}

// Membership describes a user's role in a tenant.
type Membership struct {
	ID       int64  `json:"-"` // tenant_members.id — used to resolve folder grants; not part of the public API
	TenantID int64  `json:"tenant_id"`
	Role     string `json:"role"`
	Status   string `json:"status"`
}

// Identity is what a valid session resolves to.
type Identity struct {
	User       *User
	Session    *session.Session
	Tenant     *Tenant       // nil for super-admin sessions
	Membership *Membership   // nil for super-admin sessions
	Access     *FolderAccess // nil for super-admin sessions; always set for tenant sessions
}

// FolderAccess captures what folders the current identity may touch and
// whether it may write. It is resolved once per request inside
// LoadIdentity so every handler sees a consistent, current view.
//
// Access model:
//   - owner / admin              → Full = true  (entire workspace, read + write)
//   - member                     → Full = false, Write = true,  scoped to Folders
//   - viewer                     → Full = false, Write = false, scoped to Folders
//
// For scoped roles, Folders holds the FULL accessible set: every granted
// folder plus all of its descendants, expanded at load time. A scoped role
// with no grants therefore has an empty set and can see nothing — which
// matches the invite rule that every member/viewer must be granted at
// least one folder.
type FolderAccess struct {
	Full    bool           // owner/admin — unrestricted
	Write   bool           // false for viewer (read-only)
	Folders map[int64]bool // accessible folder ids (granted + descendants); only meaningful when !Full
	// Workspaces holds the ids of workspaces this member is assigned to WHOLESALE
	// (workspace_members). Unlike folder-scoped grants, a whole-workspace member
	// may also write at that workspace's ROOT (create top-level folders, upload
	// root files) — the workspace itself is their granted container, so root
	// operations inside it are within the grant. Only meaningful when !Full.
	Workspaces map[int64]bool
}

// CanRead reports whether the identity may read/list the given folder.
// folderID nil means the tenant root: a scoped role has no blanket root
// access (the browse handler surfaces their granted folders explicitly
// instead), so root reads are denied for scoped roles.
func (a *FolderAccess) CanRead(folderID *int64) bool {
	if a == nil || a.Full {
		return true
	}
	if folderID == nil {
		return false
	}
	return a.Folders[*folderID]
}

// CanWrite reports whether the identity may create/modify/delete within
// the given folder. Viewers can never write; other scoped roles can write
// anywhere they can read.
func (a *FolderAccess) CanWrite(folderID *int64) bool {
	if a == nil {
		return true
	}
	if !a.Write {
		return false
	}
	if a.Full {
		return true
	}
	if folderID == nil {
		return false
	}
	return a.Folders[*folderID]
}

// CanWriteAt is CanWrite with workspace context for the root case: writing at
// the WORKSPACE ROOT (folderID nil) is allowed for owners/admins and for
// members assigned the whole workspace — their grant covers the workspace
// itself, not just the folders that happen to exist in it today. (Fixes
// whole-workspace members being unable to create top-level folders or upload
// root files — and being completely stuck in a still-empty workspace.)
// Folder-scoped-only members remain denied at root, as before.
func (a *FolderAccess) CanWriteAt(folderID *int64, workspaceID *int64) bool {
	if folderID != nil || a == nil || a.Full {
		return a.CanWrite(folderID)
	}
	if !a.Write || workspaceID == nil {
		return false
	}
	return a.Workspaces[*workspaceID]
}

// CanReadAt is CanRead with workspace context for the root case: reading a
// WORKSPACE ROOT (folderID nil) is allowed for owners/admins and for scoped
// members granted that whole workspace — mirrors CanWriteAt's root semantics.
func (a *FolderAccess) CanReadAt(folderID *int64, workspaceID *int64) bool {
	if folderID != nil || a == nil || a.Full {
		return a.CanRead(folderID)
	}
	if workspaceID == nil {
		return false
	}
	return a.Workspaces[*workspaceID]
}

// Restricted reports whether this identity is scoped to a subset of folders
// (i.e. a member or viewer, not an owner/admin). Handlers use this to decide
// whether tenant-wide listings need filtering.
func (a *FolderAccess) Restricted() bool {
	return a != nil && !a.Full
}

// FolderIDs returns the accessible folder ids as a slice — convenient for
// passing into ANY($N) query filters. Empty when Full (callers should check
// Restricted first) or when a scoped role has no grants.
func (a *FolderAccess) FolderIDs() []int64 {
	if a == nil || a.Full {
		return nil
	}
	out := make([]int64, 0, len(a.Folders))
	for id := range a.Folders {
		out = append(out, id)
	}
	return out
}

// WorkspaceIDs returns the ids of workspaces this member is assigned to
// wholesale, as a slice for ANY($N) query filters. Empty when Full.
func (a *FolderAccess) WorkspaceIDs() []int64 {
	if a == nil || a.Full {
		return nil
	}
	out := make([]int64, 0, len(a.Workspaces))
	for id := range a.Workspaces {
		out = append(out, id)
	}
	return out
}

// TenantMember is the row shape returned by ListTenantMembers — one user's
// membership in a tenant, joined to their email. Used by the Users page.
type TenantMember struct {
	ID        int64     `json:"id"` // users.id
	Email     string    `json:"email"`
	FullName  *string   `json:"full_name,omitempty"` // nullable — falls back to email-titleize on the client
	Role      string    `json:"role"`                // admin | member
	Status    string    `json:"status"`              // active | invited | suspended
	CreatedAt time.Time `json:"created_at"`          // membership creation time
}

// Service wires the DB client and session store for auth business logic.
type Service struct {
	client       *postgres.Client
	sessionStore session.Store
}

// NewService constructs an auth Service.
func NewService(c *postgres.Client, s session.Store) *Service {
	return &Service{client: c, sessionStore: s}
}

// ----- Signup ---------------------------------------------------------------

// SignupInput is the payload to create a new tenant + first admin user.
type SignupInput struct {
	Email      string
	Password   string
	FirstName  string
	LastName   string
	TenantName string
}

// Signup creates user + tenant + admin tenant_member + tenant session atomically.
func (s *Service) Signup(ctx context.Context, in SignupInput, ip, ua string) (*Identity, error) {
	email := normalizeEmail(in.Email)
	if !emailRE.MatchString(email) {
		return nil, ErrEmailInvalid
	}
	if len(in.Password) < MinPasswordLen {
		return nil, ErrPasswordTooShort
	}
	tenantName := strings.TrimSpace(in.TenantName)
	if tenantName == "" {
		tenantName = "My Workspace"
	}
	baseSlug := slugify(tenantName)
	if baseSlug == "" {
		baseSlug = "workspace"
	}

	hash, err := HashPassword(in.Password)
	if err != nil {
		return nil, err
	}

	db := s.client.GetDB()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("auth: begin tx: %w", err)
	}
	defer tx.Rollback()

	// Insert the user with a 15-digit random id. Both id (PK) and email carry
	// UNIQUE constraints. Email collision means the email is already taken
	// (deterministic — return ErrEmailTaken immediately). ID collision is
	// vanishingly rare but we retry just in case.
	first := strings.TrimSpace(in.FirstName)
	last := strings.TrimSpace(in.LastName)
	full := strings.TrimSpace(first + " " + last)
	user := &User{Email: email, Status: "active"}
	if first != "" {
		user.FirstName = &first
	}
	if last != "" {
		user.LastName = &last
	}
	if full != "" {
		user.FullName = &full
	}
	var userInserted bool
	for attempt := 0; attempt < userInsertMaxRetries; attempt++ {
		candidateUserID, idErr := GenerateUserID()
		if idErr != nil {
			return nil, idErr
		}
		err = tx.QueryRowContext(ctx,
			`INSERT INTO users (id, email, password_hash, first_name, last_name, full_name)
			 VALUES ($1, $2, $3, NULLIF($4, ''), NULLIF($5, ''), NULLIF($6, ''))
			 RETURNING id, created_at`,
			candidateUserID, email, hash, first, last, full,
		).Scan(&user.ID, &user.CreatedAt)
		if err == nil {
			userInserted = true
			break
		}
		if !pgUniqueViolation(err) {
			return nil, fmt.Errorf("auth: insert user: %w", err)
		}
		if isEmailUniqueViolation(err) {
			return nil, ErrEmailTaken
		}
		// PK collision — try again with a fresh id.
	}
	if !userInserted {
		return nil, fmt.Errorf("auth: user id collision after %d retries", userInsertMaxRetries)
	}

	var planID int64
	var planPriceCents int
	err = tx.QueryRowContext(ctx,
		`SELECT id, price_monthly_cents FROM plans WHERE is_active ORDER BY sort_order ASC, id ASC LIMIT 1`,
	).Scan(&planID, &planPriceCents)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("auth: no active plan available for signup")
		}
		return nil, fmt.Errorf("auth: load default plan: %w", err)
	}

	// Insert the tenant with a 12-digit random ID. Both the ID and the slug
	// carry unique constraints; either can collide. Each attempt generates a
	// fresh ID + slug to absorb both failure modes in one retry loop.
	tenant := &Tenant{
		Name: tenantName, PlanID: planID, Status: "active", UserCount: 1,
		LockedPriceCents: &planPriceCents, LockedBillingPeriod: "monthly",
	}
	var (
		finalSlug string
		inserted  bool
	)
	for attempt := 0; attempt < tenantInsertMaxRetries; attempt++ {
		candidateID, idErr := GenerateTenantID()
		if idErr != nil {
			return nil, idErr
		}
		candidateSlug := baseSlug
		if attempt > 0 {
			candidateSlug = fmt.Sprintf("%s-%d", baseSlug, time.Now().UnixNano()&0xffff+int64(attempt))
		}

		err = tx.QueryRowContext(ctx,
			`INSERT INTO tenants (id, name, slug, plan_id, user_count,
			                      locked_price_cents, locked_billing_period, locked_at)
			 VALUES ($1, $2, $3, $4, 1, $5, 'monthly', NOW())
			 RETURNING id, created_at`,
			candidateID, tenantName, candidateSlug, planID, planPriceCents,
		).Scan(&tenant.ID, &tenant.CreatedAt)
		if err == nil {
			finalSlug = candidateSlug
			inserted = true
			break
		}
		if !pgUniqueViolation(err) {
			return nil, fmt.Errorf("auth: insert tenant: %w", err)
		}
		// else: id and/or slug collision — loop and try again with fresh values.
	}
	if !inserted {
		return nil, fmt.Errorf("auth: tenant id/slug collision after %d retries", tenantInsertMaxRetries)
	}
	tenant.Slug = finalSlug

	// The signup creator is the tenant Owner — the immutable top role.
	// Admins are invited later and CAN be removed; Owner cannot.
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO tenant_members (tenant_id, user_id, role, status, joined_at)
		 VALUES ($1, $2, 'owner', 'active', NOW())`,
		tenant.ID, user.ID,
	); err != nil {
		return nil, fmt.Errorf("auth: insert membership: %w", err)
	}

	// Seed default workspace settings (invite policy). Idempotent — the
	// migration backfills existing tenants; this covers new ones.
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO tenant_settings (tenant_id) VALUES ($1) ON CONFLICT (tenant_id) DO NOTHING`,
		tenant.ID,
	); err != nil {
		return nil, fmt.Errorf("auth: insert tenant settings: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("auth: commit signup tx: %w", err)
	}

	tenantID := tenant.ID
	sess, err := s.sessionStore.Create(ctx, session.CreateInput{
		UserID: user.ID, TenantID: &tenantID, Scope: session.ScopeTenant,
		IP: ip, UserAgent: ua, TTL: session.DefaultTTL,
	})
	if err != nil {
		return nil, fmt.Errorf("auth: create session: %w", err)
	}

	_, _ = s.client.ExecContext(ctx, `UPDATE users SET last_login_at = NOW() WHERE id = $1`, user.ID)

	logging.Info(packageName, "Signup: user=%d tenant=%d", user.ID, tenant.ID)
	return &Identity{
		User:       user,
		Session:    sess,
		Tenant:     tenant,
		Membership: &Membership{TenantID: tenant.ID, Role: "owner", Status: "active"},
		Access:     &FolderAccess{Full: true, Write: true}, // signup creates the workspace owner
	}, nil
}

// UserIDByEmail returns the id of an ACTIVE user with this email and whether one
// exists. Federated login now resolves accounts through ResolveOAuthLogin (which
// enforces the provider+subject binding); this remains a plain email→id helper.
func (s *Service) UserIDByEmail(ctx context.Context, email string) (int64, bool, error) {
	var id int64
	err := s.client.QueryRowContext(ctx,
		`SELECT id FROM users WHERE lower(email) = lower($1) AND status = 'active' LIMIT 1`,
		normalizeEmail(email),
	).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("auth: user id by email: %w", err)
	}
	return id, true, nil
}

// ResolveOAuthLogin maps a verified federated identity to a Trilli account,
// enforcing a provider+subject binding so an account can't be taken over by a
// forged email. provider is "google"/"microsoft"; subject is the provider's
// stable subject (Google `sub`, Microsoft `oid`/`sub`); email is the address the
// provider returned; emailProviderVerified reports whether the PROVIDER asserts
// the email is verified (Google enforces email_verified, Microsoft has no such
// claim → false). Returns (userID, true, nil) when the caller may log the user
// in, (0, false, nil) when no account exists (caller proceeds to sign-up), or a
// binding error to refuse.
//
// Resolution order:
//  1. A user already bound to (provider, subject) → that account (authoritative;
//     the provider proved the identity even if the email later changed).
//  2. Otherwise an active user matching the email, by its binding:
//     - bound to THIS provider (different subject, since step 1 missed) →
//     ErrOAuthIdentityMismatch (someone else's identity claims this email),
//     - bound to a DIFFERENT provider → ErrOAuthDifferentProvider,
//     - unbound + provider verifies the email → link-on-first-use + log in,
//     - unbound + provider does NOT verify the email → ErrOAuthUnverified (the
//     Microsoft forged-email takeover vector: refuse rather than trust email).
//  3. No matching account → (0, false, nil).
func (s *Service) ResolveOAuthLogin(ctx context.Context, provider, subject, email string, emailProviderVerified bool) (int64, bool, error) {
	provider = strings.TrimSpace(provider)
	subject = strings.TrimSpace(subject)
	if provider == "" || subject == "" {
		return 0, false, fmt.Errorf("auth: oauth login requires provider and subject")
	}

	// 1. Authoritative: an account already bound to this exact provider identity.
	var boundID int64
	err := s.client.QueryRowContext(ctx,
		`SELECT id FROM users WHERE oauth_provider = $1 AND oauth_subject = $2 AND status = 'active' LIMIT 1`,
		provider, subject,
	).Scan(&boundID)
	if err == nil {
		return boundID, true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, false, fmt.Errorf("auth: oauth subject lookup: %w", err)
	}

	// 2. Fall back to the email, but only honor it subject to the binding rules.
	var (
		uid       int64
		uProvider string
		uSubject  string
	)
	err = s.client.QueryRowContext(ctx,
		`SELECT id, oauth_provider, oauth_subject FROM users WHERE lower(email) = lower($1) AND status = 'active' LIMIT 1`,
		normalizeEmail(email),
	).Scan(&uid, &uProvider, &uSubject)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil // no account → caller proceeds to sign-up
	}
	if err != nil {
		return 0, false, fmt.Errorf("auth: oauth email lookup: %w", err)
	}

	switch {
	case uSubject != "" && uProvider == provider:
		// Bound to this provider but a different subject (step 1 missed) → a
		// different provider identity is claiming this email.
		return 0, false, ErrOAuthIdentityMismatch
	case uSubject != "":
		// Bound to a different provider — don't silently re-bind / take over.
		return 0, false, ErrOAuthDifferentProvider
	default:
		// Unbound account. Link on first use ONLY when the provider verifies the
		// email; otherwise refuse (an unverified email match is not proof).
		if !emailProviderVerified {
			return 0, false, ErrOAuthUnverified
		}
		if err := s.bindOAuthIdentity(ctx, uid, provider, subject); err != nil {
			return 0, false, err
		}
		return uid, true, nil
	}
}

// bindOAuthIdentity attaches a provider identity to an as-yet-unbound account
// (link-on-first-use). The WHERE guard keeps it a no-op if the account got bound
// concurrently, and the partial unique index (users_oauth_identity_idx) rejects
// attaching a subject already tied to another account — surfaced as a mismatch.
func (s *Service) bindOAuthIdentity(ctx context.Context, userID int64, provider, subject string) error {
	_, err := s.client.ExecContext(ctx,
		`UPDATE users SET oauth_provider = $2, oauth_subject = $3, updated_at = NOW()
		   WHERE id = $1 AND oauth_subject = ''`,
		userID, provider, subject)
	if err != nil {
		if pgUniqueViolation(err) {
			return ErrOAuthIdentityMismatch
		}
		return fmt.Errorf("auth: bind oauth identity: %w", err)
	}
	return nil
}

// randomPassword mints a strong random password for accounts created via an
// OAuth provider (which have no user-chosen password). They sign in via the
// provider; they can set a real password later through the reset flow.
func randomPassword() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("auth: random password: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// SignupFromIntentInput provisions an account from a PAID signup intent (the
// pay-before-account funnel). Unlike Signup, the plan is the one the visitor
// purchased and the tenant is wired to the existing Stripe customer +
// subscription up front. Email is already verified (link click or OAuth).
type SignupFromIntentInput struct {
	Email          string
	Password       string
	FirstName      string
	LastName       string
	TenantName     string // business name, or "{First}'s Trilli" for individuals
	PlanCode       string
	BillingPeriod  string // "monthly" | "annual"
	StripeCustomer string
	StripeSub      string
	OAuthProvider  string // e.g. "google" — when set, Password may be empty
	OAuthSubject   string // provider's stable subject (Google sub, Microsoft oid/sub); binds the account to this identity
}

// SignupFromIntent creates the owner user + tenant for a paid funnel signup,
// locking the purchased plan's price for the chosen period and attaching the
// Stripe customer/subscription so the account is immediately active. Returns a
// logged-in Identity.
func (s *Service) SignupFromIntent(ctx context.Context, in SignupFromIntentInput, ip, ua string) (*Identity, error) {
	email := normalizeEmail(in.Email)
	if !emailRE.MatchString(email) {
		return nil, ErrEmailInvalid
	}
	// OAuth signups carry no password — mint a strong random one so the column
	// is satisfied and password-login simply never matches.
	if in.OAuthProvider != "" && in.Password == "" {
		pw, perr := randomPassword()
		if perr != nil {
			return nil, perr
		}
		in.Password = pw
	}
	if len(in.Password) < MinPasswordLen {
		return nil, ErrPasswordTooShort
	}
	tenantName := strings.TrimSpace(in.TenantName)
	if tenantName == "" {
		tenantName = "My Workspace"
	}
	baseSlug := slugify(tenantName)
	if baseSlug == "" {
		baseSlug = "workspace"
	}
	period := "monthly"
	if strings.EqualFold(in.BillingPeriod, "annual") {
		period = "annual"
	}

	hash, err := HashPassword(in.Password)
	if err != nil {
		return nil, err
	}

	db := s.client.GetDB()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("auth: begin tx: %w", err)
	}
	defer tx.Rollback()

	// Resolve the purchased plan + compute the locked MONTHLY price for the
	// period (annual = discounted-per-month, matching the billing lock math).
	var planID int64
	var priceMonthly, discount int
	var planStatus string
	err = tx.QueryRowContext(ctx,
		`SELECT id, price_monthly_cents, annual_discount_pct, status FROM plans WHERE code = $1`, in.PlanCode,
	).Scan(&planID, &priceMonthly, &discount, &planStatus)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("auth: plan %q not found", in.PlanCode)
	}
	if err != nil {
		return nil, fmt.Errorf("auth: load plan: %w", err)
	}
	lockedMonthly := priceMonthly
	if period == "annual" {
		lockedMonthly = (priceMonthly*(100-discount) + 50) / 100
	}

	first := strings.TrimSpace(in.FirstName)
	last := strings.TrimSpace(in.LastName)
	full := strings.TrimSpace(first + " " + last)
	user := &User{Email: email, Status: "active"}
	if first != "" {
		user.FirstName = &first
	}
	if last != "" {
		user.LastName = &last
	}
	if full != "" {
		user.FullName = &full
	}
	var userInserted bool
	for attempt := 0; attempt < userInsertMaxRetries; attempt++ {
		candidateUserID, idErr := GenerateUserID()
		if idErr != nil {
			return nil, idErr
		}
		err = tx.QueryRowContext(ctx,
			`INSERT INTO users (id, email, password_hash, first_name, last_name, full_name, oauth_provider, oauth_subject)
			 VALUES ($1, $2, $3, NULLIF($4, ''), NULLIF($5, ''), NULLIF($6, ''), $7, $8)
			 RETURNING id, created_at`,
			candidateUserID, email, hash, first, last, full, in.OAuthProvider, in.OAuthSubject,
		).Scan(&user.ID, &user.CreatedAt)
		if err == nil {
			userInserted = true
			break
		}
		if !pgUniqueViolation(err) {
			return nil, fmt.Errorf("auth: insert user: %w", err)
		}
		if isEmailUniqueViolation(err) {
			return nil, ErrEmailTaken
		}
	}
	if !userInserted {
		return nil, fmt.Errorf("auth: user id collision after %d retries", userInsertMaxRetries)
	}

	tenant := &Tenant{
		Name: tenantName, PlanID: planID, Status: "active", UserCount: 1,
		LockedPriceCents: &lockedMonthly, LockedBillingPeriod: period,
	}
	var (
		finalSlug string
		inserted  bool
	)
	for attempt := 0; attempt < tenantInsertMaxRetries; attempt++ {
		candidateID, idErr := GenerateTenantID()
		if idErr != nil {
			return nil, idErr
		}
		candidateSlug := baseSlug
		if attempt > 0 {
			candidateSlug = fmt.Sprintf("%s-%d", baseSlug, time.Now().UnixNano()&0xffff+int64(attempt))
		}
		err = tx.QueryRowContext(ctx,
			`INSERT INTO tenants (id, name, slug, plan_id, user_count,
			                      locked_price_cents, locked_billing_period, locked_at,
			                      stripe_customer_id, stripe_subscription_id, subscription_status)
			 VALUES ($1, $2, $3, $4, 1, $5, $6, NOW(),
			         NULLIF($7,''), $8, 'active')
			 RETURNING id, created_at`,
			candidateID, tenantName, candidateSlug, planID, lockedMonthly, period,
			in.StripeCustomer, in.StripeSub,
		).Scan(&tenant.ID, &tenant.CreatedAt)
		if err == nil {
			finalSlug = candidateSlug
			inserted = true
			break
		}
		if !pgUniqueViolation(err) {
			return nil, fmt.Errorf("auth: insert tenant: %w", err)
		}
	}
	if !inserted {
		return nil, fmt.Errorf("auth: tenant id/slug collision after %d retries", tenantInsertMaxRetries)
	}
	tenant.Slug = finalSlug

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO tenant_members (tenant_id, user_id, role, status, joined_at)
		 VALUES ($1, $2, 'owner', 'active', NOW())`,
		tenant.ID, user.ID,
	); err != nil {
		return nil, fmt.Errorf("auth: insert membership: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO tenant_settings (tenant_id) VALUES ($1) ON CONFLICT (tenant_id) DO NOTHING`,
		tenant.ID,
	); err != nil {
		return nil, fmt.Errorf("auth: insert tenant settings: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("auth: commit signup tx: %w", err)
	}

	tenantID := tenant.ID
	sess, err := s.sessionStore.Create(ctx, session.CreateInput{
		UserID: user.ID, TenantID: &tenantID, Scope: session.ScopeTenant,
		IP: ip, UserAgent: ua, TTL: session.DefaultTTL,
	})
	if err != nil {
		return nil, fmt.Errorf("auth: create session: %w", err)
	}
	_, _ = s.client.ExecContext(ctx, `UPDATE users SET last_login_at = NOW() WHERE id = $1`, user.ID)

	logging.Info(packageName, "SignupFromIntent: user=%d tenant=%d plan=%s period=%s", user.ID, tenant.ID, in.PlanCode, period)
	return &Identity{
		User:       user,
		Session:    sess,
		Tenant:     tenant,
		Membership: &Membership{TenantID: tenant.ID, Role: "owner", Status: "active"},
		Access:     &FolderAccess{Full: true, Write: true},
	}, nil
}

// NewOwnedTenantInput provisions an additional tenant OWNED by an existing,
// already-authenticated user (the "buy a second account" flow). No user is
// created and no session is minted — the caller re-scopes the existing session.
type NewOwnedTenantInput struct {
	UserID         int64
	TenantName     string
	PlanCode       string
	BillingPeriod  string // "monthly" | "annual"
	StripeCustomer string
	StripeSub      string
}

// ProvisionOwnedTenant creates a new tenant + owner membership + settings for an
// EXISTING user, locking the plan price and wiring the Stripe customer/sub —
// mirroring SignupFromIntent's tenant steps minus the user INSERT and session.
func (s *Service) ProvisionOwnedTenant(ctx context.Context, in NewOwnedTenantInput) (*Tenant, error) {
	tenantName := strings.TrimSpace(in.TenantName)
	if tenantName == "" {
		tenantName = "My Workspace"
	}
	baseSlug := slugify(tenantName)
	if baseSlug == "" {
		baseSlug = "workspace"
	}
	period := "monthly"
	if strings.EqualFold(in.BillingPeriod, "annual") {
		period = "annual"
	}

	db := s.client.GetDB()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("auth: begin tx: %w", err)
	}
	defer tx.Rollback()

	var planID int64
	var priceMonthly, discount int
	var planStatus string
	err = tx.QueryRowContext(ctx,
		`SELECT id, price_monthly_cents, annual_discount_pct, status FROM plans WHERE code = $1`, in.PlanCode,
	).Scan(&planID, &priceMonthly, &discount, &planStatus)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("auth: plan %q not found", in.PlanCode)
	}
	if err != nil {
		return nil, fmt.Errorf("auth: load plan: %w", err)
	}
	lockedMonthly := priceMonthly
	if period == "annual" {
		lockedMonthly = (priceMonthly*(100-discount) + 50) / 100
	}

	tenant := &Tenant{
		Name: tenantName, PlanID: planID, Status: "active", UserCount: 1,
		LockedPriceCents: &lockedMonthly, LockedBillingPeriod: period,
	}
	var finalSlug string
	var inserted bool
	for attempt := 0; attempt < tenantInsertMaxRetries; attempt++ {
		candidateID, idErr := GenerateTenantID()
		if idErr != nil {
			return nil, idErr
		}
		candidateSlug := baseSlug
		if attempt > 0 {
			candidateSlug = fmt.Sprintf("%s-%d", baseSlug, time.Now().UnixNano()&0xffff+int64(attempt))
		}
		err = tx.QueryRowContext(ctx,
			`INSERT INTO tenants (id, name, slug, plan_id, user_count,
			                      locked_price_cents, locked_billing_period, locked_at,
			                      stripe_customer_id, stripe_subscription_id, subscription_status)
			 VALUES ($1, $2, $3, $4, 1, $5, $6, NOW(),
			         NULLIF($7,''), $8, 'active')
			 RETURNING id, created_at`,
			candidateID, tenantName, candidateSlug, planID, lockedMonthly, period,
			in.StripeCustomer, in.StripeSub,
		).Scan(&tenant.ID, &tenant.CreatedAt)
		if err == nil {
			finalSlug = candidateSlug
			inserted = true
			break
		}
		if !pgUniqueViolation(err) {
			return nil, fmt.Errorf("auth: insert tenant: %w", err)
		}
	}
	if !inserted {
		return nil, fmt.Errorf("auth: tenant id/slug collision after %d retries", tenantInsertMaxRetries)
	}
	tenant.Slug = finalSlug

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO tenant_members (tenant_id, user_id, role, status, joined_at)
		 VALUES ($1, $2, 'owner', 'active', NOW())`,
		tenant.ID, in.UserID,
	); err != nil {
		return nil, fmt.Errorf("auth: insert membership: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO tenant_settings (tenant_id) VALUES ($1) ON CONFLICT (tenant_id) DO NOTHING`,
		tenant.ID,
	); err != nil {
		return nil, fmt.Errorf("auth: insert tenant settings: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("auth: commit new-tenant tx: %w", err)
	}
	logging.Info(packageName, "ProvisionOwnedTenant: user=%d tenant=%d plan=%s period=%s", in.UserID, tenant.ID, in.PlanCode, period)
	return tenant, nil
}

// ----- Login ----------------------------------------------------------------

// LoginInput is the credentials payload.
type LoginInput struct {
	Email    string
	Password string
}

// Login authenticates a user and creates a tenant-scoped session for their first
// active membership. Returns ErrInvalidCredentials uniformly for any failure
// involving "wrong creds" — does not leak whether the email exists.
// LoginResult is what Login returns: a full Identity when no 2FA is required,
// or TwoFARequired + a short-lived PendingToken when the user must complete a
// TOTP challenge (no session is issued in that case).
type LoginResult struct {
	Identity      *Identity
	TwoFARequired bool
	PendingToken  string
	Method        string // "totp" or "passkey" — which second factor to prompt for
}

func (s *Service) Login(ctx context.Context, in LoginInput, ip, ua, trustedDeviceToken string) (*LoginResult, error) {
	email := normalizeEmail(in.Email)
	if !emailRE.MatchString(email) {
		return nil, ErrInvalidCredentials
	}

	user, hash, err := s.loadUserAndHash(ctx, email)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrInvalidCredentials
		}
		return nil, err
	}
	ok, err := VerifyPassword(hash, in.Password)
	if err != nil {
		return nil, fmt.Errorf("auth: verify password: %w", err)
	}
	if !ok {
		return nil, ErrInvalidCredentials
	}
	if user.Status != "active" {
		return nil, ErrUserSuspended
	}

	if user.IsSuperuser {
		// Super-admin login on the tenant port is rejected — they must use the
		// admin port. (Once 0.4 ships, the admin handler will route this.)
		return nil, ErrUnauthorized
	}

	tenant, membership, err := s.loadFirstActiveTenant(ctx, user.ID)
	if err != nil {
		// If there's no active membership but the user DOES have a
		// suspended one, surface the more specific "your account is
		// disabled" error so the login UI can show a clear message.
		if errors.Is(err, ErrNoActiveTenant) {
			var suspendedCount int
			if cErr := s.client.QueryRowContext(ctx,
				`SELECT count(*) FROM tenant_members WHERE user_id = $1 AND status = 'suspended'`,
				user.ID,
			).Scan(&suspendedCount); cErr == nil && suspendedCount > 0 {
				return nil, ErrAccountDisabled
			}
		}
		return nil, err
	}

	// 2FA gate: a user with confirmed TOTP does NOT get a session yet — return
	// a short-lived pending token; the session is issued only after the code is
	// verified at POST /api/auth/login/2fa. Users without TOTP are unaffected.
	enabled, err := s.IsTOTPEnabled(ctx, user.ID)
	if err != nil {
		return nil, err
	}
	if enabled {
		// A device the user previously chose to trust skips the challenge for
		// 30 days. The token is bound to the current TOTP enrollment, so
		// disabling/re-enrolling invalidates every trusted device at once.
		if trustedDeviceToken != "" && s.trustedDeviceValid(ctx, user.ID, trustedDeviceToken) {
			identity, err := s.issueIdentity(ctx, user, tenant, membership, ip, ua)
			if err != nil {
				return nil, err
			}
			return &LoginResult{Identity: identity}, nil
		}
		tok, terr := s.NewTwoFAPendingToken(user.ID)
		if terr != nil {
			return nil, terr
		}
		return &LoginResult{TwoFARequired: true, PendingToken: tok, Method: "totp"}, nil
	}

	// No authenticator app, but a passkey can serve as the second factor. We
	// only need ONE — TOTP is the first line of defense; passkey is the
	// fallback when there's no TOTP.
	hasPasskey, err := s.userHasPasskey(ctx, user.ID)
	if err != nil {
		return nil, err
	}
	if hasPasskey {
		tok, terr := s.NewTwoFAPendingToken(user.ID)
		if terr != nil {
			return nil, terr
		}
		return &LoginResult{TwoFARequired: true, PendingToken: tok, Method: "passkey"}, nil
	}

	identity, err := s.issueIdentity(ctx, user, tenant, membership, ip, ua)
	if err != nil {
		return nil, err
	}
	return &LoginResult{Identity: identity}, nil
}

// issueIdentity creates the session, stamps last_login, and builds the folder
// access — the tail shared by a no-2FA login and a completed 2FA challenge.
func (s *Service) issueIdentity(ctx context.Context, user *User, tenant *Tenant, membership *Membership, ip, ua string) (*Identity, error) {
	tenantID := tenant.ID
	sess, err := s.sessionStore.Create(ctx, session.CreateInput{
		UserID: user.ID, TenantID: &tenantID, Scope: session.ScopeTenant,
		IP: ip, UserAgent: ua, TTL: session.DefaultTTL,
	})
	if err != nil {
		return nil, fmt.Errorf("auth: create session: %w", err)
	}
	_, _ = s.client.ExecContext(ctx, `UPDATE users SET last_login_at = NOW() WHERE id = $1`, user.ID)
	logging.Info(packageName, "Login: user=%d tenant=%d", user.ID, tenant.ID)
	access, err := s.buildFolderAccess(ctx, tenant.ID, user.ID, membership)
	if err != nil {
		return nil, err
	}
	return &Identity{User: user, Session: sess, Tenant: tenant, Membership: membership, Access: access}, nil
}

// CompleteSessionForUser issues a full session for a user who already passed
// the password + 2FA challenge (used by the login-2FA-complete handler).
func (s *Service) CompleteSessionForUser(ctx context.Context, userID int64, ip, ua string) (*Identity, error) {
	user, err := s.loadUser(ctx, userID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrUnauthorized
		}
		return nil, err
	}
	if user.Status != "active" {
		return nil, ErrUserSuspended
	}
	if user.IsSuperuser {
		return nil, ErrUnauthorized
	}
	tenant, membership, err := s.loadFirstActiveTenant(ctx, userID)
	if err != nil {
		return nil, err
	}
	return s.issueIdentity(ctx, user, tenant, membership, ip, ua)
}

// DecideOAuthLogin runs the post-identity login decision for a federated
// (e.g. Google) sign-in. The provider has verified WHO the user is — that
// replaces the password — but the app-level second factor the user enrolled
// still applies. Returns TwoFARequired + a short-lived pending token when the
// user has TOTP or a passkey, otherwise a logged-in Identity. Mirrors the tail
// of Login so federated sign-in preserves the same 2FA guarantee instead of
// bypassing it.
func (s *Service) DecideOAuthLogin(ctx context.Context, userID int64, ip, ua, trustedDeviceToken string) (*LoginResult, error) {
	user, err := s.loadUser(ctx, userID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrUnauthorized
		}
		return nil, err
	}
	if user.Status != "active" {
		return nil, ErrUserSuspended
	}
	if user.IsSuperuser {
		return nil, ErrUnauthorized
	}
	tenant, membership, err := s.loadFirstActiveTenant(ctx, userID)
	if err != nil {
		return nil, err
	}
	if enabled, err := s.IsTOTPEnabled(ctx, userID); err != nil {
		return nil, err
	} else if enabled {
		// A device the user previously chose to trust skips the challenge — the
		// same rule (and token) as password login, so federated login is
		// consistent. Device-trust is a property of the second factor, not how
		// the first factor was satisfied.
		if trustedDeviceToken != "" && s.trustedDeviceValid(ctx, userID, trustedDeviceToken) {
			identity, ierr := s.issueIdentity(ctx, user, tenant, membership, ip, ua)
			if ierr != nil {
				return nil, ierr
			}
			return &LoginResult{Identity: identity}, nil
		}
		tok, terr := s.NewTwoFAPendingToken(userID)
		if terr != nil {
			return nil, terr
		}
		return &LoginResult{TwoFARequired: true, PendingToken: tok, Method: "totp"}, nil
	}
	if hasPasskey, err := s.userHasPasskey(ctx, userID); err != nil {
		return nil, err
	} else if hasPasskey {
		tok, terr := s.NewTwoFAPendingToken(userID)
		if terr != nil {
			return nil, terr
		}
		return &LoginResult{TwoFARequired: true, PendingToken: tok, Method: "passkey"}, nil
	}
	identity, err := s.issueIdentity(ctx, user, tenant, membership, ip, ua)
	if err != nil {
		return nil, err
	}
	return &LoginResult{Identity: identity}, nil
}

// IsTOTPEnabled reports whether the user has a confirmed (active) TOTP secret.
func (s *Service) IsTOTPEnabled(ctx context.Context, userID int64) (bool, error) {
	var n int
	if err := s.client.QueryRowContext(ctx,
		`SELECT count(*) FROM user_totp WHERE user_id = $1 AND confirmed_at IS NOT NULL`, userID,
	).Scan(&n); err != nil {
		return false, fmt.Errorf("auth: totp enabled check: %w", err)
	}
	return n > 0, nil
}

// userHasPasskey reports whether the user has at least one registered passkey.
// Queried directly (not via the passkeys package) so auth keeps no import on it.
func (s *Service) userHasPasskey(ctx context.Context, userID int64) (bool, error) {
	var n int
	if err := s.client.QueryRowContext(ctx,
		`SELECT count(*) FROM user_passkeys WHERE user_id = $1`, userID,
	).Scan(&n); err != nil {
		return false, fmt.Errorf("auth: passkey check: %w", err)
	}
	return n > 0, nil
}

// NewStepUpToken mints a 5-minute, encrypted token proving a user just
// re-authenticated for a sensitive action (e.g. via OAuth re-auth). It is
// purpose-tagged ("stepup") so it can't be confused with a 2FA-pending token,
// and bound to the user id. ParseStepUpToken validates and returns that id.
func (s *Service) NewStepUpToken(userID int64) (string, error) {
	payload := fmt.Sprintf("stepup|%d|%d", userID, time.Now().Add(5*time.Minute).Unix())
	enc, err := appcrypto.Encrypt([]byte(payload))
	if err != nil {
		return "", fmt.Errorf("auth: step-up token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(enc), nil
}

// ParseStepUpToken validates a step-up token and returns the bound user id, or
// ErrInvalidStepUpToken if it's malformed, expired, or not a step-up token.
func (s *Service) ParseStepUpToken(token string) (int64, error) {
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(token))
	if err != nil {
		return 0, ErrInvalidStepUpToken
	}
	pt, err := appcrypto.Decrypt(raw)
	if err != nil {
		return 0, ErrInvalidStepUpToken
	}
	parts := strings.SplitN(string(pt), "|", 3)
	if len(parts) != 3 || parts[0] != "stepup" {
		return 0, ErrInvalidStepUpToken
	}
	uid, err1 := strconv.ParseInt(parts[1], 10, 64)
	exp, err2 := strconv.ParseInt(parts[2], 10, 64)
	if err1 != nil || err2 != nil || uid == 0 || time.Now().Unix() > exp {
		return 0, ErrInvalidStepUpToken
	}
	return uid, nil
}

// NewTwoFAPendingToken mints a 5-minute, encrypted token binding a user to the
// in-progress login (after password, before the 2FA code).
func (s *Service) NewTwoFAPendingToken(userID int64) (string, error) {
	payload := fmt.Sprintf("%d|%d", userID, time.Now().Add(5*time.Minute).Unix())
	enc, err := appcrypto.Encrypt([]byte(payload))
	if err != nil {
		return "", fmt.Errorf("auth: pending token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(enc), nil
}

// ParseTwoFAPendingToken validates a pending token and returns its user id.
func (s *Service) ParseTwoFAPendingToken(token string) (int64, error) {
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(token))
	if err != nil {
		return 0, ErrInvalidPendingToken
	}
	pt, err := appcrypto.Decrypt(raw)
	if err != nil {
		return 0, ErrInvalidPendingToken
	}
	parts := strings.SplitN(string(pt), "|", 2)
	if len(parts) != 2 {
		return 0, ErrInvalidPendingToken
	}
	uid, err1 := strconv.ParseInt(parts[0], 10, 64)
	exp, err2 := strconv.ParseInt(parts[1], 10, 64)
	if err1 != nil || err2 != nil || uid == 0 || time.Now().Unix() > exp {
		return 0, ErrInvalidPendingToken
	}
	return uid, nil
}

// TrustedDeviceTTL is how long a "remember this device" choice skips the 2FA
// challenge on a returning device.
const TrustedDeviceTTL = 30 * 24 * time.Hour

// NewTrustedDeviceToken mints an encrypted 30-day token marking the current
// device as trusted for this user. The token embeds the user's current TOTP
// confirmed_at so that disabling or re-enrolling 2FA invalidates every
// previously-trusted device. Returns the token and its expiry (for the cookie).
func (s *Service) NewTrustedDeviceToken(ctx context.Context, userID int64) (string, time.Time, error) {
	confirmedUnix, err := s.totpConfirmedUnix(ctx, userID)
	if err != nil {
		return "", time.Time{}, err
	}
	exp := time.Now().Add(TrustedDeviceTTL)
	payload := fmt.Sprintf("%d|%d|%d", userID, exp.Unix(), confirmedUnix)
	enc, err := appcrypto.Encrypt([]byte(payload))
	if err != nil {
		return "", time.Time{}, fmt.Errorf("auth: trusted device token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(enc), exp, nil
}

// trustedDeviceValid reports whether the token marks the device as trusted for
// userID: it must decrypt, match the user, be unexpired, and still match the
// user's current TOTP enrollment (so a disable/re-enroll voids it).
func (s *Service) trustedDeviceValid(ctx context.Context, userID int64, token string) bool {
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(token))
	if err != nil {
		return false
	}
	pt, err := appcrypto.Decrypt(raw)
	if err != nil {
		return false
	}
	parts := strings.SplitN(string(pt), "|", 3)
	if len(parts) != 3 {
		return false
	}
	uid, e1 := strconv.ParseInt(parts[0], 10, 64)
	exp, e2 := strconv.ParseInt(parts[1], 10, 64)
	conf, e3 := strconv.ParseInt(parts[2], 10, 64)
	if e1 != nil || e2 != nil || e3 != nil {
		return false
	}
	if uid != userID || time.Now().Unix() > exp {
		return false
	}
	cur, err := s.totpConfirmedUnix(ctx, userID)
	if err != nil || cur == 0 || cur != conf {
		return false
	}
	return true
}

// totpConfirmedUnix returns the unix time the user's TOTP was confirmed, or 0
// when there's no active enrollment.
func (s *Service) totpConfirmedUnix(ctx context.Context, userID int64) (int64, error) {
	var t sql.NullTime
	err := s.client.QueryRowContext(ctx,
		`SELECT confirmed_at FROM user_totp WHERE user_id = $1`, userID,
	).Scan(&t)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("auth: totp confirmed lookup: %w", err)
	}
	if !t.Valid {
		return 0, nil
	}
	return t.Time.Unix(), nil
}

// ----- Active sessions -------------------------------------------------------

// SessionView is one active session for the Active Sessions panel. The session
// token (the row id / auth cookie) is NEVER exposed — Ref is the public handle
// used for revocation.
type SessionView struct {
	Ref         string    `json:"ref"`
	Name        string    `json:"name"`
	Email       string    `json:"email"`
	Role        string    `json:"role"`
	IP          string    `json:"ip"`
	IPv4        string    `json:"ip_v4,omitempty"`
	IPv6        string    `json:"ip_v6,omitempty"`
	City        string    `json:"city,omitempty"`
	Region      string    `json:"region,omitempty"`
	Country     string    `json:"country,omitempty"`
	CountryCode string    `json:"country_code,omitempty"`
	UserAgent   string    `json:"user_agent"`
	LastSeenAt  time.Time `json:"last_seen_at"`
	Current     bool      `json:"current"`
}

// ListSessions returns active sessions for the Active Sessions panel. Owners and
// admins see every active session in the tenant (the "who" view); everyone else
// sees only their own. Most-recently-active first.
func (s *Service) ListSessions(ctx context.Context, identity *Identity) ([]SessionView, error) {
	if identity == nil || identity.Tenant == nil || identity.Membership == nil {
		return nil, ErrUnauthorized
	}
	args := []any{identity.Tenant.ID}
	scope := ""
	if !IsAdminOrOwner(identity.Membership.Role) {
		scope = " AND s.user_id = $2"
		args = append(args, identity.User.ID)
	}
	q := `
		SELECT s.ref::text, s.id,
		       COALESCE(u.full_name, ''), u.email, tm.role,
		       COALESCE(host(s.ip), ''), COALESCE(host(s.ip_v4), ''), COALESCE(host(s.ip_v6), ''),
		       COALESCE(s.city, ''), COALESCE(s.region, ''), COALESCE(s.country, ''), COALESCE(s.country_code, ''),
		       COALESCE(s.user_agent, ''), s.last_seen_at
		  FROM sessions s
		  JOIN users u ON u.id = s.user_id
		  JOIN tenant_members tm ON tm.user_id = s.user_id AND tm.tenant_id = s.tenant_id
		 WHERE s.tenant_id = $1 AND s.revoked_at IS NULL AND s.expires_at > NOW()` + scope + `
		 ORDER BY s.last_seen_at DESC`
	rows, err := s.client.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("auth: list sessions: %w", err)
	}
	defer rows.Close()

	currentID := ""
	if identity.Session != nil {
		currentID = identity.Session.ID
	}
	out := make([]SessionView, 0)
	for rows.Next() {
		var v SessionView
		var id string
		if err := rows.Scan(&v.Ref, &id, &v.Name, &v.Email, &v.Role,
			&v.IP, &v.IPv4, &v.IPv6, &v.City, &v.Region, &v.Country, &v.CountryCode,
			&v.UserAgent, &v.LastSeenAt); err != nil {
			return nil, fmt.Errorf("auth: scan session: %w", err)
		}
		v.Current = id == currentID
		// Display IP prefers IPv4 (its geo is more accurate), else IPv6.
		if v.IPv4 != "" {
			v.IP = v.IPv4
		} else if v.IPv6 != "" {
			v.IP = v.IPv6
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

var sessionRefRE = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// RevokeSessions revokes sessions by their public refs. A regular member may
// only revoke their own sessions; owners/admins may revoke any session in the
// tenant. Returns how many were revoked. Unknown/invalid refs are skipped.
func (s *Service) RevokeSessions(ctx context.Context, identity *Identity, refs []string) (int, error) {
	if identity == nil || identity.Tenant == nil || identity.Membership == nil {
		return 0, ErrUnauthorized
	}
	admin := IsAdminOrOwner(identity.Membership.Role)
	revoked := 0
	for _, ref := range refs {
		ref = strings.TrimSpace(ref)
		if !sessionRefRE.MatchString(ref) {
			continue
		}
		var (
			res sql.Result
			err error
		)
		if admin {
			res, err = s.client.ExecContext(ctx,
				`UPDATE sessions SET revoked_at = NOW() WHERE ref = $1::uuid AND tenant_id = $2 AND revoked_at IS NULL`,
				ref, identity.Tenant.ID)
		} else {
			res, err = s.client.ExecContext(ctx,
				`UPDATE sessions SET revoked_at = NOW() WHERE ref = $1::uuid AND tenant_id = $2 AND user_id = $3 AND revoked_at IS NULL`,
				ref, identity.Tenant.ID, identity.User.ID)
		}
		if err != nil {
			return revoked, fmt.Errorf("auth: revoke session: %w", err)
		}
		if n, _ := res.RowsAffected(); n > 0 {
			revoked++
		}
	}
	logging.Info(packageName, "RevokeSessions: caller=%d revoked=%d", identity.User.ID, revoked)
	return revoked, nil
}

// ----- Logout ---------------------------------------------------------------

// Logout revokes a session. Idempotent — no error if the session is already
// gone or already revoked.
func (s *Service) Logout(ctx context.Context, sessionID string) error {
	return s.sessionStore.Revoke(ctx, sessionID)
}

// UpdateSessionIPs records the client's probed IPv4/IPv6 on a session and
// re-resolves geolocation (IPv4 preferred). Backs the post-login IP probe.
func (s *Service) UpdateSessionIPs(ctx context.Context, sessionID, ipv4, ipv6 string) error {
	return s.sessionStore.UpdateIPs(ctx, sessionID, ipv4, ipv6)
}

// ----- Profile ---------------------------------------------------------------

// UpdateProfileInput carries the editable profile fields from the Account
// settings tab.
type UpdateProfileInput struct {
	FirstName string
	LastName  string
}

// UpdateProfile updates the user's first/last name and keeps the derived
// full_name in sync (full_name is retained for back-compat with the audit
// log, mailer, and Members list). Both names are required — the nav greeting
// and avatar initials depend on them. Returns the refreshed user.
func (s *Service) UpdateProfile(ctx context.Context, userID int64, in UpdateProfileInput) (*User, error) {
	first := strings.TrimSpace(in.FirstName)
	last := strings.TrimSpace(in.LastName)
	if first == "" || last == "" || len(first) > 60 || len(last) > 60 {
		return nil, ErrInvalidName
	}
	full := strings.TrimSpace(first + " " + last)
	if _, err := s.client.ExecContext(ctx,
		`UPDATE users SET first_name = $1, last_name = $2, full_name = $3 WHERE id = $4`,
		first, last, full, userID,
	); err != nil {
		return nil, fmt.Errorf("auth: update profile: %w", err)
	}
	return s.loadUser(ctx, userID)
}

// UpdateWorkspace renames a tenant and sets its URL slug. The slug is the
// workspace's namespace: we normalize it (lowercase, alnum + hyphens), reject
// reserved app-route names, and rely on the unique index to keep it globally
// distinct. The per-workspace web service that will eventually serve this slug
// is deferred — this just reserves + stores the name. Returns the refreshed
// tenant. Owner/admin only (gated at the handler).
func (s *Service) UpdateWorkspace(ctx context.Context, tenantID int64, name, slugInput string) (*Tenant, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, ErrWorkspaceNameRequired
	}
	if len(name) > 100 {
		name = name[:100]
	}
	slug := slugify(slugInput)
	if slug == "" {
		return nil, ErrSlugInvalid
	}
	if reservedSlugs[slug] {
		return nil, ErrSlugReserved
	}

	var t Tenant
	err := s.client.QueryRowContext(ctx, `
		UPDATE tenants SET name = $1, slug = $2 WHERE id = $3
		RETURNING id, name, slug, plan_id, status, storage_bytes_used, user_count, created_at`,
		name, slug, tenantID,
	).Scan(&t.ID, &t.Name, &t.Slug, &t.PlanID, &t.Status, &t.StorageBytesUsed, &t.UserCount, &t.CreatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrUnauthorized
		}
		if pgUniqueViolation(err) {
			return nil, ErrSlugTaken
		}
		return nil, fmt.Errorf("auth: update workspace: %w", err)
	}
	logging.Info(packageName, "UpdateWorkspace: tenant=%d name=%q slug=%q", tenantID, name, slug)
	return &t, nil
}

// SlugAvailability is the result of a workspace-URL availability check.
type SlugAvailability struct {
	Slug      string `json:"slug"`      // the normalized slug we'd actually store
	Available bool   `json:"available"` // true only when free to claim
	Reason    string `json:"reason"`    // "", "empty", "reserved", "taken"
}

// CheckSlugAvailable reports whether a workspace URL slug is free for this
// tenant to claim. Normalizes the input the same way UpdateWorkspace will, then
// checks the reserved set and existing tenants (excluding this tenant, so a
// workspace's own current slug always reads as available).
func (s *Service) CheckSlugAvailable(ctx context.Context, tenantID int64, slugInput string) (SlugAvailability, error) {
	slug := slugify(slugInput)
	out := SlugAvailability{Slug: slug}
	if slug == "" {
		out.Reason = "empty"
		return out, nil
	}
	if reservedSlugs[slug] {
		out.Reason = "reserved"
		return out, nil
	}
	var taken bool
	if err := s.client.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM tenants WHERE slug = $1 AND id <> $2)`, slug, tenantID,
	).Scan(&taken); err != nil {
		return out, fmt.Errorf("auth: slug availability: %w", err)
	}
	if taken {
		out.Reason = "taken"
		return out, nil
	}
	out.Available = true
	return out, nil
}

// SetAvatarKey points the user's avatar at a freshly-stored blob and stamps
// avatar_updated_at (for the ?v= cache bust). It returns the PREVIOUS blob key
// (empty if none) so the caller can delete the now-orphaned blob, plus the
// refreshed user.
func (s *Service) SetAvatarKey(ctx context.Context, userID int64, key string) (string, *User, error) {
	var old sql.NullString
	// Best-effort read of the prior key; a miss just means nothing to clean up.
	_ = s.client.QueryRowContext(ctx, `SELECT avatar_key FROM users WHERE id = $1`, userID).Scan(&old)
	if _, err := s.client.ExecContext(ctx,
		`UPDATE users SET avatar_key = $1, avatar_updated_at = NOW() WHERE id = $2`, key, userID,
	); err != nil {
		return "", nil, fmt.Errorf("auth: set avatar: %w", err)
	}
	u, err := s.loadUser(ctx, userID)
	if err != nil {
		return "", nil, err
	}
	return strings.TrimSpace(old.String), u, nil
}

// ClearAvatar removes the user's avatar, returning the prior blob key (empty if
// none) so the caller can delete the blob, plus the refreshed user.
func (s *Service) ClearAvatar(ctx context.Context, userID int64) (string, *User, error) {
	var old sql.NullString
	_ = s.client.QueryRowContext(ctx, `SELECT avatar_key FROM users WHERE id = $1`, userID).Scan(&old)
	if _, err := s.client.ExecContext(ctx,
		`UPDATE users SET avatar_key = NULL, avatar_updated_at = NOW() WHERE id = $1`, userID,
	); err != nil {
		return "", nil, fmt.Errorf("auth: clear avatar: %w", err)
	}
	u, err := s.loadUser(ctx, userID)
	if err != nil {
		return "", nil, err
	}
	return strings.TrimSpace(old.String), u, nil
}

// UserAvatarKey returns the blob key for a user's avatar, gated to the caller's
// tenant: the target must share a membership in tenantID, so one workspace
// can't probe another's avatars. Returns ErrAvatarNotFound when the user has no
// avatar or isn't a member of the tenant.
func (s *Service) UserAvatarKey(ctx context.Context, tenantID, userID int64) (string, error) {
	var key sql.NullString
	err := s.client.QueryRowContext(ctx, `
		SELECT u.avatar_key
		  FROM users u
		  JOIN tenant_members tm ON tm.user_id = u.id
		 WHERE u.id = $1 AND tm.tenant_id = $2`, userID, tenantID,
	).Scan(&key)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrAvatarNotFound
	}
	if err != nil {
		return "", fmt.Errorf("auth: avatar key: %w", err)
	}
	if !key.Valid || strings.TrimSpace(key.String) == "" {
		return "", ErrAvatarNotFound
	}
	return key.String, nil
}

// UserAvatarKeyUnscoped returns a user's avatar blob key WITHOUT the caller-tenant
// co-membership gate. Used only after an explicit trust decision (e.g. the
// caller shares a live collaboration session with the user).
func (s *Service) UserAvatarKeyUnscoped(ctx context.Context, userID int64) (string, error) {
	var key sql.NullString
	err := s.client.QueryRowContext(ctx,
		`SELECT avatar_key FROM users WHERE id = $1`, userID).Scan(&key)
	if errors.Is(err, sql.ErrNoRows) || err != nil || !key.Valid || strings.TrimSpace(key.String) == "" {
		return "", ErrAvatarNotFound
	}
	return key.String, nil
}

// ----- Session resolution ---------------------------------------------------

// LoadIdentity validates a session id and returns the attached user + tenant.
// Returns ErrUnauthorized for any invalid / expired / revoked / suspended case.
func (s *Service) LoadIdentity(ctx context.Context, sessionID string) (*Identity, error) {
	sess, err := s.sessionStore.Get(ctx, sessionID)
	if err != nil {
		if errors.Is(err, session.ErrNotFound) {
			return nil, ErrUnauthorized
		}
		return nil, fmt.Errorf("auth: load session: %w", err)
	}
	now := time.Now().UTC()
	if !sess.Active(now) {
		return nil, ErrUnauthorized
	}
	// Inactivity timeout: log the user out after a period with no authenticated
	// requests. sess.LastSeenAt is the value loaded by Get above — i.e. BEFORE
	// the Touch below slides it forward — so this delta is the true idle gap.
	// Revoke so the stale token can't be reused, then 401 (the client redirects
	// to /login). DefaultIdleTimeout is the single, future-CMS-tunable knob.
	if now.Sub(sess.LastSeenAt) > session.DefaultIdleTimeout {
		_ = s.sessionStore.Revoke(ctx, sess.ID)
		return nil, ErrUnauthorized
	}

	user, err := s.loadUser(ctx, sess.UserID)
	if err != nil {
		return nil, err
	}
	if user.Status != "active" {
		return nil, ErrUserSuspended
	}

	identity := &Identity{User: user, Session: sess}

	if sess.Scope == session.ScopeTenant {
		if sess.TenantID == nil {
			return nil, ErrUnauthorized
		}
		tenant, membership, err := s.loadTenantAndMembership(ctx, *sess.TenantID, user.ID)
		if err != nil {
			return nil, err
		}
		if tenant.Status != "active" {
			return nil, ErrTenantSuspended
		}
		if membership.Status != "active" {
			return nil, ErrUnauthorized
		}
		identity.Tenant = tenant
		identity.Membership = membership

		access, err := s.buildFolderAccess(ctx, tenant.ID, user.ID, membership)
		if err != nil {
			return nil, err
		}
		identity.Access = access
	}

	// Best-effort touch — don't block the request if it fails.
	if err := s.sessionStore.Touch(ctx, sess.ID, session.DefaultTTL); err != nil &&
		!errors.Is(err, session.ErrNotFound) {
		logging.Debug(packageName, "session touch failed: %v", err)
	}

	return identity, nil
}

// ----- DB helpers -----------------------------------------------------------

func (s *Service) loadUserAndHash(ctx context.Context, email string) (*User, string, error) {
	var u User
	var hash string
	var avatarKey sql.NullString
	var avatarUpdated sql.NullTime
	err := s.client.QueryRowContext(ctx, `
		SELECT id, email, first_name, last_name, full_name, is_superuser, status, password_hash, created_at, last_login_at, avatar_key, avatar_updated_at
		  FROM users
		 WHERE email = $1`, email,
	).Scan(&u.ID, &u.Email, &u.FirstName, &u.LastName, &u.FullName, &u.IsSuperuser, &u.Status, &hash, &u.CreatedAt, &u.LastLoginAt, &avatarKey, &avatarUpdated)
	if err != nil {
		return nil, "", err
	}
	setAvatarURL(&u, avatarKey, avatarUpdated)
	return &u, hash, nil
}

// VerifyUserPassword constant-time checks a plaintext password against the
// stored hash for the CURRENT user (by id). Used to gate sensitive actions
// (e.g. account deletion) behind a password re-entry. Note: OAuth-only accounts
// carry a random password the user doesn't know, so this will (correctly) fail
// for them — they confirm via a passkey/2FA/OAuth step-up instead.
func (s *Service) VerifyUserPassword(ctx context.Context, userID int64, plain string) (bool, error) {
	var hash string
	if err := s.client.QueryRowContext(ctx,
		`SELECT password_hash FROM users WHERE id = $1`, userID).Scan(&hash); err != nil {
		return false, fmt.Errorf("auth: load password hash: %w", err)
	}
	ok, err := VerifyPassword(hash, plain)
	if err != nil {
		return false, err
	}
	return ok, nil
}

func (s *Service) loadUser(ctx context.Context, id int64) (*User, error) {
	var u User
	var avatarKey sql.NullString
	var avatarUpdated sql.NullTime
	err := s.client.QueryRowContext(ctx, `
		SELECT id, email, first_name, last_name, full_name, is_superuser, status, created_at, last_login_at, avatar_key, avatar_updated_at
		  FROM users
		 WHERE id = $1`, id,
	).Scan(&u.ID, &u.Email, &u.FirstName, &u.LastName, &u.FullName, &u.IsSuperuser, &u.Status, &u.CreatedAt, &u.LastLoginAt, &avatarKey, &avatarUpdated)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrUnauthorized
		}
		return nil, fmt.Errorf("auth: load user: %w", err)
	}
	setAvatarURL(&u, avatarKey, avatarUpdated)
	return &u, nil
}

// setAvatarURL populates u.AvatarURL from the stored blob key. The URL points
// at the tenant-gated serving endpoint and carries a ?v= stamp (last update
// unix time) so a fresh upload busts any cached image immediately.
func setAvatarURL(u *User, key sql.NullString, updated sql.NullTime) {
	if !key.Valid || strings.TrimSpace(key.String) == "" {
		return
	}
	v := int64(0)
	if updated.Valid {
		v = updated.Time.Unix()
	}
	url := fmt.Sprintf("/api/users/%d/avatar?v=%d", u.ID, v)
	u.AvatarURL = &url
}

func (s *Service) loadFirstActiveTenant(ctx context.Context, userID int64) (*Tenant, *Membership, error) {
	var t Tenant
	var m Membership
	var lockedCents sql.NullInt64
	var lockedPeriod sql.NullString
	var purgeAt sql.NullTime
	err := s.client.QueryRowContext(ctx, `
		SELECT t.id, t.name, t.slug, t.plan_id, t.status, t.storage_bytes_used, t.user_count, t.created_at,
		       t.locked_price_cents, t.locked_billing_period, t.lifecycle_state, t.purge_at,
		       COALESCE(p.name, ''), COALESCE(p.max_storage_bytes, 9223372036854775807), COALESCE(p.api_access, false),
		       tm.id, tm.role, tm.status
		  FROM tenant_members tm
		  JOIN tenants t ON t.id = tm.tenant_id
		  LEFT JOIN plans p ON p.id = t.plan_id
		 WHERE tm.user_id = $1 AND tm.status = 'active' AND t.status = 'active'
		 ORDER BY (tm.role = 'owner') DESC, tm.joined_at ASC NULLS LAST, tm.id ASC
		 LIMIT 1`, userID,
	).Scan(&t.ID, &t.Name, &t.Slug, &t.PlanID, &t.Status, &t.StorageBytesUsed, &t.UserCount, &t.CreatedAt,
		&lockedCents, &lockedPeriod, &t.LifecycleState, &purgeAt,
		&t.PlanName, &t.MaxStorageBytes, &t.APIAccess,
		&m.ID, &m.Role, &m.Status)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil, ErrNoActiveTenant
		}
		return nil, nil, fmt.Errorf("auth: load tenant: %w", err)
	}
	applyLockedPrice(&t, lockedCents, lockedPeriod)
	if purgeAt.Valid {
		pa := purgeAt.Time
		t.PurgeAt = &pa
	}
	m.TenantID = t.ID
	return &t, &m, nil
}

// TenantSummary is a compact view of one tenant a user can access, for the
// account selector + in-app account switcher.
type TenantSummary struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Slug     string `json:"slug"`
	Role     string `json:"role"`
	PlanName string `json:"plan_name"`
	IsOwner  bool   `json:"is_owner"`
}

// ListTenantsForUser returns every active tenant the user belongs to — owned
// accounts first, then by join order — for the account selector + switcher.
func (s *Service) ListTenantsForUser(ctx context.Context, userID int64) ([]TenantSummary, error) {
	rows, err := s.client.QueryContext(ctx, `
		SELECT t.id, t.name, t.slug, tm.role, COALESCE(p.name, '')
		  FROM tenant_members tm
		  JOIN tenants t ON t.id = tm.tenant_id
		  LEFT JOIN plans p ON p.id = t.plan_id
		 WHERE tm.user_id = $1 AND tm.status = 'active' AND t.status = 'active'
		 ORDER BY (tm.role = 'owner') DESC, tm.joined_at ASC NULLS LAST, tm.id ASC`, userID)
	if err != nil {
		return nil, fmt.Errorf("auth: list tenants: %w", err)
	}
	defer rows.Close()
	out := make([]TenantSummary, 0)
	for rows.Next() {
		var ts TenantSummary
		if err := rows.Scan(&ts.ID, &ts.Name, &ts.Slug, &ts.Role, &ts.PlanName); err != nil {
			return nil, fmt.Errorf("auth: scan tenant: %w", err)
		}
		ts.IsOwner = ts.Role == "owner"
		out = append(out, ts)
	}
	return out, rows.Err()
}

// SwitchTenant re-binds the session to a different tenant the user actively
// belongs to and returns the rebuilt identity. Rejects any tenant the user
// isn't an active member of (the security profile is on the user, so no
// re-authentication is needed — only an active-membership check).
func (s *Service) SwitchTenant(ctx context.Context, sessionID string, userID, tenantID int64) (*Identity, error) {
	tenant, membership, err := s.loadTenantAndMembership(ctx, tenantID, userID)
	if err != nil {
		return nil, err // ErrUnauthorized when the user isn't a member
	}
	if tenant.Status != "active" || membership.Status != "active" {
		return nil, ErrUnauthorized
	}
	if err := s.sessionStore.UpdateTenant(ctx, sessionID, tenantID); err != nil {
		return nil, fmt.Errorf("auth: switch tenant: %w", err)
	}
	return s.LoadIdentity(ctx, sessionID)
}

// AccessForTenant resolves a user's effective folder access within ANY tenant
// they actively belong to — not just the one their session is currently bound
// to. It exists for cross-account operations (e.g. copying a file into another
// account the user owns or is a member of) where the actor must be authorized
// against a tenant other than identity.Tenant. Returns ErrUnauthorized when the
// user has no active membership in (or the tenant itself isn't) an active row.
func (s *Service) AccessForTenant(ctx context.Context, userID, tenantID int64) (*FolderAccess, *Tenant, *Membership, error) {
	tenant, membership, err := s.loadTenantAndMembership(ctx, tenantID, userID)
	if err != nil {
		return nil, nil, nil, err // ErrUnauthorized when the user isn't a member
	}
	if tenant.Status != "active" || membership.Status != "active" {
		return nil, nil, nil, ErrUnauthorized
	}
	access, err := s.buildFolderAccess(ctx, tenantID, userID, membership)
	if err != nil {
		return nil, nil, nil, err
	}
	return access, tenant, membership, nil
}

// applyLockedPrice copies the nullable snapshot-locked price columns onto a Tenant.
func applyLockedPrice(t *Tenant, cents sql.NullInt64, period sql.NullString) {
	if cents.Valid {
		v := int(cents.Int64)
		t.LockedPriceCents = &v
	}
	t.LockedBillingPeriod = period.String
}

func (s *Service) loadTenantAndMembership(ctx context.Context, tenantID, userID int64) (*Tenant, *Membership, error) {
	var t Tenant
	var m Membership
	var lockedCents sql.NullInt64
	var lockedPeriod sql.NullString
	var purgeAt sql.NullTime
	err := s.client.QueryRowContext(ctx, `
		SELECT t.id, t.name, t.slug, t.plan_id, t.status, t.storage_bytes_used, t.user_count, t.created_at,
		       t.locked_price_cents, t.locked_billing_period, t.lifecycle_state, t.purge_at,
		       COALESCE(p.name, ''), COALESCE(p.max_storage_bytes, 9223372036854775807), COALESCE(p.api_access, false),
		       tm.id, tm.role, tm.status
		  FROM tenants t
		  JOIN tenant_members tm ON tm.tenant_id = t.id AND tm.user_id = $2
		  LEFT JOIN plans p ON p.id = t.plan_id
		 WHERE t.id = $1`, tenantID, userID,
	).Scan(&t.ID, &t.Name, &t.Slug, &t.PlanID, &t.Status, &t.StorageBytesUsed, &t.UserCount, &t.CreatedAt,
		&lockedCents, &lockedPeriod, &t.LifecycleState, &purgeAt,
		&t.PlanName, &t.MaxStorageBytes, &t.APIAccess,
		&m.ID, &m.Role, &m.Status)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil, ErrUnauthorized
		}
		return nil, nil, fmt.Errorf("auth: load tenant+membership: %w", err)
	}
	applyLockedPrice(&t, lockedCents, lockedPeriod)
	if purgeAt.Valid {
		pa := purgeAt.Time
		t.PurgeAt = &pa
	}
	m.TenantID = t.ID
	return &t, &m, nil
}

// ChangePlan assigns the tenant to the plan identified by code and snapshot-locks
// the price for the chosen billing period (the hybrid model: allowances follow
// the plan live via plan_id, but the price is frozen here). Owner/admin gated at
// the handler. period is 'monthly' or 'annual' (defaults to monthly).
func (s *Service) ChangePlan(ctx context.Context, tenantID int64, code, period string) error {
	if period != "annual" {
		period = "monthly"
	}
	var planID, priceMonthly, discount int64
	var status string
	err := s.client.QueryRowContext(ctx,
		`SELECT id, price_monthly_cents, annual_discount_pct, status FROM plans WHERE code = $1`, code,
	).Scan(&planID, &priceMonthly, &discount, &status)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrPlanNotFound
	}
	if err != nil {
		return fmt.Errorf("auth: load plan %q: %w", code, err)
	}
	if status != "available" {
		return ErrPlanUnavailable
	}
	locked := priceMonthly
	if period == "annual" {
		// Per-month rate when billed annually, integer-rounded to the nearest cent.
		locked = (priceMonthly*(100-discount) + 50) / 100
	}
	if _, err := s.client.ExecContext(ctx,
		`UPDATE tenants
		    SET plan_id = $1, locked_price_cents = $2, locked_billing_period = $3, locked_at = NOW()
		  WHERE id = $4`,
		planID, locked, period, tenantID,
	); err != nil {
		return fmt.Errorf("auth: change plan: %w", err)
	}
	logging.Info(packageName, "ChangePlan: tenant=%d -> %s (%s, locked %d¢)", tenantID, code, period, locked)
	return nil
}

// BillingAddress is an account's billing address (captured at checkout).
type BillingAddress struct {
	Name       string `json:"name"`
	Line1      string `json:"line1"`
	Line2      string `json:"line2"`
	City       string `json:"city"`
	State      string `json:"state"`
	PostalCode string `json:"postal_code"`
	Country    string `json:"country"`
}

// SaveBillingAddress stores the account's billing address on the tenant.
func (s *Service) SaveBillingAddress(ctx context.Context, tenantID int64, a BillingAddress) error {
	_, err := s.client.ExecContext(ctx, `
		UPDATE tenants
		   SET billing_name = $1, billing_line1 = $2, billing_line2 = $3, billing_city = $4,
		       billing_state = $5, billing_postal_code = $6, billing_country = $7, updated_at = NOW()
		 WHERE id = $8`,
		a.Name, a.Line1, a.Line2, a.City, a.State, a.PostalCode, a.Country, tenantID,
	)
	if err != nil {
		return fmt.Errorf("auth: save billing address: %w", err)
	}
	return nil
}

// ----- small helpers --------------------------------------------------------

var emailRE = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)

func normalizeEmail(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

var slugUnsafe = regexp.MustCompile(`[^a-z0-9]+`)

func slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = slugUnsafe.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if len(s) > 48 {
		s = s[:48]
	}
	return s
}

func pgUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "23505"
	}
	return false
}

// ListTenantMembers returns every user with a membership row in the given
// tenant, oldest first. Includes invited/suspended members so the UI can
// render them with the appropriate status pill.
func (s *Service) ListTenantMembers(ctx context.Context, tenantID int64) ([]*TenantMember, error) {
	rows, err := s.client.QueryContext(ctx, `
		SELECT u.id, u.email, u.full_name, tm.role, tm.status, tm.created_at
		  FROM tenant_members tm
		  JOIN users u ON u.id = tm.user_id
		 WHERE tm.tenant_id = $1
		 ORDER BY tm.created_at ASC, tm.id ASC`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("auth: list tenant members: %w", err)
	}
	defer rows.Close()

	out := make([]*TenantMember, 0)
	for rows.Next() {
		var m TenantMember
		if err := rows.Scan(&m.ID, &m.Email, &m.FullName, &m.Role, &m.Status, &m.CreatedAt); err != nil {
			return nil, fmt.Errorf("auth: scan tenant member: %w", err)
		}
		out = append(out, &m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("auth: tenant members rows: %w", err)
	}
	return out, nil
}

// RemoveTenantMember removes a user's membership from a tenant. Used by
// the Users page "delete" action. Refuses to:
//   - remove the Owner (ownership must be transferred first)
//   - remove the caller themselves (avoid accidental self-lockout)
//   - remove a row that doesn't exist in this tenant
//
// On success, also revokes the removed user's sessions so they're booted
// from the SPA on their next request — otherwise their cookie stays valid
// until expiry and /api/me would 200 with a stale tenant context.
func (s *Service) RemoveTenantMember(ctx context.Context, tenantID, callerID, targetID int64) error {
	if callerID == targetID {
		return ErrCannotRemoveSelf
	}

	var role string
	err := s.client.QueryRowContext(ctx,
		`SELECT role FROM tenant_members WHERE tenant_id = $1 AND user_id = $2`,
		tenantID, targetID,
	).Scan(&role)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrMemberNotFound
	}
	if err != nil {
		return fmt.Errorf("auth: lookup member: %w", err)
	}
	if strings.EqualFold(role, "owner") {
		return ErrCannotRemoveOwner
	}

	// The tenant Owner inherits the removed member's authored content (files,
	// folders, shares, …) so nothing is lost when the user is deleted.
	var ownerID int64
	if err := s.client.QueryRowContext(ctx,
		`SELECT user_id FROM tenant_members WHERE tenant_id = $1 AND role = 'owner' LIMIT 1`,
		tenantID,
	).Scan(&ownerID); err != nil {
		return fmt.Errorf("auth: lookup owner: %w", err)
	}

	db := s.client.GetDB()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("auth: begin tx: %w", err)
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx,
		`DELETE FROM tenant_members WHERE tenant_id = $1 AND user_id = $2`,
		tenantID, targetID,
	)
	if err != nil {
		return fmt.Errorf("auth: delete tenant member: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrMemberNotFound
	}

	// If the user no longer belongs to ANY tenant, delete the user entirely so
	// the account is fully disassociated and its email frees up for re-signup.
	// (A user who still belongs to other tenants is only detached from this one.)
	var remaining int
	if err := tx.QueryRowContext(ctx,
		`SELECT count(*) FROM tenant_members WHERE user_id = $1`, targetID,
	).Scan(&remaining); err != nil {
		return fmt.Errorf("auth: count memberships: %w", err)
	}
	if remaining == 0 {
		// Reassign NOT-NULL authored content to the Owner (these FKs are NO
		// ACTION + NOT NULL, so they'd otherwise block the user delete and the
		// content is the workspace's, not the departing member's).
		for _, q := range []string{
			`UPDATE files        SET uploaded_by_user_id = $1 WHERE uploaded_by_user_id = $2`,
			`UPDATE folders      SET created_by_user_id  = $1 WHERE created_by_user_id  = $2`,
			`UPDATE shares       SET created_by_user_id  = $1 WHERE created_by_user_id  = $2`,
			`UPDATE drop_portals SET created_by_user_id  = $1 WHERE created_by_user_id  = $2`,
			`UPDATE api_keys     SET created_by_user_id  = $1 WHERE created_by_user_id  = $2`,
			`UPDATE invites      SET created_by_user_id  = $1 WHERE created_by_user_id  = $2`,
		} {
			if _, err := tx.ExecContext(ctx, q, ownerID, targetID); err != nil {
				return fmt.Errorf("auth: reassign content to owner: %w", err)
			}
		}
		// NULL the nullable "by-whom" references so they don't dangle.
		for _, q := range []string{
			`UPDATE invites           SET accepted_by_user_id = NULL WHERE accepted_by_user_id = $1`,
			`UPDATE tenant_members    SET invited_by_user_id  = NULL WHERE invited_by_user_id  = $1`,
			`UPDATE workspace_members SET assigned_by_user_id = NULL WHERE assigned_by_user_id = $1`,
			`UPDATE workspaces        SET created_by_user_id  = NULL WHERE created_by_user_id  = $1`,
		} {
			if _, err := tx.ExecContext(ctx, q, targetID); err != nil {
				return fmt.Errorf("auth: detach references: %w", err)
			}
		}
		// Delete the user. CASCADE clears sessions, 2FA, passkeys, recovery
		// codes, notification prefs, transfer usage, workspace memberships, and
		// any shares granted to them. audit_events keeps the row (no FK +
		// actor_email retained) so the history survives.
		if _, err := tx.ExecContext(ctx, `DELETE FROM users WHERE id = $1`, targetID); err != nil {
			return fmt.Errorf("auth: delete user: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("auth: commit remove-member tx: %w", err)
	}

	// Revoke any sessions. If the user was deleted this is a CASCADE no-op;
	// otherwise it boots the (now detached) user out of any open SPA.
	if err := s.sessionStore.RevokeAllForUser(ctx, targetID); err != nil {
		logging.Error(packageName, "RemoveTenantMember: revoke sessions failed for user %d: %v", targetID, err)
	}
	return nil
}

// SetTenantMemberStatus flips a member's tenant_members.status. Used
// by the Users-page bulk Status toolbar to disable / re-enable
// members without removing them outright. Refuses to:
//   - change the Owner's status (ownership is immutable)
//   - change the caller's own status (avoid self-lockout)
//   - touch a non-existent membership row
//
// When the new status is "suspended", all of the target user's
// sessions are revoked so any open SPA they have gets booted on its
// next request (auth middleware also rejects the cookie because the
// membership.status check fails).
func (s *Service) SetTenantMemberStatus(ctx context.Context, tenantID, callerID, targetID int64, newStatus string) error {
	newStatus = strings.ToLower(strings.TrimSpace(newStatus))
	if newStatus != "active" && newStatus != "suspended" {
		return ErrInvalidStatus
	}
	if callerID == targetID {
		return ErrCannotChangeSelfStatus
	}

	var role string
	err := s.client.QueryRowContext(ctx,
		`SELECT role FROM tenant_members WHERE tenant_id = $1 AND user_id = $2`,
		tenantID, targetID,
	).Scan(&role)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrMemberNotFound
	}
	if err != nil {
		return fmt.Errorf("auth: lookup member status: %w", err)
	}
	if strings.EqualFold(role, "owner") {
		return ErrCannotDisableOwner
	}

	res, err := s.client.ExecContext(ctx,
		`UPDATE tenant_members SET status = $1 WHERE tenant_id = $2 AND user_id = $3`,
		newStatus, tenantID, targetID,
	)
	if err != nil {
		return fmt.Errorf("auth: update member status: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrMemberNotFound
	}

	if newStatus == "suspended" {
		// Best-effort revoke. If this fails the membership row is still
		// suspended, so auth middleware will reject the cookie on the
		// next request — we just don't preemptively kill open sessions.
		if err := s.sessionStore.RevokeAllForUser(ctx, targetID); err != nil {
			logging.Error(packageName, "SetTenantMemberStatus: revoke sessions failed for user %d: %v", targetID, err)
		}
	}
	logging.Info(packageName, "SetTenantMemberStatus: tenant=%d caller=%d target=%d status=%s",
		tenantID, callerID, targetID, newStatus)
	return nil
}

// SetTenantMemberFolders replaces the entire set of folder grants for
// a Member or Viewer in this tenant. Owners are refused (their access
// is always full-workspace). Validates each folder exists in the
// tenant, dedupes parent+child entries to the topmost folder per
// branch, then runs DELETE + INSERTs in one transaction so the
// member's effective access never falls into a half-applied state.
func (s *Service) SetTenantMemberFolders(ctx context.Context, tenantID, callerID, targetID int64, folderIDs []int64) error {
	// Confirm target exists in this tenant and isn't the Owner.
	var (
		membershipID int64
		role         string
	)
	err := s.client.QueryRowContext(ctx,
		`SELECT id, role FROM tenant_members WHERE tenant_id = $1 AND user_id = $2`,
		tenantID, targetID,
	).Scan(&membershipID, &role)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrMemberNotFound
	}
	if err != nil {
		return fmt.Errorf("auth: load member for folder set: %w", err)
	}
	if strings.EqualFold(role, "owner") {
		return ErrCannotDisableOwner // reuse: same "owner immutable" sentinel
	}
	// Admins also have full access; setting their grants is a no-op but
	// we don't error — just delete any rows so the schema stays clean.

	// Validate folder ids belong to this tenant + are active.
	if len(folderIDs) > 0 {
		if err := s.validateTenantFolders(ctx, tenantID, folderIDs); err != nil {
			return err
		}
	}
	// Dedupe to topmost folder per branch.
	dedup, err := s.dedupeFolderSet(ctx, folderIDs)
	if err != nil {
		return err
	}

	// Apply atomically.
	tx, err := s.client.GetDB().BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("auth: begin tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	if _, err := tx.ExecContext(ctx,
		`DELETE FROM tenant_member_folders WHERE tenant_member_id = $1`,
		membershipID,
	); err != nil {
		return fmt.Errorf("auth: clear folder grants: %w", err)
	}
	for _, fid := range dedup {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO tenant_member_folders (tenant_member_id, folder_id) VALUES ($1, $2)`,
			membershipID, fid,
		); err != nil {
			return fmt.Errorf("auth: insert folder grant: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("auth: commit folder grants: %w", err)
	}
	committed = true

	logging.Info(packageName, "SetTenantMemberFolders: tenant=%d caller=%d target=%d folders=%v",
		tenantID, callerID, targetID, dedup)
	return nil
}

// buildFolderAccess resolves the effective folder access for a membership.
// Owner/admin get unrestricted access; member/viewer get a scoped set
// (viewer being read-only). For scoped roles the granted folders are
// expanded into their full descendant set so handlers can do a single
// in-memory lookup per operation rather than walking the tree each time.
func (s *Service) buildFolderAccess(ctx context.Context, tenantID, userID int64, m *Membership) (*FolderAccess, error) {
	if IsAdminOrOwner(m.Role) {
		return &FolderAccess{Full: true, Write: true}, nil
	}
	// Scoped role. "viewer" is read-only; "member" may write within reach.
	// The accessible folder set is the UNION of two grant types:
	//   - whole-workspace: every folder in a workspace the user is assigned to
	//     (workspace_members)
	//   - folder-scoped: explicitly granted folders + their descendants
	//     (tenant_member_folders) — a narrower "folder depth" inside a workspace
	write := !strings.EqualFold(strings.TrimSpace(m.Role), "viewer")
	folders, err := s.accessibleWorkspaceFolders(ctx, tenantID, userID)
	if err != nil {
		return nil, err
	}
	grants, err := s.expandAccessibleFolders(ctx, tenantID, m.ID)
	if err != nil {
		return nil, err
	}
	for id := range grants {
		folders[id] = true
	}
	workspaces, err := s.assignedWorkspaces(ctx, tenantID, userID)
	if err != nil {
		return nil, err
	}
	return &FolderAccess{Full: false, Write: write, Folders: folders, Workspaces: workspaces}, nil
}

// assignedWorkspaces returns the ids of this tenant's workspaces the user is
// assigned to wholesale (workspace_members) — the set that grants root-level
// writes inside those workspaces for scoped members.
func (s *Service) assignedWorkspaces(ctx context.Context, tenantID, userID int64) (map[int64]bool, error) {
	rows, err := s.client.QueryContext(ctx, `
		SELECT w.id
		  FROM workspaces w
		  JOIN workspace_members wm ON wm.workspace_id = w.id
		 WHERE w.tenant_id = $1 AND w.status = 'active' AND wm.user_id = $2`, tenantID, userID)
	if err != nil {
		return nil, fmt.Errorf("auth: assigned workspaces: %w", err)
	}
	defer rows.Close()
	out := make(map[int64]bool)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("auth: scan assigned workspace: %w", err)
		}
		out[id] = true
	}
	return out, rows.Err()
}

// accessibleWorkspaceFolders returns the set of folder ids a scoped member may
// reach: every active folder belonging to a workspace the user is assigned to.
// Whole-workspace access means no per-folder grant or descendant walk — every
// folder in an assigned workspace is reachable in one query.
func (s *Service) accessibleWorkspaceFolders(ctx context.Context, tenantID, userID int64) (map[int64]bool, error) {
	rows, err := s.client.QueryContext(ctx, `
		SELECT f.id
		  FROM folders f
		  JOIN workspace_members wm ON wm.workspace_id = f.workspace_id
		  JOIN workspaces w ON w.id = wm.workspace_id
		 WHERE f.tenant_id = $1 AND f.status = 'active'
		   AND wm.user_id = $2 AND w.status = 'active'`, tenantID, userID)
	if err != nil {
		return nil, fmt.Errorf("auth: accessible workspace folders: %w", err)
	}
	defer rows.Close()
	out := make(map[int64]bool)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("auth: scan workspace folder: %w", err)
		}
		out[id] = true
	}
	return out, rows.Err()
}

// MemberWorkspaceRootFolderIDs returns the top-level (parent IS NULL) folder
// ids of every workspace the user is assigned to. Browse uses these as a scoped
// member's entry points at the tenant root when no workspace is selected.
func (s *Service) MemberWorkspaceRootFolderIDs(ctx context.Context, tenantID, userID int64) ([]int64, error) {
	rows, err := s.client.QueryContext(ctx, `
		SELECT f.id
		  FROM folders f
		  JOIN workspace_members wm ON wm.workspace_id = f.workspace_id
		  JOIN workspaces w ON w.id = wm.workspace_id
		 WHERE f.tenant_id = $1 AND f.status = 'active'
		   AND f.parent_folder_id IS NULL
		   AND wm.user_id = $2 AND w.status = 'active'`, tenantID, userID)
	if err != nil {
		return nil, fmt.Errorf("auth: member workspace root folders: %w", err)
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("auth: scan workspace root folder: %w", err)
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// MemberGrantFolderIDs returns the folders explicitly granted to a
// membership — the topmost entry points, NOT their descendants. The browse
// handler uses these to surface a scoped member's accessible folders at the
// tenant root (where they have no blanket access).
func (s *Service) MemberGrantFolderIDs(ctx context.Context, tenantID, membershipID int64) ([]int64, error) {
	rows, err := s.client.QueryContext(ctx, `
		SELECT f.id
		  FROM tenant_member_folders tmf
		  JOIN folders f ON f.id = tmf.folder_id
		 WHERE tmf.tenant_member_id = $1
		   AND f.tenant_id = $2 AND f.status = 'active'`, membershipID, tenantID)
	if err != nil {
		return nil, fmt.Errorf("auth: member grant folders: %w", err)
	}
	defer rows.Close()

	out := make([]int64, 0)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("auth: scan grant folder: %w", err)
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// FolderRef is a minimal folder id+name pair.
type FolderRef struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// CurrentUserGrantRoots returns the caller's own top-level folders. full=true
// means unrestricted (owner/admin — the whole workspace is their root). For a
// scoped member/viewer, full=false and roots are their granted folders, which
// act as their top-level workspace (the SPA homes them into / bounds them to
// these). An empty roots slice for a scoped user means no access.
func (s *Service) CurrentUserGrantRoots(ctx context.Context, identity *Identity) (bool, []FolderRef, error) {
	if identity == nil || identity.Membership == nil || IsAdminOrOwner(identity.Membership.Role) {
		return true, []FolderRef{}, nil
	}
	// Whole-workspace access: a scoped member's entry points are the top-level
	// folders of every workspace they're assigned to.
	rows, err := s.client.QueryContext(ctx, `
		SELECT f.id, f.name
		  FROM folders f
		  JOIN workspace_members wm ON wm.workspace_id = f.workspace_id
		  JOIN workspaces w ON w.id = wm.workspace_id
		 WHERE f.tenant_id = $1 AND f.status = 'active'
		   AND f.parent_folder_id IS NULL
		   AND wm.user_id = $2 AND w.status = 'active'
		 ORDER BY lower(f.name)`, identity.Tenant.ID, identity.User.ID)
	if err != nil {
		return false, nil, fmt.Errorf("auth: current user grant roots: %w", err)
	}
	defer rows.Close()
	roots := make([]FolderRef, 0)
	for rows.Next() {
		var fr FolderRef
		if err := rows.Scan(&fr.ID, &fr.Name); err != nil {
			return false, nil, fmt.Errorf("auth: scan grant root: %w", err)
		}
		roots = append(roots, fr)
	}
	return false, roots, rows.Err()
}

// TenantMemberFolderIDs returns the folder grants for a target member by
// user id (resolving their membership), tenant-scoped. Returns an empty
// slice for owner/admin (full access, no grant rows) or a member with no
// grants, and ErrMemberNotFound if the user has no membership in this
// tenant. Backs the GET that pre-fills the bulk Folder Access modal.
func (s *Service) TenantMemberFolderIDs(ctx context.Context, tenantID, targetUserID int64) ([]int64, error) {
	var membershipID int64
	err := s.client.QueryRowContext(ctx,
		`SELECT id FROM tenant_members WHERE tenant_id = $1 AND user_id = $2`,
		tenantID, targetUserID,
	).Scan(&membershipID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrMemberNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("auth: load member for folders: %w", err)
	}
	return s.MemberGrantFolderIDs(ctx, tenantID, membershipID)
}

// FolderGrant pairs a granted folder with the workspace it lives in.
type FolderGrant struct {
	FolderID    int64 `json:"folder_id"`
	WorkspaceID int64 `json:"workspace_id"`
}

// TenantMemberFolderGrants is TenantMemberFolderIDs plus each grant's workspace,
// so the Workspace Access modal can render a workspace as partially selected
// (folder-scoped) without expanding its tree.
func (s *Service) TenantMemberFolderGrants(ctx context.Context, tenantID, targetUserID int64) ([]FolderGrant, error) {
	var membershipID int64
	err := s.client.QueryRowContext(ctx,
		`SELECT id FROM tenant_members WHERE tenant_id = $1 AND user_id = $2`,
		tenantID, targetUserID,
	).Scan(&membershipID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrMemberNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("auth: load member for grants: %w", err)
	}
	rows, err := s.client.QueryContext(ctx, `
		SELECT f.id, f.workspace_id
		  FROM tenant_member_folders tmf
		  JOIN folders f ON f.id = tmf.folder_id
		 WHERE tmf.tenant_member_id = $1 AND f.tenant_id = $2 AND f.status = 'active'`,
		membershipID, tenantID)
	if err != nil {
		return nil, fmt.Errorf("auth: member folder grants: %w", err)
	}
	defer rows.Close()
	out := make([]FolderGrant, 0)
	for rows.Next() {
		var g FolderGrant
		if err := rows.Scan(&g.FolderID, &g.WorkspaceID); err != nil {
			return nil, fmt.Errorf("auth: scan folder grant: %w", err)
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// expandAccessibleFolders returns the set of folder ids a scoped member may
// reach: every folder granted to the membership plus all active descendants.
// Seeded directly from the tenant_member_folders junction so no id array
// needs to round-trip through the driver. UNION (not UNION ALL) guards
// against pathological cycles.
func (s *Service) expandAccessibleFolders(ctx context.Context, tenantID, membershipID int64) (map[int64]bool, error) {
	rows, err := s.client.QueryContext(ctx, `
		WITH RECURSIVE granted AS (
			SELECT f.id
			  FROM tenant_member_folders tmf
			  JOIN folders f ON f.id = tmf.folder_id
			 WHERE tmf.tenant_member_id = $1
			   AND f.tenant_id = $2 AND f.status = 'active'
			UNION
			SELECT c.id
			  FROM folders c
			  JOIN granted g ON c.parent_folder_id = g.id
			 WHERE c.tenant_id = $2 AND c.status = 'active'
		)
		SELECT id FROM granted`, membershipID, tenantID)
	if err != nil {
		return nil, fmt.Errorf("auth: expand accessible folders: %w", err)
	}
	defer rows.Close()

	out := make(map[int64]bool)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("auth: scan accessible folder: %w", err)
		}
		out[id] = true
	}
	return out, rows.Err()
}

// validateTenantFolders + dedupeFolderSet are mirrored from the invites
// service (same logic). Kept here so auth doesn't import invites.
func (s *Service) validateTenantFolders(ctx context.Context, tenantID int64, ids []int64) error {
	for _, id := range ids {
		var ok bool
		if err := s.client.QueryRowContext(ctx,
			`SELECT EXISTS(SELECT 1 FROM folders WHERE id = $1 AND tenant_id = $2 AND status = 'active')`,
			id, tenantID,
		).Scan(&ok); err != nil {
			return fmt.Errorf("auth: folder check: %w", err)
		}
		if !ok {
			return ErrInvalidFolder
		}
	}
	return nil
}

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
			return nil, fmt.Errorf("auth: walk ancestors: %w", err)
		}
		redundant := false
		for rows.Next() {
			var parent int64
			if err := rows.Scan(&parent); err != nil {
				rows.Close()
				return nil, fmt.Errorf("auth: scan ancestor: %w", err)
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

// isEmailUniqueViolation specifically classifies a UNIQUE violation on the
// users.email column (constraint name "users_email_key"), distinct from the
// users PK collision.
func isEmailUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23505" {
		return false
	}
	return strings.Contains(strings.ToLower(pgErr.ConstraintName), "email")
}
