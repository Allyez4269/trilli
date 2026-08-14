package operators

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"trilli-cmx/system/logging"
	"trilli-cmx/system/ratelimit"
)

// CookieName is the operator session cookie. Distinct from the app's
// `trilli_session` so the two services never confuse each other's cookies.
const CookieName = "cmx_session"

type ctxKey int

const (
	ctxOperator ctxKey = iota
	ctxSession
)

// Handler exposes the operator auth API and middleware.
type Handler struct {
	svc         *Service
	loginLimit  *ratelimit.Limiter // per-IP brute-force guard on password attempts
	twofaLimit  *ratelimit.Limiter // per-IP guard on code attempts
}

// NewHandler builds the operator HTTP handler.
func NewHandler(svc *Service) *Handler {
	return &Handler{
		svc:        svc,
		loginLimit: ratelimit.PerMinute(5, 10),
		twofaLimit: ratelimit.PerMinute(10, 15),
	}
}

// Register wires the operator routes onto a mux under /api/cmx.
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/cmx/login", h.handleLogin)
	mux.HandleFunc("POST /api/cmx/login/2fa/setup", h.handleEnrollSetup)
	mux.HandleFunc("POST /api/cmx/login/2fa/enroll", h.handleEnrollConfirm)
	mux.HandleFunc("POST /api/cmx/login/2fa", h.handleTwoFA)
	mux.HandleFunc("POST /api/cmx/logout", h.RequireAuth(h.handleLogout))
	mux.HandleFunc("GET /api/cmx/me", h.RequireAuth(h.handleMe))
	mux.HandleFunc("POST /api/cmx/step-up", h.RequireAuth(h.handleStepUp))
}

// loginContext extracts IP/UA and resolves geo for the request.
func (h *Handler) loginContext(r *http.Request) LoginContext {
	ip := ratelimit.ClientIP(r)
	return LoginContext{
		IP:        ip,
		Geo:       h.svc.Locate(ip),
		UserAgent: r.UserAgent(),
	}
}

func (h *Handler) handleLogin(w http.ResponseWriter, r *http.Request) {
	ip := ratelimit.ClientIP(r)
	if !h.loginLimit.Allow(ip) {
		ratelimit.TooMany(w)
		return
	}
	var req struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if !decode(w, r, &req) {
		return
	}
	res, err := h.svc.BeginLogin(r.Context(), req.Email, req.Password, h.loginContext(r))
	if err != nil {
		writeAuthError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (h *Handler) handleEnrollSetup(w http.ResponseWriter, r *http.Request) {
	if !h.twofaLimit.Allow(ratelimit.ClientIP(r)) {
		ratelimit.TooMany(w)
		return
	}
	var req struct {
		ChallengeID string `json:"challenge_id"`
	}
	if !decode(w, r, &req) {
		return
	}
	setup, err := h.svc.ChallengeSetup(r.Context(), req.ChallengeID)
	if err != nil {
		writeAuthError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, setup)
}

func (h *Handler) handleEnrollConfirm(w http.ResponseWriter, r *http.Request) {
	if !h.twofaLimit.Allow(ratelimit.ClientIP(r)) {
		ratelimit.TooMany(w)
		return
	}
	var req struct {
		ChallengeID string `json:"challenge_id"`
		Code        string `json:"code"`
	}
	if !decode(w, r, &req) {
		return
	}
	sess, codes, err := h.svc.CompleteEnroll(r.Context(), req.ChallengeID, req.Code)
	if err != nil {
		writeAuthError(w, err)
		return
	}
	setCookie(w, sess)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "recovery_codes": codes})
}

func (h *Handler) handleTwoFA(w http.ResponseWriter, r *http.Request) {
	if !h.twofaLimit.Allow(ratelimit.ClientIP(r)) {
		ratelimit.TooMany(w)
		return
	}
	var req struct {
		ChallengeID string `json:"challenge_id"`
		Code        string `json:"code"`
	}
	if !decode(w, r, &req) {
		return
	}
	sess, err := h.svc.CompleteLogin(r.Context(), req.ChallengeID, req.Code)
	if err != nil {
		writeAuthError(w, err)
		return
	}
	setCookie(w, sess)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (h *Handler) handleLogout(w http.ResponseWriter, r *http.Request) {
	if sess := SessionFrom(r.Context()); sess != nil {
		_ = h.svc.RevokeSession(r.Context(), sess.ID)
	}
	clearCookie(w)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (h *Handler) handleMe(w http.ResponseWriter, r *http.Request) {
	op := OperatorFrom(r.Context())
	sess := SessionFrom(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{
		"operator":      op,
		"step_up_fresh": sess != nil && sess.StepUpFresh(time.Now()),
	})
}

func (h *Handler) handleStepUp(w http.ResponseWriter, r *http.Request) {
	if !h.twofaLimit.Allow(ratelimit.ClientIP(r)) {
		ratelimit.TooMany(w)
		return
	}
	op := OperatorFrom(r.Context())
	sess := SessionFrom(r.Context())
	var req struct {
		Code string `json:"code"`
	}
	if !decode(w, r, &req) {
		return
	}
	if err := h.svc.StepUp(r.Context(), sess.ID, op.ID, req.Code); err != nil {
		writeAuthError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ---- Middleware -----------------------------------------------------------

// RequireAuth loads + validates the operator session, enforces the idle/hard
// timeout, touches last_seen_at, and injects the operator + session into the
// request context. Rejects with 401 otherwise.
func (h *Handler) RequireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(CookieName)
		if err != nil || c.Value == "" {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "not authenticated"})
			return
		}
		sess, err := h.svc.GetSession(r.Context(), c.Value)
		if err != nil || !sess.Live(time.Now()) {
			if err == nil {
				_ = h.svc.RevokeSession(r.Context(), sess.ID) // proactively kill dead sessions
			}
			clearCookie(w)
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "session expired"})
			return
		}
		op, err := h.svc.GetByID(r.Context(), sess.OperatorID)
		if err != nil || op.Status != StatusActive {
			clearCookie(w)
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "operator unavailable"})
			return
		}
		_ = h.svc.touchSession(r.Context(), sess.ID)
		ctx := context.WithValue(r.Context(), ctxOperator, op)
		ctx = context.WithValue(ctx, ctxSession, sess)
		next(w, r.WithContext(ctx))
	}
}

// RequireGlobal is RequireAuth plus a Global-admin gate (SPEC §3).
func (h *Handler) RequireGlobal(next http.HandlerFunc) http.HandlerFunc {
	return h.RequireAuth(func(w http.ResponseWriter, r *http.Request) {
		if op := OperatorFrom(r.Context()); op == nil || !op.IsGlobal() {
			writeJSON(w, http.StatusForbidden, map[string]any{"error": "global admin required"})
			return
		}
		next(w, r)
	})
}

// RequireStepUp wraps a handler so it only runs with a fresh step-up (SPEC
// §6.9). Use for consequential/destructive actions.
func (h *Handler) RequireStepUp(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sess := SessionFrom(r.Context())
		if sess == nil || !sess.StepUpFresh(time.Now()) {
			writeJSON(w, http.StatusForbidden, map[string]any{"error": "step_up_required"})
			return
		}
		next(w, r)
	}
}

// OperatorFrom returns the authenticated operator from context, or nil.
func OperatorFrom(ctx context.Context) *Operator {
	op, _ := ctx.Value(ctxOperator).(*Operator)
	return op
}

// SessionFrom returns the active session from context, or nil.
func SessionFrom(ctx context.Context) *Session {
	s, _ := ctx.Value(ctxSession).(*Session)
	return s
}

// ---- HTTP helpers ---------------------------------------------------------

func setCookie(w http.ResponseWriter, sess *Session) {
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    sess.ID,
		Path:     "/",
		Expires:  sess.ExpiresAt,
		MaxAge:   int(time.Until(sess.ExpiresAt).Seconds()),
		HttpOnly: true,
		Secure:   true, // TLS terminates upstream; localhost is treated as secure
		SameSite: http.SameSiteLaxMode,
	})
}

func clearCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(v); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid request body"})
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeAuthError maps service sentinels to HTTP statuses without leaking which
// part failed for credential errors.
func writeAuthError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrInvalidCredentials):
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "Invalid email or password."})
	case errors.Is(err, ErrAccountLocked):
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "Account locked. Contact a Global admin.", "code": "locked"})
	case errors.Is(err, ErrAccountSuspended):
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "Account suspended.", "code": "suspended"})
	case errors.Is(err, ErrGeoBlocked):
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "Login not permitted from your location.", "code": "geo_blocked"})
	case errors.Is(err, ErrChallengeNotFound):
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "Login session expired. Please sign in again.", "code": "challenge_expired"})
	case errors.Is(err, ErrInvalidCode):
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "That code is incorrect or expired.", "code": "bad_code"})
	case errors.Is(err, ErrNotEnrolled):
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "Two-factor is not set up.", "code": "not_enrolled"})
	default:
		logging.Error(packageName, "auth handler error: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "Something went wrong. Please try again."})
	}
}

// normalizeEmail is a tiny helper used by the bootstrap CLI.
func normalizeEmail(s string) string { return strings.TrimSpace(strings.ToLower(s)) }
