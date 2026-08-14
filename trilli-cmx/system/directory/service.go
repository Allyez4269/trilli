package directory

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"trilli-cmx/system/database/postgres"
)

// ErrNotFound is returned when a customer/tenant does not exist.
var ErrNotFound = errors.New("directory: not found")

// Service runs read queries over the shared TRILLI database (plus operator-note
// writes to the CMX-owned cmx_customer_notes).
type Service struct {
	db *postgres.Client
}

// NewService constructs the directory Service.
func NewService(db *postgres.Client) *Service { return &Service{db: db} }

// nameExpr builds a display name from the app's users columns, preferring
// full_name, then "first last", then email.
const nameExpr = `COALESCE(NULLIF(u.full_name, ''), ` +
	`NULLIF(TRIM(COALESCE(u.first_name, '') || ' ' || COALESCE(u.last_name, '')), ''), ` +
	`u.email)`

func ptr(t sql.NullTime) *time.Time {
	if t.Valid {
		return &t.Time
	}
	return nil
}

func clampLimit(limit int) int {
	if limit <= 0 || limit > 500 {
		return 100
	}
	return limit
}

// likeArg turns a free-text query into an ILIKE pattern, or "" for no filter.
func likeArg(q string) string {
	q = strings.TrimSpace(q)
	if q == "" {
		return ""
	}
	return "%" + q + "%"
}

// ListCustomers returns owning identities (users who own >=1 tenant), with their
// CRM overlay and owned-account count. Searchable by email/name/company.
func (s *Service) ListCustomers(ctx context.Context, q string, limit int) ([]CustomerListItem, error) {
	pat := likeArg(q)
	rows, err := s.db.QueryContext(ctx, `
		SELECT u.id, u.email, `+nameExpr+` AS name,
		       COALESCE(c.company, '') AS company,
		       COALESCE(c.lifecycle_stage, 'paying') AS stage,
		       u.status, u.created_at, u.last_login_at,
		       (SELECT count(*) FROM tenant_members tm2
		         WHERE tm2.user_id = u.id AND tm2.role = 'owner') AS account_count
		  FROM users u
		  JOIN tenant_members tm ON tm.user_id = u.id AND tm.role = 'owner'
		  LEFT JOIN cmx_customers c ON c.identity_user_id = u.id
		 WHERE ($1 = '' OR u.email ILIKE $1 OR `+nameExpr+` ILIKE $1
		        OR COALESCE(c.company, '') ILIKE $1)
		 GROUP BY u.id, u.email, u.full_name, u.first_name, u.last_name,
		          c.company, c.lifecycle_stage, u.status, u.created_at, u.last_login_at
		 ORDER BY u.created_at DESC
		 LIMIT $2`, pat, clampLimit(limit))
	if err != nil {
		return nil, fmt.Errorf("directory: list customers: %w", err)
	}
	defer rows.Close()

	var out []CustomerListItem
	for rows.Next() {
		var it CustomerListItem
		var last sql.NullTime
		if err := rows.Scan(&it.IdentityUserID, &it.Email, &it.Name, &it.Company,
			&it.LifecycleStage, &it.Status, &it.CreatedAt, &last, &it.AccountCount); err != nil {
			return nil, fmt.Errorf("directory: scan customer: %w", err)
		}
		it.LastLoginAt = ptr(last)
		out = append(out, it)
	}
	return out, rows.Err()
}

// GetCustomer loads a customer (identity) with owned accounts, memberships, the
// CRM overlay, and operator notes.
func (s *Service) GetCustomer(ctx context.Context, identityUserID int64) (*CustomerDetail, error) {
	var d CustomerDetail
	var last sql.NullTime
	var verified sql.NullTime
	err := s.db.QueryRowContext(ctx, `
		SELECT u.id, u.email, `+nameExpr+` AS name, u.status,
		       u.email_verified_at, u.created_at, u.last_login_at,
		       COALESCE(c.company, ''),
		       COALESCE(c.lifecycle_stage, 'paying')
		  FROM users u
		  LEFT JOIN cmx_customers c ON c.identity_user_id = u.id
		 WHERE u.id = $1`, identityUserID,
	).Scan(&d.IdentityUserID, &d.Email, &d.Name, &d.Status, &verified,
		&d.CreatedAt, &last, &d.Company, &d.LifecycleStage)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("directory: get customer: %w", err)
	}
	d.LastLoginAt = ptr(last)
	d.EmailVerified = verified.Valid

	owned, err := s.accountsForUser(ctx, identityUserID, true)
	if err != nil {
		return nil, err
	}
	d.OwnedAccounts = owned
	memberships, err := s.accountsForUser(ctx, identityUserID, false)
	if err != nil {
		return nil, err
	}
	d.Memberships = memberships

	notes, err := s.notesForCustomer(ctx, identityUserID)
	if err != nil {
		return nil, err
	}
	d.Notes = notes
	return &d, nil
}

// accountsForUser returns the tenants a user owns (ownedOnly=true) or is a
// non-owner member of (ownedOnly=false).
func (s *Service) accountsForUser(ctx context.Context, userID int64, ownedOnly bool) ([]AccountBrief, error) {
	cmp := "="
	if !ownedOnly {
		cmp = "<>"
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT t.id, t.name, tm.role, tm.status,
		       COALESCE(p.code, ''), t.status, COALESCE(t.lifecycle_state, '')
		  FROM tenant_members tm
		  JOIN tenants t ON t.id = tm.tenant_id
		  LEFT JOIN plans p ON p.id = t.plan_id
		 WHERE tm.user_id = $1 AND tm.role `+cmp+` 'owner'
		 ORDER BY t.created_at`, userID)
	if err != nil {
		return nil, fmt.Errorf("directory: accounts for user: %w", err)
	}
	defer rows.Close()
	var out []AccountBrief
	for rows.Next() {
		var a AccountBrief
		if err := rows.Scan(&a.TenantID, &a.Name, &a.Role, &a.MemberStatus,
			&a.PlanCode, &a.TenantStatus, &a.LifecycleState); err != nil {
			return nil, fmt.Errorf("directory: scan account brief: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Service) notesForCustomer(ctx context.Context, identityUserID int64) ([]CustomerNote, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT n.id, n.operator_id, COALESCE(o.email, ''), n.body, n.created_at
		  FROM cmx_customer_notes n
		  JOIN cmx_customers c ON c.id = n.customer_id
		  LEFT JOIN cmx_operators o ON o.id = n.operator_id
		 WHERE c.identity_user_id = $1
		 ORDER BY n.created_at DESC`, identityUserID)
	if err != nil {
		return nil, fmt.Errorf("directory: notes: %w", err)
	}
	defer rows.Close()
	var out []CustomerNote
	for rows.Next() {
		var n CustomerNote
		if err := rows.Scan(&n.ID, &n.OperatorID, &n.OperatorEmail, &n.Body, &n.CreatedAt); err != nil {
			return nil, fmt.Errorf("directory: scan note: %w", err)
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// AddNote attaches an operator note to a customer, lazily creating the
// cmx_customers overlay row keyed by identity_user_id. Returns the new note.
func (s *Service) AddNote(ctx context.Context, identityUserID, operatorID int64, operatorEmail, body string) (*CustomerNote, error) {
	body = strings.TrimSpace(body)
	if body == "" {
		return nil, fmt.Errorf("directory: empty note")
	}
	// Verify the identity exists (and snapshot email for the lazy overlay).
	var email string
	if err := s.db.QueryRowContext(ctx, `SELECT email FROM users WHERE id = $1`, identityUserID).
		Scan(&email); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("directory: verify identity: %w", err)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("directory: begin: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	var customerID int64
	err = tx.QueryRowContext(ctx,
		`SELECT id FROM cmx_customers WHERE identity_user_id = $1`, identityUserID).Scan(&customerID)
	if errors.Is(err, sql.ErrNoRows) {
		if err := tx.QueryRowContext(ctx, `
			INSERT INTO cmx_customers (identity_user_id, primary_email)
			VALUES ($1, $2) RETURNING id`, identityUserID, email).Scan(&customerID); err != nil {
			return nil, fmt.Errorf("directory: create overlay: %w", err)
		}
	} else if err != nil {
		return nil, fmt.Errorf("directory: find overlay: %w", err)
	}

	var note CustomerNote
	note.OperatorID = operatorID
	note.OperatorEmail = operatorEmail
	note.Body = body
	if err := tx.QueryRowContext(ctx, `
		INSERT INTO cmx_customer_notes (customer_id, operator_id, body)
		VALUES ($1, $2, $3) RETURNING id, created_at`,
		customerID, operatorID, body).Scan(&note.ID, &note.CreatedAt); err != nil {
		return nil, fmt.Errorf("directory: insert note: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("directory: commit note: %w", err)
	}
	committed = true
	return &note, nil
}

// ownerEmailExpr is a correlated subquery for a tenant's owner email.
const ownerEmailExpr = `COALESCE((SELECT u.email FROM tenant_members tm
	JOIN users u ON u.id = tm.user_id
	WHERE tm.tenant_id = t.id AND tm.role = 'owner' LIMIT 1), '')`

// ListTenants returns the Accounts directory, searchable by name/slug and
// filterable by status.
func (s *Service) ListTenants(ctx context.Context, q, status string, limit int) ([]TenantListItem, error) {
	pat := likeArg(q)
	rows, err := s.db.QueryContext(ctx, `
		SELECT t.id, t.name, COALESCE(t.slug, ''), t.status, COALESCE(t.lifecycle_state, ''),
		       COALESCE(p.code, ''), COALESCE(p.name, ''),
		       COALESCE(t.storage_bytes_used, 0), COALESCE(p.max_storage_bytes, 0),
		       COALESCE(t.user_count, 0), COALESCE(t.extra_seats, 0),
		       COALESCE(t.subscription_status, ''),
		       `+ownerEmailExpr+`, t.created_at
		  FROM tenants t
		  LEFT JOIN plans p ON p.id = t.plan_id
		 WHERE ($1 = '' OR t.name ILIKE $1 OR COALESCE(t.slug, '') ILIKE $1)
		   AND ($2 = '' OR t.status = $2)
		 ORDER BY t.created_at DESC
		 LIMIT $3`, pat, strings.TrimSpace(status), clampLimit(limit))
	if err != nil {
		return nil, fmt.Errorf("directory: list tenants: %w", err)
	}
	defer rows.Close()
	var out []TenantListItem
	for rows.Next() {
		var t TenantListItem
		if err := rows.Scan(&t.ID, &t.Name, &t.Slug, &t.Status, &t.LifecycleState,
			&t.PlanCode, &t.PlanName, &t.StorageBytesUsed, &t.StorageBytesMax,
			&t.UserCount, &t.ExtraSeats, &t.SubscriptionStatus, &t.OwnerEmail, &t.CreatedAt); err != nil {
			return nil, fmt.Errorf("directory: scan tenant: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// GetTenant loads the full tenant view: billing/lifecycle snapshot, members, and
// workspaces.
func (s *Service) GetTenant(ctx context.Context, tenantID int64) (*TenantDetail, error) {
	var d TenantDetail
	var periodEnd, lapsed, purge sql.NullTime
	err := s.db.QueryRowContext(ctx, `
		SELECT t.id, t.name, COALESCE(t.slug, ''), t.status, COALESCE(t.lifecycle_state, ''),
		       COALESCE(p.code, ''), COALESCE(p.name, ''),
		       COALESCE(t.storage_bytes_used, 0), COALESCE(p.max_storage_bytes, 0),
		       COALESCE(t.user_count, 0), COALESCE(t.extra_seats, 0),
		       COALESCE(t.locked_price_cents, 0), COALESCE(t.locked_billing_period, ''),
		       COALESCE(t.subscription_status, ''), COALESCE(t.auto_renew, false),
		       t.current_period_end, COALESCE(t.scheduled_plan_code, ''),
		       t.lapsed_at, t.purge_at, COALESCE(t.stripe_customer_id, ''),
		       `+ownerEmailExpr+`, t.created_at
		  FROM tenants t
		  LEFT JOIN plans p ON p.id = t.plan_id
		 WHERE t.id = $1`, tenantID,
	).Scan(&d.ID, &d.Name, &d.Slug, &d.Status, &d.LifecycleState,
		&d.PlanCode, &d.PlanName, &d.StorageBytesUsed, &d.StorageBytesMax,
		&d.UserCount, &d.ExtraSeats, &d.LockedPriceCents, &d.LockedBillingPeriod,
		&d.SubscriptionStatus, &d.AutoRenew, &periodEnd, &d.ScheduledPlanCode,
		&lapsed, &purge, &d.StripeCustomerID, &d.OwnerEmail, &d.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("directory: get tenant: %w", err)
	}
	d.CurrentPeriodEnd = ptr(periodEnd)
	d.LapsedAt = ptr(lapsed)
	d.PurgeAt = ptr(purge)

	members, err := s.tenantMembers(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	d.Members = members
	workspaces, err := s.tenantWorkspaces(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	d.Workspaces = workspaces
	return &d, nil
}

func (s *Service) tenantMembers(ctx context.Context, tenantID int64) ([]TenantMember, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT u.id, u.email, `+nameExpr+` AS name, tm.role, tm.status,
		       u.last_login_at, tm.joined_at
		  FROM tenant_members tm
		  JOIN users u ON u.id = tm.user_id
		 WHERE tm.tenant_id = $1
		 ORDER BY (tm.role = 'owner') DESC, tm.joined_at NULLS LAST`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("directory: tenant members: %w", err)
	}
	defer rows.Close()
	var out []TenantMember
	for rows.Next() {
		var m TenantMember
		var last, joined sql.NullTime
		if err := rows.Scan(&m.UserID, &m.Email, &m.Name, &m.Role, &m.Status, &last, &joined); err != nil {
			return nil, fmt.Errorf("directory: scan member: %w", err)
		}
		m.LastLoginAt = ptr(last)
		m.JoinedAt = ptr(joined)
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Service) tenantWorkspaces(ctx context.Context, tenantID int64) ([]WorkspaceRow, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, COALESCE(status, ''),
		       COALESCE(disk_allocation_bytes, 0), COALESCE(storage_bytes_used, 0)
		  FROM workspaces
		 WHERE tenant_id = $1
		 ORDER BY created_at`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("directory: tenant workspaces: %w", err)
	}
	defer rows.Close()
	var out []WorkspaceRow
	for rows.Next() {
		var w WorkspaceRow
		if err := rows.Scan(&w.ID, &w.Name, &w.Status, &w.DiskAllocationBytes, &w.StorageBytesUsed); err != nil {
			return nil, fmt.Errorf("directory: scan workspace: %w", err)
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// ConsentChange is one entry in the notification-consent ledger
// (notification_pref_changes) — a write-only proof-of-consent snapshot with the
// time, IP, and geolocation it was captured at (SPEC §6.8, CAN-SPAM/GDPR).
type ConsentChange struct {
	ID        int64  `json:"id"`
	Prefs     string `json:"prefs"` // raw JSON snapshot of the prefs at that moment
	IP        string `json:"ip"`
	Country   string `json:"country"`
	Region    string `json:"region"`
	City      string `json:"city"`
	CreatedAt string `json:"created_at"`
}

// ListConsentChanges returns a customer's notification-consent ledger
// (keyed by their identity user id), newest first.
func (s *Service) ListConsentChanges(ctx context.Context, userID int64) ([]ConsentChange, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, prefs::text, COALESCE(host(ip),''), COALESCE(country,''),
		       COALESCE(region,''), COALESCE(city,''),
		       to_char(created_at, 'YYYY-MM-DD"T"HH24:MI:SSOF')
		  FROM notification_pref_changes
		 WHERE user_id = $1
		 ORDER BY created_at DESC, id DESC
		 LIMIT 200`, userID)
	if err != nil {
		return nil, fmt.Errorf("directory: list consent changes: %w", err)
	}
	defer rows.Close()
	out := []ConsentChange{}
	for rows.Next() {
		var c ConsentChange
		if err := rows.Scan(&c.ID, &c.Prefs, &c.IP, &c.Country, &c.Region, &c.City, &c.CreatedAt); err != nil {
			return nil, fmt.Errorf("directory: scan consent change: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
