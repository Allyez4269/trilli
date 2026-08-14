// Package tiering is the cost-analysis (and, later, the decision) layer for
// adaptive Azure blob storage tiering. This first cut is measurement only:
// Analyze reports, per tenant, the storage-vs-egress profile and the size of
// the "cold tail" (bytes not read recently), plus the storage savings we'd
// capture by moving that tail Hot→Cool / Hot→Cold.
//
// Nothing here moves a byte yet — it tells us whether there's money on the
// table before we build the engine that acts on it.
package tiering

import (
	"context"
	"fmt"
	"time"

	"trilli/system/database/postgres"
)

const packageName = "tiering"

// Approximate Azure Blob pricing (LRS, US East), dollars per GB-month of
// stored capacity. These are for RELATIVE comparison and projection only —
// confirm current rates on the Azure pricing calculator before billing
// decisions. Egress (internet download) is deliberately NOT here: it costs the
// same on every tier, so tiering can't reduce it (CDN/SAS is that lever).
const (
	hotPerGBMonth  = 0.0184
	coolPerGBMonth = 0.0100
	coldPerGBMonth = 0.0036

	// Retrieval (read) fee per GB when data lives in the cooler tiers. Charged
	// on every read, so they're the reason not to tier down hot data.
	coolRetrievalPerGB = 0.01
	coldRetrievalPerGB = 0.03
)

// Cold-tail thresholds: a file untouched longer than these is a candidate to
// move down a tier. Aligned to Azure's minimum-retention windows (Cool 30d,
// Cold 90d) so we don't tier something down only to pay an early-delete penalty.
const (
	coolAfterDays = 30
	coldAfterDays = 90
)

// Classification thresholds on the ratio (trailing-30d egress bytes / stored
// bytes). >1 means a tenant downloads more than it stores each month
// (egress-dominated); <0.1 means it mostly just stores (tiering-friendly).
const (
	egressHeavyRatio  = 1.0
	storageHeavyRatio = 0.1
	// Below this, a tenant's footprint is too small for tiering to matter.
	minRelevantBytes = 1 << 30 // 1 GiB
)

const bytesPerGB = 1024.0 * 1024.0 * 1024.0

// TenantProfile is one tenant's storage/egress picture.
type TenantProfile struct {
	TenantID     int64
	Name         string
	Plan         string
	StoredBytes  int64   // SUM of active file bytes
	Egress30dBytes int64 // trailing-30d internet download
	CoolBytes    int64   // active bytes last read 30–90d ago (Hot→Cool candidates)
	ColdBytes    int64   // active bytes last read >90d ago (Hot→Cold candidates)
	Ratio        float64 // egress30d / stored
	Class        string  // "egress-heavy" | "storage-heavy" | "balanced" | "negligible"
	MonthlySavings float64 // projected $/mo from tiering this tenant's cold tail
}

// Report is the full analysis across all active tenants.
type Report struct {
	GeneratedAt    time.Time
	Tenants        []TenantProfile
	TotalStored    int64
	TotalEgress30d int64
	TotalCool      int64
	TotalCold      int64
	TotalSavings   float64
}

// Service runs the analysis against Postgres.
type Service struct {
	client *postgres.Client
}

func NewService(c *postgres.Client) *Service { return &Service{client: c} }

// Analyze computes the per-tenant tiering profile from existing data: stored
// bytes + cold tail from `files`, trailing-30d egress from `transfer_usage`.
func (s *Service) Analyze(ctx context.Context, now time.Time) (*Report, error) {
	rows, err := s.client.QueryContext(ctx, `
		SELECT t.id, t.name, p.name AS plan,
		       COALESCE(SUM(f.size_bytes) FILTER (WHERE f.status = 'active'), 0) AS stored,
		       COALESCE(SUM(f.size_bytes) FILTER (
		           WHERE f.status = 'active'
		             AND f.last_accessed_at <  $1::timestamptz - make_interval(days => $2)
		             AND f.last_accessed_at >= $1::timestamptz - make_interval(days => $3)), 0) AS cool_bytes,
		       COALESCE(SUM(f.size_bytes) FILTER (
		           WHERE f.status = 'active'
		             AND f.last_accessed_at < $1::timestamptz - make_interval(days => $3)), 0) AS cold_bytes,
		       (SELECT COALESCE(SUM(tu.bytes_out), 0) FROM transfer_usage tu
		         WHERE tu.tenant_id = t.id AND tu.period_start >= ($1::date - 30)) AS egress_30d
		  FROM tenants t
		  JOIN plans p ON p.id = t.plan_id
		  LEFT JOIN files f ON f.tenant_id = t.id
		 WHERE t.status = 'active'
		 GROUP BY t.id, t.name, p.name
		 ORDER BY stored DESC`,
		now, coolAfterDays, coldAfterDays)
	if err != nil {
		return nil, fmt.Errorf("tiering: analyze query: %w", err)
	}
	defer rows.Close()

	rep := &Report{GeneratedAt: now}
	for rows.Next() {
		var p TenantProfile
		if err := rows.Scan(&p.TenantID, &p.Name, &p.Plan, &p.StoredBytes,
			&p.CoolBytes, &p.ColdBytes, &p.Egress30dBytes); err != nil {
			return nil, fmt.Errorf("tiering: scan: %w", err)
		}
		p.Ratio = ratio(p.Egress30dBytes, p.StoredBytes)
		p.Class = classify(p.StoredBytes, p.Ratio)
		p.MonthlySavings = tierSavings(p.CoolBytes, p.ColdBytes)

		rep.Tenants = append(rep.Tenants, p)
		rep.TotalStored += p.StoredBytes
		rep.TotalEgress30d += p.Egress30dBytes
		rep.TotalCool += p.CoolBytes
		rep.TotalCold += p.ColdBytes
		rep.TotalSavings += p.MonthlySavings
	}
	return rep, rows.Err()
}

func ratio(egress, stored int64) float64 {
	if stored <= 0 {
		return 0
	}
	return float64(egress) / float64(stored)
}

func classify(stored int64, r float64) string {
	if stored < minRelevantBytes {
		return "negligible"
	}
	switch {
	case r >= egressHeavyRatio:
		return "egress-heavy"
	case r < storageHeavyRatio:
		return "storage-heavy"
	default:
		return "balanced"
	}
}

// tierSavings is the gross monthly storage saving from moving the cool-band
// bytes Hot→Cool and the cold bytes Hot→Cold. Gross of retrieval risk: by
// definition these bytes are cold, so expected reads are low — but the engine
// phase will net out an expected-retrieval term per tenant profile.
func tierSavings(coolBytes, coldBytes int64) float64 {
	coolGB := float64(coolBytes) / bytesPerGB
	coldGB := float64(coldBytes) / bytesPerGB
	return coolGB*(hotPerGBMonth-coolPerGBMonth) + coldGB*(hotPerGBMonth-coldPerGBMonth)
}

// GB renders a byte count as a GB string for reports.
func GB(b int64) string { return fmt.Sprintf("%.3f GB", float64(b)/bytesPerGB) }
