package auth

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"trilli/system/logging"
	"trilli/system/mailer"
	"trilli/system/ratelimit"
	"trilli/system/session"
	"trilli/system/storage"
)

// CookieName is the session cookie used for the tenant port. Kept as a
// re-export of session.CookieName so existing call sites stay unchanged.
// The admin port (Phase 0.4) will use a distinct cookie name so a tenant
// cookie can never be replayed as a super-admin cookie and vice versa.
const CookieName = session.CookieName

// TrustedDeviceCookieName holds the encrypted "remember this device" token that
// lets a returning device skip the 2FA challenge for 30 days.
const TrustedDeviceCookieName = "trilli_td"

// AvatarMaxBytes caps an uploaded avatar. The client crops + downscales to a
// 512px PNG before sending, so anything much larger is junk we refuse early.
const AvatarMaxBytes = 4 << 20 // 4 MiB

// Handlers exposes the HTTP-facing auth surface.
type Handlers struct {
	svc    *Service
	store  storage.Store  // tenant-scoped blob store for avatar images
	mailer *mailer.Mailer // transactional email (email-change confirmations)
	// avatarTrust, when set, grants avatar visibility across the tenant gate
	// for two users who share a trust context (e.g. a live co-editing session).
	avatarTrust func(ctx context.Context, callerUserID, targetUserID int64) bool
}

// SetAvatarTrust wires an extra authorization path for GetUserAvatar: when the
// caller isn't a co-member of the target in the active tenant, this decides
// whether a shared trust context (live collaboration) grants visibility anyway.
func (h *Handlers) SetAvatarTrust(fn func(ctx context.Context, callerUserID, targetUserID int64) bool) {
	h.avatarTrust = fn
}

// NewHandlers constructs the HTTP handlers wrapping a Service. The blob store
// backs avatar upload/serve; the mailer sends email-change confirmation links.
func NewHandlers(svc *Service, store storage.Store, m *mailer.Mailer) *Handlers {
	return &Handlers{svc: svc, store: store, mailer: m}
}

// Register wires the auth routes onto a mux-like target.
type muxLike interface {
	HandleFunc(pattern string, handler func(http.ResponseWriter, *http.Request))
	Handle(pattern string, handler http.Handler)
}

func (h *Handlers) Register(m muxLike) {
	m.HandleFunc("/api/auth/signup", h.Signup)
	m.HandleFunc("/api/auth/login", h.Login)
	m.HandleFunc("/api/auth/logout", h.Logout)
	m.HandleFunc("/api/auth/forgot-password", h.ForgotPassword)
	m.Handle("/api/me", h.RequireAuth(http.HandlerFunc(h.Me)))
	m.Handle("/api/auth/switch-tenant", h.RequireAuth(http.HandlerFunc(h.SwitchTenant)))
	m.Handle("PATCH /api/me", h.RequireAuth(http.HandlerFunc(h.UpdateMe)))
	m.Handle("POST /api/me/email-change", h.RequireAuth(http.HandlerFunc(h.RequestEmailChange)))
	// Public, token-authenticated email-change confirm (clicked from the email).
	m.HandleFunc("GET /api/email-change/{token}", h.LookupEmailChange)
	m.HandleFunc("POST /api/email-change/{token}/confirm", h.ConfirmEmailChange)
	m.Handle("POST /api/me/avatar", h.RequireAuth(http.HandlerFunc(h.UploadAvatar)))
	m.Handle("DELETE /api/me/avatar", h.RequireAuth(http.HandlerFunc(h.RemoveAvatar)))
	m.Handle("GET /api/users/{id}/avatar", h.RequireAuth(http.HandlerFunc(h.GetUserAvatar)))
	m.Handle("GET /api/me/folders", h.RequireAuth(http.HandlerFunc(h.MyFolders)))
	m.Handle("POST /api/me/session/ips", h.RequireAuth(http.HandlerFunc(h.UpdateSessionIPs)))
	m.Handle("GET /api/sessions", h.RequireAuth(http.HandlerFunc(h.ListSessions)))
	m.Handle("POST /api/sessions/revoke", h.RequireAuth(http.HandlerFunc(h.RevokeSessions)))
	m.Handle("POST /api/account/plan", h.RequireAdminOrOwner(http.HandlerFunc(h.ChangePlan)))
	m.Handle("POST /api/account/billing-address", h.RequireAdminOrOwner(http.HandlerFunc(h.SaveBillingAddress)))
	m.Handle("PATCH /api/workspace", h.RequireAdminOrOwner(http.HandlerFunc(h.UpdateWorkspace)))
	m.Handle("GET /api/workspace/slug-available", h.RequireAdminOrOwner(http.HandlerFunc(h.CheckWorkspaceSlug)))
	m.Handle("GET /api/members", h.RequireAdminOrOwner(http.HandlerFunc(h.Members)))
	m.Handle("DELETE /api/members/{id}", h.RequireAdminOrOwner(http.HandlerFunc(h.RemoveMember)))
	m.Handle("PATCH /api/members/{id}/status", h.RequireAdminOrOwner(http.HandlerFunc(h.SetMemberStatus)))
	m.Handle("GET /api/members/{id}/folders", h.RequireAdminOrOwner(http.HandlerFunc(h.GetMemberFolders)))
	m.Handle("PATCH /api/members/{id}/folders", h.RequireAdminOrOwner(http.HandlerFunc(h.SetMemberFolders)))
}

// ChangePlan handles POST /api/account/plan — assign the caller's account to a
// plan and snapshot-lock the price for the chosen billing period. Owner/admin
// only (gated at registration). Body: {"code": "...", "billing_period": "..."}.
func (h *Handlers) ChangePlan(w http.ResponseWriter, r *http.Request) {
	identity, ok := IdentityFromContext(r.Context())
	if !ok || identity.Tenant == nil {
		writeJSON(w, http.StatusUnauthorized, errorResp{Error: "unauthorized"})
		return
	}
	var req struct {
		Code          string `json:"code"`
		BillingPeriod string `json:"billing_period"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResp{Error: "invalid JSON body"})
		return
	}
	if strings.TrimSpace(req.Code) == "" {
		writeJSON(w, http.StatusBadRequest, errorResp{Error: "plan code required"})
		return
	}
	err := h.svc.ChangePlan(r.Context(), identity.Tenant.ID, strings.TrimSpace(req.Code), req.BillingPeriod)
	switch {
	case errors.Is(err, ErrPlanNotFound):
		writeJSON(w, http.StatusNotFound, errorResp{Error: "plan not found"})
	case errors.Is(err, ErrPlanUnavailable):
		writeJSON(w, http.StatusConflict, errorResp{Error: "that plan isn't available"})
	case err != nil:
		logging.Error(packageName, "ChangePlan failed: %v", err)
		writeJSON(w, http.StatusInternalServerError, errorResp{Error: "internal error"})
	default:
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	}
}

// SaveBillingAddress handles POST /api/account/billing-address — stores the
// account's billing address (captured at checkout). Owner/admin only.
func (h *Handlers) SaveBillingAddress(w http.ResponseWriter, r *http.Request) {
	identity, ok := IdentityFromContext(r.Context())
	if !ok || identity.Tenant == nil {
		writeJSON(w, http.StatusUnauthorized, errorResp{Error: "unauthorized"})
		return
	}
	var a BillingAddress
	if err := json.NewDecoder(r.Body).Decode(&a); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResp{Error: "invalid JSON body"})
		return
	}
	if err := h.svc.SaveBillingAddress(r.Context(), identity.Tenant.ID, a); err != nil {
		logging.Error(packageName, "SaveBillingAddress failed: %v", err)
		writeJSON(w, http.StatusInternalServerError, errorResp{Error: "internal error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// ----- DTOs ----------------------------------------------------------------

type signupReq struct {
	Email      string `json:"email"`
	Password   string `json:"password"`
	FirstName  string `json:"first_name"`
	LastName   string `json:"last_name"`
	TenantName string `json:"tenant_name"`
}

type loginReq struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type identityResp struct {
	User       *User       `json:"user"`
	Tenant     *Tenant     `json:"tenant,omitempty"`
	Membership *Membership `json:"membership,omitempty"`
	// IdleTimeoutSeconds is the inactivity window after which the session is
	// logged out. The client reads this to auto-log-out on the same window
	// (single source of truth = session.DefaultIdleTimeout).
	IdleTimeoutSeconds int `json:"idle_timeout_seconds"`
	// Tenants is every account this user can access (owned + member-of), for the
	// account selector + in-app switcher. Set only on the endpoints that need it
	// (/api/me, login, switch-tenant); omitted elsewhere.
	Tenants []TenantSummary `json:"tenants,omitempty"`
}

// newIdentityResp builds the identity payload, always stamping the current
// idle-timeout so the field stays consistent across every endpoint that
// returns it (login, signup-complete, /api/me, profile/avatar updates, …).
func newIdentityResp(user *User, tenant *Tenant, membership *Membership) identityResp {
	return identityResp{
		User:               user,
		Tenant:             tenant,
		Membership:         membership,
		IdleTimeoutSeconds: int(session.DefaultIdleTimeout.Seconds()),
	}
}

// identityWithTenants is newIdentityResp plus the user's accessible-tenants
// list — used where the client needs to know about other accounts (boot,
// login, switch). Best-effort: a tenant-list error still returns core identity.
func (h *Handlers) identityWithTenants(ctx context.Context, user *User, tenant *Tenant, membership *Membership) identityResp {
	resp := newIdentityResp(user, tenant, membership)
	if user != nil {
		if ts, err := h.svc.ListTenantsForUser(ctx, user.ID); err == nil {
			resp.Tenants = ts
		}
	}
	return resp
}

type errorResp struct {
	Error string `json:"error"`
}

// ----- Handlers ------------------------------------------------------------

// Brute-force / abuse limiters. Argon2id verification costs ~64 MiB of RAM
// per attempt, so unauthenticated credential endpoints must be throttled both
// to slow guessing and to keep a login flood from exhausting memory.
var (
	loginIPLimiter     = ratelimit.PerMinute(10, 20) // per client IP
	loginEmailLimiter  = ratelimit.PerMinute(5, 8)   // per IP|email pair
	signupLimiter      = ratelimit.PerMinute(5, 10)
	forgotLimiter      = ratelimit.PerMinute(2, 5)
	emailChangeLimiter = ratelimit.PerMinute(2, 5)
)

func (h *Handlers) Signup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, errorResp{Error: "method not allowed"})
		return
	}
	if !signupLimiter.Allow(ratelimit.ClientIP(r)) {
		ratelimit.TooMany(w)
		return
	}
	var req signupReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResp{Error: "invalid JSON body"})
		return
	}

	identity, err := h.svc.Signup(r.Context(), SignupInput{
		Email: req.Email, Password: req.Password,
		FirstName: req.FirstName, LastName: req.LastName, TenantName: req.TenantName,
	}, ClientIP(r), r.UserAgent())
	if err != nil {
		writeAuthError(w, err)
		return
	}

	setSessionCookie(w, r, identity.Session.ID, identity.Session.ExpiresAt)
	writeJSON(w, http.StatusCreated, newIdentityResp(identity.User, identity.Tenant, identity.Membership))
}

func (h *Handlers) Login(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, errorResp{Error: "method not allowed"})
		return
	}
	var req loginReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResp{Error: "invalid JSON body"})
		return
	}
	ip := ratelimit.ClientIP(r)
	if !loginIPLimiter.Allow(ip) ||
		!loginEmailLimiter.Allow(ip+"|"+strings.ToLower(strings.TrimSpace(req.Email))) {
		ratelimit.TooMany(w)
		return
	}

	var trustedToken string
	if c, cerr := r.Cookie(TrustedDeviceCookieName); cerr == nil {
		trustedToken = c.Value
	}
	res, err := h.svc.Login(r.Context(), LoginInput{Email: req.Email, Password: req.Password},
		ClientIP(r), r.UserAgent(), trustedToken)
	if err != nil {
		writeAuthError(w, err)
		return
	}

	// TOTP-enabled user: no session yet — the SPA collects a code and POSTs to
	// /api/auth/login/2fa with this pending token to finish.
	if res.TwoFARequired {
		writeJSON(w, http.StatusOK, map[string]any{
			"twofa_required": true,
			"pending_token":  res.PendingToken,
			"method":         res.Method,
		})
		return
	}

	identity := res.Identity
	setSessionCookie(w, r, identity.Session.ID, identity.Session.ExpiresAt)
	writeJSON(w, http.StatusOK, h.identityWithTenants(r.Context(), identity.User, identity.Tenant, identity.Membership))
}

// ForgotPassword is the public, logged-out reset entry point. Body: {"email"}.
// To avoid email enumeration it ALWAYS responds 200 {ok:true}; the reset email
// is only sent when the address maps to an active account.
func (h *Handlers) ForgotPassword(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, errorResp{Error: "method not allowed"})
		return
	}
	if !forgotLimiter.Allow(ratelimit.ClientIP(r)) {
		ratelimit.TooMany(w)
		return
	}
	var req struct {
		Email string `json:"email"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResp{Error: "invalid JSON body"})
		return
	}
	reset, err := h.svc.CreatePasswordResetForEmail(r.Context(), req.Email)
	if err == nil && reset != nil && h.mailer != nil {
		if mErr := h.mailer.SendPasswordReset(r.Context(), mailer.PasswordResetEmail{
			To:        reset.Email,
			Link:      buildPasswordResetLink(r, reset.Token),
			ExpiresAt: reset.ExpiresAt,
		}); mErr != nil {
			logging.Error(packageName, "ForgotPassword: send mail failed: %v", mErr)
		}
	}
	// Uniform response regardless of whether the email exists.
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// UpdateSessionIPs handles POST /api/me/session/ips. Body: {"ipv4","ipv6"} from
// the post-login browser probe. Records both on the current session and
// re-resolves geo (IPv4 preferred). Best-effort — never fails the SPA.
func (h *Handlers) UpdateSessionIPs(w http.ResponseWriter, r *http.Request) {
	identity, ok := IdentityFromContext(r.Context())
	if !ok || identity.Session == nil {
		writeJSON(w, http.StatusUnauthorized, errorResp{Error: "unauthorized"})
		return
	}
	var req struct {
		IPv4 string `json:"ipv4"`
		IPv6 string `json:"ipv6"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResp{Error: "invalid JSON body"})
		return
	}
	if err := h.svc.UpdateSessionIPs(r.Context(), identity.Session.ID, req.IPv4, req.IPv6); err != nil {
		logging.Error(packageName, "UpdateSessionIPs failed: %v", err)
		writeJSON(w, http.StatusInternalServerError, errorResp{Error: "internal error"})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ListSessions handles GET /api/sessions — active sessions for the Active
// Sessions panel (tenant-wide for owner/admin, own-only otherwise).
func (h *Handlers) ListSessions(w http.ResponseWriter, r *http.Request) {
	identity, ok := IdentityFromContext(r.Context())
	if !ok || identity.Tenant == nil {
		writeJSON(w, http.StatusUnauthorized, errorResp{Error: "unauthorized"})
		return
	}
	views, err := h.svc.ListSessions(r.Context(), identity)
	if err != nil {
		logging.Error(packageName, "ListSessions failed: %v", err)
		writeJSON(w, http.StatusInternalServerError, errorResp{Error: "internal error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"sessions": views})
}

// RevokeSessions handles POST /api/sessions/revoke. Body: {"refs": ["...", ...]}.
// Signs out the listed sessions (own-only for members; any in-tenant for admins).
func (h *Handlers) RevokeSessions(w http.ResponseWriter, r *http.Request) {
	identity, ok := IdentityFromContext(r.Context())
	if !ok || identity.Tenant == nil {
		writeJSON(w, http.StatusUnauthorized, errorResp{Error: "unauthorized"})
		return
	}
	var req struct {
		Refs []string `json:"refs"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResp{Error: "invalid JSON body"})
		return
	}
	n, err := h.svc.RevokeSessions(r.Context(), identity, req.Refs)
	if err != nil {
		logging.Error(packageName, "RevokeSessions failed: %v", err)
		writeJSON(w, http.StatusInternalServerError, errorResp{Error: "internal error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"revoked": n})
}

func (h *Handlers) Logout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, errorResp{Error: "method not allowed"})
		return
	}
	cookie, _ := r.Cookie(CookieName)
	if cookie != nil && cookie.Value != "" {
		if err := h.svc.Logout(r.Context(), cookie.Value); err != nil {
			logging.Error(packageName, "Logout revoke failed: %v", err)
		}
	}
	clearSessionCookie(w, r)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (h *Handlers) Me(w http.ResponseWriter, r *http.Request) {
	identity, ok := IdentityFromContext(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, errorResp{Error: "unauthorized"})
		return
	}
	writeJSON(w, http.StatusOK, h.identityWithTenants(r.Context(), identity.User, identity.Tenant, identity.Membership))
}

// SwitchTenant re-binds the caller's session to another account they belong to.
// POST /api/auth/switch-tenant {tenant_id}. Returns the refreshed identity.
func (h *Handlers) SwitchTenant(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, errorResp{Error: "method not allowed"})
		return
	}
	identity, ok := IdentityFromContext(r.Context())
	if !ok || identity.User == nil || identity.Session == nil {
		writeJSON(w, http.StatusUnauthorized, errorResp{Error: "unauthorized"})
		return
	}
	var req struct {
		TenantID int64 `json:"tenant_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.TenantID == 0 {
		writeJSON(w, http.StatusBadRequest, errorResp{Error: "invalid request"})
		return
	}
	next, err := h.svc.SwitchTenant(r.Context(), identity.Session.ID, identity.User.ID, req.TenantID)
	if err != nil {
		writeAuthError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, h.identityWithTenants(r.Context(), next.User, next.Tenant, next.Membership))
}

// UpdateMe handles PATCH /api/me — the Account settings tab. Currently
// updates the caller's first/last name; returns the refreshed identity so the
// SPA can update the nav greeting + avatar without a separate refetch.
func (h *Handlers) UpdateMe(w http.ResponseWriter, r *http.Request) {
	identity, ok := IdentityFromContext(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, errorResp{Error: "unauthorized"})
		return
	}
	var req struct {
		FirstName string `json:"first_name"`
		LastName  string `json:"last_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResp{Error: "invalid JSON body"})
		return
	}
	user, err := h.svc.UpdateProfile(r.Context(), identity.User.ID, UpdateProfileInput{
		FirstName: req.FirstName, LastName: req.LastName,
	})
	if err != nil {
		if errors.Is(err, ErrInvalidName) {
			writeJSON(w, http.StatusBadRequest, errorResp{Error: err.Error()})
			return
		}
		logging.Error(packageName, "UpdateMe failed: %v", err)
		writeJSON(w, http.StatusInternalServerError, errorResp{Error: "internal error"})
		return
	}
	writeJSON(w, http.StatusOK, newIdentityResp(user, identity.Tenant, identity.Membership))
}

// RequestEmailChange handles POST /api/me/email-change. Body:
//
//	{"new_email": "new@x.com", "password": "current-password"}
//
// Verifies the password, records a pending change with a short-lived token, and
// emails a confirmation link to the NEW address. The email isn't changed until
// that link is confirmed.
func (h *Handlers) RequestEmailChange(w http.ResponseWriter, r *http.Request) {
	identity, ok := IdentityFromContext(r.Context())
	if !ok || identity.User == nil {
		writeJSON(w, http.StatusUnauthorized, errorResp{Error: "unauthorized"})
		return
	}
	// Throttled: verifies the password (argon2id) and sends an email.
	if !emailChangeLimiter.Allow(ratelimit.ClientIP(r)) {
		ratelimit.TooMany(w)
		return
	}
	var req struct {
		NewEmail string `json:"new_email"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResp{Error: "invalid JSON body"})
		return
	}

	ec, err := h.svc.RequestEmailChange(r.Context(), identity.User.ID, req.NewEmail, req.Password)
	if err != nil {
		switch {
		case errors.Is(err, ErrEmailInvalid):
			writeJSON(w, http.StatusBadRequest, errorResp{Error: "Enter a valid email address."})
		case errors.Is(err, ErrSameEmail):
			writeJSON(w, http.StatusBadRequest, errorResp{Error: err.Error()})
		case errors.Is(err, ErrInvalidCredentials):
			writeJSON(w, http.StatusForbidden, errorResp{Error: "That password is incorrect."})
		case errors.Is(err, ErrEmailTaken):
			writeJSON(w, http.StatusConflict, errorResp{Error: "That email is already in use."})
		default:
			logging.Error(packageName, "RequestEmailChange failed: %v", err)
			writeJSON(w, http.StatusInternalServerError, errorResp{Error: "internal error"})
		}
		return
	}

	// Send the confirmation link to the new address. If the mail send fails the
	// request row still exists, but we surface it so the user can retry rather
	// than wait for an email that never arrives.
	if h.mailer != nil {
		if err := h.mailer.SendEmailChange(r.Context(), mailer.EmailChangeEmail{
			To:           ec.NewEmail,
			Name:         derefName(identity.User.FirstName),
			CurrentEmail: identity.User.Email,
			Link:         buildEmailChangeLink(r, ec.Token),
			ExpiresAt:    ec.ExpiresAt,
		}); err != nil {
			logging.Error(packageName, "RequestEmailChange: send mail failed: %v", err)
			writeJSON(w, http.StatusBadGateway, errorResp{
				Error: "We couldn't send the confirmation email. Please try again in a moment.",
			})
			return
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"new_email":  ec.NewEmail,
		"expires_at": ec.ExpiresAt,
	})
}

// LookupEmailChange handles GET /api/email-change/{token} — public. Returns the
// target address so the confirm page can show what's being confirmed.
func (h *Handlers) LookupEmailChange(w http.ResponseWriter, r *http.Request) {
	ec, err := h.svc.LookupEmailChange(r.Context(), r.PathValue("token"))
	if err != nil {
		writeEmailChangeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"new_email": ec.NewEmail})
}

// ConfirmEmailChange handles POST /api/email-change/{token}/confirm — public,
// the token is the credential. Applies the new email permanently.
func (h *Handlers) ConfirmEmailChange(w http.ResponseWriter, r *http.Request) {
	newEmail, err := h.svc.ConfirmEmailChange(r.Context(), r.PathValue("token"))
	if err != nil {
		writeEmailChangeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "email": newEmail})
}

// writeEmailChangeErr maps the email-change sentinels to HTTP statuses.
func writeEmailChangeErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrEmailChangeExpired):
		writeJSON(w, http.StatusGone, errorResp{Error: err.Error()})
	case errors.Is(err, ErrEmailTaken):
		writeJSON(w, http.StatusConflict, errorResp{Error: "That email was taken before you confirmed. Please request the change again."})
	default:
		writeJSON(w, http.StatusNotFound, errorResp{Error: ErrEmailChangeToken.Error()})
	}
}

// UploadAvatar handles POST /api/me/avatar (multipart, field "avatar"). The
// client crops + downscales to a square PNG before sending; we stream the bytes
// into tenant-scoped blob storage, point the user's avatar at the new blob, and
// delete the previous one. Returns the refreshed identity so the SPA can show
// the new image app-wide without a refetch. Avatars are a workspace asset and
// are NOT counted against the storage quota.
func (h *Handlers) UploadAvatar(w http.ResponseWriter, r *http.Request) {
	identity, ok := IdentityFromContext(r.Context())
	if !ok || identity.Tenant == nil {
		writeJSON(w, http.StatusUnauthorized, errorResp{Error: "unauthorized"})
		return
	}
	if h.store == nil {
		writeJSON(w, http.StatusInternalServerError, errorResp{Error: "avatar storage unavailable"})
		return
	}
	// Cap the whole request body before parsing so an oversized upload is
	// rejected without buffering it all.
	r.Body = http.MaxBytesReader(w, r.Body, AvatarMaxBytes+1<<16)
	if err := r.ParseMultipartForm(AvatarMaxBytes + 1<<16); err != nil {
		writeJSON(w, http.StatusRequestEntityTooLarge, errorResp{Error: "image too large (max 4 MB)"})
		return
	}
	file, hdr, err := r.FormFile("avatar")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorResp{Error: "missing avatar file"})
		return
	}
	defer file.Close()
	if hdr.Size > AvatarMaxBytes {
		writeJSON(w, http.StatusRequestEntityTooLarge, errorResp{Error: "image too large (max 4 MB)"})
		return
	}
	if ct := hdr.Header.Get("Content-Type"); ct != "" && !strings.HasPrefix(ct, "image/") {
		writeJSON(w, http.StatusBadRequest, errorResp{Error: "file must be an image"})
		return
	}

	put, err := h.store.Put(r.Context(), identity.Tenant.ID, io.LimitReader(file, AvatarMaxBytes))
	if err != nil {
		logging.Error(packageName, "UploadAvatar store put failed: %v", err)
		writeJSON(w, http.StatusInternalServerError, errorResp{Error: "could not store image"})
		return
	}

	oldKey, user, err := h.svc.SetAvatarKey(r.Context(), identity.User.ID, put.BlobPath)
	if err != nil {
		// Roll back the just-written blob so we don't leak it.
		_ = h.store.Delete(r.Context(), put.BlobPath)
		logging.Error(packageName, "UploadAvatar set key failed: %v", err)
		writeJSON(w, http.StatusInternalServerError, errorResp{Error: "internal error"})
		return
	}
	if oldKey != "" && oldKey != put.BlobPath {
		if err := h.store.Delete(r.Context(), oldKey); err != nil {
			logging.Error(packageName, "UploadAvatar: delete old blob %s failed: %v", oldKey, err)
		}
	}
	writeJSON(w, http.StatusOK, newIdentityResp(user, identity.Tenant, identity.Membership))
}

// RemoveAvatar handles DELETE /api/me/avatar — drops the user's avatar and the
// backing blob, falling back to initials everywhere.
func (h *Handlers) RemoveAvatar(w http.ResponseWriter, r *http.Request) {
	identity, ok := IdentityFromContext(r.Context())
	if !ok || identity.Tenant == nil {
		writeJSON(w, http.StatusUnauthorized, errorResp{Error: "unauthorized"})
		return
	}
	oldKey, user, err := h.svc.ClearAvatar(r.Context(), identity.User.ID)
	if err != nil {
		logging.Error(packageName, "RemoveAvatar failed: %v", err)
		writeJSON(w, http.StatusInternalServerError, errorResp{Error: "internal error"})
		return
	}
	if oldKey != "" && h.store != nil {
		if err := h.store.Delete(r.Context(), oldKey); err != nil {
			logging.Error(packageName, "RemoveAvatar: delete blob %s failed: %v", oldKey, err)
		}
	}
	writeJSON(w, http.StatusOK, newIdentityResp(user, identity.Tenant, identity.Membership))
}

// GetUserAvatar handles GET /api/users/{id}/avatar — streams a tenant member's
// avatar image. Gated to the caller's tenant by UserAvatarKey. Loaded as an
// <img> src (same-origin, cookie-authenticated); the ?v= query lets the browser
// cache it hard until the next upload changes the stamp.
func (h *Handlers) GetUserAvatar(w http.ResponseWriter, r *http.Request) {
	identity, ok := IdentityFromContext(r.Context())
	if !ok || identity.Tenant == nil {
		writeJSON(w, http.StatusUnauthorized, errorResp{Error: "unauthorized"})
		return
	}
	if h.store == nil {
		http.NotFound(w, r)
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorResp{Error: "invalid id"})
		return
	}
	key, err := h.svc.UserAvatarKey(r.Context(), identity.Tenant.ID, id)
	if err != nil {
		// Not a co-member of the active tenant — but a shared live session
		// (collaboration) is its own trust boundary: honor it across tenants.
		if h.avatarTrust != nil && h.avatarTrust(r.Context(), identity.User.ID, id) {
			key, err = h.svc.UserAvatarKeyUnscoped(r.Context(), id)
		}
		if err != nil {
			http.NotFound(w, r)
			return
		}
	}
	rc, err := h.store.Get(r.Context(), key)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer rc.Close()
	w.Header().Set("Content-Type", "image/png")
	// Immutable per ?v= stamp — safe to cache for a long time; a new upload
	// changes the URL.
	w.Header().Set("Cache-Control", "private, max-age=86400")
	if _, err := io.Copy(w, rc); err != nil {
		logging.Error(packageName, "GetUserAvatar copy failed: %v", err)
	}
}

// UpdateWorkspace handles PATCH /api/workspace (owner/admin). Body:
//
//	{"name": "Acme Inc.", "slug": "acme"}
//
// Updates the workspace name + URL slug and returns the refreshed identity so
// the SPA can update the tenant name shown in the nav/header.
func (h *Handlers) UpdateWorkspace(w http.ResponseWriter, r *http.Request) {
	identity, ok := IdentityFromContext(r.Context())
	if !ok || identity.Tenant == nil {
		writeJSON(w, http.StatusUnauthorized, errorResp{Error: "unauthorized"})
		return
	}
	var req struct {
		Name string `json:"name"`
		Slug string `json:"slug"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResp{Error: "invalid JSON body"})
		return
	}
	t, err := h.svc.UpdateWorkspace(r.Context(), identity.Tenant.ID, req.Name, req.Slug)
	if err != nil {
		switch {
		case errors.Is(err, ErrWorkspaceNameRequired), errors.Is(err, ErrSlugInvalid):
			writeJSON(w, http.StatusBadRequest, errorResp{Error: err.Error()})
		case errors.Is(err, ErrSlugReserved), errors.Is(err, ErrSlugTaken):
			writeJSON(w, http.StatusConflict, errorResp{Error: err.Error()})
		default:
			logging.Error(packageName, "UpdateWorkspace failed: %v", err)
			writeJSON(w, http.StatusInternalServerError, errorResp{Error: "internal error"})
		}
		return
	}
	writeJSON(w, http.StatusOK, newIdentityResp(identity.User, t, identity.Membership))
}

// CheckWorkspaceSlug handles GET /api/workspace/slug-available?slug=... — the
// availability check behind the Settings "Check" button. Returns the normalized
// slug + whether it's free.
func (h *Handlers) CheckWorkspaceSlug(w http.ResponseWriter, r *http.Request) {
	identity, ok := IdentityFromContext(r.Context())
	if !ok || identity.Tenant == nil {
		writeJSON(w, http.StatusUnauthorized, errorResp{Error: "unauthorized"})
		return
	}
	res, err := h.svc.CheckSlugAvailable(r.Context(), identity.Tenant.ID, r.URL.Query().Get("slug"))
	if err != nil {
		logging.Error(packageName, "CheckWorkspaceSlug failed: %v", err)
		writeJSON(w, http.StatusInternalServerError, errorResp{Error: "internal error"})
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// MyFolders returns the caller's own top-level folders (their grant roots),
// so the SPA can home a scoped member into their folder and bound navigation
// to it. Owner/admin get full=true (unrestricted).
func (h *Handlers) MyFolders(w http.ResponseWriter, r *http.Request) {
	identity, ok := IdentityFromContext(r.Context())
	if !ok || identity.Tenant == nil {
		writeJSON(w, http.StatusUnauthorized, errorResp{Error: "unauthorized"})
		return
	}
	full, roots, err := h.svc.CurrentUserGrantRoots(r.Context(), identity)
	if err != nil {
		logging.Error(packageName, "MyFolders failed: %v", err)
		writeJSON(w, http.StatusInternalServerError, errorResp{Error: "internal error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"full": full, "folders": roots})
}

// Members returns every user (active, invited, suspended) in the caller's
// current tenant. Used by the Users settings page.
func (h *Handlers) Members(w http.ResponseWriter, r *http.Request) {
	identity, ok := IdentityFromContext(r.Context())
	if !ok || identity.Tenant == nil {
		writeJSON(w, http.StatusUnauthorized, errorResp{Error: "unauthorized"})
		return
	}
	members, err := h.svc.ListTenantMembers(r.Context(), identity.Tenant.ID)
	if err != nil {
		logging.Error(packageName, "Members list failed: %v", err)
		writeJSON(w, http.StatusInternalServerError, errorResp{Error: "internal error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"members": members})
}

// RemoveMember handles DELETE /api/members/{id}. The path id is the
// target user's id (matching the ID field on the row returned by
// ListTenantMembers). Owner rows and self-deletions are refused at the
// service layer.
func (h *Handlers) RemoveMember(w http.ResponseWriter, r *http.Request) {
	identity, ok := IdentityFromContext(r.Context())
	if !ok || identity.Tenant == nil {
		writeJSON(w, http.StatusUnauthorized, errorResp{Error: "unauthorized"})
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorResp{Error: "invalid id"})
		return
	}
	if err := h.svc.RemoveTenantMember(r.Context(), identity.Tenant.ID, identity.User.ID, id); err != nil {
		switch {
		case errors.Is(err, ErrMemberNotFound):
			writeJSON(w, http.StatusNotFound, errorResp{Error: err.Error()})
		case errors.Is(err, ErrCannotRemoveOwner):
			writeJSON(w, http.StatusForbidden, errorResp{Error: err.Error()})
		case errors.Is(err, ErrCannotRemoveSelf):
			writeJSON(w, http.StatusConflict, errorResp{Error: err.Error()})
		default:
			logging.Error(packageName, "RemoveMember failed: %v", err)
			writeJSON(w, http.StatusInternalServerError, errorResp{Error: "internal error"})
		}
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// SetMemberStatus handles PATCH /api/members/{id}/status. Body shape:
//
//	{"status": "active"} | {"status": "suspended"}
//
// Disabling a member also revokes their sessions so any open SPA they
// have gets booted on its next request.
func (h *Handlers) SetMemberStatus(w http.ResponseWriter, r *http.Request) {
	identity, ok := IdentityFromContext(r.Context())
	if !ok || identity.Tenant == nil {
		writeJSON(w, http.StatusUnauthorized, errorResp{Error: "unauthorized"})
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorResp{Error: "invalid id"})
		return
	}
	var req struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResp{Error: "invalid JSON body"})
		return
	}
	if err := h.svc.SetTenantMemberStatus(r.Context(), identity.Tenant.ID, identity.User.ID, id, req.Status); err != nil {
		switch {
		case errors.Is(err, ErrMemberNotFound):
			writeJSON(w, http.StatusNotFound, errorResp{Error: err.Error()})
		case errors.Is(err, ErrCannotDisableOwner):
			writeJSON(w, http.StatusForbidden, errorResp{Error: err.Error()})
		case errors.Is(err, ErrCannotChangeSelfStatus):
			writeJSON(w, http.StatusConflict, errorResp{Error: err.Error()})
		case errors.Is(err, ErrInvalidStatus):
			writeJSON(w, http.StatusBadRequest, errorResp{Error: err.Error()})
		default:
			logging.Error(packageName, "SetMemberStatus failed: %v", err)
			writeJSON(w, http.StatusInternalServerError, errorResp{Error: "internal error"})
		}
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// GetMemberFolders handles GET /api/members/{id}/folders. Returns the
// target member's current folder grants so the bulk Folder Access modal can
// pre-check what they already have. Owner/admin (full access) return an
// empty list.
func (h *Handlers) GetMemberFolders(w http.ResponseWriter, r *http.Request) {
	identity, ok := IdentityFromContext(r.Context())
	if !ok || identity.Tenant == nil {
		writeJSON(w, http.StatusUnauthorized, errorResp{Error: "unauthorized"})
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorResp{Error: "invalid id"})
		return
	}
	grants, err := h.svc.TenantMemberFolderGrants(r.Context(), identity.Tenant.ID, id)
	if err != nil {
		if errors.Is(err, ErrMemberNotFound) {
			writeJSON(w, http.StatusNotFound, errorResp{Error: err.Error()})
			return
		}
		logging.Error(packageName, "GetMemberFolders failed: %v", err)
		writeJSON(w, http.StatusInternalServerError, errorResp{Error: "internal error"})
		return
	}
	ids := make([]int64, 0, len(grants))
	for _, g := range grants {
		ids = append(ids, g.FolderID)
	}
	// folders carries each grant's workspace so the picker can show a workspace
	// as partially selected without expanding it.
	writeJSON(w, http.StatusOK, map[string]any{"folder_ids": ids, "folders": grants})
}

// SetMemberFolders handles PATCH /api/members/{id}/folders. Body:
//
//	{"folder_ids": [123, 456]}
//
// Replaces the entire set of folder grants for the target member.
// Server validates each folder belongs to the caller's tenant, dedupes
// parent+child entries to the topmost folder per branch, then applies
// the new set in one transaction.
func (h *Handlers) SetMemberFolders(w http.ResponseWriter, r *http.Request) {
	identity, ok := IdentityFromContext(r.Context())
	if !ok || identity.Tenant == nil {
		writeJSON(w, http.StatusUnauthorized, errorResp{Error: "unauthorized"})
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorResp{Error: "invalid id"})
		return
	}
	var req struct {
		FolderIDs []int64 `json:"folder_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResp{Error: "invalid JSON body"})
		return
	}
	if err := h.svc.SetTenantMemberFolders(r.Context(), identity.Tenant.ID, identity.User.ID, id, req.FolderIDs); err != nil {
		switch {
		case errors.Is(err, ErrMemberNotFound):
			writeJSON(w, http.StatusNotFound, errorResp{Error: err.Error()})
		case errors.Is(err, ErrCannotDisableOwner):
			writeJSON(w, http.StatusForbidden, errorResp{Error: "Owner has full workspace access — can't be limited to folders"})
		case errors.Is(err, ErrInvalidFolder):
			writeJSON(w, http.StatusBadRequest, errorResp{Error: err.Error()})
		default:
			logging.Error(packageName, "SetMemberFolders failed: %v", err)
			writeJSON(w, http.StatusInternalServerError, errorResp{Error: "internal error"})
		}
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

func writeAuthError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrEmailInvalid), errors.Is(err, ErrPasswordTooShort):
		writeJSON(w, http.StatusBadRequest, errorResp{Error: err.Error()})
	case errors.Is(err, ErrEmailTaken):
		writeJSON(w, http.StatusConflict, errorResp{Error: err.Error()})
	case errors.Is(err, ErrInvalidCredentials):
		writeJSON(w, http.StatusUnauthorized, errorResp{Error: "invalid credentials"})
	case errors.Is(err, ErrUserSuspended), errors.Is(err, ErrTenantSuspended), errors.Is(err, ErrAccountDisabled):
		writeJSON(w, http.StatusForbidden, errorResp{Error: err.Error()})
	case errors.Is(err, ErrNoActiveTenant), errors.Is(err, ErrUnauthorized):
		writeJSON(w, http.StatusUnauthorized, errorResp{Error: "unauthorized"})
	default:
		logging.Error(packageName, "unhandled auth error: %v", err)
		writeJSON(w, http.StatusInternalServerError, errorResp{Error: "internal error"})
	}
}

func setSessionCookie(w http.ResponseWriter, r *http.Request, token string, expiresAt time.Time) {
	session.SetCookie(w, r, token, expiresAt)
}

func clearSessionCookie(w http.ResponseWriter, r *http.Request) {
	session.ClearCookie(w, r)
}

// SetTrustedDeviceCookie writes the long-lived "remember this device" cookie.
// Mirrors the session cookie's security attributes (HttpOnly, Secure when the
// request is TLS, SameSite=Lax). Exported so the twofactor login-completion
// handler can set it once a code verifies with "remember" checked.
func SetTrustedDeviceCookie(w http.ResponseWriter, r *http.Request, token string, expiresAt time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name:     TrustedDeviceCookieName,
		Value:    token,
		Path:     "/",
		Expires:  expiresAt,
		MaxAge:   int(time.Until(expiresAt).Seconds()),
		HttpOnly: true,
		Secure:   true, // TLS terminates upstream; r.TLS is always nil in prod
		SameSite: http.SameSiteLaxMode,
	})
}

// ClearTrustedDeviceCookie removes the trusted-device cookie (used when 2FA is
// disabled, so this device no longer skips the challenge).
func ClearTrustedDeviceCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     TrustedDeviceCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   true, // TLS terminates upstream; r.TLS is always nil in prod
		SameSite: http.SameSiteLaxMode,
	})
}

// derefName returns the string a *string points at, or "" when nil.
func derefName(s *string) string {
	if s == nil {
		return ""
	}
	return strings.TrimSpace(*s)
}

// buildEmailChangeLink derives the fully-qualified confirm URL from the
// incoming request, honoring the Cloudflare proxy's X-Forwarded-* headers so
// the link points at the public hostname (app.trilli.com) rather than the
// upstream 127.0.0.1:8081. Mirrors invites' buildInviteLink.
func buildEmailChangeLink(r *http.Request, token string) string {
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
	return scheme + "://" + host + "/confirm-email/" + token
}

// buildPasswordResetLink derives the absolute /reset-password/<token> URL,
// honoring the Cloudflare proxy headers like buildEmailChangeLink.
func buildPasswordResetLink(r *http.Request, token string) string {
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

// ClientIP extracts the real client IP. We sit behind Cloudflare → nginx, so:
//  1. CF-Connecting-IP — Cloudflare's authoritative client IP. Set by Cloudflare
//     itself and NOT spoofable by the visitor (unlike X-Forwarded-For, whose
//     leading entries a client can prepend).
//  2. X-Forwarded-For last hop — fallback for non-Cloudflare deployments. We
//     take the LAST entry (the one our own trusted proxy appended) rather than
//     the first, since earlier entries can be client-supplied/spoofed.
//  3. RemoteAddr — bare host, last resort.
//
// Exported so other packages (audit) attribute events to the same IP.
func ClientIP(r *http.Request) string {
	if cf := strings.TrimSpace(r.Header.Get("CF-Connecting-IP")); cf != "" {
		return cf
	}
	if xff := strings.TrimSpace(r.Header.Get("X-Forwarded-For")); xff != "" {
		parts := strings.Split(xff, ",")
		last := strings.TrimSpace(parts[len(parts)-1])
		if last != "" {
			return last
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
