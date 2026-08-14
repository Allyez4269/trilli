package adminapi

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"trilli/system/billing"
	"trilli/system/comp"
	"trilli/system/credentials"
	"trilli/system/logging"
	"trilli/system/mailer"
	"trilli/system/plans"
	"trilli/system/signupintents"
	"trilli/system/storage/tiering"
	"trilli/system/supporttickets"
)

// Service-principal credential coordinates in the vault. CMX sends this token in
// the X-CMX-Service-Token header; the app validates it here.
const (
	credProvider = "cmx"
	credKeyName  = "service_token"
	credEnv      = "live"

	headerToken    = "X-CMX-Service-Token"
	headerOperator = "X-CMX-Operator-Email"
	headerOpID     = "X-CMX-Operator-Id"
	headerAction   = "X-CMX-Action"
)

// Handlers exposes the /api/admin/* surface.
type Handlers struct {
	svc     *Service
	creds   *credentials.Service
	plans   *plans.Service
	comp    *comp.Service
	signup  *signupintents.Service
	mailer  *mailer.Mailer
	billing *billing.Service
	support *supporttickets.Service

	// Infrastructure write ops (SPEC §6.5), attached via WithInfra after the
	// coordinator + job registry + tiering runner are built in main.
	coord   jobCoordinator
	jobs    map[string]func(context.Context) error
	tierRun func(context.Context, bool) (tiering.Summary, bool, error)
}

// jobCoordinator is the subset of cluster.Coordinator used for run-now.
type jobCoordinator interface {
	RunExclusive(ctx context.Context, job string, fn func(context.Context) error) (bool, error)
	NodeID() string
}

// WithInfra wires the Infrastructure write dependencies (job registry +
// coordinator for run-now, and the on-demand tiering runner). Called once at
// startup, before the server accepts requests.
func (h *Handlers) WithInfra(coord jobCoordinator, jobs map[string]func(context.Context) error, tierRun func(context.Context, bool) (tiering.Summary, bool, error)) *Handlers {
	h.coord = coord
	h.jobs = jobs
	h.tierRun = tierRun
	return h
}

// NewHandlers builds the adminapi Handlers.
func NewHandlers(svc *Service, creds *credentials.Service, plansSvc *plans.Service, compSvc *comp.Service, signupSvc *signupintents.Service, m *mailer.Mailer, billingSvc *billing.Service, supportSvc *supporttickets.Service) *Handlers {
	return &Handlers{svc: svc, creds: creds, plans: plansSvc, comp: compSvc, signup: signupSvc, mailer: m, billing: billingSvc, support: supportSvc}
}

// muxLike is the subset of the app server used for registration.
type muxLike interface {
	HandleFunc(pattern string, handler func(http.ResponseWriter, *http.Request))
}

// Register mounts the admin routes, each wrapped in the service-principal guard.
func (h *Handlers) Register(m muxLike) {
	m.HandleFunc("POST /api/admin/tenants/{id}/suspend", h.requirePrincipal(h.suspend))
	m.HandleFunc("POST /api/admin/tenants/{id}/reactivate", h.requirePrincipal(h.reactivate))
	m.HandleFunc("POST /api/admin/tenants/{id}/quota-override", h.requirePrincipal(h.quotaOverride))

	// Catalog: plan create/edit/status (SPEC §6.3).
	m.HandleFunc("POST /api/admin/plans", h.requirePrincipal(h.createPlan))
	m.HandleFunc("PATCH /api/admin/plans/{id}", h.requirePrincipal(h.updatePlan))
	m.HandleFunc("POST /api/admin/plans/{id}/status", h.requirePrincipal(h.setPlanStatus))

	// Comp / ambassador invites (SPEC §6.10).
	m.HandleFunc("POST /api/admin/comp-invites", h.requirePrincipal(h.createCompInvite))
	m.HandleFunc("POST /api/admin/comp-invites/{id}/revoke", h.requirePrincipal(h.revokeCompInvite))
	m.HandleFunc("POST /api/admin/comp-invites/{id}/delete", h.requirePrincipal(h.deleteCompInvite))

	// Revenue money-writes (SPEC §6.4): refund, grant credit, reconcile sub.
	m.HandleFunc("POST /api/admin/tenants/{id}/refund", h.requirePrincipal(h.refund))
	m.HandleFunc("POST /api/admin/tenants/{id}/credits", h.requirePrincipal(h.grantCredit))
	m.HandleFunc("POST /api/admin/tenants/{id}/reconcile-subscription", h.requirePrincipal(h.reconcileSubscription))

	// Subscription changes on the customer's behalf (SPEC §6.4): cancel/resume
	// (auto-renew) and change plan.
	m.HandleFunc("POST /api/admin/tenants/{id}/auto-renew", h.requirePrincipal(h.setAutoRenew))
	m.HandleFunc("POST /api/admin/tenants/{id}/change-plan", h.requirePrincipal(h.changePlan))

	// Support desk (SPEC §6.8): operator reply / internal note on a ticket.
	m.HandleFunc("POST /api/admin/tickets/{id}/reply", h.requirePrincipal(h.ticketReply))

	// Infrastructure write ops (SPEC §6.5): run-now a background job, on-demand
	// tiering dry-run/apply.
	m.HandleFunc("POST /api/admin/jobs/{job}/run", h.requirePrincipal(h.runJob))
	m.HandleFunc("POST /api/admin/tiering/run", h.requirePrincipal(h.runTiering))
}

// requirePrincipal validates the CMX service token (constant-time) against the
// vault and logs the acting operator. Without a stored token the surface is
// closed (503) — it must be provisioned before CMX can call it.
func (h *Handlers) requirePrincipal(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		expected, err := h.creds.Get(r.Context(), credProvider, credKeyName, credEnv)
		if errors.Is(err, credentials.ErrNotFound) || expected == "" {
			logging.Error(packageName, "no CMX service token provisioned; /api/admin is closed")
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "admin surface not configured"})
			return
		}
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal error"})
			return
		}
		got := r.Header.Get(headerToken)
		if got == "" || subtle.ConstantTimeCompare([]byte(got), []byte(expected)) != 1 {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
			return
		}
		// Attribution: the acting operator is carried in headers (the app's own
		// audit_events is tenant/file-scoped; CMX holds the operator-action log).
		logging.Info(packageName, "admin action=%q operator=%q(%s) %s %s",
			r.Header.Get(headerAction), r.Header.Get(headerOperator), r.Header.Get(headerOpID),
			r.Method, r.URL.Path)
		next(w, r)
	}
}

func (h *Handlers) suspend(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	status, err := h.svc.SuspendTenant(r.Context(), id)
	respondStatus(w, status, err)
}

func (h *Handlers) reactivate(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	status, err := h.svc.ReactivateTenant(r.Context(), id)
	respondStatus(w, status, err)
}

func (h *Handlers) quotaOverride(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var req struct {
		// Bytes is the override; null/omitted clears it (revert to plan).
		Bytes *int64 `json:"bytes"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid request body"})
		return
	}
	val, err := h.svc.SetQuotaOverride(r.Context(), id, req.Bytes)
	if errors.Is(err, ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "tenant not found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "quota_override_bytes": val})
}

func respondStatus(w http.ResponseWriter, status string, err error) {
	if errors.Is(err, ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "tenant not found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "status": status})
}

func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid id"})
		return 0, false
	}
	return id, true
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
