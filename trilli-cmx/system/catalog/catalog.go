// Package catalog provides CMX's read views over the app's plan catalog
// (SPEC §6.3 / §5.4.3). v1 surfaces Plans only (Add-ons / Products are future
// sub-areas). Reads hit the shared TRILLI `plans` table directly (Option C);
// plan WRITES (create/edit/retire, availability, Stripe sync) come in a later
// slice via the app's admin surface.
package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"trilli-cmx/system/database/postgres"
)

const packageName = "catalog"

// ErrNotFound is returned when a plan does not exist.
var ErrNotFound = errors.New("catalog: plan not found")

// Service runs plan read queries.
type Service struct {
	db *postgres.Client
}

// NewService constructs the catalog Service.
func NewService(db *postgres.Client) *Service { return &Service{db: db} }

// Plan is the operator view of a catalog plan, with its subscriber footprint.
type Plan struct {
	ID                    int64    `json:"id"`
	Code                  string   `json:"code"`
	Name                  string   `json:"name"`
	Status                string   `json:"status"` // draft | available | retired | archived
	Tagline               string   `json:"tagline"`
	Callout               string   `json:"callout"`
	IsPopular             bool     `json:"is_popular"`
	SortOrder             int      `json:"sort_order"`
	PriceMonthlyCents     int      `json:"price_monthly_cents"`
	AnnualDiscountPct     int      `json:"annual_discount_pct"`
	PerSeatCents          *int64   `json:"per_seat_cents,omitempty"`
	MinSeats              int      `json:"min_seats"`
	MaxUsers              *int64   `json:"max_users,omitempty"`
	MaxStorageBytes       *int64   `json:"max_storage_bytes,omitempty"`
	MaxTransferBytesMonth *int64   `json:"max_transfer_bytes_month,omitempty"`
	MaxWorkspaces         *int64   `json:"max_workspaces,omitempty"`
	MaxFileSizeBytes      *int64   `json:"max_file_size_bytes,omitempty"`
	MaxShareExpiryDays    *int64   `json:"max_share_expiry_days,omitempty"`
	TrashRetentionDays    *int64   `json:"trash_retention_days,omitempty"`
	APIAccess             bool     `json:"api_access"`
	SupportLevel          int      `json:"support_level"`
	MarketingLines        []string `json:"marketing_lines"`
	StripeProductID       string   `json:"stripe_product_id"`
	StripePriceMonthlyID  string   `json:"stripe_price_monthly_id"`
	StripePriceAnnualID   string   `json:"stripe_price_annual_id"`
	AccountCount          int      `json:"account_count"`
	MemberCount           int      `json:"member_count"`
	// Availability / inventory (SPEC §6.3). nil = no limit on that dimension.
	MaxSubscriptions *int64  `json:"max_subscriptions,omitempty"`
	AvailableFrom    *string `json:"available_from,omitempty"`
	AvailableUntil   *string `json:"available_until,omitempty"`
	// Remaining is max_subscriptions - account_count when a cap is set (else nil).
	Remaining *int64 `json:"remaining,omitempty"`
}

const selectCols = `
	p.id, p.code, p.name, p.status, COALESCE(p.tagline,''), COALESCE(p.callout,''),
	p.is_popular, p.sort_order, COALESCE(p.price_monthly_cents,0), COALESCE(p.annual_discount_pct,0),
	p.per_seat_cents, COALESCE(p.min_seats,0), p.max_users,
	p.max_storage_bytes, p.max_transfer_bytes_month, p.max_workspaces,
	p.max_file_size_bytes, p.max_share_expiry_days, p.trash_retention_days,
	COALESCE(p.api_access,false), COALESCE(p.support_level,0), p.marketing_lines,
	COALESCE(p.stripe_product_id,''), COALESCE(p.stripe_price_monthly_id,''),
	COALESCE(p.stripe_price_annual_id,''),
	p.max_subscriptions,
	to_char(p.available_from, 'YYYY-MM-DD"T"HH24:MI:SSOF'),
	to_char(p.available_until, 'YYYY-MM-DD"T"HH24:MI:SSOF')`

func scanPlan(sc interface{ Scan(...any) error }, p *Plan) error {
	var perSeat, maxUsers, storage, transfer, workspaces, fileSize, shareExpiry, trashDays sql.NullInt64
	var maxSubs sql.NullInt64
	var availFrom, availUntil sql.NullString
	var marketing []byte
	if err := sc.Scan(
		&p.ID, &p.Code, &p.Name, &p.Status, &p.Tagline, &p.Callout,
		&p.IsPopular, &p.SortOrder, &p.PriceMonthlyCents, &p.AnnualDiscountPct,
		&perSeat, &p.MinSeats, &maxUsers,
		&storage, &transfer, &workspaces,
		&fileSize, &shareExpiry, &trashDays,
		&p.APIAccess, &p.SupportLevel, &marketing,
		&p.StripeProductID, &p.StripePriceMonthlyID, &p.StripePriceAnnualID,
		&maxSubs, &availFrom, &availUntil,
		&p.AccountCount, &p.MemberCount,
	); err != nil {
		return err
	}
	p.PerSeatCents = ptr(perSeat)
	p.MaxUsers = ptr(maxUsers)
	p.MaxStorageBytes = ptr(storage)
	p.MaxTransferBytesMonth = ptr(transfer)
	p.MaxWorkspaces = ptr(workspaces)
	p.MaxFileSizeBytes = ptr(fileSize)
	p.MaxShareExpiryDays = ptr(shareExpiry)
	p.TrashRetentionDays = ptr(trashDays)
	p.MaxSubscriptions = ptr(maxSubs)
	if availFrom.Valid {
		p.AvailableFrom = &availFrom.String
	}
	if availUntil.Valid {
		p.AvailableUntil = &availUntil.String
	}
	if maxSubs.Valid {
		rem := maxSubs.Int64 - int64(p.AccountCount)
		if rem < 0 {
			rem = 0
		}
		p.Remaining = &rem
	}
	p.MarketingLines = []string{}
	if len(marketing) > 0 {
		_ = json.Unmarshal(marketing, &p.MarketingLines)
	}
	return nil
}

func ptr(n sql.NullInt64) *int64 {
	if !n.Valid {
		return nil
	}
	v := n.Int64
	return &v
}

// countsJoin appends the subscriber footprint (active accounts + members) to a
// plan query — the operator counterpart of plans.ListWithCounts.
const countsJoin = `,
	       COUNT(t.id)                    AS account_count,
	       COALESCE(SUM(t.user_count), 0) AS member_count
	  FROM plans p
	  LEFT JOIN tenants t ON t.plan_id = p.id AND t.status <> 'deleted'`

// ListPlans returns every non-archived plan with subscriber counts.
func (s *Service) ListPlans(ctx context.Context) ([]Plan, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+selectCols+countsJoin+`
		 WHERE p.status <> 'archived'
		 GROUP BY p.id
		 ORDER BY p.sort_order ASC, p.id ASC`)
	if err != nil {
		return nil, fmt.Errorf("catalog: list plans: %w", err)
	}
	defer rows.Close()
	var out []Plan
	for rows.Next() {
		var p Plan
		if err := scanPlan(rows, &p); err != nil {
			return nil, fmt.Errorf("catalog: scan plan: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// GetPlan returns one plan (any status) with subscriber counts.
func (s *Service) GetPlan(ctx context.Context, id int64) (*Plan, error) {
	var p Plan
	err := scanPlan(s.db.QueryRowContext(ctx,
		`SELECT `+selectCols+countsJoin+`
		 WHERE p.id = $1
		 GROUP BY p.id`, id), &p)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("catalog: get plan: %w", err)
	}
	return &p, nil
}
