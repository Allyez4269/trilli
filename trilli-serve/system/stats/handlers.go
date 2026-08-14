package stats

import (
	"encoding/json"
	"net/http"
	"time"

	"trilli/system/auth"
	"trilli/system/logging"
)

// Handlers exposes the Quick-Stats endpoint.
type Handlers struct {
	svc *Service
}

func NewHandlers(svc *Service) *Handlers {
	return &Handlers{svc: svc}
}

type muxLike interface {
	Handle(pattern string, handler http.Handler)
}

// Register wires GET /api/stats (session required).
func (h *Handlers) Register(m muxLike, requireAuth func(http.Handler) http.Handler) {
	m.Handle("GET /api/stats", requireAuth(http.HandlerFunc(h.Range)))
}

// Range handles GET /api/stats?from=YYYY-MM-DD&to=YYYY-MM-DD. Both dates are
// inclusive (UTC days). Defaults to the current calendar month when omitted.
func (h *Handlers) Range(w http.ResponseWriter, r *http.Request) {
	identity, ok := auth.IdentityFromContext(r.Context())
	if !ok || identity.Tenant == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}

	now := time.Now().UTC()
	defaultFrom := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	from := parseDay(r.URL.Query().Get("from"), defaultFrom)
	to := parseDay(r.URL.Query().Get("to"), now)
	if to.Before(from) {
		from, to = to, from
	}

	// Non-admin members see only their own data (transfer + their uploads);
	// admins, owners, and super-admins (no membership) see the account-wide view.
	var actor *int64
	if identity.User != nil && identity.Membership != nil && !auth.IsAdminOrOwner(identity.Membership.Role) {
		actor = &identity.User.ID
	}

	sum, err := h.svc.Range(r.Context(), identity.Tenant.ID, actor, from, to)
	if err != nil {
		logging.Error(packageName, "Range failed (tenant=%d): %v", identity.Tenant.ID, err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	writeJSON(w, http.StatusOK, sum)
}

// parseDay parses a YYYY-MM-DD date (UTC midnight), falling back to def.
func parseDay(s string, def time.Time) time.Time {
	if s == "" {
		return def
	}
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return def
	}
	return t.UTC()
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
