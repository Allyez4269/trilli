// Package stats powers the Home "Quick stats" panel — span-aware account
// metrics over an arbitrary date range (today, this month, 90 days, custom).
//
// Everything here is computed from real timestamped data:
//   - transfer bytes  -> transfer_usage (daily rows; see system/metering)
//   - files added     -> files.created_at
//   - members joined  -> tenant_members.joined_at
// No fabricated trends; an empty span returns zeros.
package stats

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"trilli/system/database/postgres"
	"trilli/system/metering"
)

const packageName = "stats"

// Service computes account metrics for a date range.
type Service struct {
	client *postgres.Client
	meter  *metering.Service
}

func NewService(c *postgres.Client, m *metering.Service) *Service {
	return &Service{client: c, meter: m}
}

// Summary is the metric set for one span.
type Summary struct {
	TransferIn    int64 `json:"transfer_in"`    // bytes uploaded in span
	TransferOut   int64 `json:"transfer_out"`   // bytes downloaded in span
	FilesAdded    int64 `json:"files_added"`    // files created in span
	MembersJoined int64 `json:"members_joined"` // members who joined in span
	// Series is the per-day breakdown over the span, powering the tile
	// sparklines. Real data only — empty days are zeros, never interpolated.
	Series []DayPoint `json:"series"`
}

// DayPoint is one UTC day's metrics within a span.
type DayPoint struct {
	Day      string `json:"day"`      // YYYY-MM-DD (UTC)
	Transfer int64  `json:"transfer"` // bytes in+out that day
	Files    int64  `json:"files"`    // files created that day
	Members  int64  `json:"members"`  // members who joined that day
}

// maxSeriesDays caps the per-day series so a very large custom range can't
// produce an unbounded payload; the tile sparklines never need more.
const maxSeriesDays = 366

const dayFmt = "2006-01-02"

// Range computes the summary over an inclusive [from, to] day range. When
// actorUserID is non-nil the metrics are scoped to that one member (their own
// transfer + uploads); nil yields the account-wide totals for admins/owners.
func (s *Service) Range(ctx context.Context, tenantID int64, actorUserID *int64, from, to time.Time) (Summary, error) {
	var out Summary

	var (
		in, eg int64
		err    error
	)
	if actorUserID == nil {
		in, eg, err = s.meter.RangeTransfer(ctx, tenantID, from, to)
	} else {
		in, eg, err = s.meter.RangeTransferUser(ctx, tenantID, *actorUserID, from, to)
	}
	if err != nil {
		return out, err
	}
	out.TransferIn, out.TransferOut = in, eg

	// created_at / joined_at are timestamps; make `to` inclusive of the whole day.
	toExcl := to.AddDate(0, 0, 1)

	filesQ := `SELECT COUNT(*) FROM files
		 WHERE tenant_id = $1 AND created_at >= $2 AND created_at < $3`
	filesArgs := []any{tenantID, from, toExcl}
	if actorUserID != nil {
		filesQ += ` AND uploaded_by_user_id = $4`
		filesArgs = append(filesArgs, *actorUserID)
	}
	if err := s.client.QueryRowContext(ctx, filesQ, filesArgs...).Scan(&out.FilesAdded); err != nil {
		return out, fmt.Errorf("stats: files added: %w", err)
	}

	// "New users" is an account-level metric — only the account-wide (admin/owner)
	// view computes it; members don't see that tile.
	if actorUserID == nil {
		if err := s.client.QueryRowContext(ctx, `
			SELECT COUNT(*) FROM tenant_members
			 WHERE tenant_id = $1 AND status = 'active' AND joined_at >= $2 AND joined_at < $3`,
			tenantID, from, toExcl,
		).Scan(&out.MembersJoined); err != nil {
			return out, fmt.Errorf("stats: members joined: %w", err)
		}
	}

	out.Series, err = s.series(ctx, tenantID, actorUserID, from, to)
	if err != nil {
		return out, err
	}

	return out, nil
}

// series builds the per-day breakdown over [from, to] from real rows. Each
// metric is summed/counted per UTC day; days with no activity stay zero. When
// actorUserID is non-nil, transfer + files are scoped to that member and the
// account-only "members joined" series is left empty.
func (s *Service) series(ctx context.Context, tenantID int64, actorUserID *int64, from, to time.Time) ([]DayPoint, error) {
	transfer := map[string]int64{}
	files := map[string]int64{}
	members := map[string]int64{}
	toExcl := to.AddDate(0, 0, 1)

	scan := func(rows *sql.Rows, err error, dst map[string]int64) error {
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var d time.Time
			var v int64
			if err := rows.Scan(&d, &v); err != nil {
				return err
			}
			dst[d.UTC().Format(dayFmt)] = v
		}
		return rows.Err()
	}

	// Transfer per day — account-wide (transfer_usage) or per-member
	// (transfer_usage_user).
	var trows *sql.Rows
	var terr error
	if actorUserID == nil {
		trows, terr = s.client.QueryContext(ctx, `
			SELECT period_start, bytes_in + bytes_out FROM transfer_usage
			 WHERE tenant_id = $1 AND period_start >= $2 AND period_start <= $3`,
			tenantID, from, to)
	} else {
		trows, terr = s.client.QueryContext(ctx, `
			SELECT period_start, bytes_in + bytes_out FROM transfer_usage_user
			 WHERE tenant_id = $1 AND user_id = $2 AND period_start >= $3 AND period_start <= $4`,
			tenantID, *actorUserID, from, to)
	}
	if err := scan(trows, terr, transfer); err != nil {
		return nil, fmt.Errorf("stats: transfer series: %w", err)
	}

	// Files per day, optionally scoped to the member's own uploads.
	filesQ := `SELECT created_at::date AS d, COUNT(*) FROM files
		 WHERE tenant_id = $1 AND created_at >= $2 AND created_at < $3`
	filesArgs := []any{tenantID, from, toExcl}
	if actorUserID != nil {
		filesQ += ` AND uploaded_by_user_id = $4`
		filesArgs = append(filesArgs, *actorUserID)
	}
	filesQ += ` GROUP BY d`
	frows, ferr := s.client.QueryContext(ctx, filesQ, filesArgs...)
	if err := scan(frows, ferr, files); err != nil {
		return nil, fmt.Errorf("stats: files series: %w", err)
	}

	// Members joined per day — account-wide view only.
	if actorUserID == nil {
		mrows, merr := s.client.QueryContext(ctx, `
			SELECT joined_at::date AS d, COUNT(*) FROM tenant_members
			 WHERE tenant_id = $1 AND status = 'active' AND joined_at >= $2 AND joined_at < $3
			 GROUP BY d`, tenantID, from, toExcl)
		if err := scan(mrows, merr, members); err != nil {
			return nil, fmt.Errorf("stats: members series: %w", err)
		}
	}

	out := make([]DayPoint, 0)
	for d := from; !d.After(to); d = d.AddDate(0, 0, 1) {
		key := d.UTC().Format(dayFmt)
		out = append(out, DayPoint{
			Day:      key,
			Transfer: transfer[key],
			Files:    files[key],
			Members:  members[key],
		})
		if len(out) >= maxSeriesDays {
			break
		}
	}
	return out, nil
}
