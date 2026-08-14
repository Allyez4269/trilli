// Package comp implements complimentary ("comp") / ambassador accounts
// (SPEC §6.10): an operator sends an email invite that bypasses payment and
// routes the recipient to a registration page, which provisions a FREE tenant
// on the granted plan (billing_mode='comp', no Stripe). The comp term expires
// at tenants.comp_expires_at, after which the lifecycle engine drops the tenant
// to read-only grace.
package comp

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"trilli/system/auth"
	"trilli/system/database/postgres"
	"trilli/system/logging"
)

const packageName = "comp"

var (
	ErrNotFound  = errors.New("comp: invite not found")
	ErrNotUsable = errors.New("comp: invite is expired, revoked, or already used")
)

// Service manages comp invites + comp-tenant provisioning.
type Service struct {
	client *postgres.Client
	auth   *auth.Service
}

// NewService constructs the comp Service. auth provides the shared
// account-provisioning primitive (SignupFromIntent).
func NewService(c *postgres.Client, a *auth.Service) *Service {
	return &Service{client: c, auth: a}
}

// Invite is a comp invitation record.
type Invite struct {
	ID                  int64      `json:"id"`
	Token               string     `json:"token"`
	Email               string     `json:"email"`
	PlanCode            string     `json:"plan_code"`
	FreeTermDays        int        `json:"free_term_days"`
	Status              string     `json:"status"`
	InviteExpiresAt     time.Time  `json:"invite_expires_at"`
	InvitedByOperatorID int64      `json:"invited_by_operator_id"`
	InvitedByEmail      string     `json:"invited_by_email"`
	PromoNote           string     `json:"promo_note"`
	TenantID            *int64     `json:"tenant_id,omitempty"`
	CreatedAt           time.Time  `json:"created_at"`
	RegisteredAt        *time.Time `json:"registered_at,omitempty"`
}

func newToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// CreateInput is the data an operator supplies to send a comp invite.
type CreateInput struct {
	Email              string
	PlanCode           string
	FreeTermDays       int
	PromoNote          string
	OperatorID         int64
	OperatorEmail      string
	InviteValidForDays int // how long the acceptance link stays valid
}

// Create stores a new comp invite and returns it (the caller emails the link).
func (s *Service) Create(ctx context.Context, in CreateInput) (*Invite, error) {
	in.Email = strings.TrimSpace(strings.ToLower(in.Email))
	if in.Email == "" || in.PlanCode == "" {
		return nil, fmt.Errorf("comp: email and plan_code required")
	}
	if in.FreeTermDays < 0 {
		return nil, fmt.Errorf("comp: free_term_days must be zero (never expires) or positive")
	}
	if in.InviteValidForDays <= 0 {
		in.InviteValidForDays = 14
	}
	tok, err := newToken()
	if err != nil {
		return nil, err
	}
	inviteExpires := time.Now().Add(time.Duration(in.InviteValidForDays) * 24 * time.Hour)
	var inv Invite
	err = s.client.QueryRowContext(ctx, `
		INSERT INTO comp_invites
			(token, email, plan_code, free_term_days, invite_expires_at,
			 invited_by_operator_id, invited_by_email, promo_note)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		RETURNING id, token, email, plan_code, free_term_days, status, invite_expires_at,
		          invited_by_operator_id, invited_by_email, promo_note, created_at`,
		tok, in.Email, in.PlanCode, in.FreeTermDays, inviteExpires,
		in.OperatorID, in.OperatorEmail, in.PromoNote,
	).Scan(&inv.ID, &inv.Token, &inv.Email, &inv.PlanCode, &inv.FreeTermDays, &inv.Status,
		&inv.InviteExpiresAt, &inv.InvitedByOperatorID, &inv.InvitedByEmail, &inv.PromoNote, &inv.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("comp: create invite: %w", err)
	}
	logging.Info(packageName, "comp invite created for %s (plan=%s, %dd) by operator %d",
		in.Email, in.PlanCode, in.FreeTermDays, in.OperatorID)
	return &inv, nil
}

// GetByToken loads a usable invite for the registration page. Marks an invite
// 'expired' (and refuses) when its acceptance window has passed.
func (s *Service) GetByToken(ctx context.Context, token string) (*Invite, error) {
	var inv Invite
	var tenantID sql.NullInt64
	var registeredAt sql.NullTime
	err := s.client.QueryRowContext(ctx, `
		SELECT id, token, email, plan_code, free_term_days, status, invite_expires_at,
		       invited_by_operator_id, invited_by_email, promo_note, tenant_id, created_at, registered_at
		  FROM comp_invites WHERE token = $1`, strings.TrimSpace(token),
	).Scan(&inv.ID, &inv.Token, &inv.Email, &inv.PlanCode, &inv.FreeTermDays, &inv.Status,
		&inv.InviteExpiresAt, &inv.InvitedByOperatorID, &inv.InvitedByEmail, &inv.PromoNote,
		&tenantID, &inv.CreatedAt, &registeredAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("comp: get invite: %w", err)
	}
	if tenantID.Valid {
		inv.TenantID = &tenantID.Int64
	}
	if registeredAt.Valid {
		inv.RegisteredAt = &registeredAt.Time
	}
	if inv.Status != "invited" {
		return nil, ErrNotUsable
	}
	if time.Now().After(inv.InviteExpiresAt) {
		_, _ = s.client.ExecContext(ctx, `UPDATE comp_invites SET status='expired' WHERE id=$1 AND status='invited'`, inv.ID)
		return nil, ErrNotUsable
	}
	return &inv, nil
}

// RegisterInput is the registration form for accepting a comp invite.
type RegisterInput struct {
	Token     string
	FirstName string
	LastName  string
	Password  string
	IP        string
	UserAgent string
}

// Register provisions the comp tenant: it reuses the shared signup primitive
// (no Stripe), flags the tenant billing_mode='comp' with a comp_expires_at term,
// and marks the invite registered. Returns the logged-in identity.
func (s *Service) Register(ctx context.Context, in RegisterInput) (*auth.Identity, error) {
	inv, err := s.GetByToken(ctx, in.Token)
	if err != nil {
		return nil, err
	}
	first := strings.TrimSpace(in.FirstName)
	tenantName := "My Trilli"
	if first != "" {
		tenantName = first + "'s Trilli"
	}
	identity, err := s.auth.SignupFromIntent(ctx, auth.SignupFromIntentInput{
		Email:         inv.Email,
		Password:      in.Password,
		FirstName:     in.FirstName,
		LastName:      in.LastName,
		TenantName:    tenantName,
		PlanCode:      inv.PlanCode,
		BillingPeriod: "monthly",
		// No Stripe customer/subscription — this is a comp tenant.
	}, in.IP, in.UserAgent)
	if err != nil {
		return nil, err
	}

	// FreeTermDays == 0 means an indefinite comp term: store a NULL
	// comp_expires_at, which the lifecycle sweep never lapses
	// (it only acts on comp_expires_at IS NOT NULL AND < NOW()).
	var compExpires sql.NullTime
	if inv.FreeTermDays > 0 {
		compExpires = sql.NullTime{Time: time.Now().Add(time.Duration(inv.FreeTermDays) * 24 * time.Hour), Valid: true}
	}
	if _, err := s.client.ExecContext(ctx,
		`UPDATE tenants SET billing_mode='comp', comp_expires_at=$2, updated_at=NOW() WHERE id=$1`,
		identity.Tenant.ID, compExpires,
	); err != nil {
		return nil, fmt.Errorf("comp: flag tenant comp: %w", err)
	}
	if _, err := s.client.ExecContext(ctx,
		`UPDATE comp_invites SET status='registered', tenant_id=$2, registered_at=NOW() WHERE id=$1`,
		inv.ID, identity.Tenant.ID,
	); err != nil {
		return nil, fmt.Errorf("comp: mark registered: %w", err)
	}
	term := "indefinite"
	if compExpires.Valid {
		term = compExpires.Time.Format(time.RFC3339)
	}
	logging.Info(packageName, "comp tenant %d provisioned from invite %d (expires %s)",
		identity.Tenant.ID, inv.ID, term)
	return identity, nil
}

// PlanName returns a plan's display name by code (for the invite email), or the
// code itself if not found.
func (s *Service) PlanName(ctx context.Context, code string) string {
	var name string
	if err := s.client.QueryRowContext(ctx, `SELECT name FROM plans WHERE code=$1`, code).Scan(&name); err != nil || name == "" {
		return code
	}
	return name
}

// Revoke cancels an unredeemed invite.
func (s *Service) Revoke(ctx context.Context, id int64) error {
	res, err := s.client.ExecContext(ctx,
		`UPDATE comp_invites SET status='revoked', revoked_at=NOW() WHERE id=$1 AND status='invited'`, id)
	if err != nil {
		return fmt.Errorf("comp: revoke: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
