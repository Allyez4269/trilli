// Package passwordreset wires the verified "reset your password by email" flow
// for a logged-in user. It orchestrates a step-up check (prefer a 2FA code,
// fall back to a passkey, else nothing) before issuing a single-use reset token
// and emailing the set-password link. The token-authenticated set-password
// endpoints are public (the email recipient may not have a session).
//
// It depends on auth (token storage + password apply), twofactor + passkeys
// (step-up verification), and the mailer — none of which import it, so there's
// no cycle.
package passwordreset

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"trilli/system/auth"
	"trilli/system/logging"
	"trilli/system/mailer"
	"trilli/system/passkeys"
	"trilli/system/ratelimit"
	"trilli/system/twofactor"
)

const packageName = "passwordreset"

// passkeyStateHeader mirrors the header the passkeys package reads its sealed
// challenge from.
const passkeyStateHeader = "X-Passkey-State"

// Handlers wires the dependencies for the reset flow.
type Handlers struct {
	authSvc  *auth.Service
	twofa    *twofactor.Service
	passkeys *passkeys.Service
	mailer   *mailer.Mailer
}

// NewHandlers constructs the reset handlers.
func NewHandlers(a *auth.Service, t *twofactor.Service, p *passkeys.Service, m *mailer.Mailer) *Handlers {
	return &Handlers{authSvc: a, twofa: t, passkeys: p, mailer: m}
}

type muxLike interface {
	Handle(pattern string, handler http.Handler)
}

// Register wires the routes. The /api/me/* routes require a session; the
// token-authenticated set-password routes are public.
func (h *Handlers) Register(m muxLike, requireAuth func(http.Handler) http.Handler) {
	m.Handle("POST /api/me/password-reset/request", requireAuth(http.HandlerFunc(h.Request)))
	m.Handle("POST /api/me/password-reset/passkey/begin", requireAuth(http.HandlerFunc(h.PasskeyBegin)))
	m.Handle("GET /api/password-reset/{token}", http.HandlerFunc(h.Lookup))
	m.Handle("POST /api/password-reset/{token}", http.HandlerFunc(h.Apply))
}

// PasskeyBegin returns a passkey assertion challenge scoped to the caller, used
// when the step-up method is a passkey.
func (h *Handlers) PasskeyBegin(w http.ResponseWriter, r *http.Request) {
	identity, ok := auth.IdentityFromContext(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	assertion, state, err := h.passkeys.BeginStepUp(r.Context(), identity.User.ID)
	if err != nil {
		logging.Error(packageName, "PasskeyBegin failed: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"publicKey": assertion.Response, "state": state})
}

// Request runs the step-up (2FA code, else passkey, else none), then issues a
// reset token and emails the set-password link.
//
// For the 2FA path the body is {"code": "..."}; for the passkey path the body
// is the raw assertion JSON with the challenge in the X-Passkey-State header.
var stepUpLimiter = ratelimit.PerMinute(6, 10)

func (h *Handlers) Request(w http.ResponseWriter, r *http.Request) {
	// Throttled: the step-up verifies a 6-digit TOTP — unlimited attempts
	// would let a hijacked session brute-force its way to a reset email.
	if !stepUpLimiter.Allow(ratelimit.ClientIP(r)) {
		ratelimit.TooMany(w)
		return
	}
	identity, ok := auth.IdentityFromContext(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	userID := identity.User.ID

	// Decide the required step-up server-side — never trust the client about
	// which factor (or none) is needed. 2FA takes precedence over a passkey.
	totpOn, err := h.authSvc.IsTOTPEnabled(r.Context(), userID)
	if err != nil {
		logging.Error(packageName, "Request: totp check failed: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}

	switch {
	case totpOn:
		var req struct {
			Code string `json:"code"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
			return
		}
		if err := h.twofa.VerifyCode(r.Context(), userID, req.Code); err != nil {
			writeStepUpErr(w, err)
			return
		}
	default:
		pkCount, err := h.passkeys.Count(r.Context(), userID)
		if err != nil {
			logging.Error(packageName, "Request: passkey count failed: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
			return
		}
		if pkCount > 0 {
			state := r.Header.Get(passkeyStateHeader)
			if err := h.passkeys.VerifyStepUp(r.Context(), userID, state, r); err != nil {
				writeStepUpErr(w, err)
				return
			}
		}
		// else: no second factor configured — allow it through (the email link
		// is the gate).
	}

	reset, err := h.authSvc.CreatePasswordReset(r.Context(), userID)
	if err != nil {
		logging.Error(packageName, "Request: create reset failed: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}

	if h.mailer != nil {
		if err := h.mailer.SendPasswordReset(r.Context(), mailer.PasswordResetEmail{
			To:        reset.Email,
			Name:      firstName(identity.User.FirstName),
			Link:      buildResetLink(r, reset.Token),
			ExpiresAt: reset.ExpiresAt,
		}); err != nil {
			logging.Error(packageName, "Request: send mail failed: %v", err)
			writeJSON(w, http.StatusBadGateway, map[string]string{
				"error": "We couldn't send the reset email. Please try again in a moment.",
			})
			return
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "email": reset.Email})
}

// Lookup validates a reset token so the set-password page can confirm the link.
func (h *Handlers) Lookup(w http.ResponseWriter, r *http.Request) {
	reset, err := h.authSvc.LookupPasswordReset(r.Context(), r.PathValue("token"))
	if err != nil {
		writeResetErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"email": reset.Email})
}

// Apply sets the new password for a valid token. Body: {"password": "..."}.
func (h *Handlers) Apply(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}
	if err := h.authSvc.ResetPassword(r.Context(), r.PathValue("token"), req.Password); err != nil {
		writeResetErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// ----- helpers --------------------------------------------------------------

func writeStepUpErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, twofactor.ErrInvalidCode):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "That code is incorrect or expired. Try the latest one from your app."})
	case errors.Is(err, passkeys.ErrInvalidAssertion), errors.Is(err, passkeys.ErrInvalidState):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "We couldn't verify your passkey. Try again."})
	case errors.Is(err, twofactor.ErrNotEnabled), errors.Is(err, passkeys.ErrNotFound):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Verification is no longer available — refresh and try again."})
	default:
		logging.Error(packageName, "step-up error: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
	}
}

func writeResetErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, auth.ErrPasswordResetExpired):
		writeJSON(w, http.StatusGone, map[string]string{"error": err.Error()})
	case errors.Is(err, auth.ErrPasswordTooShort):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Use at least 8 characters."})
	case errors.Is(err, auth.ErrPasswordResetToken):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
	default:
		logging.Error(packageName, "reset error: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
	}
}

// buildResetLink derives the absolute /reset-password/<token> URL from the
// incoming request, honoring the Cloudflare proxy's X-Forwarded-* headers so it
// points at the public hostname rather than the upstream 127.0.0.1.
func buildResetLink(r *http.Request, token string) string {
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
	return scheme + "://" + host + "/reset-password/" + token
}

func firstName(s *string) string {
	if s == nil {
		return ""
	}
	return strings.TrimSpace(*s)
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		logging.Error(packageName, "writeJSON encode failed: %v", err)
	}
}
