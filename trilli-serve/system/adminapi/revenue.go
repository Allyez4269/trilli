package adminapi

// revenue.go implements the §6.4 money-write admin endpoints: refund a charge,
// grant account credit, and reconcile a stale Stripe subscription. Each delegates
// to billing.Service (which holds the Stripe client) and attributes the acting
// operator via the X-CMX-Operator-* headers already validated in requirePrincipal.

import (
	"encoding/json"
	"errors"
	"net/http"

	"trilli/system/billing"
	"trilli/system/logging"
)

// billingReady guards the money endpoints: without a configured Stripe client
// there is nothing to refund/credit/reconcile against.
func (h *Handlers) billingReady(w http.ResponseWriter) bool {
	if h.billing == nil || !h.billing.Enabled() {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "billing not configured"})
		return false
	}
	return true
}

// refund issues a refund against one of the tenant's succeeded transactions.
// Body: {transaction_id, amount_cents}. amount_cents <= 0 means full remaining.
func (h *Handlers) refund(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if !h.billingReady(w) {
		return
	}
	var req struct {
		TransactionID int64 `json:"transaction_id"`
		AmountCents   int64 `json:"amount_cents"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid request body"})
		return
	}
	if req.TransactionID <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "transaction_id is required"})
		return
	}
	res, err := h.billing.AdminRefund(r.Context(), id, req.TransactionID, req.AmountCents)
	if errors.Is(err, billing.ErrTransactionNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "transaction not found"})
		return
	}
	if errors.Is(err, billing.ErrNotRefundable) {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "charge has nothing left to refund"})
		return
	}
	if err != nil {
		logging.Error(packageName, "refund tenant=%d: %v", id, err)
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "refund failed at the payment processor"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "refunded_cents": res.RefundedCents, "refund_id": res.RefundID})
}

// grantCredit posts account credit. Body: {amount_cents, reason}.
func (h *Handlers) grantCredit(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if !h.billingReady(w) {
		return
	}
	var req struct {
		AmountCents int64  `json:"amount_cents"`
		Reason      string `json:"reason"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid request body"})
		return
	}
	if req.AmountCents <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "amount_cents must be > 0"})
		return
	}
	g, err := h.billing.GrantCredit(r.Context(), id, req.AmountCents, req.Reason,
		r.Header.Get(headerOpID), r.Header.Get(headerOperator))
	if err != nil {
		logging.Error(packageName, "grantCredit tenant=%d: %v", id, err)
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "could not grant credit at the payment processor"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "credit": g})
}

// setAutoRenew cancels (enabled=false → Stripe cancel_at_period_end) or resumes
// (enabled=true) the subscription on the customer's behalf. Body: {enabled}.
func (h *Handlers) setAutoRenew(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if !h.billingReady(w) {
		return
	}
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid request body"})
		return
	}
	if err := h.billing.SetAutoRenew(r.Context(), id, req.Enabled); err != nil {
		if errors.Is(err, billing.ErrNoCardForRenew) {
			writeJSON(w, http.StatusConflict, map[string]any{"error": billing.ErrNoCardForRenew.Error()})
			return
		}
		logging.Error(packageName, "setAutoRenew tenant=%d: %v", id, err)
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "could not update auto-renew at the payment processor"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "auto_renew": req.Enabled})
}

// changePlan moves the subscription to a new plan/period on the customer's
// behalf. Upgrades take effect immediately (prorated); downgrades are scheduled
// to term-end. Body: {plan_code, billing_period}.
func (h *Handlers) changePlan(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if !h.billingReady(w) {
		return
	}
	var req struct {
		PlanCode      string `json:"plan_code"`
		BillingPeriod string `json:"billing_period"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid request body"})
		return
	}
	if req.PlanCode == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "plan_code is required"})
		return
	}
	res, err := h.billing.ChangeSubscription(r.Context(), id, req.PlanCode, req.BillingPeriod)
	switch {
	case errors.Is(err, billing.ErrNoSubscription):
		writeJSON(w, http.StatusConflict, map[string]any{"error": "this account has no active subscription to change"})
		return
	case errors.Is(err, billing.ErrSamePlan):
		writeJSON(w, http.StatusConflict, map[string]any{"error": "account is already on this plan"})
		return
	case errors.Is(err, billing.ErrPlanUnavailable):
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "that plan is not available"})
		return
	case errors.Is(err, billing.ErrNoPrice):
		writeJSON(w, http.StatusConflict, map[string]any{"error": "that plan has no Stripe price configured"})
		return
	case err != nil:
		logging.Error(packageName, "changePlan tenant=%d: %v", id, err)
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "could not change the plan at the payment processor"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "effect": res.Effect, "effective_at": res.EffectiveAt, "plan_name": res.PlanName,
	})
}

// reconcileSubscription force-pulls the tenant's subscription from Stripe.
func (h *Handlers) reconcileSubscription(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if !h.billingReady(w) {
		return
	}
	res, err := h.billing.ReconcileSubscription(r.Context(), id)
	if errors.Is(err, billing.ErrNoSubscription) {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "this account has no Stripe subscription"})
		return
	}
	if err != nil {
		logging.Error(packageName, "reconcileSubscription tenant=%d: %v", id, err)
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "could not reconcile with the payment processor"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "subscription": res})
}
