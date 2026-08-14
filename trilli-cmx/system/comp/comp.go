// Package comp is CMX's comp/ambassador surface (SPEC §6.10): list invites +
// the ambassador registry (read directly from the shared DB) and send/revoke
// invites (proxied to the app's admin API). Comp invites are unrestricted —
// any operator may send one; the list is scoped per-operator for CMX admins.
package comp

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"trilli-cmx/system/database/postgres"
)

const packageName = "comp"

// Service reads comp invites + manages the guardrail policy.
type Service struct {
	db *postgres.Client
}

// NewService constructs the CMX comp Service.
func NewService(db *postgres.Client) *Service { return &Service{db: db} }

// InviteRow is one comp-invite row joined with its plan name + (if registered)
// the resulting tenant's name and comp expiry.
type InviteRow struct {
	ID              int64      `json:"id"`
	Email           string     `json:"email"`
	PlanCode        string     `json:"plan_code"`
	PlanName        string     `json:"plan_name"`
	FreeTermDays    int        `json:"free_term_days"`
	Status          string     `json:"status"`
	InviteExpiresAt time.Time  `json:"invite_expires_at"`
	InvitedByEmail  string     `json:"invited_by_email"`
	PromoNote       string     `json:"promo_note"`
	TenantID        *int64     `json:"tenant_id,omitempty"`
	TenantName      *string    `json:"tenant_name,omitempty"`
	CompExpiresAt   *string    `json:"comp_expires_at,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	RegisteredAt    *time.Time `json:"registered_at,omitempty"`
}

// ListInvites returns comp invites newest-first, optionally filtered by status.
// onlyOperatorID > 0 scopes the result to invites a single operator sent — used
// so CMX admins see only their own invites while Global admins (onlyOperatorID
// == 0) see every operator's.
func (s *Service) ListInvites(ctx context.Context, status string, limit int, onlyOperatorID int64) ([]InviteRow, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	// Comp invites are signup_intents flagged is_comp (unified registration with
	// the paid funnel). Map the intent lifecycle onto the comp vocabulary the UI
	// expects: email_verified=invited, completed=registered, cancelled=revoked.
	rows, err := s.db.QueryContext(ctx, `
		SELECT si.id, si.email, si.plan_code, COALESCE(p.name, si.plan_code),
		       si.comp_free_term_days,
		       CASE si.status
		         WHEN 'completed' THEN 'registered'
		         WHEN 'cancelled' THEN 'revoked'
		         WHEN 'expired'   THEN 'expired'
		         ELSE 'invited'
		       END,
		       si.expires_at, si.comp_invited_by_email,
		       ''::text, si.comp_tenant_id, t.name,
		       to_char(t.comp_expires_at, 'YYYY-MM-DD"T"HH24:MI:SSOF'),
		       si.created_at, si.completed_at
		  FROM signup_intents si
		  LEFT JOIN plans p ON p.code = si.plan_code
		  LEFT JOIN tenants t ON t.id = si.comp_tenant_id
		 WHERE si.is_comp = TRUE
		   AND ($1 = '' OR si.status = $1)
		   AND ($3 = 0 OR si.comp_invited_by_operator_id = $3)
		 ORDER BY si.created_at DESC
		 LIMIT $2`, status, limit, onlyOperatorID)
	if err != nil {
		return nil, fmt.Errorf("comp: list invites: %w", err)
	}
	defer rows.Close()
	var out []InviteRow
	for rows.Next() {
		var r InviteRow
		var tenantID sql.NullInt64
		var tenantName, compExpires sql.NullString
		var registeredAt sql.NullTime
		if err := rows.Scan(&r.ID, &r.Email, &r.PlanCode, &r.PlanName, &r.FreeTermDays,
			&r.Status, &r.InviteExpiresAt, &r.InvitedByEmail, &r.PromoNote,
			&tenantID, &tenantName, &compExpires, &r.CreatedAt, &registeredAt); err != nil {
			return nil, fmt.Errorf("comp: scan invite: %w", err)
		}
		if tenantID.Valid {
			r.TenantID = &tenantID.Int64
		}
		if tenantName.Valid {
			r.TenantName = &tenantName.String
		}
		if compExpires.Valid {
			r.CompExpiresAt = &compExpires.String
		}
		if registeredAt.Valid {
			r.RegisteredAt = &registeredAt.Time
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
