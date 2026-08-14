// Package lifecycle runs the account-lapse end game: the escalating warning
// emails sent during a lapsed account's 30-day read-only window, and the
// permanent purge of its file data once that window closes.
//
// It is the deletion half of S5. The state itself (lifecycle_state='lapsed',
// lapsed_at, purge_at) is owned by the billing webhook (MarkLapsed/ClearLapsed);
// this package only acts on rows that are already lapsed:
//
//   - sendDueWarnings: emails the account owners as each notice threshold is
//     crossed (day 0, 7, 23, 29), tracked by tenants.purge_warn_stage so a
//     given notice is sent exactly once.
//   - purgeExpired: once purge_at passes, permanently deletes the tenant's
//     blobs + file/folder/workspace rows. The tenant row, billing_transactions,
//     and audit_events are intentionally KEPT (financial/legal record); the
//     tenant becomes an empty shell with status='purged' so it can no longer
//     be signed into.
//
// Re-subscribing within the window clears the lapse via the webhook, so the
// janitor simply stops seeing the row — no coordination needed here.
package lifecycle

import (
	"context"
	"database/sql"
	"fmt"

	"trilli/system/database/postgres"
	"trilli/system/logging"
	"trilli/system/mailer"
	"trilli/system/storage"
)

const packageName = "lifecycle"

// warnThresholds are the days-since-lapse at which each warning notice fires.
// Index i corresponds to warning stage i+1, stored in tenants.purge_warn_stage.
// The last threshold is the "final" (≈24h-left) notice. Mirrors the 30-day
// window: day 0 (lapsed), day 7, day 23 (1 week left), day 29 (tomorrow).
var warnThresholds = []int{0, 7, 23, 29}

// Service holds the dependencies the lifecycle sweep needs: DB, blob store
// (to delete file bytes), and mailer (to send warnings).
type Service struct {
	client *postgres.Client
	store  storage.Store
	mailer *mailer.Mailer
}

// NewService wires the lifecycle service. mailer may be nil in environments
// without email configured — warnings are then skipped (purge still runs).
func NewService(c *postgres.Client, s storage.Store, m *mailer.Mailer) *Service {
	return &Service{client: c, store: s, mailer: m}
}

// RunSweep is the janitor entry point. It first sends any due warnings, then
// purges any accounts whose window has closed. Both halves are best-effort
// per tenant: one tenant's failure never blocks the rest of the sweep.
func (s *Service) RunSweep(ctx context.Context) error {
	if n := s.expireCompTerms(ctx); n > 0 {
		logging.Info(packageName, "sweep: %d comp account(s) entered read-only grace at term end", n)
	}
	s.sendDueWarnings(ctx)
	purged := s.purgeExpired(ctx)
	if purged > 0 {
		logging.Info(packageName, "sweep: purged %d lapsed account(s)", purged)
	}
	return nil
}

// expireCompTerms drops comp (complimentary) tenants whose free term has ended
// into read-only grace (SPEC §6.10): lifecycle_state='lapsed' + lapsed_at set,
// but purge_at deliberately LEFT NULL so the purge sweep never deletes them —
// comp data is preserved indefinitely until the ambassador adds payment. The
// app's lapsed-state read-only enforcement (view/download/export ok, uploads
// blocked) applies. Returns the number transitioned.
func (s *Service) expireCompTerms(ctx context.Context) int {
	res, err := s.client.ExecContext(ctx, `
		UPDATE tenants
		   SET lifecycle_state = 'lapsed', lapsed_at = NOW(), updated_at = NOW()
		 WHERE billing_mode = 'comp'
		   AND comp_expires_at IS NOT NULL
		   AND comp_expires_at < NOW()
		   AND lifecycle_state = 'current'`)
	if err != nil {
		logging.Error(packageName, "expire comp terms: %v", err)
		return 0
	}
	n, _ := res.RowsAffected()
	return int(n)
}

// warnStageFor returns the highest warning stage (1..len) whose day threshold
// the elapsed time has reached, or 0 if none have (negative elapsed).
func warnStageFor(daysSinceLapse int) int {
	stage := 0
	for i, t := range warnThresholds {
		if daysSinceLapse >= t {
			stage = i + 1
		}
	}
	return stage
}

// pendingWarn is a tenant in a purge window ('lapsed' or 'closing') whose
// warning progress the sweep evaluates.
type pendingWarn struct {
	tenantID  int64
	name      string
	state     string // 'lapsed' (non-payment) | 'closing' (owner delete)
	warnStage int
	daysSince int
	daysLeft  int
	purgeDate string
}

// sendDueWarnings emails each lapsed account owner when a new notice threshold
// has been crossed, then advances purge_warn_stage so it's sent only once.
func (s *Service) sendDueWarnings(ctx context.Context) {
	rows, err := s.client.QueryContext(ctx, `
		SELECT id, name, lifecycle_state, purge_warn_stage,
		       GREATEST(0, FLOOR(EXTRACT(EPOCH FROM (NOW() - lapsed_at)) / 86400))::int AS days_since,
		       GREATEST(0, CEIL(EXTRACT(EPOCH FROM (purge_at - NOW())) / 86400))::int     AS days_left,
		       to_char(purge_at, 'FMMonth FMDD, YYYY')                                    AS purge_date
		  FROM tenants
		 WHERE lifecycle_state IN ('lapsed', 'closing') AND lapsed_at IS NOT NULL AND purge_at IS NOT NULL`)
	if err != nil {
		logging.Error(packageName, "sendDueWarnings: scan: %v", err)
		return
	}
	var pending []pendingWarn
	for rows.Next() {
		var p pendingWarn
		if err := rows.Scan(&p.tenantID, &p.name, &p.state, &p.warnStage, &p.daysSince, &p.daysLeft, &p.purgeDate); err != nil {
			rows.Close()
			logging.Error(packageName, "sendDueWarnings: scan row: %v", err)
			return
		}
		pending = append(pending, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		logging.Error(packageName, "sendDueWarnings: rows: %v", err)
		return
	}

	for _, p := range pending {
		target := warnStageFor(p.daysSince)
		if target <= p.warnStage {
			continue // already sent the latest due notice
		}
		final := target >= len(warnThresholds)
		// Best-effort send to every owner; we advance the stage regardless so
		// a flaky inbox can't loop the sweep into re-sending forever.
		if s.mailer != nil {
			for _, owner := range s.ownerEmails(ctx, p.tenantID) {
				var err error
				if p.state == "closing" {
					// Owner-initiated delete: delete-specific wording.
					err = s.mailer.SendAccountCloseWarning(ctx, mailer.AccountCloseWarningEmail{
						To:          owner.email,
						Name:        owner.name,
						AccountName: p.name,
						PurgeDate:   p.purgeDate,
						DaysLeft:    p.daysLeft,
						Final:       final,
					})
				} else {
					err = s.mailer.SendLapseWarning(ctx, mailer.LapseWarningEmail{
						To:        owner.email,
						Name:      owner.name,
						PurgeDate: p.purgeDate,
						DaysLeft:  p.daysLeft,
						Final:     final,
					})
				}
				if err != nil {
					logging.Error(packageName, "sendDueWarnings: tenant=%d send to %s: %v", p.tenantID, owner.email, err)
				}
			}
		}
		if _, err := s.client.ExecContext(ctx,
			`UPDATE tenants SET purge_warn_stage = $1 WHERE id = $2 AND lifecycle_state IN ('lapsed', 'closing')`,
			target, p.tenantID); err != nil {
			logging.Error(packageName, "sendDueWarnings: tenant=%d advance stage: %v", p.tenantID, err)
		}
	}
}

type ownerContact struct {
	email string
	name  string
}

// ownerEmails returns the active owners of a tenant (the people who pay and
// can reactivate). Best-effort: errors yield no recipients.
func (s *Service) ownerEmails(ctx context.Context, tenantID int64) []ownerContact {
	rows, err := s.client.QueryContext(ctx, `
		SELECT u.email, COALESCE(u.first_name, '')
		  FROM users u
		  JOIN tenant_members tm ON tm.user_id = u.id
		 WHERE tm.tenant_id = $1 AND tm.role = 'owner'
		   AND tm.status = 'active' AND u.status = 'active'`, tenantID)
	if err != nil {
		logging.Error(packageName, "ownerEmails: tenant=%d: %v", tenantID, err)
		return nil
	}
	defer rows.Close()
	var out []ownerContact
	for rows.Next() {
		var c ownerContact
		if err := rows.Scan(&c.email, &c.name); err != nil {
			logging.Error(packageName, "ownerEmails: scan: %v", err)
			return out
		}
		out = append(out, c)
	}
	return out
}

// purgeExpired permanently deletes every lapsed account whose purge_at has
// passed. Returns the number purged.
func (s *Service) purgeExpired(ctx context.Context) int {
	rows, err := s.client.QueryContext(ctx, `
		SELECT id, name FROM tenants
		 WHERE lifecycle_state IN ('lapsed', 'closing') AND purge_at IS NOT NULL AND purge_at < NOW()`)
	if err != nil {
		logging.Error(packageName, "purgeExpired: scan: %v", err)
		return 0
	}
	type ref struct {
		id   int64
		name string
	}
	var refs []ref
	for rows.Next() {
		var r ref
		if err := rows.Scan(&r.id, &r.name); err != nil {
			rows.Close()
			logging.Error(packageName, "purgeExpired: scan row: %v", err)
			return 0
		}
		refs = append(refs, r)
	}
	rows.Close()

	purged := 0
	for _, r := range refs {
		if err := s.PurgeTenant(ctx, r.id); err != nil {
			logging.Error(packageName, "purgeExpired: tenant=%d (%s) failed: %v", r.id, r.name, err)
			continue
		}
		purged++
	}
	return purged
}

// PurgeTenant permanently deletes a single lapsed tenant's file data: every
// blob, then the file/folder/workspace rows, then it marks the tenant shell
// 'purged' (login-blocked) with quota zeroed. The tenant row, its
// billing_transactions, and audit_events are KEPT for the financial record.
//
// Guarded: it re-checks lifecycle_state='lapsed' inside the write so a tenant
// that reactivated between the scan and now is never purged. Blob deletes are
// best-effort (an orphaned blob is only a storage cost), exactly like the
// trash janitor.
func (s *Service) PurgeTenant(ctx context.Context, tenantID int64) error {
	// Gather every blob path up front (all files, active or trashed).
	brows, err := s.client.QueryContext(ctx,
		`SELECT blob_path FROM files WHERE tenant_id = $1`, tenantID)
	if err != nil {
		return fmt.Errorf("gather blobs: %w", err)
	}
	var blobs []string
	for brows.Next() {
		var p sql.NullString
		if err := brows.Scan(&p); err != nil {
			brows.Close()
			return fmt.Errorf("scan blob: %w", err)
		}
		if p.Valid && p.String != "" {
			blobs = append(blobs, p.String)
		}
	}
	brows.Close()
	if err := brows.Err(); err != nil {
		return fmt.Errorf("blob rows: %w", err)
	}

	tx, err := s.client.GetDB().BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin purge tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	// Guard inside the transaction: only proceed if still in a purge state
	// ('lapsed' = non-payment, 'closing' = owner-initiated delete). A row that
	// reactivated (lifecycle_state back to 'current') is skipped entirely.
	var state string
	if err := tx.QueryRowContext(ctx,
		`SELECT lifecycle_state FROM tenants WHERE id = $1 FOR UPDATE`, tenantID).Scan(&state); err != nil {
		return fmt.Errorf("lock tenant: %w", err)
	}
	if state != "lapsed" && state != "closing" {
		logging.Info(packageName, "PurgeTenant: tenant=%d no longer in a purge state (state=%s) — skipping", tenantID, state)
		return nil
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM files WHERE tenant_id = $1`, tenantID); err != nil {
		return fmt.Errorf("delete files: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM folders WHERE tenant_id = $1`, tenantID); err != nil {
		return fmt.Errorf("delete folders: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM workspaces WHERE tenant_id = $1`, tenantID); err != nil {
		return fmt.Errorf("delete workspaces: %w", err)
	}
	// Terminal shell: status='deleted' (the allowed terminal value — login is
	// gated on status='active'), quota zeroed, no longer scanned by the janitor
	// (purge_at cleared). lifecycle_state stays 'lapsed' as the historical
	// reason, so lapsed+deleted reads as "purged for non-payment" — distinct
	// from an admin delete (status='deleted', lifecycle_state='current').
	if _, err := tx.ExecContext(ctx, `
		UPDATE tenants
		   SET status = 'deleted', storage_bytes_used = 0, purge_at = NULL, updated_at = NOW()
		 WHERE id = $1`, tenantID); err != nil {
		return fmt.Errorf("mark deleted: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit purge: %w", err)
	}
	committed = true

	// Rows are gone; drop the bytes. A failed delete is logged, not fatal.
	deleted := 0
	for _, path := range blobs {
		if err := s.store.Delete(ctx, path); err != nil {
			logging.Error(packageName, "PurgeTenant: blob delete failed (orphaned) path=%s: %v", path, err)
			continue
		}
		deleted++
	}
	logging.Info(packageName, "PurgeTenant: tenant=%d purged blobs=%d/%d", tenantID, deleted, len(blobs))
	return nil
}

// LapsedRow is one lapsed account in the lifecycle status report (CLI).
type LapsedRow struct {
	TenantID  int64
	Name      string
	Status    string
	DaysLeft  int
	WarnStage int
	PurgeDate string
	Expired   bool // purge_at already passed (eligible for purge on next sweep)
}

// Status returns the current lapse pipeline — every lapsed tenant with its
// days-left and warning progress. Used by `trilli lifecycle status` to inspect
// what the sweep will do before it runs.
func (s *Service) Status(ctx context.Context) ([]LapsedRow, error) {
	rows, err := s.client.QueryContext(ctx, `
		SELECT id, name, status, purge_warn_stage,
		       GREATEST(0, CEIL(EXTRACT(EPOCH FROM (purge_at - NOW())) / 86400))::int AS days_left,
		       to_char(purge_at, 'FMMonth FMDD, YYYY')                                AS purge_date,
		       (purge_at < NOW())                                                     AS expired
		  FROM tenants
		 WHERE lifecycle_state = 'lapsed' AND purge_at IS NOT NULL
		 ORDER BY purge_at ASC`)
	if err != nil {
		return nil, fmt.Errorf("lifecycle: status: %w", err)
	}
	defer rows.Close()
	var out []LapsedRow
	for rows.Next() {
		var r LapsedRow
		if err := rows.Scan(&r.TenantID, &r.Name, &r.Status, &r.WarnStage, &r.DaysLeft, &r.PurgeDate, &r.Expired); err != nil {
			return nil, fmt.Errorf("lifecycle: scan status: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
