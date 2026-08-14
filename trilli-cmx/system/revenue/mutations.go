package revenue

// mutations.go holds the Revenue money-WRITE handlers (SPEC §6.4): issue a
// refund, grant account credit, and reconcile a stale Stripe subscription. Each
// is Global-only + fresh step-up (wired in Register), proxies the write to the
// app's /api/admin surface via appadmin, and records the operator action in the
// CMX audit. Reading the credit ledger is a direct DB read (Option C).

import (
	"encoding/json"
	"net/http"
	"strconv"

	"trilli-cmx/system/appadmin"
	"trilli-cmx/system/operators"
)

// actor extracts the acting operator + request geo for the app call + audit.
func actor(r *http.Request) (*operators.Operator, operators.LoginContext, appadmin.ActingOperator) {
	op := operators.OperatorFrom(r.Context())
	lc := operators.LoginContext{}
	if sess := operators.SessionFrom(r.Context()); sess != nil {
		lc = operators.LoginContext{
			IP:  sess.IP,
			Geo: operators.Geo{ContinentCode: sess.ContinentCode, CountryCode: sess.CountryCode, Region: sess.Region},
		}
	}
	return op, lc, appadmin.ActingOperator{ID: op.ID, Email: op.Email}
}

func (h *Handler) audit(r *http.Request, op *operators.Operator, lc operators.LoginContext, action, summary string, tenantID int64, meta map[string]any) {
	_ = h.ops.Audit(r.Context(), op, lc, operators.AuditInput{
		Action:     action,
		TargetType: "tenant",
		TargetID:   strconv.FormatInt(tenantID, 10),
		TenantID:   &tenantID,
		Summary:    summary,
		Meta:       meta,
	})
}

func (h *Handler) listCredits(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	credits, total, err := h.svc.ListCredits(r.Context(), id)
	if err != nil {
		h.fail(w, "list credits", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"credits": credits, "total_cents": total})
}

func (h *Handler) refund(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var req struct {
		TransactionID int64 `json:"transaction_id"`
		AmountCents   int64 `json:"amount_cents"` // <= 0 = full remaining
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.TransactionID <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "transaction_id is required"})
		return
	}
	op, lc, act := actor(r)
	res, err := h.app.Refund(r.Context(), id, req.TransactionID, req.AmountCents, act)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}
	h.audit(r, op, lc, "revenue.refund",
		"Refunded "+dollars(res.RefundedCents)+" on tenant "+strconv.FormatInt(id, 10), id,
		map[string]any{"transaction_id": req.TransactionID, "refunded_cents": res.RefundedCents, "refund_id": res.RefundID})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "refunded_cents": res.RefundedCents, "refund_id": res.RefundID})
}

func (h *Handler) grantCredit(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var req struct {
		AmountCents int64  `json:"amount_cents"`
		Reason      string `json:"reason"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.AmountCents <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "amount must be greater than zero"})
		return
	}
	op, lc, act := actor(r)
	if err := h.app.GrantCredit(r.Context(), id, req.AmountCents, req.Reason, act); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}
	h.audit(r, op, lc, "revenue.credit",
		"Granted "+dollars(req.AmountCents)+" credit to tenant "+strconv.FormatInt(id, 10), id,
		map[string]any{"amount_cents": req.AmountCents, "reason": req.Reason})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (h *Handler) autoRenew(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	op, lc, act := actor(r)
	if err := h.app.SetAutoRenew(r.Context(), id, req.Enabled, act); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}
	verb := "Canceled auto-renew (lapses at term end) for tenant "
	if req.Enabled {
		verb = "Resumed auto-renew for tenant "
	}
	h.audit(r, op, lc, "revenue.auto_renew", verb+strconv.FormatInt(id, 10), id,
		map[string]any{"enabled": req.Enabled})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "auto_renew": req.Enabled})
}

func (h *Handler) changePlan(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var req struct {
		PlanCode      string `json:"plan_code"`
		BillingPeriod string `json:"billing_period"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.PlanCode == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "plan_code is required"})
		return
	}
	op, lc, act := actor(r)
	res, err := h.app.ChangePlan(r.Context(), id, req.PlanCode, req.BillingPeriod, act)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}
	h.audit(r, op, lc, "revenue.change_plan",
		"Changed plan to "+res.PlanName+" ("+res.Effect+") for tenant "+strconv.FormatInt(id, 10), id,
		map[string]any{"plan_code": req.PlanCode, "billing_period": req.BillingPeriod, "effect": res.Effect})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "effect": res.Effect, "effective_at": res.EffectiveAt, "plan_name": res.PlanName})
}

func (h *Handler) reconcile(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	op, lc, act := actor(r)
	if err := h.app.ReconcileSubscription(r.Context(), id, act); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}
	h.audit(r, op, lc, "revenue.reconcile",
		"Reconciled subscription for tenant "+strconv.FormatInt(id, 10), id, nil)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// dollars renders cents as a $-string for audit summaries.
func dollars(cents int64) string {
	neg := ""
	if cents < 0 {
		neg = "-"
		cents = -cents
	}
	return neg + "$" + strconv.FormatInt(cents/100, 10) + "." + leftpad2(cents%100)
}

func leftpad2(n int64) string {
	s := strconv.FormatInt(n, 10)
	if len(s) < 2 {
		return "0" + s
	}
	return s
}

// pathID + decodeJSON mirror the directory package's helpers (kept package-local
// to avoid a cross-package dependency for two tiny utilities).
func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid id"})
		return 0, false
	}
	return id, true
}

func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(v); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid request body"})
		return false
	}
	return true
}
