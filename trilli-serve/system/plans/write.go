package plans

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrNotFound is returned when a plan id doesn't exist.
var ErrNotFound = errors.New("plans: not found")

// ErrDuplicateCode is returned when creating a plan with an existing code.
var ErrDuplicateCode = errors.New("plans: code already exists")

// Input carries the editable fields of a plan. Pointer fields are nullable
// allowances (nil = Unlimited). Used by both Create and Update.
type Input struct {
	Code                  string   `json:"code"`
	Name                  string   `json:"name"`
	Tagline               string   `json:"tagline"`
	Callout               string   `json:"callout"`
	IconKey               string   `json:"icon_key"`
	IsPopular             bool     `json:"is_popular"`
	SortOrder             int      `json:"sort_order"`
	PriceMonthlyCents     int      `json:"price_monthly_cents"`
	AnnualDiscountPct     int      `json:"annual_discount_pct"`
	PerSeatCents          *int64   `json:"per_seat_cents"`
	MinSeats              int      `json:"min_seats"`
	MaxUsers              *int64   `json:"max_users"`
	MaxStorageBytes       *int64   `json:"max_storage_bytes"`
	MaxTransferBytesMonth *int64   `json:"max_transfer_bytes_month"`
	MaxWorkspaces         *int64   `json:"max_workspaces"`
	MaxFileSizeBytes      *int64   `json:"max_file_size_bytes"`
	MaxShareExpiryDays    *int64   `json:"max_share_expiry_days"`
	TrashRetentionDays    *int64   `json:"trash_retention_days"`
	APIAccess             bool     `json:"api_access"`
	SupportLevel          int      `json:"support_level"`
	MarketingLines        []string `json:"marketing_lines"`
	// Availability / inventory (SPEC §6.3). nil = no limit on that dimension.
	MaxSubscriptions *int64     `json:"max_subscriptions"`
	AvailableFrom    *time.Time `json:"available_from"`
	AvailableUntil   *time.Time `json:"available_until"`
}

// validStatuses are the lifecycle states a plan may occupy (SPEC §5.4.3).
var validStatuses = map[string]bool{"draft": true, "available": true, "retired": true, "archived": true}

func (in Input) normalize() (Input, error) {
	in.Code = strings.TrimSpace(strings.ToLower(in.Code))
	in.Name = strings.TrimSpace(in.Name)
	if in.Code == "" || in.Name == "" {
		return in, fmt.Errorf("plans: code and name are required")
	}
	if in.MinSeats < 0 || in.PriceMonthlyCents < 0 || in.AnnualDiscountPct < 0 || in.AnnualDiscountPct > 100 {
		return in, fmt.Errorf("plans: invalid numeric value")
	}
	if in.MarketingLines == nil {
		in.MarketingLines = []string{}
	}
	return in, nil
}

func isUniqueViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "plans_code_key")
}

// Create inserts a new plan in 'draft' status and returns its id.
func (s *Service) Create(ctx context.Context, in Input) (int64, error) {
	in, err := in.normalize()
	if err != nil {
		return 0, err
	}
	marketing, _ := json.Marshal(in.MarketingLines)
	var id int64
	err = s.client.QueryRowContext(ctx, `
		INSERT INTO plans (
			code, name, tagline, callout, icon_key, is_popular, sort_order,
			price_monthly_cents, annual_discount_pct, per_seat_cents, min_seats, max_users,
			max_storage_bytes, max_transfer_bytes_month, max_workspaces,
			max_file_size_bytes, max_share_expiry_days, trash_retention_days,
			api_access, support_level, marketing_lines,
			max_subscriptions, available_from, available_until, status, is_active)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,'draft',true)
		RETURNING id`,
		in.Code, in.Name, in.Tagline, in.Callout, in.IconKey, in.IsPopular, in.SortOrder,
		in.PriceMonthlyCents, in.AnnualDiscountPct, in.PerSeatCents, in.MinSeats, in.MaxUsers,
		in.MaxStorageBytes, in.MaxTransferBytesMonth, in.MaxWorkspaces,
		in.MaxFileSizeBytes, in.MaxShareExpiryDays, in.TrashRetentionDays,
		in.APIAccess, in.SupportLevel, marketing,
		in.MaxSubscriptions, in.AvailableFrom, in.AvailableUntil,
	).Scan(&id)
	if isUniqueViolation(err) {
		return 0, ErrDuplicateCode
	}
	if err != nil {
		return 0, fmt.Errorf("plans: create: %w", err)
	}
	return id, nil
}

// Update edits a plan's editable fields (status is changed via SetStatus; code
// is immutable once created — Stripe price-lock and tenant references key on it).
func (s *Service) Update(ctx context.Context, id int64, in Input) error {
	in, err := in.normalize()
	if err != nil {
		return err
	}
	marketing, _ := json.Marshal(in.MarketingLines)
	res, err := s.client.ExecContext(ctx, `
		UPDATE plans SET
			name=$2, tagline=$3, callout=$4, icon_key=$5, is_popular=$6, sort_order=$7,
			price_monthly_cents=$8, annual_discount_pct=$9, per_seat_cents=$10, min_seats=$11,
			max_users=$12, max_storage_bytes=$13, max_transfer_bytes_month=$14, max_workspaces=$15,
			max_file_size_bytes=$16, max_share_expiry_days=$17, trash_retention_days=$18,
			api_access=$19, support_level=$20, marketing_lines=$21,
			max_subscriptions=$22, available_from=$23, available_until=$24, updated_at=NOW()
		WHERE id=$1`,
		id, in.Name, in.Tagline, in.Callout, in.IconKey, in.IsPopular, in.SortOrder,
		in.PriceMonthlyCents, in.AnnualDiscountPct, in.PerSeatCents, in.MinSeats,
		in.MaxUsers, in.MaxStorageBytes, in.MaxTransferBytesMonth, in.MaxWorkspaces,
		in.MaxFileSizeBytes, in.MaxShareExpiryDays, in.TrashRetentionDays,
		in.APIAccess, in.SupportLevel, marketing,
		in.MaxSubscriptions, in.AvailableFrom, in.AvailableUntil,
	)
	if err != nil {
		return fmt.Errorf("plans: update: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// SetStatus transitions a plan's lifecycle status (draft/available/retired/
// archived). is_active mirrors 'available' for legacy consumers.
func (s *Service) SetStatus(ctx context.Context, id int64, status string) error {
	status = strings.TrimSpace(strings.ToLower(status))
	if !validStatuses[status] {
		return fmt.Errorf("plans: invalid status %q", status)
	}
	res, err := s.client.ExecContext(ctx,
		`UPDATE plans SET status=$2, is_active=($2='available'), updated_at=NOW() WHERE id=$1`, id, status)
	if err != nil {
		return fmt.Errorf("plans: set status: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// CountActiveAccounts returns how many non-deleted tenants ride on a plan —
// used as a guard before retire/archive (don't strand subscribers silently).
func (s *Service) CountActiveAccounts(ctx context.Context, id int64) (int, error) {
	var n int
	err := s.client.QueryRowContext(ctx,
		`SELECT count(*) FROM tenants WHERE plan_id=$1 AND status <> 'deleted'`, id).Scan(&n)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, nil
		}
		return 0, fmt.Errorf("plans: count accounts: %w", err)
	}
	return n, nil
}
