// Package plans is the read model for the admin-managed plan catalog. Each plan
// carries the per-plan allowance dials, pricing, presentation, and a lifecycle
// status; accounts (tenants) associate to one via tenants.plan_id and inherit
// its allowances live. NULL on a numeric allowance means "Unlimited".
package plans

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"trilli/system/database/postgres"
)

const packageName = "plans"

// Plan mirrors a row in `plans`. Nullable numeric allowances are pointers;
// nil = Unlimited. AccountCount/MemberCount are populated only by ListWithCounts.
type Plan struct {
	ID                    int64
	Code                  string
	Name                  string
	IconKey               string
	Callout               string
	Tagline               string
	PriceMonthlyCents     int
	AnnualDiscountPct     int
	MaxStorageBytes       *int64
	MaxTransferBytesMonth *int64
	MaxWorkspaces         *int64
	MaxUsers              *int64 // seats included with the plan (NULL = unlimited)
	MinSeats              int
	PerSeatCents          *int64 // monthly price per extra seat (NULL = seats not sold)
	MaxFileSizeBytes      *int64
	MaxShareExpiryDays    *int64
	TrashRetentionDays    *int64
	APIAccess             bool
	SupportLevel          int
	MarketingLines        []string
	IsPopular             bool
	Status                string
	SortOrder             int
	AccountCount          int
	MemberCount           int
}

type Service struct {
	client *postgres.Client
}

func NewService(c *postgres.Client) *Service {
	return &Service{client: c}
}

// selectCols is the shared column list, in scan order. Qualified with the `p`
// alias so the counts query (which joins tenants) has no ambiguous columns.
const selectCols = `
	p.id, p.code, p.name, p.icon_key, p.callout, p.tagline,
	p.price_monthly_cents, p.annual_discount_pct,
	p.max_storage_bytes, p.max_transfer_bytes_month, p.max_workspaces,
	p.max_users, p.min_seats, p.max_file_size_bytes, p.max_share_expiry_days,
	p.trash_retention_days, p.api_access, p.support_level,
	p.marketing_lines, p.is_popular, p.status, p.sort_order, p.per_seat_cents`

// scanPlan reads one row in selectCols order, plus optional trailing counts.
func scanPlan(rows *sql.Rows, withCounts bool) (Plan, error) {
	var p Plan
	var (
		storage, transfer, workspaces, users sql.NullInt64
		fileSize, shareExpiry, trashDays     sql.NullInt64
		perSeat                              sql.NullInt64
		marketing                            []byte
	)
	dest := []any{
		&p.ID, &p.Code, &p.Name, &p.IconKey, &p.Callout, &p.Tagline,
		&p.PriceMonthlyCents, &p.AnnualDiscountPct,
		&storage, &transfer, &workspaces,
		&users, &p.MinSeats, &fileSize, &shareExpiry,
		&trashDays, &p.APIAccess, &p.SupportLevel,
		&marketing, &p.IsPopular, &p.Status, &p.SortOrder, &perSeat,
	}
	if withCounts {
		dest = append(dest, &p.AccountCount, &p.MemberCount)
	}
	if err := rows.Scan(dest...); err != nil {
		return p, err
	}
	p.MaxStorageBytes = nullToPtr(storage)
	p.MaxTransferBytesMonth = nullToPtr(transfer)
	p.MaxWorkspaces = nullToPtr(workspaces)
	p.MaxUsers = nullToPtr(users)
	p.PerSeatCents = nullToPtr(perSeat)
	p.MaxFileSizeBytes = nullToPtr(fileSize)
	p.MaxShareExpiryDays = nullToPtr(shareExpiry)
	p.TrashRetentionDays = nullToPtr(trashDays)
	if len(marketing) > 0 {
		_ = json.Unmarshal(marketing, &p.MarketingLines)
	}
	return p, nil
}

func nullToPtr(n sql.NullInt64) *int64 {
	if !n.Valid {
		return nil
	}
	v := n.Int64
	return &v
}

// ListPublic returns the plans that are purchasable (status='available'),
// ordered for display. No counts.
func (s *Service) ListPublic(ctx context.Context) ([]Plan, error) {
	// Availability/inventory enforcement (SPEC §6.3): only offer plans that are
	// 'available', within their [available_from, available_until) window, and
	// not sold out (active subscribers < max_subscriptions). Sold-out/expired
	// plans are hidden entirely from the public catalog.
	rows, err := s.client.QueryContext(ctx,
		`SELECT `+selectCols+` FROM plans p
		  WHERE p.status = 'available'
		    AND (p.available_from IS NULL OR p.available_from <= NOW())
		    AND (p.available_until IS NULL OR p.available_until > NOW())
		    AND (p.max_subscriptions IS NULL OR p.max_subscriptions > (
		          SELECT count(*) FROM tenants t WHERE t.plan_id = p.id AND t.status <> 'deleted'))
		  ORDER BY p.sort_order ASC, p.id ASC`)
	if err != nil {
		return nil, fmt.Errorf("plans: list public: %w", err)
	}
	defer rows.Close()
	var out []Plan
	for rows.Next() {
		p, err := scanPlan(rows, false)
		if err != nil {
			return nil, fmt.Errorf("plans: scan: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ListWithCounts returns every non-archived plan with its dependency footprint:
// how many active accounts ride on it and the total members across them. Powers
// the admin list, the delete guard, and the reassign flow.
func (s *Service) ListWithCounts(ctx context.Context) ([]Plan, error) {
	rows, err := s.client.QueryContext(ctx, `
		SELECT `+selectCols+`,
		       COUNT(t.id)                    AS account_count,
		       COALESCE(SUM(t.user_count), 0) AS member_count
		  FROM plans p
		  LEFT JOIN tenants t
		         ON t.plan_id = p.id AND t.status <> 'deleted'
		 WHERE p.status <> 'archived'
		 GROUP BY p.id
		 ORDER BY p.sort_order ASC, p.id ASC`)
	if err != nil {
		return nil, fmt.Errorf("plans: list with counts: %w", err)
	}
	defer rows.Close()
	var out []Plan
	for rows.Next() {
		p, err := scanPlan(rows, true)
		if err != nil {
			return nil, fmt.Errorf("plans: scan: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
