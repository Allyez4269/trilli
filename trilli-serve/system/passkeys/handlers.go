package passkeys

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"trilli/system/auth"
	"trilli/system/logging"
	"trilli/system/session"
)

// stateHeader carries the sealed challenge between begin and finish so the
// request body stays a clean WebAuthn payload the library can parse directly.
const stateHeader = "X-Passkey-State"

// Handlers exposes the passkey HTTP surface. authSvc is used by the public
// login-completion endpoint to mint a session once an assertion verifies.
type Handlers struct {
	svc     *Service
	authSvc *auth.Service
}

// NewHandlers wraps the passkeys Service + the auth Service.
func NewHandlers(svc *Service, authSvc *auth.Service) *Handlers {
	return &Handlers{svc: svc, authSvc: authSvc}
}

type muxLike interface {
	Handle(pattern string, handler http.Handler)
}

// Register wires the routes. /api/me/passkeys* require a session; the
// /api/auth/passkey/* login routes are public (no session yet).
func (h *Handlers) Register(m muxLike, requireAuth func(http.Handler) http.Handler) {
	m.Handle("GET /api/me/passkeys", requireAuth(http.HandlerFunc(h.List)))
	m.Handle("POST /api/me/passkeys/register/begin", requireAuth(http.HandlerFunc(h.RegisterBegin)))
	m.Handle("POST /api/me/passkeys/register/finish", requireAuth(http.HandlerFunc(h.RegisterFinish)))
	m.Handle("PATCH /api/me/passkeys/{id}", requireAuth(http.HandlerFunc(h.Rename)))
	m.Handle("DELETE /api/me/passkeys/{id}", requireAuth(http.HandlerFunc(h.Delete)))
	m.Handle("POST /api/auth/passkey/begin", http.HandlerFunc(h.LoginBegin))
	m.Handle("POST /api/auth/passkey/finish", http.HandlerFunc(h.LoginFinish))
	// Passkey used as the second factor in a password-first login. Bound to the
	// user from the 2FA pending token (not discoverable).
	m.Handle("POST /api/auth/login/2fa/passkey/begin", http.HandlerFunc(h.ChallengeBegin))
	m.Handle("POST /api/auth/login/2fa/passkey/finish", http.HandlerFunc(h.ChallengeFinish))
}

// pending2FAHeader carries the 2FA pending token on the challenge-finish request
// (whose body is the raw assertion JSON).
const pending2FAHeader = "X-2FA-Pending"

// ChallengeBegin returns a passkey assertion challenge for the user identified
// by a 2FA pending token (password already verified, passkey is the 2nd factor).
// Body: {"pending_token": "..."}.
func (h *Handlers) ChallengeBegin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		PendingToken string `json:"pending_token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}
	userID, err := h.authSvc.ParseTwoFAPendingToken(req.PendingToken)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "Your sign-in expired — start again."})
		return
	}
	assertion, state, err := h.svc.BeginStepUp(r.Context(), userID)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"publicKey": assertion.Response, "state": state})
}

// ChallengeFinish verifies the passkey assertion for the pending user and issues
// the session. Body is the raw assertion JSON; the challenge state is in the
// X-Passkey-State header and the 2FA pending token in X-2FA-Pending.
func (h *Handlers) ChallengeFinish(w http.ResponseWriter, r *http.Request) {
	userID, err := h.authSvc.ParseTwoFAPendingToken(r.Header.Get(pending2FAHeader))
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "Your sign-in expired — start again."})
		return
	}
	if err := h.svc.VerifyStepUp(r.Context(), userID, r.Header.Get(stateHeader), r); err != nil {
		writeErr(w, err)
		return
	}
	identity, err := h.authSvc.CompleteSessionForUser(r.Context(), userID, auth.ClientIP(r), r.UserAgent())
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "Couldn't complete sign-in."})
		return
	}
	session.SetCookie(w, r, identity.Session.ID, identity.Session.ExpiresAt)
	tenants, _ := h.authSvc.ListTenantsForUser(r.Context(), userID)
	writeJSON(w, http.StatusOK, map[string]any{
		"user":                 identity.User,
		"tenant":               identity.Tenant,
		"membership":           identity.Membership,
		"idle_timeout_seconds": int(session.DefaultIdleTimeout.Seconds()),
		"tenants":              tenants,
	})
}

// ----- registration (logged-in user) ---------------------------------------

// RegisterBegin returns creation options + the sealed challenge state.
func (h *Handlers) RegisterBegin(w http.ResponseWriter, r *http.Request) {
	identity, ok := auth.IdentityFromContext(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	creation, state, err := h.svc.BeginRegistration(r.Context(), identity.User.ID)
	if err != nil {
		logging.Error(packageName, "RegisterBegin failed: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"publicKey": creation.Response, "state": state})
}

// RegisterFinish verifies the attestation and stores the passkey. Body is the
// raw credential JSON; state comes via the header; an optional ?label= names it.
func (h *Handlers) RegisterFinish(w http.ResponseWriter, r *http.Request) {
	identity, ok := auth.IdentityFromContext(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	state := r.Header.Get(stateHeader)
	label := r.URL.Query().Get("label")
	if err := h.svc.FinishRegistration(r.Context(), identity.User.ID, state, label, r); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ----- management -----------------------------------------------------------

// List returns the caller's registered passkeys.
func (h *Handlers) List(w http.ResponseWriter, r *http.Request) {
	identity, ok := auth.IdentityFromContext(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	list, err := h.svc.List(r.Context(), identity.User.ID)
	if err != nil {
		logging.Error(packageName, "List failed: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"passkeys": list})
}

// Rename sets a passkey's label. Body: {"label": "MacBook"}.
func (h *Handlers) Rename(w http.ResponseWriter, r *http.Request) {
	identity, ok := auth.IdentityFromContext(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid id"})
		return
	}
	var req struct {
		Label string `json:"label"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}
	if err := h.svc.Rename(r.Context(), identity.User.ID, id, req.Label); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Delete removes a passkey.
func (h *Handlers) Delete(w http.ResponseWriter, r *http.Request) {
	identity, ok := auth.IdentityFromContext(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid id"})
		return
	}
	if err := h.svc.Delete(r.Context(), identity.User.ID, id); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ----- login (public, no session yet) ---------------------------------------

// LoginBegin returns request options + the sealed challenge state for a
// passwordless passkey sign-in.
func (h *Handlers) LoginBegin(w http.ResponseWriter, r *http.Request) {
	assertion, state, err := h.svc.BeginLogin()
	if err != nil {
		logging.Error(packageName, "LoginBegin failed: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"publicKey": assertion.Response, "state": state})
}

// LoginFinish verifies the assertion and issues a session. Body is the raw
// assertion JSON; state comes via the header.
func (h *Handlers) LoginFinish(w http.ResponseWriter, r *http.Request) {
	state := r.Header.Get(stateHeader)
	userID, err := h.svc.FinishLogin(r.Context(), state, r)
	if err != nil {
		writeErr(w, err)
		return
	}
	identity, err := h.authSvc.CompleteSessionForUser(r.Context(), userID, auth.ClientIP(r), r.UserAgent())
	if err != nil {
		logging.Error(packageName, "LoginFinish: session failed: %v", err)
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "Couldn't complete sign-in."})
		return
	}
	session.SetCookie(w, r, identity.Session.ID, identity.Session.ExpiresAt)
	tenants, _ := h.authSvc.ListTenantsForUser(r.Context(), userID)
	writeJSON(w, http.StatusOK, map[string]any{
		"user":                 identity.User,
		"tenant":               identity.Tenant,
		"membership":           identity.Membership,
		"idle_timeout_seconds": int(session.DefaultIdleTimeout.Seconds()),
		"tenants":              tenants,
	})
}

// ----- helpers --------------------------------------------------------------

func writeErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrInvalidState):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
	case errors.Is(err, ErrCredentialUsed):
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
	case errors.Is(err, ErrInvalidAssertion):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
	case errors.Is(err, ErrNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
	default:
		logging.Error(packageName, "handler error: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
	}
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		logging.Error(packageName, "writeJSON encode failed: %v", err)
	}
}
