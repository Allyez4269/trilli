// Package directory provides CMX's read-only views over the app's customer and
// tenant data (SPEC §6.1 Customers, §6.2 Accounts). Per the Option C hybrid
// model (SPEC §7), reads hit the shared TRILLI database directly; the only
// writes here are operator notes, which live in the CMX-owned cmx_customer_notes
// table (no app invariant involved).
//
// A "Customer" is an owning identity (a row in users that owns >=1 tenant). The
// owned-Accounts roll-up is DERIVED from tenant ownership (tenant_members.role
// = 'owner'), never duplicated, so it cannot drift. cmx_customers is a thin CRM
// overlay (lifecycle stage, company) keyed by identity_user_id, created lazily.
package directory

import "time"

const packageName = "directory"

// CustomerListItem is one row in the Customers directory (SPEC §6.1).
type CustomerListItem struct {
	IdentityUserID int64      `json:"identity_user_id"`
	Email          string     `json:"email"`
	Name           string     `json:"name"`
	Company        string     `json:"company"`
	LifecycleStage string     `json:"lifecycle_stage"`
	AccountCount   int        `json:"account_count"`
	Status         string     `json:"status"`
	CreatedAt      time.Time  `json:"created_at"`
	LastLoginAt    *time.Time `json:"last_login_at,omitempty"`
}

// CustomerDetail is the full customer record with its owned-accounts roll-up,
// memberships (identity→tenants map), CRM overlay, and operator notes.
type CustomerDetail struct {
	IdentityUserID int64           `json:"identity_user_id"`
	Email          string          `json:"email"`
	Name           string          `json:"name"`
	Company        string          `json:"company"`
	LifecycleStage string          `json:"lifecycle_stage"`
	Status         string          `json:"status"`
	EmailVerified  bool            `json:"email_verified"`
	CreatedAt      time.Time       `json:"created_at"`
	LastLoginAt    *time.Time      `json:"last_login_at,omitempty"`
	OwnedAccounts  []AccountBrief  `json:"owned_accounts"`  // tenants this identity owns
	Memberships    []AccountBrief  `json:"memberships"`     // tenants joined but not owned
	Notes          []CustomerNote  `json:"notes"`
}

// AccountBrief is a compact tenant reference used in roll-ups and the
// identity→tenants map.
type AccountBrief struct {
	TenantID       int64  `json:"tenant_id"`
	Name           string `json:"name"`
	Role           string `json:"role"`
	MemberStatus   string `json:"member_status"`
	PlanCode       string `json:"plan_code"`
	TenantStatus   string `json:"tenant_status"`
	LifecycleState string `json:"lifecycle_state"`
}

// CustomerNote is a freeform operator note (cmx_customer_notes).
type CustomerNote struct {
	ID            int64     `json:"id"`
	OperatorID    int64     `json:"operator_id"`
	OperatorEmail string    `json:"operator_email"`
	Body          string    `json:"body"`
	CreatedAt     time.Time `json:"created_at"`
}

// TenantListItem is one row in the Accounts directory (SPEC §6.2).
type TenantListItem struct {
	ID                 int64     `json:"id"`
	Name               string    `json:"name"`
	Slug               string    `json:"slug"`
	Status             string    `json:"status"`
	LifecycleState     string    `json:"lifecycle_state"`
	PlanCode           string    `json:"plan_code"`
	PlanName           string    `json:"plan_name"`
	StorageBytesUsed   int64     `json:"storage_bytes_used"`
	StorageBytesMax    int64     `json:"storage_bytes_max"` // 0 = unknown/unlimited
	UserCount          int       `json:"user_count"`
	ExtraSeats         int       `json:"extra_seats"`
	SubscriptionStatus string    `json:"subscription_status"`
	OwnerEmail         string    `json:"owner_email"`
	CreatedAt          time.Time `json:"created_at"`
}

// TenantDetail is the full tenant view (SPEC §6.2): plan, storage, billing
// snapshot, lifecycle, members, and workspaces.
type TenantDetail struct {
	ID                 int64           `json:"id"`
	Name               string          `json:"name"`
	Slug               string          `json:"slug"`
	Status             string          `json:"status"`
	LifecycleState     string          `json:"lifecycle_state"`
	PlanCode           string          `json:"plan_code"`
	PlanName           string          `json:"plan_name"`
	StorageBytesUsed   int64           `json:"storage_bytes_used"`
	StorageBytesMax    int64           `json:"storage_bytes_max"`
	UserCount          int             `json:"user_count"`
	ExtraSeats         int             `json:"extra_seats"`
	LockedPriceCents   int             `json:"locked_price_cents"`
	LockedBillingPeriod string         `json:"locked_billing_period"`
	SubscriptionStatus string          `json:"subscription_status"`
	AutoRenew          bool            `json:"auto_renew"`
	CurrentPeriodEnd   *time.Time      `json:"current_period_end,omitempty"`
	ScheduledPlanCode  string          `json:"scheduled_plan_code"`
	LapsedAt           *time.Time      `json:"lapsed_at,omitempty"`
	PurgeAt            *time.Time      `json:"purge_at,omitempty"`
	StripeCustomerID   string          `json:"stripe_customer_id"`
	OwnerEmail         string          `json:"owner_email"`
	CreatedAt          time.Time       `json:"created_at"`
	Members            []TenantMember  `json:"members"`
	Workspaces         []WorkspaceRow  `json:"workspaces"`
}

// TenantMember is one roster row in the tenant detail.
type TenantMember struct {
	UserID      int64      `json:"user_id"`
	Email       string     `json:"email"`
	Name        string     `json:"name"`
	Role        string     `json:"role"`
	Status      string     `json:"status"`
	LastLoginAt *time.Time `json:"last_login_at,omitempty"`
	JoinedAt    *time.Time `json:"joined_at,omitempty"`
}

// WorkspaceRow is one workspace in the tenant detail, with allocation vs usage.
type WorkspaceRow struct {
	ID                  int64  `json:"id"`
	Name                string `json:"name"`
	Status              string `json:"status"`
	DiskAllocationBytes int64  `json:"disk_allocation_bytes"`
	StorageBytesUsed    int64  `json:"storage_bytes_used"`
}
