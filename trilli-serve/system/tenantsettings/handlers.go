package tenantsettings

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"trilli/system/auth"
	"trilli/system/logging"
)

// Handlers exposes the workspace-settings HTTP surface.
type Handlers struct {
	svc *Service
}

// NewHandlers wraps a Service.
func NewHandlers(svc *Service) *Handlers {
	return &Handlers{svc: svc}
}

type muxLike interface {
	Handle(pattern string, handler http.Handler)
}

// Register wires the invite-settings routes. Both are owner/admin only — the
// caller passes auth's RequireAdminOrOwner middleware. (Owner-only enforcement
// of the allow-admin-invites field happens inside Update, since admins may
// still edit the other fields.)
func (h *Handlers) Register(m muxLike, requireAdminOrOwner func(http.Handler) http.Handler) {
	m.Handle("GET /api/workspace/invite-settings", requireAdminOrOwner(http.HandlerFunc(h.Get)))
	m.Handle("PATCH /api/workspace/invite-settings", requireAdminOrOwner(http.HandlerFunc(h.Update)))
}

func (h *Handlers) Get(w http.ResponseWriter, r *http.Request) {
	identity, ok := auth.IdentityFromContext(r.Context())
	if !ok || identity.Tenant == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	st, err := h.svc.Get(r.Context(), identity.Tenant.ID)
	if err != nil {
		logging.Error(packageName, "Get failed: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (h *Handlers) Update(w http.ResponseWriter, r *http.Request) {
	identity, ok := auth.IdentityFromContext(r.Context())
	if !ok || identity.Tenant == nil || identity.Membership == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	var in Settings
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}
	isOwner := strings.EqualFold(strings.TrimSpace(identity.Membership.Role), "owner")
	st, err := h.svc.Update(r.Context(), identity.Tenant.ID, isOwner, in)
	if err != nil {
		switch {
		case errors.Is(err, ErrInvalidRole):
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error": "Default role must be Admin, Member, or Viewer.",
			})
		case errors.Is(err, ErrInvalidDomain):
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error": "One of the domains isn't valid — use bare domains like acme.com (no @ or spaces).",
			})
		default:
			logging.Error(packageName, "Update failed: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		}
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		logging.Error(packageName, "writeJSON encode failed: %v", err)
	}
}
