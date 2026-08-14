package tiering

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"trilli/system/database/postgres"
	"trilli/system/logging"
	"trilli/system/storage"
)

// Mode controls what the engine is allowed to do.
type Mode string

const (
	// ModeOff: engine does nothing (janitor won't even start).
	ModeOff Mode = "off"
	// ModeDryRun: computes and logs the moves it WOULD make, touches no bytes.
	ModeDryRun Mode = "dryrun"
	// ModeLive: actually re-tiers blobs and records the new tier.
	ModeLive Mode = "live"
)

// ParseMode maps an env/flag string to a Mode, defaulting to Off.
func ParseMode(s string) Mode {
	switch Mode(s) {
	case ModeDryRun:
		return ModeDryRun
	case ModeLive:
		return ModeLive
	default:
		return ModeOff
	}
}

// Engine decisioning + tuning knobs.
const (
	// Files smaller than this are never tiered: the per-blob SetTier + future
	// read transactions cost more than the storage they'd save.
	minTierBytes = 256 << 10 // 256 KiB

	// Azure minimum retention before a Cool blob can move on without an
	// early-deletion penalty. We hold Cool→Cold until this elapses.
	coolMinRetentionDays = 30

	// SQL pre-filter: the most-aggressive cool threshold across all tenant
	// classes (storageHeavyFactor * coolAfterDays = 0.5 * 30). Any file that
	// could possibly be tiered down under any class is at least this idle;
	// non-hot files are always included (for promotion / Cool→Cold).
	candidateMinIdleDays = 15

	// Safety caps per run so a backlog can't issue an unbounded burst of Azure
	// operations (each SetTier is a billable transaction).
	maxScanRows    = 5000
	maxMovesPerRun = 500

	// Per-tenant threshold multipliers by behavioral class.
	egressHeavyFactor  = 2.0 // re-reads likely → tier down conservatively
	storageHeavyFactor = 0.5 // rarely read → tier down aggressively
	balancedFactor     = 1.0
)

// Move is one planned tier transition.
type Move struct {
	FileID   int64
	TenantID int64
	BlobPath string
	Bytes    int64
	From     string
	To       string
	Reason   string
}

// Summary reports what a run did (or would do).
type Summary struct {
	Mode       Mode
	Scanned    int
	Planned    int
	Applied    int
	Failed     int
	Promotions int // moves back up to Hot (a file went cold then was accessed)
	Demotions  int // moves down to Cool/Cold
	Capped     bool
}

// Engine runs the adaptive tiering decisions.
type Engine struct {
	client *postgres.Client
	store  storage.TieredStore
	mode   Mode
}

// NewEngine builds the engine. store may be nil or a non-tiered backend — in
// that case the engine forces ModeOff (nothing to act on).
func NewEngine(c *postgres.Client, store storage.TieredStore, mode Mode) *Engine {
	if store == nil {
		mode = ModeOff
	}
	return &Engine{client: c, store: store, mode: mode}
}

// Run computes the plan and, in ModeLive, applies it. Safe to call on a timer.
func (e *Engine) Run(ctx context.Context, now time.Time) (Summary, error) {
	sum := Summary{Mode: e.mode}
	if e.mode == ModeOff {
		return sum, nil
	}

	moves, scanned, capped, err := e.Plan(ctx, now)
	if err != nil {
		return sum, err
	}
	sum.Scanned = scanned
	sum.Planned = len(moves)
	sum.Capped = capped
	for _, m := range moves {
		if m.To == storage.TierHot {
			sum.Promotions++
		} else {
			sum.Demotions++
		}
	}

	if e.mode == ModeDryRun {
		for _, m := range moves {
			logging.Info(packageName, "[dryrun] file=%d %s→%s (%s, %s)", m.FileID, m.From, m.To, GB(m.Bytes), m.Reason)
		}
		return sum, nil
	}

	// ModeLive: apply each move, recording the new tier only on success.
	for _, m := range moves {
		if err := e.store.SetTier(ctx, m.BlobPath, m.To); err != nil {
			logging.Error(packageName, "SetTier file=%d %s→%s: %v", m.FileID, m.From, m.To, err)
			sum.Failed++
			continue
		}
		if _, err := e.client.ExecContext(ctx,
			`UPDATE files SET access_tier = $2, tier_changed_at = NOW() WHERE id = $1`,
			m.FileID, m.To); err != nil {
			logging.Error(packageName, "record tier file=%d: %v", m.FileID, err)
			sum.Failed++
			continue
		}
		sum.Applied++
		logging.Info(packageName, "tiered file=%d %s→%s (%s)", m.FileID, m.From, m.To, m.Reason)
	}
	return sum, nil
}

// Plan computes the pending moves without touching anything.
func (e *Engine) Plan(ctx context.Context, now time.Time) (moves []Move, scanned int, capped bool, err error) {
	factors, err := e.classFactors(ctx)
	if err != nil {
		return nil, 0, false, err
	}

	rows, err := e.client.QueryContext(ctx, `
		SELECT id, tenant_id, blob_path, size_bytes, access_tier,
		       last_accessed_at, tier_changed_at
		  FROM files
		 WHERE status = 'active'
		   AND ( (access_tier = 'hot' AND last_accessed_at < $1::timestamptz - make_interval(days => $2))
		         OR access_tier <> 'hot' )
		 ORDER BY last_accessed_at ASC NULLS FIRST
		 LIMIT $3`,
		now, candidateMinIdleDays, maxScanRows)
	if err != nil {
		return nil, 0, false, fmt.Errorf("tiering: plan scan: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var (
			id, tenantID, size int64
			blobPath, tier     string
			lastAccessed       sql.NullTime
			tierChanged        sql.NullTime
		)
		if err := rows.Scan(&id, &tenantID, &blobPath, &size, &tier, &lastAccessed, &tierChanged); err != nil {
			return nil, scanned, false, fmt.Errorf("tiering: plan scan row: %w", err)
		}
		scanned++

		if size < minTierBytes {
			continue // never worth tiering
		}
		idleDays := daysSince(lastAccessed, now)
		factor := factors[tenantID]
		if factor == 0 {
			factor = balancedFactor
		}
		desired := desiredTier(idleDays, factor)
		if desired == tier {
			continue
		}
		// Respect Cool's minimum retention before moving Cool→Cold so we don't
		// incur an early-deletion penalty. Promotions (→Hot) are never blocked.
		if desired == storage.TierCold && tier == storage.TierCool &&
			daysSince(tierChanged, now) < coolMinRetentionDays {
			continue
		}
		moves = append(moves, Move{
			FileID: id, TenantID: tenantID, BlobPath: blobPath, Bytes: size,
			From: tier, To: desired, Reason: reasonFor(desired, idleDays, factor),
		})
		if len(moves) >= maxMovesPerRun {
			capped = true
			break
		}
	}
	return moves, scanned, capped, rows.Err()
}

// desiredTier is the recency-based target. Because it keys off how long since
// the file was last read, a recently-accessed Cool/Cold file naturally resolves
// to Hot — that's the automatic promotion that keeps the system fluid.
func desiredTier(idleDays, factor float64) string {
	switch {
	case idleDays >= coldAfterDays*factor:
		return storage.TierCold
	case idleDays >= coolAfterDays*factor:
		return storage.TierCool
	default:
		return storage.TierHot
	}
}

func reasonFor(target string, idleDays, factor float64) string {
	if target == storage.TierHot {
		return fmt.Sprintf("accessed %.0fd ago — promote", idleDays)
	}
	return fmt.Sprintf("idle %.0fd (factor %.1f)", idleDays, factor)
}

// classFactors loads each active tenant's threshold multiplier from its
// egress-vs-storage profile (reusing the same classification as the report).
func (e *Engine) classFactors(ctx context.Context) (map[int64]float64, error) {
	rows, err := e.client.QueryContext(ctx, `
		SELECT t.id,
		       COALESCE(SUM(f.size_bytes) FILTER (WHERE f.status = 'active'), 0) AS stored,
		       (SELECT COALESCE(SUM(tu.bytes_out), 0) FROM transfer_usage tu
		         WHERE tu.tenant_id = t.id AND tu.period_start >= (current_date - 30)) AS egress
		  FROM tenants t
		  LEFT JOIN files f ON f.tenant_id = t.id
		 WHERE t.status = 'active'
		 GROUP BY t.id`)
	if err != nil {
		return nil, fmt.Errorf("tiering: class factors: %w", err)
	}
	defer rows.Close()

	out := make(map[int64]float64)
	for rows.Next() {
		var id, stored, egress int64
		if err := rows.Scan(&id, &stored, &egress); err != nil {
			return nil, err
		}
		out[id] = factorFor(classify(stored, ratio(egress, stored)))
	}
	return out, rows.Err()
}

func factorFor(class string) float64 {
	switch class {
	case "egress-heavy":
		return egressHeavyFactor
	case "storage-heavy":
		return storageHeavyFactor
	default:
		return balancedFactor
	}
}

func daysSince(t sql.NullTime, now time.Time) float64 {
	if !t.Valid {
		return 1 << 20 // effectively infinite — treat unknown as very old
	}
	return now.Sub(t.Time).Hours() / 24.0
}
