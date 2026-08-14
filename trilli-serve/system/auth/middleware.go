package auth

import (
	"context"
	"net/http"
	"strings"
)

// AdminOrOwnerRoles is the set of role values that grant access to
// workspace-management surfaces (Users page, Plan & usage page, and
// the corresponding APIs). Anything below this — "member", "viewer" —
// is denied with a 403 by RequireAdminOrOwner.
func IsAdminOrOwner(role string) bool {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "owner", "admin":
		return true
	}
	return false
}

// ctxKey is unexported so callers can only reach identity via the helpers below.
type ctxKey struct{ name string }

var identityCtxKey = &ctxKey{name: "auth.identity"}

// RequireAuth wraps next so it only runs for requests carrying a valid tenant session.
// On success the resolved Identity is available via IdentityFromContext.
func (h *Handlers) RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(CookieName)
		if err != nil || cookie.Value == "" {
			writeJSON(w, http.StatusUnauthorized, errorResp{Error: "unauthorized"})
			return
		}
		identity, err := h.svc.LoadIdentity(r.Context(), cookie.Value)
		if err != nil {
			writeAuthError(w, err)
			return
		}
		// Read-only gate for an account in its purge window. Two states share
		// it: 'lapsed' (subscription ended) and 'closing' (owner asked to delete
		// it). In both, the tenant has 30 days of read-only access before its
		// data is purged: full read (view + download/export) so they can
		// retrieve everything, DELETE to free space, and billing / auth /
		// account self-service open so they can reactivate or sign out — every
		// other mutation (upload, create, rename, move, invite, share) refused.
		// Server is authoritative here; the UI mirrors it for clarity only.
		if identity.Tenant != nil &&
			(identity.Tenant.LifecycleState == "lapsed" || identity.Tenant.LifecycleState == "closing") &&
			!lapsedAllows(r) {
			writeJSON(w, http.StatusForbidden, errorResp{Error: "account_lapsed"})
			return
		}
		ctx := context.WithValue(r.Context(), identityCtxKey, identity)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// lapsedAllows reports whether a request is permitted while the tenant is in
// the lapsed (read-only) state. Reads are always fine; deletes are allowed so
// the user can free space; and the billing / auth / account-self-service APIs
// stay open so they can pay to reactivate or sign out. Everything else that
// mutates is blocked.
func lapsedAllows(r *http.Request) bool {
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodDelete:
		return true
	}
	p := r.URL.Path
	for _, prefix := range []string{"/api/billing/", "/api/auth/", "/api/me/", "/api/me", "/api/account/"} {
		if p == prefix || strings.HasPrefix(p, prefix) {
			return true
		}
	}
	return false
}

// RequireAdminOrOwner is a stricter wrapper: after RequireAuth has
// resolved the identity, it also enforces that the caller's tenant
// membership role is "owner" or "admin". Members and viewers get a
// 403. Use this for the Users-management and Plan-management APIs.
func (h *Handlers) RequireAdminOrOwner(next http.Handler) http.Handler {
	return h.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		identity, ok := IdentityFromContext(r.Context())
		if !ok || identity.Membership == nil || !IsAdminOrOwner(identity.Membership.Role) {
			writeJSON(w, http.StatusForbidden, errorResp{Error: "forbidden"})
			return
		}
		next.ServeHTTP(w, r)
	}))
}

// IdentityFromContext returns the authenticated identity attached by RequireAuth.
func IdentityFromContext(ctx context.Context) (*Identity, bool) {
	v, ok := ctx.Value(identityCtxKey).(*Identity)
	return v, ok
}

// TenantIDFromContext is a convenience that returns the tenant id of the
// current request, or 0 if no authenticated identity is attached.
func TenantIDFromContext(ctx context.Context) int64 {
	identity, ok := IdentityFromContext(ctx)
	if !ok || identity.Tenant == nil {
		return 0
	}
	return identity.Tenant.ID
}

// UserIDFromContext is a convenience that returns the user id of the current
// request, or 0 if no authenticated identity is attached.
func UserIDFromContext(ctx context.Context) int64 {
	identity, ok := IdentityFromContext(ctx)
	if !ok {
		return 0
	}
	return identity.User.ID
}
