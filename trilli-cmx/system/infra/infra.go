// Package infra provides CMX's Infrastructure dashboard (SPEC §6.5): the
// background-jobs board, system health, a storage-tiering snapshot, and Azure
// transfer/cost — all read directly from the shared DB (Option C) plus CMX's
// own runtime (master key, GeoIP). The guarded WRITES (run-now / ping, tiering
// mode toggle + apply) are a later slice needing app-side endpoints.
package infra

import (
	"context"
	"database/sql"
	"fmt"

	"trilli-cmx/system/crypto"
	"trilli-cmx/system/database/postgres"
)

const packageName = "infra"

// Azure block-blob $/GB-month by tier (mirrors system/storage/tiering). Used to
// project the savings already realized by tiered bytes.
const (
	hotPerGBMonth  = 0.0184
	coolPerGBMonth = 0.0100
	coldPerGBMonth = 0.0036
	bytesPerGB     = 1024.0 * 1024.0 * 1024.0
)

// Service runs infrastructure read queries.
type Service struct {
	db        *postgres.Client
	geoReady  func() bool
	geoSource string
}

// NewService constructs the infra Service. geoReady reports whether CMX's GeoIP
// database is loaded (platform GeoIP health); geoSource names the DB file.
func NewService(db *postgres.Client, geoReady func() bool, geoSource string) *Service {
	if geoReady == nil {
		geoReady = func() bool { return false }
	}
	return &Service{db: db, geoReady: geoReady, geoSource: geoSource}
}

// Job is one row of the background-jobs board (job_runs).
type Job struct {
	Job            string  `json:"job"`
	LastNode       string  `json:"last_node"`
	LastStartedAt  *string `json:"last_started_at,omitempty"`
	LastFinishedAt *string `json:"last_finished_at,omitempty"`
	LastOK         bool    `json:"last_ok"`
	LastNote       string  `json:"last_note"`
	RunCount       int64   `json:"run_count"`
}

// ListJobs returns the coordinated background jobs, most-recently-run first.
func (s *Service) ListJobs(ctx context.Context) ([]Job, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT job, COALESCE(last_node,''),
		       to_char(last_started_at,  'YYYY-MM-DD"T"HH24:MI:SSOF'),
		       to_char(last_finished_at, 'YYYY-MM-DD"T"HH24:MI:SSOF'),
		       last_ok, COALESCE(last_note,''), run_count
		  FROM job_runs
		 ORDER BY last_finished_at DESC NULLS LAST, job ASC`)
	if err != nil {
		return nil, fmt.Errorf("infra: list jobs: %w", err)
	}
	defer rows.Close()
	out := []Job{}
	for rows.Next() {
		var j Job
		var started, finished *string
		if err := rows.Scan(&j.Job, &j.LastNode, &started, &finished, &j.LastOK, &j.LastNote, &j.RunCount); err != nil {
			return nil, fmt.Errorf("infra: scan job: %w", err)
		}
		j.LastStartedAt, j.LastFinishedAt = started, finished
		out = append(out, j)
	}
	return out, rows.Err()
}

// Health is the system-health snapshot (SPEC §6.5).
type Health struct {
	DBVersion           int64  `json:"db_version"`
	DBDirty             bool   `json:"db_dirty"`
	MasterKeyConfigured bool   `json:"master_key_configured"`
	GeoIPReady          bool   `json:"geoip_ready"`
	GeoIPSource         string `json:"geoip_source"`
	// Blob encryption-at-rest migration progress.
	FilesTotal     int64 `json:"files_total"`
	FilesEncrypted int64 `json:"files_encrypted"`
	FilesLegacy    int64 `json:"files_legacy"`
	// Storage-tier distribution (active files).
	HotBytes  int64 `json:"hot_bytes"`
	CoolBytes int64 `json:"cool_bytes"`
	ColdBytes int64 `json:"cold_bytes"`
	// Realized monthly $ saving from the bytes already in Cool/Cold vs all-Hot.
	TieringSavingsUSD float64 `json:"tiering_savings_usd"`
	// Last scheduled tiering execution (job_runs row for the 'tiering' job).
	TieringLastRun *string `json:"tiering_last_run,omitempty"`
	TieringLastOK  bool    `json:"tiering_last_ok"`
	TieringRuns    int64   `json:"tiering_runs"`
}

// Health computes the system-health snapshot.
func (s *Service) Health(ctx context.Context) (*Health, error) {
	var h Health
	h.MasterKeyConfigured = crypto.KeyConfigured()
	h.GeoIPReady = s.geoReady()
	h.GeoIPSource = s.geoSource

	// App schema version (the app's own migration table, not CMX's).
	_ = s.db.QueryRowContext(ctx,
		`SELECT version, dirty FROM schema_migrations LIMIT 1`).Scan(&h.DBVersion, &h.DBDirty)

	// Blob encryption-at-rest progress over active files.
	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*),
		       COUNT(*) FILTER (WHERE encrypted),
		       COUNT(*) FILTER (WHERE NOT encrypted)
		  FROM files WHERE status = 'active'`).Scan(&h.FilesTotal, &h.FilesEncrypted, &h.FilesLegacy); err != nil {
		return nil, fmt.Errorf("infra: encryption progress: %w", err)
	}

	// Tier byte distribution over active files.
	if err := s.db.QueryRowContext(ctx, `
		SELECT
		  COALESCE(SUM(size_bytes) FILTER (WHERE access_tier = 'hot'), 0),
		  COALESCE(SUM(size_bytes) FILTER (WHERE access_tier = 'cool'), 0),
		  COALESCE(SUM(size_bytes) FILTER (WHERE access_tier = 'cold'), 0)
		  FROM files WHERE status = 'active'`).Scan(&h.HotBytes, &h.CoolBytes, &h.ColdBytes); err != nil {
		return nil, fmt.Errorf("infra: tier distribution: %w", err)
	}
	coolGB := float64(h.CoolBytes) / bytesPerGB
	coldGB := float64(h.ColdBytes) / bytesPerGB
	h.TieringSavingsUSD = coolGB*(hotPerGBMonth-coolPerGBMonth) + coldGB*(hotPerGBMonth-coldPerGBMonth)

	// Last scheduled tiering execution + run count, for the status line.
	var lastRun sql.NullString
	var lastOK sql.NullBool
	_ = s.db.QueryRowContext(ctx, `
		SELECT to_char(last_finished_at, 'YYYY-MM-DD"T"HH24:MI:SSOF'), last_ok, run_count
		  FROM job_runs WHERE job = 'tiering'`).Scan(&lastRun, &lastOK, &h.TieringRuns)
	if lastRun.Valid {
		h.TieringLastRun = &lastRun.String
	}
	h.TieringLastOK = lastOK.Bool
	return &h, nil
}

// CostRow is a tenant's transfer (egress/ingress) for the current period.
type CostRow struct {
	TenantID   int64  `json:"tenant_id"`
	TenantName string `json:"tenant_name"`
	BytesIn    int64  `json:"bytes_in"`
	BytesOut   int64  `json:"bytes_out"`
}

// CostReport is the platform transfer summary for a period.
type CostReport struct {
	PeriodStart   string    `json:"period_start"`
	TotalBytesIn  int64     `json:"total_bytes_in"`
	TotalBytesOut int64     `json:"total_bytes_out"`
	TopTenants    []CostRow `json:"top_tenants"`
}

// Cost aggregates transfer_usage for the most recent period present, with the
// top egress tenants. Egress is explicitly NOT reducible by tiering (SPEC §6.5).
func (s *Service) Cost(ctx context.Context) (*CostReport, error) {
	var r CostReport
	var period *string
	if err := s.db.QueryRowContext(ctx,
		`SELECT to_char(MAX(period_start), 'YYYY-MM-DD') FROM transfer_usage`).Scan(&period); err != nil {
		return nil, fmt.Errorf("infra: cost period: %w", err)
	}
	if period == nil {
		return &r, nil // no usage recorded yet
	}
	r.PeriodStart = *period
	if err := s.db.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(bytes_in),0), COALESCE(SUM(bytes_out),0)
		  FROM transfer_usage WHERE period_start = $1::date`, *period).Scan(&r.TotalBytesIn, &r.TotalBytesOut); err != nil {
		return nil, fmt.Errorf("infra: cost totals: %w", err)
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT u.tenant_id, COALESCE(t.name,''), u.bytes_in, u.bytes_out
		  FROM transfer_usage u LEFT JOIN tenants t ON t.id = u.tenant_id
		 WHERE u.period_start = $1::date
		 ORDER BY u.bytes_out DESC, u.bytes_in DESC
		 LIMIT 20`, *period)
	if err != nil {
		return nil, fmt.Errorf("infra: cost tenants: %w", err)
	}
	defer rows.Close()
	r.TopTenants = []CostRow{}
	for rows.Next() {
		var c CostRow
		if err := rows.Scan(&c.TenantID, &c.TenantName, &c.BytesIn, &c.BytesOut); err != nil {
			return nil, fmt.Errorf("infra: scan cost row: %w", err)
		}
		r.TopTenants = append(r.TopTenants, c)
	}
	return &r, rows.Err()
}
