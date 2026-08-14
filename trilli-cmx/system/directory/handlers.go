package directory

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"trilli-cmx/system/appadmin"
	"trilli-cmx/system/logging"
	"trilli-cmx/system/operators"
)

// Handler exposes the Customers + Accounts read APIs (SPEC §6.1/§6.2). All
// routes require an authenticated operator; both CMX and Global admins may view
// (SPEC §5.4.1). Note creation is the one write — to the CMX-owned notes table —
// and is recorded to the operator audit.
type Handler struct {
	svc *Service
	ops *operators.Service
	mw  *operators.Handler
	app *appadmin.Client
}

// NewHandler builds the directory Handler. mw provides RequireAuth; ops is used
// for the operator-action audit; app is the client for guarded tenant
// mutations (the Option-C write path).
func NewHandler(svc *Service, ops *operators.Service, mw *operators.Handler, app *appadmin.Client) *Handler {
	return &Handler{svc: svc, ops: ops, mw: mw, app: app}
}

// Register mounts the directory routes onto the mux.
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/cmx/customers", h.mw.RequireAuth(h.listCustomers))
	mux.HandleFunc("GET /api/cmx/customers/{id}", h.mw.RequireAuth(h.getCustomer))
	mux.HandleFunc("GET /api/cmx/customers/{id}/consent", h.mw.RequireAuth(h.getConsent))
	mux.HandleFunc("POST /api/cmx/customers/{id}/notes", h.mw.RequireAuth(h.addNote))
	mux.HandleFunc("GET /api/cmx/tenants", h.mw.RequireAuth(h.listTenants))
	mux.HandleFunc("GET /api/cmx/tenants/{id}", h.mw.RequireAuth(h.getTenant))

	// Tenant mutations (Option-C write path → app /api/admin/*). Consequential,
	// so gated behind fresh step-up 2FA (SPEC §6.9) on top of RequireAuth.
	mux.HandleFunc("POST /api/cmx/tenants/{id}/suspend",
		h.mw.RequireAuth(h.mw.RequireStepUp(h.suspendTenant)))
	mux.HandleFunc("POST /api/cmx/tenants/{id}/reactivate",
		h.mw.RequireAuth(h.mw.RequireStepUp(h.reactivateTenant)))
	mux.HandleFunc("POST /api/cmx/tenants/{id}/quota-override",
		h.mw.RequireAuth(h.mw.RequireStepUp(h.quotaOverride)))
}

func (h *Handler) listCustomers(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	items, err := h.svc.ListCustomers(r.Context(), q, limit)
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"customers": items})
}

func (h *Handler) getConsent(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	changes, err := h.svc.ListConsentChanges(r.Context(), id)
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"changes": changes})
}

func (h *Handler) getCustomer(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	cust, err := h.svc.GetCustomer(r.Context(), id)
	if errors.Is(err, ErrNotFound) {
		notFound(w)
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, cust)
}

func (h *Handler) addNote(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	op := operators.OperatorFrom(r.Context())
	var req struct {
		Body string `json:"body"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	note, err := h.svc.AddNote(r.Context(), id, op.ID, op.Email, req.Body)
	if errors.Is(err, ErrNotFound) {
		notFound(w)
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	// Record the operator action (append-only audit, SPEC §6.7).
	if sess := operators.SessionFrom(r.Context()); sess != nil {
		_ = h.ops.Audit(r.Context(), op, operators.LoginContext{
			IP:  sess.IP,
			Geo: operators.Geo{ContinentCode: sess.ContinentCode, CountryCode: sess.CountryCode, Region: sess.Region},
		}, operators.AuditInput{
			Action:     "customer.note.add",
			TargetType: "customer",
			TargetID:   strconv.FormatInt(id, 10),
			Summary:    "Added a note to customer " + strconv.FormatInt(id, 10),
		})
	}
	writeJSON(w, http.StatusOK, note)
}

func (h *Handler) listTenants(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	status := r.URL.Query().Get("status")
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	items, err := h.svc.ListTenants(r.Context(), q, status, limit)
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tenants": items})
}

func (h *Handler) getTenant(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	t, err := h.svc.GetTenant(r.Context(), id)
	if errors.Is(err, ErrNotFound) {
		notFound(w)
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, t)
}

// ---- helpers ----

func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid id"})
		return 0, false
	}
	return id, true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// decodeJSON decodes a JSON request body (size-capped), writing a 400 on error.
func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(v); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid request body"})
		return false
	}
	return true
}

func notFound(w http.ResponseWriter) {
	writeJSON(w, http.StatusNotFound, map[string]any{"error": "not found"})
}

func serverError(w http.ResponseWriter, err error) {
	logging.Error(packageName, "request error: %v", err)
	writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "Something went wrong."})
}
