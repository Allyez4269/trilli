// Package metering tracks per-account monthly transfer (bandwidth) usage — the
// enforcement subsystem behind the plans.max_transfer_bytes_month dial.
//
// "Transfer" = bytes uploaded (ingress) + bytes downloaded (egress) within the
// current calendar month (UTC). Usage is stored one row per tenant per month in
// transfer_usage; a new month is just a new row, so there's no reset job.
//
// Enforcement policy (owner decision): when an account is over its monthly
// transfer cap we block NEW UPLOADS but never downloads — the owner can always
// retrieve their data. Downloads are still metered (so the bar reflects reality
// and a future policy could tighten), they're just never refused for being over.
package metering

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"trilli/system/database/postgres"
)

const packageName = "metering"

// Service records and reports transfer usage.
type Service struct {
	client *postgres.Client
}

func NewService(c *postgres.Client) *Service {
	return &Service{client: c}
}

// Usage is the current calendar month's transfer for an account.
type Usage struct {
	UsedBytes int64  `json:"used_bytes"`           // bytes_in + bytes_out this month
	CapBytes  *int64 `json:"cap_bytes,omitempty"`  // plan cap; nil = unlimited
	Over      bool   `json:"over"`                 // used >= cap (false when unlimited)
}

// RecordIn adds uploaded bytes to the current month. Best-effort by contract:
// callers log and ignore the error so metering never breaks a transfer.
func (s *Service) RecordIn(ctx context.Context, tenantID int64, userID *int64, bytes int64) error {
	return s.record(ctx, tenantID, userID, bytes, 0)
}

// RecordOut adds downloaded bytes to the current month.
func (s *Service) RecordOut(ctx context.Context, tenantID int64, userID *int64, bytes int64) error {
	return s.record(ctx, tenantID, userID, 0, bytes)
}

// record upserts the account-wide row always, and — when userID is non-nil — a
// matching per-user row in transfer_usage_user so a member can see their own
// transfer on Home. Anonymous/programmatic transfer (public links, API keys)
// passes a nil userID and is counted at the account level only.
func (s *Service) record(ctx context.Context, tenantID int64, userID *int64, bytesIn, bytesOut int64) error {
	if tenantID == 0 || (bytesIn == 0 && bytesOut == 0) {
		return nil
	}
	// One row per DAY (period_start = current_date). The monthly cap is the
	// SUM over the calendar month (see Usage); arbitrary spans sum over their
	// date range (see RangeTransfer). Legacy month-1st rows still sum in
	// correctly. PK (tenant_id, period_start) already permits a row per day.
	if _, err := s.client.ExecContext(ctx, `
		INSERT INTO transfer_usage (tenant_id, period_start, bytes_in, bytes_out, updated_at)
		VALUES ($1, current_date, $2, $3, now())
		ON CONFLICT (tenant_id, period_start) DO UPDATE
		   SET bytes_in   = transfer_usage.bytes_in  + EXCLUDED.bytes_in,
		       bytes_out  = transfer_usage.bytes_out + EXCLUDED.bytes_out,
		       updated_at = now()`,
		tenantID, bytesIn, bytesOut); err != nil {
		return fmt.Errorf("metering: record transfer: %w", err)
	}

	if userID != nil && *userID != 0 {
		if _, err := s.client.ExecContext(ctx, `
			INSERT INTO transfer_usage_user (tenant_id, user_id, period_start, bytes_in, bytes_out, updated_at)
			VALUES ($1, $2, current_date, $3, $4, now())
			ON CONFLICT (tenant_id, user_id, period_start) DO UPDATE
			   SET bytes_in   = transfer_usage_user.bytes_in  + EXCLUDED.bytes_in,
			       bytes_out  = transfer_usage_user.bytes_out + EXCLUDED.bytes_out,
			       updated_at = now()`,
			tenantID, *userID, bytesIn, bytesOut); err != nil {
			return fmt.Errorf("metering: record user transfer: %w", err)
		}
	}
	return nil
}

// RangeTransfer sums uploaded + downloaded bytes over an inclusive [from, to]
// date range — the basis for the Quick-Stats spans (today, this month, 90 days,
// custom). Dates are compared against period_start (day granularity).
func (s *Service) RangeTransfer(ctx context.Context, tenantID int64, from, to time.Time) (bytesIn, bytesOut int64, err error) {
	err = s.client.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(bytes_in), 0), COALESCE(SUM(bytes_out), 0)
		  FROM transfer_usage
		 WHERE tenant_id = $1 AND period_start >= $2 AND period_start <= $3`,
		tenantID, from, to,
	).Scan(&bytesIn, &bytesOut)
	if err != nil {
		return 0, 0, fmt.Errorf("metering: range transfer: %w", err)
	}
	return bytesIn, bytesOut, nil
}

// RangeTransferUser is RangeTransfer scoped to one member's own transfer
// (transfer_usage_user). Used for the per-member Home dashboard tiles.
func (s *Service) RangeTransferUser(ctx context.Context, tenantID, userID int64, from, to time.Time) (bytesIn, bytesOut int64, err error) {
	err = s.client.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(bytes_in), 0), COALESCE(SUM(bytes_out), 0)
		  FROM transfer_usage_user
		 WHERE tenant_id = $1 AND user_id = $2 AND period_start >= $3 AND period_start <= $4`,
		tenantID, userID, from, to,
	).Scan(&bytesIn, &bytesOut)
	if err != nil {
		return 0, 0, fmt.Errorf("metering: range user transfer: %w", err)
	}
	return bytesIn, bytesOut, nil
}

// Usage returns the account's current-month transfer total and plan cap.
func (s *Service) Usage(ctx context.Context, tenantID int64) (Usage, error) {
	var used int64
	var cap sql.NullInt64
	err := s.client.QueryRowContext(ctx, `
		SELECT COALESCE((SELECT SUM(bytes_in + bytes_out)
		                   FROM transfer_usage
		                  WHERE tenant_id = t.id
		                    AND period_start >= date_trunc('month', now())::date
		                    AND period_start <  (date_trunc('month', now()) + interval '1 month')::date), 0),
		       p.max_transfer_bytes_month
		  FROM tenants t JOIN plans p ON p.id = t.plan_id
		 WHERE t.id = $1`, tenantID,
	).Scan(&used, &cap)
	if err != nil {
		return Usage{}, fmt.Errorf("metering: usage: %w", err)
	}
	u := Usage{UsedBytes: used}
	if cap.Valid {
		c := cap.Int64
		u.CapBytes = &c
		u.Over = used >= c
	}
	return u, nil
}

// OverCap reports whether the account has met or exceeded its monthly transfer
// cap. Unlimited plans are never over.
func (s *Service) OverCap(ctx context.Context, tenantID int64) (bool, error) {
	u, err := s.Usage(ctx, tenantID)
	if err != nil {
		return false, err
	}
	return u.Over, nil
}
