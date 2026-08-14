package billing

// adminops.go holds the operator-initiated money-write operations exposed to
// Trilli CMX through the app's /api/admin surface (SPEC §6.4): issue an
// arbitrary refund, grant account credit (first-class ledger), and reconcile a
// stale Stripe subscription. They live in the billing package so they reuse the
// configured Stripe client, the DB pool, and the enabled flag. Attribution
// (which operator did this, and why) is carried by the caller and recorded in
// CMX's operator audit; the negative ledger row / credit row is the app-side
// record.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/stripe/stripe-go/v82"
	"github.com/stripe/stripe-go/v82/customer"
	"github.com/stripe/stripe-go/v82/customerbalancetransaction"
	"github.com/stripe/stripe-go/v82/invoice"
	"github.com/stripe/stripe-go/v82/refund"
	"github.com/stripe/stripe-go/v82/subscription"

	"trilli/system/logging"
)

// resolveRefundParams turns a stored transaction anchor into Stripe refund
// params. PaymentIntents (pi_) and charges (ch_) refund directly; subscription
// invoices (in_, what RecordInvoiceOrder stores) are resolved to their
// underlying payment_intent / charge — refund.New rejects a raw invoice id.
func resolveRefundParams(anchorPI, chargeID string) (*stripe.RefundParams, error) {
	rp := &stripe.RefundParams{}
	switch {
	case chargeID != "":
		rp.Charge = stripe.String(chargeID)
	case strings.HasPrefix(anchorPI, "pi_"):
		rp.PaymentIntent = stripe.String(anchorPI)
	case strings.HasPrefix(anchorPI, "ch_"):
		rp.Charge = stripe.String(anchorPI)
	case strings.HasPrefix(anchorPI, "in_"):
		ip := &stripe.InvoiceParams{}
		ip.AddExpand("payments.data.payment.payment_intent")
		inv, err := invoice.Get(anchorPI, ip)
		if err != nil {
			return nil, fmt.Errorf("billing: resolve invoice %s: %w", anchorPI, err)
		}
		if inv.Payments != nil {
			for _, p := range inv.Payments.Data {
				if p.Payment == nil {
					continue
				}
				if p.Payment.PaymentIntent != nil && p.Payment.PaymentIntent.ID != "" {
					rp.PaymentIntent = stripe.String(p.Payment.PaymentIntent.ID)
					return rp, nil
				}
				if p.Payment.Charge != nil && p.Payment.Charge.ID != "" {
					rp.Charge = stripe.String(p.Payment.Charge.ID)
					return rp, nil
				}
			}
		}
		return nil, ErrNotRefundable
	default:
		return nil, ErrNotRefundable
	}
	return rp, nil
}

var (
	// ErrTransactionNotFound is returned when the target charge doesn't exist,
	// isn't a positive succeeded charge, or belongs to another tenant.
	ErrTransactionNotFound = errors.New("billing: transaction not found")
	// ErrNotRefundable is returned when a charge has nothing left to refund.
	ErrNotRefundable = errors.New("billing: charge has nothing left to refund")
)

// RefundResult reports the outcome of an admin refund.
type RefundResult struct {
	RefundedCents int64  `json:"refunded_cents"`
	RefundID      string `json:"refund_id"`
}

// AdminRefund refunds one succeeded billing transaction. amountCents <= 0 (or
// greater than the remaining refundable balance) refunds the full remaining
// amount; otherwise it's a partial refund. It records a NEGATIVE 'refunded'
// ledger row mirroring CloseBilling so the invoices view and refund totals stay
// consistent. The transaction must belong to the tenant and be a positive
// 'succeeded' charge.
func (s *Service) AdminRefund(ctx context.Context, tenantID, transactionID, amountCents int64) (RefundResult, error) {
	if !s.enabled {
		return RefundResult{}, ErrNotConfigured
	}
	var (
		pi, ch, cur, plan, period string
		gross                     int64
	)
	err := s.client.QueryRowContext(ctx,
		`SELECT COALESCE(stripe_payment_intent_id,''), COALESCE(stripe_charge_id,''),
		        amount_cents, COALESCE(currency,'usd'), COALESCE(plan_code,''), COALESCE(billing_period,'')
		   FROM billing_transactions
		  WHERE id = $1 AND tenant_id = $2 AND status = 'succeeded' AND amount_cents > 0`,
		transactionID, tenantID).Scan(&pi, &ch, &gross, &cur, &plan, &period)
	if errors.Is(err, sql.ErrNoRows) {
		return RefundResult{}, ErrTransactionNotFound
	}
	if err != nil {
		return RefundResult{}, fmt.Errorf("billing: load transaction: %w", err)
	}
	if pi == "" && ch == "" {
		return RefundResult{}, ErrNotRefundable
	}
	// anchor is the original charge's stable identity (its payment-intent /
	// invoice / charge id). Refund rows tag stripe_charge_id with it so partial
	// refunds across multiple operator actions sum correctly.
	anchor := pi
	if anchor == "" {
		anchor = ch
	}

	// Subtract refunds already recorded against this same charge so partial
	// refunds can't exceed the original amount across multiple operator actions.
	var alreadyRefunded int64
	_ = s.client.QueryRowContext(ctx,
		`SELECT COALESCE(-SUM(amount_cents),0) FROM billing_transactions
		  WHERE tenant_id = $1 AND status = 'refunded' AND stripe_charge_id = $2`,
		tenantID, anchor).Scan(&alreadyRefunded)
	refundable := gross - alreadyRefunded
	if refundable <= 0 {
		return RefundResult{}, ErrNotRefundable
	}
	amt := amountCents
	if amt <= 0 || amt > refundable {
		amt = refundable
	}

	rp, err := resolveRefundParams(pi, ch)
	if err != nil {
		return RefundResult{}, err
	}
	rp.Amount = stripe.Int64(amt)
	rf, err := refund.New(rp)
	if err != nil {
		return RefundResult{}, fmt.Errorf("billing: stripe refund: %w", err)
	}
	// Record the refund as its own ledger row. stripe_payment_intent_id is
	// NOT NULL + UNIQUE, so we anchor the row on the (unique) Stripe refund id
	// rather than re-using the original charge's id, and keep the original
	// anchor in stripe_charge_id to tie partial refunds back to their charge.
	if _, err := s.client.ExecContext(ctx,
		`INSERT INTO billing_transactions
		   (tenant_id, plan_code, billing_period, amount_cents, currency, stripe_payment_intent_id, stripe_charge_id, status)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,'refunded')`,
		tenantID, plan, period, -rf.Amount, cur, rf.ID, anchor); err != nil {
		// The money already moved at Stripe; a failed ledger write must not look
		// like a failed refund. Log loudly and report success.
		logging.Error(packageName, "AdminRefund: record refund tenant=%d txn=%d failed: %v", tenantID, transactionID, err)
	}
	logging.Info(packageName, "AdminRefund: tenant=%d txn=%d refunded_cents=%d refund=%s", tenantID, transactionID, rf.Amount, rf.ID)
	return RefundResult{RefundedCents: rf.Amount, RefundID: rf.ID}, nil
}

// CreditGrant is one row of the account-credit ledger.
type CreditGrant struct {
	ID             int64     `json:"id"`
	AmountCents    int64     `json:"amount_cents"`
	Currency       string    `json:"currency"`
	Reason         string    `json:"reason"`
	GrantedByEmail string    `json:"granted_by_email"`
	CreatedAt      time.Time `json:"created_at"`
}

// GrantCredit grants account credit to a tenant. It ensures a Stripe customer,
// posts a NEGATIVE customer-balance transaction (Stripe automatically consumes
// it against the next invoice — "applied at billing time"), and records a
// positive grant row in account_credits for the audit/display ledger.
func (s *Service) GrantCredit(ctx context.Context, tenantID, amountCents int64, reason, operatorID, operatorEmail string) (CreditGrant, error) {
	if !s.enabled {
		return CreditGrant{}, ErrNotConfigured
	}
	if amountCents <= 0 {
		return CreditGrant{}, fmt.Errorf("billing: credit amount must be > 0")
	}

	var tenantName string
	var ownerEmail sql.NullString
	if err := s.client.QueryRowContext(ctx,
		`SELECT t.name,
		        (SELECT u.email FROM tenant_members m JOIN users u ON u.id = m.user_id
		          WHERE m.tenant_id = t.id AND m.role = 'owner' ORDER BY m.user_id ASC LIMIT 1)
		   FROM tenants t WHERE t.id = $1`, tenantID).Scan(&tenantName, &ownerEmail); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return CreditGrant{}, fmt.Errorf("billing: tenant %d not found", tenantID)
		}
		return CreditGrant{}, fmt.Errorf("billing: load tenant: %w", err)
	}

	custID, err := s.EnsureCustomer(ctx, tenantID, ownerEmail.String, tenantName)
	if err != nil {
		return CreditGrant{}, err
	}

	const currency = "usd"
	cbt, err := customerbalancetransaction.New(&stripe.CustomerBalanceTransactionParams{
		Customer:    stripe.String(custID),
		Amount:      stripe.Int64(-amountCents), // negative = credit on the customer
		Currency:    stripe.String(currency),
		Description: stripe.String(reason),
	})
	if err != nil {
		return CreditGrant{}, fmt.Errorf("billing: stripe customer credit: %w", err)
	}

	var g CreditGrant
	g.AmountCents = amountCents
	g.Currency = currency
	g.Reason = reason
	g.GrantedByEmail = operatorEmail
	if err := s.client.QueryRowContext(ctx,
		`INSERT INTO account_credits
		   (tenant_id, amount_cents, currency, reason, stripe_balance_txn_id, granted_by_operator_id, granted_by_email)
		 VALUES ($1,$2,$3,$4,$5,$6,$7)
		 RETURNING id, created_at`,
		tenantID, amountCents, currency, reason, cbt.ID, operatorID, operatorEmail).Scan(&g.ID, &g.CreatedAt); err != nil {
		// Credit is live at Stripe; a failed ledger insert must not double-grant on
		// retry. Log and return success without the row id.
		logging.Error(packageName, "GrantCredit: record grant tenant=%d failed (stripe txn=%s): %v", tenantID, cbt.ID, err)
	}
	logging.Info(packageName, "GrantCredit: tenant=%d amount_cents=%d operator=%q stripe_txn=%s", tenantID, amountCents, operatorEmail, cbt.ID)
	return g, nil
}

// ListCredits returns the account-credit grant ledger for a tenant, newest
// first. Read-only — safe even when billing is disabled.
func (s *Service) ListCredits(ctx context.Context, tenantID int64) ([]CreditGrant, error) {
	rows, err := s.client.QueryContext(ctx,
		`SELECT id, amount_cents, currency, reason, granted_by_email, created_at
		   FROM account_credits WHERE tenant_id = $1 ORDER BY created_at DESC`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("billing: list credits: %w", err)
	}
	defer rows.Close()
	var out []CreditGrant
	for rows.Next() {
		var g CreditGrant
		if err := rows.Scan(&g.ID, &g.AmountCents, &g.Currency, &g.Reason, &g.GrantedByEmail, &g.CreatedAt); err != nil {
			return nil, fmt.Errorf("billing: scan credit: %w", err)
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// CreditBalanceCents returns the tenant's LIVE available credit (the magnitude
// of a negative Stripe customer balance). 0 when billing is off, the tenant has
// no customer, or the balance is non-negative.
func (s *Service) CreditBalanceCents(ctx context.Context, tenantID int64) (int64, error) {
	if !s.enabled {
		return 0, nil
	}
	var custID sql.NullString
	if err := s.client.QueryRowContext(ctx,
		`SELECT stripe_customer_id FROM tenants WHERE id = $1`, tenantID).Scan(&custID); err != nil {
		return 0, fmt.Errorf("billing: load customer: %w", err)
	}
	if !custID.Valid || custID.String == "" {
		return 0, nil
	}
	c, err := customer.Get(custID.String, nil)
	if err != nil || c.Deleted {
		return 0, nil
	}
	if c.Balance < 0 {
		return -c.Balance, nil
	}
	return 0, nil
}

// ReconcileResult reports the freshly-pulled subscription state.
type ReconcileResult struct {
	Status           string     `json:"subscription_status"`
	AutoRenew        bool       `json:"auto_renew"`
	CurrentPeriodEnd *time.Time `json:"current_period_end,omitempty"`
}

// ReconcileSubscription force-pulls the tenant's subscription from Stripe and
// rewrites the local subscription_status / current_period_end / auto_renew —
// the manual counterpart of the customer.subscription.updated webhook, for when
// a webhook was missed and local state drifted.
func (s *Service) ReconcileSubscription(ctx context.Context, tenantID int64) (ReconcileResult, error) {
	if !s.enabled {
		return ReconcileResult{}, ErrNotConfigured
	}
	var subID sql.NullString
	if err := s.client.QueryRowContext(ctx,
		`SELECT stripe_subscription_id FROM tenants WHERE id = $1`, tenantID).Scan(&subID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ReconcileResult{}, ErrNoSubscription
		}
		return ReconcileResult{}, fmt.Errorf("billing: load subscription: %w", err)
	}
	if !subID.Valid || subID.String == "" {
		return ReconcileResult{}, ErrNoSubscription
	}
	sub, err := subscription.Get(subID.String, nil)
	if err != nil {
		return ReconcileResult{}, fmt.Errorf("billing: stripe get subscription: %w", err)
	}
	status := string(sub.Status)
	var periodEnd time.Time
	if sub.Items != nil && len(sub.Items.Data) > 0 && sub.Items.Data[0].CurrentPeriodEnd > 0 {
		periodEnd = time.Unix(sub.Items.Data[0].CurrentPeriodEnd, 0)
	}
	autoRenew := !sub.CancelAtPeriodEnd
	if err := s.StoreSubscriptionState(ctx, tenantID, sub.ID, status, periodEnd, autoRenew); err != nil {
		return ReconcileResult{}, err
	}
	logging.Info(packageName, "ReconcileSubscription: tenant=%d sub=%s status=%s auto_renew=%t", tenantID, sub.ID, status, autoRenew)
	res := ReconcileResult{Status: status, AutoRenew: autoRenew}
	if !periodEnd.IsZero() {
		pe := periodEnd.UTC()
		res.CurrentPeriodEnd = &pe
	}
	return res, nil
}
