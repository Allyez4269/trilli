// Package revenue provides CMX's read views over the money-IN side of the
// platform (SPEC §6.4): a subscription console, the invoices/orders ledger, a
// past-due / dunning list, and signup-intent operations. All reads hit the
// shared TRILLI tables directly (Option C); the money-moving WRITES — refunds,
// the account-credit ledger, change/cancel/reconcile a subscription — are a
// later slice that needs new app-side admin endpoints.
//
// Billing facts this package leans on:
//   - tenants.locked_price_cents is a MONTHLY figure regardless of billing
//     period, so MRR is COALESCE(locked_price_cents, plan.price_monthly_cents).
//   - extra seats are a flat $5/seat/mo ($5 = 500 cents) on every plan.
//   - billing_transactions records refunds as a NEGATIVE amount_cents row with
//     status='refunded'; succeeded charges are positive with status='succeeded'.
//   - comp/ambassador tenants (billing_mode='comp') pay nothing — excluded from
//     MRR but still surfaced in the subscription console.
package revenue

import (
	"context"
	"database/sql"
	"fmt"

	"trilli-cmx/system/database/postgres"
)

const packageName = "revenue"

// extraSeatCents is the flat per-seat add-on price ($5/mo) used for MRR.
const extraSeatCents = 500

// Service runs revenue read queries.
type Service struct {
	db *postgres.Client
}

// NewService constructs the revenue Service.
func NewService(db *postgres.Client) *Service { return &Service{db: db} }

// Overview is the Revenue landing summary (SPEC §5.4 Main → Revenue tile).
type Overview struct {
	MRRCents              int64 `json:"mrr_cents"`
	ActiveSubscriptions   int   `json:"active_subscriptions"`
	TrialingSubscriptions int   `json:"trialing_subscriptions"`
	CompAccounts          int   `json:"comp_accounts"`
	PastDueCount          int   `json:"past_due_count"`
	PendingIntents        int   `json:"pending_intents"`
	Collected30dCents     int64 `json:"collected_30d_cents"`
	Refunded30dCents      int64 `json:"refunded_30d_cents"`
	NetCollectedCents     int64 `json:"net_collected_cents"`
}

// effectiveMonthly is the SQL expression for a tenant's monthly revenue in
// cents: locked monthly price (falling back to the plan's list monthly price)
// plus flat per-seat add-ons.
const effectiveMonthly = `(COALESCE(t.locked_price_cents, p.price_monthly_cents, 0) + t.extra_seats * 500)`

// Overview computes the revenue summary in a single round trip per metric.
func (s *Service) Overview(ctx context.Context) (*Overview, error) {
	var o Overview
	err := s.db.QueryRowContext(ctx, `
		SELECT
		  COALESCE(SUM(`+effectiveMonthly+`) FILTER (
		    WHERE t.billing_mode <> 'comp'
		      AND t.subscription_status IN ('active','trialing')), 0)            AS mrr_cents,
		  COUNT(*) FILTER (WHERE t.subscription_status = 'active')                AS active_subs,
		  COUNT(*) FILTER (WHERE t.subscription_status = 'trialing')              AS trialing_subs,
		  COUNT(*) FILTER (WHERE t.billing_mode = 'comp'
		                     AND t.status <> 'deleted')                          AS comp_accounts,
		  COUNT(*) FILTER (WHERE t.subscription_status IN ('past_due','unpaid')
		                     OR t.lifecycle_state IN ('lapsed','closing'))       AS past_due
		FROM tenants t
		JOIN plans p ON p.id = t.plan_id`).Scan(
		&o.MRRCents, &o.ActiveSubscriptions, &o.TrialingSubscriptions, &o.CompAccounts, &o.PastDueCount)
	if err != nil {
		return nil, fmt.Errorf("revenue: overview tenants: %w", err)
	}

	if err := s.db.QueryRowContext(ctx, `
		SELECT
		  COALESCE(SUM(amount_cents) FILTER (WHERE status = 'succeeded' AND created_at >= now() - interval '30 days'), 0),
		  COALESCE(-SUM(amount_cents) FILTER (WHERE status = 'refunded' AND created_at >= now() - interval '30 days'), 0),
		  COALESCE(SUM(amount_cents), 0)
		FROM billing_transactions`).Scan(
		&o.Collected30dCents, &o.Refunded30dCents, &o.NetCollectedCents); err != nil {
		return nil, fmt.Errorf("revenue: overview transactions: %w", err)
	}

	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM signup_intents
		WHERE status IN ('pending_email','email_verified','paid')`).Scan(&o.PendingIntents); err != nil {
		return nil, fmt.Errorf("revenue: overview intents: %w", err)
	}
	return &o, nil
}

// Subscription is one row in the subscription console (SPEC §6.4).
type Subscription struct {
	TenantID           int64   `json:"tenant_id"`
	Name               string  `json:"name"`
	OwnerEmail         string  `json:"owner_email"`
	PlanCode           string  `json:"plan_code"`
	PlanName           string  `json:"plan_name"`
	BillingMode        string  `json:"billing_mode"`
	SubscriptionStatus string  `json:"subscription_status"`
	LifecycleState     string  `json:"lifecycle_state"`
	BillingPeriod      string  `json:"billing_period"`
	MonthlyCents       int64   `json:"monthly_cents"`
	ExtraSeats         int     `json:"extra_seats"`
	AutoRenew          bool    `json:"auto_renew"`
	HasCardOnFile      bool    `json:"has_card_on_file"`
	CurrentPeriodEnd   *string `json:"current_period_end,omitempty"`
	CompExpiresAt      *string `json:"comp_expires_at,omitempty"`
	StripeSubID        string  `json:"stripe_subscription_id"`
}

// ownerJoin resolves a tenant's owning identity email via tenant_members.
const ownerJoin = `
	LEFT JOIN LATERAL (
	  SELECT u.email
	  FROM tenant_members m JOIN users u ON u.id = m.user_id
	  WHERE m.tenant_id = t.id AND m.role = 'owner'
	  ORDER BY m.user_id ASC LIMIT 1
	) owner ON true`

// ListSubscriptions returns the subscription console for all non-deleted
// tenants, newest first. It is read-only; per-account actions live on the
// account detail page (suspend/quota) and the future money-write slice.
func (s *Service) ListSubscriptions(ctx context.Context) ([]Subscription, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT t.id, t.name, COALESCE(owner.email,''),
		       t.plan_id, p.code, p.name,
		       t.billing_mode, t.subscription_status, t.lifecycle_state,
		       COALESCE(t.locked_billing_period,''),
		       `+effectiveMonthly+`,
		       t.extra_seats, t.auto_renew,
		       (COALESCE(t.stripe_customer_id,'') <> '') AS has_card,
		       to_char(t.current_period_end, 'YYYY-MM-DD"T"HH24:MI:SSOF'),
		       to_char(t.comp_expires_at,    'YYYY-MM-DD"T"HH24:MI:SSOF'),
		       t.stripe_subscription_id
		FROM tenants t
		JOIN plans p ON p.id = t.plan_id`+ownerJoin+`
		WHERE t.status <> 'deleted'
		ORDER BY t.created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("revenue: list subscriptions: %w", err)
	}
	defer rows.Close()
	var out []Subscription
	for rows.Next() {
		var sub Subscription
		var planID int64
		var periodEnd, compExpires sql.NullString
		if err := rows.Scan(
			&sub.TenantID, &sub.Name, &sub.OwnerEmail,
			&planID, &sub.PlanCode, &sub.PlanName,
			&sub.BillingMode, &sub.SubscriptionStatus, &sub.LifecycleState,
			&sub.BillingPeriod, &sub.MonthlyCents, &sub.ExtraSeats, &sub.AutoRenew,
			&sub.HasCardOnFile, &periodEnd, &compExpires, &sub.StripeSubID,
		); err != nil {
			return nil, fmt.Errorf("revenue: scan subscription: %w", err)
		}
		if periodEnd.Valid {
			sub.CurrentPeriodEnd = &periodEnd.String
		}
		if compExpires.Valid {
			sub.CompExpiresAt = &compExpires.String
		}
		out = append(out, sub)
	}
	return out, rows.Err()
}

// Transaction is one invoice/order row (SPEC §6.4 invoices & orders).
type Transaction struct {
	ID            int64  `json:"id"`
	TenantID      int64  `json:"tenant_id"`
	TenantName    string `json:"tenant_name"`
	PlanCode      string `json:"plan_code"`
	BillingPeriod string `json:"billing_period"`
	AmountCents   int64  `json:"amount_cents"`
	Currency      string `json:"currency"`
	Status        string `json:"status"` // succeeded | refunded
	OrderNumber   string `json:"order_number"`
	ReceiptNumber string `json:"receipt_number"`
	ReceiptURL    string `json:"receipt_url"`
	PaymentIntent string `json:"stripe_payment_intent_id"`
	CreatedAt     string `json:"created_at"`
}

// ListTransactions returns the billing_transactions ledger newest-first. When
// tenantID > 0 it is scoped to that account; limit caps the row count.
func (s *Service) ListTransactions(ctx context.Context, tenantID int64, limit int) ([]Transaction, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT bt.id, bt.tenant_id, t.name, bt.plan_code, bt.billing_period,
		       bt.amount_cents, bt.currency, bt.status,
		       COALESCE(bt.order_number,''), bt.receipt_number, bt.receipt_url,
		       bt.stripe_payment_intent_id,
		       to_char(bt.created_at, 'YYYY-MM-DD"T"HH24:MI:SSOF')
		FROM billing_transactions bt
		JOIN tenants t ON t.id = bt.tenant_id
		WHERE ($1 = 0 OR bt.tenant_id = $1)
		ORDER BY bt.created_at DESC
		LIMIT $2`, tenantID, limit)
	if err != nil {
		return nil, fmt.Errorf("revenue: list transactions: %w", err)
	}
	defer rows.Close()
	var out []Transaction
	for rows.Next() {
		var tx Transaction
		if err := rows.Scan(
			&tx.ID, &tx.TenantID, &tx.TenantName, &tx.PlanCode, &tx.BillingPeriod,
			&tx.AmountCents, &tx.Currency, &tx.Status,
			&tx.OrderNumber, &tx.ReceiptNumber, &tx.ReceiptURL,
			&tx.PaymentIntent, &tx.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("revenue: scan transaction: %w", err)
		}
		out = append(out, tx)
	}
	return out, rows.Err()
}

// PastDueAccount is one row in the dunning list (SPEC §6.4 past-due/failed).
type PastDueAccount struct {
	TenantID           int64   `json:"tenant_id"`
	Name               string  `json:"name"`
	OwnerEmail         string  `json:"owner_email"`
	PlanCode           string  `json:"plan_code"`
	SubscriptionStatus string  `json:"subscription_status"`
	LifecycleState     string  `json:"lifecycle_state"`
	MonthlyCents       int64   `json:"monthly_cents"`
	CurrentPeriodEnd   *string `json:"current_period_end,omitempty"`
	LapsedAt           *string `json:"lapsed_at,omitempty"`
	PurgeAt            *string `json:"purge_at,omitempty"`
}

// ListPastDue lists accounts needing billing attention: Stripe past_due/unpaid
// subscriptions, plus tenants in the read-only grace (lapsed) or closing states.
func (s *Service) ListPastDue(ctx context.Context) ([]PastDueAccount, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT t.id, t.name, COALESCE(owner.email,''), p.code,
		       t.subscription_status, t.lifecycle_state,
		       `+effectiveMonthly+`,
		       to_char(t.current_period_end, 'YYYY-MM-DD"T"HH24:MI:SSOF'),
		       to_char(t.lapsed_at,          'YYYY-MM-DD"T"HH24:MI:SSOF'),
		       to_char(t.purge_at,           'YYYY-MM-DD"T"HH24:MI:SSOF')
		FROM tenants t
		JOIN plans p ON p.id = t.plan_id`+ownerJoin+`
		WHERE t.status <> 'deleted'
		  AND (t.subscription_status IN ('past_due','unpaid')
		       OR t.lifecycle_state IN ('lapsed','closing'))
		ORDER BY t.purge_at ASC NULLS LAST, t.lapsed_at ASC NULLS LAST, t.id ASC`)
	if err != nil {
		return nil, fmt.Errorf("revenue: list past-due: %w", err)
	}
	defer rows.Close()
	var out []PastDueAccount
	for rows.Next() {
		var a PastDueAccount
		var periodEnd, lapsedAt, purgeAt sql.NullString
		if err := rows.Scan(
			&a.TenantID, &a.Name, &a.OwnerEmail, &a.PlanCode,
			&a.SubscriptionStatus, &a.LifecycleState, &a.MonthlyCents,
			&periodEnd, &lapsedAt, &purgeAt,
		); err != nil {
			return nil, fmt.Errorf("revenue: scan past-due: %w", err)
		}
		if periodEnd.Valid {
			a.CurrentPeriodEnd = &periodEnd.String
		}
		if lapsedAt.Valid {
			a.LapsedAt = &lapsedAt.String
		}
		if purgeAt.Valid {
			a.PurgeAt = &purgeAt.String
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// CreditGrant is one row of the account-credit ledger (read directly from the
// shared account_credits table; grants are written via the app admin surface).
type CreditGrant struct {
	ID             int64  `json:"id"`
	AmountCents    int64  `json:"amount_cents"`
	Currency       string `json:"currency"`
	Reason         string `json:"reason"`
	GrantedByEmail string `json:"granted_by_email"`
	CreatedAt      string `json:"created_at"`
}

// ListCredits returns a tenant's account-credit grants, newest first, plus the
// total granted in cents.
func (s *Service) ListCredits(ctx context.Context, tenantID int64) ([]CreditGrant, int64, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, amount_cents, currency, COALESCE(reason,''), COALESCE(granted_by_email,''),
		       to_char(created_at, 'YYYY-MM-DD"T"HH24:MI:SSOF')
		FROM account_credits WHERE tenant_id = $1 ORDER BY created_at DESC`, tenantID)
	if err != nil {
		return nil, 0, fmt.Errorf("revenue: list credits: %w", err)
	}
	defer rows.Close()
	var out []CreditGrant
	var total int64
	for rows.Next() {
		var g CreditGrant
		if err := rows.Scan(&g.ID, &g.AmountCents, &g.Currency, &g.Reason, &g.GrantedByEmail, &g.CreatedAt); err != nil {
			return nil, 0, fmt.Errorf("revenue: scan credit: %w", err)
		}
		total += g.AmountCents
		out = append(out, g)
	}
	return out, total, rows.Err()
}

// SignupIntent is one row in the signup-intent operations view (SPEC §6.4).
type SignupIntent struct {
	ID            int64   `json:"id"`
	Email         string  `json:"email"`
	AccountType   string  `json:"account_type"`
	PlanCode      string  `json:"plan_code"`
	BillingCycle  string  `json:"billing_cycle"`
	Status        string  `json:"status"`
	OAuthProvider string  `json:"oauth_provider"`
	StripeSubID   string  `json:"stripe_subscription_id"`
	CreatedAt     string  `json:"created_at"`
	ExpiresAt     string  `json:"expires_at"`
	PaidAt        *string `json:"paid_at,omitempty"`
	VerifiedAt    *string `json:"verified_at,omitempty"`
	// Stuck flags a paid intent that never completed account provisioning — the
	// case operators most need to chase (today only the resume sweep handles it).
	Stuck bool `json:"stuck"`
}

// ListSignupIntents returns in-flight signup intents (not completed/cancelled/
// expired), newest first, flagging paid-but-not-completed ones as stuck.
func (s *Service) ListSignupIntents(ctx context.Context) ([]SignupIntent, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, email, account_type, plan_code, billing_cycle, status,
		       oauth_provider, stripe_subscription_id,
		       to_char(created_at, 'YYYY-MM-DD"T"HH24:MI:SSOF'),
		       to_char(expires_at, 'YYYY-MM-DD"T"HH24:MI:SSOF'),
		       to_char(paid_at,     'YYYY-MM-DD"T"HH24:MI:SSOF'),
		       to_char(verified_at, 'YYYY-MM-DD"T"HH24:MI:SSOF'),
		       (status = 'paid') AS stuck
		FROM signup_intents
		WHERE status IN ('pending_email','email_verified','paid')
		ORDER BY (status = 'paid') DESC, created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("revenue: list signup intents: %w", err)
	}
	defer rows.Close()
	var out []SignupIntent
	for rows.Next() {
		var si SignupIntent
		var paidAt, verifiedAt sql.NullString
		if err := rows.Scan(
			&si.ID, &si.Email, &si.AccountType, &si.PlanCode, &si.BillingCycle, &si.Status,
			&si.OAuthProvider, &si.StripeSubID, &si.CreatedAt, &si.ExpiresAt,
			&paidAt, &verifiedAt, &si.Stuck,
		); err != nil {
			return nil, fmt.Errorf("revenue: scan signup intent: %w", err)
		}
		if paidAt.Valid {
			si.PaidAt = &paidAt.String
		}
		if verifiedAt.Valid {
			si.VerifiedAt = &verifiedAt.String
		}
		out = append(out, si)
	}
	return out, rows.Err()
}
