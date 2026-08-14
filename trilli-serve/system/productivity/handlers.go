package productivity

import (
	"encoding/json"
	"net/http"

	"trilli/system/auth"
)

type muxLike interface {
	Handle(pattern string, handler http.Handler)
}

type Handlers struct {
	svc *Service
}

func NewHandlers(svc *Service) *Handlers { return &Handlers{svc: svc} }

func (h *Handlers) Register(m muxLike, requireAuth, requireAdmin func(http.Handler) http.Handler) {
	m.Handle("GET /api/productivity/settings", requireAuth(http.HandlerFunc(h.GetSettings)))
	m.Handle("PUT /api/productivity/prefs", requireAuth(http.HandlerFunc(h.SetPref)))
	m.Handle("PUT /api/productivity/settings", requireAdmin(http.HandlerFunc(h.UpdateSettings)))
}

type errorResp struct {
	Error string `json:"error"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// GetSettings returns the resolved Productivity view for the current user:
// whether they're allowed, the tenant default zoom, evolving settings, and the
// user's own prefs. The SPA resolves zoom = prefs.zoom ?? default_zoom ?? code.
func (h *Handlers) GetSettings(w http.ResponseWriter, r *http.Request) {
	id, ok := auth.IdentityFromContext(r.Context())
	if !ok || id.Tenant == nil || id.User == nil {
		writeJSON(w, http.StatusUnauthorized, errorResp{"unauthorized"})
		return
	}
	ts, err := h.svc.GetTenant(r.Context(), id.Tenant.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResp{"internal error"})
		return
	}
	prefs, err := h.svc.GetUserPrefs(r.Context(), id.User.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResp{"internal error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"allowed":      ts.IsAllowed(id.User.Email),
		"enabled":      ts.Enabled,
		"allowlist":    ts.Allowlist,
		"default_zoom": ts.DefaultZoom,
		"settings":     ts.Settings,
		"prefs":        prefs,
	})
}

// SetPref upserts a single per-user pref (e.g. zoom).
func (h *Handlers) SetPref(w http.ResponseWriter, r *http.Request) {
	id, ok := auth.IdentityFromContext(r.Context())
	if !ok || id.User == nil {
		writeJSON(w, http.StatusUnauthorized, errorResp{"unauthorized"})
		return
	}
	var req struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Key == "" {
		writeJSON(w, http.StatusBadRequest, errorResp{"invalid request"})
		return
	}
	if err := h.svc.SetUserPref(r.Context(), id.User.ID, req.Key, req.Value); err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResp{"internal error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// UpdateSettings writes the tenant's Productivity config (admin/owner only).
func (h *Handlers) UpdateSettings(w http.ResponseWriter, r *http.Request) {
	id, ok := auth.IdentityFromContext(r.Context())
	if !ok || id.Tenant == nil {
		writeJSON(w, http.StatusUnauthorized, errorResp{"unauthorized"})
		return
	}
	var in TenantSettings
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResp{"invalid request"})
		return
	}
	if err := h.svc.UpsertTenant(r.Context(), id.Tenant.ID, &in); err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResp{"internal error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
