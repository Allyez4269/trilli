package catalog

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"trilli-cmx/system/appadmin"
	"trilli-cmx/system/logging"
	"trilli-cmx/system/operators"
)

// Handler exposes the Catalog API (SPEC §6.3). Reads are viewable by both CMX
// and Global admins; plan WRITES are Global-only and step-up gated, proxied to
// the app's admin surface.
type Handler struct {
	svc *Service
	mw  *operators.Handler
	ops *operators.Service
	app *appadmin.Client
}

// NewHandler builds the catalog Handler.
func NewHandler(svc *Service, mw *operators.Handler, ops *operators.Service, app *appadmin.Client) *Handler {
	return &Handler{svc: svc, mw: mw, ops: ops, app: app}
}

// Register mounts the catalog routes.
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/cmx/plans", h.mw.RequireAuth(h.listPlans))
	mux.HandleFunc("GET /api/cmx/plans/{id}", h.mw.RequireAuth(h.getPlan))

	// Plan editing — Global-only (SPEC §3) + fresh step-up 2FA (§6.9).
	mux.HandleFunc("POST /api/cmx/plans", h.mw.RequireGlobal(h.mw.RequireStepUp(h.createPlan)))
	mux.HandleFunc("PATCH /api/cmx/plans/{id}", h.mw.RequireGlobal(h.mw.RequireStepUp(h.updatePlan)))
	mux.HandleFunc("POST /api/cmx/plans/{id}/status", h.mw.RequireGlobal(h.mw.RequireStepUp(h.setPlanStatus)))
}

func (h *Handler) listPlans(w http.ResponseWriter, r *http.Request) {
	plans, err := h.svc.ListPlans(r.Context())
	if err != nil {
		logging.Error(packageName, "list plans: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "Something went wrong."})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"plans": plans})
}

func (h *Handler) getPlan(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid id"})
		return
	}
	p, err := h.svc.GetPlan(r.Context(), id)
	if errors.Is(err, ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "not found"})
		return
	}
	if err != nil {
		logging.Error(packageName, "get plan: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "Something went wrong."})
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
