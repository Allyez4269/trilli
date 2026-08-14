// Package reports powers CMX's Reports & Marketing section (SPEC §6.6): business
// reporting (MRR/ARR, plan mix, lifecycle funnel, signup conversion) and a
// first-cut unit-economics view (recurring revenue vs estimated Azure storage
// cost). All read directly from the shared DB (Option C); email/segment tooling
// is deferred (platform email sending is unbuilt). Global-only.
package reports

import (
	"context"
	"fmt"

	"trilli-cmx/system/database/postgres"
)

const packageName = "reports"

// Azure block-blob $/GB-month by tier (mirrors system/storage/tiering) for the
// storage-cost side of unit economics.
const (
	hotPerGBMonth  = 0.0184
	coolPerGBMonth = 0.0100
	coldPerGBMonth = 0.0036
	bytesPerGB     = 1024.0 * 1024.0 * 1024.0
)

// effMonthly is the per-tenant monthly revenue expression in cents.
const effMonthly = `(COALESCE(t.locked_price_cents, p.price_monthly_cents, 0) + t.extra_seats * 500)`

// Service runs reporting queries.
type Service struct {
	db *postgres.Client
}

// NewService constructs the reports Service.
func NewService(db *postgres.Client) *Service { return &Service{db: db} }

// PlanMix is the subscriber + revenue footprint of one plan.
type PlanMix struct {
	Code        string `json:"code"`
	Name        string `json:"name"`
	Subscribers int    `json:"subscribers"`
	MRRCents    int64  `json:"mrr_cents"`
}

// Report is the full Reports payload.
type Report struct {
	// Revenue.
	MRRCents       int64     `json:"mrr_cents"`
	ARRCents       int64     `json:"arr_cents"`
	ARPUCents      int64     `json:"arpu_cents"` // MRR / paying accounts
	PayingAccounts int       `json:"paying_accounts"`
	Trialing       int       `json:"trialing"`
	Comp           int       `json:"comp"`
	PlanMix        []PlanMix `json:"plan_mix"`

	// Lifecycle / funnel.
	Leads       int `json:"leads"`        // signup intents in flight (not paid/completed)
	IntentsPaid int `json:"intents_paid"` // paid but not completed (stuck)
	Churned     int `json:"churned"`      // lapsed / closing tenants
	ActiveTotal int `json:"active_total"` // non-deleted tenants

	// Conversion: completed intents / (completed + cancelled + expired terminal).
	IntentsCompleted int     `json:"intents_completed"`
	IntentsTerminal  int     `json:"intents_terminal"`
	ConversionPct    float64 `json:"conversion_pct"`

	// Unit economics (monthly, USD).
	StorageCostUSD float64 `json:"storage_cost_usd"`
	GrossMarginUSD float64 `json:"gross_margin_usd"` // MRR - storage cost
}

// Build computes the full report.
func (s *Service) Build(ctx context.Context) (*Report, error) {
	var r Report

	// Revenue totals.
	if err := s.db.QueryRowContext(ctx, `
		SELECT
		  COALESCE(SUM(`+effMonthly+`) FILTER (WHERE t.billing_mode <> 'comp' AND t.subscription_status IN ('active','trialing')),0),
		  COUNT(*) FILTER (WHERE t.billing_mode <> 'comp' AND t.subscription_status = 'active'),
		  COUNT(*) FILTER (WHERE t.subscription_status = 'trialing'),
		  COUNT(*) FILTER (WHERE t.billing_mode = 'comp' AND t.status <> 'deleted'),
		  COUNT(*) FILTER (WHERE t.status <> 'deleted'),
		  COUNT(*) FILTER (WHERE t.lifecycle_state IN ('lapsed','closing'))
		FROM tenants t JOIN plans p ON p.id = t.plan_id`).Scan(
		&r.MRRCents, &r.PayingAccounts, &r.Trialing, &r.Comp, &r.ActiveTotal, &r.Churned); err != nil {
		return nil, fmt.Errorf("reports: revenue totals: %w", err)
	}
	r.ARRCents = r.MRRCents * 12
	if r.PayingAccounts > 0 {
		r.ARPUCents = r.MRRCents / int64(r.PayingAccounts)
	}

	// Plan mix.
	rows, err := s.db.QueryContext(ctx, `
		SELECT p.code, p.name,
		       COUNT(t.id) FILTER (WHERE t.status <> 'deleted'),
		       COALESCE(SUM(`+effMonthly+`) FILTER (WHERE t.billing_mode <> 'comp' AND t.subscription_status IN ('active','trialing')),0)
		  FROM plans p LEFT JOIN tenants t ON t.plan_id = p.id
		 GROUP BY p.id, p.code, p.name
		HAVING COUNT(t.id) FILTER (WHERE t.status <> 'deleted') > 0
		 ORDER BY 3 DESC`)
	if err != nil {
		return nil, fmt.Errorf("reports: plan mix: %w", err)
	}
	defer rows.Close()
	r.PlanMix = []PlanMix{}
	for rows.Next() {
		var m PlanMix
		if err := rows.Scan(&m.Code, &m.Name, &m.Subscribers, &m.MRRCents); err != nil {
			return nil, fmt.Errorf("reports: scan plan mix: %w", err)
		}
		r.PlanMix = append(r.PlanMix, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Signup funnel + conversion.
	if err := s.db.QueryRowContext(ctx, `
		SELECT
		  COUNT(*) FILTER (WHERE status IN ('pending_email','email_verified')),
		  COUNT(*) FILTER (WHERE status = 'paid'),
		  COUNT(*) FILTER (WHERE status = 'completed'),
		  COUNT(*) FILTER (WHERE status IN ('completed','cancelled','expired'))
		FROM signup_intents`).Scan(&r.Leads, &r.IntentsPaid, &r.IntentsCompleted, &r.IntentsTerminal); err != nil {
		return nil, fmt.Errorf("reports: funnel: %w", err)
	}
	if r.IntentsTerminal > 0 {
		r.ConversionPct = float64(r.IntentsCompleted) / float64(r.IntentsTerminal) * 100
	}

	// Unit economics: estimated monthly storage cost from tier byte distribution.
	var hot, cool, cold int64
	if err := s.db.QueryRowContext(ctx, `
		SELECT
		  COALESCE(SUM(size_bytes) FILTER (WHERE access_tier='hot'),0),
		  COALESCE(SUM(size_bytes) FILTER (WHERE access_tier='cool'),0),
		  COALESCE(SUM(size_bytes) FILTER (WHERE access_tier='cold'),0)
		FROM files WHERE status='active'`).Scan(&hot, &cool, &cold); err != nil {
		return nil, fmt.Errorf("reports: storage: %w", err)
	}
	r.StorageCostUSD = float64(hot)/bytesPerGB*hotPerGBMonth +
		float64(cool)/bytesPerGB*coolPerGBMonth +
		float64(cold)/bytesPerGB*coldPerGBMonth
	r.GrossMarginUSD = float64(r.MRRCents)/100.0 - r.StorageCostUSD
	return &r, nil
}
