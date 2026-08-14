package operators

import (
	"context"
	"encoding/json"
	"fmt"
)

// AuditEntry is one operator-action record (cmx_operator_audit, SPEC §6.7). The
// table is append-only (DB trigger); these rows can never be updated/deleted.
type AuditEntry struct {
	ID            int64           `json:"id"`
	OperatorID    int64           `json:"operator_id"`
	OperatorEmail string          `json:"operator_email"`
	RoleSnapshot  string          `json:"role_snapshot"`
	Action        string          `json:"action"`
	TargetType    string          `json:"target_type"`
	TargetID      string          `json:"target_id"`
	TenantID      *int64          `json:"tenant_id,omitempty"`
	Summary       string          `json:"summary"`
	Meta          json.RawMessage `json:"meta"`
	IP            string          `json:"ip"`
	ContinentCode string          `json:"continent_code"`
	CountryCode   string          `json:"country_code"`
	Region        string          `json:"region"`
	CreatedAt     string          `json:"created_at"`
}

// AuditInput captures one action to record.
type AuditInput struct {
	Action     string
	TargetType string
	TargetID   string
	TenantID   *int64
	Summary    string
	Meta       map[string]any
}

// Audit appends an operator-action entry, snapshotting the actor's identity,
// role, and request geo so attribution survives later changes. Best-effort
// metadata: a marshal failure degrades to an empty object rather than failing
// the action.
func (s *Service) Audit(ctx context.Context, actor *Operator, lc LoginContext, in AuditInput) error {
	meta := []byte(`{}`)
	if in.Meta != nil {
		if b, err := json.Marshal(in.Meta); err == nil {
			meta = b
		}
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO cmx_operator_audit
			(operator_id, operator_email, role_snapshot, action, target_type, target_id,
			 tenant_id, summary, meta, ip, continent_code, country_code, region)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)`,
		actor.ID, actor.Email, string(actor.Role), in.Action, in.TargetType, in.TargetID,
		in.TenantID, in.Summary, meta, lc.IP, lc.Geo.ContinentCode, lc.Geo.CountryCode, lc.Geo.Region,
	)
	if err != nil {
		return fmt.Errorf("operators: write audit: %w", err)
	}
	return nil
}

// ListAudit returns operator-action entries newest-first, scoped per SPEC §6.7:
// Global admins see ALL entries; a CMX admin sees only their OWN. limit is
// clamped to a sane maximum.
func (s *Service) ListAudit(ctx context.Context, viewer *Operator, limit int) ([]AuditEntry, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	var (
		rows interface {
			Next() bool
			Scan(...any) error
			Close() error
			Err() error
		}
		err error
	)
	const cols = `id, operator_id, operator_email, role_snapshot, action, target_type,
		target_id, tenant_id, summary, meta, ip, continent_code, country_code, region,
		to_char(created_at, 'YYYY-MM-DD"T"HH24:MI:SSOF') AS created_at`
	if viewer.IsGlobal() {
		rows, err = s.db.QueryContext(ctx,
			`SELECT `+cols+` FROM cmx_operator_audit ORDER BY created_at DESC LIMIT $1`, limit)
	} else {
		rows, err = s.db.QueryContext(ctx,
			`SELECT `+cols+` FROM cmx_operator_audit WHERE operator_id = $1 ORDER BY created_at DESC LIMIT $2`,
			viewer.ID, limit)
	}
	if err != nil {
		return nil, fmt.Errorf("operators: list audit: %w", err)
	}
	defer rows.Close()

	var out []AuditEntry
	for rows.Next() {
		var e AuditEntry
		var tenantID *int64
		var meta []byte
		if err := rows.Scan(&e.ID, &e.OperatorID, &e.OperatorEmail, &e.RoleSnapshot,
			&e.Action, &e.TargetType, &e.TargetID, &tenantID, &e.Summary, &meta,
			&e.IP, &e.ContinentCode, &e.CountryCode, &e.Region, &e.CreatedAt); err != nil {
			return nil, fmt.Errorf("operators: scan audit: %w", err)
		}
		e.TenantID = tenantID
		if len(meta) == 0 {
			meta = []byte(`{}`)
		}
		e.Meta = meta
		out = append(out, e)
	}
	return out, rows.Err()
}
