package reports

import (
	"encoding/json"
	"net/http"

	"trilli-cmx/system/logging"
	"trilli-cmx/system/operators"
)

// Handler exposes the Reports API (SPEC §6.6). Global-only, read-only.
type Handler struct {
	svc *Service
	mw  *operators.Handler
}

// NewHandler builds the reports Handler.
func NewHandler(svc *Service, mw *operators.Handler) *Handler {
	return &Handler{svc: svc, mw: mw}
}

// Register mounts the reports routes.
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/cmx/reports", h.mw.RequireGlobal(h.report))
}

func (h *Handler) report(w http.ResponseWriter, r *http.Request) {
	rep, err := h.svc.Build(r.Context())
	if err != nil {
		logging.Error(packageName, "build: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "Something went wrong."})
		return
	}
	writeJSON(w, http.StatusOK, rep)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
