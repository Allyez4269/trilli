package invites

import (
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"trilli/system/auth"
	"trilli/system/logging"
	"trilli/system/mailer"
	"trilli/system/ratelimit"
	"trilli/system/session"
)

// Handlers is the HTTP surface over the invites Service.
type Handlers struct {
	svc    *Service
	mailer *mailer.Mailer
}

// NewHandlers returns a wired HTTP handler set. The mailer is required
// (use mailer.New() at startup); pass nil only in tests that don't
// exercise the Create path.
func NewHandlers(svc *Service, m *mailer.Mailer) *Handlers {
	return &Handlers{svc: svc, mailer: m}
}

type muxLike interface {
	HandleFunc(pattern string, handler func(http.ResponseWriter, *http.Request))
	Handle(pattern string, handler http.Handler)
}

// Register wires the routes. /api/invites and DELETE /api/invites/:id
// require auth (any tenant member can invite for now); the lookup +
// accept endpoints are public because the invitee won't have a session
// yet.
func (h *Handlers) Register(m muxLike, requireAuth func(http.Handler) http.Handler) {
	m.Handle("POST /api/invites", requireAuth(http.HandlerFunc(h.Create)))
	m.Handle("GET /api/invites", requireAuth(http.HandlerFunc(h.ListPending)))
	m.Handle("POST /api/invites/{id}/resend", requireAuth(http.HandlerFunc(h.Resend)))
	m.Handle("DELETE /api/invites/{id}", requireAuth(http.HandlerFunc(h.Revoke)))
	m.HandleFunc("GET /api/invites/{token}", h.Lookup)
	m.HandleFunc("POST /api/invites/{token}/accept", h.Accept)
}

// ----- DTOs ----------------------------------------------------------------

type errorResp struct {
	Error string `json:"error"`
}

type createReq struct {
	// Plural "emails" supports comma-separated batch invites from the UI.
	// Single "email" is also accepted to keep the API ergonomic for one-offs.
	Emails       []string `json:"emails"`
	Email        string   `json:"email"`
	Role         string   `json:"role"`
	WorkspaceIDs []int64  `json:"workspace_ids,omitempty"`
	FolderIDs    []int64  `json:"folder_ids,omitempty"`
}

type createResp struct {
	Invites []invitePayload `json:"invites"`
}

type invitePayload struct {
	ID         int64     `json:"id"`
	Email      string    `json:"email"`
	Role       string    `json:"role"`
	Token      string    `json:"token"`
	Link       string    `json:"link"`
	Status     string    `json:"status"`
	ExpiresAt  time.Time `json:"expires_at"`
	WorkspaceIDs []int64 `json:"workspace_ids,omitempty"`
	FolderIDs    []int64 `json:"folder_ids,omitempty"`
}

type acceptReq struct {
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	Password  string `json:"password"`
}

// ----- handlers ------------------------------------------------------------

// Create handles POST /api/invites.
func (h *Handlers) Create(w http.ResponseWriter, r *http.Request) {
	identity, ok := auth.IdentityFromContext(r.Context())
	if !ok || identity.Tenant == nil {
		writeJSON(w, http.StatusUnauthorized, errorResp{Error: "unauthorized"})
		return
	}
	var req createReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResp{Error: "invalid JSON body"})
		return
	}

	// Normalize: split comma-separated single field too, then dedupe.
	all := append([]string{}, req.Emails...)
	if strings.TrimSpace(req.Email) != "" {
		all = append(all, req.Email)
	}
	emails := splitAndDedupe(all)
	if len(emails) == 0 {
		writeJSON(w, http.StatusBadRequest, errorResp{Error: "no email provided"})
		return
	}

	results := make([]invitePayload, 0, len(emails))
	for _, e := range emails {
		inv, err := h.svc.Create(r.Context(), CreateInput{
			TenantID:    identity.Tenant.ID,
			InviterID:   identity.User.ID,
			InviterRole: roleOf(identity),
			Email:        e,
			Role:         req.Role,
			WorkspaceIDs: req.WorkspaceIDs,
			FolderIDs:    req.FolderIDs,
		})
		if err != nil {
			writeJSON(w, statusFor(err), errorResp{Error: err.Error() + " (" + e + ")"})
			return
		}
		link := buildInviteLink(r, inv.Token)

		// Best-effort send. If SendGrid fails the invite row is still
		// valid — the Owner can copy the link from the modal as a
		// fallback. We log so deliverability issues are diagnosable.
		if h.mailer != nil {
			tenantName := ""
			if identity.Tenant != nil {
				tenantName = identity.Tenant.Name
			}
			inviterName := ""
			if identity.User.FullName != nil {
				inviterName = *identity.User.FullName
			}
			if mErr := h.mailer.SendInvite(r.Context(), mailer.InviteEmail{
				To:           inv.Email,
				TenantName:   tenantName,
				InviterName:  inviterName,
				InviterEmail: identity.User.Email,
				Role:         inv.Role,
				Link:         link,
				ExpiresAt:    inv.ExpiresAt,
			}); mErr != nil {
				logging.Error(packageName, "invite email send failed: %v", mErr)
			}
		}

		results = append(results, invitePayload{
			ID:        inv.ID,
			Email:     inv.Email,
			Role:      inv.Role,
			Token:     inv.Token,
			Link:      link,
			Status:    inv.Status,
			ExpiresAt: inv.ExpiresAt,
			WorkspaceIDs: inv.WorkspaceIDs,
			FolderIDs:    inv.FolderIDs,
		})
	}
	writeJSON(w, http.StatusCreated, createResp{Invites: results})
}

// Lookup handles GET /api/invites/{token}.
func (h *Handlers) Lookup(w http.ResponseWriter, r *http.Request) {
	tok := r.PathValue("token")
	out, err := h.svc.Lookup(r.Context(), tok)
	if err != nil {
		writeJSON(w, statusFor(err), errorResp{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// Accept handles POST /api/invites/{token}/accept. Sets a session cookie
// on success so the SPA can immediately use authenticated endpoints.
// acceptLimiter throttles invite-accept attempts per IP — the endpoint
// verifies passwords (argon2id) and is unauthenticated.
var acceptLimiter = ratelimit.PerMinute(5, 10)

func (h *Handlers) Accept(w http.ResponseWriter, r *http.Request) {
	// Throttled: verifies a password (argon2id) on existing accounts.
	if !acceptLimiter.Allow(ratelimit.ClientIP(r)) {
		ratelimit.TooMany(w)
		return
	}
	tok := r.PathValue("token")
	var req acceptReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResp{Error: "invalid JSON body"})
		return
	}

	res, err := h.svc.Accept(r.Context(), AcceptInput{
		Token:     tok,
		FirstName: req.FirstName,
		LastName:  req.LastName,
		Password:  req.Password,
		IP:        clientIP(r),
		UserAgent: r.UserAgent(),
	})
	if err != nil {
		writeJSON(w, statusFor(err), errorResp{Error: err.Error()})
		return
	}

	// 2FA-enrolled accounts get no session here — the membership exists, but
	// the user must sign in through /login where the second factor applies.
	if res.TwoFARequired {
		writeJSON(w, http.StatusOK, map[string]any{
			"tenant_id":      res.TenantID,
			"role":           res.Role,
			"is_new_user":    false,
			"twofa_required": true,
		})
		return
	}

	// Issue the session cookie using the same shape as auth.Login.
	session.SetCookie(w, r, res.Session.ID, res.Session.ExpiresAt)
	writeJSON(w, http.StatusOK, map[string]any{
		"tenant_id":   res.TenantID,
		"role":        res.Role,
		"is_new_user": res.IsNewUser,
	})
}

// ListPending handles GET /api/invites — returns every pending invite
// for the caller's tenant, with the link rebuilt for each so the UI can
// render copy buttons.
func (h *Handlers) ListPending(w http.ResponseWriter, r *http.Request) {
	identity, ok := auth.IdentityFromContext(r.Context())
	if !ok || identity.Tenant == nil {
		writeJSON(w, http.StatusUnauthorized, errorResp{Error: "unauthorized"})
		return
	}
	invites, err := h.svc.ListPending(r.Context(), identity.Tenant.ID)
	if err != nil {
		logging.Error(packageName, "ListPending failed: %v", err)
		writeJSON(w, http.StatusInternalServerError, errorResp{Error: "internal error"})
		return
	}
	out := make([]invitePayload, 0, len(invites))
	for _, inv := range invites {
		out = append(out, invitePayload{
			ID:        inv.ID,
			Email:     inv.Email,
			Role:      inv.Role,
			Token:     inv.Token,
			Link:      buildInviteLink(r, inv.Token),
			Status:    inv.Status,
			ExpiresAt: inv.ExpiresAt,
			WorkspaceIDs: inv.WorkspaceIDs,
			FolderIDs:    inv.FolderIDs,
		})
	}
	writeJSON(w, http.StatusOK, createResp{Invites: out})
}

// Resend handles POST /api/invites/{id}/resend. Extends the expires_at
// on the existing invite (same token, same link) and re-fires the
// SendGrid email. Best-effort on the email — if delivery fails the
// row's still valid and the modal still shows the copy link.
func (h *Handlers) Resend(w http.ResponseWriter, r *http.Request) {
	identity, ok := auth.IdentityFromContext(r.Context())
	if !ok || identity.Tenant == nil {
		writeJSON(w, http.StatusUnauthorized, errorResp{Error: "unauthorized"})
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorResp{Error: "invalid id"})
		return
	}
	inv, err := h.svc.Resend(r.Context(), identity.Tenant.ID, id, roleOf(identity))
	if err != nil {
		writeJSON(w, statusFor(err), errorResp{Error: err.Error()})
		return
	}
	link := buildInviteLink(r, inv.Token)

	if h.mailer != nil {
		tenantName := identity.Tenant.Name
		inviterName := ""
		if identity.User.FullName != nil {
			inviterName = *identity.User.FullName
		}
		if mErr := h.mailer.SendInvite(r.Context(), mailer.InviteEmail{
			To:           inv.Email,
			TenantName:   tenantName,
			InviterName:  inviterName,
			InviterEmail: identity.User.Email,
			Role:         inv.Role,
			Link:         link,
			ExpiresAt:    inv.ExpiresAt,
		}); mErr != nil {
			logging.Error(packageName, "resend email failed: %v", mErr)
		}
	}

	writeJSON(w, http.StatusOK, invitePayload{
		ID:        inv.ID,
		Email:     inv.Email,
		Role:      inv.Role,
		Token:     inv.Token,
		Link:      link,
		Status:    inv.Status,
		ExpiresAt: inv.ExpiresAt,
		WorkspaceIDs: inv.WorkspaceIDs,
			FolderIDs:    inv.FolderIDs,
	})
}

// Revoke handles DELETE /api/invites/{id}.
func (h *Handlers) Revoke(w http.ResponseWriter, r *http.Request) {
	identity, ok := auth.IdentityFromContext(r.Context())
	if !ok || identity.Tenant == nil {
		writeJSON(w, http.StatusUnauthorized, errorResp{Error: "unauthorized"})
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorResp{Error: "invalid id"})
		return
	}
	if err := h.svc.Revoke(r.Context(), identity.Tenant.ID, id); err != nil {
		writeJSON(w, statusFor(err), errorResp{Error: err.Error()})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ----- helpers -------------------------------------------------------------

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		logging.Error(packageName, "writeJSON encode failed: %v", err)
	}
}

func statusFor(err error) int {
	switch {
	case errors.Is(err, ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, ErrExpired), errors.Is(err, ErrAlreadyAccepted), errors.Is(err, ErrRevoked):
		return http.StatusGone
	case errors.Is(err, ErrAlreadyMember):
		return http.StatusConflict
	case errors.Is(err, ErrInvalidEmail), errors.Is(err, ErrInvalidRole), errors.Is(err, ErrPasswordRequired), errors.Is(err, ErrFolderRequired), errors.Is(err, ErrInvalidFolder), errors.Is(err, ErrWorkspaceRequired), errors.Is(err, ErrInvalidWorkspace):
		return http.StatusBadRequest
	case errors.Is(err, ErrWrongPassword):
		return http.StatusUnauthorized
	case errors.Is(err, ErrTokenForbidden), errors.Is(err, ErrAdminInvitesDisabled):
		return http.StatusForbidden
	case errors.Is(err, ErrDomainNotAllowed):
		return http.StatusBadRequest
	case errors.Is(err, ErrSeatLimit):
		return http.StatusConflict
	default:
		return http.StatusInternalServerError
	}
}

// roleOf returns the caller's tenant role (empty if no membership) — used to
// gate owner-only invite policy.
func roleOf(identity *auth.Identity) string {
	if identity == nil || identity.Membership == nil {
		return ""
	}
	return identity.Membership.Role
}

func splitAndDedupe(input []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(input))
	for _, raw := range input {
		for _, e := range strings.Split(raw, ",") {
			e = strings.ToLower(strings.TrimSpace(e))
			if e == "" {
				continue
			}
			if _, ok := seen[e]; ok {
				continue
			}
			seen[e] = struct{}{}
			out = append(out, e)
		}
	}
	return out
}

// clientIP extracts the bare host from r.RemoteAddr (which is host:port),
// preferring the leftmost X-Forwarded-For entry when present. The
// sessions.ip column is `inet` type and won't accept a host:port string.
func clientIP(r *http.Request) string {
	if xff := strings.TrimSpace(r.Header.Get("X-Forwarded-For")); xff != "" {
		if i := strings.Index(xff, ","); i > 0 {
			return strings.TrimSpace(xff[:i])
		}
		return xff
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// buildInviteLink derives a fully-qualified invite URL from the incoming
// request. Honors X-Forwarded-* headers from the Cloudflare proxy so the
// link points at the public hostname (app.trilli.com) rather than at
// the upstream 127.0.0.1:8081.
func buildInviteLink(r *http.Request, token string) string {
	host := r.Host
	if h := r.Header.Get("X-Forwarded-Host"); h != "" {
		host = h
	}
	scheme := "http"
	if p := r.Header.Get("X-Forwarded-Proto"); p != "" {
		scheme = p
	} else if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + host + "/invite/" + token
}
