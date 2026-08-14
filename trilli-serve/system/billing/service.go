// Package billing integrates Stripe for plan purchases. The secret key is read
// from the encrypted credentials vault (never env/git); cards are collected by
// Stripe Elements on our own page, so card data never touches our server. A
// PaymentIntent is created per checkout; its webhook confirms the charge and
// triggers the account's plan assignment + price lock.
package billing

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"time"

	"github.com/stripe/stripe-go/v82"
	"github.com/stripe/stripe-go/v82/charge"
	"github.com/stripe/stripe-go/v82/checkout/session"
	"github.com/stripe/stripe-go/v82/customer"
	"github.com/stripe/stripe-go/v82/customersession"
	"github.com/stripe/stripe-go/v82/invoice"
	"github.com/stripe/stripe-go/v82/paymentintent"
	"github.com/stripe/stripe-go/v82/paymentmethod"
	"github.com/stripe/stripe-go/v82/price"
	"github.com/stripe/stripe-go/v82/product"
	"github.com/stripe/stripe-go/v82/refund"
	"github.com/stripe/stripe-go/v82/setupintent"
	"github.com/stripe/stripe-go/v82/subscription"
	"github.com/stripe/stripe-go/v82/subscriptionschedule"

	"trilli/system/credentials"
	"trilli/system/database/postgres"
	"trilli/system/logging"
)

const packageName = "billing"

var (
	ErrNotConfigured   = errors.New("billing: stripe not configured")
	ErrPlanUnavailable = errors.New("billing: plan unavailable")
	// User-safe messages surfaced to the client when a card can't be removed.
	ErrLastCard    = errors.New("You must keep at least one payment method on file.")
	ErrDefaultCard = errors.New("Set another card as your default before removing this one.")
	// ErrNoCardForRenew blocks enabling auto-renew on an active subscription
	// with no card on file — there'd be nothing to charge at renewal.
	ErrNoCardForRenew = errors.New("Add a card on file before turning on auto-renew.")
	// Returned when a tenant already has a live subscription (the change-plan /
	// upgrade-downgrade path handles modifications, not a new subscription).
	ErrAlreadySubscribed = errors.New("billing: account already has an active subscription")
	ErrNoPrice           = errors.New("billing: plan has no Stripe price (run `trilli stripe sync-plans`)")
	// Returned by ChangeSubscription when there's no live subscription to modify
	// (the caller should route to first-purchase checkout instead).
	ErrNoSubscription = errors.New("billing: no active subscription to change")
	ErrSamePlan       = errors.New("billing: already on this plan")
	// Returned when the current plan doesn't sell extra seats (per_seat_cents NULL).
	ErrSeatsNotSold = errors.New("billing: plan does not sell extra seats")
)

type Service struct {
	client      *postgres.Client
	publishable string
	webhookKey  string
	enabled     bool
}

// NewService loads the Stripe keys from the vault. If the secret key is absent,
// billing is disabled (endpoints return 503) — non-fatal so the app still runs.
func NewService(pg *postgres.Client, creds *credentials.Service) *Service {
	s := &Service{client: pg}
	ctx := context.Background()
	secret, _, err := creds.GetActive(ctx, "stripe", "secret_key")
	if err != nil || secret == "" {
		logging.Error(packageName, "Stripe secret key not in vault — billing disabled: %v", err)
		return s
	}
	stripe.Key = secret
	if pub, _, e := creds.GetActive(ctx, "stripe", "publishable_key"); e == nil {
		s.publishable = pub
	}
	if wh, _, e := creds.GetActive(ctx, "stripe", "webhook_secret"); e == nil {
		s.webhookKey = wh
	}
	s.enabled = true
	logging.Info(packageName, "Stripe billing enabled")
	return s
}

func (s *Service) Enabled() bool          { return s.enabled }
func (s *Service) PublishableKey() string { return s.publishable }
func (s *Service) WebhookSecret() string  { return s.webhookKey }

// assertPurchasable enforces a plan's availability + inventory limits (SPEC
// §6.3): it must be 'available', within its [available_from, available_until)
// window, and below its max_subscriptions cap. Returns ErrPlanUnavailable
// otherwise. Used to gate new-subscription creation.
func (s *Service) assertPurchasable(ctx context.Context, code string) error {
	var (
		planID  int64
		status  string
		maxSubs sql.NullInt64
		from    sql.NullTime
		until   sql.NullTime
	)
	err := s.client.QueryRowContext(ctx,
		`SELECT id, status, max_subscriptions, available_from, available_until
		   FROM plans WHERE code = $1`, code,
	).Scan(&planID, &status, &maxSubs, &from, &until)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrPlanUnavailable
	}
	if err != nil {
		return fmt.Errorf("billing: load plan availability: %w", err)
	}
	if status != "available" {
		return ErrPlanUnavailable
	}
	now := time.Now()
	if from.Valid && now.Before(from.Time) {
		return ErrPlanUnavailable
	}
	if until.Valid && !now.Before(until.Time) {
		return ErrPlanUnavailable
	}
	if maxSubs.Valid {
		var cnt int64
		if err := s.client.QueryRowContext(ctx,
			`SELECT count(*) FROM tenants WHERE plan_id = $1 AND status <> 'deleted'`, planID,
		).Scan(&cnt); err != nil {
			return fmt.Errorf("billing: count subscribers: %w", err)
		}
		if cnt >= maxSubs.Int64 {
			return ErrPlanUnavailable // sold out
		}
	}
	return nil
}

// planAmount returns the charge amount (cents) for a plan code + billing period,
// matching the lock math used on assignment (annual = discounted per-month × 12).
func (s *Service) planAmount(ctx context.Context, code, period string) (int64, error) {
	var priceMonthly, discount int64
	var status string
	err := s.client.QueryRowContext(ctx,
		`SELECT price_monthly_cents, annual_discount_pct, status FROM plans WHERE code = $1`, code,
	).Scan(&priceMonthly, &discount, &status)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrPlanUnavailable
	}
	if err != nil {
		return 0, fmt.Errorf("billing: load plan: %w", err)
	}
	if status != "available" {
		return 0, ErrPlanUnavailable
	}
	if period == "annual" {
		annualMonthly := (priceMonthly*(100-discount) + 50) / 100
		return annualMonthly * 12, nil
	}
	return priceMonthly, nil
}

// EnsureCustomer returns the tenant's Stripe customer id, creating + persisting
// it on first use.
func (s *Service) EnsureCustomer(ctx context.Context, tenantID int64, email, name string) (string, error) {
	var existing sql.NullString
	if err := s.client.QueryRowContext(ctx,
		`SELECT stripe_customer_id FROM tenants WHERE id = $1`, tenantID).Scan(&existing); err != nil {
		return "", fmt.Errorf("billing: load customer: %w", err)
	}
	if existing.Valid && existing.String != "" {
		// Trust the stored id only if it still resolves in the ACTIVE Stripe
		// account. A stale id (e.g. a test-mode customer while the live key is
		// active, or a since-deleted customer) must not wedge the account — fall
		// through and create a fresh customer, overwriting the stale id.
		if c, err := customer.Get(existing.String, nil); err == nil && !c.Deleted {
			return existing.String, nil
		}
	}
	params := &stripe.CustomerParams{Email: stripe.String(email), Name: stripe.String(name)}
	params.AddMetadata("tenant_id", fmt.Sprintf("%d", tenantID))
	cust, err := customer.New(params)
	if err != nil {
		return "", fmt.Errorf("billing: create customer: %w", err)
	}
	if _, err := s.client.ExecContext(ctx,
		`UPDATE tenants SET stripe_customer_id = $1 WHERE id = $2`, cust.ID, tenantID); err != nil {
		return "", fmt.Errorf("billing: save customer: %w", err)
	}
	return cust.ID, nil
}

// CardSummary is the safe, displayable subset of a saved card — brand/last4/exp
// only, never the PAN. Used to convey "card on file" and route the plan switch.
type CardSummary struct {
	ID       string `json:"id"`
	Brand    string `json:"brand"`
	Last4    string `json:"last4"`
	ExpMonth int64  `json:"exp_month"`
	ExpYear  int64  `json:"exp_year"`
	Default  bool   `json:"default"`
}

// ListCards returns the tenant's saved cards from the Stripe customer's
// PaymentMethods. Returns nil (no error) when billing is off or the tenant has
// no Stripe customer yet — both mean "no card on file".
func (s *Service) ListCards(ctx context.Context, tenantID int64) ([]CardSummary, error) {
	if !s.enabled {
		return nil, nil
	}
	var custID sql.NullString
	if err := s.client.QueryRowContext(ctx,
		`SELECT stripe_customer_id FROM tenants WHERE id = $1`, tenantID).Scan(&custID); err != nil {
		return nil, fmt.Errorf("billing: load customer: %w", err)
	}
	if !custID.Valid || custID.String == "" {
		return nil, nil
	}
	// The customer's default payment method (so the UI can flag "Default").
	defaultPM := ""
	if cust, err := customer.Get(custID.String, nil); err == nil &&
		cust.InvoiceSettings != nil && cust.InvoiceSettings.DefaultPaymentMethod != nil {
		defaultPM = cust.InvoiceSettings.DefaultPaymentMethod.ID
	}
	params := &stripe.PaymentMethodListParams{
		Customer: stripe.String(custID.String),
		Type:     stripe.String("card"),
	}
	params.Limit = stripe.Int64(10)
	var out []CardSummary
	it := paymentmethod.List(params)
	for it.Next() {
		pm := it.PaymentMethod()
		if pm.Card == nil {
			continue
		}
		out = append(out, CardSummary{
			ID:       pm.ID,
			Brand:    string(pm.Card.Brand),
			Last4:    pm.Card.Last4,
			ExpMonth: pm.Card.ExpMonth,
			ExpYear:  pm.Card.ExpYear,
			Default:  pm.ID == defaultPM,
		})
	}
	if err := it.Err(); err != nil {
		return nil, fmt.Errorf("billing: list cards: %w", err)
	}
	return out, nil
}

// Address is a billing address for prefilling the Address Element.
type Address struct {
	Line1      string `json:"line1"`
	Line2      string `json:"line2"`
	City       string `json:"city"`
	State      string `json:"state"`
	PostalCode string `json:"postal_code"`
	Country    string `json:"country"`
}

// IntentResult is everything the checkout page needs: the PaymentIntent secret,
// a Customer Session secret (so the Element shows saved cards), and prefill
// (name/address) pulled from the customer's saved card.
type IntentResult struct {
	ClientSecret                string
	CustomerSessionClientSecret string
	Amount                      int64
	PrefillName                 string
	PrefillAddress              *Address
}

// CreateIntent starts a recurring **subscription** for the plan/period using
// payment_behavior=default_incomplete, and returns the first invoice's
// confirmation secret for the Payment Element to confirm — plus a Customer
// Session (saved cards) and prefill. The subscription saves the confirmed card
// as its default for future renewals. Plan activation + receipt happen on the
// subscription/invoice webhooks, not here.
func (s *Service) CreateIntent(
	ctx context.Context, tenantID int64, email, name, code, period string,
) (*IntentResult, error) {
	if !s.enabled {
		return nil, ErrNotConfigured
	}
	if period != "annual" {
		period = "monthly"
	}

	// Resolve the plan's synced Stripe price for the chosen period.
	var priceMonthlyID, priceAnnualID, planName, status string
	err := s.client.QueryRowContext(ctx,
		`SELECT stripe_price_monthly_id, stripe_price_annual_id, name, status FROM plans WHERE code = $1`, code,
	).Scan(&priceMonthlyID, &priceAnnualID, &planName, &status)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrPlanUnavailable
	}
	if err != nil {
		return nil, fmt.Errorf("billing: load plan: %w", err)
	}
	if status != "available" {
		return nil, ErrPlanUnavailable
	}
	priceID := priceMonthlyID
	if period == "annual" {
		priceID = priceAnnualID
	}
	if priceID == "" {
		return nil, ErrNoPrice
	}

	// One live subscription per account — changes go through the change-plan path.
	var existingSub, existingStatus sql.NullString
	_ = s.client.QueryRowContext(ctx,
		`SELECT stripe_subscription_id, subscription_status FROM tenants WHERE id = $1`, tenantID,
	).Scan(&existingSub, &existingStatus)
	if existingSub.Valid && existingSub.String != "" &&
		(existingStatus.String == "active" || existingStatus.String == "trialing" || existingStatus.String == "past_due") {
		// Only block when that subscription GENUINELY still exists in the active
		// Stripe account. A stale id (different Stripe mode, or deleted) must not
		// trap a prepaid/manually-provisioned account out of ever subscribing —
		// let it create a fresh subscription (the webhook overwrites the old id).
		if _, gerr := subscription.Get(existingSub.String, nil); gerr == nil {
			return nil, ErrAlreadySubscribed
		}
	}

	custID, err := s.EnsureCustomer(ctx, tenantID, email, name)
	if err != nil {
		return nil, err
	}

	subParams := &stripe.SubscriptionParams{
		Customer:        stripe.String(custID),
		Items:           []*stripe.SubscriptionItemsParams{{Price: stripe.String(priceID)}},
		PaymentBehavior: stripe.String("default_incomplete"),
		PaymentSettings: &stripe.SubscriptionPaymentSettingsParams{
			SaveDefaultPaymentMethod: stripe.String("on_subscription"),
		},
	}
	subParams.AddMetadata("tenant_id", fmt.Sprintf("%d", tenantID))
	subParams.AddMetadata("plan_code", code)
	subParams.AddMetadata("plan_name", planName)
	subParams.AddMetadata("billing_period", period)
	subParams.AddMetadata("buyer_email", email)
	subParams.AddMetadata("buyer_name", name)
	subParams.AddExpand("latest_invoice.confirmation_secret")
	sub, err := subscription.New(subParams)
	if err != nil {
		return nil, fmt.Errorf("billing: create subscription: %w", err)
	}

	clientSecret, amount := "", int64(0)
	if inv := sub.LatestInvoice; inv != nil {
		amount = inv.AmountDue
		if inv.ConfirmationSecret != nil {
			clientSecret = inv.ConfirmationSecret.ClientSecret
		}
	}
	if clientSecret == "" {
		return nil, fmt.Errorf("billing: subscription %s has no confirmation secret", sub.ID)
	}

	res := &IntentResult{ClientSecret: clientSecret, Amount: amount}

	// Customer Session — lets the Payment Element render saved cards.
	cs, csErr := customersession.New(&stripe.CustomerSessionParams{
		Customer: stripe.String(custID),
		Components: &stripe.CustomerSessionComponentsParams{
			PaymentElement: &stripe.CustomerSessionComponentsPaymentElementParams{
				Enabled: stripe.Bool(true),
				Features: &stripe.CustomerSessionComponentsPaymentElementFeaturesParams{
					PaymentMethodSave:      stripe.String("enabled"),
					PaymentMethodRedisplay: stripe.String("enabled"),
				},
			},
		},
	})
	if csErr != nil {
		logging.Error(packageName, "CustomerSession failed (saved cards hidden): %v", csErr)
	} else {
		res.CustomerSessionClientSecret = cs.ClientSecret
	}

	// Prefill name/address from the customer's most recent saved card.
	listParams := &stripe.PaymentMethodListParams{
		Customer: stripe.String(custID),
		Type:     stripe.String("card"),
	}
	listParams.Limit = stripe.Int64(1)
	it := paymentmethod.List(listParams)
	if it.Next() {
		if pm := it.PaymentMethod(); pm.BillingDetails != nil {
			res.PrefillName = pm.BillingDetails.Name
			if a := pm.BillingDetails.Address; a != nil {
				res.PrefillAddress = &Address{
					Line1: a.Line1, Line2: a.Line2, City: a.City,
					State: a.State, PostalCode: a.PostalCode, Country: a.Country,
				}
			}
		}
	}
	// Fall back to the account's stored billing address if no saved card has one.
	if res.PrefillAddress == nil {
		s.fillPrefillFromTenant(ctx, tenantID, res)
	}

	return res, nil
}

// SignupCheckout is the result of opening a hosted Checkout for the funnel.
type SignupCheckout struct {
	SessionID string
	URL       string
}

// SignupSubResult backs the embedded-Elements funnel checkout: a
// default-incomplete subscription whose first-invoice confirmation secret the
// Payment Element confirms. No tenant exists yet, so the Stripe customer is
// created from the email; ids are stored on the signup intent.
type SignupSubResult struct {
	ClientSecret   string
	CustomerID     string
	SubscriptionID string
	Amount         int64
}

// CreateSignupSubscription creates the Stripe customer (from email) and a
// default-incomplete subscription for the chosen plan/period, returning the
// confirmation secret for the embedded Payment Element. The intent token rides
// in metadata (no tenant_id yet) so the subscription webhook can mark the
// intent paid once it activates.
func (s *Service) CreateSignupSubscription(ctx context.Context, token, email, code, period string) (*SignupSubResult, error) {
	if !s.enabled {
		return nil, ErrNotConfigured
	}
	if period != "annual" {
		period = "monthly"
	}
	// Availability/inventory gate (SPEC §6.3): refuse a sold-out or
	// outside-window plan even via a direct checkout link.
	if err := s.assertPurchasable(ctx, code); err != nil {
		return nil, err
	}
	var priceMonthlyID, priceAnnualID, planName, status string
	err := s.client.QueryRowContext(ctx,
		`SELECT stripe_price_monthly_id, stripe_price_annual_id, name, status FROM plans WHERE code = $1`, code,
	).Scan(&priceMonthlyID, &priceAnnualID, &planName, &status)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrPlanUnavailable
	}
	if err != nil {
		return nil, fmt.Errorf("billing: load plan: %w", err)
	}
	if status != "available" {
		return nil, ErrPlanUnavailable
	}
	priceID := priceMonthlyID
	if period == "annual" {
		priceID = priceAnnualID
	}
	if priceID == "" {
		return nil, ErrNoPrice
	}

	cust, err := customer.New(&stripe.CustomerParams{Email: stripe.String(email)})
	if err != nil {
		return nil, fmt.Errorf("billing: create customer: %w", err)
	}

	subParams := &stripe.SubscriptionParams{
		Customer:        stripe.String(cust.ID),
		Items:           []*stripe.SubscriptionItemsParams{{Price: stripe.String(priceID)}},
		PaymentBehavior: stripe.String("default_incomplete"),
		PaymentSettings: &stripe.SubscriptionPaymentSettingsParams{
			SaveDefaultPaymentMethod: stripe.String("on_subscription"),
			// Card only → the Payment Element shows just the card form (no Klarna).
			PaymentMethodTypes: stripe.StringSlice([]string{"card"}),
		},
	}
	subParams.AddMetadata("signup_token", token)
	subParams.AddMetadata("plan_code", code)
	subParams.AddMetadata("plan_name", planName)
	subParams.AddMetadata("billing_period", period)
	subParams.AddExpand("latest_invoice.confirmation_secret")
	sub, err := subscription.New(subParams)
	if err != nil {
		return nil, fmt.Errorf("billing: create signup subscription: %w", err)
	}

	res := &SignupSubResult{CustomerID: cust.ID, SubscriptionID: sub.ID}
	if inv := sub.LatestInvoice; inv != nil {
		res.Amount = inv.AmountDue
		if inv.ConfirmationSecret != nil {
			res.ClientSecret = inv.ConfirmationSecret.ClientSecret
		}
	}
	if res.ClientSecret == "" {
		return nil, fmt.Errorf("billing: signup subscription %s has no confirmation secret", sub.ID)
	}
	return res, nil
}

// SignupSubClientSecret re-fetches an existing funnel subscription's first-
// invoice confirmation secret (so reloading the checkout page reuses the same
// subscription instead of creating a duplicate). Also reports its status + amount.
func (s *Service) SignupSubClientSecret(subID string) (clientSecret string, amount int64, status string, err error) {
	if !s.enabled {
		return "", 0, "", ErrNotConfigured
	}
	p := &stripe.SubscriptionParams{}
	p.AddExpand("latest_invoice.confirmation_secret")
	sub, err := subscription.Get(subID, p)
	if err != nil {
		return "", 0, "", fmt.Errorf("billing: get signup subscription: %w", err)
	}
	status = string(sub.Status)
	if inv := sub.LatestInvoice; inv != nil {
		amount = inv.AmountDue
		if inv.ConfirmationSecret != nil {
			clientSecret = inv.ConfirmationSecret.ClientSecret
		}
	}
	return clientSecret, amount, status, nil
}

// SignupSubPaid reports whether a funnel subscription's payment is secured —
// either the subscription is live or its first invoice is paid. Used by the
// synchronous finalize step after the Payment Element confirms.
func (s *Service) SignupSubPaid(subID string) (bool, error) {
	if !s.enabled {
		return false, ErrNotConfigured
	}
	p := &stripe.SubscriptionParams{}
	p.AddExpand("latest_invoice")
	sub, err := subscription.Get(subID, p)
	if err != nil {
		return false, fmt.Errorf("billing: get signup subscription: %w", err)
	}
	if sub.Status == "active" || sub.Status == "trialing" {
		return true, nil
	}
	if inv := sub.LatestInvoice; inv != nil && string(inv.Status) == "paid" {
		return true, nil
	}
	return false, nil
}

// CreateSignupCheckout opens a hosted Stripe Checkout Session (subscription
// mode) for the pay-before-account funnel. There is no tenant yet, so Stripe
// creates the customer from customer_email at checkout; we capture the customer
// + subscription ids from the checkout.session.completed webhook. The intent
// token threads through client_reference_id + metadata and the success URL so
// the webhook and Step-6 page can resolve the intent.
func (s *Service) CreateSignupCheckout(ctx context.Context, token, email, code, period string) (*SignupCheckout, error) {
	if !s.enabled {
		return nil, ErrNotConfigured
	}
	if period != "annual" {
		period = "monthly"
	}
	var priceMonthlyID, priceAnnualID, planName, status string
	err := s.client.QueryRowContext(ctx,
		`SELECT stripe_price_monthly_id, stripe_price_annual_id, name, status FROM plans WHERE code = $1`, code,
	).Scan(&priceMonthlyID, &priceAnnualID, &planName, &status)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrPlanUnavailable
	}
	if err != nil {
		return nil, fmt.Errorf("billing: load plan: %w", err)
	}
	if status != "available" {
		return nil, ErrPlanUnavailable
	}
	priceID := priceMonthlyID
	if period == "annual" {
		priceID = priceAnnualID
	}
	if priceID == "" {
		return nil, ErrNoPrice
	}

	const base = "https://app.trilli.com"
	params := &stripe.CheckoutSessionParams{
		Mode:              stripe.String("subscription"),
		CustomerEmail:     stripe.String(email),
		ClientReferenceID: stripe.String(token),
		LineItems: []*stripe.CheckoutSessionLineItemParams{
			{Price: stripe.String(priceID), Quantity: stripe.Int64(1)},
		},
		// Card only → the card form is expanded by default (no payment-method
		// accordion / Klarna). Restricting types disables automatic methods.
		PaymentMethodTypes: stripe.StringSlice([]string{"card"}),
		// Collect the billing address so Step 6 can pre-fill it.
		BillingAddressCollection: stripe.String("required"),
		// Clean routes: success → Step-6 (token in path); cancel → back to the
		// signup modal with the chosen plan (plan/billing are entity params).
		SuccessURL: stripe.String(base + "/signup/complete/" + token),
		CancelURL:  stripe.String(base + "/signup?plan=" + code + "&billing=" + period),
	}
	params.AddMetadata("signup_token", token)
	params.AddMetadata("plan_code", code)
	params.AddMetadata("billing_period", period)
	params.SubscriptionData = &stripe.CheckoutSessionSubscriptionDataParams{}
	params.SubscriptionData.AddMetadata("signup_token", token)
	params.SubscriptionData.AddMetadata("plan_code", code)
	params.SubscriptionData.AddMetadata("plan_name", planName)
	params.SubscriptionData.AddMetadata("billing_period", period)

	sess, err := session.New(params)
	if err != nil {
		return nil, fmt.Errorf("billing: create checkout session: %w", err)
	}
	return &SignupCheckout{SessionID: sess.ID, URL: sess.URL}, nil
}

// UpdateCustomerAddress writes the billing name + address onto the Stripe
// customer (used when Step 6 edits the address collected at checkout).
func (s *Service) UpdateCustomerAddress(ctx context.Context, custID, name string, a Address) error {
	_ = ctx
	if !s.enabled || custID == "" {
		return nil
	}
	params := &stripe.CustomerParams{}
	if strings.TrimSpace(name) != "" {
		params.Name = stripe.String(name)
	}
	params.Address = &stripe.AddressParams{
		Line1:      stripe.String(a.Line1),
		Line2:      stripe.String(a.Line2),
		City:       stripe.String(a.City),
		State:      stripe.String(a.State),
		PostalCode: stripe.String(a.PostalCode),
		Country:    stripe.String(a.Country),
	}
	if _, err := customer.Update(custID, params); err != nil {
		return fmt.Errorf("billing: update customer address: %w", err)
	}
	return nil
}

// StampSubscriptionTenant writes tenant_id onto a funnel subscription's
// metadata after the account is provisioned. The metadata update fires a
// customer.subscription.updated webhook which — now that tenant_id is present —
// syncs status/period-end/seats and assigns the plan to the new tenant.
func (s *Service) StampSubscriptionTenant(ctx context.Context, subID string, tenantID int64) error {
	if !s.enabled || subID == "" {
		return nil
	}
	params := &stripe.SubscriptionParams{}
	params.AddMetadata("tenant_id", fmt.Sprintf("%d", tenantID))
	if _, err := subscription.Update(subID, params); err != nil {
		return fmt.Errorf("billing: stamp subscription tenant: %w", err)
	}
	// The funnel's first invoice was paid at checkout BEFORE this tenant existed,
	// so the invoice.paid webhook (onInvoicePaid) bailed on the missing tenant_id
	// and the first charge was never recorded locally. Now that the tenant is
	// provisioned, record it. Best-effort + idempotent — never block provisioning
	// on a billing-ledger write.
	s.RecordFunnelFirstInvoice(ctx, subID, tenantID)
	return nil
}

// RecordFunnelFirstInvoice records the first (subscription_create) invoice of a
// pay-before-account funnel subscription into billing_transactions. That invoice
// is paid at checkout before the tenant is provisioned, so onInvoicePaid bails on
// the missing tenant_id and the first charge is never captured locally (renewals,
// which fire post-provisioning, record fine). Called from StampSubscriptionTenant
// at provisioning, and reusable as a one-time backfill. Best-effort and
// idempotent on the invoice id (upsertOrder's ON CONFLICT) — safe to re-run.
func (s *Service) RecordFunnelFirstInvoice(ctx context.Context, subID string, tenantID int64) {
	if !s.enabled || subID == "" || tenantID == 0 {
		return
	}
	sub, err := subscription.Get(subID, nil)
	if err != nil {
		logging.Error(packageName, "first-invoice: get sub %s: %v", subID, err)
		return
	}
	listParams := &stripe.InvoiceListParams{Subscription: stripe.String(subID)}
	listParams.Limit = stripe.Int64(100)
	var first *stripe.Invoice
	iter := invoice.List(listParams)
	for iter.Next() {
		if inv := iter.Invoice(); inv.BillingReason == stripe.InvoiceBillingReasonSubscriptionCreate {
			first = inv
			break
		}
	}
	if err := iter.Err(); err != nil {
		logging.Error(packageName, "first-invoice: list for sub %s: %v", subID, err)
		return
	}
	if first == nil || first.Status != stripe.InvoiceStatusPaid || first.AmountPaid <= 0 {
		return // no paid creation invoice (e.g. comp/trial/$0) — nothing to record
	}
	rec, err := s.RecordInvoiceOrder(ctx, tenantID,
		sub.Metadata["plan_code"], sub.Metadata["billing_period"],
		first.AmountPaid, string(first.Currency), first.ID, first.HostedInvoiceURL)
	if err != nil {
		logging.Error(packageName, "first-invoice: record %s: %v", first.ID, err)
		return
	}
	if rec.IsNew {
		logging.Info(packageName, "first-invoice: recorded %s tenant=%d amount=%d %s",
			first.ID, tenantID, first.AmountPaid, first.Currency)
	} else {
		logging.Info(packageName, "first-invoice: %s already recorded tenant=%d (no-op)", first.ID, tenantID)
	}
}

// ChangeEffect describes the outcome of a plan change.
type ChangeEffect string

const (
	EffectUpgraded  ChangeEffect = "upgraded"  // immediate, prorated
	EffectScheduled ChangeEffect = "scheduled" // deferred to term-end (downgrade)
)

// ChangeResult is returned to the caller (and surfaced to the UI).
type ChangeResult struct {
	Effect      ChangeEffect
	EffectiveAt *time.Time // when it takes effect (now for upgrade, term-end for downgrade)
	PlanName    string
}

// ChangePreview is a read-only summary of what a plan change would cost,
// surfaced in the confirmation modal BEFORE we actually mutate the
// subscription. For upgrades it carries the exact prorated amount due today
// (from Stripe's invoice preview); for downgrades it carries the date the
// change takes effect (no charge today).
type ChangePreview struct {
	Effect           ChangeEffect
	PlanName         string
	CurrentPlanName  string
	Currency         string
	DueTodayCents    int64      // prorated charge collected immediately (upgrades)
	NextRenewalCents int64      // what the account pays at the next renewal
	NextRenewalAt    *time.Time // when the next renewal invoice lands
	EffectiveAt      *time.Time // when the new plan takes effect (now / term-end)
	BillingPeriod    string
}

// PreviewChange computes — without mutating anything — what ChangeSubscription
// would do for the given target plan/period. Upgrades return the exact prorated
// amount due today via Stripe's upcoming-invoice preview (always_invoice math).
// Returns ErrNoSubscription / ErrNotConfigured when there's no live subscription
// to change (the caller then routes to first-purchase checkout).
func (s *Service) PreviewChange(ctx context.Context, tenantID int64, newCode, newPeriod string) (ChangePreview, error) {
	if !s.enabled {
		return ChangePreview{}, ErrNotConfigured
	}
	if newPeriod != "annual" {
		newPeriod = "monthly"
	}

	// Current state.
	var subID, subStatus, curCode, curName, curPeriod sql.NullString
	var curTier sql.NullInt64
	err := s.client.QueryRowContext(ctx, `
		SELECT t.stripe_subscription_id, t.subscription_status, t.locked_billing_period,
		       p.code, p.name, p.price_monthly_cents
		  FROM tenants t LEFT JOIN plans p ON p.id = t.plan_id
		 WHERE t.id = $1`, tenantID,
	).Scan(&subID, &subStatus, &curPeriod, &curCode, &curName, &curTier)
	if err != nil {
		return ChangePreview{}, fmt.Errorf("billing: load current plan: %w", err)
	}
	live := subID.Valid && subID.String != "" &&
		(subStatus.String == "active" || subStatus.String == "trialing" || subStatus.String == "past_due")
	if !live {
		return ChangePreview{}, ErrNoSubscription
	}

	// New plan.
	var newName, newStatus, priceMonthlyID, priceAnnualID string
	var newTier int64
	err = s.client.QueryRowContext(ctx, `
		SELECT name, status, price_monthly_cents, stripe_price_monthly_id, stripe_price_annual_id
		  FROM plans WHERE code = $1`, newCode,
	).Scan(&newName, &newStatus, &newTier, &priceMonthlyID, &priceAnnualID)
	if errors.Is(err, sql.ErrNoRows) || newStatus != "available" {
		return ChangePreview{}, ErrPlanUnavailable
	}
	if err != nil {
		return ChangePreview{}, fmt.Errorf("billing: load new plan: %w", err)
	}
	newPriceID := priceMonthlyID
	if newPeriod == "annual" {
		newPriceID = priceAnnualID
	}
	if newPriceID == "" {
		return ChangePreview{}, ErrNoPrice
	}
	curPeriodStr := curPeriod.String
	if curPeriodStr == "" {
		curPeriodStr = "annual"
	}
	if newCode == curCode.String && newPeriod == curPeriodStr {
		return ChangePreview{}, ErrSamePlan
	}

	isUpgrade := newTier > curTier.Int64 ||
		(newTier == curTier.Int64 && curPeriodStr == "monthly" && newPeriod == "annual")

	sub, err := subscription.Get(subID.String, nil)
	if err != nil {
		// Stored subscription doesn't exist in the active Stripe account (stale
		// id from another Stripe mode, or deleted). Treat as "no subscription"
		// so the caller routes to first-purchase checkout instead of 500-ing.
		var se *stripe.Error
		if errors.As(err, &se) && se.Code == stripe.ErrorCodeResourceMissing {
			return ChangePreview{}, ErrNoSubscription
		}
		return ChangePreview{}, fmt.Errorf("billing: load subscription: %w", err)
	}
	if sub.Items == nil || len(sub.Items.Data) == 0 {
		return ChangePreview{}, fmt.Errorf("billing: subscription has no items")
	}
	item := sub.Items.Data[0]

	out := ChangePreview{
		PlanName:        newName,
		CurrentPlanName: curName.String,
		Currency:        "usd",
		BillingPeriod:   newPeriod,
	}
	// What the next renewal costs (the new plan's recurring price).
	if pr, perr := price.Get(newPriceID, nil); perr == nil {
		out.NextRenewalCents = pr.UnitAmount
		if pr.Currency != "" {
			out.Currency = string(pr.Currency)
		}
	}
	// When the next renewal lands = current period end.
	if item.CurrentPeriodEnd > 0 {
		t := time.Unix(item.CurrentPeriodEnd, 0).UTC()
		out.NextRenewalAt = &t
	}

	if !isUpgrade {
		// Downgrade — scheduled to term-end, no charge today.
		out.Effect = EffectScheduled
		out.EffectiveAt = out.NextRenewalAt
		return out, nil
	}

	// Upgrade — preview the immediate prorated invoice (always_invoice math).
	out.Effect = EffectUpgraded
	now := time.Now().UTC()
	out.EffectiveAt = &now
	customerID := ""
	if sub.Customer != nil {
		customerID = sub.Customer.ID
	}
	preview, perr := invoice.CreatePreview(&stripe.InvoiceCreatePreviewParams{
		Customer:     stripe.String(customerID),
		Subscription: stripe.String(subID.String),
		SubscriptionDetails: &stripe.InvoiceCreatePreviewSubscriptionDetailsParams{
			Items: []*stripe.InvoiceCreatePreviewSubscriptionDetailsItemParams{
				{ID: stripe.String(item.ID), Price: stripe.String(newPriceID)},
			},
			ProrationBehavior: stripe.String("always_invoice"),
			ProrationDate:     stripe.Int64(now.Unix()),
		},
	})
	if perr != nil {
		return ChangePreview{}, fmt.Errorf("billing: preview upgrade invoice: %w", perr)
	}
	// Sum only the proration lines — that net (charge for the new plan's
	// remaining days minus credit for the old plan's unused days) is exactly
	// what Stripe collects upfront. Fall back to the invoice total if Stripe
	// didn't tag any line as a proration.
	var dueToday int64
	var sawProration bool
	if preview.Lines != nil {
		for _, ln := range preview.Lines.Data {
			if ln.Parent != nil && ln.Parent.SubscriptionItemDetails != nil &&
				ln.Parent.SubscriptionItemDetails.Proration {
				dueToday += ln.Amount
				sawProration = true
			}
		}
	}
	if !sawProration {
		dueToday = preview.AmountDue
	}
	if dueToday < 0 {
		dueToday = 0 // never show a negative "due today"
	}
	out.DueTodayCents = dueToday
	if preview.Currency != "" {
		out.Currency = string(preview.Currency)
	}
	return out, nil
}

// ChangeSubscription moves an EXISTING subscription to a different plan/period.
// Upgrades (higher tier, or month→annual same tier) apply immediately and the
// prorated difference is charged UPFRONT (always_invoice). Downgrades defer to
// term-end via a Subscription Schedule (the account keeps the higher plan until
// then; no refund). Returns ErrNoSubscription when there's nothing to change
// (caller routes to first-purchase checkout).
func (s *Service) ChangeSubscription(ctx context.Context, tenantID int64, newCode, newPeriod string) (ChangeResult, error) {
	if !s.enabled {
		return ChangeResult{}, ErrNotConfigured
	}
	if newPeriod != "annual" {
		newPeriod = "monthly"
	}

	// Current state.
	var subID, subStatus, curCode, curPeriod sql.NullString
	var curTier sql.NullInt64
	err := s.client.QueryRowContext(ctx, `
		SELECT t.stripe_subscription_id, t.subscription_status, t.locked_billing_period,
		       p.code, p.price_monthly_cents
		  FROM tenants t LEFT JOIN plans p ON p.id = t.plan_id
		 WHERE t.id = $1`, tenantID,
	).Scan(&subID, &subStatus, &curPeriod, &curCode, &curTier)
	if err != nil {
		return ChangeResult{}, fmt.Errorf("billing: load current plan: %w", err)
	}
	live := subID.Valid && subID.String != "" &&
		(subStatus.String == "active" || subStatus.String == "trialing" || subStatus.String == "past_due")
	if !live {
		return ChangeResult{}, ErrNoSubscription
	}

	// New plan.
	var newName, newStatus, priceMonthlyID, priceAnnualID string
	var newTier int64
	err = s.client.QueryRowContext(ctx, `
		SELECT name, status, price_monthly_cents, stripe_price_monthly_id, stripe_price_annual_id
		  FROM plans WHERE code = $1`, newCode,
	).Scan(&newName, &newStatus, &newTier, &priceMonthlyID, &priceAnnualID)
	if errors.Is(err, sql.ErrNoRows) || newStatus != "available" {
		return ChangeResult{}, ErrPlanUnavailable
	}
	if err != nil {
		return ChangeResult{}, fmt.Errorf("billing: load new plan: %w", err)
	}
	newPriceID := priceMonthlyID
	if newPeriod == "annual" {
		newPriceID = priceAnnualID
	}
	if newPriceID == "" {
		return ChangeResult{}, ErrNoPrice
	}
	curPeriodStr := curPeriod.String
	if curPeriodStr == "" {
		curPeriodStr = "annual"
	}
	if newCode == curCode.String && newPeriod == curPeriodStr {
		return ChangeResult{}, ErrSamePlan
	}

	// Upgrade if moving to a higher tier, or to the (committed) annual on the
	// same tier; everything else (lower tier, annual→monthly) is a downgrade.
	isUpgrade := newTier > curTier.Int64 ||
		(newTier == curTier.Int64 && curPeriodStr == "monthly" && newPeriod == "annual")

	sub, err := subscription.Get(subID.String, nil)
	if err != nil {
		// Stale/missing subscription in the active Stripe account → route to
		// first-purchase checkout rather than erroring (see PreviewChange).
		var se *stripe.Error
		if errors.As(err, &se) && se.Code == stripe.ErrorCodeResourceMissing {
			return ChangeResult{}, ErrNoSubscription
		}
		return ChangeResult{}, fmt.Errorf("billing: load subscription: %w", err)
	}
	if sub.Items == nil || len(sub.Items.Data) == 0 {
		return ChangeResult{}, fmt.Errorf("billing: subscription has no items")
	}
	item := sub.Items.Data[0]
	meta := changeMetadata(sub.Metadata, fmt.Sprintf("%d", tenantID), newCode, newName, newPeriod)

	if isUpgrade {
		// Release any pending (scheduled downgrade) so it doesn't override.
		if sub.Schedule != nil && sub.Schedule.ID != "" {
			_, _ = subscriptionschedule.Release(sub.Schedule.ID, nil)
		}
		// always_invoice (not create_prorations): charge the prorated difference
		// UPFRONT, immediately, instead of rolling it onto the next renewal
		// invoice. Otherwise an annual subscriber could upgrade and get the
		// higher plan for up to a full year before we ever collect the
		// difference. Stripe creates + attempts payment on the proration invoice
		// now (a successful charge fires invoice.paid → our receipt/order).
		params := &stripe.SubscriptionParams{
			Items: []*stripe.SubscriptionItemsParams{
				{ID: stripe.String(item.ID), Price: stripe.String(newPriceID)},
			},
			ProrationBehavior: stripe.String("always_invoice"),
		}
		params.Metadata = meta
		if _, err := subscription.Update(subID.String, params); err != nil {
			return ChangeResult{}, fmt.Errorf("billing: upgrade subscription: %w", err)
		}
		_, _ = s.client.ExecContext(ctx,
			`UPDATE tenants SET scheduled_plan_code = '', scheduled_change_at = NULL WHERE id = $1`, tenantID)
		now := time.Now().UTC()
		return ChangeResult{Effect: EffectUpgraded, EffectiveAt: &now, PlanName: newName}, nil
	}

	// Downgrade — defer to term-end via a Subscription Schedule (2 phases).
	curPriceID := ""
	if item.Price != nil {
		curPriceID = item.Price.ID
	}
	schedID := ""
	if sub.Schedule != nil && sub.Schedule.ID != "" {
		schedID = sub.Schedule.ID
	} else {
		sched, err := subscriptionschedule.New(&stripe.SubscriptionScheduleParams{
			FromSubscription: stripe.String(subID.String),
		})
		if err != nil {
			return ChangeResult{}, fmt.Errorf("billing: create schedule: %w", err)
		}
		schedID = sched.ID
	}
	// Phase boundary = current period end.
	var phaseStart, phaseEnd int64
	if sub.Items.Data[0].CurrentPeriodEnd > 0 {
		phaseEnd = sub.Items.Data[0].CurrentPeriodEnd
	}
	if sched, err := subscriptionschedule.Get(schedID, nil); err == nil && sched.CurrentPhase != nil {
		phaseStart = sched.CurrentPhase.StartDate
		if phaseEnd == 0 {
			phaseEnd = sched.CurrentPhase.EndDate
		}
	}
	curMeta := changeMetadata(sub.Metadata, fmt.Sprintf("%d", tenantID), curCode.String, "", curPeriodStr)
	_, err = subscriptionschedule.Update(schedID, &stripe.SubscriptionScheduleParams{
		EndBehavior: stripe.String("release"),
		Phases: []*stripe.SubscriptionSchedulePhaseParams{
			{
				Items:             []*stripe.SubscriptionSchedulePhaseItemParams{{Price: stripe.String(curPriceID)}},
				StartDate:         stripe.Int64(phaseStart),
				EndDate:           stripe.Int64(phaseEnd),
				ProrationBehavior: stripe.String("none"),
				Metadata:          curMeta,
			},
			{
				Items:             []*stripe.SubscriptionSchedulePhaseItemParams{{Price: stripe.String(newPriceID)}},
				ProrationBehavior: stripe.String("none"),
				Metadata:          meta,
			},
		},
	})
	if err != nil {
		return ChangeResult{}, fmt.Errorf("billing: schedule downgrade: %w", err)
	}
	var effAt *time.Time
	if phaseEnd > 0 {
		t := time.Unix(phaseEnd, 0).UTC()
		effAt = &t
		_, _ = s.client.ExecContext(ctx,
			`UPDATE tenants SET scheduled_plan_code = $1, scheduled_change_at = $2 WHERE id = $3`,
			newCode, t, tenantID)
	}
	return ChangeResult{Effect: EffectScheduled, EffectiveAt: effAt, PlanName: newName}, nil
}

// SeatAddPreview summarizes the cost of buying N extra seats.
type SeatAddPreview struct {
	Quantity         int64
	PlanName         string
	Currency         string
	DueTodayCents    int64      // prorated charge collected immediately
	NextRenewalCents int64      // recurring cost the purchased seats add each renewal
	NextRenewalAt    *time.Time // when that renewal lands
	BillingPeriod    string
}

// seatCtx bundles the live-subscription details both seat operations need.
type seatCtx struct {
	subID            string
	customerID       string
	seatItemID       string // "" when no seat line exists yet
	period           string // "annual" | "monthly"
	interval         string // "year" | "month"
	perSeatUnit      int64  // unit amount for the period (annual = discounted ×12)
	productID        string
	planName         string
	currency         string
	extra            int64 // current purchased extra seats
	currentPeriodEnd int64
}

// loadSeatCtx gathers the live subscription + plan seat pricing. Returns
// ErrNoSubscription/ErrNotConfigured when there's nothing to attach seats to,
// and ErrSeatsNotSold when the plan has no per-seat price.
func (s *Service) loadSeatCtx(ctx context.Context, tenantID int64) (seatCtx, error) {
	if !s.enabled {
		return seatCtx{}, ErrNotConfigured
	}
	var subID, subStatus, curPeriod, planName, productID sql.NullString
	var perSeat sql.NullInt64
	var extra int64
	err := s.client.QueryRowContext(ctx, `
		SELECT t.stripe_subscription_id, t.subscription_status, t.locked_billing_period,
		       COALESCE(t.extra_seats, 0), p.name, p.per_seat_cents,
		       p.stripe_product_id
		  FROM tenants t LEFT JOIN plans p ON p.id = t.plan_id
		 WHERE t.id = $1`, tenantID,
	).Scan(&subID, &subStatus, &curPeriod, &extra, &planName, &perSeat, &productID)
	if err != nil {
		return seatCtx{}, fmt.Errorf("billing: load seat ctx: %w", err)
	}
	live := subID.Valid && subID.String != "" &&
		(subStatus.String == "active" || subStatus.String == "trialing" || subStatus.String == "past_due")
	if !live {
		return seatCtx{}, ErrNoSubscription
	}
	if !perSeat.Valid {
		return seatCtx{}, ErrSeatsNotSold
	}
	period := curPeriod.String
	if period == "" {
		period = "annual"
	}
	c := seatCtx{
		subID:       subID.String,
		period:      period,
		interval:    "month",
		perSeatUnit: perSeat.Int64,
		productID:   productID.String,
		planName:    planName.String,
		currency:    "usd",
		extra:       extra,
	}
	if period == "annual" {
		c.interval = "year"
		// Extra seats are a flat $5/seat/mo on EVERY plan — the annual *plan*
		// discount does not apply to seats. One annual seat = monthly × 12.
		c.perSeatUnit = perSeat.Int64 * 12
	}
	sub, err := subscription.Get(subID.String, nil)
	if err != nil {
		// The stored subscription no longer exists in the active Stripe account
		// — cancelled, or (commonly) a stale id from a different Stripe mode
		// (e.g. a test-mode sub while the live key is active). Treat it as "no
		// live subscription" so the UI cleanly offers checkout instead of
		// surfacing a 500 "internal error".
		var se *stripe.Error
		if errors.As(err, &se) && se.Code == stripe.ErrorCodeResourceMissing {
			return seatCtx{}, ErrNoSubscription
		}
		return seatCtx{}, fmt.Errorf("billing: load subscription: %w", err)
	}
	if sub.Items == nil || len(sub.Items.Data) == 0 {
		return seatCtx{}, fmt.Errorf("billing: subscription has no items")
	}
	if sub.Customer != nil {
		c.customerID = sub.Customer.ID
	}
	c.currentPeriodEnd = sub.Items.Data[0].CurrentPeriodEnd
	for _, it := range sub.Items.Data {
		if it.Metadata != nil && it.Metadata["trilli_kind"] == "seat" {
			c.seatItemID = it.ID
		} else if c.productID == "" && it.Price != nil && it.Price.Product != nil {
			c.productID = it.Price.Product.ID // fall back to the plan item's product
		}
	}
	return c, nil
}

// seatItemParam builds the subscription-item params (add new seat line or bump
// the existing one's quantity) for a target total of `newQty` extra seats.
func (c seatCtx) seatItemParam(newQty int64) *stripe.SubscriptionItemsParams {
	if c.seatItemID != "" {
		return &stripe.SubscriptionItemsParams{
			ID:       stripe.String(c.seatItemID),
			Quantity: stripe.Int64(newQty),
		}
	}
	return &stripe.SubscriptionItemsParams{
		Quantity: stripe.Int64(newQty),
		Metadata: map[string]string{"trilli_kind": "seat"},
		PriceData: &stripe.SubscriptionItemPriceDataParams{
			Product:    stripe.String(c.productID),
			Currency:   stripe.String(string(stripe.CurrencyUSD)),
			UnitAmount: stripe.Int64(c.perSeatUnit),
			Recurring:  &stripe.SubscriptionItemPriceDataRecurringParams{Interval: stripe.String(c.interval)},
		},
	}
}

// PreviewAddSeats computes the prorated cost of adding `qty` seats now, without
// mutating the subscription.
func (s *Service) PreviewAddSeats(ctx context.Context, tenantID, qty int64) (SeatAddPreview, error) {
	if qty < 1 {
		qty = 1
	}
	c, err := s.loadSeatCtx(ctx, tenantID)
	if err != nil {
		return SeatAddPreview{}, err
	}
	newQty := c.extra + qty
	out := SeatAddPreview{
		Quantity:         qty,
		PlanName:         c.planName,
		Currency:         c.currency,
		NextRenewalCents: c.perSeatUnit * qty, // recurring cost the new seats add
		BillingPeriod:    c.period,
	}
	if c.currentPeriodEnd > 0 {
		t := time.Unix(c.currentPeriodEnd, 0).UTC()
		out.NextRenewalAt = &t
	}

	now := time.Now().UTC()
	item := c.seatItemParam(newQty)
	previewItem := &stripe.InvoiceCreatePreviewSubscriptionDetailsItemParams{
		Quantity: item.Quantity,
	}
	if item.ID != nil {
		previewItem.ID = item.ID
	} else {
		previewItem.PriceData = &stripe.InvoiceCreatePreviewSubscriptionDetailsItemPriceDataParams{
			Product:    stripe.String(c.productID),
			Currency:   stripe.String(string(stripe.CurrencyUSD)),
			UnitAmount: stripe.Int64(c.perSeatUnit),
			Recurring:  &stripe.InvoiceCreatePreviewSubscriptionDetailsItemPriceDataRecurringParams{Interval: stripe.String(c.interval)},
		}
	}
	preview, perr := invoice.CreatePreview(&stripe.InvoiceCreatePreviewParams{
		Customer:     stripe.String(c.customerID),
		Subscription: stripe.String(c.subID),
		SubscriptionDetails: &stripe.InvoiceCreatePreviewSubscriptionDetailsParams{
			Items:             []*stripe.InvoiceCreatePreviewSubscriptionDetailsItemParams{previewItem},
			ProrationBehavior: stripe.String("always_invoice"),
			ProrationDate:     stripe.Int64(now.Unix()),
		},
	})
	if perr != nil {
		return SeatAddPreview{}, fmt.Errorf("billing: preview seats invoice: %w", perr)
	}
	var dueToday int64
	var sawProration bool
	if preview.Lines != nil {
		for _, ln := range preview.Lines.Data {
			if ln.Parent != nil && ln.Parent.SubscriptionItemDetails != nil &&
				ln.Parent.SubscriptionItemDetails.Proration {
				dueToday += ln.Amount
				sawProration = true
			}
		}
	}
	if !sawProration {
		dueToday = preview.AmountDue
	}
	if dueToday < 0 {
		dueToday = 0
	}
	out.DueTodayCents = dueToday
	if preview.Currency != "" {
		out.Currency = string(preview.Currency)
	}
	return out, nil
}

// SetExtraSeats reconciles tenants.extra_seats to an authoritative value (used
// by the subscription webhook, which is the source of truth for the seat item
// quantity). No-op-safe; clamps negatives to 0.
func (s *Service) SetExtraSeats(ctx context.Context, tenantID, seats int64) error {
	if seats < 0 {
		seats = 0
	}
	if _, err := s.client.ExecContext(ctx,
		`UPDATE tenants SET extra_seats = $1 WHERE id = $2`, seats, tenantID); err != nil {
		return fmt.Errorf("billing: set extra_seats: %w", err)
	}
	return nil
}

// SeatQuantityOf returns the total quantity across a subscription's seat line
// items (metadata trilli_kind=seat). 0 when there's no seat line.
func SeatQuantityOf(sub *stripe.Subscription) int64 {
	if sub == nil || sub.Items == nil {
		return 0
	}
	var total int64
	for _, it := range sub.Items.Data {
		if it.Metadata != nil && it.Metadata["trilli_kind"] == "seat" {
			total += it.Quantity
		}
	}
	return total
}

// AddSeats purchases `qty` extra seats immediately (prorated, always_invoice)
// and bumps tenants.extra_seats. Returns the new total of purchased seats.
func (s *Service) AddSeats(ctx context.Context, tenantID, qty int64) (int64, error) {
	if qty < 1 {
		qty = 1
	}
	c, err := s.loadSeatCtx(ctx, tenantID)
	if err != nil {
		return 0, err
	}
	newQty := c.extra + qty
	params := &stripe.SubscriptionParams{
		Items:             []*stripe.SubscriptionItemsParams{c.seatItemParam(newQty)},
		ProrationBehavior: stripe.String("always_invoice"),
	}
	if _, err := subscription.Update(c.subID, params); err != nil {
		return 0, fmt.Errorf("billing: add seats: %w", err)
	}
	// Bridge-assign now for instant UI (webhook reconciles from the seat item).
	if _, err := s.client.ExecContext(ctx,
		`UPDATE tenants SET extra_seats = $1 WHERE id = $2`, newQty, tenantID); err != nil {
		return 0, fmt.Errorf("billing: save extra_seats: %w", err)
	}
	return newQty, nil
}

// CancelScheduledChange releases a pending scheduled downgrade — the
// subscription stays on its current plan and keeps renewing.
func (s *Service) CancelScheduledChange(ctx context.Context, tenantID int64) error {
	var subID sql.NullString
	if err := s.client.QueryRowContext(ctx,
		`SELECT stripe_subscription_id FROM tenants WHERE id = $1`, tenantID).Scan(&subID); err != nil {
		return fmt.Errorf("billing: load subscription: %w", err)
	}
	if subID.Valid && subID.String != "" {
		if sub, err := subscription.Get(subID.String, nil); err == nil && sub.Schedule != nil && sub.Schedule.ID != "" {
			if _, err := subscriptionschedule.Release(sub.Schedule.ID, nil); err != nil {
				return fmt.Errorf("billing: release schedule: %w", err)
			}
		}
	}
	_, _ = s.client.ExecContext(ctx,
		`UPDATE tenants SET scheduled_plan_code = '', scheduled_change_at = NULL WHERE id = $1`, tenantID)
	return nil
}

// changeMetadata clones the subscription's metadata, overlaying the target
// plan fields. Phase metadata is applied to the subscription when entered, so
// this is how a scheduled downgrade reassigns the plan at term-end.
func changeMetadata(existing map[string]string, tenantID, code, name, period string) map[string]string {
	m := map[string]string{}
	for k, v := range existing {
		m[k] = v
	}
	m["tenant_id"] = tenantID
	m["plan_code"] = code
	m["billing_period"] = period
	if name != "" {
		m["plan_name"] = name
	}
	return m
}

// fillPrefillFromTenant loads the account's stored billing address (saved at a
// prior checkout) into the prefill, when no saved card carries one.
func (s *Service) fillPrefillFromTenant(ctx context.Context, tenantID int64, res *IntentResult) {
	var name, l1, l2, city, st, pc, country sql.NullString
	err := s.client.QueryRowContext(ctx, `
		SELECT billing_name, billing_line1, billing_line2, billing_city,
		       billing_state, billing_postal_code, billing_country
		  FROM tenants WHERE id = $1`, tenantID,
	).Scan(&name, &l1, &l2, &city, &st, &pc, &country)
	if err != nil || !l1.Valid || l1.String == "" {
		return
	}
	if res.PrefillName == "" {
		res.PrefillName = name.String
	}
	res.PrefillAddress = &Address{
		Line1: l1.String, Line2: l2.String, City: city.String,
		State: st.String, PostalCode: pc.String, Country: country.String,
	}
}

// Transaction is our order record for a completed purchase (what the thank-you
// page and support look at). Stripe IDs are kept alongside our own order number.
type Transaction struct {
	OrderNumber   string    `json:"order_number"`
	PlanCode      string    `json:"plan_code"`
	BillingPeriod string    `json:"billing_period"`
	AmountCents   int64     `json:"amount_cents"`
	Currency      string    `json:"currency"`
	ReceiptURL    string    `json:"receipt_url"`
	ReceiptNumber string    `json:"receipt_number"`
	CreatedAt     time.Time `json:"created_at"`
}

// RecordResult is the outcome of recording an order. IsNew is true only when
// this call inserted the row (not a duplicate webhook delivery) — the caller
// uses it to send the receipt email exactly once.
type RecordResult struct {
	OrderNumber string
	ReceiptURL  string
	IsNew       bool
}

// upsertOrder is the shared order-row writer. `ref` is the Stripe id used as the
// idempotency anchor (a PaymentIntent id for one-time charges, or an invoice id
// for subscription renewals — both unique). Mints the order number on insert.
func (s *Service) upsertOrder(
	ctx context.Context, tenantID int64, ref, code, period string,
	amount int64, currency, chargeID, receiptURL, receiptNumber string,
) (RecordResult, error) {
	var id int64
	var inserted bool
	// (xmax = 0) is true only for a freshly INSERTed row, false for ON CONFLICT updates.
	err := s.client.QueryRowContext(ctx, `
		INSERT INTO billing_transactions
			(tenant_id, plan_code, billing_period, amount_cents, currency,
			 stripe_payment_intent_id, stripe_charge_id, receipt_url, receipt_number, status)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,'succeeded')
		ON CONFLICT (stripe_payment_intent_id) DO UPDATE SET
			stripe_charge_id = EXCLUDED.stripe_charge_id,
			receipt_url      = CASE WHEN EXCLUDED.receipt_url    <> '' THEN EXCLUDED.receipt_url    ELSE billing_transactions.receipt_url    END,
			receipt_number   = CASE WHEN EXCLUDED.receipt_number <> '' THEN EXCLUDED.receipt_number ELSE billing_transactions.receipt_number END
		RETURNING id, (xmax = 0)`,
		tenantID, code, period, amount, currency,
		ref, chargeID, receiptURL, receiptNumber,
	).Scan(&id, &inserted)
	if err != nil {
		return RecordResult{}, fmt.Errorf("billing: record order: %w", err)
	}
	orderNum, err := s.assignOrderNumber(ctx, id)
	if err != nil {
		return RecordResult{}, err
	}
	return RecordResult{OrderNumber: orderNum, ReceiptURL: receiptURL, IsNew: inserted}, nil
}

// RecordTransaction records an order from a one-time PaymentIntent (legacy
// flow), pulling the receipt from its charge.
func (s *Service) RecordTransaction(ctx context.Context, tenantID int64, pi *stripe.PaymentIntent) (RecordResult, error) {
	chargeID, receiptURL, receiptNumber := "", "", ""
	if pi.LatestCharge != nil && pi.LatestCharge.ID != "" {
		chargeID = pi.LatestCharge.ID
		if ch, err := charge.Get(chargeID, nil); err == nil {
			receiptURL = ch.ReceiptURL
			receiptNumber = ch.ReceiptNumber
		}
	}
	return s.upsertOrder(ctx, tenantID, pi.ID, pi.Metadata["plan_code"], pi.Metadata["billing_period"],
		pi.Amount, string(pi.Currency), chargeID, receiptURL, receiptNumber)
}

// RecordInvoiceOrder records an order from a paid subscription invoice. The
// invoice id is the idempotency anchor; the hosted invoice page is the receipt.
func (s *Service) RecordInvoiceOrder(
	ctx context.Context, tenantID int64, code, period string, amount int64, currency, invoiceID, receiptURL string,
) (RecordResult, error) {
	return s.upsertOrder(ctx, tenantID, invoiceID, code, period, amount, currency, "", receiptURL, "")
}

// StoreSubscriptionState mirrors the Stripe subscription's id/status/period-end
// and auto-renew (= !cancel_at_period_end) onto the tenant. A zero periodEnd
// stores NULL.
func (s *Service) StoreSubscriptionState(ctx context.Context, tenantID int64, subID, status string, periodEnd time.Time, autoRenew bool) error {
	var pe any
	if !periodEnd.IsZero() {
		pe = periodEnd.UTC()
	}
	if _, err := s.client.ExecContext(ctx,
		`UPDATE tenants SET stripe_subscription_id = $1, subscription_status = $2, current_period_end = $3, auto_renew = $4 WHERE id = $5`,
		subID, status, pe, autoRenew, tenantID); err != nil {
		return fmt.Errorf("billing: store subscription state: %w", err)
	}
	return nil
}

// LapseGraceDays is how long a lapsed account stays read-only (data intact)
// before the lifecycle janitor permanently purges it. Locked policy.
const LapseGraceDays = 30

// MarkLapsed drops a tenant into the read-only lapse window: lifecycle_state
// 'lapsed', lapsed_at now, purge_at now+30d. It is idempotent — if the tenant
// is already lapsed the existing purge_at and warning progress are preserved,
// so a duplicate webhook can't reset the deletion clock or re-send warnings.
func (s *Service) MarkLapsed(ctx context.Context, tenantID int64) error {
	if _, err := s.client.ExecContext(ctx,
		`UPDATE tenants
		    SET lifecycle_state = 'lapsed',
		        lapsed_at = NOW(),
		        purge_at = NOW() + make_interval(days => $2),
		        purge_warn_stage = 0
		  WHERE id = $1 AND lifecycle_state <> 'lapsed'`,
		tenantID, LapseGraceDays); err != nil {
		return fmt.Errorf("billing: mark lapsed: %w", err)
	}
	return nil
}

// ClearLapsed returns a tenant to full access and cancels any pending purge.
// Idempotent — a no-op when the tenant is already current. Called when a
// subscription becomes healthy again (covers re-subscribe within the window).
func (s *Service) ClearLapsed(ctx context.Context, tenantID int64) error {
	if _, err := s.client.ExecContext(ctx,
		`UPDATE tenants
		    SET lifecycle_state = 'current',
		        lapsed_at = NULL,
		        purge_at = NULL,
		        purge_warn_stage = 0
		  WHERE id = $1 AND lifecycle_state = 'lapsed'`,
		tenantID); err != nil {
		return fmt.Errorf("billing: clear lapsed: %w", err)
	}
	return nil
}

// ErrNoPaymentMethod is returned by ReopenBilling when a refunded account
// can't be reactivated because there's no usable payment method on file to
// re-charge. The caller prompts the owner to add one.
var ErrNoPaymentMethod = errors.New("billing: no payment method on file")

// isLiveSubStatus reports whether a subscription status is one we can still
// act on (cancel/un-cancel). Mirrors the check used by SetAutoRenew.
func isLiveSubStatus(status string) bool {
	return status == "active" || status == "trialing" || status == "past_due"
}

// DeletionBillingPreview reports, without touching Stripe, what deleting the
// account would do to billing: how much would be refunded (net of any prior
// refunds, for an account inside the money-back window; 0 otherwise) and
// whether there's a live subscription that would be cancelled at period end.
// Drives the delete modal's billing notice.
func (s *Service) DeletionBillingPreview(ctx context.Context, tenantID int64, createdAt time.Time, refundWindow time.Duration) (refundCents int64, hasActiveSub bool, err error) {
	var subID, status sql.NullString
	if e := s.client.QueryRowContext(ctx,
		`SELECT stripe_subscription_id, subscription_status FROM tenants WHERE id = $1`, tenantID).
		Scan(&subID, &status); e != nil {
		return 0, false, fmt.Errorf("billing: deletion preview: %w", e)
	}
	hasActiveSub = subID.Valid && subID.String != "" && isLiveSubStatus(status.String)
	if time.Since(createdAt) < refundWindow {
		var net sql.NullInt64
		if e := s.client.QueryRowContext(ctx,
			`SELECT SUM(amount_cents) FROM billing_transactions WHERE tenant_id = $1 AND status IN ('succeeded','refunded')`,
			tenantID).Scan(&net); e == nil && net.Valid && net.Int64 > 0 {
			refundCents = net.Int64
		}
	}
	return refundCents, hasActiveSub, nil
}

// CloseBilling handles the billing side of an owner-initiated account deletion
// and returns how much (in cents) was refunded. It always cancels a live
// subscription at period end (no further charges). Additionally, for an account
// younger than refundWindow, it issues a FULL refund of every successful
// payment (the money-back guarantee) and records each as a negative
// transaction. Returns 0 when billing is disabled or there's nothing to refund.
func (s *Service) CloseBilling(ctx context.Context, tenantID int64, createdAt time.Time, refundWindow time.Duration) (int64, error) {
	if !s.enabled {
		return 0, nil
	}
	var subID, status sql.NullString
	if err := s.client.QueryRowContext(ctx,
		`SELECT stripe_subscription_id, subscription_status FROM tenants WHERE id = $1`, tenantID).
		Scan(&subID, &status); err != nil {
		return 0, fmt.Errorf("billing: load tenant for close: %w", err)
	}

	// Cancel at period end (no refund for the remaining term). Idempotent.
	if subID.Valid && subID.String != "" && isLiveSubStatus(status.String) {
		if _, err := subscription.Update(subID.String, &stripe.SubscriptionParams{
			CancelAtPeriodEnd: stripe.Bool(true),
		}); err != nil {
			return 0, fmt.Errorf("billing: cancel at period end: %w", err)
		}
	}

	// Outside the money-back window → cancel only, no refund.
	if time.Since(createdAt) >= refundWindow {
		return 0, nil
	}

	rows, err := s.client.QueryContext(ctx,
		`SELECT COALESCE(stripe_payment_intent_id,''), COALESCE(stripe_charge_id,''),
		        amount_cents, COALESCE(currency,'usd'), COALESCE(plan_code,''), COALESCE(billing_period,'')
		   FROM billing_transactions
		  WHERE tenant_id = $1 AND status = 'succeeded' AND amount_cents > 0`, tenantID)
	if err != nil {
		return 0, fmt.Errorf("billing: list charges to refund: %w", err)
	}
	type chargeRow struct {
		pi, ch, cur, plan, period string
		amount                    int64
	}
	var charges []chargeRow
	for rows.Next() {
		var c chargeRow
		if err := rows.Scan(&c.pi, &c.ch, &c.amount, &c.cur, &c.plan, &c.period); err != nil {
			rows.Close()
			return 0, fmt.Errorf("billing: scan charge: %w", err)
		}
		charges = append(charges, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("billing: charge rows: %w", err)
	}

	var refunded int64
	for _, c := range charges {
		// Resolve the stored anchor to a refundable target — subscription
		// invoices (in_) must be resolved to their payment_intent/charge, which
		// refund.New can't take directly (same as AdminRefund).
		rp, err := resolveRefundParams(c.pi, c.ch)
		if err != nil {
			logging.Error(packageName, "CloseBilling: resolve refund target tenant=%d pi=%q ch=%q: %v", tenantID, c.pi, c.ch, err)
			continue
		}
		rf, err := refund.New(rp)
		if err != nil {
			// Un-refundable (e.g. already refunded) — log and skip; never fail the
			// whole deletion over one charge.
			logging.Error(packageName, "CloseBilling: refund tenant=%d pi=%q ch=%q failed: %v", tenantID, c.pi, c.ch, err)
			continue
		}
		refunded += rf.Amount
		// Anchor the refund row on the (unique, non-null) Stripe refund id; keep
		// the original charge anchor in stripe_charge_id. The columns are
		// NOT NULL + UNIQUE, so the old NULLIF($pi,'') both nulled out empty
		// invoice charges and collided on the original pi — this row never saved.
		anchor := c.pi
		if anchor == "" {
			anchor = c.ch
		}
		if _, err := s.client.ExecContext(ctx,
			`INSERT INTO billing_transactions
			   (tenant_id, plan_code, billing_period, amount_cents, currency, stripe_payment_intent_id, stripe_charge_id, status)
			 VALUES ($1,$2,$3,$4,$5,$6,$7,'refunded')`,
			tenantID, c.plan, c.period, -rf.Amount, c.cur, rf.ID, anchor); err != nil {
			logging.Error(packageName, "CloseBilling: record refund tenant=%d failed: %v", tenantID, err)
		}
	}
	logging.Info(packageName, "CloseBilling: tenant=%d refunded_cents=%d (charges=%d)", tenantID, refunded, len(charges))
	return refunded, nil
}

// ReopenBilling reverses CloseBilling when an owner reactivates within the
// grace window: if a refund was issued at deletion it re-charges that amount
// (off the saved payment method) BEFORE restoring access, then un-cancels the
// subscription. Returns ErrNoPaymentMethod when a required re-charge can't be
// taken (no/declined payment method) so the caller can prompt for one.
func (s *Service) ReopenBilling(ctx context.Context, tenantID int64) error {
	if !s.enabled {
		return nil
	}
	var subID, custID, status sql.NullString
	var refundCents int64
	if err := s.client.QueryRowContext(ctx,
		`SELECT stripe_subscription_id, stripe_customer_id, subscription_status, closing_refund_cents
		   FROM tenants WHERE id = $1`, tenantID).
		Scan(&subID, &custID, &status, &refundCents); err != nil {
		return fmt.Errorf("billing: load tenant for reopen: %w", err)
	}

	// Re-charge a refunded account before restoring access.
	if refundCents > 0 {
		if !custID.Valid || custID.String == "" {
			return ErrNoPaymentMethod
		}
		pm := s.defaultPaymentMethod(custID.String)
		if pm == "" {
			return ErrNoPaymentMethod
		}
		piParams := &stripe.PaymentIntentParams{
			Amount:        stripe.Int64(refundCents),
			Currency:      stripe.String("usd"),
			Customer:      stripe.String(custID.String),
			PaymentMethod: stripe.String(pm),
			OffSession:    stripe.Bool(true),
			Confirm:       stripe.Bool(true),
			Description:   stripe.String("Trilli account reactivation (re-charge of refunded amount)"),
		}
		piParams.AddExpand("latest_charge")
		piParams.AddMetadata("tenant_id", fmt.Sprintf("%d", tenantID))
		piParams.AddMetadata("reason", "reactivation_recharge")
		pi, err := paymentintent.New(piParams)
		if err != nil {
			logging.Error(packageName, "ReopenBilling: re-charge tenant=%d failed: %v", tenantID, err)
			return ErrNoPaymentMethod
		}
		if pi.Status != stripe.PaymentIntentStatusSucceeded {
			logging.Error(packageName, "ReopenBilling: re-charge tenant=%d not succeeded (status=%s)", tenantID, pi.Status)
			return ErrNoPaymentMethod
		}
		chID := ""
		if pi.LatestCharge != nil {
			chID = pi.LatestCharge.ID
		}
		if _, err := s.client.ExecContext(ctx,
			`INSERT INTO billing_transactions
			   (tenant_id, amount_cents, currency, stripe_payment_intent_id, stripe_charge_id, status)
			 VALUES ($1,$2,'usd',$3,NULLIF($4,''),'succeeded')`,
			tenantID, refundCents, pi.ID, chID); err != nil {
			logging.Error(packageName, "ReopenBilling: record re-charge tenant=%d failed: %v", tenantID, err)
		}
		logging.Info(packageName, "ReopenBilling: tenant=%d re-charged cents=%d pi=%s", tenantID, refundCents, pi.ID)
	}

	// Un-cancel the subscription so it renews again.
	if subID.Valid && subID.String != "" && isLiveSubStatus(status.String) {
		if _, err := subscription.Update(subID.String, &stripe.SubscriptionParams{
			CancelAtPeriodEnd: stripe.Bool(false),
		}); err != nil {
			return fmt.Errorf("billing: un-cancel subscription: %w", err)
		}
	}
	return nil
}

// defaultPaymentMethod returns the customer's default card payment method id,
// falling back to any saved card; "" when none. Mirrors ListCards' lookup.
func (s *Service) defaultPaymentMethod(custID string) string {
	if cust, err := customer.Get(custID, nil); err == nil &&
		cust.InvoiceSettings != nil && cust.InvoiceSettings.DefaultPaymentMethod != nil {
		return cust.InvoiceSettings.DefaultPaymentMethod.ID
	}
	params := &stripe.PaymentMethodListParams{
		Customer: stripe.String(custID),
		Type:     stripe.String("card"),
	}
	params.Limit = stripe.Int64(1)
	it := paymentmethod.List(params)
	for it.Next() {
		return it.PaymentMethod().ID
	}
	return ""
}

// MarkClosing drops a tenant into the owner-initiated deletion window: a
// distinct lifecycle_state 'closing' (vs 'lapsed' for non-payment), lapsed_at
// now (reused as the window start), purge_at now+30d. refundCents records how
// much was refunded at deletion (full money-back for accounts < 30 days old) so
// reactivation knows how much to re-charge; pass 0 when nothing was refunded.
// Idempotent on the state: it won't reset the clock for an already-closing
// tenant, but always stores the refund amount.
func (s *Service) MarkClosing(ctx context.Context, tenantID int64, refundCents int64) error {
	if _, err := s.client.ExecContext(ctx,
		`UPDATE tenants
		    SET lifecycle_state = 'closing',
		        lapsed_at = COALESCE(lapsed_at, NOW()),
		        purge_at = COALESCE(purge_at, NOW() + make_interval(days => $2)),
		        purge_warn_stage = 0,
		        closing_refund_cents = $3
		  WHERE id = $1 AND lifecycle_state = 'current'`,
		tenantID, LapseGraceDays, refundCents); err != nil {
		return fmt.Errorf("billing: mark closing: %w", err)
	}
	return nil
}

// ClearClosing reactivates an owner-deleted tenant within its grace window:
// back to 'current', purge canceled, refund record cleared. Idempotent — a
// no-op unless the tenant is currently 'closing'. The caller handles any
// billing re-charge BEFORE calling this so a failed charge leaves the account
// closing.
func (s *Service) ClearClosing(ctx context.Context, tenantID int64) error {
	if _, err := s.client.ExecContext(ctx,
		`UPDATE tenants
		    SET lifecycle_state = 'current',
		        lapsed_at = NULL,
		        purge_at = NULL,
		        purge_warn_stage = 0,
		        closing_refund_cents = 0
		  WHERE id = $1 AND lifecycle_state = 'closing'`,
		tenantID); err != nil {
		return fmt.Errorf("billing: clear closing: %w", err)
	}
	return nil
}

// ClosingRefundCents returns the amount refunded when this tenant was deleted
// (0 if none), used by reactivation to decide whether a re-charge is required.
func (s *Service) ClosingRefundCents(ctx context.Context, tenantID int64) (int64, error) {
	var cents int64
	if err := s.client.QueryRowContext(ctx,
		`SELECT closing_refund_cents FROM tenants WHERE id = $1`, tenantID).Scan(&cents); err != nil {
		return 0, fmt.Errorf("billing: closing refund cents: %w", err)
	}
	return cents, nil
}

// assignOrderNumber sets a TRL-<15 random digits> order number on the row the
// first time, retrying on the (vanishingly rare) UNIQUE collision. If the row
// already has one (idempotent re-record), the existing number is returned.
func (s *Service) assignOrderNumber(ctx context.Context, id int64) (string, error) {
	var existing sql.NullString
	if err := s.client.QueryRowContext(ctx,
		`SELECT order_number FROM billing_transactions WHERE id = $1`, id).Scan(&existing); err == nil && existing.Valid {
		return existing.String, nil
	}
	for attempt := 0; attempt < 8; attempt++ {
		num, err := random15Digits()
		if err != nil {
			return "", fmt.Errorf("billing: generate order number: %w", err)
		}
		orderNum := "TRL-" + num
		res, err := s.client.ExecContext(ctx,
			`UPDATE billing_transactions SET order_number = $1 WHERE id = $2 AND order_number IS NULL`,
			orderNum, id)
		if err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "duplicate key") {
				continue // collision — try a fresh number
			}
			return "", fmt.Errorf("billing: set order number: %w", err)
		}
		if n, _ := res.RowsAffected(); n == 1 {
			return orderNum, nil
		}
		// 0 rows updated → the number was set concurrently; read it back.
		if err := s.client.QueryRowContext(ctx,
			`SELECT order_number FROM billing_transactions WHERE id = $1`, id).Scan(&existing); err == nil && existing.Valid {
			return existing.String, nil
		}
	}
	return "", fmt.Errorf("billing: could not assign a unique order number after retries")
}

// random15Digits returns a uniformly-random 15-digit string (zero-padded),
// using crypto/rand — 10^15 of space, so collisions are practically impossible.
func random15Digits() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(1_000_000_000_000_000)) // [0, 10^15)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%015d", n), nil
}

// LatestTransaction returns the tenant's most recent fully-formed order, or nil
// if none exists yet (e.g. the webhook hasn't recorded it).
func (s *Service) LatestTransaction(ctx context.Context, tenantID int64) (*Transaction, error) {
	var t Transaction
	var orderNum sql.NullString
	err := s.client.QueryRowContext(ctx, `
		SELECT order_number, plan_code, billing_period, amount_cents, currency,
		       receipt_url, receipt_number, created_at
		  FROM billing_transactions
		 WHERE tenant_id = $1 AND order_number IS NOT NULL
		 ORDER BY created_at DESC
		 LIMIT 1`, tenantID,
	).Scan(&orderNum, &t.PlanCode, &t.BillingPeriod, &t.AmountCents, &t.Currency,
		&t.ReceiptURL, &t.ReceiptNumber, &t.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("billing: latest transaction: %w", err)
	}
	t.OrderNumber = orderNum.String
	return &t, nil
}

// ListTransactions returns the tenant's recent orders, newest first.
func (s *Service) ListTransactions(ctx context.Context, tenantID int64, limit int) ([]Transaction, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.client.QueryContext(ctx, `
		SELECT order_number, plan_code, billing_period, amount_cents, currency,
		       receipt_url, receipt_number, created_at
		  FROM billing_transactions
		 WHERE tenant_id = $1 AND order_number IS NOT NULL
		 ORDER BY created_at DESC
		 LIMIT $2`, tenantID, limit)
	if err != nil {
		return nil, fmt.Errorf("billing: list transactions: %w", err)
	}
	defer rows.Close()
	var out []Transaction
	for rows.Next() {
		var t Transaction
		var orderNum sql.NullString
		if err := rows.Scan(&orderNum, &t.PlanCode, &t.BillingPeriod, &t.AmountCents,
			&t.Currency, &t.ReceiptURL, &t.ReceiptNumber, &t.CreatedAt); err != nil {
			return nil, fmt.Errorf("billing: scan transaction: %w", err)
		}
		t.OrderNumber = orderNum.String
		out = append(out, t)
	}
	return out, rows.Err()
}

// AutoRenew returns the account's auto-renew preference (defaults true).
func (s *Service) AutoRenew(ctx context.Context, tenantID int64) (bool, error) {
	var v sql.NullBool
	if err := s.client.QueryRowContext(ctx,
		`SELECT auto_renew FROM tenants WHERE id = $1`, tenantID).Scan(&v); err != nil {
		return true, fmt.Errorf("billing: load auto_renew: %w", err)
	}
	if !v.Valid {
		return true, nil
	}
	return v.Bool, nil
}

// SetAutoRenew updates the account's auto-renew preference. When there's a live
// subscription, this drives Stripe's cancel_at_period_end (off = the plan lapses
// at term end). With no subscription it's just a stored preference.
func (s *Service) SetAutoRenew(ctx context.Context, tenantID int64, enabled bool) error {
	var subID, status sql.NullString
	_ = s.client.QueryRowContext(ctx,
		`SELECT stripe_subscription_id, subscription_status FROM tenants WHERE id = $1`, tenantID,
	).Scan(&subID, &status)
	liveSub := s.enabled && subID.Valid && subID.String != "" &&
		(status.String == "active" || status.String == "trialing" || status.String == "past_due")
	// Can't auto-renew a live subscription with no card to charge. Block the
	// rare case where a stale client tries to turn it on without one (the UI
	// also disables the toggle; this is the server-side backstop).
	if enabled && liveSub {
		if cards, err := s.ListCards(ctx, tenantID); err == nil && len(cards) == 0 {
			return ErrNoCardForRenew
		}
	}
	if liveSub {
		if _, err := subscription.Update(subID.String, &stripe.SubscriptionParams{
			CancelAtPeriodEnd: stripe.Bool(!enabled),
		}); err != nil {
			return fmt.Errorf("billing: set cancel_at_period_end: %w", err)
		}
	}
	if _, err := s.client.ExecContext(ctx,
		`UPDATE tenants SET auto_renew = $1 WHERE id = $2`, enabled, tenantID); err != nil {
		return fmt.Errorf("billing: set auto_renew: %w", err)
	}
	return nil
}

// SubscriptionSummary is the lifecycle snapshot the Billing tab renders.
type SubscriptionSummary struct {
	Status            string     `json:"status"`             // active, past_due, canceled, ...
	CurrentPeriodEnd  *time.Time `json:"current_period_end"` // nil when none
	AutoRenew         bool       `json:"auto_renew"`
	PastDue           bool       `json:"past_due"`
	ScheduledPlan     string     `json:"scheduled_plan"`      // pending downgrade plan code ("" if none)
	ScheduledPlanName string     `json:"scheduled_plan_name"` // friendly name
	ScheduledChangeAt *time.Time `json:"scheduled_change_at"`
	// S5 enforcement state. LifecycleState is 'current' or 'lapsed'; PurgeAt is
	// set while lapsed (data deleted then). OverQuota (+ StorageOver/SeatsOver)
	// is derived live from usage vs the current plan caps — the downgrade
	// "grace" condition that drives the over-limit banner. Uploads/invites are
	// already blocked by the quota/seat guards; these flags are for messaging.
	LifecycleState string     `json:"lifecycle_state"`
	PurgeAt        *time.Time `json:"purge_at"`
	OverQuota      bool       `json:"over_quota"`
	StorageOver    bool       `json:"storage_over"`
	SeatsOver      bool       `json:"seats_over"`
}

// SeatSummary is the seat-usage snapshot the Users area renders for its meter
// and add-seats upsell. Used = active members + outstanding pending invites
// (matches the invite seat guard). Included/Total are nil for unlimited plans;
// PerSeatMonthly is nil when extra seats aren't sold.
type SeatSummary struct {
	Used           int64    `json:"used"`
	Included       *int64   `json:"included"`         // seats bundled with the plan (nil = unlimited)
	Extra          int64    `json:"extra"`            // purchased add-on seats
	Total          *int64   `json:"total"`            // included + extra (nil = unlimited)
	PerSeatMonthly *float64 `json:"per_seat_monthly"` // dollars/mo per extra seat (nil = not sold)
	Currency       string   `json:"currency"`
	PlanCode       string   `json:"plan_code"`
	PlanName       string   `json:"plan_name"`
	Sellable       bool     `json:"sellable"` // true when more seats can be purchased
}

// SeatUsage returns the tenant's seat snapshot (no Stripe call). Always
// available, even when billing isn't configured — the meter still renders.
func (s *Service) SeatUsage(ctx context.Context, tenantID int64) (SeatSummary, error) {
	var maxUsers, perSeat sql.NullInt64
	var extra int64
	var planCode, planName sql.NullString
	err := s.client.QueryRowContext(ctx, `
		SELECT p.max_users, p.per_seat_cents, COALESCE(t.extra_seats, 0), p.code, p.name
		  FROM tenants t LEFT JOIN plans p ON p.id = t.plan_id
		 WHERE t.id = $1`, tenantID,
	).Scan(&maxUsers, &perSeat, &extra, &planCode, &planName)
	if err != nil {
		return SeatSummary{}, fmt.Errorf("billing: seat usage: %w", err)
	}
	var used int64
	if err := s.client.QueryRowContext(ctx, `
		SELECT
		  (SELECT count(*) FROM tenant_members WHERE tenant_id = $1 AND status = 'active')
		+ (SELECT count(*) FROM invites        WHERE tenant_id = $1 AND status = 'pending')`,
		tenantID).Scan(&used); err != nil {
		return SeatSummary{}, fmt.Errorf("billing: count seats: %w", err)
	}
	out := SeatSummary{
		Used:     used,
		Extra:    extra,
		Currency: "usd",
		PlanCode: planCode.String,
		PlanName: planName.String,
	}
	if maxUsers.Valid {
		inc := maxUsers.Int64
		out.Included = &inc
		total := inc + extra
		out.Total = &total
	}
	if perSeat.Valid {
		v := float64(perSeat.Int64) / 100
		out.PerSeatMonthly = &v
		// Seats are sellable when the plan caps users AND sells extra seats.
		out.Sellable = maxUsers.Valid
	}
	return out, nil
}

// Summary returns the tenant's subscription lifecycle snapshot.
func (s *Service) Summary(ctx context.Context, tenantID int64) (SubscriptionSummary, error) {
	var status, schedCode, lifecycleState sql.NullString
	var pe, schedAt, purgeAt sql.NullTime
	var auto sql.NullBool
	var storageUsed, maxStorage, maxUsers sql.NullInt64
	var userCount sql.NullInt64
	var extraSeats int64
	err := s.client.QueryRowContext(ctx,
		`SELECT t.subscription_status, t.current_period_end, t.auto_renew,
		        t.scheduled_plan_code, t.scheduled_change_at, t.lifecycle_state, t.purge_at,
		        t.storage_bytes_used, t.user_count, p.max_storage_bytes, p.max_users,
		        COALESCE(t.extra_seats, 0)
		   FROM tenants t LEFT JOIN plans p ON p.id = t.plan_id
		  WHERE t.id = $1`, tenantID,
	).Scan(&status, &pe, &auto, &schedCode, &schedAt, &lifecycleState, &purgeAt,
		&storageUsed, &userCount, &maxStorage, &maxUsers, &extraSeats)
	if err != nil {
		return SubscriptionSummary{}, fmt.Errorf("billing: summary: %w", err)
	}
	out := SubscriptionSummary{Status: status.String, AutoRenew: !auto.Valid || auto.Bool}
	if pe.Valid {
		t := pe.Time
		out.CurrentPeriodEnd = &t
	}
	out.PastDue = status.String == "past_due" || status.String == "unpaid"
	out.LifecycleState = lifecycleState.String
	if out.LifecycleState == "" {
		out.LifecycleState = "current"
	}
	if purgeAt.Valid {
		t := purgeAt.Time
		out.PurgeAt = &t
	}
	// Derived grace (over-quota) signals — NULL cap = unlimited (never over).
	out.StorageOver = maxStorage.Valid && storageUsed.Valid && storageUsed.Int64 > maxStorage.Int64
	out.SeatsOver = maxUsers.Valid && userCount.Valid && userCount.Int64 > maxUsers.Int64+extraSeats
	out.OverQuota = out.StorageOver || out.SeatsOver
	if schedCode.Valid && schedCode.String != "" {
		out.ScheduledPlan = schedCode.String
		_ = s.client.QueryRowContext(ctx, `SELECT name FROM plans WHERE code = $1`, schedCode.String).Scan(&out.ScheduledPlanName)
		if schedAt.Valid {
			t := schedAt.Time
			out.ScheduledChangeAt = &t
		}
	}
	return out, nil
}

// ClearScheduledChangeIfApplied clears a pending scheduled change once the
// subscription has actually moved onto that plan (called from the webhook).
func (s *Service) ClearScheduledChangeIfApplied(ctx context.Context, tenantID int64, appliedCode string) {
	_, _ = s.client.ExecContext(ctx,
		`UPDATE tenants SET scheduled_plan_code = '', scheduled_change_at = NULL WHERE id = $1 AND scheduled_plan_code = $2`,
		tenantID, appliedCode)
}

// OpenInvoice is the outstanding invoice (when a payment failed), for the retry UI.
type OpenInvoice struct {
	ID          string `json:"id"`
	HostedURL   string `json:"hosted_invoice_url"`
	AmountCents int64  `json:"amount_cents"`
	Currency    string `json:"currency"`
}

// LatestOpenInvoice returns the customer's most recent open (unpaid) invoice, or nil.
func (s *Service) LatestOpenInvoice(ctx context.Context, tenantID int64) (*OpenInvoice, error) {
	if !s.enabled {
		return nil, nil
	}
	var custID sql.NullString
	if err := s.client.QueryRowContext(ctx,
		`SELECT stripe_customer_id FROM tenants WHERE id = $1`, tenantID).Scan(&custID); err != nil {
		return nil, err
	}
	if !custID.Valid || custID.String == "" {
		return nil, nil
	}
	params := &stripe.InvoiceListParams{
		Customer: stripe.String(custID.String),
		Status:   stripe.String("open"),
	}
	params.Limit = stripe.Int64(1)
	it := invoice.List(params)
	if it.Next() {
		inv := it.Invoice()
		return &OpenInvoice{ID: inv.ID, HostedURL: inv.HostedInvoiceURL, AmountCents: inv.AmountDue, Currency: string(inv.Currency)}, nil
	}
	return nil, it.Err()
}

// RetryInvoice attempts to pay an open invoice now (off the customer's default
// card), instead of waiting for Stripe's next automatic retry.
func (s *Service) RetryInvoice(ctx context.Context, tenantID int64, invoiceID string) error {
	if !s.enabled {
		return ErrNotConfigured
	}
	var custID sql.NullString
	if err := s.client.QueryRowContext(ctx,
		`SELECT stripe_customer_id FROM tenants WHERE id = $1`, tenantID).Scan(&custID); err != nil {
		return err
	}
	inv, err := invoice.Get(invoiceID, nil)
	if err != nil {
		return fmt.Errorf("billing: load invoice: %w", err)
	}
	if inv.Customer == nil || !custID.Valid || inv.Customer.ID != custID.String {
		return errors.New("billing: invoice does not belong to this account")
	}
	if _, err := invoice.Pay(invoiceID, &stripe.InvoicePayParams{}); err != nil {
		return fmt.Errorf("billing: pay invoice: %w", err)
	}
	return nil
}

// CreateSetupIntent makes a SetupIntent so the customer can add/save a card
// without a charge (Stripe Elements confirmSetup). Returns the client secret.
func (s *Service) CreateSetupIntent(ctx context.Context, tenantID int64, email, name string) (string, error) {
	if !s.enabled {
		return "", ErrNotConfigured
	}
	custID, err := s.EnsureCustomer(ctx, tenantID, email, name)
	if err != nil {
		return "", err
	}
	si, err := setupintent.New(&stripe.SetupIntentParams{
		Customer:           stripe.String(custID),
		PaymentMethodTypes: stripe.StringSlice([]string{"card"}),
		Usage:              stripe.String("off_session"),
	})
	if err != nil {
		return "", fmt.Errorf("billing: create setup intent: %w", err)
	}
	return si.ClientSecret, nil
}

// ownsPaymentMethod confirms the payment method is attached to the tenant's
// Stripe customer — guards the default/remove operations.
func (s *Service) ownsPaymentMethod(ctx context.Context, tenantID int64, pmID string) (string, error) {
	var custID sql.NullString
	if err := s.client.QueryRowContext(ctx,
		`SELECT stripe_customer_id FROM tenants WHERE id = $1`, tenantID).Scan(&custID); err != nil {
		return "", fmt.Errorf("billing: load customer: %w", err)
	}
	if !custID.Valid || custID.String == "" {
		return "", ErrNotConfigured
	}
	pm, err := paymentmethod.Get(pmID, nil)
	if err != nil {
		return "", fmt.Errorf("billing: load payment method: %w", err)
	}
	if pm.Customer == nil || pm.Customer.ID != custID.String {
		return "", errors.New("billing: payment method does not belong to this account")
	}
	return custID.String, nil
}

// SetDefaultCard makes the given payment method the customer's default.
func (s *Service) SetDefaultCard(ctx context.Context, tenantID int64, pmID string) error {
	custID, err := s.ownsPaymentMethod(ctx, tenantID, pmID)
	if err != nil {
		return err
	}
	_, err = customer.Update(custID, &stripe.CustomerParams{
		InvoiceSettings: &stripe.CustomerInvoiceSettingsParams{
			DefaultPaymentMethod: stripe.String(pmID),
		},
	})
	if err != nil {
		return fmt.Errorf("billing: set default card: %w", err)
	}
	return nil
}

// annualAmountCents is the yearly charge for a plan billed annually — the
// discounted per-month rate × 12 (matches the lock math in auth.ChangePlan).
func annualAmountCents(priceMonthly, discountPct int64) int64 {
	return ((priceMonthly*(100-discountPct) + 50) / 100) * 12
}

// humanBytes renders a byte count as a compact TB/GB/MB string ("3 TB").
func humanBytes(n int64) string {
	const TB, GB, MB = 1 << 40, 1 << 30, 1 << 20
	switch {
	case n >= TB:
		return strconv.FormatFloat(float64(n)/TB, 'f', -1, 64) + " TB"
	case n >= GB:
		return strconv.FormatFloat(float64(n)/GB, 'f', -1, 64) + " GB"
	case n >= MB:
		return strconv.FormatFloat(float64(n)/MB, 'f', -1, 64) + " MB"
	default:
		return fmt.Sprintf("%d B", n)
	}
}

// allowanceLabel formats a nullable allowance for the Checkout description —
// NULL means unlimited (e.g. "Unlimited storage", "7 TB transfer/mo").
func allowanceLabel(v sql.NullInt64, suffix string) string {
	if !v.Valid {
		return "Unlimited " + suffix
	}
	return humanBytes(v.Int64) + " " + suffix
}

// SyncPlans mirrors the plan catalog into Stripe: a Product per plan plus a
// monthly and an annual recurring Price, storing the ids back on the plans row.
// Idempotent — reuses existing ids, and (since Prices are immutable) creates a
// fresh Price + archives the old one only when the amount/interval changed.
// Returns a human-readable report line per plan.
func (s *Service) SyncPlans(ctx context.Context) ([]string, error) {
	if !s.enabled {
		return nil, ErrNotConfigured
	}
	rows, err := s.client.QueryContext(ctx, `
		SELECT code, name, COALESCE(tagline, ''), price_monthly_cents, annual_discount_pct,
		       max_storage_bytes, max_transfer_bytes_month, max_file_size_bytes, COALESCE(min_seats, 2),
		       stripe_product_id, stripe_price_monthly_id, stripe_price_annual_id
		  FROM plans
		 WHERE status IN ('available','draft')
		 ORDER BY price_monthly_cents`)
	// Retired plans are deliberately NOT synced: their Stripe ids stay in our
	// DB for any existing price-locked subscribers, but they shouldn't be
	// (re)created in the live catalog — that's how the retired bootstrap
	// "Default (placeholder)" plan ended up in Stripe's product list.
	if err != nil {
		return nil, fmt.Errorf("billing: load plans: %w", err)
	}
	defer rows.Close()

	type planRow struct {
		code, name, tagline                 string
		priceMonthly, discount              int64
		storage, transfer, fileSize         sql.NullInt64
		seats                               int64
		prodID, priceMonthlyID, priceAnnual string
	}
	var plans []planRow
	for rows.Next() {
		var p planRow
		if err := rows.Scan(&p.code, &p.name, &p.tagline, &p.priceMonthly, &p.discount,
			&p.storage, &p.transfer, &p.fileSize, &p.seats,
			&p.prodID, &p.priceMonthlyID, &p.priceAnnual); err != nil {
			return nil, fmt.Errorf("billing: scan plan: %w", err)
		}
		plans = append(plans, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	var report []string
	for _, p := range plans {
		// 1. Product — create or keep the name in sync.
		prodID := p.prodID
		prodParams := &stripe.ProductParams{
			Name:     stripe.String(p.name),
			Metadata: map[string]string{"plan_code": p.code},
		}
		// Compose a "what you're getting" description for the Stripe Checkout
		// left panel from the plan's actual allowances (NULL = unlimited).
		feat := []string{
			allowanceLabel(p.storage, "storage"),
			allowanceLabel(p.transfer, "transfer/mo"),
			allowanceLabel(p.fileSize, "max file size"),
			fmt.Sprintf("%d seats included", p.seats),
			"Sharing, links & drop portals",
			"2FA, passkeys & immutable audit log",
			"HIPAA compliant · encrypted at rest & in transit",
		}
		desc := strings.Join(feat, " · ")
		if strings.TrimSpace(p.tagline) != "" {
			desc = p.tagline + " — " + desc
		}
		prodParams.Description = stripe.String(desc)
		if prodID == "" {
			pr, err := product.New(prodParams)
			if err != nil {
				return report, fmt.Errorf("billing: create product %s: %w", p.code, err)
			}
			prodID = pr.ID
		} else {
			_, _ = product.Update(prodID, prodParams)
		}

		// 2. Prices (monthly + annual).
		monthlyID, err := s.ensurePrice(prodID, p.priceMonthlyID, p.priceMonthly, "month")
		if err != nil {
			return report, fmt.Errorf("billing: monthly price %s: %w", p.code, err)
		}
		annualID, err := s.ensurePrice(prodID, p.priceAnnual, annualAmountCents(p.priceMonthly, p.discount), "year")
		if err != nil {
			return report, fmt.Errorf("billing: annual price %s: %w", p.code, err)
		}

		if _, err := s.client.ExecContext(ctx, `
			UPDATE plans SET stripe_product_id = $1, stripe_price_monthly_id = $2, stripe_price_annual_id = $3
			 WHERE code = $4`, prodID, monthlyID, annualID, p.code); err != nil {
			return report, fmt.Errorf("billing: save price ids %s: %w", p.code, err)
		}
		report = append(report, fmt.Sprintf("%-10s product=%s monthly=%s annual=%s", p.code, prodID, monthlyID, annualID))
	}
	return report, nil
}

// ensurePrice returns a Stripe Price id matching (amount, interval) for the
// product, reusing the existing id when unchanged, else creating a new active
// Price (and archiving the prior one — Stripe Prices can't be edited).
func (s *Service) ensurePrice(productID, existingID string, amount int64, interval string) (string, error) {
	if existingID != "" {
		if pr, err := price.Get(existingID, nil); err == nil {
			if pr.Active && pr.UnitAmount == amount && pr.Recurring != nil && string(pr.Recurring.Interval) == interval {
				return existingID, nil
			}
			_, _ = price.Update(existingID, &stripe.PriceParams{Active: stripe.Bool(false)})
		}
	}
	np, err := price.New(&stripe.PriceParams{
		Product:    stripe.String(productID),
		Currency:   stripe.String(string(stripe.CurrencyUSD)),
		UnitAmount: stripe.Int64(amount),
		Recurring:  &stripe.PriceRecurringParams{Interval: stripe.String(interval)},
	})
	if err != nil {
		return "", err
	}
	return np.ID, nil
}

// RemoveCard detaches a saved card from the customer. Guards: the account must
// always keep at least one card, and the default card can't be removed while
// other cards exist (the user must promote another to default first).
func (s *Service) RemoveCard(ctx context.Context, tenantID int64, pmID string) error {
	if _, err := s.ownsPaymentMethod(ctx, tenantID, pmID); err != nil {
		return err
	}
	cards, err := s.ListCards(ctx, tenantID)
	if err != nil {
		return err
	}
	if len(cards) <= 1 {
		return ErrLastCard
	}
	for _, c := range cards {
		if c.ID == pmID && c.Default {
			return ErrDefaultCard
		}
	}
	if _, err := paymentmethod.Detach(pmID, nil); err != nil {
		return fmt.Errorf("billing: detach card: %w", err)
	}
	return nil
}
