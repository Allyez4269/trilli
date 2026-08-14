package billing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/stripe/stripe-go/v82"
	"github.com/stripe/stripe-go/v82/invoice"
	"github.com/stripe/stripe-go/v82/subscription"
	"github.com/stripe/stripe-go/v82/webhook"

	"trilli/system/auth"
	"trilli/system/logging"
	"trilli/system/mailer"
)

// PlanAssigner performs the account's plan assignment + price lock (auth.ChangePlan).
// Injected to avoid an import cycle.
type PlanAssigner func(ctx context.Context, tenantID int64, code, period string) error

// SignupPaidHook is invoked when a funnel checkout completes (payment secured),
// so the signup-intent layer can mark the intent paid + provision later. Wired
// from main to avoid a billing → signupintents import. May be nil.
type SignupPaidHook func(ctx context.Context, signupToken, sessionID, subscriptionID, customerID string) error

// SignupSubActivatedHook fires when an embedded-Elements funnel subscription
// (signup_token in metadata, no tenant_id yet) goes active — the authoritative
// backup to the synchronous finalize step. May be nil.
type SignupSubActivatedHook func(ctx context.Context, signupToken, subscriptionID, customerID string) error

type Handlers struct {
	svc             *Service
	assign          PlanAssigner
	mailer          *mailer.Mailer         // transactional email (purchase receipts); may be nil
	signupPaid      SignupPaidHook         // funnel checkout.session.completed sink; may be nil
	signupSubActive SignupSubActivatedHook // funnel embedded-sub activation sink; may be nil
}

func NewHandlers(svc *Service, assign PlanAssigner, m *mailer.Mailer) *Handlers {
	return &Handlers{svc: svc, assign: assign, mailer: m}
}

// SetSignupPaidHook wires the hosted-Checkout funnel payment sink.
func (h *Handlers) SetSignupPaidHook(fn SignupPaidHook) { h.signupPaid = fn }

// SetSignupSubActivatedHook wires the embedded-Elements funnel activation sink.
func (h *Handlers) SetSignupSubActivatedHook(fn SignupSubActivatedHook) { h.signupSubActive = fn }

type muxLike interface {
	Handle(pattern string, handler http.Handler)
}

func (h *Handlers) Register(
	m muxLike,
	requireAuth func(http.Handler) http.Handler,
	requireAdminOrOwner func(http.Handler) http.Handler,
) {
	m.Handle("GET /api/billing/config", requireAuth(http.HandlerFunc(h.Config)))
	m.Handle("GET /api/billing/payment-methods", requireAuth(http.HandlerFunc(h.PaymentMethods)))
	m.Handle("GET /api/billing/transactions/latest", requireAuth(http.HandlerFunc(h.LatestTransaction)))
	m.Handle("POST /api/billing/intent", requireAdminOrOwner(http.HandlerFunc(h.Intent)))
	// Billing-tab surface (admin/owner only — exposes cards + receipts).
	m.Handle("GET /api/billing/overview", requireAdminOrOwner(http.HandlerFunc(h.Overview)))
	m.Handle("PUT /api/billing/auto-renew", requireAdminOrOwner(http.HandlerFunc(h.SetAutoRenew)))
	m.Handle("POST /api/billing/setup-intent", requireAdminOrOwner(http.HandlerFunc(h.SetupIntent)))
	m.Handle("POST /api/billing/payment-methods/default", requireAdminOrOwner(http.HandlerFunc(h.SetDefaultCard)))
	m.Handle("POST /api/billing/payment-methods/remove", requireAdminOrOwner(http.HandlerFunc(h.RemoveCard)))
	m.Handle("POST /api/billing/retry-invoice", requireAdminOrOwner(http.HandlerFunc(h.RetryInvoice)))
	m.Handle("GET /api/billing/seats", requireAdminOrOwner(http.HandlerFunc(h.Seats)))
	m.Handle("POST /api/billing/preview-seats", requireAdminOrOwner(http.HandlerFunc(h.PreviewSeats)))
	m.Handle("POST /api/billing/add-seats", requireAdminOrOwner(http.HandlerFunc(h.AddSeats)))
	m.Handle("POST /api/billing/preview-change", requireAdminOrOwner(http.HandlerFunc(h.PreviewChange)))
	m.Handle("POST /api/billing/change-plan", requireAdminOrOwner(http.HandlerFunc(h.ChangePlan)))
	m.Handle("POST /api/billing/cancel-scheduled-change", requireAdminOrOwner(http.HandlerFunc(h.CancelScheduledChange)))
	// Webhook is public — Stripe signs it; we verify the signature instead of a session.
	m.Handle("POST /api/billing/webhook", http.HandlerFunc(h.Webhook))
}

// Config — publishable key + whether billing is live, for the frontend Elements.
func (h *Handlers) Config(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"enabled":         h.svc.Enabled(),
		"publishable_key": h.svc.PublishableKey(),
	})
}

// PaymentMethods — the tenant's saved cards (brand/last4/exp only). Drives the
// card-on-file experience: routing the plan switch and showing what's on file.
func (h *Handlers) PaymentMethods(w http.ResponseWriter, r *http.Request) {
	identity, ok := auth.IdentityFromContext(r.Context())
	if !ok || identity.Tenant == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	cards, err := h.svc.ListCards(r.Context(), identity.Tenant.ID)
	if err != nil {
		logging.Error(packageName, "ListCards failed: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	if cards == nil {
		cards = []CardSummary{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"enabled":  h.svc.Enabled(),
		"has_card": len(cards) > 0,
		"cards":    cards,
	})
}

// LatestTransaction — the tenant's most recent order (for the thank-you page).
// Returns {} when none exists yet (e.g. the webhook hasn't recorded it).
func (h *Handlers) LatestTransaction(w http.ResponseWriter, r *http.Request) {
	identity, ok := auth.IdentityFromContext(r.Context())
	if !ok || identity.Tenant == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	tx, err := h.svc.LatestTransaction(r.Context(), identity.Tenant.ID)
	if err != nil {
		logging.Error(packageName, "LatestTransaction failed: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	if tx == nil {
		writeJSON(w, http.StatusOK, map[string]any{})
		return
	}
	writeJSON(w, http.StatusOK, tx)
}

// Overview — everything the Billing tab needs in one call: auto-renew setting,
// saved cards, and recent receipts/orders.
func (h *Handlers) Overview(w http.ResponseWriter, r *http.Request) {
	identity, ok := auth.IdentityFromContext(r.Context())
	if !ok || identity.Tenant == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	tid := identity.Tenant.ID
	cards, err := h.svc.ListCards(r.Context(), tid)
	if err != nil {
		logging.Error(packageName, "Overview ListCards: %v", err)
	}
	txs, err := h.svc.ListTransactions(r.Context(), tid, 50)
	if err != nil {
		logging.Error(packageName, "Overview ListTransactions: %v", err)
	}
	summary, _ := h.svc.Summary(r.Context(), tid)
	if cards == nil {
		cards = []CardSummary{}
	}
	if txs == nil {
		txs = []Transaction{}
	}
	out := map[string]any{
		"enabled":      h.svc.Enabled(),
		"auto_renew":   summary.AutoRenew,
		"subscription": summary,
		"cards":        cards,
		"transactions": txs,
	}
	// When a payment has failed, surface the outstanding invoice for the retry UI.
	if summary.PastDue {
		if open, err := h.svc.LatestOpenInvoice(r.Context(), tid); err == nil && open != nil {
			out["open_invoice"] = open
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// Seats returns the tenant's seat-usage snapshot for the Users-area meter.
func (h *Handlers) Seats(w http.ResponseWriter, r *http.Request) {
	identity, ok := auth.IdentityFromContext(r.Context())
	if !ok || identity.Tenant == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	sum, err := h.svc.SeatUsage(r.Context(), identity.Tenant.ID)
	if err != nil {
		logging.Error(packageName, "SeatUsage failed: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	writeJSON(w, http.StatusOK, sum)
}

// PreviewSeats returns the prorated cost of adding N seats (read-only), so the
// add-seats modal can show "due today" before committing. needs_checkout means
// there's no live subscription to attach seats to yet.
func (h *Handlers) PreviewSeats(w http.ResponseWriter, r *http.Request) {
	identity, ok := auth.IdentityFromContext(r.Context())
	if !ok || identity.Tenant == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	qty := decodeSeatQty(r)
	if qty < 1 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid quantity"})
		return
	}
	p, err := h.svc.PreviewAddSeats(r.Context(), identity.Tenant.ID, qty)
	switch {
	case err == ErrNoSubscription || err == ErrNotConfigured:
		writeJSON(w, http.StatusOK, map[string]any{"needs_checkout": true})
	case err == ErrSeatsNotSold:
		writeJSON(w, http.StatusConflict, map[string]string{"error": "This plan doesn't sell extra seats."})
	case err != nil:
		logging.Error(packageName, "PreviewAddSeats failed: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
	default:
		out := map[string]any{
			"quantity":           p.Quantity,
			"plan_name":          p.PlanName,
			"currency":           p.Currency,
			"due_today_cents":    p.DueTodayCents,
			"next_renewal_cents": p.NextRenewalCents,
			"billing_period":     p.BillingPeriod,
		}
		if p.NextRenewalAt != nil {
			out["next_renewal_at"] = p.NextRenewalAt.Format(time.RFC3339)
		}
		writeJSON(w, http.StatusOK, out)
	}
}

// AddSeats purchases N additional seats (immediate, prorated) on the live
// subscription and bumps tenants.extra_seats.
func (h *Handlers) AddSeats(w http.ResponseWriter, r *http.Request) {
	identity, ok := auth.IdentityFromContext(r.Context())
	if !ok || identity.Tenant == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	qty := decodeSeatQty(r)
	if qty < 1 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid quantity"})
		return
	}
	total, err := h.svc.AddSeats(r.Context(), identity.Tenant.ID, qty)
	switch {
	case err == ErrNoSubscription || err == ErrNotConfigured:
		writeJSON(w, http.StatusOK, map[string]any{"needs_checkout": true})
	case err == ErrSeatsNotSold:
		writeJSON(w, http.StatusConflict, map[string]string{"error": "This plan doesn't sell extra seats."})
	case err != nil:
		logging.Error(packageName, "AddSeats failed: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
	default:
		writeJSON(w, http.StatusOK, map[string]any{"extra_seats": total})
	}
}

// decodeSeatQty pulls a positive seat count from the request body.
func decodeSeatQty(r *http.Request) int64 {
	var req struct {
		Quantity int64 `json:"quantity"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return 0
	}
	return req.Quantity
}

// PreviewChange returns a read-only summary of a plan change (prorated amount
// due today for upgrades, effective date for downgrades) so the UI can show a
// confirmation modal BEFORE committing. needs_checkout signals there's no live
// subscription, so the frontend routes to first-purchase checkout instead.
func (h *Handlers) PreviewChange(w http.ResponseWriter, r *http.Request) {
	identity, ok := auth.IdentityFromContext(r.Context())
	if !ok || identity.Tenant == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	var req struct {
		Code          string `json:"code"`
		BillingPeriod string `json:"billing_period"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Code) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}
	p, err := h.svc.PreviewChange(r.Context(), identity.Tenant.ID, strings.TrimSpace(req.Code), req.BillingPeriod)
	switch {
	case err == ErrNoSubscription || err == ErrNotConfigured:
		// No live subscription to prorate against → first-purchase checkout.
		writeJSON(w, http.StatusOK, map[string]any{"needs_checkout": true})
	case err == ErrSamePlan:
		writeJSON(w, http.StatusConflict, map[string]string{"error": "You're already on this plan."})
	case err == ErrPlanUnavailable || err == ErrNoPrice:
		writeJSON(w, http.StatusConflict, map[string]string{"error": "plan unavailable"})
	case err != nil:
		logging.Error(packageName, "PreviewChange failed: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
	default:
		out := map[string]any{
			"effect":             string(p.Effect),
			"plan_name":          p.PlanName,
			"current_plan_name":  p.CurrentPlanName,
			"currency":           p.Currency,
			"due_today_cents":    p.DueTodayCents,
			"next_renewal_cents": p.NextRenewalCents,
			"billing_period":     p.BillingPeriod,
		}
		if p.NextRenewalAt != nil {
			out["next_renewal_at"] = p.NextRenewalAt.Format(time.RFC3339)
		}
		if p.EffectiveAt != nil {
			out["effective_at"] = p.EffectiveAt.Format(time.RFC3339)
		}
		writeJSON(w, http.StatusOK, out)
	}
}

// ChangePlan moves an existing subscriber to a different plan/period. Upgrades
// apply immediately (we also bridge-assign for instant UI); downgrades are
// scheduled for term-end. Returns needs_checkout when there's no live
// subscription (the frontend then routes to first-purchase checkout).
func (h *Handlers) ChangePlan(w http.ResponseWriter, r *http.Request) {
	identity, ok := auth.IdentityFromContext(r.Context())
	if !ok || identity.Tenant == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	var req struct {
		Code          string `json:"code"`
		BillingPeriod string `json:"billing_period"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Code) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}
	code := strings.TrimSpace(req.Code)
	res, err := h.svc.ChangeSubscription(r.Context(), identity.Tenant.ID, code, req.BillingPeriod)
	switch {
	case err == ErrNoSubscription:
		writeJSON(w, http.StatusOK, map[string]any{"needs_checkout": true})
	case err == ErrSamePlan:
		writeJSON(w, http.StatusConflict, map[string]string{"error": "You're already on this plan."})
	case err == ErrPlanUnavailable || err == ErrNoPrice:
		writeJSON(w, http.StatusConflict, map[string]string{"error": "plan unavailable"})
	case err != nil:
		logging.Error(packageName, "ChangeSubscription failed: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
	default:
		// Immediate upgrade → bridge-assign now for instant UI (webhook is the
		// source of truth and also assigns). Scheduled downgrade → leave the
		// current plan until term-end.
		if res.Effect == EffectUpgraded {
			if aerr := h.assign(r.Context(), identity.Tenant.ID, code, req.BillingPeriod); aerr != nil {
				logging.Error(packageName, "ChangePlan bridge-assign failed: %v", aerr)
			}
		}
		out := map[string]any{"effect": string(res.Effect), "plan_name": res.PlanName}
		if res.EffectiveAt != nil {
			out["effective_at"] = res.EffectiveAt.Format(time.RFC3339)
		}
		writeJSON(w, http.StatusOK, out)
	}
}

// CancelScheduledChange releases a pending scheduled downgrade.
func (h *Handlers) CancelScheduledChange(w http.ResponseWriter, r *http.Request) {
	identity, ok := auth.IdentityFromContext(r.Context())
	if !ok || identity.Tenant == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	if err := h.svc.CancelScheduledChange(r.Context(), identity.Tenant.ID); err != nil {
		logging.Error(packageName, "CancelScheduledChange: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// RetryInvoice attempts to pay the tenant's outstanding invoice now.
func (h *Handlers) RetryInvoice(w http.ResponseWriter, r *http.Request) {
	identity, ok := auth.IdentityFromContext(r.Context())
	if !ok || identity.Tenant == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	var req struct {
		InvoiceID string `json:"invoice_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.InvoiceID) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}
	if err := h.svc.RetryInvoice(r.Context(), identity.Tenant.ID, strings.TrimSpace(req.InvoiceID)); err != nil {
		logging.Error(packageName, "RetryInvoice: %v", err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "We couldn't process that payment. Try updating your card."})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// SetAutoRenew toggles the account's auto-renew preference.
func (h *Handlers) SetAutoRenew(w http.ResponseWriter, r *http.Request) {
	identity, ok := auth.IdentityFromContext(r.Context())
	if !ok || identity.Tenant == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}
	if err := h.svc.SetAutoRenew(r.Context(), identity.Tenant.ID, req.Enabled); err != nil {
		if errors.Is(err, ErrNoCardForRenew) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		logging.Error(packageName, "SetAutoRenew: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"auto_renew": req.Enabled})
}

// SetupIntent — a SetupIntent client secret so the customer can add a new card
// via Stripe Elements (confirmSetup) without a charge.
func (h *Handlers) SetupIntent(w http.ResponseWriter, r *http.Request) {
	identity, ok := auth.IdentityFromContext(r.Context())
	if !ok || identity.Tenant == nil || identity.User == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	name := identity.User.Email
	if identity.User.FullName != nil && *identity.User.FullName != "" {
		name = *identity.User.FullName
	}
	secret, err := h.svc.CreateSetupIntent(r.Context(), identity.Tenant.ID, identity.User.Email, name)
	switch {
	case err == ErrNotConfigured:
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "billing not configured"})
	case err != nil:
		logging.Error(packageName, "CreateSetupIntent: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
	default:
		writeJSON(w, http.StatusOK, map[string]any{
			"client_secret":   secret,
			"publishable_key": h.svc.PublishableKey(),
		})
	}
}

// SetDefaultCard / RemoveCard — manage the saved cards. Both validate the
// payment method belongs to the account's customer.
func (h *Handlers) SetDefaultCard(w http.ResponseWriter, r *http.Request) {
	h.cardOp(w, r, h.svc.SetDefaultCard)
}

func (h *Handlers) RemoveCard(w http.ResponseWriter, r *http.Request) {
	h.cardOp(w, r, h.svc.RemoveCard)
}

func (h *Handlers) cardOp(w http.ResponseWriter, r *http.Request, op func(context.Context, int64, string) error) {
	identity, ok := auth.IdentityFromContext(r.Context())
	if !ok || identity.Tenant == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	var req struct {
		PaymentMethodID string `json:"payment_method_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.PaymentMethodID) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}
	if err := op(r.Context(), identity.Tenant.ID, strings.TrimSpace(req.PaymentMethodID)); err != nil {
		logging.Error(packageName, "card op: %v", err)
		msg := "could not update payment method"
		if errors.Is(err, ErrLastCard) || errors.Is(err, ErrDefaultCard) {
			msg = err.Error() // user-safe guard messages
		}
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": msg})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// Intent — create a PaymentIntent for {plan_code, billing_period}; returns the
// client secret the Payment Element confirms against. Owner/admin only.
func (h *Handlers) Intent(w http.ResponseWriter, r *http.Request) {
	identity, ok := auth.IdentityFromContext(r.Context())
	if !ok || identity.Tenant == nil || identity.User == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	var req struct {
		Code          string `json:"code"`
		BillingPeriod string `json:"billing_period"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Code) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}
	name := identity.User.Email
	if identity.User.FullName != nil && *identity.User.FullName != "" {
		name = *identity.User.FullName
	}
	res, err := h.svc.CreateIntent(
		r.Context(), identity.Tenant.ID, identity.User.Email, name, strings.TrimSpace(req.Code), req.BillingPeriod,
	)
	switch {
	case err == ErrNotConfigured:
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "billing not configured"})
	case err == ErrPlanUnavailable || err == ErrNoPrice:
		writeJSON(w, http.StatusConflict, map[string]string{"error": "plan unavailable"})
	case err == ErrAlreadySubscribed:
		writeJSON(w, http.StatusConflict, map[string]string{"error": "Your account already has an active subscription. Use change plan instead."})
	case err != nil:
		logging.Error(packageName, "CreateIntent failed: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
	default:
		out := map[string]any{
			"client_secret":                  res.ClientSecret,
			"customer_session_client_secret": res.CustomerSessionClientSecret,
			"publishable_key":                h.svc.PublishableKey(),
			"amount":                         res.Amount,
		}
		if res.PrefillName != "" || res.PrefillAddress != nil {
			out["prefill"] = map[string]any{"name": res.PrefillName, "address": res.PrefillAddress}
		}
		writeJSON(w, http.StatusOK, out)
	}
}

// Webhook — Stripe-signed events. On payment_intent.succeeded we read the metadata
// and assign the plan + lock the (actually-charged) price.
func (h *Handlers) Webhook(w http.ResponseWriter, r *http.Request) {
	payload, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	secret := h.svc.WebhookSecret()
	if secret == "" {
		logging.Error(packageName, "Webhook received but no signing secret configured")
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	// IgnoreAPIVersionMismatch: the endpoint may deliver events on the account's
	// (newer) API version than stripe-go pins. We only read version-stable
	// metadata (tenant_id/plan_code/billing_period), so the object shape is safe.
	event, err := webhook.ConstructEventWithOptions(
		payload, r.Header.Get("Stripe-Signature"), secret,
		webhook.ConstructEventOptions{IgnoreAPIVersionMismatch: true},
	)
	if err != nil {
		logging.Error(packageName, "Webhook signature verification failed: %v", err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	var herr error
	switch event.Type {
	case "checkout.session.completed":
		herr = h.onCheckoutSessionCompleted(r.Context(), event.Data.Raw) // signup funnel
	case "payment_intent.succeeded":
		herr = h.onPaymentIntentSucceeded(r.Context(), event.Data.Raw) // legacy one-time flow
	case "customer.subscription.created", "customer.subscription.updated", "customer.subscription.deleted":
		herr = h.onSubscriptionEvent(r.Context(), event.Data.Raw)
	case "invoice.paid":
		herr = h.onInvoicePaid(r.Context(), event.Data.Raw)
	case "invoice.payment_failed":
		herr = h.onInvoicePaymentFailed(r.Context(), event.Data.Raw)
	}
	if herr != nil {
		logging.Error(packageName, "Webhook %s handler failed: %v", event.Type, herr)
		w.WriteHeader(http.StatusInternalServerError) // 500 → Stripe retries
		return
	}
	w.WriteHeader(http.StatusOK)
}

// onCheckoutSessionCompleted handles the pay-before-account funnel: a completed
// hosted Checkout means payment is secured. We hand the intent token + Stripe
// ids to the signup layer (via the wired hook) to mark the intent paid. The
// account itself is provisioned later at Step 6 (it needs a password +
// account-type the visitor hasn't supplied yet). Non-funnel sessions (no
// signup token) are ignored. The customer.subscription.* events for this
// subscription no-op until provisioning stamps tenant_id metadata on it.
func (h *Handlers) onCheckoutSessionCompleted(ctx context.Context, raw []byte) error {
	var obj struct {
		ID                string            `json:"id"`
		ClientReferenceID string            `json:"client_reference_id"`
		Customer          string            `json:"customer"`
		Subscription      string            `json:"subscription"`
		PaymentStatus     string            `json:"payment_status"`
		Metadata          map[string]string `json:"metadata"`
	}
	if err := json.Unmarshal(raw, &obj); err != nil {
		return err
	}
	token := obj.ClientReferenceID
	if token == "" {
		token = obj.Metadata["signup_token"]
	}
	if token == "" {
		return nil // not a funnel session
	}
	if obj.PaymentStatus != "" && obj.PaymentStatus != "paid" && obj.PaymentStatus != "no_payment_required" {
		logging.Info(packageName, "Webhook: signup checkout %s not paid (%s) — ignoring", obj.ID, obj.PaymentStatus)
		return nil
	}
	if h.signupPaid == nil {
		logging.Error(packageName, "checkout.session.completed but no signup hook wired (session=%s)", obj.ID)
		return nil
	}
	if err := h.signupPaid(ctx, token, obj.ID, obj.Subscription, obj.Customer); err != nil {
		return fmt.Errorf("signup paid hook: %w", err)
	}
	logging.Info(packageName, "Webhook: signup checkout %s paid (token=%s sub=%s)", obj.ID, token, obj.Subscription)
	return nil
}

// onSubscriptionEvent syncs subscription id/status/period-end onto the tenant
// and (when active) assigns the plan. Fetches the subscription fresh via the
// SDK so we read its current, version-stable shape.
func (h *Handlers) onSubscriptionEvent(ctx context.Context, raw []byte) error {
	var obj struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &obj); err != nil {
		return err
	}
	sub, err := subscription.Get(obj.ID, nil)
	if err != nil {
		return fmt.Errorf("get subscription %s: %w", obj.ID, err)
	}
	tenantID, _ := strconv.ParseInt(sub.Metadata["tenant_id"], 10, 64)
	if tenantID == 0 {
		// Pre-account funnel subscription: mark its intent paid when it goes
		// live (backup to the synchronous finalize step). The account + tenant
		// are provisioned later at Step 6, which stamps tenant_id on the sub.
		if tok := sub.Metadata["signup_token"]; tok != "" && h.signupSubActive != nil &&
			(sub.Status == "active" || sub.Status == "trialing") {
			custID := ""
			if sub.Customer != nil {
				custID = sub.Customer.ID
			}
			if err := h.signupSubActive(ctx, tok, sub.ID, custID); err != nil {
				return fmt.Errorf("signup sub activated hook: %w", err)
			}
		}
		return nil // not a tenant subscription
	}
	status := string(sub.Status)
	var periodEnd time.Time
	if sub.Items != nil && len(sub.Items.Data) > 0 && sub.Items.Data[0].CurrentPeriodEnd > 0 {
		periodEnd = time.Unix(sub.Items.Data[0].CurrentPeriodEnd, 0)
	}
	if err := h.svc.StoreSubscriptionState(ctx, tenantID, sub.ID, status, periodEnd, !sub.CancelAtPeriodEnd); err != nil {
		return err
	}
	logging.Info(packageName, "Webhook: tenant=%d subscription %s status=%s auto_renew=%t", tenantID, sub.ID, status, !sub.CancelAtPeriodEnd)
	// Reconcile purchased seats from the subscription (the seat line item is the
	// source of truth). A fully-ended subscription drops extra seats to 0.
	seats := SeatQuantityOf(sub)
	if status == "canceled" || status == "unpaid" {
		seats = 0
	}
	if err := h.svc.SetExtraSeats(ctx, tenantID, seats); err != nil {
		return err
	}
	// Lapse lifecycle. A fully-ended subscription (canceled at term-end with
	// auto-renew off, or dunning exhausted → unpaid) drops the account into the
	// 30-day read-only window. A healthy status revives it (covers re-subscribe
	// within the window). past_due/incomplete are mid-dunning — left untouched
	// so the account stays active behind the existing past-due banner.
	switch status {
	case "active", "trialing":
		if err := h.svc.ClearLapsed(ctx, tenantID); err != nil {
			return err
		}
	case "canceled", "unpaid":
		if err := h.svc.MarkLapsed(ctx, tenantID); err != nil {
			return err
		}
	}
	if code := sub.Metadata["plan_code"]; code != "" && (status == "active" || status == "trialing") {
		if err := h.assign(ctx, tenantID, code, sub.Metadata["billing_period"]); err != nil {
			return fmt.Errorf("assign tenant=%d plan=%s: %w", tenantID, code, err)
		}
		// Clear a pending scheduled change once it has actually applied.
		h.svc.ClearScheduledChangeIfApplied(ctx, tenantID, code)
		logging.Info(packageName, "Webhook: tenant=%d assigned %s via subscription", tenantID, code)
	}
	return nil
}

// onInvoicePaid records the order + sends our receipt for a paid subscription
// invoice (first charge and every renewal). Idempotent on the invoice id.
func (h *Handlers) onInvoicePaid(ctx context.Context, raw []byte) error {
	var obj struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &obj); err != nil {
		return err
	}
	inv, err := invoice.Get(obj.ID, nil)
	if err != nil {
		return fmt.Errorf("get invoice %s: %w", obj.ID, err)
	}
	if inv.Parent == nil || inv.Parent.SubscriptionDetails == nil {
		return nil // not a subscription invoice
	}
	meta := inv.Parent.SubscriptionDetails.Metadata
	tenantID, _ := strconv.ParseInt(meta["tenant_id"], 10, 64)
	if tenantID == 0 {
		return nil
	}
	rec, err := h.svc.RecordInvoiceOrder(
		ctx, tenantID, meta["plan_code"], meta["billing_period"],
		inv.AmountPaid, string(inv.Currency), inv.ID, inv.HostedInvoiceURL,
	)
	if err != nil {
		return err
	}
	logging.Info(packageName, "Webhook: tenant=%d recorded order %s (invoice %s)", tenantID, rec.OrderNumber, inv.ID)

	// Classify the receipt. A mid-term update whose prorations are all on the
	// seat line is a seat purchase — not a plan upgrade — so it gets its own
	// wording + the new seat total instead of a "your plan was upgraded" email.
	kind := receiptKind(string(inv.BillingReason))
	seatCount := 0
	if kind == "upgrade" && inv.Parent.SubscriptionDetails.Subscription != nil {
		if sub, sErr := subscription.Get(inv.Parent.SubscriptionDetails.Subscription.ID, nil); sErr == nil {
			seatItemID := ""
			for _, it := range sub.Items.Data {
				if it.Metadata != nil && it.Metadata["trilli_kind"] == "seat" {
					seatItemID = it.ID
				}
			}
			if seatItemID != "" && isSeatOnlyInvoice(inv, seatItemID) {
				kind = "seats"
				seatCount = int(SeatQuantityOf(sub))
			}
		}
	}
	// Receipt email — only on first record; failures are non-fatal (no 500/retry).
	if rec.IsNew && h.mailer != nil {
		if to := meta["buyer_email"]; to != "" {
			paidAt := time.Now()
			if inv.Created > 0 {
				paidAt = time.Unix(inv.Created, 0)
			}
				// Gross = the invoice's line total; credit = the portion covered by
				// account credit balance (e.g. proration credit from a prior plan
				// change), so the receipt can show "charged today" net of it.
				grossCents := inv.AmountPaid
				creditCents := int64(0)
				if inv.Total > inv.AmountDue && inv.AmountDue >= 0 {
					grossCents = inv.Total
					creditCents = inv.Total - inv.AmountDue
				}
				if mErr := h.mailer.SendReceipt(ctx, mailer.ReceiptEmail{
					To:            to,
					Name:          firstName(meta["buyer_name"]),
					PlanName:      meta["plan_name"],
					BillingPeriod: meta["billing_period"],
					AmountCents:   grossCents,
					CreditCents:   creditCents,
					Currency:      string(inv.Currency),
					OrderNumber:   rec.OrderNumber,
					ReceiptURL:    rec.ReceiptURL,
					PaidAt:        paidAt,
					Kind:          kind,
					SeatCount:     seatCount,
				}); mErr != nil {
				logging.Error(packageName, "Webhook receipt email failed (order=%s): %v", rec.OrderNumber, mErr)
			}
		}
	}
	return nil
}

// onInvoicePaymentFailed nudges the customer (email) to fix a failed renewal.
// Stripe Smart Retries own the automatic retry cadence; the subscription moves
// to past_due via customer.subscription.updated (handled above). Best-effort.
func (h *Handlers) onInvoicePaymentFailed(ctx context.Context, raw []byte) error {
	var obj struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &obj); err != nil {
		return err
	}
	inv, err := invoice.Get(obj.ID, nil)
	if err != nil {
		return fmt.Errorf("get invoice %s: %w", obj.ID, err)
	}
	if inv.Parent == nil || inv.Parent.SubscriptionDetails == nil {
		return nil
	}
	meta := inv.Parent.SubscriptionDetails.Metadata
	logging.Info(packageName, "Webhook: payment failed for tenant=%s invoice=%s", meta["tenant_id"], inv.ID)
	if h.mailer != nil {
		if to := meta["buyer_email"]; to != "" {
			if mErr := h.mailer.SendPaymentFailed(ctx, mailer.PaymentFailedEmail{
				To:          to,
				Name:        firstName(meta["buyer_name"]),
				PlanName:    meta["plan_name"],
				AmountCents: inv.AmountDue,
				Currency:    string(inv.Currency),
				InvoiceURL:  inv.HostedInvoiceURL,
			}); mErr != nil {
				logging.Error(packageName, "Webhook payment-failed email failed (invoice=%s): %v", inv.ID, mErr)
			}
		}
	}
	return nil
}

// onPaymentIntentSucceeded handles the legacy one-time PaymentIntent flow.
// No-ops for subscription PaymentIntents (they carry no tenant metadata here).
func (h *Handlers) onPaymentIntentSucceeded(ctx context.Context, raw []byte) error {
	var pi stripe.PaymentIntent
	if err := json.Unmarshal(raw, &pi); err != nil {
		return err
	}
	tenantID, _ := strconv.ParseInt(pi.Metadata["tenant_id"], 10, 64)
	code := pi.Metadata["plan_code"]
	if tenantID == 0 || code == "" {
		return nil
	}
	period := pi.Metadata["billing_period"]
	if err := h.assign(ctx, tenantID, code, period); err != nil {
		return fmt.Errorf("assign tenant=%d plan=%s: %w", tenantID, code, err)
	}
	logging.Info(packageName, "Webhook: tenant=%d assigned %s (%s)", tenantID, code, period)
	rec, err := h.svc.RecordTransaction(ctx, tenantID, &pi)
	if err != nil {
		logging.Error(packageName, "Webhook record transaction failed (tenant=%d): %v", tenantID, err)
		return nil // assignment already succeeded; don't 500 + retry
	}
	logging.Info(packageName, "Webhook: tenant=%d recorded order %s", tenantID, rec.OrderNumber)
	if rec.IsNew && h.mailer != nil {
		if to := pi.Metadata["buyer_email"]; to != "" {
			paidAt := time.Now()
			if pi.Created > 0 {
				paidAt = time.Unix(pi.Created, 0)
			}
			if mErr := h.mailer.SendReceipt(ctx, mailer.ReceiptEmail{
				To:            to,
				Name:          firstName(pi.Metadata["buyer_name"]),
				PlanName:      pi.Metadata["plan_name"],
				BillingPeriod: period,
				AmountCents:   pi.Amount,
				Currency:      string(pi.Currency),
				OrderNumber:   rec.OrderNumber,
				ReceiptURL:    rec.ReceiptURL,
				PaidAt:        paidAt,
			}); mErr != nil {
				logging.Error(packageName, "Webhook receipt email failed (order=%s): %v", rec.OrderNumber, mErr)
			}
		}
	}
	return nil
}

// isSeatOnlyInvoice reports whether every proration line on the invoice belongs
// to the seat item — i.e. the charge is purely a seat add/remove, not a plan
// change. Returns false when there are no proration lines.
func isSeatOnlyInvoice(inv *stripe.Invoice, seatItemID string) bool {
	if inv.Lines == nil {
		return false
	}
	sawProration := false
	for _, ln := range inv.Lines.Data {
		d := ln.Parent
		if d == nil || d.SubscriptionItemDetails == nil || !d.SubscriptionItemDetails.Proration {
			continue
		}
		sawProration = true
		if d.SubscriptionItemDetails.SubscriptionItem != seatItemID {
			return false // a non-seat item was prorated → it's a plan change
		}
	}
	return sawProration
}

// firstName returns the first whitespace-separated token of a name (for the
// email greeting), or "" when blank.
func firstName(name string) string {
	fields := strings.Fields(name)
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

// receiptKind maps Stripe's invoice billing_reason to the receipt wording:
// a mid-term plan change (subscription_update) is an upgrade with a prorated
// adjustment, a cycle is a renewal, everything else is a first purchase.
func receiptKind(billingReason string) string {
	switch billingReason {
	case "subscription_update":
		return "upgrade"
	case "subscription_cycle":
		return "renewal"
	default:
		return "purchase"
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
