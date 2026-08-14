package audit

import (
	"encoding/json"
	"net/http"
	"strconv"

	"trilli/system/auth"
	"trilli/system/logging"
)

// Handlers exposes the read side of the audit log.
type Handlers struct {
	svc *Service
}

func NewHandlers(svc *Service) *Handlers {
	return &Handlers{svc: svc}
}

type muxLike interface {
	Handle(pattern string, handler http.Handler)
}

// Register wires the Activity read endpoint. It only requires an authenticated
// session: admins/owners get the full workspace audit, while a non-admin member
// is scoped (in List) to their own events.
func (h *Handlers) Register(m muxLike, requireAuth func(http.Handler) http.Handler) {
	m.Handle("GET /api/activity", requireAuth(http.HandlerFunc(h.List)))
}

type errorResp struct {
	Error string `json:"error"`
}

// List handles GET /api/activity?range=&action=&q=&offset=&limit=.
func (h *Handlers) List(w http.ResponseWriter, r *http.Request) {
	identity, ok := auth.IdentityFromContext(r.Context())
	if !ok || identity.Tenant == nil {
		writeJSON(w, http.StatusUnauthorized, errorResp{Error: "unauthorized"})
		return
	}
	q := r.URL.Query()
	offset, _ := strconv.Atoi(q.Get("offset"))
	limit, _ := strconv.Atoi(q.Get("limit"))
	// Non-admin members may only see their own events; admins/owners (and
	// super-admins with no membership) see the whole workspace.
	var actor *int64
	if identity.User != nil && identity.Membership != nil && !auth.IsAdminOrOwner(identity.Membership.Role) {
		actor = &identity.User.ID
	}

	res, err := h.svc.List(r.Context(), identity.Tenant.ID, ListFilter{
		Range:       q.Get("range"),
		Action:      q.Get("action"),
		Query:       q.Get("q"),
		ActorUserID: actor,
		Offset:      offset,
		Limit:       limit,
	})
	if err != nil {
		logging.Error(packageName, "List failed: %v", err)
		writeJSON(w, http.StatusInternalServerError, errorResp{Error: "internal error"})
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if body == nil {
		return
	}
	if err := json.NewEncoder(w).Encode(body); err != nil {
		logging.Error(packageName, "writeJSON encode failed: %v", err)
	}
}
