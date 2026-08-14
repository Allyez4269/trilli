package admin

import (
	"encoding/json"
	"net/http"
	"strconv"

	"trilli-cmx/system/logging"
	"trilli-cmx/system/operators"
)

// Handler exposes the Administration API (SPEC §6.7). The audit viewer is
// available to any operator (the operators service scopes the rows: Global sees
// all, CMX sees only their own). The vault inventory is Global-only.
type Handler struct {
	svc *Service
	mw  *operators.Handler
	ops *operators.Service
}

// NewHandler builds the admin Handler.
func NewHandler(svc *Service, mw *operators.Handler, ops *operators.Service) *Handler {
	return &Handler{svc: svc, mw: mw, ops: ops}
}

// Register mounts the Administration routes.
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/cmx/admin/audit", h.mw.RequireAuth(h.auditList))
	mux.HandleFunc("GET /api/cmx/admin/vault", h.mw.RequireGlobal(h.vault))
	h.registerOperators(mux)
}

func (h *Handler) auditList(w http.ResponseWriter, r *http.Request) {
	viewer := operators.OperatorFrom(r.Context())
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	entries, err := h.ops.ListAudit(r.Context(), viewer, limit)
	if err != nil {
		logging.Error(packageName, "list audit: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "Something went wrong."})
		return
	}
	if entries == nil {
		entries = []operators.AuditEntry{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": entries, "scope": string(viewer.Role)})
}

func (h *Handler) vault(w http.ResponseWriter, r *http.Request) {
	entries, err := h.svc.ListVault(r.Context())
	if err != nil {
		logging.Error(packageName, "list vault: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "Something went wrong."})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": entries})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
