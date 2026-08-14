package supporttickets

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"trilli/system/auth"
	"trilli/system/logging"
)

// Handlers exposes the customer-facing support ticket endpoints.
type Handlers struct {
	svc *Service
}

// NewHandlers builds the HTTP layer.
func NewHandlers(svc *Service) *Handlers {
	return &Handlers{svc: svc}
}

type muxLike interface {
	Handle(pattern string, handler http.Handler)
}

type errorResp struct {
	Error string `json:"error"`
}

// Register mounts the support routes. All require a session.
func (h *Handlers) Register(m muxLike, requireAuth func(http.Handler) http.Handler) {
	m.Handle("GET /api/support/tickets", requireAuth(http.HandlerFunc(h.List)))
	m.Handle("POST /api/support/tickets", requireAuth(http.HandlerFunc(h.Create)))
	m.Handle("GET /api/support/tickets/{ref}", requireAuth(http.HandlerFunc(h.Get)))
	m.Handle("POST /api/support/tickets/{ref}/messages", requireAuth(http.HandlerFunc(h.Reply)))
	m.Handle("POST /api/support/tickets/{ref}/status", requireAuth(http.HandlerFunc(h.Status)))
}

// isAdmin reports whether the caller sees the whole tenant's tickets.
func isAdmin(id *auth.Identity) bool {
	if id == nil || id.Membership == nil {
		return false
	}
	role := strings.ToLower(id.Membership.Role)
	return role == "owner" || role == "admin"
}

func (h *Handlers) List(w http.ResponseWriter, r *http.Request) {
	id, ok := auth.IdentityFromContext(r.Context())
	if !ok || id.Tenant == nil {
		writeJSON(w, http.StatusUnauthorized, errorResp{Error: "unauthorized"})
		return
	}
	tickets, err := h.svc.List(r.Context(), id.Tenant.ID, id.User.ID, isAdmin(id))
	if err != nil {
		logging.Error(packageName, "list: %v", err)
		writeJSON(w, http.StatusInternalServerError, errorResp{Error: "could not load tickets"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tickets": tickets, "scope": scopeOf(id)})
}

func (h *Handlers) Create(w http.ResponseWriter, r *http.Request) {
	id, ok := auth.IdentityFromContext(r.Context())
	if !ok || id.Tenant == nil {
		writeJSON(w, http.StatusUnauthorized, errorResp{Error: "unauthorized"})
		return
	}
	var req struct {
		Subject  string `json:"subject"`
		Body     string `json:"body"`
		Category string `json:"category"`
		Severity string `json:"severity"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResp{Error: "invalid JSON body"})
		return
	}

	t, err := h.svc.Create(r.Context(), CreateInput{
		TenantID:       id.Tenant.ID,
		UserID:         id.User.ID,
		RequesterEmail: id.User.Email,
		RequesterName:  requesterName(id),
		Subject:        req.Subject,
		Body:           req.Body,
		Category:       req.Category,
		Severity:       req.Severity,
	})
	if err != nil {
		writeJSON(w, statusForErr(err), errorResp{Error: userMessage(err)})
		return
	}
	writeJSON(w, http.StatusCreated, t)
}

func (h *Handlers) Get(w http.ResponseWriter, r *http.Request) {
	id, ok := auth.IdentityFromContext(r.Context())
	if !ok || id.Tenant == nil {
		writeJSON(w, http.StatusUnauthorized, errorResp{Error: "unauthorized"})
		return
	}
	ref := strings.TrimSpace(r.PathValue("ref"))
	if ref == "" {
		writeJSON(w, http.StatusBadRequest, errorResp{Error: "invalid ticket reference"})
		return
	}
	t, err := h.svc.Get(r.Context(), id.Tenant.ID, id.User.ID, ref, isAdmin(id))
	if err != nil {
		writeJSON(w, statusForErr(err), errorResp{Error: userMessage(err)})
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (h *Handlers) Reply(w http.ResponseWriter, r *http.Request) {
	id, ok := auth.IdentityFromContext(r.Context())
	if !ok || id.Tenant == nil {
		writeJSON(w, http.StatusUnauthorized, errorResp{Error: "unauthorized"})
		return
	}
	ref := strings.TrimSpace(r.PathValue("ref"))
	if ref == "" {
		writeJSON(w, http.StatusBadRequest, errorResp{Error: "invalid ticket reference"})
		return
	}
	var req struct {
		Body string `json:"body"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResp{Error: "invalid JSON body"})
		return
	}
	t, err := h.svc.AddCustomerMessage(r.Context(), id.Tenant.ID, id.User.ID, ref, isAdmin(id), requesterName(id), req.Body)
	if err != nil {
		writeJSON(w, statusForErr(err), errorResp{Error: userMessage(err)})
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (h *Handlers) Status(w http.ResponseWriter, r *http.Request) {
	id, ok := auth.IdentityFromContext(r.Context())
	if !ok || id.Tenant == nil {
		writeJSON(w, http.StatusUnauthorized, errorResp{Error: "unauthorized"})
		return
	}
	ref := strings.TrimSpace(r.PathValue("ref"))
	if ref == "" {
		writeJSON(w, http.StatusBadRequest, errorResp{Error: "invalid ticket reference"})
		return
	}
	var req struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResp{Error: "invalid JSON body"})
		return
	}
	// Customers may only close or reopen their own ticket from the UI.
	switch req.Status {
	case StatusClosed, StatusOpen:
	default:
		writeJSON(w, http.StatusBadRequest, errorResp{Error: "unsupported status change"})
		return
	}
	t, err := h.svc.SetStatus(r.Context(), id.Tenant.ID, id.User.ID, ref, isAdmin(id), req.Status)
	if err != nil {
		writeJSON(w, statusForErr(err), errorResp{Error: userMessage(err)})
		return
	}
	writeJSON(w, http.StatusOK, t)
}

// --- helpers ---

func requesterName(id *auth.Identity) string {
	if id == nil || id.User == nil {
		return ""
	}
	if n := strings.TrimSpace(deref(id.User.FullName)); n != "" {
		return n
	}
	first := strings.TrimSpace(deref(id.User.FirstName))
	last := strings.TrimSpace(deref(id.User.LastName))
	if combined := strings.TrimSpace(first + " " + last); combined != "" {
		return combined
	}
	return id.User.Email
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func scopeOf(id *auth.Identity) string {
	if isAdmin(id) {
		return "tenant"
	}
	return "mine"
}

func statusForErr(err error) int {
	switch {
	case errors.Is(err, ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, ErrValidation):
		return http.StatusBadRequest
	case errors.Is(err, ErrClosed):
		return http.StatusConflict
	default:
		return http.StatusInternalServerError
	}
}

func userMessage(err error) string {
	switch {
	case errors.Is(err, ErrNotFound):
		return "ticket not found"
	case errors.Is(err, ErrClosed):
		return "this ticket is closed — reopen it to reply"
	case errors.Is(err, ErrValidation):
		// Strip the sentinel prefix for a clean user-facing message.
		msg := err.Error()
		if i := strings.Index(msg, ": "); i >= 0 {
			return msg[i+2:]
		}
		return msg
	default:
		logging.Error(packageName, "unexpected: %v", err)
		return "something went wrong"
	}
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if body == nil {
		return
	}
	_ = json.NewEncoder(w).Encode(body)
}
