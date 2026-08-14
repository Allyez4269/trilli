package revenue

import (
	"encoding/json"
	"net/http"
	"strconv"

	"trilli-cmx/system/appadmin"
	"trilli-cmx/system/logging"
	"trilli-cmx/system/operators"
)

// Handler exposes the Revenue API (SPEC §6.4). Revenue is a Global-only section
// (SPEC §3 / §5.4.1): every endpoint requires a Global operator. Reads are pure
// (no step-up); the money-WRITES (refund, grant credit, reconcile subscription)
// additionally require a fresh step-up 2FA and are proxied to the app's admin
// surface (Option C), with the operator action recorded in the CMX audit.
type Handler struct {
	svc *Service
	mw  *operators.Handler
	ops *operators.Service
	app *appadmin.Client
}

// NewHandler builds the revenue Handler.
func NewHandler(svc *Service, mw *operators.Handler, ops *operators.Service, app *appadmin.Client) *Handler {
	return &Handler{svc: svc, mw: mw, ops: ops, app: app}
}

// Register mounts the revenue routes.
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/cmx/revenue/overview", h.mw.RequireGlobal(h.overview))
	mux.HandleFunc("GET /api/cmx/revenue/subscriptions", h.mw.RequireGlobal(h.subscriptions))
	mux.HandleFunc("GET /api/cmx/revenue/transactions", h.mw.RequireGlobal(h.transactions))
	mux.HandleFunc("GET /api/cmx/revenue/past-due", h.mw.RequireGlobal(h.pastDue))
	mux.HandleFunc("GET /api/cmx/revenue/signup-intents", h.mw.RequireGlobal(h.signupIntents))
	mux.HandleFunc("GET /api/cmx/revenue/tenants/{id}/credits", h.mw.RequireGlobal(h.listCredits))

	// Money-writes — Global + fresh step-up 2FA (§6.9), proxied + audited.
	mux.HandleFunc("POST /api/cmx/revenue/tenants/{id}/refund",
		h.mw.RequireGlobal(h.mw.RequireStepUp(h.refund)))
	mux.HandleFunc("POST /api/cmx/revenue/tenants/{id}/credits",
		h.mw.RequireGlobal(h.mw.RequireStepUp(h.grantCredit)))
	mux.HandleFunc("POST /api/cmx/revenue/tenants/{id}/reconcile-subscription",
		h.mw.RequireGlobal(h.mw.RequireStepUp(h.reconcile)))
	mux.HandleFunc("POST /api/cmx/revenue/tenants/{id}/auto-renew",
		h.mw.RequireGlobal(h.mw.RequireStepUp(h.autoRenew)))
	mux.HandleFunc("POST /api/cmx/revenue/tenants/{id}/change-plan",
		h.mw.RequireGlobal(h.mw.RequireStepUp(h.changePlan)))
}

func (h *Handler) overview(w http.ResponseWriter, r *http.Request) {
	o, err := h.svc.Overview(r.Context())
	if err != nil {
		h.fail(w, "overview", err)
		return
	}
	writeJSON(w, http.StatusOK, o)
}

func (h *Handler) subscriptions(w http.ResponseWriter, r *http.Request) {
	subs, err := h.svc.ListSubscriptions(r.Context())
	if err != nil {
		h.fail(w, "subscriptions", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"subscriptions": subs})
}

func (h *Handler) transactions(w http.ResponseWriter, r *http.Request) {
	var tenantID int64
	if v := r.URL.Query().Get("tenant_id"); v != "" {
		tenantID, _ = strconv.ParseInt(v, 10, 64)
	}
	limit := 0
	if v := r.URL.Query().Get("limit"); v != "" {
		limit, _ = strconv.Atoi(v)
	}
	txs, err := h.svc.ListTransactions(r.Context(), tenantID, limit)
	if err != nil {
		h.fail(w, "transactions", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"transactions": txs})
}

func (h *Handler) pastDue(w http.ResponseWriter, r *http.Request) {
	accts, err := h.svc.ListPastDue(r.Context())
	if err != nil {
		h.fail(w, "past-due", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"accounts": accts})
}

func (h *Handler) signupIntents(w http.ResponseWriter, r *http.Request) {
	intents, err := h.svc.ListSignupIntents(r.Context())
	if err != nil {
		h.fail(w, "signup-intents", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"intents": intents})
}

func (h *Handler) fail(w http.ResponseWriter, what string, err error) {
	logging.Error(packageName, "%s: %v", what, err)
	writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "Something went wrong."})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
