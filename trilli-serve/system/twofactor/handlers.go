package twofactor

import (
	"encoding/json"
	"errors"
	"net/http"

	"trilli/system/auth"
	"trilli/system/logging"
	"trilli/system/ratelimit"
	"trilli/system/session"
)

// twofaLimiter throttles code attempts per client IP. A 6-digit TOTP has a
// 1e6 space and the pending token lives 5 minutes — without a limiter the
// second factor is brute-forceable. 6/min (burst 10) keeps honest retries
// painless and guessing hopeless.
var twofaLimiter = ratelimit.PerMinute(6, 10)

// Handlers exposes the per-user 2FA HTTP surface. authSvc is used by the
// public login-completion endpoint to issue a session once a code verifies.
type Handlers struct {
	svc     *Service
	authSvc *auth.Service
}

// NewHandlers wraps the 2FA Service + the auth Service.
func NewHandlers(svc *Service, authSvc *auth.Service) *Handlers {
	return &Handlers{svc: svc, authSvc: authSvc}
}

type muxLike interface {
	Handle(pattern string, handler http.Handler)
}

// Register wires the routes. The /api/me/2fa* management routes require a
// session; the login-completion route is public (the user has no session yet).
func (h *Handlers) Register(m muxLike, requireAuth func(http.Handler) http.Handler) {
	m.Handle("GET /api/me/2fa", requireAuth(http.HandlerFunc(h.Status)))
	m.Handle("POST /api/me/2fa/totp/setup", requireAuth(http.HandlerFunc(h.Setup)))
	m.Handle("POST /api/me/2fa/totp/verify", requireAuth(http.HandlerFunc(h.Verify)))
	m.Handle("DELETE /api/me/2fa/totp", requireAuth(http.HandlerFunc(h.Disable)))
	m.Handle("POST /api/auth/login/2fa", http.HandlerFunc(h.CompleteLogin))
}

// CompleteLogin finishes a 2FA login: validate the pending token, verify the
// code (TOTP or recovery), then issue the real session. Public — no session yet.
// Body: {"pending_token": "...", "code": "123456"}.
func (h *Handlers) CompleteLogin(w http.ResponseWriter, r *http.Request) {
	if !twofaLimiter.Allow(ratelimit.ClientIP(r)) {
		ratelimit.TooMany(w)
		return
	}
	var req struct {
		PendingToken string `json:"pending_token"`
		Code         string `json:"code"`
		Remember     bool   `json:"remember"`
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
	if err := h.svc.VerifyCode(r.Context(), userID, req.Code); err != nil {
		writeErr(w, err)
		return
	}
	identity, err := h.authSvc.CompleteSessionForUser(r.Context(), userID, auth.ClientIP(r), r.UserAgent())
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "Couldn't complete sign-in."})
		return
	}
	session.SetCookie(w, r, identity.Session.ID, identity.Session.ExpiresAt)
	// "Remember this device" — mint a 30-day trusted-device cookie so this
	// device skips the challenge until it expires (or 2FA is disabled).
	if req.Remember {
		if tok, exp, terr := h.authSvc.NewTrustedDeviceToken(r.Context(), userID); terr == nil {
			auth.SetTrustedDeviceCookie(w, r, tok, exp)
		} else {
			logging.Error(packageName, "CompleteLogin: trusted device token failed: %v", terr)
		}
	}
	tenants, _ := h.authSvc.ListTenantsForUser(r.Context(), userID)
	writeJSON(w, http.StatusOK, map[string]any{
		"user":                 identity.User,
		"tenant":               identity.Tenant,
		"membership":           identity.Membership,
		"idle_timeout_seconds": int(session.DefaultIdleTimeout.Seconds()),
		"tenants":              tenants,
	})
}

// Status handles GET /api/me/2fa.
func (h *Handlers) Status(w http.ResponseWriter, r *http.Request) {
	identity, ok := auth.IdentityFromContext(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	st, err := h.svc.GetStatus(r.Context(), identity.User.ID)
	if err != nil {
		logging.Error(packageName, "Status failed: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	writeJSON(w, http.StatusOK, st)
}

// Setup handles POST /api/me/2fa/totp/setup — generate a pending secret + QR.
func (h *Handlers) Setup(w http.ResponseWriter, r *http.Request) {
	identity, ok := auth.IdentityFromContext(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	setup, err := h.svc.BeginSetup(r.Context(), identity.User.ID, identity.User.Email)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, setup)
}

// Verify handles POST /api/me/2fa/totp/verify. Body: {"code": "123456"}.
// On success returns the one-time recovery codes (shown once).
func (h *Handlers) Verify(w http.ResponseWriter, r *http.Request) {
	identity, ok := auth.IdentityFromContext(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	var req struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}
	codes, err := h.svc.Confirm(r.Context(), identity.User.ID, req.Code)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"recovery_codes": codes})
}

// Disable handles DELETE /api/me/2fa/totp. Hardened: the caller must prove
// they still control the second factor by supplying a current authenticator
// code (or a recovery code) in the body — so a hijacked but unlocked session
// can't silently turn 2FA off. Body: {"code": "123456"}.
func (h *Handlers) Disable(w http.ResponseWriter, r *http.Request) {
	identity, ok := auth.IdentityFromContext(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	var req struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}
	if err := h.svc.VerifyCode(r.Context(), identity.User.ID, req.Code); err != nil {
		writeErr(w, err)
		return
	}
	if err := h.svc.Disable(r.Context(), identity.User.ID); err != nil {
		writeErr(w, err)
		return
	}
	// Drop this device's trust cookie — with 2FA off it's meaningless, and
	// other devices' trust tokens are voided by the enrollment binding.
	auth.ClearTrustedDeviceCookie(w, r)
	w.WriteHeader(http.StatusNoContent)
}

func writeErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrAlreadyEnabled):
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
	case errors.Is(err, ErrInvalidCode):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "That code is incorrect or expired. Try the latest one from your app."})
	case errors.Is(err, ErrNoPending):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Start setup again — there's no pending code to confirm."})
	case errors.Is(err, ErrNotEnabled):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
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
