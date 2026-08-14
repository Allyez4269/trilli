package notifications

import (
	"encoding/json"
	"net/http"

	"trilli/system/auth"
	"trilli/system/logging"
)

// Handlers exposes the per-user notification preference endpoints.
type Handlers struct {
	svc *Service
}

// NewHandlers wraps the notifications Service.
func NewHandlers(svc *Service) *Handlers {
	return &Handlers{svc: svc}
}

type muxLike interface {
	Handle(pattern string, handler http.Handler)
}

// Register wires the routes (all require a session).
func (h *Handlers) Register(m muxLike, requireAuth func(http.Handler) http.Handler) {
	m.Handle("GET /api/me/notifications", requireAuth(http.HandlerFunc(h.Get)))
	m.Handle("PUT /api/me/notifications", requireAuth(http.HandlerFunc(h.Update)))
}

// Get handles GET /api/me/notifications — the user's full preference map.
func (h *Handlers) Get(w http.ResponseWriter, r *http.Request) {
	identity, ok := auth.IdentityFromContext(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	prefs, err := h.svc.Get(r.Context(), identity.User.ID)
	if err != nil {
		logging.Error(packageName, "Get failed: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"prefs": prefs})
}

// Update handles PUT /api/me/notifications. Body: {"prefs": {"key": bool, ...}}.
// Records the change in the consent ledger with the session's ip + geo + time.
func (h *Handlers) Update(w http.ResponseWriter, r *http.Request) {
	identity, ok := auth.IdentityFromContext(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	var req struct {
		Prefs map[string]bool `json:"prefs"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}

	// Capture audit context from the current session (already-resolved geo,
	// IPv4-preferred IP). Falls back to the request's client IP if needed.
	audit := AuditContext{IP: auth.ClientIP(r)}
	if identity.Session != nil {
		if identity.Session.IPv4 != "" {
			audit.IP = identity.Session.IPv4
		} else if identity.Session.IP != "" {
			audit.IP = identity.Session.IP
		}
		audit.CountryCode = identity.Session.Geo.CountryCode
		audit.Country = identity.Session.Geo.Country
		audit.Region = identity.Session.Geo.Region
		audit.City = identity.Session.Geo.City
		audit.Latitude = identity.Session.Geo.Latitude
		audit.Longitude = identity.Session.Geo.Longitude
	}

	prefs, err := h.svc.Update(r.Context(), identity.User.ID, req.Prefs, audit)
	if err != nil {
		logging.Error(packageName, "Update failed: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"prefs": prefs})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		logging.Error(packageName, "writeJSON encode failed: %v", err)
	}
}
