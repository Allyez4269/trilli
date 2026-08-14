// Package signupintents backs the pay-before-account registration funnel. It
// holds the pre-account state (email, chosen plan/cycle, verification + Stripe
// progress) in the signup_intents table so that NO users/tenants row exists
// until payment is secured. Abandoned intents expire and free the email.
//
// Lifecycle: pending_email -> email_verified -> paid -> completed.
package signupintents

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"trilli/system/database/postgres"
	"trilli/system/logging"
)

const packageName = "signupintents"

// Status values (also enforced by a CHECK constraint on the table).
const (
	StatusPendingEmail  = "pending_email"
	StatusEmailVerified = "email_verified"
	StatusPaid          = "paid"
	StatusCompleted     = "completed"
	StatusCancelled     = "cancelled"
	StatusExpired       = "expired"
)

var (
	// ErrNotFound is returned when a token/session resolves to no live intent.
	ErrNotFound = errors.New("signupintents: not found")
	// ErrEmailRegistered means a real account already owns this email — the
	// caller should route the visitor to /login instead of starting a funnel.
	ErrEmailRegistered = errors.New("signupintents: email already registered")
)

// Intent is one row of signup_intents.
type Intent struct {
	ID                      int64
	Token                   string
	Email                   string
	AccountType             string
	PlanCode                string
	BillingCycle            string
	Status                  string
	OAuthProvider           string
	OAuthSubject            string
	FullName                string
	StripeCustomerID        string
	StripeCheckoutSessionID string
	StripeSubscriptionID    string
	CreatedAt               time.Time
	ExpiresAt               time.Time
	VerifiedAt              sql.NullTime
	PaidAt                  sql.NullTime
	CompletedAt             sql.NullTime
	// UserID is set only for an AUTHENTICATED "buy a second account" intent:
	// completing it provisions a new tenant owned by this existing user instead
	// of creating a brand-new user. Null for the normal cold-signup funnel.
	UserID sql.NullInt64
	// Comp ("ambassador") fields. IsComp marks a payment-bypass invite that
	// shares the registration page with paid sign-ups; CompFreeTermDays is the
	// granted free term (0 = never expires); the InvitedBy* fields drive the CMX
	// registry; CompTenantID records the provisioned tenant once completed.
	IsComp                  bool
	CompFreeTermDays        int
	CompInvitedByOperatorID sql.NullInt64
	CompInvitedByEmail      string
	CompTenantID            sql.NullInt64
}

// Service reads and writes signup intents.
type Service struct {
	client *postgres.Client
}

// NewService returns a wired Service.
func NewService(c *postgres.Client) *Service {
	return &Service{client: c}
}

func newToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("signupintents: token: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

func normalizeCycle(c string) string {
	if strings.EqualFold(strings.TrimSpace(c), "monthly") {
		return "monthly"
	}
	return "annual"
}

// emailTaken reports whether a real (provisioned) account already owns email.
func (s *Service) emailTaken(ctx context.Context, email string) (bool, error) {
	var one int
	err := s.client.QueryRowContext(ctx,
		`SELECT 1 FROM users WHERE lower(email) = lower($1) LIMIT 1`, email).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("signupintents: email check: %w", err)
	}
	return true, nil
}

// Create starts an email-path intent: it supersedes any live (pre-paid) intent
// for the same email, then inserts a fresh pending_email row with a new token.
// Returns ErrEmailRegistered if a real account already owns the email.
func (s *Service) Create(ctx context.Context, email, planCode, cycle string) (*Intent, error) {
	email = strings.TrimSpace(email)
	if taken, err := s.emailTaken(ctx, email); err != nil {
		return nil, err
	} else if taken {
		return nil, ErrEmailRegistered
	}
	// Supersede any outstanding live intent so the partial-unique index can't
	// collide and a re-attempt always gets a clean token.
	if _, err := s.client.ExecContext(ctx,
		`UPDATE signup_intents SET status = $1, updated_at = NOW()
		   WHERE lower(email) = lower($2) AND status IN ($3, $4)`,
		StatusCancelled, email, StatusPendingEmail, StatusEmailVerified); err != nil {
		return nil, fmt.Errorf("signupintents: supersede: %w", err)
	}
	tok, err := newToken()
	if err != nil {
		return nil, err
	}
	return scanIntent(s.client.QueryRowContext(ctx, `
		INSERT INTO signup_intents (token, email, plan_code, billing_cycle, status)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING `+selectCols,
		tok, email, planCode, normalizeCycle(cycle), StatusPendingEmail))
}

// CreateComp starts a complimentary ("ambassador") invite as a signup_intent so
// comp recipients use the SAME registration page as paid sign-ups. The intent is
// created pre-verified in email_verified status (no email-confirmation, no
// payment); completing it provisions a FREE tenant (no Stripe) on a comp term of
// freeTermDays (0 = never expires). The link lapses after inviteValidDays
// (ExpireStale expires an un-accepted email_verified comp intent). Returns
// ErrEmailRegistered if a real account already owns the email.
func (s *Service) CreateComp(ctx context.Context, email, planCode string, freeTermDays, inviteValidDays int, operatorID int64, operatorEmail string) (*Intent, error) {
	email = strings.TrimSpace(email)
	if email == "" || planCode == "" {
		return nil, fmt.Errorf("signupintents: email and plan required")
	}
	if freeTermDays < 0 {
		return nil, fmt.Errorf("signupintents: free term must be zero (never expires) or positive")
	}
	if inviteValidDays <= 0 {
		inviteValidDays = 14
	}
	if taken, err := s.emailTaken(ctx, email); err != nil {
		return nil, err
	} else if taken {
		return nil, ErrEmailRegistered
	}
	if _, err := s.client.ExecContext(ctx,
		`UPDATE signup_intents SET status = $1, updated_at = NOW()
		   WHERE lower(email) = lower($2) AND status IN ($3, $4)`,
		StatusCancelled, email, StatusPendingEmail, StatusEmailVerified); err != nil {
		return nil, fmt.Errorf("signupintents: supersede: %w", err)
	}
	tok, err := newToken()
	if err != nil {
		return nil, err
	}
	return scanIntent(s.client.QueryRowContext(ctx, `
		INSERT INTO signup_intents
		    (token, email, plan_code, billing_cycle, status, verified_at, expires_at,
		     is_comp, comp_free_term_days, comp_invited_by_operator_id, comp_invited_by_email)
		VALUES ($1, $2, $3, 'monthly', $4, NOW(), NOW() + make_interval(days => $5),
		        TRUE, $6, $7, $8)
		RETURNING `+selectCols,
		tok, email, planCode, StatusEmailVerified, inviteValidDays,
		freeTermDays, operatorID, strings.TrimSpace(operatorEmail)))
}

// Cancel marks a non-completed intent cancelled — the operator-side comp "revoke".
// Returns the number of rows affected (0 if already completed/absent).
func (s *Service) Cancel(ctx context.Context, id int64) (int64, error) {
	res, err := s.client.ExecContext(ctx,
		`UPDATE signup_intents SET status = $1, updated_at = NOW()
		   WHERE id = $2 AND status <> $3`, StatusCancelled, id, StatusCompleted)
	if err != nil {
		return 0, fmt.Errorf("signupintents: cancel: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// DeleteComp permanently removes a comp invite (signup_intent) from the registry.
// Restricted to comp rows so a paid funnel intent can never be deleted here.
func (s *Service) DeleteComp(ctx context.Context, id int64) (int64, error) {
	res, err := s.client.ExecContext(ctx, `DELETE FROM signup_intents WHERE id = $1 AND is_comp`, id)
	if err != nil {
		return 0, fmt.Errorf("signupintents: delete comp: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// MarkTenantComp flags a freshly provisioned tenant as a comp account with the
// granted term. freeTermDays == 0 stores a NULL comp_expires_at (never lapses;
// the lifecycle sweep only acts on comp_expires_at IS NOT NULL AND < NOW()).
func (s *Service) MarkTenantComp(ctx context.Context, tenantID int64, freeTermDays int) error {
	var expires sql.NullTime
	if freeTermDays > 0 {
		expires = sql.NullTime{Time: time.Now().Add(time.Duration(freeTermDays) * 24 * time.Hour), Valid: true}
	}
	if _, err := s.client.ExecContext(ctx,
		`UPDATE tenants SET billing_mode='comp', comp_expires_at=$2, updated_at=NOW() WHERE id=$1`,
		tenantID, expires); err != nil {
		return fmt.Errorf("signupintents: mark tenant comp: %w", err)
	}
	return nil
}

// SetCompTenant records the tenant a comp invite provisioned (for the CMX registry).
func (s *Service) SetCompTenant(ctx context.Context, intentID, tenantID int64) error {
	_, err := s.client.ExecContext(ctx,
		`UPDATE signup_intents SET comp_tenant_id = $2, updated_at = NOW() WHERE id = $1`, intentID, tenantID)
	return err
}

// CreateOAuth starts a funnel intent for a provider sign-up (Google/Microsoft).
// The provider has already verified the email, so the intent is created
// directly in email_verified status — skipping the email-confirmation step —
// with the provider identity + display name recorded. Supersedes any live
// intent for the same email. Returns ErrEmailRegistered if a real account
// already owns the email.
func (s *Service) CreateOAuth(ctx context.Context, email, planCode, cycle, provider, subject, fullName string) (*Intent, error) {
	email = strings.TrimSpace(email)
	if taken, err := s.emailTaken(ctx, email); err != nil {
		return nil, err
	} else if taken {
		return nil, ErrEmailRegistered
	}
	if _, err := s.client.ExecContext(ctx,
		`UPDATE signup_intents SET status = $1, updated_at = NOW()
		   WHERE lower(email) = lower($2) AND status IN ($3, $4)`,
		StatusCancelled, email, StatusPendingEmail, StatusEmailVerified); err != nil {
		return nil, fmt.Errorf("signupintents: supersede: %w", err)
	}
	tok, err := newToken()
	if err != nil {
		return nil, err
	}
	return scanIntent(s.client.QueryRowContext(ctx, `
		INSERT INTO signup_intents
		    (token, email, plan_code, billing_cycle, status,
		     oauth_provider, oauth_subject, full_name, verified_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, NOW())
		RETURNING `+selectCols,
		tok, email, planCode, normalizeCycle(cycle), StatusEmailVerified,
		provider, subject, strings.TrimSpace(fullName)))
}

// CreateForUser starts an AUTHENTICATED "buy a second account" intent for an
// existing, signed-in user. Identity is already proven, so it skips the
// email-taken guard (the user owns this email — that's the point) and the
// email-confirmation step: the intent goes straight to email_verified with
// user_id set, so completing it provisions a NEW tenant owned by this user.
func (s *Service) CreateForUser(ctx context.Context, userID int64, email, planCode, cycle string) (*Intent, error) {
	email = strings.TrimSpace(email)
	// Supersede any outstanding live intent for this email so the partial-unique
	// index can't collide (e.g. an abandoned attempt).
	if _, err := s.client.ExecContext(ctx,
		`UPDATE signup_intents SET status = $1, updated_at = NOW()
		   WHERE lower(email) = lower($2) AND status IN ($3, $4)`,
		StatusCancelled, email, StatusPendingEmail, StatusEmailVerified); err != nil {
		return nil, fmt.Errorf("signupintents: supersede: %w", err)
	}
	tok, err := newToken()
	if err != nil {
		return nil, err
	}
	return scanIntent(s.client.QueryRowContext(ctx, `
		INSERT INTO signup_intents
		    (token, email, plan_code, billing_cycle, status, user_id, verified_at)
		VALUES ($1, $2, $3, $4, $5, $6, NOW())
		RETURNING `+selectCols,
		tok, email, planCode, normalizeCycle(cycle), StatusEmailVerified, userID))
}

// scan reads a full intent row from a query that selects the canonical columns.
func scanIntent(row interface{ Scan(...any) error }) (*Intent, error) {
	var in Intent
	err := row.Scan(&in.ID, &in.Token, &in.Email, &in.AccountType, &in.PlanCode, &in.BillingCycle,
		&in.Status, &in.OAuthProvider, &in.OAuthSubject, &in.FullName, &in.StripeCustomerID,
		&in.StripeCheckoutSessionID, &in.StripeSubscriptionID,
		&in.CreatedAt, &in.ExpiresAt, &in.VerifiedAt, &in.PaidAt, &in.CompletedAt, &in.UserID,
		&in.IsComp, &in.CompFreeTermDays, &in.CompInvitedByOperatorID, &in.CompInvitedByEmail, &in.CompTenantID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("signupintents: scan: %w", err)
	}
	return &in, nil
}

const selectCols = `id, token, email, account_type, plan_code, billing_cycle, status,
	oauth_provider, oauth_subject, full_name, stripe_customer_id,
	stripe_checkout_session_id, stripe_subscription_id,
	created_at, expires_at, verified_at, paid_at, completed_at, user_id,
	is_comp, comp_free_term_days, comp_invited_by_operator_id, comp_invited_by_email, comp_tenant_id`

// GetByToken returns the intent for a token (any status).
func (s *Service) GetByToken(ctx context.Context, token string) (*Intent, error) {
	return scanIntent(s.client.QueryRowContext(ctx,
		`SELECT `+selectCols+` FROM signup_intents WHERE token = $1`, token))
}

// GetByCheckoutSession returns the intent tied to a Stripe Checkout session.
func (s *Service) GetByCheckoutSession(ctx context.Context, sessionID string) (*Intent, error) {
	return scanIntent(s.client.QueryRowContext(ctx,
		`SELECT `+selectCols+` FROM signup_intents WHERE stripe_checkout_session_id = $1`, sessionID))
}

// Verify consumes the email-confirmation token: pending_email -> email_verified.
// It is idempotent on repeat clicks (already-verified stays verified) and
// refuses an expired pending token.
func (s *Service) Verify(ctx context.Context, token string) (*Intent, error) {
	in, err := s.GetByToken(ctx, token)
	if err != nil {
		return nil, err
	}
	switch in.Status {
	case StatusEmailVerified, StatusPaid, StatusCompleted:
		return in, nil // already past verification
	case StatusPendingEmail:
		if time.Now().After(in.ExpiresAt) {
			_, _ = s.client.ExecContext(ctx,
				`UPDATE signup_intents SET status = $1, updated_at = NOW() WHERE id = $2`,
				StatusExpired, in.ID)
			return nil, ErrNotFound
		}
		if _, err := s.client.ExecContext(ctx,
			`UPDATE signup_intents SET status = $1, verified_at = NOW(), updated_at = NOW() WHERE id = $2`,
			StatusEmailVerified, in.ID); err != nil {
			return nil, fmt.Errorf("signupintents: verify: %w", err)
		}
		in.Status = StatusEmailVerified
		return in, nil
	default:
		return nil, ErrNotFound // cancelled/expired
	}
}

// SetCheckoutSession records the Stripe Checkout session + customer on the intent.
func (s *Service) SetCheckoutSession(ctx context.Context, id int64, sessionID, customerID string) error {
	if _, err := s.client.ExecContext(ctx,
		`UPDATE signup_intents
		    SET stripe_checkout_session_id = $1, stripe_customer_id = $2, updated_at = NOW()
		  WHERE id = $3`, sessionID, customerID, id); err != nil {
		return fmt.Errorf("signupintents: set checkout session: %w", err)
	}
	return nil
}

// MarkPaid is the authoritative payment transition, driven by the
// checkout.session.completed webhook. Idempotent.
func (s *Service) MarkPaid(ctx context.Context, sessionID, subscriptionID, customerID string) (*Intent, error) {
	in, err := s.GetByCheckoutSession(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if in.Status == StatusPaid || in.Status == StatusCompleted {
		return in, nil
	}
	if _, err := s.client.ExecContext(ctx, `
		UPDATE signup_intents
		   SET status = $1, stripe_subscription_id = $2, stripe_customer_id = $3,
		       paid_at = NOW(), updated_at = NOW()
		 WHERE id = $4`,
		StatusPaid, subscriptionID, customerID, in.ID); err != nil {
		return nil, fmt.Errorf("signupintents: mark paid: %w", err)
	}
	in.Status = StatusPaid
	in.StripeSubscriptionID = subscriptionID
	in.StripeCustomerID = customerID
	return in, nil
}

// SetStripeRefs stores the embedded-checkout Stripe customer + subscription on
// the intent (so a page reload reuses the same subscription).
func (s *Service) SetStripeRefs(ctx context.Context, id int64, customerID, subscriptionID string) error {
	if _, err := s.client.ExecContext(ctx,
		`UPDATE signup_intents
		    SET stripe_customer_id = $1, stripe_subscription_id = $2, updated_at = NOW()
		  WHERE id = $3`, customerID, subscriptionID, id); err != nil {
		return fmt.Errorf("signupintents: set stripe refs: %w", err)
	}
	return nil
}

// MarkPaidByToken is the embedded-checkout payment transition (synchronous
// finalize + subscription webhook both call it). Idempotent.
func (s *Service) MarkPaidByToken(ctx context.Context, token, subscriptionID, customerID string) (*Intent, error) {
	in, err := s.GetByToken(ctx, token)
	if err != nil {
		return nil, err
	}
	if in.Status == StatusPaid || in.Status == StatusCompleted {
		return in, nil
	}
	if _, err := s.client.ExecContext(ctx, `
		UPDATE signup_intents
		   SET status = $1,
		       stripe_subscription_id = COALESCE(NULLIF($2,''), stripe_subscription_id),
		       stripe_customer_id     = COALESCE(NULLIF($3,''), stripe_customer_id),
		       paid_at = NOW(), updated_at = NOW()
		 WHERE id = $4`,
		StatusPaid, subscriptionID, customerID, in.ID); err != nil {
		return nil, fmt.Errorf("signupintents: mark paid by token: %w", err)
	}
	in.Status = StatusPaid
	if subscriptionID != "" {
		in.StripeSubscriptionID = subscriptionID
	}
	if customerID != "" {
		in.StripeCustomerID = customerID
	}
	return in, nil
}

// MarkCompleted records that the account was provisioned from this intent.
func (s *Service) MarkCompleted(ctx context.Context, id int64) error {
	if _, err := s.client.ExecContext(ctx,
		`UPDATE signup_intents SET status = $1, completed_at = NOW(), updated_at = NOW() WHERE id = $2`,
		StatusCompleted, id); err != nil {
		return fmt.Errorf("signupintents: mark completed: %w", err)
	}
	return nil
}

// ExpireStale marks abandoned pre-paid intents expired so their email frees up.
// Run periodically. Paid/completed intents are never expired.
func (s *Service) ExpireStale(ctx context.Context) (int64, error) {
	res, err := s.client.ExecContext(ctx, `
		UPDATE signup_intents SET status = $1, updated_at = NOW()
		 WHERE status IN ($2, $3) AND expires_at < NOW()`,
		StatusExpired, StatusPendingEmail, StatusEmailVerified)
	if err != nil {
		return 0, fmt.Errorf("signupintents: expire stale: %w", err)
	}
	n, _ := res.RowsAffected()
	if n > 0 {
		logging.Debug(packageName, "expired %d stale signup intent(s)", n)
	}
	return n, nil
}

// ResumeCandidate is the minimal data the deferred "finish setting up" reminder
// needs to address one paid-but-incomplete intent.
type ResumeCandidate struct {
	ID    int64
	Token string
	Email string
}

// PaidAwaitingResume returns paid intents that have waited longer than `grace`
// without completing and haven't been reminded yet — the targets for the
// deferred resume email. The status='paid' filter excludes completed signups,
// so a prompt completer is never returned (the bug this replaces sent the email
// from the payment webhook, hitting everyone).
func (s *Service) PaidAwaitingResume(ctx context.Context, grace time.Duration) ([]ResumeCandidate, error) {
	rows, err := s.client.QueryContext(ctx, `
		SELECT id, token, email
		  FROM signup_intents
		 WHERE status = $1
		   AND resume_emailed_at IS NULL
		   AND paid_at IS NOT NULL
		   AND paid_at < NOW() - make_interval(secs => $2)`,
		StatusPaid, int(grace.Seconds()))
	if err != nil {
		return nil, fmt.Errorf("signupintents: paid awaiting resume: %w", err)
	}
	defer rows.Close()
	var out []ResumeCandidate
	for rows.Next() {
		var c ResumeCandidate
		if err := rows.Scan(&c.ID, &c.Token, &c.Email); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// MarkResumeEmailed records that the resume reminder has gone out for an intent
// so a later sweep won't mail it again.
func (s *Service) MarkResumeEmailed(ctx context.Context, id int64) error {
	_, err := s.client.ExecContext(ctx,
		`UPDATE signup_intents SET resume_emailed_at = NOW() WHERE id = $1`, id)
	return err
}
